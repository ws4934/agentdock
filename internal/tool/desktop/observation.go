package desktop

import (
	"context"
	"errors"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// observeAfter 只执行一次只读观察，不重放输入、不更换目标、不获取新的控制代次。
// 调用方持有 desktopGate；单步许可直到观察结束才释放。
func (s *Service) observeAfter(ctx context.Context, before *observed, result core.Result) {
	r := before.options
	r.Mode, r.WindowID, r.PID = "background", before.window.ID, before.window.PID
	var observation core.Result
	var err error
	if !s.control.valid(ctx, before.epoch) {
		err = controlError("DESKTOP_CONTROL_BLOCKED", "Control changed before post-action observation")
	} else {
		observation, err = s.snapshotBackgroundLocked(ctx, r)
	}
	if err == nil && !s.control.valid(ctx, before.epoch) {
		err = controlError("DESKTOP_CONTROL_BLOCKED", "Control changed during post-action observation")
	}
	if err != nil {
		s.invalidate()
		code := "OBSERVATION_FAILED"
		var toolError *core.ToolError
		if errors.As(err, &toolError) {
			code = toolError.Code
		} else if errors.Is(err, context.Canceled) {
			code = "CANCELLED"
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "DEADLINE_EXCEEDED"
		}
		// 输入成功与观察失败分别报告，防止客户端因工具错误而重复执行已完成的点击。
		result["observation_status"] = "unavailable"
		result["observation_error"] = map[string]any{"code": code, "input_already_dispatched": true, "retry_input": false}
		result["next_required_action"] = "desktop_snapshot; respect local pause/stop and never replay the input"
		return
	}
	result["observation_status"] = "captured"
	result["next_required_action"] = "review_observation"
	if s.control.status().StepMode {
		// 单步完成会立即暂停；观察证据可以返回，但不能提供貌似可用的下一步令牌。
		s.invalidate()
		observation["snapshot_id"] = ""
		observation["expires_in_ms"] = 0
		observation["observation_only"] = true
		result["next_required_action"] = "local_resume_then_desktop_snapshot"
	}
	for _, key := range []string{"_mcp_image_base64", "_mcp_image_mime_type"} {
		if value, ok := observation[key]; ok {
			result[key] = value
			delete(observation, key)
		}
	}
	result["observation"] = observation
}
