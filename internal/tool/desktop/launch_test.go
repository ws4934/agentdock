package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

type launchBackend struct {
	*fakeWindowBackend
	target       ApplicationTarget
	launchCount  int
	resolveCount int
	mode         string
	existed      bool
	noWindow     bool
	resolveErr   error
	launchErr    error
	afterLaunch  func()
	observeErr   error
}

func launchFixture() *launchBackend {
	return &launchBackend{fakeWindowBackend: windowFixture(), target: ApplicationTarget{BundleID: "test.agentdock.fixture", AppPath: "/Applications/Fixture.app", Name: "Fixture"}}
}
func (b *launchBackend) ResolveApplication(ctx context.Context, r LaunchRequest) (ApplicationTarget, error) {
	b.resolveCount++
	return b.target, b.resolveErr
}
func (b *launchBackend) LaunchApplication(ctx context.Context, target ApplicationTarget, mode string) (ApplicationLaunch, error) {
	b.launchCount++
	b.mode = mode
	if b.afterLaunch != nil {
		b.afterLaunch()
	}
	if b.noWindow {
		b.fakeBackend.state.Windows = nil
	}
	if mode == "foreground" {
		b.fakeBackend.state.FrontmostPID = 20
	}
	return ApplicationLaunch{Application: Application{PID: 20, Name: target.Name, BundleID: target.BundleID}, AlreadyRunning: b.existed, LaunchRequested: !b.existed, ActivationRequested: mode == "foreground"}, b.launchErr
}
func (b *launchBackend) State(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if b.launchCount > 0 && b.observeErr != nil {
		return State{}, b.observeErr
	}
	return b.fakeBackend.State(ctx)
}
func zeroWait() *int { n := 0; return &n }
func TestLaunchDefaultsReuseAndWindowFlow(t *testing.T) {
	for _, existed := range []bool{false, true} {
		b := launchFixture()
		b.existed = existed
		s := New(true, b)
		id := observeWindow(t, s)
		result, err := s.Launch(t.Context(), LaunchRequest{BundleID: b.target.BundleID})
		if err != nil {
			t.Fatal(err)
		}
		if b.mode != "background" || result["window_ready"] != true || result["already_running"] != existed || result["launch_requested"] == existed || result["foreground_fallback"] != false || result["application_verified"] != false {
			t.Fatalf("%v mode=%s", result, b.mode)
		}
		if _, ok := result["snapshot_id"]; ok {
			t.Fatal("launch authorized input without snapshot")
		}
		_, err = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
		requireCode(t, err, "STALE_SNAPSHOT")
		_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: observeWindow(t, s)})
		if err != nil {
			t.Fatal(err)
		}
		if b.launchCount != 1 || len(b.inputs) != 1 || len(b.events) != 0 {
			t.Fatal("launch/observe/action flow escaped background target")
		}
		_ = s.Close()
	}
}
func TestLaunchValidationAndPreflightNoSideEffects(t *testing.T) {
	neg := -1
	large := 30001
	cases := []LaunchRequest{{}, {BundleID: "a.b", AppPath: "/A.app"}, {AppPath: "https://example.com/A.app"}, {AppPath: "relative.app"}, {AppPath: "/bin/sh"}, {AppPath: "/A.app/Contents/MacOS/A"}, {BundleID: "bad id"}, {BundleID: "a.b\x00"}, {AppName: "../Safari"}, {AppName: " Safari"}, {BundleID: "a.b", Mode: "auto"}, {BundleID: "a.b", WaitMS: &neg}, {BundleID: "a.b", WaitMS: &large}, {AppName: string([]byte{255})}}
	for _, r := range cases {
		b := launchFixture()
		s := New(true, b)
		_, err := s.Launch(t.Context(), r)
		requireCode(t, err, "INVALID_ARGUMENT")
		if b.launchCount+b.resolveCount != 0 {
			t.Fatal("invalid launch touched native API")
		}
		_ = s.Close()
	}
	for _, mode := range []string{"disabled", "unsupported", "no_session", "resolve_failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			b := launchFixture()
			s := New(mode != "disabled", b)
			defer s.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "unsupported":
				b.supported = false
			case "no_session":
				b.state.FrontmostPID = 0
			case "resolve_failure":
				b.resolveErr = errors.New("not installed")
			case "cancelled":
				cancel()
			}
			_, err := s.Launch(ctx, LaunchRequest{BundleID: "test.app"})
			if err == nil || b.launchCount != 0 {
				t.Fatal("preflight dispatched launch", err)
			}
		})
	}
	legacy := New(true, fixtureBackend())
	defer legacy.Close()
	_, err := legacy.Launch(t.Context(), LaunchRequest{BundleID: "a.b"})
	requireCode(t, err, "LAUNCH_UNSUPPORTED")
}
func TestLaunchNoWindowAndForegroundObservation(t *testing.T) {
	b := launchFixture()
	s := New(true, b)
	defer s.Close()
	b.noWindow = true
	result, err := s.Launch(t.Context(), LaunchRequest{AppName: "Fixture", WaitMS: zeroWait()})
	if err != nil {
		t.Fatal(err)
	}
	if result["window_ready"] != false || result["wait_timed_out"] != false || result["next_required_action"] != "desktop_snapshot" {
		t.Fatal(result)
	}
	short := 2
	result, err = s.Launch(t.Context(), LaunchRequest{BundleID: "a.b", WaitMS: &short})
	if err != nil {
		t.Fatal(err)
	}
	if result["wait_timed_out"] != true || b.launchCount != 2 {
		t.Fatal("window wait retried launch", result, b.launchCount)
	}
	b.noWindow = false
	b.state.Windows = []Window{{ID: 9, PID: 20, Bounds: Rect{Width: 20, Height: 20}}}
	result, err = s.Launch(t.Context(), LaunchRequest{AppPath: b.target.AppPath, Mode: "foreground", WaitMS: zeroWait()})
	if err != nil {
		t.Fatal(err)
	}
	if result["activation_observed"] != true || result["activation_requested"] != true || result["background_input_allowed"] != false {
		t.Fatal(result)
	}
}
func TestLaunchFailureCancellationAndInterference(t *testing.T) {
	b := launchFixture()
	s := New(true, b)
	defer s.Close()
	b.launchErr = core.NewErrorDetails("DESKTOP_LAUNCH_TIMEOUT", "may still launch", "desktop", map[string]any{"may_have_launched": true})
	_, err := s.Launch(t.Context(), LaunchRequest{BundleID: "a.b"})
	requireCode(t, err, "DESKTOP_LAUNCH_TIMEOUT")
	if b.launchCount != 1 {
		t.Fatal("timeout retried launch")
	}
	b.launchErr = nil
	b.afterLaunch = func() { b.observeErr = errors.New("no metadata") }
	result, err := s.Launch(t.Context(), LaunchRequest{BundleID: "a.b"})
	if err != nil || result["observation_complete"] != false || result["observation_error"] == nil {
		t.Fatal(result, err)
	}
	b.observeErr = nil
	b.afterLaunch = func() { b.state.FrontmostPID = 20 }
	result, err = s.Launch(t.Context(), LaunchRequest{BundleID: "a.b"})
	if err != nil || result["background_interference"] != true || b.mode != "background" {
		t.Fatal(result, err)
	}
	b.afterLaunch = nil
	b.state.FrontmostPID = 10
	b.noWindow = true
	ctx, cancel := context.WithCancel(t.Context())
	b.afterLaunch = cancel
	_, err = s.Launch(ctx, LaunchRequest{BundleID: "a.b"})
	requireCode(t, err, "DESKTOP_LAUNCH_INCOMPLETE")
}
func TestCloseCancelsLaunchWindowWait(t *testing.T) {
	b := launchFixture()
	b.noWindow = true
	s := New(true, b)
	started := make(chan struct{})
	b.afterLaunch = func() { close(started) }
	done := make(chan error, 1)
	go func() { _, err := s.Launch(context.Background(), LaunchRequest{BundleID: "a.b"}); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("launch never started")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	requireCode(t, <-done, "DESKTOP_LAUNCH_INCOMPLETE")
	_, err := s.Launch(t.Context(), LaunchRequest{BundleID: "a.b"})
	requireCode(t, err, "RUNTIME_CLOSING")
}
func TestApplicationNameLookupExactAndAmbiguous(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Utilities", "Sample.app")
	if err := os.MkdirAll(app, 0700); err != nil {
		t.Fatal(err)
	}
	app, err := filepath.EvalSymlinks(app)
	if err != nil {
		t.Fatal(err)
	}
	found, err := lookupApplicationName(t.Context(), "sample", []string{root})
	if err != nil || found != app {
		t.Fatal(found, err)
	}
	found, err = lookupApplicationName(t.Context(), "Sample.app", []string{root})
	if err != nil || found != app {
		t.Fatal(found, err)
	}
	_, err = lookupApplicationName(t.Context(), "Sam", []string{root})
	requireCode(t, err, "APPLICATION_NOT_FOUND")
	// 不扫描 .app 内的嵌套 helper，也不会因同一路径的别名误报多个实例。
	if err := os.MkdirAll(filepath.Join(app, "Nested.app"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = lookupApplicationName(t.Context(), "Nested", []string{root})
	requireCode(t, err, "APPLICATION_NOT_FOUND")
	if err := os.MkdirAll(filepath.Join(root, "Sample.app"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = lookupApplicationName(t.Context(), "sample", []string{root})
	requireCode(t, err, "APPLICATION_AMBIGUOUS")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = lookupApplicationName(ctx, "Sample", []string{root})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNativeLaunchDoesNotUseShellOrBypassSystemGuards(t *testing.T) {
	data, err := os.ReadFile("native_launch_impl.h")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"system(", "popen(", "CGEventPost(", "configuration.arguments=", "configuration.environment=", "xattr", "TCC.db"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("launch bridge contains " + forbidden)
		}
	}
	for _, required := range []string{"configuration.activates=foreground?YES:NO", "configuration.createsNewApplicationInstance=NO", "configuration.allowsRunningApplicationSubstitution=NO"} {
		if !strings.Contains(string(data), required) {
			t.Fatal("missing explicit launch policy: " + required)
		}
	}
}
