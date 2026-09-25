package mcp

import (
	"encoding/json"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
)

func TestTaskFeedbackLifecycleAutomaticallyRendersWithoutShow(t *testing.T) {
	root := t.TempDir()
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root})
	call := func(name string, args map[string]any, wantsUI bool) map[string]any {
		t.Helper()
		result, err := h.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %#v %v", name, result, err)
		}
		if (result.Meta["ui"] != nil) != wantsUI {
			t.Fatalf("%s unexpected presentation: %#v", name, result.Meta)
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(encoded, &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	created := call("task_manage", map[string]any{"action": "create", "title": "Automatic card", "goal": "Verify MCP lifecycle feedback", "completion_conditions": []string{"Fixture completed"}, "steps": []map[string]any{{"id": "verify", "title": "Verify"}}}, true)
	id := created["task_id"].(string)
	for i := 0; i < 100; i++ {
		call("task_update", map[string]any{"action": "checkpoint", "task_id": id, "step_id": "verify", "status": "in_progress", "summary": "A bounded checkpoint"}, false)
	}
	snapshot := call("task_read", map[string]any{"action": "snapshot", "task_id": id}, false)
	unchanged := call("task_read", map[string]any{"action": "snapshot", "task_id": id, "if_revision": snapshot["revision"]}, false)
	if unchanged["unchanged"] != true || unchanged["task_summary"] != nil {
		t.Fatal("conditional snapshot returned history")
	}
	call("task_manage", map[string]any{"action": "block", "task_id": id, "summary": "Await decision"}, true)
	call("task_manage", map[string]any{"action": "resume", "task_id": id, "summary": "Decision recorded"}, true)
	call("task_update", map[string]any{"action": "checkpoint", "task_id": id, "completed_step_ids": []string{"verify"}, "summary": "Fixture completed"}, false)
	call("task_update", map[string]any{"action": "final_review", "task_id": id, "status": "pass", "summary": "Verified fixture", "verified": []string{"cond_01: Fixture completed"}}, false)
	call("task_manage", map[string]any{"action": "complete", "task_id": id}, true)
	for _, name := range []string{"task_manage", "work_result_freeze"} {
		d, ok := h.runtime.ToolDefinition(name)
		if !ok || d.UIBinding == nil {
			t.Fatalf("%s needs automatic feedback", name)
		}
		result, err := h.server.Invoke(t.Context(), name, map[string]any{})
		if err != nil || result["_meta"].(mcpsdk.Meta)["ui"] == nil || result["isError"] != true {
			t.Fatalf("%s failed calls lost feedback: %#v %v", name, result, err)
		}
	}
}
