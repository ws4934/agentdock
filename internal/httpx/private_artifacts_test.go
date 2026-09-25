package httpx

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcp"
)

type privateArtifactTransport struct{ token string }

func (a privateArtifactTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestPrivateArtifactAndSafeDiagnosticsUseAuthenticatedTransport(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, "home"), AuthToken: "fixture-owner-credential", MCPAppsEnabled: true}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	mux := http.NewServeMux()
	oauth := auth.NewOAuthStore()
	mux.HandleFunc("/mcp", mcpEndpointHandler(mcp.NewServer(runtime, cfg), cfg, oauth))
	registerRuntimeAPI(mux, runtime, cfg, oauth)
	host := httptest.NewServer(mux)
	defer host.Close()
	for _, path := range []string{"/mcp", "/internal/runtime/diagnostics"} {
		method := http.MethodGet
		body := ""
		if path == "/mcp" {
			method = http.MethodPost
			body = `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"artifact://agentdock/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
		}
		req, _ := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized || bytes.Contains(b, []byte(cfg.AuthToken)) {
			t.Fatalf("unauthenticated route %s: %d %s", path, response.StatusCode, b)
		}
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "private-artifact-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{Endpoint: host.URL + "/mcp", HTTPClient: &http.Client{Transport: privateArtifactTransport{cfg.AuthToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	path := filepath.Join(root, "private.txt")
	payload := []byte("private fixture payload\n")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	published, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "file_publish", Arguments: map[string]any{"path": path, "delivery": "private"}})
	if err != nil || published.IsError {
		t.Fatalf("publish: %#v %v", published, err)
	}
	data := published.StructuredContent.(map[string]any)
	uri, _ := data["resource_uri"].(string)
	publicURL, _ := data["url"].(string)
	if uri == "" || publicURL != "" {
		t.Fatalf("unexpected private delivery: %#v", data)
	}
	found := false
	for _, content := range published.Content {
		if link, ok := content.(*mcpsdk.ResourceLink); ok && link.URI == uri {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP resource link missing")
	}
	read, err := session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: uri})
	if err != nil || len(read.Contents) != 1 || !bytes.Equal(read.Contents[0].Blob, payload) {
		t.Fatalf("private read: %#v %v", read, err)
	}

	exported, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "diagnostic_export", Arguments: map[string]any{}})
	if err != nil || exported.IsError {
		t.Fatalf("export: %#v %v", exported, err)
	}
	result := exported.StructuredContent.(map[string]any)
	read, err = session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: result["resource_uri"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	bundle := read.Contents[0].Blob
	z, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"diagnostic-report.json": true, "diagnostic-report.md": true, "activity-safe.json": true}
	if len(z.File) != len(allowed) {
		t.Fatalf("unexpected diagnostics entries: %d", len(z.File))
	}
	for _, file := range z.File {
		if !allowed[file.Name] {
			t.Fatal("unexpected support bundle entry", file.Name)
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(reader)
		reader.Close()
		if bytes.Contains(content, []byte(cfg.AuthToken)) || bytes.Contains(content, payload) || bytes.Contains(content, []byte(path)) {
			t.Fatal("diagnostic export leaked credential/project data")
		}
	}
	// Invalid resource URIs may close the SDK transport; test them after successful authenticated reads.
	if _, err = session.ReadResource(t.Context(), &mcpsdk.ReadResourceParams{URI: uri + "/../config"}); err == nil {
		t.Fatal("malformed resource accepted")
	}

}
