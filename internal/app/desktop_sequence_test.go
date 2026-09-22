package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

type sequenceContractBackend struct{ backgroundContractBackend }

func (*sequenceContractBackend) WindowTree(context.Context, desktop.Window, int, int) (desktop.Tree, error) {
	return desktop.Tree{Elements: []desktop.Element{
		{Role: "AXButton", Title: "Next", EnabledKnown: true, Enabled: true, Pressable: true, Path: []int{0}},
		{Role: "AXTextField", Title: "Field", EnabledKnown: true, Enabled: true, ValueSettable: true, Path: []int{1}},
	}}, nil
}

func TestDesktopSequenceRuntimeContract(t *testing.T) {
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
	b := &sequenceContractBackend{}
	r.desktop = desktop.New(true, b)
	definition, ok := r.ToolDefinition(desktop.ToolSequence)
	if !ok || definition.Annotations.ReadOnlyHint {
		t.Fatal("missing mutating sequence tool")
	}
	assertSchemaMatchesRequestType(t, desktop.ToolSequence, definition.InputSchema, reflect.TypeOf(desktop.SequenceRequest{}), true, nil)
	getSnapshot := func() Result {
		v, err := r.Call(t.Context(), desktop.ToolSnapshot, map[string]any{"window_id": 9, "accessibility": true})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	steps := []map[string]any{
		{"action": "set_value", "element": map[string]any{"role": "AXTextField", "title": "Field"}, "text": "example"},
		{"action": "click", "element": map[string]any{"role": "AXButton", "title": "Next"}, "after": map[string]any{"condition": "element_enabled", "element": map[string]any{"role": "AXButton", "title": "Next"}, "timeout_ms": 0}},
	}
	before := getSnapshot()
	result, err := r.Call(t.Context(), desktop.ToolSequence, map[string]any{"snapshot_id": before["snapshot_id"], "steps": steps})
	if err != nil || result["outcome"] != "completed" || len(b.inputs) != 2 {
		t.Fatal(result, err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSequence, result)
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSnapshot, result["observation"].(Result))
	if result["_mcp_image_base64"] == nil {
		t.Fatal("missing final image transport")
	}
	for _, bad := range []map[string]any{
		{"action": "click"},
		{"action": "click", "element": map[string]any{}},
		{"action": "click", "point": map[string]any{"x": 120, "y": 80}, "element": map[string]any{"title": "Next"}},
		{"action": "click", "element": map[string]any{"role": "AXButton", "title": "Next"}, "text": ""},
		{"action": "move"},
		{"action": "drag"},
		{"action": "scroll", "delta_y": -100},
		{"action": "scroll", "point": map[string]any{"x": 120, "y": 80}},
		{"action": "type"},
		{"action": "type", "text": ""},
		{"action": "set_value", "element": map[string]any{"title": "Field"}},
		{"action": "set_value", "element": map[string]any{"role": "AXTextField", "title": "Field"}, "text": nil},
		{"action": "key"},
		{"action": "key", "key": "not-a-key"},
		{"action": "wait", "text": "not-input"},
		{"action": "click", "point": map[string]any{"x": 1, "y": 1}},
		{"action": "click", "element": map[string]any{"role": "AXButton", "title": "Next"}, "script": "arbitrary"},
	} {
		// 每份计划使用未消耗的观察，且把坏参数放在有效首步之后；旧测试复用已消耗
		// 快照会因 STALE_SNAPSHOT 假通过，无法证明整段参数在首个输入前被拒绝。
		fresh := getSnapshot()
		_, err := r.Call(t.Context(), desktop.ToolSequence, map[string]any{"snapshot_id": fresh["snapshot_id"], "steps": []map[string]any{steps[0], bad}})
		var validation *ToolError
		if !errors.As(err, &validation) || validation.Code != "INVALID_ARGUMENT" {
			t.Fatalf("invalid tail did not fail validation: %v, %#v", err, bad)
		}
		if len(b.inputs) != 2 {
			t.Fatal("invalid tail allowed an earlier input", bad)
		}
	}
	if len(b.inputs) != 2 {
		t.Fatal("invalid request dispatched input")
	}
	before = getSnapshot()
	result, err = r.Call(t.Context(), desktop.ToolSequence, map[string]any{"snapshot_id": before["snapshot_id"], "steps": []map[string]any{{"action": "click", "element": map[string]any{"role": "AXButton", "title": "Missing"}}}})
	if err != nil || result["outcome"] != "interrupted" || result["dispatched_steps"] != 0 {
		t.Fatal(result, err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSequence, result)
	if _, ok := newRuntimeValidationTestRuntime(t).ToolDefinition(desktop.ToolSequence); ok {
		t.Fatal("sequence exposed while disabled")
	}
	before, err = r.Call(t.Context(), desktop.ToolSnapshot, map[string]any{"window_id": 9, "screenshot": false})
	if err != nil {
		t.Fatal(err)
	}
	mixed := []map[string]any{
		{"action": "click", "point": map[string]any{"x": 120, "y": 80}, "click_count": 2},
		{"action": "type", "text": "hello"},
		{"action": "key", "key": "enter"},
		{"action": "scroll", "point": map[string]any{"x": 120, "y": 80}, "delta_y": -100},
		{"action": "drag", "path": []map[string]any{{"x": 120, "y": 80}, {"x": 150, "y": 90}}, "duration_ms": 16},
		{"action": "wait", "duration_ms": 1},
	}
	result, err = r.Call(t.Context(), desktop.ToolSequence, map[string]any{"snapshot_id": before["snapshot_id"], "steps": mixed})
	if err != nil || result["outcome"] != "completed" || result["completed_steps"] != 6 || result["dispatched_steps"] != 5 || result["_mcp_image_base64"] != nil {
		t.Fatal("mixed action protocol failed", result, err)
	}
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSequence, result)
	assertToolResultMatchestestOutputSchema(t, desktop.ToolSnapshot, result["observation"].(Result))
}
