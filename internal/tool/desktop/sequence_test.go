package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// 全部使用内存后端；不启动应用、不请求系统权限、不向真实桌面发送事件。
type sequenceBackend struct {
	*postObservationBackend
	treeHook func()
}

func sequenceFixture() *sequenceBackend {
	b := &sequenceBackend{postObservationBackend: &postObservationBackend{fakeWindowBackend: windowFixture()}}
	b.tree.Elements = nil
	for i, label := range []string{"1", "2", "+", "3", "="} {
		b.tree.Elements = append(b.tree.Elements, Element{Role: "AXButton", Title: label, EnabledKnown: true, Enabled: true, Pressable: true, Path: []int{i}})
	}
	return b
}
func (b *sequenceBackend) WindowTree(ctx context.Context, w Window, nodes, depth int) (Tree, error) {
	if b.treeHook != nil {
		b.treeHook()
	}
	return b.postObservationBackend.WindowTree(ctx, w, nodes, depth)
}
func seqSelector(role, title string) ElementSelector {
	return ElementSelector{Role: role, Title: &title}
}
func seqSteps() []SequenceStep {
	steps := []SequenceStep{}
	for _, label := range []string{"1", "2", "+", "3", "="} {
		steps = append(steps, SequenceStep{Action: "click", Element: seqSelector("AXButton", label)})
	}
	return steps
}
func seqCode(r core.Result) string {
	e, _ := r["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestSequenceRunsFiveInputsWithFreshSelectorsAndOneFinalImage(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	labels := []string{}
	b.hook = func(_ context.Context, in WindowInput) error {
		labels = append(labels, in.Element.Title)
		v := s.control.status()
		if v.SequenceStep != len(labels) || v.SequenceTotal != 5 {
			t.Fatal("missing progress", v)
		}
		// 每一步重排树和原生路径，固定元素ID的盲执行应在此暴露。
		first := b.tree.Elements[0]
		b.tree.Elements = append(append([]Element{}, b.tree.Elements[1:]...), first)
		for i := range b.tree.Elements {
			b.tree.Elements[i].Path = []int{i}
		}
		return nil
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	if err != nil {
		t.Fatal(err)
	}
	if r["outcome"] != "completed" || r["completed_steps"] != 5 || r["dispatched_steps"] != 5 || r["application_verified"] != false || r["foreground_fallback"] != false {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(labels, []string{"1", "2", "+", "3", "="}) || len(b.events) != 0 {
		t.Fatal(labels, b.events)
	}
	if len(b.captured) != 2 || b.treeReads != 8 {
		t.Fatalf("expected initial + final image and per-step AX, got captures=%d tree=%d", len(b.captured), b.treeReads)
	}
	obs := r["observation"].(core.Result)
	if obs["snapshot_id"] == id || r["_mcp_image_base64"] == nil || obs["_mcp_image_base64"] != nil {
		t.Fatal(r)
	}
	if s.control.status().SequenceTotal != 0 {
		t.Fatal("progress retained after completion")
	}
	_, err = s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	requireCode(t, err, "STALE_SNAPSHOT")
	b.hook = nil
	_, err = s.Act(t.Context(), ActionRequest{SnapshotID: obs["snapshot_id"].(string), Action: "click", ElementID: obs["elements"].([]Element)[0].ID})
	if err != nil {
		t.Fatal("final snapshot is not usable", err)
	}
}

func TestSequenceInheritsNoImageAndNeverStoresPlanText(t *testing.T) {
	b := sequenceFixture()
	b.tree.Elements = append(b.tree.Elements, Element{Role: "AXTextField", Title: "Field", EnabledKnown: true, Enabled: true, ValueSettable: true, Path: []int{5}})
	s := New(true, b)
	defer s.Close()
	no := false
	r, err := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9, Accessibility: true, Screenshot: &no})
	if err != nil {
		t.Fatal(err)
	}
	secret := "mock-private-text-not-in-history"
	r, err = s.Sequence(t.Context(), SequenceRequest{SnapshotID: r["snapshot_id"].(string), Steps: []SequenceStep{{Action: "set_value", Element: seqSelector("AXTextField", "Field"), Text: &secret}}})
	if err != nil || r["outcome"] != "completed" {
		t.Fatal(r, err)
	}
	if len(b.captured) != 0 || r["_mcp_image_base64"] != nil || b.inputs[0].Value != secret {
		t.Fatal(r)
	}
	encoded, _ := json.Marshal(s.control.status())
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "Field") {
		t.Fatal("input leaked into control history")
	}
}

