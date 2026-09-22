package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/desktopcontrol"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

type permissionDiagnosticBackend struct {
	desktopContractBackend
	permissions desktop.Permissions
	unintended  int
}

func (b *permissionDiagnosticBackend) Permissions() desktop.Permissions { return b.permissions }
func (b *permissionDiagnosticBackend) State(context.Context) (desktop.State, error) {
	b.unintended++
	return desktop.State{}, nil
}
func (b *permissionDiagnosticBackend) RequestPermission(string) error { b.unintended++; return nil }
func TestLocalPermissionsReportActualCoreWithoutAcquiringMonitor(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root}
	if e := cfg.Normalize(); e != nil {
		t.Fatal(e)
	}
	r, e := NewRuntime(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	_ = r.desktop.Close()
	b := &permissionDiagnosticBackend{}
	r.desktop = desktop.New(false, b)
	r.RequireDesktopMonitor()
	before, _ := r.desktop.Status(t.Context())
	for _, p := range []desktop.Permissions{{}, {Accessibility: true}, {ScreenRecording: true}, {Accessibility: true, ScreenRecording: true, SecureInput: true}} {
		b.permissions = p
		got, e := r.LocalDesktopControl(t.Context(), desktopcontrol.Request{Method: "computeruse.permissions", Params: json.RawMessage(`{}`)})
		if e != nil {
			t.Fatal(e)
		}
		result := got.(Result)
		if result["process_id"] != os.Getpid() || result["enabled"] != false || result["permissions"] != p || result["control_session"] != nil {
			t.Fatal(result)
		}
		if result["executable_path"] == "" || result["checked_at"] == "" {
			t.Fatal("missing provenance", result)
		}
	}
	after, _ := r.desktop.Status(t.Context())
	if !reflect.DeepEqual(before["control_session"], after["control_session"]) || b.unintended != 0 {
		t.Fatal("permission diagnostics changed control state or requested/captured", b.unintended)
	}
	for _, raw := range []string{`null`, `[]`, `{"request":true}`, `{"controller_id":"test-controller-0001"}`, `{} {}`} {
		if _, e := r.LocalDesktopControl(t.Context(), desktopcontrol.Request{Method: "computeruse.permissions", Params: json.RawMessage(raw)}); e == nil {
			t.Fatalf("accepted diagnostic side-effect arguments: %s", raw)
		}
	}
	if _, ok := r.ToolDefinition("computeruse.permissions"); ok {
		t.Fatal("private diagnostic route exposed as MCP input tool")
	}
}
