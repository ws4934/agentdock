package desktop

import (
	"context"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

func (m *controlSession) sequenceProgress(ctx context.Context, step, total int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view.Epoch == m.epoch(ctx) {
		m.view.SequenceStep, m.view.SequenceTotal = step, total
	}
}

func (s *Service) sequenceGuard(ctx context.Context, binding *observed) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.control.valid(ctx, binding.epoch) {
		return sequenceError("DESKTOP_CONTROL_BLOCKED")
	}
	if !s.backend.Permissions().Accessibility {
		return permissionError("accessibility")
	}
	return nil
}

func (s *Service) sequenceObservation(ctx context.Context, binding *observed, screenshot, accessibility bool) (core.Result, error) {
	if err := s.sequenceGuard(ctx, binding); err != nil {
		return nil, err
	}
	r := binding.options
	r.PID, r.WindowID, r.Mode, r.Screenshot = binding.window.PID, binding.window.ID, "background", &screenshot
	r.Accessibility = accessibility
	result, err := s.snapshotBackgroundLocked(ctx, r)
	if err != nil {
		return nil, err
	}
	if err = s.sequenceGuard(ctx, binding); err != nil {
		return nil, err
	}
	// 复用快照已经读取的窗口状态，不再在每步前后重复枚举整台桌面。
	state := result["state"].(State)
	if !sameBackgroundTarget(binding, state) {
		return nil, sequenceError("SEQUENCE_CONTEXT_CHANGED")
	}
	if state.FrontmostPID == binding.window.PID {
		return nil, sequenceError("TARGET_IN_USE")
	}
	// 标题更新、同应用其他窗口及普通弹层不等于目标已丢失。
	// 控件身份由下一次 AX 定位验证；坐标输入由既有窗口绑定桥再次校验。
	s.mu.Lock()
	if s.latest != nil {
		// 中间不截图，但 image 坐标继续使用初始图像的精确比例。
		if !screenshot {
			s.latest.image = binding.image
		}
		s.latest.options = binding.options
	}
	s.mu.Unlock()
	return result, nil
}

func sequenceMatches(observation core.Result, selector ElementSelector) (Element, int) {
	var match Element
	count := 0
	for _, e := range observation["elements"].([]Element) {
		if matchesElement(e, selector) {
			match = e
			count++
		}
	}
	return match, count
}

func sequenceTarget(observation core.Result, step SequenceStep) (Element, error) {
	if observation["tree_truncated"] == true {
		return Element{}, sequenceError("SEQUENCE_TREE_INCOMPLETE")
	}
	e, count := sequenceMatches(observation, step.Element)
	if count == 0 {
		return e, sequenceError("SEQUENCE_ELEMENT_MISSING")
	}
	if count != 1 {
		return e, sequenceError("SEQUENCE_ELEMENT_AMBIGUOUS")
	}
	if !e.EnabledKnown || !e.Enabled || step.Action == "click" && !e.Pressable || step.Action == "set_value" && !e.ValueSettable {
		return e, sequenceError("SEQUENCE_ELEMENT_UNAVAILABLE")
	}
	return e, nil
}

func sequenceDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func (s *Service) sequenceWait(ctx context.Context, binding *observed, current core.Result, condition SequenceCondition, deadline time.Time) (core.Result, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if *condition.TimeoutMS > 0 && !time.Now().Before(deadline) {
			return nil, sequenceError("SEQUENCE_CONDITION_TIMEOUT")
		}
		// 只有依赖唯一匹配或证明不存在的语义操作才要求树完整。
		if current["tree_truncated"] == true {
			return nil, sequenceError("SEQUENCE_TREE_INCOMPLETE")
		}
		e, count := sequenceMatches(current, condition.Element)
		if count > 1 {
			return nil, sequenceError("SEQUENCE_ELEMENT_AMBIGUOUS")
		}
		met := condition.Condition == "element_absent" && count == 0
		if count == 1 {
			switch condition.Condition {
			case "element_exists":
				met = true
			case "element_enabled", "element_disabled":
				if !e.EnabledKnown {
					return nil, sequenceError("SEQUENCE_ELEMENT_STATE_UNKNOWN")
				}
				met = e.Enabled == (condition.Condition == "element_enabled")
			}
		}
		if met {
			return current, nil
		}
		if !time.Now().Before(deadline) {
			return nil, sequenceError("SEQUENCE_CONDITION_TIMEOUT")
		}
		if err := sequenceDelay(ctx, min(100*time.Millisecond, time.Until(deadline))); err != nil {
			return nil, err
		}
		var err error
		current, err = s.sequenceObservation(ctx, binding, false, true)
		if err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, sequenceError("SEQUENCE_CONDITION_TIMEOUT")
		}
	}
}
