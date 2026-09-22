package desktop

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// 以下为内存输入和截图，不调用真实桌面。
func TestSequenceMixedActionsWithoutAXOrIntermediateImages(t *testing.T) {
	b := sequenceFixture()
	b.tree.Truncated = true // 图像/键盘操作不能依赖 AX 是否完整。
	s := New(true, b)
	defer s.Close()
	before, err := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9})
	if err != nil {
		t.Fatal(err)
	}
	text := "Hello 世界🙂"
	steps := []SequenceStep{
		{Action: "click", Point: &Point{100, 60}, Space: "image", ClickCount: 2},
		{Action: "key", Key: "a", Modifiers: []string{"command"}},
		{Action: "type", Text: &text},
		{Action: "scroll", Point: &Point{100, 60}, Space: "image", DeltaY: -200},
		{Action: "move", Point: &Point{110, 70}, Space: "image"},
		{Action: "drag", Path: []Point{{110, 70}, {200, 100}}, Space: "image", DurationMS: 16},
		{Action: "wait", DurationMS: 1},
	}
	result, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: before["snapshot_id"].(string), Steps: steps})
	if err != nil || result["outcome"] != "completed" || result["completed_steps"] != 7 || result["dispatched_steps"] != 6 {
		t.Fatal(result, err)
	}
	if b.treeReads != 0 || len(b.captured) != 2 || len(b.events) != 0 {
		t.Fatal("unnecessary AX/image/global input", b.treeReads, len(b.captured), b.events)
	}
	kinds := []string{}
	for _, in := range b.inputs {
		kinds = append(kinds, in.Action)
	}
	if !reflect.DeepEqual(kinds, []string{"click", "key", "type", "scroll", "move", "down", "drag", "up"}) {
		t.Fatal(kinds)
	}
	want := Point{200 + 100*500.0/720, 150 + 60*300.0/450}
	if b.inputs[0].Point != want || b.inputs[3].Point != want || b.inputs[0].Count != 2 {
		t.Fatal("image mapping lost between actions", b.inputs)
	}
	if s.latest.options.Accessibility || s.latest.options.Screenshot != nil {
		t.Fatal("intermediate settings replaced original options")
	}
	for _, target := range b.targets {
		if target.ID != 9 || target.PID != 20 {
			t.Fatal("input escaped window")
		}
	}
	t.Logf("mixed_actions=7 dispatched=6 AX_reads=%d captures=%d (initial + final)", b.treeReads, len(b.captured))
}

func TestSequenceAllowsLongerBatchesAndGenericAXRoles(t *testing.T) {
	b := sequenceFixture()
	b.tree.Elements = []Element{{Role: "AXCheckBox", Title: "Option", EnabledKnown: true, Enabled: true, Pressable: true, Path: []int{0}}}
	s := New(true, b)
	defer s.Close()
	before := observeWindow(t, s)
	steps := make([]SequenceStep, 40)
	for i := range steps {
		selector := ElementSelector{Role: "AXCheckBox"}
		if i%2 == 0 {
			title := "Option"
			selector = ElementSelector{Title: &title}
		}
		steps[i] = SequenceStep{Action: "click", Element: selector}
	}
	result, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: before, Steps: steps, TimeoutMS: 120000})
	if err != nil || result["outcome"] != "completed" || len(b.inputs) != 40 {
		t.Fatal(result, err)
	}
	normalized, err := normalizeSequence(SequenceRequest{SnapshotID: before, Steps: steps})
	if err != nil || normalized.TimeoutMS != 60000 {
		t.Fatal(normalized.TimeoutMS, err)
	}
}

func TestSequenceValidatesMixedTailBeforeAnyInput(t *testing.T) {
	for _, tail := range []SequenceStep{
		{Action: "click", Point: &Point{math.NaN(), 200}},
		{Action: "drag", Path: []Point{{250, 200}, {9999, 200}}},
		{Action: "scroll", DeltaY: 100},
		{Action: "key", Key: "not-a-key"},
		{Action: "wait", DurationMS: 10, Key: "enter"},
		{Action: "wait", DurationMS: 10, Point: &Point{250, 200}},
		{Action: "type"},
		{Action: "drag", Path: []Point{{250, 200}, {300, 200}}, DurationMS: 3000},
	} {
		b := sequenceFixture()
		s := New(true, b)
		id := observeWindow(t, s)
		_, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "click", Point: &Point{250, 200}}, tail}})
		if err == nil || len(b.inputs) != 0 {
			t.Fatal("invalid tail dispatched input", tail, err)
		}
		s.Close()
	}
}

