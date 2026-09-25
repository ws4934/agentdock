package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

type mcpAppTestHarness struct {
	runtime    *app.Runtime
	server     *Server
	session    *mcpsdk.ClientSession
	serverDone chan error
}

func assertToolUIResource(t *testing.T, tool *mcpsdk.Tool, uri string, allowedMetaKeys ...string) {
	t.Helper()
	if tool == nil {
		t.Fatal("UI tool is nil")
	}
	ui, ok := tool.Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != uri {
		t.Fatalf("%s standard ui metadata = %#v", tool.Name, tool.Meta["ui"])
	}
	allowed := map[string]bool{"ui": true}
	for _, key := range allowedMetaKeys {
		allowed[key] = true
	}
	for key := range tool.Meta {
		if !allowed[key] {
			t.Fatalf("%s has unexpected tool metadata %q: %#v", tool.Name, key, tool.Meta)
		}
	}
}

func assertResourceUIMeta(t *testing.T, meta mcpsdk.Meta, domain string) {
	t.Helper()
	ui, ok := meta["ui"].(map[string]any)
	if !ok || ui["prefersBorder"] != true {
		t.Fatalf("standard resource ui metadata = %#v", meta["ui"])
	}
	csp, ok := ui["csp"].(map[string]any)
	if !ok || csp["connectDomains"] == nil || csp["resourceDomains"] == nil {
		t.Fatalf("standard resource csp = %#v", ui["csp"])
	}
	if domain == "" {
		if _, exists := ui["domain"]; exists {
			t.Fatalf("unexpected resource ui.domain = %#v", ui["domain"])
		}
	} else if ui["domain"] != domain {
		t.Fatalf("resource domain metadata = %#v", meta)
	}
	if len(meta) != 1 {
		t.Fatalf("resource should expose only standard ui metadata: %#v", meta)
	}
}

func newMCPAppTestHarness(t *testing.T, cfg config.Config) *mcpAppTestHarness {
	return newMCPAppTestHarnessWithApps(t, cfg, true)
}

func newMCPAppTestHarnessWithApps(t *testing.T, cfg config.Config, enabled bool) *mcpAppTestHarness {
	t.Helper()
	cfg.MCPAppsEnabled = enabled
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	server := NewServer(runtime, cfg)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.sdk.Run(t.Context(), serverTransport) }()

	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "agentdock-mcp-apps-test", Version: "1.0.0"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("Connect() error = %v", err)
	}

	harness := &mcpAppTestHarness{runtime: runtime, server: server, session: session, serverDone: serverDone}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := <-serverDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Server.Run() error = %v", err)
		}
		if err := runtime.Close(); err != nil {
			t.Errorf("Runtime.Close() error = %v", err)
		}
	})
	return harness
}

func TestMCPAppsCanBeDisabledWithoutRemovingTools(t *testing.T) {
	root := t.TempDir()
	harness := newMCPAppTestHarnessWithApps(t, config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
	}, false)

	tools := map[string]*mcpsdk.Tool{}
	for tool, err := range harness.session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("Tools() error = %v", err)
		}
		tools[tool.Name] = tool
	}
	if tools["agentdock_context"] == nil || tools["file_edit"] == nil || tools["task_manage"] == nil {
		t.Fatalf("core tools disappeared when MCP Apps UI was disabled: %#v", tools)
	}
	for _, name := range []string{"agentdock_context", "file_edit", "task_manage", "mcp_tool_call", "file_publish"} {
		if ui := tools[name].Meta["ui"]; ui != nil {
			t.Fatalf("%s still exposes Apps UI metadata while disabled: %#v", name, ui)
		}
	}
	if tools["file_publish"].Meta["file_arg_rewrite_paths"] == nil {
		t.Fatalf("file_publish lost non-UI metadata while MCP Apps UI was disabled: %#v", tools["file_publish"].Meta)
	}

	resources := 0
	for _, err := range harness.session.Resources(t.Context(), nil) {
		if err != nil {
			t.Fatalf("Resources() error = %v", err)
		}
		resources++
	}
	if resources != 0 {
		t.Fatalf("resources/list count = %d, want 0 while MCP Apps UI is disabled", resources)
	}
	if got := harness.server.UIResources(); len(got) != 0 {
		t.Fatalf("UIResources() = %#v, want empty while disabled", got)
	}
	if _, err := harness.server.ReadAppResource(protocol.ContextUIResourceURI); err == nil {
		t.Fatal("ReadAppResource() served an MCP App while disabled")
	}
}

