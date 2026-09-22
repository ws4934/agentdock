package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

func trustFixture(t *testing.T, path string) (*Service, *fakeWindowBackend) {
	t.Helper()
	b := windowFixture()
	b.state.Applications[1] = Application{PID: 20, Name: "Fixture app", BundleID: "test.trust.fixture", Path: filepath.Join(filepath.Dir(path), "Fixture.app")}
	s := New(true, b)
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ConfigureApplicationTrust(path); err != nil {
		t.Fatal(err)
	}
	s.RequireTaskScope()
	localPoll(t, s, "")
	return s, b
}
func trustSnapshot(t *testing.T, s *Service, token, decision string) core.Result {
	t.Helper()
	type reply struct {
		result core.Result
		err    error
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan reply, 1)
	go func() {
		result, err := s.Snapshot(ctx, SnapshotRequest{TaskID: token, WindowID: 9, Accessibility: true})
		done <- reply{result, err}
	}()
	authorizePending(t, s, decision)
	v := <-done
	if v.err != nil {
		t.Fatal(v.err)
	}
	return v.result
}
func TestApplicationTrustPersistsAcrossTasksAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted.json")
	s, b := trustFixture(t, path)
	token := beginDesktopTask(t, s)
	trustSnapshot(t, s, token, "approve_application_always")
	local := localPoll(t, s, s.control.status().ID)
	if !local.TrustAvailable || len(local.TrustedApplications) != 1 {
		t.Fatal(local)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), `"pid":20`) {
		t.Fatal("private capability or process identity persisted")
	}
	stat, _ := os.Stat(path)
	if runtime.GOOS != "windows" && stat.Mode().Perm() != 0600 {
		t.Fatal("nonprivate trust file", stat.Mode())
	}
	_, err = s.Task(t.Context(), TaskRequest{Action: "end", TaskID: token})
	if err != nil {
		t.Fatal(err)
	}
	token = beginDesktopTask(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = s.Snapshot(ctx, SnapshotRequest{TaskID: token, WindowID: 9}); err != nil {
		t.Fatal("trusted application asked again", err)
	}
	status, _ := s.Status(t.Context())
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "trusted_applications") {
		t.Fatal("local trust list leaked into MCP status")
	}
	a := b.state.Applications[1]
	if s.control.applicationAllowedLocked(applicationGrantKey(a, "foreground")) {
		t.Fatal("background approval granted foreground access")
	}
	a.Path = filepath.Join(filepath.Dir(path), "Other.app")
	if s.control.applicationAllowedLocked(applicationGrantKey(a, "background")) {
		t.Fatal("changed app path inherited trust")
	}
	_ = s.Close()
	restarted, backend := trustFixture(t, path)
	backend.state.Applications[1].PID = 21
	backend.state.Windows[1].PID = 21
	nextToken := beginDesktopTask(t, restarted)
	if _, err = restarted.Snapshot(ctx, SnapshotRequest{TaskID: nextToken, WindowID: 9}); err != nil {
		t.Fatal("restart/new pid lost application trust", err)
	}
	if restarted.control.status().PendingApplication != nil {
		t.Fatal("trusted application prompted again")
	}
}

func TestApplicationTrustRevocationStopsAnActiveSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted.json")
	s, b := trustFixture(t, path)
	token := beginDesktopTask(t, s)
	snapshot := trustSnapshot(t, s, token, "approve_application_always")
	entry := localPoll(t, s, s.control.status().ID).TrustedApplications[0]
	b.hook = func(context.Context, WindowInput) error {
		_, err := s.LocalControl(ControlRequest{ControllerID: testController, SessionID: s.control.status().ID, Operation: "revoke_application", TrustID: entry.ID}, false)
		return err
	}
	result, err := s.Sequence(t.Context(), SequenceRequest{TaskID: token, SnapshotID: snapshot["snapshot_id"].(string), Steps: []SequenceStep{{Action: "click", Point: &Point{250, 200}}, {Action: "click", Point: &Point{260, 210}}}})
	if err != nil || result["outcome"] != "interrupted" || len(b.inputs) != 1 || s.control.status().Phase != "paused" {
		t.Fatal(result, err)
	}
	restarted, _ := trustFixture(t, path)
	if len(restarted.control.trusted) != 0 {
		t.Fatal("revocation not persisted")
	}
}

