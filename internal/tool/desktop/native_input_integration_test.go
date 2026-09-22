//go:build darwin && cgo && desktop_integration

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

type fixtureMouseState struct {
	TapActive         bool  `json:"tap_active"`
	OwnedGlobalEvents int64 `json:"owned_global_events"`
	ExternalMoves     int64 `json:"external_moves"`
	Cursor            Point `json:"cursor"`
}
type fixtureState struct {
	Downs             int               `json:"downs"`
	Moves             int               `json:"moves"`
	MenuEnabled       bool              `json:"menu_enabled"`
	SelectionLength   int               `json:"selection_length"`
	LastKeyCode       int               `json:"last_key_code"`
	LastKeyLength     int               `json:"last_key_length"`
	LastKeyFlags      uint64            `json:"last_key_flags"`
	LastEventWindow   int               `json:"last_event_window"`
	LastEventLocation Point             `json:"last_event_location"`
	MouseObserver     fixtureMouseState `json:"mouse_observer"`
	WindowID          uint32            `json:"window_id"`
	Active            bool              `json:"active"`
	KeyWindow         bool              `json:"key_window"`
	Responder         string            `json:"responder"`
	KeyEvents         int               `json:"key_events"`
	MouseEvents       int               `json:"mouse_events"`
	PID               int               `json:"pid"`
	Clicks            int               `json:"clicks"`
	Scrolls           int               `json:"scrolls"`
	Drags             int               `json:"drags"`
	Releases          int               `json:"releases"`
	Text              string            `json:"text"`
	Field             Point             `json:"field"`
	Button            Point             `json:"button"`
	Canvas            Point             `json:"canvas"`
}

