package desktop

import (
	"context"
	"encoding/json"
	"github.com/uvwt/agentdock/internal/tool/core"
	"strings"
	"testing"
	"time"
)

func beginDesktopTask(t *testing.T, s *Service) string {
	t.Helper()
	r, e := s.Task(t.Context(), TaskRequest{Action: "begin", Title: "isolated task"})
	if e != nil {
		t.Fatal(e)
	}
	return r["task_id"].(string)
}
func authorizePending(t *testing.T, s *Service, op string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		v := localPoll(t, s, "")
		if v.PendingApplication != nil {
			_, err := s.LocalControl(ControlRequest{ControllerID: testController, SessionID: v.ID, Operation: op, ApprovalID: v.PendingApplication.ID}, false)
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("application approval not requested")
}
func scopedSnapshot(t *testing.T, s *Service, token string) core.Result {
	t.Helper()
	type response struct {
		r core.Result
		e error
	}
	done := make(chan response, 1)
	go func() {
		r, e := s.Snapshot(context.Background(), SnapshotRequest{TaskID: token, WindowID: 9, Accessibility: true})
		done <- response{r, e}
	}()
	authorizePending(t, s, "approve_application")
	select {
	case v := <-done:
		if v.e != nil {
			t.Fatal(v.e)
		}
		return v.r
	case <-time.After(time.Second):
		t.Fatal("approved snapshot did not finish")
	}
	return nil
}
func TestTaskCapabilityOwnershipAndLocalApplicationGrant(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	s.RequireTaskScope()
	localPoll(t, s, "")
	token := beginDesktopTask(t, s)
	_, e := s.Task(t.Context(), TaskRequest{Action: "begin", Title: "another task"})
	requireCode(t, e, "DESKTOP_TASK_BUSY")
	_, e = s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9})
	requireCode(t, e, "DESKTOP_TASK_OWNED")
	_, e = s.Snapshot(t.Context(), SnapshotRequest{TaskID: strings.Repeat("0", 64), WindowID: 9})
	requireCode(t, e, "DESKTOP_TASK_OWNED")
	observed := scopedSnapshot(t, s, token)
	status, _ := s.Status(t.Context())
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), token) {
		t.Fatal("task capability leaked into public state")
	}
	_, e = s.Act(t.Context(), ActionRequest{TaskID: token, Action: "click", ElementID: "e1", SnapshotID: observed["snapshot_id"].(string)})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.inputs) != 1 {
		t.Fatal("approved task input not dispatched")
	}
	localCommand(t, s, "stop")
	_, e = s.Task(t.Context(), TaskRequest{Action: "end", TaskID: token})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Task(t.Context(), TaskRequest{Action: "begin", Title: "do not undo stop"})
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
}
func TestTaskDeniedApplicationAndForgedApproval(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	localPoll(t, s, "")
	token := beginDesktopTask(t, s)
	done := make(chan error, 1)
	go func() {
		_, e := s.Snapshot(context.Background(), SnapshotRequest{TaskID: token, WindowID: 9})
		done <- e
	}()
	authorizePending(t, s, "deny_application")
	requireCode(t, <-done, "DESKTOP_APPLICATION_DENIED")
	_, e := s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, WindowID: 9})
	requireCode(t, e, "DESKTOP_APPLICATION_DENIED")
	_, e = s.LocalControl(ControlRequest{ControllerID: testController, SessionID: s.control.status().ID, Operation: "approve_application", ApprovalID: "old"}, false)
	requireCode(t, e, "STALE_SNAPSHOT")
	_, e = s.Task(t.Context(), TaskRequest{Action: "end", TaskID: strings.Repeat("f", 64)})
	requireCode(t, e, "DESKTOP_TASK_OWNED")
}
func TestSingleStepPermitsObservationAndOnlyOneMutation(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	localPoll(t, s, "")
	observeWindow(t, s)
	localCommand(t, s, "pause")
	localCommand(t, s, "step")
	id := observeWindow(t, s)
	if _, e := s.Wait(t.Context(), WaitRequest{PID: 20, WindowID: 9, Condition: "window_exists", TimeoutMS: msPointer(0)}); e != nil {
		t.Fatal(e)
	}
	_, e := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	if e != nil {
		t.Fatal(e)
	}
	v := s.control.status()
	if v.Phase != "paused" || v.Reason != "single_step_completed" {
		t.Fatal(v)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
	if len(b.inputs) != 1 {
		t.Fatal("single step allowed more than one input")
	}
}
func TestHeartbeatStopIntentAndCleanupAcknowledgment(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	observeWindow(t, s)
	localPoll(t, s, "")
	id := s.control.status().ID
	v, e := s.LocalControl(ControlRequest{ControllerID: testController, StopSessionID: id}, true)
	if e != nil || v.Phase != "stopped" {
		t.Fatalf("%+v %v", v, e)
	}
	epoch := v.Epoch
	v, e = s.LocalControl(ControlRequest{ControllerID: testController, StopSessionID: id}, true)
	if e != nil || v.Epoch != epoch {
		t.Fatal("stop intent is not idempotent")
	}
	s.control.mu.Lock()
	s.control.view.CleanupFailed = true
	s.control.blockLocked("cleanup_failed", "test")
	s.control.mu.Unlock()
	_, e = s.LocalControl(ControlRequest{ControllerID: testController, SessionID: id, Operation: "resume"}, false)
	requireCode(t, e, "DESKTOP_CLEANUP_REQUIRED")
	v = localCommand(t, s, "acknowledge_cleanup")
	if v.Phase != "stopped" || !v.CanResume {
		t.Fatal(v)
	}
}