func TestSequencePrevalidatesAllStepsBeforeInput(t *testing.T) {
	for _, fault := range []string{"empty", "too_many", "bad_tail", "role_only", "click_text", "null_text", "long_text", "bad_condition", "large_timeout", "nul_label"} {
		t.Run(fault, func(t *testing.T) {
			b := sequenceFixture()
			s := New(true, b)
			defer s.Close()
			r := SequenceRequest{SnapshotID: observeWindow(t, s), Steps: seqSteps()}
			switch fault {
			case "empty":
				r.Steps = nil
			case "too_many":
				r.Steps = make([]SequenceStep, 17)
			case "bad_tail":
				r.Steps[4].Action = "key"
			case "role_only":
				r.Steps[4].Element.Title = nil
			case "click_text":
				v := ""
				r.Steps[4].Text = &v
			case "null_text":
				r.Steps[4].Action = "set_value"
				r.Steps[4].Element.Role = "AXTextField"
			case "long_text":
				v := strings.Repeat("界", 4097)
				r.Steps[4] = SequenceStep{Action: "set_value", Element: seqSelector("AXTextField", "Field"), Text: &v}
			case "bad_condition":
				r.Steps[4].After = &SequenceCondition{Condition: "window_exists", Element: seqSelector("AXButton", "1")}
			case "large_timeout":
				r.TimeoutMS = 30001
			case "nul_label":
				r.Steps[4].Element = seqSelector("AXButton", "x\x00")
			}
			_, err := s.Sequence(t.Context(), r)
			if err == nil || len(b.inputs) != 0 {
				t.Fatal("malformed tail partially executed", err)
			}
		})
	}
}

func TestSequenceStopsOnChangedContextOrSelector(t *testing.T) {
	for _, fault := range []string{"missing", "ambiguous", "disabled", "unknown_enabled", "truncated", "foreground", "window_owner", "window_move", "new_window", "title_change", "modal", "secure_input", "permission", "window_list"} {
		t.Run(fault, func(t *testing.T) {
			b := sequenceFixture()
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			b.hook = func(context.Context, WindowInput) error {
				b.state.Windows = append([]Window{}, b.state.Windows...)
				switch fault {
				case "missing":
					b.tree.Elements = append([]Element{}, b.tree.Elements[2:]...)
				case "ambiguous":
					b.tree.Elements = append(b.tree.Elements, b.tree.Elements[1])
				case "disabled":
					b.tree.Elements[1].Enabled = false
				case "unknown_enabled":
					b.tree.Elements[1].EnabledKnown = false
				case "truncated":
					b.tree.Truncated = true
				case "foreground":
					b.state.FrontmostPID = 20
				case "window_owner":
					b.state.Windows[1].PID = 99
				case "window_move":
					b.state.Windows[1].Bounds.X++
				case "new_window":
					b.state.Windows = append(b.state.Windows, Window{ID: 100, PID: 20, Bounds: Rect{Width: 100, Height: 100}})
				case "title_change":
					b.state.Windows[1].Title = "Different page"
				case "modal":
					b.tree.Elements = append(b.tree.Elements, Element{Role: "AXSheet"})
				case "secure_input":
					b.permissions.SecureInput = true
				case "permission":
					b.permissions.Accessibility = false
				case "window_list":
					b.state.WindowsTruncated = true
				}
				return nil
			}
			r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
			if err != nil || r["outcome"] != "interrupted" || len(b.inputs) != 1 || s.latest != nil || len(b.events) != 0 {
				t.Fatal(r, err, b.inputs)
			}
			if r["error"].(map[string]any)["retry_input"] != false {
				t.Fatal("missing no-replay boundary")
			}
		})
	}
}