func TestApplicationTrustOnceDoesNotPersistAndForgedApprovalFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted.json")
	s, _ := trustFixture(t, path)
	token := beginDesktopTask(t, s)
	trustSnapshot(t, s, token, "approve_application")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("one-task permission was persisted", err)
	}
	_, err := s.LocalControl(ControlRequest{ControllerID: testController, SessionID: s.control.status().ID, Operation: "approve_application_always", ApprovalID: "stale"}, false)
	requireCode(t, err, "STALE_SNAPSHOT")
	_, err = s.LocalControl(ControlRequest{ControllerID: testController, TrustID: "anything"}, true)
	if err == nil {
		t.Fatal("poll accepted trust mutation")
	}
	_, err = s.LocalControl(ControlRequest{ControllerID: "other-controller-1234", SessionID: s.control.status().ID, Operation: "revoke_application", TrustID: "anything"}, false)
	requireCode(t, err, "DESKTOP_MONITOR_BUSY")
	if len(s.control.trusted) != 0 {
		t.Fatal("forged operation stored trust")
	}
}

func TestApplicationTrustInvalidStorageFailsClosed(t *testing.T) {
	for _, fault := range []string{"corrupt", "trailing", "unknown_version", "symlink", "directory", "public", "process_only", "forged_id"} {
		t.Run(fault, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trusted.json")
			if fault == "public" && runtime.GOOS == "windows" {
				t.Skip("Unix permission mode")
			}
			raw := []byte(`{"version":1,"applications":[]}`)
			switch fault {
			case "corrupt":
				raw = []byte("{")
			case "trailing":
				raw = append(raw, []byte(" {}")...)
			case "unknown_version":
				raw = []byte(`{"version":99,"applications":[]}`)
			case "process_only", "forged_id":
				a := Application{PID: 20, Name: "App", BundleID: "test.app", Path: filepath.Join(filepath.Dir(path), "App.app")}
				entry, e := trustedApplication(a, "background")
				if e != nil {
					t.Fatal(e)
				}
				if fault == "process_only" {
					entry.Application.PID = 20
				} else {
					entry.ID = "forged"
				}
				raw, _ = json.Marshal(applicationTrustFile{Version: 1, Applications: []TrustedApplication{entry}})
			}
			if fault == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if fault == "symlink" {
				target := filepath.Join(filepath.Dir(path), "other.json")
				_ = os.WriteFile(target, raw, 0600)
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			} else {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if fault == "public" {
					_ = os.Chmod(path, 0644)
				}
			}
			s := New(true, windowFixture())
			defer s.Close()
			if err := s.ConfigureApplicationTrust(path); err == nil {
				t.Fatal("invalid trust store loaded")
			}
			if s.control.trustPath != "" || len(s.control.trusted) != 0 {
				t.Fatal("failed read retained permission")
			}
		})
	}
}

func TestApplicationTrustFailedWriteDoesNotGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted.json")
	s, b := trustFixture(t, path)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s.control.mu.Lock()
	err := s.control.rememberApplicationLocked(b.state.Applications[1], "background")
	s.control.mu.Unlock()
	if err == nil || len(s.control.trusted) != 0 {
		t.Fatal("failed write granted trust", err)
	}
	if _, err = trustedApplication(Application{PID: 20}, "background"); err == nil {
		t.Fatal("PID-only permanent approval accepted")
	}
	a := b.state.Applications[1]
	a.BundleID = "com.uvwt.agentdock"
	if _, err = trustedApplication(a, "background"); err == nil {
		t.Fatal("own controller could be trusted automatically")
	}
}

func TestApplicationTrustDenialOverridesPersistentPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trusted.json")
	s, b := trustFixture(t, path)
	s.control.mu.Lock()
	defer s.control.mu.Unlock()
	if err := s.control.rememberApplicationLocked(b.state.Applications[1], "background"); err != nil {
		t.Fatal(err)
	}
	key := applicationGrantKey(b.state.Applications[1], "background")
	s.control.denials[key] = true
	if s.control.applicationAllowedLocked(key) {
		t.Fatal("task denial overridden")
	}
}
