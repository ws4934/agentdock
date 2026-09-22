package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 故障注入仅使用内存后端，不向任何真实应用发送事件。
func TestRegressionStopMustSurfaceReleaseFailure(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	localPoll(t, s, "")
	pressed := make(chan struct{})
	done := make(chan error, 1)
	b.hook = func(_ context.Context, in WindowInput) error {
		if in.Action == "down" {
			close(pressed)
		}
		if in.Action == "up" {
			return errors.New("audit: mouse-up failed")
		}
		return nil
	}
	go func() {
		_, err := s.Act(context.Background(), ActionRequest{Action: "drag", SnapshotID: id, Path: []Point{{250, 200}, {300, 220}}, DurationMS: 2000})
		done <- err
	}()
	select {
	case <-pressed:
	case <-time.After(time.Second):
		t.Fatal("setup did not start drag")
	}
	localCommand(t, s, "stop")
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not drain")
	}
	state := s.control.status()
	upAttempted := len(b.inputs) > 0 && b.inputs[len(b.inputs)-1].Action == "up"
	t.Logf("mouse_up_attempted=%v, returned_error=%v, phase=%s, active=%d, can_resume=%v", upAttempted, err, state.Phase, state.Active, state.CanResume)
	if !upAttempted {
		t.Fatal("fault did not reach release")
	}
	if !strings.Contains(err.Error(), "mouse-up failed") || state.Phase != "cleanup_failed" || state.CanResume {
		t.Fatal("release failure must be preserved, block resume, and require local cleanup acknowledgment")
	}
}

type auditLaunchStateBackend struct {
	*fakeWindowBackend
	failState bool
}

func (b *auditLaunchStateBackend) ResolveApplication(context.Context, LaunchRequest) (ApplicationTarget, error) {
	return ApplicationTarget{BundleID: "audit.other.app", Name: "Other application", AppPath: "/Applications/Other.app"}, nil
}
func (b *auditLaunchStateBackend) LaunchApplication(context.Context, ApplicationTarget, string) (ApplicationLaunch, error) {
	return ApplicationLaunch{}, errors.New("should not reach native launch")
}
func (b *auditLaunchStateBackend) State(ctx context.Context) (State, error) {
	if b.failState {
		return State{}, errors.New("audit: transient desktop state failure")
	}
	return b.fakeBackend.State(ctx)
}
func TestRegressionFailedLaunchMustNotDesyncMonitorTarget(t *testing.T) {
	b := &auditLaunchStateBackend{fakeWindowBackend: windowFixture()}
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.failState = true
	_, err := s.Launch(t.Context(), LaunchRequest{AppName: "Other"})
	if err == nil {
		t.Fatal("launch fault was not injected")
	}
	b.failState = false
	_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	if err != nil {
		t.Logf("old snapshot rejected: %v", err)
		return
	}
	actual := b.targets[len(b.targets)-1]
	displayed := s.control.status()
	t.Logf("actual_input_window=%d pid=%d; monitor_window=%d pid=%d app=%q", actual.ID, actual.PID, displayed.Window.ID, displayed.Window.PID, displayed.Application.Name)
	if actual.ID != displayed.Window.ID || actual.PID != displayed.Window.PID {
		t.Fatal("CONFIRMED: valid old snapshot dispatches to A while monitor still describes failed launch of B")
	}
}
