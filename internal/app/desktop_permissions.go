package app

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

// 仅查询正在提供本地socket的Core本身，不启动辅助进程、不申请权限、也不续租控制会话。
func (r *Runtime) localDesktopPermissions(ctx context.Context, raw json.RawMessage) (Result, error) {
	if len(raw) > 0 {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(raw, &params); err != nil || params == nil || len(params) != 0 {
			return nil, toolErrorDetails("INVALID_ARGUMENT", "permission diagnostics accept only an empty object", "validation", nil)
		}
	}
	status, err := r.desktop.Status(ctx)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return Result{
		"process_id": os.Getpid(), "executable_path": executable, "build": buildinfo.Current(),
		"checked_at": time.Now().UTC().Format(time.RFC3339Nano),
		"enabled":    status["enabled"], "supported": status["supported"], "permissions": status["permissions"],
	}, nil
}
