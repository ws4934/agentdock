package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/desktopcontrol"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

func TestLocalDesktopControlIsNotExposedAsMCP(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, DesktopEnabled: true}
	if e := cfg.Normalize(); e != nil {
		t.Fatal(e)
	}
	r, e := NewRuntime(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	_ = r.desktop.Close()
	r.desktop = desktop.New(true, &backgroundContractBackend{})
	for _, name := range []string{"computeruse.poll", "computeruse.command", "desktop_resume", "desktop_control"} {
		if _, ok := r.ToolDefinition(name); ok {
			t.Fatal("local user control exposed remotely", name)
		}
	}
	for _, raw := range []string{`{"controller_id":"test-controller-0001","operation":"resume"}`, `{"controller_id":"test-controller-0001","hidden":true}`, `{} {}`} {
		_, e = r.LocalDesktopControl(context.Background(), desktopcontrol.Request{Method: "computeruse.poll", Params: json.RawMessage(raw)})
		if e == nil {
			t.Fatal("invalid/local mutation poll accepted", raw)
		}
	}
	_, e = r.LocalDesktopControl(t.Context(), desktopcontrol.Request{Method: "computeruse.resume", Params: json.RawMessage(`{}`)})
	if e == nil {
		t.Fatal("unregistered local operation accepted")
	}
	r.RequireDesktopMonitor()
	_, e = r.Call(t.Context(), desktop.ToolSnapshot, map[string]any{"window_id": 9})
	if e == nil {
		t.Fatal("supervised input allowed without local monitor")
	}
	status, e := r.Call(t.Context(), desktop.ToolStatus, nil)
	if e != nil {
		t.Fatal(e)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolStatus, status)
}
