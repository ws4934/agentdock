package app

import (
	"context"
	"encoding/json"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/toolcatalog"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAllRegisteredToolsHaveGroupsAndNonemptyOutputContracts(t *testing.T) {
	for _, d := range ToolDefinitions() {
		if _, ok := toolcatalog.Lookup(d.Group); !ok {
			t.Errorf("%s group=%q", d.Name, d.Group)
		}
		validator, err := compileBuiltInInputValidator(d.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if validator.ValidateValue(map[string]any{}, 0) == nil {
			t.Errorf("%s output accepts an empty result", d.Name)
		}
	}
}
func TestGroupFilterAffectsDiscoveryAndExecution(t *testing.T) {
	cfg := config.Config{AgentDockHome: filepath.Join(t.TempDir(), "home"), AgentDockDefaultDir: t.TempDir(), ToolGroups: []string{"files"}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, d := range r.ToolDefinitions() {
		if d.Group != "core" && d.Group != "files" {
			t.Fatalf("group filter leaked %s", d.Name)
		}
	}
	if _, err := r.Call(t.Context(), "exec_command", map[string]any{"cmd": "must-not-execute"}); err == nil {
		t.Fatal("disabled command executed")
	}
	if _, err := r.CallNamespaced(t.Context(), "files", "exec_command", map[string]any{"cmd": "must-not-execute"}); err == nil {
		t.Fatal("namespace bypassed availability")
	}
	if _, err := r.CallNamespaced(t.Context(), "execution", "read_file", map[string]any{"path": "missing"}); err == nil {
		t.Fatal("wrong namespace accepted")
	}
	result, err := r.CallNamespaced(t.Context(), "core", "tool_catalog", nil)
	if err != nil || result["count"] == 0 {
		t.Fatalf("%+v %#v", result, err)
	}
}
func TestCatalogExportsUseExactDescriptorsAndStableNamespaces(t *testing.T) {
	cfg := config.Config{DesktopEnabled: true, MCPAppsEnabled: true}
	export, err := ExportToolCatalog(cfg, CatalogRequest{Format: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range export["tools"].([]map[string]any) {
		d, ok := toolDefinitionForConfig(item["name"].(string), cfg)
		if !ok {
			t.Fatal(item)
		}
		if !reflect.DeepEqual(item, MCPToolDescriptor(d, true)) {
			t.Fatal("descriptor drift")
		}
		count++
	}
	openai, err := ExportToolCatalog(cfg, CatalogRequest{Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	functions := 0
	for _, ns := range openai["tools"].([]map[string]any) {
		if ns["type"] == "tool_search" {
			continue
		}
		tools := ns["tools"].([]map[string]any)
		if len(tools) > 10 {
			t.Fatalf("oversized namespace: %s", ns["name"])
		}
		for _, tool := range tools {
			if tool["type"] != "function" || tool["parameters"] == nil || tool["strict"] != false {
				t.Fatal(tool)
			}
			functions++
		}
	}
	if functions != count {
		t.Fatalf("missing functions %d != %d", functions, count)
	}
	again, _ := ExportToolCatalog(cfg, CatalogRequest{Format: "openai"})
	if again["catalog_revision"] != openai["catalog_revision"] {
		t.Fatal("unstable catalog")
	}
	b, _ := json.Marshal(openai)
	if len(b) > 256<<10 {
		t.Fatal("catalog exceeds export budget")
	}
}
func TestActionSchemasRejectMissingBusinessArguments(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"worktree_manage", map[string]any{"action": "create"}},
		{"job_observe", map[string]any{"action": "logs"}},
		{"validation_run", map[string]any{"request_id": "x", "adapter": "junit"}},
		{"file_edit", map[string]any{"action": "move"}},
		{"task_read", map[string]any{"action": "get"}},
		{"skill_package", map[string]any{"action": "install"}},
	}
	r := newRuntimeValidationTestRuntime(t)
	for _, tc := range cases {
		if _, err := r.Call(context.Background(), tc.name, tc.args); err == nil {
			t.Errorf("%s accepted incomplete arguments", tc.name)
		}
	}
}
