package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/sourceproof"
)

type Environment func(map[string]string) ([]string, error)
type Service struct {
	binary string
	env    Environment
	slots  chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func New(binary string, env Environment) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{binary: binary, env: env, slots: make(chan struct{}, 2), ctx: ctx, cancel: cancel}
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("semantic queries did not stop")
	}
}

type Request struct {
	Action               string `json:"action"`
	Project              string `json:"project,omitempty"`
	Path                 string `json:"path,omitempty"`
	Line                 int    `json:"line,omitempty"`
	Character            int    `json:"character,omitempty"`
	Query                string `json:"query,omitempty"`
	ExpectedReadRevision string `json:"expected_read_revision,omitempty"`
	TimeoutMS            int    `json:"timeout_ms,omitempty"`
	Limit                int    `json:"limit,omitempty"`
}

func (s *Service) executable() (string, error) {
	if s.binary != "" {
		if !filepath.IsAbs(s.binary) {
			return "", errors.New("configured gopls path must be absolute")
		}
		return exec.LookPath(s.binary)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if current, err := os.Executable(); err == nil {
		bundled := filepath.Join(filepath.Dir(current), "gopls"+suffix)
		if info, err := os.Stat(bundled); err == nil && info.Mode().IsRegular() {
			return bundled, nil
		}
	}
	return exec.LookPath("gopls")
}
func (s *Service) Status() map[string]any {
	_, err := s.executable()
	status := "available"
	if err != nil {
		status = "not_installed"
	}
	return map[string]any{"status": status, "supported": true, "language": "go", "server": "gopls", "read_only": true, "starts_server": false, "automatic_install": false, "position_encoding": "utf-16", "query_lifetime": "fresh_bounded_server", "scope": "workspace_results_only_not_process_sandbox"}
}
func fileURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
func uriPath(root, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	path := filepath.FromSlash(u.Path)
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '\\' && path[2] == ':' {
		path = path[1:]
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || !sourceproof.Within(root, real) {
		return "", false
	}
	rel, err := filepath.Rel(root, real)
	return filepath.ToSlash(rel), err == nil
}
func readSource(root, path string) (string, []byte, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	if !sourceproof.Within(root, real) || filepath.Ext(real) != ".go" {
		return "", nil, errors.New("semantic source must be a Go file within the selected project")
	}
	f, err := os.Open(real)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return "", nil, errors.New("Go document exceeds the regular-file limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		return "", nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if len(b) > 2<<20 || int64(len(b)) != info.Size() || !info.ModTime().Equal(after.ModTime()) || !utf8.Valid(b) {
		return "", nil, errors.New("Go document changed or has unsupported encoding")
	}
	return real, b, nil
}
func fileRevision(path string, data []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return fmt.Sprintf("read1:%x", h.Sum(nil))
}
func validPosition(data []byte, line, character int) bool {
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) || character < 0 {
		return false
	}
	row := strings.TrimSuffix(lines[line-1], "\r")
	position := 0
	if character == 0 {
		return true
	}
	for _, r := range row {
		position += len(utf16.Encode([]rune{r}))
		if position == character {
			return true
		}
		if position > character {
			return false
		}
	}
	return false
}
func offlineEnvironment() map[string]string {
	return map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GONOPROXY": "none", "GOSUMDB": "off", "GOPACKAGESDRIVER": "off", "GOVCS": "*:off", "GOTELEMETRY": "off", "GOFLAGS": "-mod=readonly"}
}
func (s *Service) Query(ctx context.Context, r Request) (map[string]any, error) {
	if r.Action == "status" {
		return s.Status(), nil
	}
	methods := map[string]string{"document_symbols": "textDocument/documentSymbol", "definition": "textDocument/definition", "references": "textDocument/references", "hover": "textDocument/hover", "diagnostics": "textDocument/diagnostic", "workspace_symbols": "workspace/symbol"}
	method, ok := methods[r.Action]
	if !ok {
		return nil, errors.New("unsupported read-only LSP action")
	}
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 15000
	}
	if r.TimeoutMS < 100 || r.TimeoutMS > 30000 {
		return nil, errors.New("timeout_ms must be 100..30000")
	}
	if r.Limit == 0 {
		r.Limit = 64
	}
	if r.Limit < 1 || r.Limit > 128 || len(r.Query) > 256 {
		return nil, errors.New("semantic query exceeds bounds")
	}
	binary, err := s.executable()
	if err != nil {
		return s.Status(), nil
	}
	root, err := sourceproof.CanonicalRoot(r.Project)
	if err != nil {
		return nil, err
	}
	var path string
	var source []byte
	revision := ""
	if r.Action != "workspace_symbols" {
		path, source, err = readSource(root, r.Path)
		if err != nil {
			return nil, err
		}
		revision = fileRevision(path, source)
		if r.ExpectedReadRevision != "" && r.ExpectedReadRevision != revision {
			return nil, errors.New("READ_REVISION_CONFLICT")
		}
	}
	if r.Action == "definition" || r.Action == "references" || r.Action == "hover" {
		if !validPosition(source, r.Line, r.Character) {
			return nil, errors.New("invalid 1-based line or zero-based UTF-16 character position")
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("semantic service is closing")
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.TimeoutMS)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	offline := offlineEnvironment()
	env, err := s.env(offline)
	if err != nil {
		return nil, err
	}
	selected, goVersion := localGoEnvironment(ctx, env)
	for key, value := range selected {
		offline[key] = value
	}
	env, err = s.env(offline)
	if err != nil {
		return nil, err
	}
	options := map[string]any{"env": offline, "staticcheck": false, "vulncheck": "Off", "expandWorkspaceToModule": false, "allowImplicitNetworkAccess": false, "analysisProgressReporting": false, "pullDiagnostics": true, "directoryFilters": []string{"-**/.git", "-**/node_modules"}}
	command := exec.Command(binary, "serve")
	command.Dir = root
	command.Env = env
	client, err := startClient(ctx, command, fileURI(root), options)
	if err != nil {
		return nil, err
	}
	defer func() {
		client.close()
		select {
		case <-client.wait:
		case <-time.After(3 * time.Second):
		}
	}()
	initialized, err := client.call(ctx, "initialize", map[string]any{"processId": os.Getpid(), "rootUri": fileURI(root), "clientInfo": map[string]any{"name": "AgentDock-read-only", "version": "1"}, "workspaceFolders": []map[string]any{{"uri": fileURI(root), "name": "project"}}, "initializationOptions": options, "capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-16"}}, "workspace": map[string]any{"configuration": true, "workspaceFolders": true, "applyEdit": false}, "textDocument": map[string]any{"hover": map[string]any{"contentFormat": []string{"plaintext"}}, "documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true}, "publishDiagnostics": map[string]any{"versionSupport": true}}}})
	if err != nil {
		return nil, err
	}
	var init struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err = json.Unmarshal(initialized, &init); err != nil {
		return nil, err
	}
	if encoding := init.Capabilities["positionEncoding"]; len(encoding) > 0 && string(encoding) != `"utf-16"` {
		return nil, errors.New("server selected an unsupported position encoding")
	}
	if err = client.notify("initialized", map[string]any{}); err != nil {
		return nil, err
	}
	uri := fileURI(path)
	if path != "" {
		if err = client.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "go", "version": 1, "text": string(source)}}); err != nil {
			return nil, err
		}
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}}
	if r.Action == "workspace_symbols" {
		params = map[string]any{"query": r.Query}
	}
	if r.Action == "definition" || r.Action == "references" || r.Action == "hover" {
		params["position"] = map[string]any{"line": r.Line - 1, "character": r.Character}
	}
	if r.Action == "references" {
		params["context"] = map[string]any{"includeDeclaration": true}
	}
	var raw json.RawMessage
	proven := true
	if r.Action == "diagnostics" && (len(init.Capabilities["diagnosticProvider"]) == 0 || string(init.Capabilities["diagnosticProvider"]) == "false") {
		raw, proven, err = client.waitDiagnostics(ctx, uri, 1)
		if err != nil && ctx.Err() != nil {
			return map[string]any{"status": "unknown", "reason": "no_current_document_diagnostics", "read_revision": revision, "items": []any{}, "diagnostics_complete": false}, nil
		}
	} else {
		raw, err = client.call(ctx, method, params)
	}
	if err != nil {
		return nil, err
	}
	result, err := projectResult(root, path, r.Action, raw, r.Limit)
	if err != nil {
		return nil, err
	}
	result["status"] = "ready"
	result["server"] = "gopls"
	result["position_encoding"] = "utf-16"
	result["read_only"] = true
	result["go_toolchain"] = goVersion
	result["workspace_snapshot_atomic"] = false
	if r.Action == "diagnostics" {
		result["diagnostics_complete"] = proven && result["diagnostics_report_confirmed"] != false
		if result["diagnostics_report_confirmed"] == false {
			result["status"] = "unknown"
			result["reason"] = "language_server_omitted_report_kind"
		}
	}
	if path != "" {
		_, after, readErr := readSource(root, path)
		if readErr != nil || fileRevision(path, after) != revision {
			result["status"] = "stale"
			result["reason"] = "source_changed_during_query"
			if r.Action == "diagnostics" {
				result["diagnostics_complete"] = false
			}
		}
		result["read_revision"] = revision
	}
	return result, nil
}
