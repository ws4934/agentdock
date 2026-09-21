//go:build darwin && cgo && desktop_integration

package mcp

import (
	"bytes"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/config"
	desktop "github.com/uvwt/agentdock/internal/tool/desktop"
)

// 同时验证原生截图、Runtime 契约和 SDK transport，绝不请求权限或发送输入。
func TestNativeDesktopMCPImage(t *testing.T) {
	backend := desktop.NewBackend()
	p := backend.Permissions()
	if !backend.Supported() || !p.ScreenRecording {
		t.Skip("native capture permission not available")
	}
	root := t.TempDir()
	harness := newMCPAppTestHarness(t, config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: root, DesktopEnabled: true})
	var result *mcpsdk.CallToolResult
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		result, err = harness.session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: desktop.ToolSnapshot, Arguments: map[string]any{"mode": "foreground", "max_dimension": 1024}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			break
		}
		data, ok := result.StructuredContent.(map[string]any)
		if !ok || data["code"] != "STALE_SNAPSHOT" {
			t.Fatalf("native snapshot failed: %#v", result.StructuredContent)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if result.IsError {
		t.Fatal("unstable desktop prevented capture")
	}
	gotText, gotImage := false, false
	for _, item := range result.Content {
		switch value := item.(type) {
		case *mcpsdk.TextContent:
			gotText = value.Text != ""
		case *mcpsdk.ImageContent:
			if value.MIMEType != "image/jpeg" {
				t.Fatal(value.MIMEType)
			}
			image, err := jpeg.Decode(bytes.NewReader(value.Data))
			if err != nil {
				t.Fatal(err)
			}
			gotImage = image.Bounds().Dx() > 0
		}
	}
	if !gotText || !gotImage {
		t.Fatal("MCP result must carry both usable coordinate metadata and a JPEG")
	}
	t.Log("native JPEG and snapshot metadata verified through MCP SDK session")
}
