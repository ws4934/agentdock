package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/uvwt/agentdock/internal/desktopcontrol"
	desktop "github.com/uvwt/agentdock/internal/tool/desktop"
)

// RequireDesktopMonitor 只由宿主入口启用；嵌入式测试可使用无 UI 的独立 Runtime。
// 正式 CLI/HTTP 服务不提供远程关闭监视器或恢复停止状态的 MCP 方法。
func (r *Runtime) RequireDesktopMonitor() { r.desktop.RequireMonitor(); r.desktop.RequireTaskScope() }
func (r *Runtime) LocalDesktopControl(ctx context.Context, request desktopcontrol.Request) (any, error) {
	if request.Method == "computeruse.permissions" {
		return r.localDesktopPermissions(ctx, request.Params)
	}
	if request.Method != "computeruse.poll" && request.Method != "computeruse.command" {
		return nil, fmt.Errorf("unsupported local Computer Use method")
	}
	var params desktop.ControlRequest
	decoder := json.NewDecoder(bytes.NewReader(request.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, fmt.Errorf("invalid local Computer Use parameters: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing local parameters")
	}
	return r.desktop.LocalControl(params, request.Method == "computeruse.poll")
}
