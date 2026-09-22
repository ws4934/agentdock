package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestDesktopSnapshotReturnsImageAndCoordinateMetadata(t *testing.T) {
	input := map[string]any{"snapshot_id": "abc", "image": map[string]any{"width": 100, "screen_points_per_pixel_x": 2}, "_mcp_image_base64": "aGVsbG8=", "_mcp_image_mime_type": "image/jpeg"}
	envelope := toolEnvelope("desktop_snapshot", input, nil)
	clean := envelope["structuredContent"].(map[string]any)
	if _, ok := clean["_mcp_image_base64"]; ok {
		t.Fatal("base64 leaked into structured content")
	}
	if clean["snapshot_id"] != "abc" {
		t.Fatal(clean)
	}
	content := envelope["content"].([]map[string]any)
	if len(content) != 2 || content[0]["type"] != "text" || content[1]["type"] != "image" || content[1]["mimeType"] != "image/jpeg" {
		t.Fatal(content)
	}
	if input["_mcp_image_base64"] == nil {
		t.Fatal("envelope mutated source result")
	}
}
func TestDesktopActionReturnsObservationImageWithoutBase64InMetadata(t *testing.T) {
	input := map[string]any{
		"action": "click", "event_dispatched": true, "application_verified": false,
		"observation":       map[string]any{"snapshot_id": "fresh", "image": map[string]any{"window_id": 9}},
		"_mcp_image_base64": "aGVsbG8=", "_mcp_image_mime_type": "image/jpeg",
	}
	envelope := toolEnvelope("desktop_act", input, nil)
	clean := envelope["structuredContent"].(map[string]any)
	if clean["_mcp_image_base64"] != nil || clean["event_dispatched"] != true || clean["observation"] == nil {
		t.Fatal(clean)
	}
	content := envelope["content"].([]map[string]any)
	if len(content) != 2 || content[0]["type"] != "text" || content[1]["type"] != "image" {
		t.Fatal(content)
	}
	if input["_mcp_image_base64"] == nil {
		t.Fatal("source result was mutated")
	}
}
func TestDesktopSequenceImageAndPartialProgressEnvelope(t *testing.T) {
	input := map[string]any{"outcome": "completed", "completed_steps": 5, "application_verified": false, "observation": map[string]any{"snapshot_id": "new"}, "_mcp_image_base64": "aGVsbG8=", "_mcp_image_mime_type": "image/jpeg"}
	envelope := toolEnvelope("desktop_sequence", input, nil)
	clean := envelope["structuredContent"].(map[string]any)
	content := envelope["content"].([]map[string]any)
	if clean["_mcp_image_base64"] != nil || clean["completed_steps"] != 5 || len(content) != 2 || content[1]["type"] != "image" {
		t.Fatal(envelope)
	}
	partial := map[string]any{"outcome": "interrupted", "completed_steps": 2, "failed_step": 3, "error": map[string]any{"may_have_dispatched": true, "retry_input": false}}
	envelope = toolEnvelope("desktop_sequence", partial, nil)
	if envelope["structuredContent"].(map[string]any)["completed_steps"] != 2 || len(envelope["content"].([]map[string]any)) != 1 {
		t.Fatal("partial progress lost", envelope)
	}
}

func TestDesktopSequenceAdvertisesExplicitStepsThroughSDK(t *testing.T) {
	root := t.TempDir()
	harness := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, DesktopEnabled: true})
	listed, err := harness.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "desktop_sequence" {
			continue
		}
		// 契约改为易读对象，而不是把有副作用的批量工具伪装成只读或可重放操作。
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint || a.IdempotentHint || a.DestructiveHint == nil || !*a.DestructiveHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Fatal("batch side-effect annotations changed", a)
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		steps := schema["properties"].(map[string]any)["steps"].(map[string]any)
		item := steps["items"].(map[string]any)
		if item["type"] != "object" || item["additionalProperties"] != false || item["allOf"] != nil || item["if"] != nil || item["then"] != nil {
			t.Fatal("opaque step composition or unknown fields", item)
		}
		fields := item["properties"].(map[string]any)
		for field, kind := range map[string]string{"action": "string", "point": "object", "path": "array", "key": "string", "text": "string", "element": "object", "after": "object"} {
			v, ok := fields[field].(map[string]any)
			if !ok || v["type"] != kind {
				t.Fatalf("step field %s lost during tools/list: %#v", field, v)
			}
		}
		if steps["maxItems"] != float64(256) || steps["minItems"] != float64(1) {
			t.Fatal("batch limits changed", steps)
		}
		return
	}
	t.Fatal("desktop_sequence not advertised")
}

func TestDesktopSequenceLogSeparatesDispatchFromOutcome(t *testing.T) {
	result := app.Result{
		"outcome": "interrupted", "total_steps": 5, "dispatched_steps": 2, "completed_steps": 2,
		"failed_step": 3, "failure_stage": "before", "task_id": "private-capability", "snapshot_id": "private-snapshot",
		"steps": []any{map[string]any{"text": "private-content"}},
		"error": map[string]any{"code": "TARGET_IN_USE", "message": "private-content"},
	}
	attrs := appendSequenceLogFields([]any{"ok", true}, result)
	fields := map[string]any{}
	for i := 0; i < len(attrs); i += 2 {
		fields[attrs[i].(string)] = attrs[i+1]
	}
	if fields["outcome"] != "interrupted" || fields["code"] != "TARGET_IN_USE" || fields["dispatched_steps"] != 2 {
		t.Fatal("partial execution lost", fields)
	}
	raw, err := json.Marshal(fields)
	if err != nil || strings.Contains(string(raw), "private-") {
		t.Fatal("sensitive batch data entered diagnostics", err)
	}
}
