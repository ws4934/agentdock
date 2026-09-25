package mcp

import (
	"bytes"
	"encoding/json"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"strings"
	"testing"
)

func TestResponseBudgetPreservesFullPrivateResult(t *testing.T) {
	s := &Server{cfg: config.Config{AgentDockHome: t.TempDir(), MaxToolResultBytes: 4096}}
	original := map[string]any{"content": strings.Repeat("exact-source-", 3000), "read_revision": "read1:stable", "next_cursor": "opaque"}
	result := s.boundedEnvelope("read_file", toolEnvelope("read_file", original, nil))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096 || result["isError"] != true {
		t.Fatalf("budget not enforced: %d", len(data))
	}
	payload := asMap(result["structuredContent"])
	if payload["code"] != "RESULT_DEFERRED" || payload["do_not_replay"] != true {
		t.Fatal(payload)
	}
	full := asMap(payload["full_result"])
	store := publicartifacts.New(s.cfg.AgentDockHome, "", 0)
	meta, raw, err := store.Read(full["artifact_id"].(string), 1<<20)
	if err != nil || !meta.Private {
		t.Fatalf("private result unavailable: %+v %v", meta, err)
	}
	var recovered map[string]any
	if err = json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	source := asMap(recovered["structuredContent"])
	if source["content"] != original["content"] || source["read_revision"] != "read1:stable" || source["next_cursor"] != "opaque" {
		t.Fatal("full output lost data")
	}
	if bytes.Contains(data, []byte("http://")) || bytes.Contains(data, []byte("https://")) {
		t.Fatal("private result exposed a URL")
	}
}
func TestExplicitJSONTextModeRemainsCompatible(t *testing.T) {
	original := map[string]any{"content": "complete text", "next_cursor": "cursor", "code": "SAFE_CODE"}
	s := &Server{cfg: config.Config{ResultTextMode: "json"}}
	response := s.boundedEnvelope("read_file", toolEnvelope("read_file", original, nil))
	text := response["content"].([]map[string]any)[0]["text"].(string)
	var parsed map[string]any
	if json.Unmarshal([]byte(text), &parsed) != nil || parsed["content"] != "complete text" || parsed["next_cursor"] != "cursor" {
		t.Fatal(text)
	}
}
