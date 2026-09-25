package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestUpstreamMetadataNeverBecomesModelVisible(t *testing.T) {
	upstream := map[string]any{"_meta": map[string]any{"private": "SYNTHETIC_CLIENT_ONLY", "ui": map[string]any{"resourceUri": "untrusted"}}, "content": []any{map[string]any{"type": "text", "text": "visible"}}}
	result := toolEnvelope("mcp_tool_call", map[string]any{"name": "fixture:call", "result": upstream}, nil)
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("SYNTHETIC_CLIENT_ONLY")) || result["_meta"] != nil {
		t.Fatal("private metadata promoted")
	}
	if upstream["_meta"] == nil {
		t.Fatal("input mutated")
	}
}
