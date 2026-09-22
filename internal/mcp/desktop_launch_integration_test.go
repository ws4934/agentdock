//go:build darwin && cgo && desktop_integration

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/desktop"
)

type launchFixtureState struct {
	PID           int    `json:"pid"`
	WindowID      uint32 `json:"window_id"`
	Active        bool   `json:"active"`
	KeyWindow     bool   `json:"key_window"`
	Responder     string `json:"responder"`
	Text          string `json:"text"`
	Clicks        int    `json:"clicks"`
	KeyEvents     int    `json:"key_events"`
	MouseEvents   int    `json:"mouse_events"`
	MouseObserver struct {
		TapActive bool          `json:"tap_active"`
		Owned     int64         `json:"owned_global_events"`
		External  int64         `json:"external_moves"`
		Cursor    desktop.Point `json:"cursor"`
	} `json:"mouse_observer"`
}

func fixtureXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// 使用真实临时 .app，通过 MCP 完成启动→窗口就绪→快照→输入→复用；不操作用户文档。
func TestNativeDesktopLaunchMCPFlow(t *testing.T) {
	if os.Getenv("AGENTDOCK_DESKTOP_INPUT_TEST") != "1" {
		t.Skip("requires explicit GUI test opt-in")
	}
	backend := desktop.NewBackend()
	permissions := backend.Permissions()
	if !backend.Supported() || !permissions.Accessibility || !permissions.ScreenRecording || permissions.SecureInput {
		t.Skipf("native GUI permission/session preconditions unavailable: %+v", permissions)
	}
	before, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bundleID := fmt.Sprintf("test.agentdock.launch.p%d.n%d", os.Getpid(), time.Now().UnixNano())
	appPath := filepath.Join(root, "Launch Fixture.app")
	contents := filepath.Join(appPath, "Contents")
	binary := filepath.Join(contents, "MacOS", "LaunchFixture")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	targetState := filepath.Join(root, "target.json")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleName</key><string>LaunchFixture</string><key>CFBundleExecutable</key><string>LaunchFixture</string><key>CFBundlePackageType</key><string>APPL</string><key>NSPrincipalClass</key><string>NSApplication</string><key>AgentDockTestStatePath</key><string>%s</string><key>AgentDockTestBackground</key><true/></dict></plist>`, bundleID, fixtureXML(targetState))
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	compile := exec.CommandContext(t.Context(), "xcrun", "clang", "-fobjc-arc", "-framework", "Cocoa", "-framework", "ApplicationServices", "../tool/desktop/testdata/fixture.m", "-o", binary)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("fixture compile: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(t.Context(), "codesign", "--force", "--sign", "-", appPath).CombinedOutput(); err != nil {
		t.Fatalf("fixture signing: %v\n%s", err, out)
	}
	sentinelBinary := filepath.Join(root, "Sentinel")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(sentinelBinary, raw, 0700); err != nil {
		t.Fatal(err)
	}
	sentinelState := filepath.Join(root, "sentinel.json")
	sentinel := exec.Command(sentinelBinary, sentinelState)
	sentinel.Env = append(os.Environ(), "AGENTDOCK_TEST_MONITOR_MOUSE=1")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	targetPID := 0
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		state, _ := backend.State(ctx)
		owned := state.FrontmostPID == sentinel.Process.Pid || targetPID > 0 && state.FrontmostPID == targetPID
		// 包括启动请求超时后晚到的实例，只按本次随机 bundle ID 清理。
		for _, app := range state.Applications {
			if app.BundleID == bundleID {
				if p, err := os.FindProcess(app.PID); err == nil {
					_ = p.Kill()
				}
			}
		}
		_ = sentinel.Process.Kill()
		_ = sentinel.Wait()
		if owned && before.FrontmostPID > 0 {
			_ = backend.Activate(ctx, before.FrontmostPID)
		}
	})
	read := func(path string) (launchFixtureState, error) {
		data, e := os.ReadFile(path)
		var state launchFixtureState
		if e == nil {
			e = json.Unmarshal(data, &state)
		}
		return state, e
	}
	wait := func(label, path string, condition func(launchFixtureState) bool) launchFixtureState {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var last launchFixtureState
		for time.Now().Before(deadline) {
			state, e := read(path)
			if e == nil {
				last = state
				if condition(state) {
					return state
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("%s did not complete: %+v", label, last)
		return last
	}
	base := wait("foreground sentinel", sentinelState, func(s launchFixtureState) bool { return s.Active && s.KeyWindow && s.MouseObserver.TapActive })
	harness := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, DesktopEnabled: true})
	call := func(name string, args map[string]any) (map[string]any, *mcpsdk.CallToolResult) {
		t.Helper()
		result, e := harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		if result.IsError {
			t.Fatalf("%s: %#v", name, result.StructuredContent)
		}
		data, e := json.Marshal(result.StructuredContent)
		if e != nil {
			t.Fatal(e)
		}
		var object map[string]any
		if e = json.Unmarshal(data, &object); e != nil {
			t.Fatal(e)
		}
		return object, result
	}
	launched, _ := call(desktop.ToolLaunch, map[string]any{"app_path": appPath, "wait_ms": 5000})
	app := launched["application"].(map[string]any)
	targetPID = int(app["pid"].(float64))
	if launched["already_running"] != false || launched["launch_requested"] != true || launched["window_ready"] != true || launched["activation_requested"] != false || launched["background_interference"] != false {
		t.Fatalf("unexpected launch metadata: %#v", launched)
	}
	target := wait("newly launched app", targetState, func(s launchFixtureState) bool { return s.PID == targetPID && s.WindowID > 0 && !s.Active })
	t.Log("verified native MCP background launch, exact PID and ready window")
	time.Sleep(200 * time.Millisecond) // 独立应用的首次窗口动画是异步的，先等待过渡结束。
	snapshot, imageResult := call(desktop.ToolSnapshot, map[string]any{"window_id": target.WindowID, "pid": targetPID, "accessibility": true})
	imageOK := false
	for _, content := range imageResult.Content {
		if c, ok := content.(*mcpsdk.ImageContent); ok {
			img, e := jpeg.Decode(bytes.NewReader(c.Data))
			if e != nil {
				t.Fatal(e)
			}
			imageOK = img.Bounds().Dx() > 0
		}
	}
	if !imageOK {
		t.Fatal("launch→window snapshot did not produce a JPEG")
	}
	elements := snapshot["elements"].([]any)
	field, button := "", ""
	for _, item := range elements {
		e := item.(map[string]any)
		if e["value_settable"] == true {
			field = e["id"].(string)
		}
		if e["title"] == "Test Click" && e["pressable"] == true {
			button = e["id"].(string)
		}
	}
	if field == "" || button == "" {
		t.Fatal("fixture AX controls missing")
	}
	call(desktop.ToolAct, map[string]any{"action": "set_value", "snapshot_id": snapshot["snapshot_id"], "element_id": field, "text": "启动后后台输入 🚀"})
	wait("background text", targetState, func(s launchFixtureState) bool { return s.Text == "启动后后台输入 🚀" })
	snapshot, _ = call(desktop.ToolSnapshot, map[string]any{"window_id": target.WindowID, "pid": targetPID, "accessibility": true, "screenshot": false})
	// element_id 只能来自本次新快照。
	for _, item := range snapshot["elements"].([]any) {
		e := item.(map[string]any)
		if e["title"] == "Test Click" && e["pressable"] == true {
			button = e["id"].(string)
		}
	}
	call(desktop.ToolAct, map[string]any{"action": "click", "snapshot_id": snapshot["snapshot_id"], "element_id": button})
	wait("background button", targetState, func(s launchFixtureState) bool { return s.Clicks == 1 })
	reused, _ := call(desktop.ToolLaunch, map[string]any{"bundle_id": bundleID, "wait_ms": 0})
	if reused["already_running"] != true || reused["launch_requested"] != false || int(reused["application"].(map[string]any)["pid"].(float64)) != targetPID {
		t.Fatalf("existing instance was not reused: %#v", reused)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, err := read(sentinelState)
	if err != nil {
		t.Fatal(err)
	}
	if after.FrontmostPID != sentinel.Process.Pid || !current.Active || !current.KeyWindow || current.Responder != base.Responder || current.Text != base.Text || current.KeyEvents != base.KeyEvents || current.MouseEvents != base.MouseEvents || !current.MouseObserver.TapActive || current.MouseObserver.Owned != base.MouseObserver.Owned {
		t.Fatalf("launch/action changed sentinel focus or input: %+v", current)
	}
	if current.MouseObserver.External == base.MouseObserver.External && (math.Abs(current.MouseObserver.Cursor.X-base.MouseObserver.Cursor.X) > .5 || math.Abs(current.MouseObserver.Cursor.Y-base.MouseObserver.Cursor.Y) > .5) {
		t.Fatal("physical cursor changed without external movement")
	}
	t.Log("verified MCP launch→snapshot image/AX→text/button→same-PID reuse; foreground focus/input and automation-owned global mouse stream unchanged")
	// 只在完成后台隔离断言后显式测试前台模式，目标仍是本次创建的应用。
	foreground, _ := call(desktop.ToolLaunch, map[string]any{"app_path": appPath, "mode": "foreground", "wait_ms": 3000})
	if foreground["already_running"] != true || foreground["launch_requested"] != false || foreground["activation_observed"] != true {
		t.Fatalf("explicit foreground launch not observed: %#v", foreground)
	}
	t.Log("verified explicit foreground activation reuses the existing app")
}
