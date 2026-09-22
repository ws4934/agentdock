//go:build darwin && cgo && desktop_integration

package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/uvwt/agentdock/internal/tool/core"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 两个临时应用：前台哨兵承接可能泄漏的输入，后台目标被完全遮挡。
// 不对真实用户应用发送测试输入；退出只清理本次创建的进程。
func TestNativeBackgroundTwoApplications(t *testing.T) {
	if os.Getenv("AGENTDOCK_DESKTOP_INPUT_TEST") != "1" {
		t.Skip("requires explicit isolated GUI input test opt-in")
	}
	b := NewBackend()
	p := b.Permissions()
	if !b.Supported() || !p.Accessibility || !p.ScreenRecording || p.SecureInput {
		t.Skipf("requires native permissions and inactive Secure Input: %+v", p)
	}
	before, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "AgentDockBackgroundFixture")
	if out, err := exec.CommandContext(t.Context(), "xcrun", "clang", "-fobjc-arc", "-framework", "Cocoa", "-framework", "ApplicationServices", "testdata/fixture.m", "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	var processes []*exec.Cmd
	t.Cleanup(func() {
		state, _ := b.State(context.Background())
		owned := false
		for _, cmd := range processes {
			if state.FrontmostPID == cmd.Process.Pid {
				owned = true
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if owned && before.FrontmostPID > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = b.Activate(ctx, before.FrontmostPID)
		}
	})
	read := func(path string) fixtureState {
		t.Helper()
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		var s fixtureState
		if e = json.Unmarshal(data, &s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	launch := func(name string, background bool) (string, fixtureState) {
		t.Helper()
		path := filepath.Join(root, name+".json")
		args := []string{path}
		if background {
			args = append(args, "--background")
		}
		cmd := exec.Command(binary, args...)
		if !background {
			cmd.Env = append(os.Environ(), "AGENTDOCK_TEST_MONITOR_MOUSE=1", "AGENTDOCK_TEST_TARGET_PID="+strconv.Itoa(processes[0].Process.Pid))
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, cmd)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, e := os.ReadFile(path)
			var s fixtureState
			if e == nil && json.Unmarshal(data, &s) == nil && s.WindowID > 0 {
				return path, s
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("fixture did not launch")
		return "", fixtureState{}
	}
	targetPath, target := launch("target", true)
	sentinelPath, sentinel := launch("sentinel", false)
	time.Sleep(400 * time.Millisecond)
	sentinel = read(sentinelPath)
	baseline, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if baseline.FrontmostPID != sentinel.PID || !sentinel.Active || !sentinel.KeyWindow {
		t.Fatal("foreground sentinel unavailable; refusing real-app input")
	}
	service := New(true, b)
	defer service.Close()
	no := false
	snapshot := func(image bool) core.Result {
		t.Helper()
		r := SnapshotRequest{WindowID: target.WindowID, PID: target.PID, Accessibility: true, Screenshot: &no}
		if image {
			r.Screenshot = nil
		}
		result, e := service.Snapshot(t.Context(), r)
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	if !sentinel.MouseObserver.TapActive {
		t.Fatal("passive mouse observation unavailable; cannot verify global-input isolation")
	}
	lastMouse := sentinel.MouseObserver
	isolated := func() {
		t.Helper()
		state, e := b.State(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		current := read(sentinelPath)
		if state.FrontmostPID != sentinel.PID {
			t.Fatalf("foreground or physical cursor changed; input stopped (frontmost=%d expected=%d cursor=%+v baseline=%+v)", state.FrontmostPID, sentinel.PID, state.Cursor, baseline.Cursor)
		}
		mouse := current.MouseObserver
		if !mouse.TapActive || mouse.OwnedGlobalEvents != sentinel.MouseObserver.OwnedGlobalEvents {
			t.Fatalf("background automation entered the global mouse stream or monitor stopped: %+v", mouse)
		}
		if mouse.ExternalMoves == lastMouse.ExternalMoves && (math.Abs(mouse.Cursor.X-lastMouse.Cursor.X) > .5 || math.Abs(mouse.Cursor.Y-lastMouse.Cursor.Y) > .5) {
			t.Fatalf("physical cursor moved without an external mouse event: before=%+v after=%+v", lastMouse, mouse)
		}
		lastMouse = mouse
		if !current.Active || !current.KeyWindow || current.Responder != sentinel.Responder || current.KeyEvents != sentinel.KeyEvents || current.MouseEvents != sentinel.MouseEvents || current.Text != sentinel.Text {
			t.Fatalf("input/focus leaked into sentinel: %+v baseline=%+v", current, sentinel)
		}
	}
	wait := func(label string, condition func(fixtureState) bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			isolated()
			target = read(targetPath)
			if condition(target) {
				t.Log("verified " + label)
				return
			}
		}
		t.Fatalf("target did not complete %s: %+v", label, target)
	}
	act := func(a ActionRequest, element func(Element) bool) {
		t.Helper()
		result := snapshot(false)
		a.SnapshotID = result["snapshot_id"].(string)
		if element != nil {
			for _, e := range result["elements"].([]Element) {
				if element(e) {
					a.ElementID = e.ID
					break
				}
			}
			if a.ElementID == "" {
				t.Fatal("required AX element unavailable")
			}
		}
		if _, e := service.Act(t.Context(), a); e != nil {
			t.Fatalf("%s: %v", a.Action, e)
		}
	}
	imageResult := snapshot(true)
	data, err := base64.StdEncoding.DecodeString(imageResult["_mcp_image_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	window := imageResult["target_window"].(Window)
	x := int((target.Canvas.X - window.Bounds.X) * float64(img.Bounds().Dx()) / window.Bounds.Width)
	y := int((target.Canvas.Y - window.Bounds.Y) * float64(img.Bounds().Dy()) / window.Bounds.Height)
	red, _, blue, _ := img.At(x, y).RGBA()
	if blue <= red+10000 {
		t.Fatalf("capture is not the occluded blue target window: r=%d b=%d", red, blue)
	}
	if path := os.Getenv("AGENTDOCK_DESKTOP_TEST_IMAGE"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	isolated()
	t.Logf("verified occluded window capture %dx%d and window-scoped AX tree", img.Bounds().Dx(), img.Bounds().Dy())
	field := func(e Element) bool { return e.ValueSettable }
	button := func(e Element) bool { return e.Title == "Test Click" && e.Pressable }
	act(ActionRequest{Action: "set_value", Text: "后台替换 🚀"}, field)
	wait("AX text replacement", func(s fixtureState) bool { return s.Text == "后台替换 🚀" })
	act(ActionRequest{Action: "set_value", Text: ""}, field)
	wait("AX empty replacement", func(s fixtureState) bool { return s.Text == "" })
	act(ActionRequest{Action: "click"}, button)
	wait("background AXPress", func(s fixtureState) bool { return s.Clicks == 1 })
	act(ActionRequest{Action: "click", Point: &target.Button}, nil)
	wait("background coordinate click", func(s fixtureState) bool { return s.Clicks == 2 })
	act(ActionRequest{Action: "click", Point: &target.Field}, nil)
	act(ActionRequest{Action: "type", Text: "后台你好 AgentDock 🚀"}, nil)
	wait("background Unicode typing", func(s fixtureState) bool { return s.Text == "后台你好 AgentDock 🚀" })
	// 后台禁用的菜单命令必须显式拒绝，而不是报告空操作成功或激活应用。
	disabled := snapshot(false)
	_, disabledErr := service.Act(t.Context(), ActionRequest{Action: "key", Key: "a", Modifiers: []string{"command"}, SnapshotID: disabled["snapshot_id"].(string)})
	if disabledErr == nil || !strings.Contains(disabledErr.Error(), "command is disabled") {
		t.Fatalf("disabled menu shortcut was not refused: %v", disabledErr)
	}
	isolated()
	t.Log("verified disabled background menu command is refused without activation")
	act(ActionRequest{Action: "key", Key: "backspace"}, nil)
	wait("background physical backspace", func(s fixtureState) bool { return s.Text == "后台你好 AgentDock " })
	act(ActionRequest{Action: "key", Key: "k", Modifiers: []string{"command"}}, nil)
	wait("enabled background menu shortcut", func(s fixtureState) bool { return s.Clicks == 3 })
	act(ActionRequest{Action: "set_value", ElementID: "", Text: "后台替换成功"}, field)
	wait("semantic text replacement after keyboard operations", func(s fixtureState) bool { return s.Text == "后台替换成功" })
	act(ActionRequest{Action: "move", Point: &target.Canvas}, nil)
	wait("background pointer event delivery", func(s fixtureState) bool { return s.Moves > 0 })
	act(ActionRequest{Action: "scroll", Point: &target.Canvas, DeltaY: -100}, nil)
	wait("window-bound background scroll", func(s fixtureState) bool { return s.Scrolls > 0 })
	start := Point{target.Canvas.X - 80, target.Canvas.Y}
	end := Point{target.Canvas.X + 80, target.Canvas.Y}
	act(ActionRequest{Action: "drag", Path: []Point{start, end}, DurationMS: 200}, nil)
	wait("background drag and release", func(s fixtureState) bool { return s.Drags > 0 && s.Releases > 0 })
	time.Sleep(200 * time.Millisecond)
	isolated()
	t.Logf("verified: no automation-owned global mouse events; %d external mouse moves observed; foreground app/window, first responder and sentinel keyboard/mouse/text unchanged", lastMouse.ExternalMoves-sentinel.MouseObserver.ExternalMoves)
	// 直接观察已在前台的哨兵来验证拒绝，不依赖异步激活、不改变用户焦点。
	protected, err := service.Snapshot(t.Context(), SnapshotRequest{WindowID: sentinel.WindowID, PID: sentinel.PID, Screenshot: &no})
	if err != nil {
		t.Fatal(err)
	}
	_, guardErr := service.Act(t.Context(), ActionRequest{Action: "click", ElementID: "not-observed", SnapshotID: protected["snapshot_id"].(string)})
	requireCode(t, guardErr, "TARGET_IN_USE")
	isolated()
	t.Log("verified native foreground application refuses background input")
}

// 该用例只获取元数据并触发前置拒绝，Secure Input 开启时也不发送任何输入。
func TestNativeBackgroundForegroundRefusal(t *testing.T) {
	b := NewBackend()
	if !b.Supported() || !b.Permissions().Accessibility {
		t.Skip("native Accessibility permission required")
	}
	state, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var w Window
	for _, candidate := range state.Windows {
		if candidate.PID == state.FrontmostPID && candidate.Bounds.Width > 0 && candidate.Bounds.Height > 0 {
			w = candidate
			break
		}
	}
	if w.ID == 0 {
		t.Skip("no foreground application window available")
	}
	s := New(true, b)
	defer s.Close()
	no := false
	snapshot, err := s.Snapshot(t.Context(), SnapshotRequest{WindowID: w.ID, PID: w.PID, Screenshot: &no})
	if err != nil {
		t.Fatal(err)
	}
	// 快照未观察任何 AX 元素。即便前台拒绝逻辑回归，未知元素也不能发送输入。
	_, err = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "not-observed", SnapshotID: snapshot["snapshot_id"].(string)})
	requireCode(t, err, "TARGET_IN_USE")
	t.Log("verified foreground refusal with a metadata-only native snapshot; no input dispatched")
	if b.Permissions().SecureInput {
		snapshot, err = s.Snapshot(t.Context(), SnapshotRequest{WindowID: w.ID, PID: w.PID, Screenshot: &no})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Act(t.Context(), ActionRequest{Action: "type", Text: "must-not-be-dispatched", SnapshotID: snapshot["snapshot_id"].(string)})
		requireCode(t, err, "SECURE_INPUT")
		t.Log("verified active Secure Input refuses text before dispatch")
	}
}
