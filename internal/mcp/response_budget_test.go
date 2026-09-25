package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
)

func TestLargeSourceAppearsOnceWithoutLosingRevisionOrCursor(t *testing.T) {
	text := "SOURCE_SENTINEL:" + strings.Repeat("source line\n", 10000)
	payload := map[string]any{"content": text, "read_revision": "read1:exact", "next_start_line": 10001, "truncated": true}
	result := toolEnvelope("read_file", payload, nil)
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(wire, []byte("SOURCE_SENTINEL:")) != 1 {
		t.Fatal("source duplicated in wire result")
	}
	legacyText, _ := json.MarshalIndent(payload, "", "  ")
	legacy, _ := json.Marshal(map[string]any{"structuredContent": payload, "content": []map[string]any{{"type": "text", "text": string(legacyText)}}})
	t.Logf("wire bytes: optimized=%d duplicated=%d", len(wire), len(legacy))
	if len(wire)*10 > len(legacy)*6 {
		t.Fatal("large-response budget regressed")
	}
	structured := asMap(result["structuredContent"])
	if structured["content"] != text || structured["read_revision"] != "read1:exact" || structured["next_start_line"] != 10001 {
		t.Fatal("exact source or cursor lost")
	}
	summary := result["content"].([]map[string]any)[0]["text"].(string)
	if len(summary) > maxResultSummaryBytes || strings.Contains(summary, "SOURCE_SENTINEL:") {
		t.Fatal("summary repeated source")
	}
}
func TestErrorSummaryIsBoundedAndFullErrorSurvives(t *testing.T) {
	message := strings.Repeat("错误说明", 1000)
	result := toolEnvelope("file_edit", nil, errors.New(message))
	summary := result["content"].([]map[string]any)[0]["text"].(string)
	if len(summary) > maxResultSummaryBytes || !utf8.ValidString(summary) || !strings.Contains(summary, "error=") {
		t.Fatalf("bad summary: %q", summary)
	}
	if result["isError"] != true || asMap(result["structuredContent"])["error"] != message {
		t.Fatal("error evidence lost")
	}
}
func TestDynamicContentRelocatedWithoutCopyOrMutation(t *testing.T) {
	image := "UNIQUE_IMAGE_SENTINEL" + strings.Repeat("abcd", 100000)
	blocks := []any{map[string]any{"type": "image", "data": image, "mimeType": "image/png"}, map[string]any{"type": "text", "text": "exact upstream output"}}
	remote := map[string]any{"content": blocks, "structuredContent": map[string]any{"important": true}, "isError": true, "vendor": 17}
	input := map[string]any{"name": "test:call", "result": remote}
	result := toolEnvelope("mcp_tool_call", input, nil)
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(wire, []byte("UNIQUE_IMAGE_SENTINEL")) != 1 {
		t.Fatal("image duplicated")
	}
	if remote["content"] == nil || remote["content_location"] != nil {
		t.Fatal("input was mutated")
	}
	projected := asMap(asMap(result["structuredContent"])["result"])
	if projected["content"] != nil || projected["content_location"] != "mcp.content" || projected["vendor"] != 17 || projected["structuredContent"] == nil || result["isError"] != true {
		t.Fatalf("lost metadata: %#v", projected)
	}
	if result["content"].([]any)[1].(map[string]any)["text"] != "exact upstream output" {
		t.Fatal("text changed")
	}
}
func TestHighFrequencyCallsNeverCreateFeedbackCards(t *testing.T) {
	root := t.TempDir()
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root})
	dataOnly := map[string]bool{"agentdock_context": true, "file_edit": true, "task_manage": true, "mcp_tool_call": true, "work_result_read": true, "work_result_freeze": true}
	found := 0
	for tool, err := range h.session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if dataOnly[tool.Name] {
			found++
			if tool.Meta["ui"] != nil {
				t.Fatalf("descriptor mounts %s", tool.Name)
			}
		}
	}
	if found != len(dataOnly) {
		t.Fatal("missing data tools")
	}
	for i := 0; i < 100; i++ {
		result, err := h.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "task_manage", Arguments: map[string]any{"action": "list"}})
		if err != nil || result.IsError || result.Meta["ui"] != nil || result.StructuredContent == nil {
			t.Fatalf("call %d: %#v %v", i, result, err)
		}
	}
	shown, err := h.server.Invoke(t.Context(), "work_result_show", map[string]any{"task_id": "missing", "workdir": root})
	if err != nil || shown["_meta"].(mcpsdk.Meta)["ui"] == nil || shown["isError"] != true {
		t.Fatalf("explicit presentation lost binding/error: %#v %v", shown, err)
	}
}
