package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testController = "test-controller-000001"

func localPoll(t *testing.T, s *Service, visible string) ControlState {
	t.Helper()
	v, e := s.LocalControl(ControlRequest{ControllerID: testController, VisibleSessionID: visible}, true)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func localCommand(t *testing.T, s *Service, op string) ControlState {
	t.Helper()
	id := s.control.status().ID
	v, e := s.LocalControl(ControlRequest{ControllerID: testController, SessionID: id, Operation: op}, false)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestLocalPauseResumeInvalidatesObservedEpoch(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	localPoll(t, s, "")
	v := localCommand(t, s, "pause")
	if v.Phase != "paused" || !v.CanResume {
		t.Fatal(v)
	}
	_, e := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
	no := false
	_, e = s.Snapshot(t.Context(), SnapshotRequest{Screenshot: &no})
	if e != nil {
		t.Fatal(e)
	}
	if s.control.status().Phase != "paused" {
		t.Fatal("metadata discovery resumed paused control")
	}
	localCommand(t, s, "resume")
	_, e = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	requireCode(t, e, "STALE_SNAPSHOT")
	_, e = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: observeWindow(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.inputs) != 1 {
		t.Fatal(b.inputs)
	}
}
func TestLocalStopCancelsDragBeforeAcknowledgedStopped(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	localPoll(t, s, "")
	pressed := make(chan struct{})
	releaseStarted := make(chan struct{})
	allowRelease := make(chan struct{})
	done := make(chan error, 1)
	b.hook = func(ctx context.Context, in WindowInput) error {
		if in.Action == "down" {
			close(pressed)
		}
		if in.Action == "up" {
			close(releaseStarted)
			<-allowRelease
		}
		return nil
	}
	go func() {
		_, e := s.Act(context.Background(), ActionRequest{Action: "drag", SnapshotID: id, Path: []Point{{250, 200}, {300, 220}}, DurationMS: 2000})
		done <- e
	}()
	select {
	case <-pressed:
	case <-time.After(time.Second):
		t.Fatal("drag did not start")
	}
	v := localCommand(t, s, "stop")
	if v.Phase != "stopping" || v.Active != 1 {
		t.Fatal("stop acknowledged before release", v)
	}
	select {
	case <-releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not trigger release")
	}
	_, e := s.LocalControl(ControlRequest{ControllerID: testController, SessionID: v.ID, Operation: "resume"}, false)
	requireCode(t, e, "DESKTOP_CONTROL_DRAINING")
	close(allowRelease)
	if e := <-done; e == nil {
		t.Fatal("cancelled drag succeeded")
	}
	v = s.control.status()
	if v.Phase != "stopped" || v.Active != 0 {
		t.Fatal(v)
	}
	if len(b.inputs) != 2 || b.inputs[1].Action != "up" || b.targets[1].PID != 20 {
		t.Fatal("release escaped bound target", b.inputs, b.targets)
	}
	_, e = s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9})
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
	if len(b.inputs) != 2 {
		t.Fatal("input after stopped acknowledgment")
	}
}
func TestMonitorVisibilityRequiredBeforeOperation(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	s.control.mu.Lock()
	s.control.view.Required = true
	s.control.mu.Unlock()
	_, e := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9})
	requireCode(t, e, "DESKTOP_MONITOR_UNAVAILABLE")
	localPoll(t, s, "")
	done := make(chan error, 1)
	go func() { _, e := s.Snapshot(context.Background(), SnapshotRequest{WindowID: 9}); done <- e }()
	var v ControlState
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		v = s.control.status()
		if v.ID != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if v.ID == "" {
		t.Fatal("pending session not visible to local UI")
	}
	select {
	case e := <-done:
		t.Fatal("operation executed before local acknowledgment", e)
	default:
	}
	localPoll(t, s, v.ID)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("acknowledged operation did not finish")
	}
}
func TestMonitorDisconnectPausesAndDoesNotAutoResume(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	now := time.Now()
	s.control.now = func() time.Time { return now }
	localPoll(t, s, "")
	ctx, finish, e := s.controlled(t.Context(), "drag")
	if e != nil {
		t.Fatal(e)
	}
	s.control.mu.Lock()
	s.control.view.Required = true
	now = now.Add(monitorLease + time.Millisecond)
	s.control.mu.Unlock()
	v := s.control.status()
	if v.Phase != "pausing" || v.Reason != "monitor_disconnected" {
		t.Fatal(v)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("lease expiry did not cancel input")
	}
	finish()
	localPoll(t, s, v.ID)
	_, _, e = s.controlled(t.Context(), "type")
	requireCode(t, e, "DESKTOP_CONTROL_BLOCKED")
	if s.control.status().Phase != "paused" {
		t.Fatal("reconnect silently resumed")
	}
}
func TestLocalControllerStaleAndForeignCommands(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	observeWindow(t, s)
	localPoll(t, s, "")
	_, e := s.LocalControl(ControlRequest{ControllerID: "another-controller-0002"}, true)
	requireCode(t, e, "DESKTOP_MONITOR_BUSY")
	_, e = s.LocalControl(ControlRequest{ControllerID: testController, SessionID: "old", Operation: "stop"}, false)
	requireCode(t, e, "STALE_SNAPSHOT")
	_, e = s.LocalControl(ControlRequest{ControllerID: testController, Operation: "resume"}, true)
	requireCode(t, e, "INVALID_ARGUMENT")
	localCommand(t, s, "stop")
	localCommand(t, s, "resume")
	observeWindow(t, s)
	if s.control.status().Phase != "running" {
		t.Fatal("local restart did not permit new session")
	}
}
func TestLocalMetadataDoesNotExposeTypedContentOrAXPaths(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	_, e := s.Act(t.Context(), ActionRequest{Action: "set_value", ElementID: "e2", Text: "secret-monitor-test", SnapshotID: id})
	if e != nil {
		t.Fatal(e)
	}
	data, e := json.Marshal(localPoll(t, s, ""))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "secret-monitor-test") || strings.Contains(string(data), `"path"`) {
		t.Fatal("input value or AX path leaked into preview metadata")
	}
}
func TestLocalIdleCompletionDoesNotClearStopLatch(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	now := time.Now()
	s.control.now = func() time.Time { return now }
	observeWindow(t, s)
	now = now.Add(monitorIdle + time.Second)
	if s.control.status().Phase != "completed" {
		t.Fatal("idle session did not complete")
	}
	observeWindow(t, s)
	localPoll(t, s, "")
	localCommand(t, s, "stop")
	now = now.Add(24 * time.Hour)
	if s.control.status().Phase != "stopped" {
		t.Fatal("idle timeout cleared user stop")
	}
}