func TestMCPAppsBusinessResultsRemainStructured(t *testing.T) {
	root := t.TempDir()
	harness := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root})
	contextResult, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "agentdock_context", Arguments: map[string]any{}})
	if err != nil || contextResult.IsError {
		t.Fatalf("agentdock_context result=%#v err=%v", contextResult, err)
	}
	contextStructured, ok := contextResult.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("agentdock_context structuredContent = %#v", contextResult.StructuredContent)
	}
	for _, field := range []string{"skills", "dynamic_mcp", "workflow_templates", "rules"} {
		if contextStructured[field] == nil {
			t.Fatalf("agentdock_context structuredContent missing %s: %#v", field, contextStructured)
		}
	}
	if contextStructured["context"] != nil {
		t.Fatalf("agentdock_context structuredContent still contains legacy Markdown context: %#v", contextStructured)
	}

	filePath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(filePath, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileEditResult, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "file_edit",
		Arguments: map[string]any{
			"action": "replace", "path": "note.txt", "old": "alpha", "new": "beta", "dry_run": true,
		},
	})
	if err != nil || fileEditResult.IsError {
		t.Fatalf("file_edit result=%#v err=%v", fileEditResult, err)
	}
	fileEditStructured, ok := fileEditResult.StructuredContent.(map[string]any)
	if !ok || fileEditStructured["action"] != "replace" || fileEditStructured["dry_run"] != true || fileEditStructured["view"] != nil {
		t.Fatalf("file_edit structuredContent = %#v", fileEditResult.StructuredContent)
	}
	diffPreview, _ := fileEditStructured["diff_preview"].(string)
	if !strings.Contains(diffPreview, "beta") {
		t.Fatalf("file_edit diff_preview = %q", diffPreview)
	}
	fileBytes, err := os.ReadFile(filePath)
	if err != nil || string(fileBytes) != "alpha\n" {
		t.Fatalf("dry-run changed file: content=%q err=%v", fileBytes, err)
	}
	errorResult, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "file_edit",
		Arguments: map[string]any{
			"action": "replace", "path": "note.txt", "old": "missing", "new": "beta", "expected_matches": 1,
		},
	})
	if err != nil || !errorResult.IsError {
		t.Fatalf("file_edit validation error result=%#v err=%v", errorResult, err)
	}
	errorStructured, ok := errorResult.StructuredContent.(map[string]any)
	if !ok || errorStructured["code"] != "MATCH_COUNT_MISMATCH" {
		t.Fatalf("file_edit validation structuredContent = %#v", errorResult.StructuredContent)
	}

	createdTask, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "task_manage",
		Arguments: map[string]any{
			"action": "create", "title": "Widget task", "goal": "verify direct task UI",
			"completion_conditions": []string{"done"},
			"steps":                 []map[string]any{{"id": "verify", "title": "Verify"}},
		},
	})
	if err != nil || createdTask.IsError {
		t.Fatalf("task_manage create result=%#v err=%v", createdTask, err)
	}
	createdTaskStructured, ok := createdTask.StructuredContent.(map[string]any)
	if !ok || createdTaskStructured["action"] != "create" || createdTaskStructured["view"] != nil || createdTaskStructured["task_summary"] == nil {
		t.Fatalf("task_manage create structuredContent = %#v", createdTask.StructuredContent)
	}
	listedTasks, err := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "task_manage", Arguments: map[string]any{"action": "list"}})
	if err != nil || listedTasks.IsError {
		t.Fatalf("task_manage list result=%#v err=%v", listedTasks, err)
	}
	listedStructured, ok := listedTasks.StructuredContent.(map[string]any)
	if !ok || listedStructured["action"] != "list" || listedStructured["tasks"] == nil {
		t.Fatalf("task_manage list structuredContent = %#v", listedTasks.StructuredContent)
	}

}

func TestAppWidgetDomainRequiresHTTPSOrigin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https origin", raw: "https://dockmini.example.test/", want: "https://dockmini.example.test"},
		{name: "https port", raw: "https://dockmini.example.test:8443", want: "https://dockmini.example.test:8443"},
		{name: "empty", raw: "", want: ""},
		{name: "http", raw: "http://127.0.0.1:8765", want: ""},
		{name: "path", raw: "https://dockmini.example.test/mcp", want: ""},
		{name: "query", raw: "https://dockmini.example.test?x=1", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := appWidgetDomain(test.raw); got != test.want {
				t.Fatalf("appWidgetDomain(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}
