package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// 本文件所有输入、窗口和截图均为内存模拟，不控制用户桌面。
type postObservationBackend struct {
	*fakeWindowBackend
	captureError error
	captureHook  func()
	treeReads    int
	limits       [3]int
}

func (b *postObservationBackend) CaptureWindow(ctx context.Context, w Window, dimension int) (Capture, error) {
	b.limits[0] = dimension
	if b.captureHook != nil {
		b.captureHook()
	}
	if b.captureError != nil {
		return Capture{}, b.captureError
	}
	return b.fakeWindowBackend.CaptureWindow(ctx, w, dimension)
}

func (b *postObservationBackend) WindowTree(ctx context.Context, w Window, nodes, depth int) (Tree, error) {
	b.treeReads++
	b.limits[1], b.limits[2] = nodes, depth
	return b.fakeWindowBackend.WindowTree(ctx, w, nodes, depth)
}

func TestObserveAfterReturnsFreshSnapshotAndSingleInput(t *testing.T) {
	b := &postObservationBackend{fakeWindowBackend: windowFixture()}
	s := New(true, b)
	defer s.Close()
	before, err := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9, Accessibility: true, MaxDimension: 256, MaxNodes: 15, MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	b.hook = func(context.Context, WindowInput) error {
		b.tree.Elements[0].Title = "Observed after input"
		return nil
	}
	r, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: before["snapshot_id"].(string), ObserveAfter: true})
	if err != nil {
		t.Fatal(err)
	}
	if r["event_dispatched"] != true || r["application_verified"] != false || r["foreground_fallback"] != false || r["observation_status"] != "captured" {
		t.Fatal(r)
	}
	obs := r["observation"].(core.Result)
	if obs["snapshot_id"] == before["snapshot_id"] || obs["target_window"].(Window).PID != 20 || obs["elements"].([]Element)[0].Title != "Observed after input" {
		t.Fatal(obs)
	}
	if len(b.inputs) != 1 || len(b.events) != 0 || b.treeReads != 2 || b.limits != [3]int{256, 15, 3} {
		t.Fatalf("unexpected input or observation: %+v", b)
	}
	if r["_mcp_image_base64"] == nil || obs["_mcp_image_base64"] != nil || s.latest.options.TaskID != "" {
		t.Fatal("invalid image transport or cached task token")
	}
	_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: before["snapshot_id"].(string)})
	requireCode(t, err, "STALE_SNAPSHOT")
	_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: obs["snapshot_id"].(string)})
	if err != nil || len(b.inputs) != 2 {
		t.Fatalf("new snapshot unusable: %v", err)
	}
}

func TestObserveAfterIsOptInAndPreservesNoImageSetting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "metadata_only"}[enabled], func(t *testing.T) {
			b := &postObservationBackend{fakeWindowBackend: windowFixture()}
			s := New(true, b)
			defer s.Close()
			image := false
			before, err := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9, Accessibility: true, Screenshot: &image})
			if err != nil {
				t.Fatal(err)
			}
			image = true // 已观察的设置不得受调用方后续指针写入影响。
			r, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: before["snapshot_id"].(string), ObserveAfter: enabled})
			if err != nil {
				t.Fatal(err)
			}
			if len(b.captured) != 0 || r["_mcp_image_base64"] != nil || (r["observation"] != nil) != enabled {
				t.Fatal(r)
			}
			want := 1
			if enabled {
				want = 2
			}
			if b.treeReads != want {
				t.Fatal("unexpected observation count", b.treeReads)
			}
		})
	}
}

func TestObserveAfterFailureNeverReplaysSuccessfulInput(t *testing.T) {
	for _, fault := range []string{"capture", "permission", "window_owner", "stop_before", "stop_during"} {
		t.Run(fault, func(t *testing.T) {
			b := &postObservationBackend{fakeWindowBackend: windowFixture()}
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			localPoll(t, s, "")
			b.hook = func(context.Context, WindowInput) error {
				switch fault {
				case "capture":
					b.captureError = errors.New("simulated capture failure")
				case "permission":
					b.permissions.ScreenRecording = false
				case "window_owner":
					b.state.Windows = append([]Window{}, b.state.Windows...)
					b.state.Windows[1].PID = 99
				case "stop_before":
					localCommand(t, s, "stop")
				case "stop_during":
					b.captureHook = func() { localCommand(t, s, "stop") }
				}
				return nil
			}
			r, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id, ObserveAfter: true})
			if err != nil {
				t.Fatalf("successful input was mislabeled as failed: %v", err)
			}
			if r["event_dispatched"] != true || r["application_verified"] != false || r["observation_status"] != "unavailable" || r["observation"] != nil {
				t.Fatal(r)
			}
			detail := r["observation_error"].(map[string]any)
			if detail["retry_input"] != false || detail["input_already_dispatched"] != true || len(b.inputs) != 1 || s.latest != nil || len(b.events) != 0 {
				t.Fatal(r)
			}
			if fault == "stop_before" && b.treeReads != 1 {
				t.Fatal("observation started after local stop")
			}
		})
	}
}

func TestObserveAfterDoesNotObserveOrReplayFailedInput(t *testing.T) {
	b := &postObservationBackend{fakeWindowBackend: windowFixture()}
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.hook = func(context.Context, WindowInput) error { return errors.New("simulated partial dispatch") }
	r, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id, ObserveAfter: true})
	requireCode(t, err, "DESKTOP_ACTION_FAILED")
	if r != nil || b.treeReads != 1 || len(b.inputs) != 1 || s.latest != nil {
		t.Fatal("failed input retried or hidden", r)
	}
}

func TestObserveAfterSingleStepReturnsEvidenceWithoutActionToken(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	localPoll(t, s, "")
	observeWindow(t, s)
	localCommand(t, s, "pause")
	localCommand(t, s, "step")
	id := observeWindow(t, s)
	r, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id, ObserveAfter: true})
	if err != nil {
		t.Fatal(err)
	}
	obs := r["observation"].(core.Result)
	if obs["snapshot_id"] != "" || obs["expires_in_ms"] != 0 || obs["observation_only"] != true || s.latest != nil || s.control.status().Phase != "paused" {
		t.Fatal(r, s.control.status())
	}
	_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id, ObserveAfter: true})
	requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
	if len(b.inputs) != 1 {
		t.Fatal("single-step permitted multiple mutations")
	}
}

func TestObserveAfterRejectsForegroundBeforeInput(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	defer s.Close()
	_, err := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: observe(t, s), ObserveAfter: true})
	if err == nil || len(b.events) != 0 {
		t.Fatal("foreground action was dispatched", err)
	}
}
