package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestToolDiscoveryGuidanceSurvivesInstructionPrefixes(t *testing.T) {
	for _, nexus := range []bool{false, true} {
		got := serverInstructions(nexus, "  Keep existing operator rules.  ")
		if strings.Count(got, app.ToolDiscoveryGuidance) != 1 {
			t.Fatal("discovery guidance missing or duplicated")
		}
		if !strings.HasSuffix(got, "Additional operator instructions:\nKeep existing operator rules.") {
			t.Fatal("operator instructions changed")
		}
		prefix := []rune(got)
		if len(prefix) > 512 {
			prefix = prefix[:512]
		}
		for _, term := range []string{"exec_command", "file_edit", "tool_catalog", "只读"} {
			if !strings.Contains(string(prefix), term) {
				t.Fatalf("early instructions missing %s (nexus=%v)", term, nexus)
			}
		}
	}
}

func TestToolDiscoveryOverMCPStdio(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := NewServer(runtime, cfg)
	clientInput, serverOutput := io.Pipe()
	serverInput, clientOutput := io.Pipe()
	defer clientInput.Close()
	defer serverOutput.Close()
	defer serverInput.Close()
	defer clientOutput.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ServeStdio(serverInput, serverOutput) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "discovery-regression", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !strings.Contains(session.InitializeResult().Instructions, app.ToolDiscoveryGuidance) {
		t.Fatal("initialize lost discovery guidance")
	}
	found := map[string]bool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name == "exec_command" || tool.Name == "file_edit" {
			found[tool.Name] = true
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
				t.Fatalf("%s lost write annotation", tool.Name)
			}
		}
	}
	if len(found) != 2 {
		t.Fatal("write tools absent from MCP tools/list")
	}
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("%s returned error: %+v", name, result)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	bootstrap := call("agentdock_context", map[string]any{})
	discovery, ok := bootstrap["tool_discovery"].(map[string]any)
	if !ok || discovery["client_visibility"] != "not_observable" || discovery["local_permissions"] != "not_probed" {
		t.Fatalf("bootstrap discovery: %+v", bootstrap)
	}
	for _, name := range []string{"exec_command", "file_edit"} {
		catalog := call("tool_catalog", map[string]any{"format": "mcp", "name": name})
		if catalog["count"] != float64(1) {
			t.Fatalf("exact catalog: %+v", catalog)
		}
		tools := catalog["tools"].([]any)
		if len(tools) != 1 || tools[0].(map[string]any)["name"] != name {
			t.Fatal("wrong exact MCP schema")
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stdio server did not stop")
	}
}
