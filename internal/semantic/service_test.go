package semantic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "serve" {
		fakeServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fakeServer() {
	reader := bufio.NewReader(os.Stdin)
	uri := ""
	root := ""
	write := func(value any) {
		b, _ := json.Marshal(value)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	rng := map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 3}}
	for {
		n := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "" {
				break
			}
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Content-Length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n <= 0 || n > maxFrame {
			return
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(reader, b); err != nil {
			return
		}
		var message packet
		if json.Unmarshal(b, &message) != nil {
			return
		}
		var params map[string]json.RawMessage
		_ = json.Unmarshal(message.Params, &params)
		reply := func(value any) { write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": value}) }
		switch message.Method {
		case "initialize":
			_ = json.Unmarshal(params["rootUri"], &root)
			reply(map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-16", "diagnosticProvider": map[string]any{}}})
		case "textDocument/didOpen":
			var doc struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(params["textDocument"], &doc)
			uri = doc.URI
		case "textDocument/documentSymbol":
			if os.Getenv("AGENTDOCK_LSP_FIXTURE") == "stall" {
				time.Sleep(time.Minute)
				return
			}
			write(map[string]any{"jsonrpc": "2.0", "id": "readonly-check", "method": "workspace/applyEdit", "params": map[string]any{"edit": map[string]any{}}})
			reply([]map[string]any{{"name": "Example", "kind": 12, "range": rng, "selectionRange": rng}})
		case "textDocument/definition", "textDocument/references":
			reply([]map[string]any{{"uri": uri, "range": rng}, {"uri": "file:///outside/secret.go", "range": rng}})
		case "workspace/symbol":
			reply([]map[string]any{{"name": "Example", "kind": 12, "location": map[string]any{"uri": root + "/main.go", "range": rng}}})
		case "textDocument/hover":
			reply(map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "func Example() — <not HTML>"}, "range": rng})
		case "textDocument/diagnostic":
			reply(map[string]any{"kind": "full", "items": []map[string]any{{"range": rng, "message": "Fixture warning", "severity": 2}}})
		}
	}
}
func fixture(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package example\nfunc Example() {}\n"), 0600)
	binary, _ := os.Executable()
	s := New(binary, func(values map[string]string) ([]string, error) {
		env := os.Environ()
		for k, v := range values {
			env = append(env, k+"="+v)
		}
		return env, nil
	})
	t.Cleanup(func() { _ = s.Close() })
	return s, root
}
func TestReadOnlyProtocolAndProjection(t *testing.T) {
	s, root := fixture(t)
	for _, action := range []string{"document_symbols", "definition", "references", "hover", "diagnostics", "workspace_symbols"} {
		t.Run(action, func(t *testing.T) {
			r, err := s.Query(t.Context(), Request{Action: action, Project: root, Path: "main.go", Line: 2, Character: 5})
			if err != nil {
				t.Fatal(err)
			}
			if r["status"] != "ready" || r["read_only"] != true {
				t.Fatalf("%#v", r)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "file:///outside") || strings.Contains(string(b), "secret.go") {
				t.Fatalf("leaked outside result: %s", b)
			}
			if action == "diagnostics" && r["diagnostics_complete"] != true {
				t.Fatal("lost diagnostics completion")
			}
		})
	}
	if _, err := s.Query(t.Context(), Request{Action: "definition", Project: root, Path: "main.go", Line: 999, Character: 0}); err == nil {
		t.Fatal("invalid line accepted")
	}
	if _, err := s.Query(t.Context(), Request{Action: "document_symbols", Project: root, Path: "main.go", ExpectedReadRevision: "old"}); err == nil {
		t.Fatal("stale source accepted")
	}
	if _, err := s.Query(t.Context(), Request{Action: "executeCommand", Project: root}); err == nil {
		t.Fatal("mutation protocol exposed")
	}
}
func TestReadOnlyTimeoutAndClose(t *testing.T) {
	t.Setenv("AGENTDOCK_LSP_FIXTURE", "stall")
	s, root := fixture(t)
	start := time.Now()
	_, err := s.Query(t.Context(), Request{Action: "document_symbols", Project: root, Path: "main.go", TimeoutMS: 200})
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("timeout err=%v elapsed=%v", err, time.Since(start))
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Query(t.Context(), Request{Action: "document_symbols", Project: root, Path: "main.go"}); err == nil {
		t.Fatal("closed service accepted query")
	}
}
func TestUTF16BoundariesAndScope(t *testing.T) {
	if !validPosition([]byte("a😀b"), 1, 3) || validPosition([]byte("a😀b"), 1, 2) {
		t.Fatal("UTF16 surrogate boundary is incorrect")
	}
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a b.go"), []byte("package p"), 0600)
	root, _ = filepath.EvalSymlinks(root)
	if path, ok := uriPath(root, fileURI(filepath.Join(root, "a b.go"))); !ok || path != "a b.go" {
		t.Fatalf("path=%q ok=%v", path, ok)
	}
	if _, ok := uriPath(root, "https://example.invalid/file"); ok {
		t.Fatal("non-file URI accepted")
	}
}
func TestActualGopls(t *testing.T) {
	binary := os.Getenv("AGENTDOCK_TEST_GOPLS")
	if binary == "" {
		t.Skip("set AGENTDOCK_TEST_GOPLS to an explicitly installed gopls; no automatic download")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/fixture\n\ngo 1.26.5\n"), 0600)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package fixture\nfunc Add(a,b int) int { return a+b }\nfunc Use() int { return Add(1,2) }\n"), 0600)
	s := New(binary, func(values map[string]string) ([]string, error) {
		env := os.Environ()
		for k, v := range values {
			env = append(env, k+"="+v)
		}
		return env, nil
	})
	defer s.Close()
	for _, action := range []string{"document_symbols", "definition", "references", "hover", "diagnostics", "workspace_symbols"} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		start := time.Now()
		r, err := s.Query(ctx, Request{Action: action, Project: root, Path: "main.go", Line: 3, Character: 25, Query: "Add", TimeoutMS: 25000})
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if r["status"] != "ready" && !(action == "diagnostics" && r["status"] == "unknown" && r["diagnostics_complete"] == false) {
			t.Fatalf("%s: %#v", action, r)
		}
		if action == "document_symbols" || action == "definition" || action == "references" {
			if items, ok := r["items"].([]map[string]any); !ok || len(items) == 0 {
				t.Fatalf("%s returned no fixture symbols: %#v", action, r)
			}
		}
		if action == "hover" && r["text"] == "" {
			t.Fatal("real gopls hover omitted content")
		}
		t.Logf("actual %s: %v", action, time.Since(start))
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package fixture\nfunc Invalid() int { return \"not an integer\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.Query(t.Context(), Request{Action: "diagnostics", Project: root, Path: "main.go", TimeoutMS: 20000})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := r["items"].([]map[string]any)
	if len(items) == 0 {
		t.Fatalf("real compiler diagnostic was not returned: %#v", r)
	}
	if r["diagnostics_complete"] != true && (r["status"] != "unknown" || r["reason"] != "language_server_omitted_report_kind") {
		t.Fatalf("unproven diagnostics not labeled: %#v", r)
	}
}
