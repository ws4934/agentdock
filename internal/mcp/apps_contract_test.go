package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcpapps"
)

// 验证真正的 tools/list、resources/read 和桥接结果，而不是锁定 CSS/JS 实现字符串。
func TestMCPAppsResourceContractMatrix(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name       string
		nexus, acp bool
		count      int
	}{
		{"standalone", false, false, 5}, {"nexus", true, false, 7}, {"acp", false, true, 6}, {"all", true, true, 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, OAuthServerURL: "https://dock.example.test"}
			if tt.nexus {
				cfg.NexusEndpoint = "https://nexus.example.test"
			}
			if tt.acp {
				cfg.ACPEnabled = true
				cfg.ACPProfiles = []config.ACPProfile{{ID: "helper", Kind: "custom", Command: executable, Enabled: true}}
				cfg.ACPDefaultProfile = "helper"
			}
			h := newMCPAppTestHarness(t, cfg)
			available := map[string]bool{}
			for r, err := range h.session.Resources(t.Context(), nil) {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(r.URI, "/v2-") || !strings.HasSuffix(r.URI, ".html") {
					t.Fatalf("not versioned: %s", r.URI)
				}
				if available[r.URI] {
					t.Fatalf("duplicate resource %s", r.URI)
				}
				available[r.URI] = true
				assertResourceUIMeta(t, r.Meta, "https://dock.example.test")
				read, err := h.session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: r.URI})
				if err != nil {
					t.Fatal(err)
				}
				if len(read.Contents) != 1 || read.Contents[0].URI != r.URI || read.Contents[0].MIMEType != "text/html;profile=mcp-app" {
					t.Fatalf("invalid resource result %s", r.URI)
				}
				assertResourceUIMeta(t, read.Contents[0].Meta, "https://dock.example.test")
				// 桥接与直连必须服务同一份不可变产物。
				bridge, err := h.server.ReadAppResource(r.URI)
				if err != nil {
					t.Fatal(err)
				}
				content := bridge["contents"].([]any)[0].(map[string]any)
				if content["text"] != read.Contents[0].Text || content["uri"] != r.URI {
					t.Fatalf("bridge drift for %s", r.URI)
				}
			}
			if len(available) != tt.count {
				t.Fatalf("resources %d want %d", len(available), tt.count)
			}
			caps := h.server.UIResources()
			if len(caps) != len(available) {
				t.Fatal("capability discovery drift")
			}
			for _, cap := range caps {
				contract, ok := mcpapps.Contract(cap.URI)
				if !ok || contract != cap.Contract || !available[cap.URI] {
					t.Fatalf("invalid bridge capability %#v", cap)
				}
			}
			bound := 0
			for tool, err := range h.session.Tools(t.Context(), nil) {
				if err != nil {
					t.Fatal(err)
				}
				ui, ok := tool.Meta["ui"].(map[string]any)
				if !ok {
					continue
				}
				bound++
				uri, _ := ui["resourceUri"].(string)
				if !available[uri] {
					t.Fatalf("dangling binding %s -> %s", tool.Name, uri)
				}
				def, ok := h.runtime.ToolDefinition(tool.Name)
				if !ok {
					t.Fatal(tool.Name)
				}
				for _, action := range []string{"match", "list", "get", "checkpoint"} {
					if !reflect.DeepEqual(toolResultMetadata(def, map[string]any{"action": action}, true)["ui"], ui) {
						t.Fatalf("result/discovery binding mismatch %s", tool.Name)
					}
				}
			}
			if bound != tt.count {
				t.Fatalf("bound tools %d want %d", bound, tt.count)
			}
			for _, uri := range []string{"ui://agentdock/not-found", "ui://agentdock/context/v2-forged.html", "file:///etc/passwd"} {
				if _, err := h.server.ReadAppResource(uri); err == nil {
					t.Fatalf("accepted unknown URI %s", uri)
				}
			}
		})
	}
}

func TestMCPAppsInvokePreservesErrorUIAndTextFallback(t *testing.T) {
	root := t.TempDir()
	h := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root})
	for _, tc := range []struct {
		name      string
		args      map[string]any
		wantError bool
	}{
		{"agentdock_context", map[string]any{}, false}, {"file_edit", map[string]any{"action": "replace", "path": "missing", "old": "a", "new": "b"}, true},
	} {
		result, err := h.server.Invoke(t.Context(), tc.name, tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if result["isError"] != tc.wantError || result["structuredContent"] == nil || result["content"] == nil {
			t.Fatalf("envelope lost data %#v", result)
		}
		meta, ok := result["_meta"].(mcpsdk.Meta)
		if !ok || meta["ui"] == nil {
			t.Fatal("bridge call lost UI binding")
		}
	}
}
