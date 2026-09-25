package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestToolCatalogCLIIsPureAndFiltersGroups(t *testing.T) {
	home := t.TempDir() + "/must-not-be-created"
	t.Setenv("AGENTDOCK_HOME", home)
	t.Setenv("AGENTDOCK_TOOL_GROUPS", "files")
	t.Setenv("AGENTDOCK_ACP_ENABLED", "false")
	var out, errout bytes.Buffer
	if err := runToolsCommand([]string{"--format", "openai"}, &out, &errout); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["format"] != "openai" {
		t.Fatal(result)
	}
	for _, raw := range result["groups"].([]any) {
		id := raw.(map[string]any)["id"]
		if id != "files" && id != "core" {
			t.Fatal(id)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("catalog CLI initialized runtime state")
	}
}
