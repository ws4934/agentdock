package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

func TestDesktopWaitAndTaskRuntimeContracts(t *testing.T) {
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
	r.desktop = desktop.New(true, &backgroundContractBackend{})
	task, err := r.Call(t.Context(), desktop.ToolTask, map[string]any{"action": "begin", "title": "contract test"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolTask, task)
	for _, action := range []string{"status", "end"} {
		result, err := r.Call(t.Context(), desktop.ToolTask, map[string]any{"action": action, "task_id": task["task_id"]})
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolTask, result)
	}
	for _, window := range []int{9, 999} {
		result, err := r.Call(t.Context(), desktop.ToolWait, map[string]any{"condition": "window_exists", "pid": 20, "window_id": window, "timeout_ms": 0})
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, desktop.ToolWait, result)
		if result["met"] != (window == 9) || result["input_dispatched"] != false {
			t.Fatal(result)
		}
	}
	for name, request := range map[string]any{desktop.ToolWait: desktop.WaitRequest{}, desktop.ToolTask: desktop.TaskRequest{}} {
		definition, _ := r.ToolDefinition(name)
		assertSchemaMatchesRequestType(t, name, definition.InputSchema, reflect.TypeOf(request), true, nil)
	}
	for _, args := range []map[string]any{{"condition": "window_exists", "pid": 20, "poll_ms": 1}, {"condition": "window_exists", "pid": 20, "element": map[string]any{"unknown": true}}, {"condition": "window_exists", "pid": 20, "task_id": "borrowed"}} {
		if _, err := r.Call(t.Context(), desktop.ToolWait, args); err == nil {
			t.Fatalf("invalid wait accepted: %#v", args)
		}
	}
}
