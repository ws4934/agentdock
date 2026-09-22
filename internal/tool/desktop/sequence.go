package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

func sequenceError(code string) error {
	return core.NewError(code, "Sequence interrupted; observe before deciding what to do next", "desktop")
}

// 动作流沿用原有后台输入链路。只有语义定位或显式条件需要 AX，通常只在结束时截图。
func (s *Service) Sequence(ctx context.Context, r SequenceRequest) (core.Result, error) {
	var err error
	r, err = normalizeSequence(r)
	if err != nil {
		return nil, err
	}
	ctx = taskContext(ctx, r.TaskID)
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = s.ready(); err != nil {
		return nil, err
	}
	activity := "wait"
	for _, step := range r.Steps {
		if step.Action != "wait" {
			activity = "sequence"
			break
		}
	}
	ctx, release, err := s.controlled(ctx, activity)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = lockDesktop(ctx); err != nil {
		return nil, err
	}
	defer unlockDesktop()
	s.control.started(ctx)
	s.mu.Lock()
	binding := s.latest
	s.mu.Unlock()
	if binding == nil || binding.id != r.SnapshotID || s.now().Sub(binding.at) > snapshotTTL || !s.control.valid(ctx, binding.epoch) {
		return nil, stale("Sequence requires a fresh, unconsumed snapshot")
	}
	if binding.mode != "background" || binding.window.ID == 0 {
		return nil, invalid("sequence requires a selected background window snapshot")
	}
	// 坐标仍来自本次观察；先校验整段，不能在执行一半后才发现后面的坐标无效。
	for _, step := range r.Steps {
		if step.Point != nil {
			if _, err = mapWindowPoint(*step.Point, step.Space, binding); err != nil {
				return nil, err
			}
		}
		for _, point := range step.Path {
			if _, err = mapWindowPoint(point, step.Space, binding); err != nil {
				return nil, err
			}
		}
	}
	s.invalidate()
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.TimeoutMS)*time.Millisecond)
	defer cancel()
	defer s.control.sequenceProgress(ctx, 0, 0)
	stepMode := s.control.status().StepMode
	singleStep := false
	reports := make([]SequenceStepResult, 0, len(r.Steps))
	dispatched, completed := 0, 0
	result := core.Result{"mode": "background", "total_steps": len(r.Steps), "snapshot_consumed": true, "foreground_fallback": false, "application_verified": false}
	completeReport := func() core.Result {
		result["steps"], result["dispatched_steps"], result["completed_steps"] = reports, dispatched, completed
		result["elapsed_ms"] = time.Since(started).Milliseconds()
		return result
	}
	interrupt := func(step int, stage string, cause error) (core.Result, error) {
		s.invalidate()
		code, partial := "SEQUENCE_FAILED", false
		var toolError *core.ToolError
		if errors.As(cause, &toolError) {
			code = toolError.Code
			partial, _ = toolError.Details["may_have_dispatched"].(bool)
		}
		if errors.Is(cause, context.Canceled) {
			code = "CANCELLED"
		}
		if errors.Is(cause, context.DeadlineExceeded) {
			code = "DEADLINE_EXCEEDED"
		}
		result["outcome"], result["failed_step"], result["failure_stage"] = "interrupted", step, stage
		result["error"] = map[string]any{"code": code, "may_have_dispatched": partial, "input_already_dispatched": dispatched > 0, "retry_input": false}
		result["observation_status"] = "unavailable"
		result["next_required_action"] = "desktop_snapshot; respect local pause/stop and never replay completed or uncertain input"
		s.control.traceOutcome(ctx, "sequence_interrupted")
		if s.control.status().CleanupFailed {
			s.control.traceOutcome(ctx, "cleanup_failed")
		}
		return completeReport(), nil
	}
	current, err := s.sequenceObservation(ctx, binding, false, hasSequenceSelector(r.Steps[0].Element))
	if err != nil {
		return interrupt(1, "before", err)
	}
	finalObserved := false
	for i, step := range r.Steps {
		s.control.sequenceProgress(ctx, i+1, len(r.Steps))
		reports = append(reports, SequenceStepResult{Index: i + 1, Action: step.Action})
		if err = s.sequenceGuard(ctx, binding); err != nil {
			return interrupt(i+1, "before", err)
		}
		actionCtx := context.WithValue(ctx, controlActivityKey{}, step.Action)
		s.control.started(actionCtx)
		if step.Action == "wait" {
			if err = sequenceDelay(ctx, time.Duration(step.DurationMS)*time.Millisecond); err != nil {
				return interrupt(i+1, "wait", err)
			}
		} else {
			action := step.actionRequest()
			if hasSequenceSelector(step.Element) {
				element, targetErr := sequenceTarget(current, step)
				if targetErr != nil {
					return interrupt(i+1, "selector", targetErr)
				}
				action.ElementID = element.ID
			}
			if (step.Action == "key" || step.Action == "type" || step.Action == "set_value") && s.backend.Permissions().SecureInput {
				return interrupt(i+1, "before", sequenceError("SECURE_INPUT"))
			}
			s.mu.Lock()
			observed := s.latest
			s.mu.Unlock()
			if observed == nil || observed.epoch != binding.epoch || observed.id != current["snapshot_id"] || s.now().Sub(observed.at) > snapshotTTL {
				return interrupt(i+1, "before", stale("Sequence observation expired or was invalidated"))
			}
			if _, err = s.actBackground(actionCtx, observed, action); err != nil {
				return interrupt(i+1, "input", err)
			}
			dispatched++
			reports[i].EventDispatched = true
			singleStep = stepMode
		}
		last := i == len(r.Steps)-1 || singleStep
		if step.After == nil {
			completed++
		}
		postDeadline := time.Time{}
		if step.After != nil {
			postDeadline = time.Now().Add(time.Duration(*step.After.TimeoutMS) * time.Millisecond)
		}
		needAX := step.After != nil
		if last {
			needAX = needAX || binding.options.Accessibility
		} else {
			needAX = needAX || hasSequenceSelector(r.Steps[i+1].Element)
		}
		// 最后一步无等待条件时，直接合并最终观察，避免多读一遍树和窗口。
		finalObserved = last && step.After == nil
		image := finalObserved && (binding.options.Screenshot == nil || *binding.options.Screenshot)
		current, err = s.sequenceObservation(ctx, binding, image, needAX)
		if err != nil {
			if finalObserved {
				// 输入已成功，最后的观察失败不应要求重放已执行的整段。
				return interrupt(0, "final_observation", err)
			}
			return interrupt(i+1, "after", err)
		}
		if step.After != nil {
			current, err = s.sequenceWait(ctx, binding, current, *step.After, postDeadline)
			if err != nil {
				return interrupt(i+1, "postcondition", err)
			}
			reports[i].PostconditionVerified = true
			completed++
		}
		if singleStep {
			break
		}
	}
	if !finalObserved {
		current, err = s.sequenceObservation(ctx, binding, binding.options.Screenshot == nil || *binding.options.Screenshot, binding.options.Accessibility)
		if err != nil {
			return interrupt(0, "final_observation", err)
		}
	}
	if err = s.sequenceGuard(ctx, binding); err != nil {
		return interrupt(0, "final_observation", err)
	}
	result["outcome"], result["observation_status"], result["next_required_action"] = "completed", "captured", "review_observation"
	if singleStep {
		s.invalidate()
		current["snapshot_id"], current["expires_in_ms"], current["observation_only"] = "", 0, true
		result["outcome"], result["next_required_action"] = "single_step", "local_resume_then_desktop_snapshot; do not replay completed input"
	}
	for _, key := range []string{"_mcp_image_base64", "_mcp_image_mime_type"} {
		if value, ok := current[key]; ok {
			result[key] = value
			delete(current, key)
		}
	}
	result["observation"] = current
	s.control.traceOutcome(ctx, "sequence_completed")
	return completeReport(), nil
}
