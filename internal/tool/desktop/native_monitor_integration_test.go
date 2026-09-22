//go:build darwin && cgo && desktop_integration

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/desktopcontrol"
)

type monitorFixtureState struct {
	CanResume  bool   `json:"can_resume"`
	ApprovalID string `json:"approval_id"`
	PID        int    `json:"pid"`
	Phase      string `json:"phase"`
	ID         string `json:"session_id"`
	Connected  bool   `json:"connected"`
	Visible    bool   `json:"visible"`
	Key        bool   `json:"key_window"`
	Main       bool   `json:"main_window"`
	Active     bool   `json:"active_app"`
	Frames     int    `json:"frames"`
	WindowID   uint32 `json:"window_id"`
}

// 实际 Swift NSPanel、ScreenCaptureKit 视频、Unix socket 和 Go 输入取消链路。
func TestNativeComputerUseMonitor(t *testing.T) {
	if os.Getenv("AGENTDOCK_DESKTOP_INPUT_TEST") != "1" {
		t.Skip("explicit isolated GUI test opt-in required")
	}
	b := NewBackend()
	permissions := b.Permissions()
	if !b.Supported() || !permissions.Accessibility || !permissions.ScreenRecording || permissions.SecureInput {
		t.Skipf("native input unavailable: %+v", permissions)
	}
	before, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "admon-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	swiftRoot := filepath.Join(repository, "desktop/macos/AgentDockApp")
	fixture := filepath.Join(root, "Fixture")
	out, err := exec.CommandContext(t.Context(), "xcrun", "clang", "-fobjc-arc", "-framework", "Cocoa", "-framework", "ApplicationServices", "testdata/fixture.m", "-o", fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture compile: %v\n%s", err, out)
	}
	monitor := filepath.Join(root, "MonitorFixture")
	arch := "arm64"
	if runtime.GOARCH == "amd64" {
		arch = "x86_64"
	}
	args := []string{"-swift-version", "5", "-parse-as-library", "-target", arch + "-apple-macosx13.0"}
	for _, name := range []string{"Localization.swift", "ComputerUseTransport.swift", "ComputerUseEmergencyHotkey.swift", "ComputerUsePreview.swift", "ComputerUseMonitor.swift"} {
		args = append(args, filepath.Join(swiftRoot, "Sources", name))
	}
	args = append(args, filepath.Join(swiftRoot, "Tests/ComputerUseMonitorFixture.swift"), "-o", monitor)
	out, err = exec.CommandContext(t.Context(), "swiftc", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("monitor compile: %v\n%s", err, out)
	}
	var children []*exec.Cmd
	defer func() {
		current, _ := b.State(context.Background())
		owned := false
		for _, cmd := range children {
			if cmd.Process != nil {
				if current.FrontmostPID == cmd.Process.Pid {
					owned = true
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
		if owned && before.FrontmostPID > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = b.Activate(ctx, before.FrontmostPID)
		}
	}()
	read := func(path string, to any) bool {
		data, e := os.ReadFile(path)
		return e == nil && json.Unmarshal(data, to) == nil
	}
	wait := func(label string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for %s", label)
	}
	start := func(binary string, args []string, env []string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), env...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
		return cmd
	}
	targetFile := filepath.Join(root, "target.json")
	sentinelFile := filepath.Join(root, "sentinel.json")
	start(fixture, []string{targetFile, "--background"}, []string{"AGENTDOCK_FIXTURE_ANIMATE=1"})
	var target fixtureState
	wait("target", func() bool { return read(targetFile, &target) && target.PID > 0 })
	start(fixture, []string{sentinelFile}, []string{"AGENTDOCK_TEST_MONITOR_MOUSE=1", "AGENTDOCK_TEST_STRICT_MOUSE=1", fmt.Sprintf("AGENTDOCK_TEST_TARGET_PID=%d", target.PID)})
	var sentinel fixtureState
	wait("sentinel", func() bool { return read(sentinelFile, &sentinel) && sentinel.Active && sentinel.KeyWindow })
	baseline, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if baseline.FrontmostPID != sentinel.PID {
		t.Fatal("test sentinel not foreground")
	}
	s := New(true, b)
	defer s.Close()
	s.RequireMonitor()
	s.RequireTaskScope()
	serverCtx, cancelServer := context.WithCancel(t.Context())
	defer cancelServer()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- desktopcontrol.Serve(serverCtx, root, func(ctx context.Context, r desktopcontrol.Request) (any, error) {
			var q ControlRequest
			if e := json.Unmarshal(r.Params, &q); e != nil {
				return nil, e
			}
			return s.LocalControl(q, r.Method == "computeruse.poll")
		})
	}()
	wait("socket", func() bool { _, e := os.Stat(filepath.Join(root, "control.sock")); return e == nil })
	report := filepath.Join(root, "monitor.json")
	commands := filepath.Join(root, "command.txt")
	process := start(monitor, []string{root, report, commands}, nil)
	var panel monitorFixtureState
	wait("monitor lease", func() bool { return read(report, &panel) && panel.Connected })
	token := beginDesktopTask(t, s)
	sequence := 0
	command := func(action string) {
		t.Helper()
		sequence++
		if e := os.WriteFile(commands, []byte(fmt.Sprintf("%d:%s", sequence, action)), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if !sentinel.MouseObserver.TapActive {
		t.Fatal("read-only mouse source observer unavailable")
	}
	lastMouse := sentinel.MouseObserver
	isolated := func() {
		t.Helper()
		current, e := b.State(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		var front fixtureState
		if !read(sentinelFile, &front) {
			t.Fatal("sentinel state unavailable")
		}
		mouse := front.MouseObserver
		if !mouse.TapActive || mouse.OwnedGlobalEvents != sentinel.MouseObserver.OwnedGlobalEvents {
			t.Fatalf("non-hardware mouse event entered global stream: %+v", mouse)
		}
		if mouse.ExternalMoves == lastMouse.ExternalMoves && (math.Abs(mouse.Cursor.X-lastMouse.Cursor.X) > .5 || math.Abs(mouse.Cursor.Y-lastMouse.Cursor.Y) > .5) {
			t.Fatalf("cursor moved without external input: %+v -> %+v", lastMouse, mouse)
		}
		lastMouse = mouse
		if current.FrontmostPID != sentinel.PID || front.KeyEvents != sentinel.KeyEvents || front.MouseEvents != sentinel.MouseEvents || front.Text != sentinel.Text || !front.KeyWindow || !read(report, &panel) || panel.Key || panel.Main || panel.Active {
			t.Fatalf("monitor stole focus/cursor or leaked input: panel=%+v front=%+v cursor=%+v expected=%+v", panel, front, current.Cursor, baseline.Cursor)
		}
	}
	no := false
	snapshot := func() string {
		t.Helper()
		result, e := s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, WindowID: target.WindowID, PID: target.PID, Accessibility: true, Screenshot: &no})
		if e != nil {
			t.Fatal(e)
		}
		return result["snapshot_id"].(string)
	}
	// 首次捕获必须先由真实本地面板确认应用范围。
	firstDone := make(chan error, 1)
	go func() {
		_, e := s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, WindowID: target.WindowID, PID: target.PID, Screenshot: &no})
		firstDone <- e
	}()
	wait("local application approval", func() bool {
		approval := s.control.status().PendingApplication
		return approval != nil && read(report, &panel) && panel.Visible && panel.ApprovalID == approval.ID
	})
	command("approve")
	if e := <-firstDone; e != nil {
		t.Fatal(e)
	}
	id := snapshot()
	wait("live nonactivating panel frames", func() bool { return read(report, &panel) && panel.Visible && panel.Frames >= 3 })
	isolated()
	s.mu.Lock()
	unchanged := s.latest != nil && s.latest.id == id
	s.mu.Unlock()
	if !unchanged {
		t.Fatal("live preview consumed model snapshot")
	}
	if destination := os.Getenv("AGENTDOCK_MONITOR_TEST_IMAGE"); destination != "" {
		capture, e := b.(WindowBackend).CaptureWindow(t.Context(), Window{ID: panel.WindowID, PID: panel.PID}, 1024)
		if e != nil {
			t.Fatal("capture actual test panel", e)
		}
		if e = os.WriteFile(destination, capture.Data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("verified automatic nonactivating panel, continuous target-window frames and unchanged snapshot ID")
	command("collapse")
	wait("collapsed panel", func() bool { return read(report, &panel) && !panel.Visible })
	time.Sleep(4 * time.Second)
	if s.control.status().Phase != "running" {
		t.Fatal("collapse ended monitor lease")
	}
	isolated()
	command("show")
	wait("restored preview", func() bool { return read(report, &panel) && panel.Visible })
	command("pause")
	wait("paused", func() bool {
		return s.control.status().Phase == "paused" && read(report, &panel) && panel.Phase == "paused" && panel.CanResume
	})
	_, err = s.Act(t.Context(), ActionRequest{TaskID: token, Action: "click", ElementID: "e1", SnapshotID: id})
	requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
	command("resume")
	wait("local resume", func() bool { return s.control.status().Phase == "idle" })
	_, err = s.Act(t.Context(), ActionRequest{TaskID: token, Action: "click", ElementID: "e1", SnapshotID: id})
	requireCode(t, err, "STALE_SNAPSHOT")
	t.Log("verified collapse retains local stop control; pause/resume rejects old snapshot")
	result, e := s.Wait(t.Context(), WaitRequest{TaskID: token, PID: target.PID, WindowID: target.WindowID, Condition: "element_enabled", Element: &ElementSelector{Role: "AXButton", Title: textPointer("Test Click")}, TimeoutMS: msPointer(1000)})
	if e != nil || result["met"] != true {
		t.Fatalf("native wait condition: %v %v", result, e)
	}
	command("pause")
	wait("pause before single step", func() bool {
		return s.control.status().Phase == "paused" && read(report, &panel) && panel.Phase == "paused" && panel.CanResume
	})
	command("step")
	wait("one step authorized", func() bool { return s.control.status().Phase == "idle" })
	stepID := snapshot()
	_, e = s.Act(t.Context(), ActionRequest{TaskID: token, Action: "click", Point: &target.Button, SnapshotID: stepID})
	if e != nil {
		t.Fatal(e)
	}
	wait("step consumed", func() bool {
		return s.control.status().Phase == "paused" && s.control.status().Reason == "single_step_completed" && read(report, &panel) && panel.Phase == "paused" && panel.CanResume
	})
	command("resume")
	wait("resume after step", func() bool { return s.control.status().Phase == "idle" })
	t.Log("verified real local app grant, native AX wait and one-mutation single-step with fresh snapshot")

	id = snapshot()
	read(targetFile, &target)
	downBefore := target.Downs
	releaseBefore := target.Releases
	done := make(chan error, 1)
	go func() {
		_, e := s.Act(t.Context(), ActionRequest{TaskID: token, Action: "drag", SnapshotID: id, Path: []Point{{target.Canvas.X - 60, target.Canvas.Y}, {target.Canvas.X + 60, target.Canvas.Y}}, DurationMS: 2000})
		done <- e
	}()
	wait("drag started", func() bool { var value fixtureState; return read(targetFile, &value) && value.Downs > downBefore })
	command("close")
	wait("stop acknowledged", func() bool { return s.control.status().Phase == "stopped" })
	if err = <-done; err == nil {
		t.Fatal("close failed to cancel in-flight drag")
	}
	wait("released original mouse button", func() bool { var value fixtureState; return read(targetFile, &value) && value.Releases > releaseBefore })
	wait("panel closed after stop", func() bool { return read(report, &panel) && !panel.Visible && panel.Phase == "stopped" })
	_, err = s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, WindowID: target.WindowID})
	requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
	isolated()
	t.Log("verified close button cancels active drag, releases original target, then acknowledges stopped and closes panel")
	command("resume")
	wait("new local authorization", func() bool { return s.control.status().Phase == "idle" })
	snapshot()
	if err = process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	wait("monitor disconnect pause", func() bool {
		return s.control.status().Phase == "paused" && s.control.status().Reason == "monitor_disconnected"
	})
	_, err = s.Snapshot(t.Context(), SnapshotRequest{TaskID: token, WindowID: target.WindowID})
	requireCode(t, err, "DESKTOP_CONTROL_BLOCKED")
	t.Logf("verified monitor-process loss pauses core; %d hardware mouse movements distinguished from automation", lastMouse.ExternalMoves-sentinel.MouseObserver.ExternalMoves)
	cancelServer()
	if err = <-serverDone; err != nil {
		t.Fatal(err)
	}
}
