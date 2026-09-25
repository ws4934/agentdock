package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcpapps"
)

func TestOperationFeedbackBindingsMatchDiscoveryCatalogAndBridge(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), MCPAppsEnabled: true, NexusEndpoint: "https://nexus.example.test", ACPEnabled: true, ACPProfiles: []config.ACPProfile{{ID: "fixture", Kind: "custom", Command: executable, Enabled: true}}, ACPDefaultProfile: "fixture"}
	h := newMCPAppTestHarness(t, cfg)
	want := map[string]string{
		"agentdock_context": "agentdock_context", "file_edit": "file_change", "mcp_tool_call": "dynamic_mcp",
		"task_manage": "task_progress", "work_result_show": "work_result", "work_result_freeze": "work_result",
		"acp_session": "acp_status", "workflow_template_manage": "workflow", "recall_write": "recall",
		"file_publish": "artifact", "diagnostic_export": "artifact",
	}
	seen := map[string]bool{}
	for tool, err := range h.session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		view, expected := want[tool.Name]
		if (tool.Meta["ui"] != nil) != expected {
			t.Fatalf("unexpected binding %s: %v", tool.Name, tool.Meta)
		}
		if !expected {
			continue
		}
		seen[tool.Name] = true
		definition, _ := h.runtime.ToolDefinition(tool.Name)
		uri := mcpapps.ResourceURI(definition.UIBinding.ResourceURI)
		resource, err := h.session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: uri})
		if err != nil || len(resource.Contents) != 1 || resource.Contents[0].Text != mcpapps.HTML(view, "") {
			t.Fatalf("%s wrong resource %v", tool.Name, err)
		}
		catalog, err := app.ExportToolCatalog(cfg, app.CatalogRequest{Format: "mcp", Name: tool.Name})
		if err != nil {
			t.Fatal(err)
		}
		descriptor := catalog["tools"].([]map[string]any)[0]
		if !reflect.DeepEqual(descriptor["_meta"].(map[string]any)["ui"], tool.Meta["ui"]) {
			t.Fatalf("catalog drift %s", tool.Name)
		}
		if !reflect.DeepEqual(toolResultMetadata(definition, nil, true)["ui"], tool.Meta["ui"]) {
			t.Fatalf("result drift %s", tool.Name)
		}
		if toolResultMetadata(definition, nil, false) != nil || app.ToolMetadata(definition, false)["ui"] != nil {
			t.Fatal("global UI disable ignored")
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("missing operation feedback: %v", seen)
	}
}

func TestFileEditAutomaticallyReturnsCardForEveryAction(t *testing.T) {
	root := t.TempDir()
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: root})
	definition, _ := h.runtime.ToolDefinition("file_edit")
	if definition.Annotations.ReadOnlyHint {
		t.Fatal("editing must remain a write tool")
	}
	uri := mcpapps.ResourceURI(definition.UIBinding.ResourceURI)
	call := func(args map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := h.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "file_edit", Arguments: args})
		if err != nil || result.IsError != wantError {
			t.Fatalf("%v: result=%#v error=%v", args, result, err)
		}
		if asMap(result.Meta["ui"])["resourceUri"] != uri {
			t.Fatal("editing did not return its automatic card")
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(encoded, &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	read := func(path string) string {
		t.Helper()
		result, err := h.runtime.Call(t.Context(), "read_file", map[string]any{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		return result["read_revision"].(string)
	}
	assertBytes := func(path, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(data) != want {
			t.Fatalf("%s bytes=%q err=%v", path, data, err)
		}
	}
	call(map[string]any{"action": "add", "path": "note.txt", "content": "alpha\n", "expected_read_revision": "absent"}, false)
	revision := read("note.txt")
	preview := call(map[string]any{"action": "replace", "path": "note.txt", "old": "alpha", "new": "beta", "dry_run": true, "expected_read_revision": revision}, false)
	if preview["dry_run"] != true || !strings.Contains(preview["diff_preview"].(string), "beta") {
		t.Fatal("lost preview semantics")
	}
	assertBytes("note.txt", "alpha\n")
	call(map[string]any{"action": "replace", "path": "note.txt", "old": "alpha", "new": "beta", "expected_read_revision": revision}, false)
	call(map[string]any{"action": "replace", "path": "note.txt", "old": "beta", "new": "unsafe", "expected_read_revision": revision}, true)
	assertBytes("note.txt", "beta\n")
	call(map[string]any{"action": "patch", "workdir": root, "patch": "*** Begin Patch\n*** Update File: note.txt\n@@\n-beta\n+gamma\n*** End Patch", "expected_revisions": map[string]any{"note.txt": read("note.txt")}}, false)
	call(map[string]any{"action": "move", "path": "note.txt", "new_path": "moved.txt", "expected_read_revision": read("note.txt")}, false)
	assertBytes("moved.txt", "gamma\n")
	unchanged := call(map[string]any{"action": "replace", "path": "moved.txt", "old": "gamma", "new": "gamma", "expected_read_revision": read("moved.txt")}, false)
	if unchanged["changed"] != false {
		t.Fatalf("unchanged operation: %v", unchanged)
	}
	call(map[string]any{"action": "delete", "path": "moved.txt", "expected_read_revision": read("moved.txt")}, false)
	if _, err := os.Stat(filepath.Join(root, "moved.txt")); !os.IsNotExist(err) {
		t.Fatal("delete did not remove fixture")
	}
	bridge, err := h.server.Invoke(t.Context(), "file_edit", map[string]any{"action": "add", "path": "bridge.txt", "content": "bridge\n", "expected_read_revision": "absent"})
	if err != nil || bridge["isError"] != false || asMap(bridge["_meta"].(mcpsdk.Meta)["ui"])["resourceUri"] != uri {
		t.Fatalf("bridge lost feedback: %v %v", bridge, err)
	}
	assertBytes("bridge.txt", "bridge\n")
}