func TestSequenceFailurePreservesPartialProgress(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.hook = func(context.Context, WindowInput) error {
		if len(b.inputs) == 3 {
			return errors.New("mock partial dispatch")
		}
		return nil
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	if err != nil || r["completed_steps"] != 2 || r["dispatched_steps"] != 2 || r["failed_step"] != 3 || r["failure_stage"] != "input" || r["error"].(map[string]any)["may_have_dispatched"] != true {
		t.Fatal(r, err)
	}
	if len(b.inputs) != 3 || s.latest != nil {
		t.Fatal("failed sequence was replayed")
	}
}

func TestSequenceLocalStopPauseAndSingleStep(t *testing.T) {
	for _, mode := range []string{"stop", "pause", "step"} {
		t.Run(mode, func(t *testing.T) {
			b := sequenceFixture()
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			localPoll(t, s, "")
			if mode == "step" {
				localCommand(t, s, "pause")
				localCommand(t, s, "step")
				id = observeWindow(t, s)
			} else {
				b.hook = func(context.Context, WindowInput) error { localCommand(t, s, mode); return nil }
			}
			r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
			if err != nil || len(b.inputs) != 1 || s.latest != nil || s.control.status().SequenceTotal != 0 {
				t.Fatal(r, err)
			}
			if mode == "step" {
				obs := r["observation"].(core.Result)
				if r["outcome"] != "single_step" || obs["snapshot_id"] != "" || obs["observation_only"] != true || s.control.status().Phase != "paused" {
					t.Fatal(r)
				}
			} else if r["outcome"] != "interrupted" {
				t.Fatal(r)
			}
			_, err = s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
			requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
		})
	}
}

func TestSequencePostconditionWaitsWithoutReplayingInput(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	steps := seqSteps()
	steps[0].After = &SequenceCondition{Condition: "element_enabled", Element: seqSelector("AXButton", "2")}
	reads := 0
	b.hook = func(context.Context, WindowInput) error {
		if len(b.inputs) == 1 {
			b.tree.Elements[1].Enabled = false
		}
		return nil
	}
	b.treeHook = func() {
		if len(b.inputs) == 1 {
			reads++
			if reads == 3 {
				b.tree.Elements[1].Enabled = true
			}
		}
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: steps})
	if err != nil || r["outcome"] != "completed" || len(b.inputs) != 5 || reads != 3 {
		t.Fatal(r, err, reads)
	}
	if !r["steps"].([]SequenceStepResult)[0].PostconditionVerified {
		t.Fatal("predicate not recorded")
	}
}

func TestSequenceTimeoutAndPostconditionFailure(t *testing.T) {
	for _, fault := range []string{"condition", "total", "late_first_sample", "late_poll", "unknown", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			b := sequenceFixture()
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			steps := seqSteps()
			timeout := 10
			condition := "element_exists"
			selector := seqSelector("AXStaticText", "Done")
			if fault == "unknown" {
				condition = "element_enabled"
				selector = seqSelector("AXButton", "2")
			}
			if fault == "total" || fault == "cancel" {
				timeout = 5000
			}
			if fault == "late_poll" {
				timeout = 120
			}
			steps[0].After = &SequenceCondition{Condition: condition, Element: selector, TimeoutMS: &timeout}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			b.hook = func(context.Context, WindowInput) error {
				if fault == "unknown" {
					b.tree.Elements[1].EnabledKnown = false
				}
				if fault == "cancel" {
					cancel()
				}
				return nil
			}
			reads := 0
			b.treeHook = func() {
				if len(b.inputs) == 1 {
					reads++
					if fault == "late_first_sample" || fault == "late_poll" && reads == 2 {
						time.Sleep(40 * time.Millisecond)
						b.tree.Elements = append(b.tree.Elements, Element{Role: "AXStaticText", Title: "Done"})
					}
				}
			}
			totalBudget := 1000
			if fault == "total" {
				totalBudget = 100
			}
			r, err := s.Sequence(ctx, SequenceRequest{SnapshotID: id, Steps: steps, TimeoutMS: totalBudget})
			if err != nil || r["outcome"] != "interrupted" || len(b.inputs) != 1 || r["completed_steps"] != 0 || s.latest != nil {
				t.Fatal(r, err)
			}
			want := "SEQUENCE_CONDITION_TIMEOUT"
			if fault == "unknown" {
				want = "SEQUENCE_ELEMENT_STATE_UNKNOWN"
			}
			if fault == "total" {
				want = "DEADLINE_EXCEEDED"
			}
			if fault == "cancel" {
				want = "CANCELLED"
			}
			if seqCode(r) != want {
				t.Fatal("wrong interruption cause", r)
			}
		})
	}
}

