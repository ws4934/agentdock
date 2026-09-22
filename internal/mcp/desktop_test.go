package mcp

import "testing"

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
