package app

import (
	"context"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/desktop"
	"path/filepath"
	"testing"
)

type backgroundContractBackend struct {
	desktopContractBackend
	inputs []desktop.WindowInput
}

func (*backgroundContractBackend) State(context.Context) (desktop.State, error) {
	return desktop.State{FrontmostPID: 1, Displays: []desktop.Display{{ID: 1, Main: true, Bounds: desktop.Rect{Width: 1000, Height: 700}}}, Applications: []desktop.Application{{PID: 1}, {PID: 20}}, Windows: []desktop.Window{{ID: 9, PID: 20, Bounds: desktop.Rect{X: 100, Y: 50, Width: 500, Height: 300}}}}, nil
}
func (b *backgroundContractBackend) CaptureWindow(ctx context.Context, w desktop.Window, n int) (desktop.Capture, error) {
	return b.Capture(ctx, desktop.Display{}, n)
}
func (*backgroundContractBackend) WindowTree(context.Context, desktop.Window, int, int) (desktop.Tree, error) {
	return desktop.Tree{Elements: []desktop.Element{{Role: "AXButton", EnabledKnown: true, Enabled: true, Pressable: true, Path: []int{0}}, {Role: "AXTextField", EnabledKnown: true, Enabled: true, ValueSettable: true, Path: []int{1}}}}, nil
}
func (b *backgroundContractBackend) WindowInput(_ context.Context, _ desktop.Window, in desktop.WindowInput) error {
	b.inputs = append(b.inputs, in)
	return nil
}

func TestDesktopBackgroundRuntimeContracts(t *testing.T) {
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
	b := &backgroundContractBackend{}
	r.desktop = desktop.New(true, b)
	discovery, err := r.Call(t.Context(), desktop.ToolSnapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSnapshot, discovery)
	if discovery["mode"] != "background" || discovery["observation_only"] != true || discovery["image"] != nil {
		t.Fatal(discovery)
	}
	if _, err := r.Call(t.Context(), desktop.ToolAct, map[string]any{"action": "key", "key": "a", "snapshot_id": discovery["snapshot_id"]}); err == nil {
		t.Fatal("discovery authorized input")
	}
	for _, action := range []map[string]any{
		{"action": "click", "point": map[string]any{"x": 120, "y": 80}},
		{"action": "click", "element_id": "e1"},
		{"action": "move", "point": map[string]any{"x": 120, "y": 80}},
		{"action": "drag", "path": []map[string]any{{"x": 120, "y": 80}, {"x": 150, "y": 90}}, "duration_ms": 16},
		{"action": "scroll", "point": map[string]any{"x": 120, "y": 80}, "delta_y": -100},
		{"action": "key", "key": "a", "modifiers": []string{"command"}},
		{"action": "type", "text": "后台中文 🚀"},
		{"action": "set_value", "element_id": "e2", "text": ""},
	} {
		snapshot, err := r.Call(t.Context(), desktop.ToolSnapshot, map[string]any{"window_id": 9, "pid": 20, "accessibility": true})
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolSnapshot, snapshot)
		action["snapshot_id"] = snapshot["snapshot_id"]
		result, err := r.Call(t.Context(), desktop.ToolAct, action)
		if err != nil {
			t.Fatalf("%s: %v", action["action"], err)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolAct, result)
		if result["mode"] != "background" || result["foreground_fallback"] != false || result["application_verified"] != false {
			t.Fatal(result)
		}
	}
	for _, args := range []map[string]any{{"mode": "auto"}, {"window_id": 0}, {"mode": "background", "future": true}, {"pid": -1}} {
		if _, err := r.Call(t.Context(), desktop.ToolSnapshot, args); err == nil {
			t.Fatalf("accepted invalid snapshot: %#v", args)
		}
	}
	if len(b.inputs) < 8 {
		t.Fatal("missing background dispatch coverage")
	}
}
