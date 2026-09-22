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
	p := s.backend.Permissions()
	if !p.Accessibility {
		return permissionError("accessibility")
	}
	if p.SecureInput {
		return sequenceError("SECURE_INPUT")
	}
	state, err := s.backend.State(ctx)
	if err != nil {
		return err
	}
	if state.WindowsTruncated || !sameBackgroundTarget(binding, state) {
		return sequenceError("SEQUENCE_CONTEXT_CHANGED")
	}
	if state.FrontmostPID == binding.window.PID {
		return sequenceError("TARGET_IN_USE")
	}
	// 新窗口/模态界面或标题变化需要重新判断；不能对旧计划继续盲点。
	old := map[uint32]string{}
	for _, w := range binding.state.Windows {
		if w.PID == binding.window.PID {
			old[w.ID] = w.Title
		}
	}
	count := 0
	for _, w := range state.Windows {
		if w.PID != binding.window.PID {
			continue
		}
		title, exists := old[w.ID]
		if !exists || title != w.Title {
			return sequenceError("SEQUENCE_CONTEXT_CHANGED")
		}
		count++
	}
	if count != len(old) {
		return sequenceError("SEQUENCE_CONTEXT_CHANGED")
	}
	return nil
}

func (s *Service) sequenceObservation(ctx context.Context, binding *observed, screenshot bool) (core.Result, error) {
	if err := s.sequenceGuard(ctx, binding); err != nil {
		return nil, err
	}
	r := binding.options
	r.PID, r.WindowID, r.Mode, r.Screenshot = binding.window.PID, binding.window.ID, "background", &screenshot
	result, err := s.snapshotBackgroundLocked(ctx, r)
	if err != nil {
		return nil, err
	}
	if err = s.sequenceGuard(ctx, binding); err != nil {
		return nil, err
	}
	if result["tree_truncated"] == true {
		return nil, sequenceError("SEQUENCE_TREE_INCOMPLETE")
	}
	for _, e := range result["elements"].([]Element) {
		if e.Role == "AXSheet" || e.Role == "AXDialog" {
			return nil, sequenceError("SEQUENCE_CONTEXT_CHANGED")
		}
	}
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

func (s *Service) sequenceWait(ctx context.Context, binding *observed, current core.Result, condition SequenceCondition, deadline time.Time) (core.Result, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if *condition.TimeoutMS > 0 && !time.Now().Before(deadline) {
			return nil, sequenceError("SEQUENCE_CONDITION_TIMEOUT")
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
		timer := time.NewTimer(min(100*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		var err error
		current, err = s.sequenceObservation(ctx, binding, false)
		if err != nil {
			return nil, err
		}
		// 原生观察可能晚于谓词预算返回，不能将过期样本认作验证通过。
		if !time.Now().Before(deadline) {
			return nil, sequenceError("SEQUENCE_CONDITION_TIMEOUT")
		}
	}
}
