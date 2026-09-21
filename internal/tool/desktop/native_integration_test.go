//go:build darwin && cgo && desktop_integration

package desktop

import (
	"bytes"
	"encoding/base64"
	"github.com/uvwt/agentdock/internal/tool/core"
	"image/jpeg"
	"os"
	"testing"
	"time"
)

// 显式 tag 的真实主机只读测试；不请求或绕过系统授权，不发送任何输入事件。
func TestNativeReadOnlyDesktop(t *testing.T) {
	b := NewBackend()
	p := b.Permissions()
	t.Logf("supported=%v screen_recording=%v accessibility=%v secure_input=%v", b.Supported(), p.ScreenRecording, p.Accessibility, p.SecureInput)
	if !b.Supported() {
		t.Skip("requires macOS 14+")
	}
	state, err := b.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("displays=%d windows=%d applications=%d foreground_available=%v", len(state.Displays), len(state.Windows), len(state.Applications), state.FrontmostPID > 0)
	if len(state.Displays) == 0 || state.FrontmostPID <= 0 {
		t.Skip("no interactive desktop session")
	}
	if !p.ScreenRecording {
		t.Skip("Screen Recording not granted to test host; no prompt requested")
	}
	service := New(true, b)
	var result core.Result
	for attempt := 0; attempt < 3; attempt++ {
		result, err = service.Snapshot(t.Context(), SnapshotRequest{Accessibility: p.Accessibility, MaxNodes: 80, MaxDepth: 6})
		if err == nil {
			break
		}
		if typed, ok := err.(*core.ToolError); !ok || typed.Code != "STALE_SNAPSHOT" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(result["_mcp_image_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() < 1 || img.Bounds().Dy() < 1 {
		t.Fatal("empty screenshot")
	}
	t.Logf("jpeg=%dx%d bytes=%d elements=%d truncated=%v", img.Bounds().Dx(), img.Bounds().Dy(), len(data), len(result["elements"].([]Element)), result["tree_truncated"])
	// 只有显式指定本地验收路径时才保存，生产工具从不落盘。
	if path := os.Getenv("AGENTDOCK_DESKTOP_TEST_IMAGE"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
