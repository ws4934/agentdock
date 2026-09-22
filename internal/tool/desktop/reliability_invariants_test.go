package desktop

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOperationHistoryIsBoundedAndDoesNotStoreInput(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	for i := 0; i < 24; i++ {
		id := observeWindow(t, s)
		if _, err := s.Act(t.Context(), ActionRequest{Action: "set_value", ElementID: "e2", Text: "private-input-history-test", SnapshotID: id}); err != nil {
			t.Fatal(err)
		}
	}
	view := s.control.status()
	if len(view.Events) != 32 {
		t.Fatalf("history length=%d", len(view.Events))
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "private-input-history-test") {
		t.Fatal("history contains typed text")
	}
	for i, event := range view.Events {
		if event.ElapsedMS < 0 || event.Outcome == "cancelled" {
			t.Fatal("successful observation incorrectly cancelled", event)
		}
		if i > 0 && event.Sequence <= view.Events[i-1].Sequence {
			t.Fatal("history sequence is not ordered")
		}
	}
	if view.Events[len(view.Events)-1].Outcome != "dispatched" {
		t.Fatal("last action is not represented")
	}
	view.Events[0].Outcome = "mutated-client-copy"
	if s.control.status().Events[0].Outcome == "mutated-client-copy" {
		t.Fatal("history aliases mutable core state")
	}
}
func TestApplicationProtectionAndForegroundScopeAreSeparate(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	localPoll(t, s, "")
	token := beginDesktopTask(t, s)
	scopedSnapshot(t, s, token)
	ctx, finish, err := s.controlled(taskContext(t.Context(), token), "wait")
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	for _, app := range []Application{{PID: os.Getpid()}, {PID: 20, BundleID: "com.uvwt.agentdock"}, {PID: 20, BundleID: "com.apple.systempreferences"}} {
		requireCode(t, s.control.authorize(ctx, app, "background"), "DESKTOP_PROTECTED_APPLICATION")
	}
	done := make(chan error, 1)
	go func() { done <- s.control.authorize(ctx, Application{PID: 20}, "foreground") }()
	authorizePending(t, s, "deny_application")
	select {
	case err := <-done:
		requireCode(t, err, "DESKTOP_APPLICATION_DENIED")
	case <-time.After(time.Second):
		t.Fatal("foreground denial did not resolve")
	}
	requireCode(t, s.authorizeWindow(ctx, State{}, Window{PID: 999}, "background"), "DESKTOP_APPLICATION_UNIDENTIFIED")
}
func TestScopedForegroundObservationCannotCaptureWithoutTarget(t *testing.T) {
	b := windowFixture()
	b.state.Windows = nil
	s := New(true, b)
	defer s.Close()
	localPoll(t, s, "")
	token := beginDesktopTask(t, s)
	_, err := s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, Mode: "foreground"})
	requireCode(t, err, "DESKTOP_TARGET_REQUIRED")
	if len(b.captured) != 0 {
		t.Fatal("untargeted foreground capture escaped approval")
	}
}
func TestPauseIntentInHeartbeatIsIdempotentAndFailClosed(t *testing.T) {
	s := New(true, windowFixture())
	defer s.Close()
	observeWindow(t, s)
	localPoll(t, s, "")
	id := s.control.status().ID
	first, err := s.LocalControl(ControlRequest{ControllerID: testController, PauseSessionID: id}, true)
	if err != nil || first.Phase != "paused" {
		t.Fatalf("%+v %v", first, err)
	}
	next, err := s.LocalControl(ControlRequest{ControllerID: testController, PauseSessionID: id}, true)
	if err != nil || next.Epoch != first.Epoch {
		t.Fatal("pause heartbeat changes epoch repeatedly")
	}
	_, err = s.Snapshot(context.Background(), SnapshotRequest{WindowID: 9})
	requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
}
