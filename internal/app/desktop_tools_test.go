package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	desktop "github.com/uvwt/agentdock/internal/tool/desktop"
)

type desktopContractBackend struct{}

func (desktopContractBackend) Supported() bool { return true }
func (desktopContractBackend) Permissions() desktop.Permissions {
	return desktop.Permissions{ScreenRecording: true, Accessibility: true}
}
func (desktopContractBackend) RequestPermission(string) error { return nil }
func (desktopContractBackend) State(context.Context) (desktop.State, error) {
	return desktop.State{FrontmostPID: 1, Displays: []desktop.Display{{ID: 1, Main: true, Bounds: desktop.Rect{Width: 800, Height: 600}}}, Applications: []desktop.Application{{PID: 1}}, Windows: []desktop.Window{}}, nil
}
func (desktopContractBackend) Capture(context.Context, desktop.Display, int) (desktop.Capture, error) {
	return desktop.Capture{Data: []byte("test image"), Width: 800, Height: 600}, nil
}
func (desktopContractBackend) Tree(context.Context, int, int, int) (desktop.Tree, error) {
	return desktop.Tree{Elements: []desktop.Element{}}, nil
}
func (desktopContractBackend) Press(context.Context, int, desktop.Element) error { return nil }
func (desktopContractBackend) Activate(context.Context, int) error               { return nil }
func (desktopContractBackend) Mouse(context.Context, int, string, desktop.Point, int, int, uint64) error {
	return nil
}
func (desktopContractBackend) Scroll(context.Context, int, int, int) error    { return nil }
func (desktopContractBackend) Key(context.Context, int, uint16, uint64) error { return nil }
func (desktopContractBackend) Text(context.Context, int, []uint16) error      { return nil }
func TestDesktopRuntimeContracts(t *testing.T) {
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
	r.desktop = desktop.New(true, desktopContractBackend{})
	for _, call := range []struct {
		name string
		args map[string]any
	}{{desktop.ToolStatus, nil}, {desktop.ToolPermissions, map[string]any{"permission": "accessibility"}}, {desktop.ToolSnapshot, map[string]any{"mode": "foreground", "accessibility": true}}} {
		result, e := r.Call(t.Context(), call.name, call.args)
		if e != nil {
			t.Fatal(e)
		}
		assertToolResultMatchestestOutputSchema(t, call.name, result)
	}
	for _, action := range []map[string]any{{"action": "activate", "pid": 1}, {"action": "click", "point": map[string]any{"x": 10, "y": 10}}, {"action": "move", "point": map[string]any{"x": 10, "y": 10}}, {"action": "scroll", "delta_y": -100}, {"action": "key", "key": "s", "modifiers": []string{"cmd"}}, {"action": "type", "text": "你好"}, {"action": "drag", "path": []map[string]any{{"x": 1, "y": 1}, {"x": 2, "y": 2}}, "duration_ms": 16}} {
		snapshot, e := r.Call(t.Context(), desktop.ToolSnapshot, map[string]any{"mode": "foreground", "screenshot": false})
		if e != nil {
			t.Fatal(e)
		}
		action["snapshot_id"] = snapshot["snapshot_id"]
		result, e := r.Call(t.Context(), desktop.ToolAct, action)
		if e != nil {
			t.Fatal(e)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolAct, result)
	}
	for name, request := range map[string]any{desktop.ToolPermissions: desktop.PermissionRequest{}, desktop.ToolSnapshot: desktop.SnapshotRequest{}, desktop.ToolAct: desktop.ActionRequest{}} {
		definition, _ := r.ToolDefinition(name)
		assertSchemaMatchesRequestType(t, name, definition.InputSchema, reflect.TypeOf(request), true, nil)
	}
}
func TestDesktopDisabledAndStrictSchemas(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	if _, ok := r.ToolDefinition(desktop.ToolStatus); !ok {
		t.Fatal("status must be available when desktop is disabled")
	}
	for _, name := range []string{desktop.ToolPermissions, desktop.ToolSnapshot, desktop.ToolAct} {
		if _, ok := r.ToolDefinition(name); ok {
			t.Fatalf("%s exposed while disabled", name)
		}
	}
	for _, name := range []string{desktop.ToolStatus, desktop.ToolSnapshot, desktop.ToolAct, desktop.ToolPermissions} {
		definition, _ := toolDefinitionForConfig(name, config.Config{})
		if definition.Annotations == nil {
			t.Fatal(name)
		}
		if name == desktop.ToolAct && definition.Annotations.ReadOnlyHint {
			t.Fatal("actions marked read-only")
		}
	}
}
