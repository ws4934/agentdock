package desktop

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/tool/core"
)

type SequenceCondition struct {
	Condition string          `json:"condition"`
	Element   ElementSelector `json:"element"`
	TimeoutMS *int            `json:"timeout_ms,omitempty"`
}
type SequenceStep struct {
	Action  string             `json:"action"`
	Element ElementSelector    `json:"element"`
	Text    *string            `json:"text,omitempty"`
	After   *SequenceCondition `json:"after,omitempty"`
}
type SequenceRequest struct {
	TaskID     string         `json:"task_id,omitempty"`
	SnapshotID string         `json:"snapshot_id"`
	Steps      []SequenceStep `json:"steps"`
	TimeoutMS  int            `json:"timeout_ms,omitempty"`
}
type SequenceStepResult struct {
	Index                 int    `json:"index"`
	Action                string `json:"action"`
	EventDispatched       bool   `json:"event_dispatched"`
	PostconditionVerified bool   `json:"postcondition_verified"`
}

func sequenceSelector(selector ElementSelector) (ElementSelector, error) {
	if selector.Role == "" || (selector.Title == nil || *selector.Title == "") && (selector.Description == nil || *selector.Description == "") {
		return selector, invalid("sequence selectors require an exact role and nonempty title or description")
	}
	_, err := normalizeWait(WaitRequest{PID: 1, WindowID: 1, Condition: "element_exists", Element: &selector})
	if selector.Title != nil {
		v := *selector.Title
		selector.Title = &v
	}
	if selector.Description != nil {
		v := *selector.Description
		selector.Description = &v
	}
	return selector, err
}

func normalizeSequence(r SequenceRequest) (SequenceRequest, error) {
	if _, err := hex.DecodeString(r.SnapshotID); err != nil || len(r.SnapshotID) != 32 || r.SnapshotID != strings.ToLower(r.SnapshotID) {
		return r, invalid("sequence requires a fresh snapshot_id")
	}
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 10000
	}
	if r.TimeoutMS < 100 || r.TimeoutMS > 30000 || len(r.Steps) == 0 || len(r.Steps) > 16 {
		return r, invalid("sequence requires 1..16 steps and a 100..30000 ms budget")
	}
	// 在任何输入前校验整份计划，并复制指针，执行期间不接受调用方改写计划。
	r.Steps = append([]SequenceStep(nil), r.Steps...)
	for i := range r.Steps {
		step := &r.Steps[i]
		var err error
		step.Element, err = sequenceSelector(step.Element)
		if err != nil {
			return r, err
		}
		switch step.Action {
		case "click":
			if step.Text != nil || step.Element.Role != "AXButton" {
				return r, invalid("sequence click requires AXButton and no text")
			}
		case "set_value":
			if step.Text == nil || (step.Element.Role != "AXTextField" && step.Element.Role != "AXTextArea") || !utf8.ValidString(*step.Text) || utf8.RuneCountInString(*step.Text) > 4096 || strings.ContainsRune(*step.Text, 0) {
				return r, invalid("sequence set_value requires a text field/area and bounded UTF-8 text")
			}
			v := *step.Text
			step.Text = &v
		default:
			return r, invalid("sequence only supports click and set_value")
		}
		if step.After != nil {
			after := *step.After
			after.Element, err = sequenceSelector(after.Element)
			if err != nil {
				return r, err
			}
			switch after.Condition {
			case "element_exists", "element_absent", "element_enabled", "element_disabled":
			default:
				return r, invalid("unsupported sequence postcondition")
			}
			timeout := 2000
			if after.TimeoutMS != nil {
				timeout = *after.TimeoutMS
			}
			if timeout < 0 || timeout > 5000 {
				return r, invalid("postcondition timeout must be 0..5000 ms")
			}
			after.TimeoutMS = &timeout
			step.After = &after
		}
	}
	return r, nil
}

func sequenceError(code string) error {
	return core.NewError(code, "Sequence interrupted; observe before deciding what to do next", "desktop")
}

// 一次请求内有界执行；共享原有授权、后台输入和取消链路，不经 shell 或全局输入。
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
	ctx, release, err := s.controlled(ctx, "sequence")
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
	if binding.mode != "background" || binding.window.ID == 0 || !binding.options.Accessibility {
		return nil, invalid("sequence requires a selected background window snapshot with accessibility:true")
	}
	s.invalidate()
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.TimeoutMS)*time.Millisecond)
	defer cancel()
	defer s.control.sequenceProgress(ctx, 0, 0)
	stepMode := s.control.status().StepMode
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
	current, err := s.sequenceObservation(ctx, binding, false)
	if err != nil {
		return interrupt(1, "before", err)
	}
	for i, step := range r.Steps {
		s.control.sequenceProgress(ctx, i+1, len(r.Steps))
		reports = append(reports, SequenceStepResult{Index: i + 1, Action: step.Action})
		element, err := sequenceTarget(current, step)
		if err != nil {
			return interrupt(i+1, "selector", err)
		}
		if err = s.sequenceGuard(ctx, binding); err != nil {
			return interrupt(i+1, "before", err)
		}
		s.mu.Lock()
		observed := s.latest
		s.mu.Unlock()
		if observed == nil || observed.epoch != binding.epoch || observed.id != current["snapshot_id"] || s.now().Sub(observed.at) > snapshotTTL {
			return interrupt(i+1, "before", stale("Sequence observation expired or was invalidated"))
		}
		action := ActionRequest{Action: step.Action, ElementID: element.ID}
		if step.Text != nil {
			action.Text = *step.Text
		}
		actionCtx := context.WithValue(ctx, controlActivityKey{}, step.Action)
		s.control.started(actionCtx)
		if _, err = s.actBackground(actionCtx, observed, action); err != nil {
			return interrupt(i+1, "input", err)
		}
		dispatched++
		reports[i].EventDispatched = true
		postDeadline := time.Time{}
		if step.After != nil {
			postDeadline = time.Now().Add(time.Duration(*step.After.TimeoutMS) * time.Millisecond)
		}
		current, err = s.sequenceObservation(ctx, binding, false)
		if err != nil {
			return interrupt(i+1, "after", err)
		}
		if step.After != nil {
			current, err = s.sequenceWait(ctx, binding, current, *step.After, postDeadline)
			if err != nil {
				return interrupt(i+1, "postcondition", err)
			}
			reports[i].PostconditionVerified = true
		}
		completed++
		if stepMode {
			break
		} // 本机下一步绝不被解释为批准整份计划。
	}
	// 中间仅 AX 观察，最后按原快照设置返回一幅图像，不为每一步编码/传输截图。
	if binding.options.Screenshot == nil || *binding.options.Screenshot {
		current, err = s.sequenceObservation(ctx, binding, true)
		if err != nil {
			return interrupt(0, "final_observation", err)
		}
	}
	if !s.control.valid(ctx, binding.epoch) {
		return interrupt(0, "final_observation", sequenceError("DESKTOP_CONTROL_BLOCKED"))
	}
	result["outcome"], result["observation_status"], result["next_required_action"] = "completed", "captured", "review_observation"
	if stepMode {
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
