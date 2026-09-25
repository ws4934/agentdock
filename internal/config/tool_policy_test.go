package config

import (
	"testing"
)

func TestToolGroupAndResultPolicyValidation(t *testing.T) {
	for _, groups := range [][]string{{"missing"}, {"files", "files"}} {
		cfg := Config{ToolGroups: groups}
		if cfg.Normalize() == nil {
			t.Fatalf("invalid groups accepted: %v", groups)
		}
	}
	cfg := Config{ResultTextMode: "guess"}
	if cfg.Normalize() == nil {
		t.Fatal("ambiguous result mode accepted")
	}
	t.Setenv("AGENTDOCK_MAX_TOOL_RESULT_BYTES", "8192")
	t.Setenv("AGENTDOCK_TOOL_GROUPS", "files,execution")
	t.Setenv("AGENTDOCK_RESULT_TEXT_MODE", "json")
	parsed, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MaxToolResultBytes != 8192 || parsed.ResultTextMode != "json" || len(parsed.ToolGroups) != 2 {
		t.Fatalf("%+v", parsed.ToolGroups)
	}
}
