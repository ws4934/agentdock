package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

type launchContractBackend struct {
	backgroundContractBackend
	calls    int
	noWindow bool
}

func (b *launchContractBackend) ResolveApplication(context.Context, desktop.LaunchRequest) (desktop.ApplicationTarget, error) {
	return desktop.ApplicationTarget{BundleID: "test.fixture", AppPath: "/Applications/Fixture.app", Name: "Fixture"}, nil
}
func (b *launchContractBackend) LaunchApplication(_ context.Context, a desktop.ApplicationTarget, mode string) (desktop.ApplicationLaunch, error) {
	b.calls++
	return desktop.ApplicationLaunch{Application: desktop.Application{PID: 20, BundleID: a.BundleID, Name: a.Name}, AlreadyRunning: b.calls > 1, LaunchRequested: b.calls == 1, ActivationRequested: mode == "foreground"}, nil
}
func (b *launchContractBackend) State(ctx context.Context) (desktop.State, error) {
	state, err := b.backgroundContractBackend.State(ctx)
	if b.noWindow {
		state.Windows = nil
	}
	return state, err
}
func TestDesktopLaunchRuntimeContracts(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, DesktopEnabled: true}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_ = r.desktop.Close()
	backend := &launchContractBackend{}
	r.desktop = desktop.New(true, backend)
	definition, ok := r.ToolDefinition(desktop.ToolLaunch)
	if !ok {
		t.Fatal("launch tool not registered")
	}
	if definition.Annotations.ReadOnlyHint {
		t.Fatal("launch incorrectly marked read-only")
	}
	assertSchemaMatchesRequestType(t, desktop.ToolLaunch, definition.InputSchema, reflect.TypeOf(desktop.LaunchRequest{}), true, nil)
	for _, args := range []map[string]any{{"bundle_id": "test.fixture", "wait_ms": 0}, {"app_path": "/Applications/Fixture.app", "wait_ms": 0}, {"app_name": "Fixture", "wait_ms": 0}} {
		result, err := r.Call(t.Context(), desktop.ToolLaunch, args)
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolLaunch, result)
		if result["next_required_action"] != "desktop_snapshot" || result["foreground_fallback"] != false {
			t.Fatal(result)
		}
	}
	backend.noWindow = true
	result, err := r.Call(t.Context(), desktop.ToolLaunch, map[string]any{"bundle_id": "test.fixture", "wait_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolLaunch, result)
	if result["window_ready"] != false || result["already_running"] != true {
		t.Fatal(result)
	}
	calls := backend.calls
	for _, args := range []map[string]any{{}, {"bundle_id": "test.app", "app_name": "Fixture"}, {"app_name": "Fixture", "arguments": []string{"-bad"}}, {"app_path": "/bin/sh"}, {"app_name": "Fixture", "mode": "auto"}, {"app_name": "Fixture", "wait_ms": 30001}, {"bundle_id": ""}, {"app_name": "Fixture", "url": "https://example.com"}} {
		if _, err := r.Call(t.Context(), desktop.ToolLaunch, args); err == nil {
			t.Fatalf("accepted invalid launch: %#v", args)
		}
	}
	if backend.calls != calls {
		t.Fatal("invalid arguments reached launcher")
	}
}