func TestSequenceRejectsOtherTaskAndUnsupportedSnapshot(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	s.RequireTaskScope()
	_, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	requireCode(t, err, "DESKTOP_TASK_REQUIRED")
	if len(b.inputs) != 0 {
		t.Fatal("task ownership bypassed")
	}
	for _, mode := range []string{"foreground", "no_ax", "expired"} {
		t.Run(mode, func(t *testing.T) {
			b := sequenceFixture()
			s := New(true, b)
			defer s.Close()
			r := SnapshotRequest{WindowID: 9, Accessibility: mode != "no_ax"}
			if mode == "foreground" {
				r = SnapshotRequest{Mode: "foreground", Accessibility: true}
			}
			obs, err := s.Snapshot(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expired" {
				s.now = func() time.Time { return time.Now().Add(time.Minute) }
			}
			_, err = s.Sequence(t.Context(), SequenceRequest{SnapshotID: obs["snapshot_id"].(string), Steps: seqSteps()})
			if err == nil || len(b.inputs) != 0 {
				t.Fatal("invalid observation authorized sequence")
			}
		})
	}
}

func TestSequenceFinalCaptureFailureDoesNotReplayCompletedPlan(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.captureError = errors.New("mock image failure")
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	if err != nil || r["completed_steps"] != 5 || r["dispatched_steps"] != 5 || r["failure_stage"] != "final_observation" || r["failed_step"] != 0 || s.latest != nil || len(b.inputs) != 5 {
		t.Fatal(r, err)
	}
}
func TestSequenceTaskGrantIsRequiredForEveryObservation(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	s.RequireTaskScope()
	localPoll(t, s, "")
	token := beginDesktopTask(t, s)
	observed := scopedSnapshot(t, s, token)
	_, err := s.Sequence(t.Context(), SequenceRequest{TaskID: strings.Repeat("0", 64), SnapshotID: observed["snapshot_id"].(string), Steps: seqSteps()})
	requireCode(t, err, "DESKTOP_TASK_OWNED")
	b.hook = func(context.Context, WindowInput) error {
		s.control.mu.Lock()
		for key := range s.control.grants {
			s.control.grants[key] = false
			s.control.denials[key] = true
		}
		s.control.mu.Unlock()
		return nil
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{TaskID: token, SnapshotID: observed["snapshot_id"].(string), Steps: seqSteps()})
	if err != nil || seqCode(r) != "DESKTOP_APPLICATION_DENIED" || len(b.inputs) != 1 {
		t.Fatal(r, err)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), token) {
		t.Fatal("task capability leaked")
	}
}

func TestSequenceMonitorLossStopsTheRemainingPlan(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	var offset atomic.Int64
	base := time.Now()
	s.control.now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	s.RequireMonitor()
	localPoll(t, s, s.control.status().ID)
	b.hook = func(context.Context, WindowInput) error {
		offset.Store(int64(4 * time.Second))
		return nil
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	if err != nil || r["outcome"] != "interrupted" || len(b.inputs) != 1 || s.control.status().Phase != "paused" || s.latest != nil {
		t.Fatal(r, err)
	}
}

func TestSequencePreservesCleanupFailureLatch(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.hook = func(context.Context, WindowInput) error {
		return &InputCleanupError{Cause: errors.New("mock cleanup failure")}
	}
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: seqSteps()})
	v := s.control.status()
	if err != nil || r["outcome"] != "interrupted" || !v.CleanupFailed || v.Phase != "cleanup_failed" || len(b.inputs) != 1 || v.Events[len(v.Events)-1].Outcome != "cleanup_failed" {
		t.Fatal(r, err, v)
	}
}

func TestSequenceCancellationWhileWaitingDoesNotLeakAnotherInput(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	localPoll(t, s, "")
	steps := seqSteps()
	steps[0].After = &SequenceCondition{Condition: "element_exists", Element: seqSelector("AXStaticText", "Awaiting")}
	observing := make(chan struct{}, 1)
	continueRead := make(chan struct{})
	b.treeHook = func() {
		if len(b.inputs) == 1 {
			observing <- struct{}{}
			<-continueRead
		}
	}
	done := make(chan core.Result, 1)
	go func() {
		result, _ := s.Sequence(context.Background(), SequenceRequest{SnapshotID: id, Steps: steps})
		done <- result
	}()
	select {
	case <-observing:
	case <-time.After(time.Second):
		close(continueRead)
		t.Fatal("observation did not start")
	}
	localCommand(t, s, "stop")
	close(continueRead)
	select {
	case r := <-done:
		if r["outcome"] != "interrupted" || len(b.inputs) != 1 || s.control.status().Active != 0 {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the sequence")
	}
}