// 需要显式设置环境变量，默认 CI 和普通单测绝不发送桌面事件。
func TestNativeInputIsolatedFixture(t *testing.T) {
	if os.Getenv("AGENTDOCK_DESKTOP_INPUT_TEST") != "1" {
		t.Skip("set AGENTDOCK_DESKTOP_INPUT_TEST=1 to run isolated GUI input verification")
	}
	b := NewBackend()
	p := b.Permissions()
	if !b.Supported() || !p.Accessibility || !p.ScreenRecording || p.SecureInput {
		t.Skipf("requires native support, granted permissions and inactive Secure Input: %+v", p)
	}
	before, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "AgentDockCUFixture")
	statePath := filepath.Join(root, "state.json")
	compile := exec.CommandContext(t.Context(), "xcrun", "clang", "-fobjc-arc", "-framework", "Cocoa", "-framework", "ApplicationServices", "testdata/fixture.m", "-o", binary)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v\n%s", err, output)
	}
	cmd := exec.Command(binary, statePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 只终止本次创建的测试进程；不接触其他应用或用户文档。
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if before.FrontmostPID > 0 {
			_ = b.Activate(ctx, before.FrontmostPID)
		}
	})
	var state fixtureState
	wait := func(label string, condition func(fixtureState) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, e := os.ReadFile(statePath)
			if e == nil && json.Unmarshal(data, &state) == nil && condition(state) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("fixture failed verification: %s (clicks=%d scrolls=%d drags=%d releases=%d text_runes=%d)", label, state.Clicks, state.Scrolls, state.Drags, state.Releases, len([]rune(state.Text)))
	}
	wait("launch and self-activation", func(s fixtureState) bool {
		current, e := b.State(t.Context())
		return e == nil && s.PID > 0 && s.Active && s.KeyWindow && current.FrontmostPID == s.PID
	})
	service := New(true, b)
	t.Cleanup(func() { _ = service.Close() })
	noImage := false
	// 启动期间窗口几何/焦点会短暂变化。只对“尚未派发”的 STALE_SNAPSHOT 重新观察；
	// 任何已进入原生动作后的错误都立即失败，不重放可能已发送的输入。
	activated := false
	for attempt := 0; attempt < 5; attempt++ {
		activation, e := service.Snapshot(t.Context(), SnapshotRequest{Mode: "foreground", Screenshot: &noImage})
		if e == nil {
			if activation["state"].(State).FrontmostPID != state.PID {
				t.Fatal("fixture lost foreground during activation preflight")
			}
			_, e = service.Act(t.Context(), ActionRequest{Action: "activate", SnapshotID: activation["snapshot_id"].(string), PID: state.PID})
			if e == nil {
				activated = true
				break
			}
			if typed, ok := e.(*core.ToolError); !ok || typed.Code != "STALE_SNAPSHOT" {
				t.Fatal(e)
			}
			current, _ := b.State(t.Context())
			observed := activation["state"].(State)
			previousWindow, _ := targetWindow(observed)
			currentWindow, _ := targetWindow(current)
			t.Logf("un-dispatched activation invalidated: observed_pid=%d current_pid=%d observed_window=%d current_window=%d", observed.FrontmostPID, current.FrontmostPID, previousWindow.ID, currentWindow.ID)
		} else if typed, ok := e.(*core.ToolError); !ok || typed.Code != "STALE_SNAPSHOT" {
			t.Fatal(e)
		}
		time.Sleep(120 * time.Millisecond)
	}
	if !activated {
		t.Fatal("fixture activation could not obtain stable preflight; no input retry was performed")
	}
	time.Sleep(300 * time.Millisecond)
	snapshot := func() core.Result {
		t.Helper()
		for attempt := 0; attempt < 3; attempt++ {
			result, e := service.Snapshot(t.Context(), SnapshotRequest{Mode: "foreground", Accessibility: true, MaxNodes: 150, MaxDepth: 8})
			if e == nil {
				if result["state"].(State).FrontmostPID != state.PID {
					t.Fatal("test fixture lost foreground; refusing input into another application")
				}
				return result
			}
			if typed, ok := e.(*core.ToolError); !ok || typed.Code != "STALE_SNAPSHOT" {
				t.Fatal(e)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("could not obtain stable fixture snapshot")
		return nil
	}
	act := func(a ActionRequest) {
		t.Helper()
		r := snapshot()
		a.SnapshotID = r["snapshot_id"].(string)
		if _, err := service.Act(t.Context(), a); err != nil {
			t.Fatalf("%s: %v", a.Action, err)
		}
	}
	act(ActionRequest{Action: "click", Point: &state.Button})
	wait("coordinate click", func(s fixtureState) bool { return s.Clicks == 1 })
	result := snapshot()
	elementID := ""
	for _, e := range result["elements"].([]Element) {
		if e.Title == "Test Click" && e.Pressable {
			elementID = e.ID
			break
		}
	}
	if elementID == "" {
		t.Fatal("fixture button is missing from AX snapshot")
	}
	if _, err := service.Act(t.Context(), ActionRequest{Action: "click", SnapshotID: result["snapshot_id"].(string), ElementID: elementID}); err != nil {
		t.Fatal(err)
	}
	wait("AXPress click", func(s fixtureState) bool { return s.Clicks == 2 })
	act(ActionRequest{Action: "click", Point: &state.Field})
	act(ActionRequest{Action: "type", Text: "你好 AgentDock 🚀"})
	wait("Unicode text", func(s fixtureState) bool { return s.Text == "你好 AgentDock 🚀" })
	act(ActionRequest{Action: "key", Key: "a", Modifiers: []string{"command"}})
	act(ActionRequest{Action: "type", Text: "替换成功"})
	wait("shortcut select-all and replace", func(s fixtureState) bool { return s.Text == "替换成功" })
	act(ActionRequest{Action: "move", Point: &state.Canvas})
	act(ActionRequest{Action: "scroll", DeltaY: -100})
	wait("scroll", func(s fixtureState) bool { return s.Scrolls > 0 })
	start := Point{X: state.Canvas.X - 80, Y: state.Canvas.Y}
	end := Point{X: state.Canvas.X + 80, Y: state.Canvas.Y}
	act(ActionRequest{Action: "drag", Path: []Point{start, end}, DurationMS: 200})
	wait("drag release", func(s fixtureState) bool { return s.Drags > 0 && s.Releases > 0 })
	t.Log("verified isolated native screenshot, coordinate click, AXPress, Unicode, Command+A, move, scroll, drag and release")
}
