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

type fixtureState struct {
	PID      int    `json:"pid"`
	Clicks   int    `json:"clicks"`
	Scrolls  int    `json:"scrolls"`
	Drags    int    `json:"drags"`
	Releases int    `json:"releases"`
	Text     string `json:"text"`
	Field    Point  `json:"field"`
	Button   Point  `json:"button"`
	Canvas   Point  `json:"canvas"`
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
	compile := exec.CommandContext(t.Context(), "xcrun", "clang", "-fobjc-arc", "-framework", "Cocoa", "testdata/fixture.m", "-o", binary)
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
	wait("launch", func(s fixtureState) bool { return s.PID > 0 })
	service := New(true, b)
	t.Cleanup(func() { _ = service.Close() })
	noImage := false
	var activation core.Result
	for attempt := 0; attempt < 5; attempt++ {
		activation, err = service.Snapshot(t.Context(), SnapshotRequest{Screenshot: &noImage})
		if err == nil {
			break
		}
		if typed, ok := err.(*core.ToolError); !ok || typed.Code != "STALE_SNAPSHOT" {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Act(t.Context(), ActionRequest{Action: "activate", SnapshotID: activation["snapshot_id"].(string), PID: state.PID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	snapshot := func() core.Result {
		t.Helper()
		for attempt := 0; attempt < 3; attempt++ {
			result, e := service.Snapshot(t.Context(), SnapshotRequest{Accessibility: true, MaxNodes: 150, MaxDepth: 8})
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