func TestSequenceSecureInputStopsTypingButDoesNotBlockMouse(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	text := "not sent"
	b.hook = func(context.Context, WindowInput) error { b.permissions.SecureInput = true; return nil }
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "click", Point: &Point{250, 200}}, {Action: "type", Text: &text}}})
	if err != nil || seqCode(r) != "SECURE_INPUT" || len(b.inputs) != 1 || r["failed_step"] != 2 {
		t.Fatal(r, err)
	}
}

func TestSequenceWaitPrefixAndReadOnlySequenceRespectSingleStep(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	localPoll(t, s, "")
	observeWindow(t, s)
	localCommand(t, s, "pause")
	localCommand(t, s, "step")
	id := observeWindow(t, s)
	r, err := s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "wait", DurationMS: 1}}})
	if err != nil || r["outcome"] != "completed" || s.control.status().Phase != "running" || len(b.inputs) != 0 {
		t.Fatal(r, err)
	}
	id = r["observation"].(core.Result)["snapshot_id"].(string)
	r, err = s.Sequence(t.Context(), SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "wait", DurationMS: 1}, {Action: "click", Point: &Point{250, 200}}, {Action: "key", Key: "enter"}}})
	if err != nil || r["outcome"] != "single_step" || r["completed_steps"] != 2 || len(b.inputs) != 1 || s.control.status().Phase != "paused" || s.latest != nil {
		t.Fatal(r, err)
	}
}

func TestSequenceStopDuringExplicitWaitCancelsWithoutInput(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	localPoll(t, s, "")
	done := make(chan core.Result, 1)
	go func() {
		r, _ := s.Sequence(context.Background(), SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "wait", DurationMS: 30000}, {Action: "key", Key: "enter"}}})
		done <- r
	}()
	deadline := time.Now().Add(time.Second)
	for s.control.status().SequenceStep != 1 {
		if time.Now().After(deadline) {
			t.Fatal("wait not started")
		}
		time.Sleep(time.Millisecond)
	}
	localCommand(t, s, "stop")
	select {
	case r := <-done:
		if r["outcome"] != "interrupted" || len(b.inputs) != 0 || s.control.status().Active != 0 {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel wait")
	}
}

func TestSequenceDragCleanupUsesOriginalTarget(t *testing.T) {
	b := sequenceFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.hook = func(_ context.Context, in WindowInput) error {
		if in.Action == "down" {
			cancel()
		}
		if in.Action == "up" {
			return errors.New("simulated release failure")
		}
		return nil
	}
	r, err := s.Sequence(ctx, SequenceRequest{SnapshotID: id, Steps: []SequenceStep{{Action: "drag", Path: []Point{{250, 200}, {300, 200}}}, {Action: "key", Key: "enter"}}})
	if err != nil || r["outcome"] != "interrupted" || !s.control.status().CleanupFailed || len(b.inputs) != 2 || b.inputs[1].Action != "up" || len(b.events) != 0 {
		t.Fatal(r, err, b.inputs)
	}
	for _, w := range b.targets {
		if w.ID != 9 || w.PID != 20 {
			t.Fatal("cleanup escaped original target")
		}
	}
}

func TestNormalizeSequenceCopiesAllMutableParameters(t *testing.T) {
	p := &Point{250, 200}
	modifiers := []string{"command"}
	path := []Point{{250, 200}, {300, 200}}
	text := "before"
	r, err := normalizeSequence(SequenceRequest{SnapshotID: "00000000000000000000000000000000", Steps: []SequenceStep{
		{Action: "click", Point: p, Modifiers: modifiers}, {Action: "drag", Path: path}, {Action: "type", Text: &text},
	}})
	if err != nil {
		t.Fatal(err)
	}
	p.X = 999
	modifiers[0] = "shift"
	path[0].X = 999
	text = "after"
	if r.Steps[0].Point.X != 250 || r.Steps[0].Modifiers[0] != "command" || r.Steps[1].Path[0].X != 250 || *r.Steps[2].Text != "before" {
		t.Fatal("plan changed after normalization")
	}
}
