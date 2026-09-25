package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTaskFeedbackLifecycleAndConditionalRead(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	call := func(name string, args map[string]any) Result {
		t.Helper()
		result, err := r.Call(t.Context(), name, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertToolResultMatchestestOutputSchema(t, name, result)
		return result
	}
	created := call("task_manage", map[string]any{"action": "create", "title": "Visible task", "goal": "Verify feedback without executing commands", "completion_conditions": []string{"All fixture steps completed"}, "steps": []map[string]any{{"id": "check", "title": "Check"}}})
	id := created["task_id"].(string)
	first := call("task_read", map[string]any{"action": "snapshot", "task_id": id})
	before := call("task_read", map[string]any{"action": "get", "task_id": id})
	for range 20 {
		same := call("task_read", map[string]any{"action": "snapshot", "task_id": id, "if_revision": first["revision"]})
		if same["unchanged"] != true || same["task_summary"] != nil || same["task"] != nil {
			t.Fatalf("unchanged read: %#v", same)
		}
	}
	after := call("task_read", map[string]any{"action": "get", "task_id": id})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read mutated task state")
	}
	call("task_read", map[string]any{"action": "list"})
	updated := call("task_update", map[string]any{"action": "checkpoint", "task_id": id, "step_id": "check", "status": "completed", "summary": "Fixture checked"})
	if updated["task_summary"] == nil {
		t.Fatal("checkpoint lost projection")
	}
	changed := call("task_read", map[string]any{"action": "snapshot", "task_id": id, "if_revision": first["revision"]})
	if changed["unchanged"] != false || changed["revision"] == first["revision"] {
		t.Fatal("checkpoint was not observable")
	}
	encoded, _ := json.Marshal(changed)
	if len(encoded) > 10000 || strings.Contains(string(encoded), `"events"`) || strings.Contains(string(encoded), `"guidance_context"`) {
		t.Fatal("snapshot retained full history")
	}
	call("task_manage", map[string]any{"action": "block", "task_id": id, "summary": "Need a fixture decision"})
	blocked := call("task_read", map[string]any{"action": "snapshot", "task_id": id})
	if blocked["task_summary"].(map[string]any)["blocker"] != "Need a fixture decision" {
		t.Fatal("missing blocker")
	}
	call("task_manage", map[string]any{"action": "resume", "task_id": id, "summary": "Fixture decision recorded"})
	call("task_update", map[string]any{"action": "final_review", "task_id": id, "status": "pass", "summary": "Fixture review", "verified": []string{"cond_01: fixture step completed"}})
	call("task_manage", map[string]any{"action": "complete", "task_id": id})
	terminal := call("task_read", map[string]any{"action": "snapshot", "task_id": id})
	if stringValue := terminal["task_summary"].(map[string]any)["status"]; stringValue != "completed" {
		// taskstate.Status is a distinct string type in direct Runtime results.
		data, _ := json.Marshal(stringValue)
		if string(data) != `"completed"` {
			t.Fatal(stringValue)
		}
	}
}

func TestTaskFeedbackContractsSeparateObservationMutationAndPresentation(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	for _, tc := range []struct {
		name         string
		actions      []string
		ui, readonly bool
	}{
		{"task_manage", []string{"create", "block", "resume", "complete"}, true, false},
		{"task_update", []string{"checkpoint", "final_review"}, false, false},
		{"task_read", []string{"list", "get", "snapshot"}, false, true},
	} {
		d, ok := r.ToolDefinition(tc.name)
		if !ok {
			t.Fatal(tc.name)
		}
		if (d.UIBinding != nil) != tc.ui || d.Annotations.ReadOnlyHint != tc.readonly {
			t.Fatalf("metadata drift: %s", tc.name)
		}
		enum := d.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"]
		if !reflect.DeepEqual(enum, tc.actions) {
			t.Fatalf("%s actions: %v", tc.name, enum)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"task_read", map[string]any{"action": "create", "title": "must not create"}},
		{"task_read", map[string]any{"action": "snapshot", "task_id": "tsk_0123456789abcdef", "cmd": "must not run"}},
		{"task_read", map[string]any{"action": "snapshot", "task_id": "tsk_0123456789abcdef", "limit": 50}},
		{"task_read", map[string]any{"action": "list", "if_revision": "tsk1:" + strings.Repeat("a", 64)}},
		{"task_read", map[string]any{"action": "snapshot", "task_id": "tsk_0123456789abcdef", "if_revision": "forged"}},
		{"task_update", map[string]any{"action": "create"}},
		{"task_manage", map[string]any{"action": "checkpoint", "task_id": "tsk_0123456789abcdef"}},
		{"task_manage", map[string]any{"action": "get", "task_id": "tsk_0123456789abcdef"}},
	} {
		if _, err := r.Call(t.Context(), tc.name, tc.args); err == nil {
			t.Fatalf("accepted cross-boundary request %s %v", tc.name, tc.args)
		}
	}
}
