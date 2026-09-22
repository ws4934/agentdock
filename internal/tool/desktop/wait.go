package desktop

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// 等待只观察一个明确 PID 的窗口或控件；不发送事件、不读取 AXValue，也不生成可输入快照。
type ElementSelector struct {
	Role        string  `json:"role,omitempty"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}
type WaitRequest struct {
	TaskID      string           `json:"task_id,omitempty"`
	PID         int              `json:"pid"`
	WindowID    uint32           `json:"window_id,omitempty"`
	WindowTitle *string          `json:"window_title,omitempty"`
	Condition   string           `json:"condition"`
	Element     *ElementSelector `json:"element,omitempty"`
	TimeoutMS   *int             `json:"timeout_ms,omitempty"`
	PollMS      int              `json:"poll_ms,omitempty"`
	StableMS    int              `json:"stable_ms,omitempty"`
	MaxNodes    int              `json:"max_nodes,omitempty"`
	MaxDepth    int              `json:"max_depth,omitempty"`
}
type WaitObservation struct {
	Window   *Window `json:"window,omitempty"`
	Matches  int     `json:"matches"`
	Complete bool    `json:"complete"`
	Reason   string  `json:"reason"`
}

func normalizeWait(r WaitRequest) (WaitRequest, error) {
	if r.PID <= 0 {
		return r, invalid("wait requires an explicit application pid")
	}
	if r.TimeoutMS == nil {
		n := 10000
		r.TimeoutMS = &n
	}
	if r.PollMS == 0 {
		r.PollMS = 200
	}
	if r.StableMS == 0 {
		r.StableMS = 300
	}
	if r.MaxNodes == 0 {
		r.MaxNodes = 200
	}
	if r.MaxDepth == 0 {
		r.MaxDepth = 8
	}
	if *r.TimeoutMS < 0 || *r.TimeoutMS > 30000 || r.PollMS < 100 || r.PollMS > 1000 || r.StableMS < 100 || r.StableMS > 5000 || r.MaxNodes < 1 || r.MaxNodes > 500 || r.MaxDepth < 1 || r.MaxDepth > 16 {
		return r, invalid("wait limits out of range")
	}
	switch r.Condition {
	case "window_exists", "window_stable":
		if r.Element != nil {
			return r, invalid("window conditions do not accept an element selector")
		}
	case "window_absent":
		if r.Element != nil || r.WindowID == 0 || r.WindowTitle != nil {
			return r, invalid("window_absent requires a previously observed window_id")
		}
	case "element_exists", "element_absent", "element_enabled", "element_disabled":
		if r.Element == nil || r.WindowID == 0 {
			return r, invalid("element conditions require window_id and an exact element selector")
		}
		if r.Element.Role == "" && r.Element.Title == nil && r.Element.Description == nil {
			return r, invalid("element selector must constrain role, title or description")
		}
	default:
		return r, invalid("unknown wait condition")
	}
	labels := []string{}
	if r.WindowTitle != nil {
		labels = append(labels, *r.WindowTitle)
	}
	if r.Element != nil {
		labels = append(labels, r.Element.Role)
		if r.Element.Title != nil {
			labels = append(labels, *r.Element.Title)
		}
		if r.Element.Description != nil {
			labels = append(labels, *r.Element.Description)
		}
	}
	for _, v := range labels {
		if !utf8.ValidString(v) || len(v) > 1024 || strings.ContainsRune(v, 0) {
			return r, invalid("wait selectors must be bounded UTF-8 labels")
		}
	}
	return r, nil
}
func matchesElement(e Element, s ElementSelector) bool {
	return (s.Role == "" || e.Role == s.Role) && (s.Title == nil || e.Title == *s.Title) && (s.Description == nil || e.Description == *s.Description)
}
func (s *Service) waitObservation(ctx context.Context, r WaitRequest) (WaitObservation, bool, error) {
	if err := lockDesktop(ctx); err != nil {
		return WaitObservation{}, false, err
	}
	defer unlockDesktop()
	s.control.started(ctx)
	state, err := s.backend.State(ctx)
	if err != nil {
		return WaitObservation{}, false, err
	}
	if err := s.authorizeWindow(ctx, state, Window{PID: r.PID}, "background"); err != nil {
		return WaitObservation{}, false, err
	}
	matches := []Window{}
	for _, w := range state.Windows {
		if r.WindowID != 0 && w.ID == r.WindowID && w.PID != r.PID {
			return WaitObservation{}, false, stale("wait window changed owner")
		}
		if w.PID == r.PID && w.ID > 0 && w.Bounds.Width > 0 && w.Bounds.Height > 0 && (r.WindowID == 0 || w.ID == r.WindowID) && (r.WindowTitle == nil || w.Title == *r.WindowTitle) {
			matches = append(matches, w)
		}
	}
	o := WaitObservation{Matches: len(matches), Complete: true, Reason: "window_missing"}
	if state.WindowsTruncated && (len(matches) == 0 || r.WindowID == 0) {
		o.Complete = false
		o.Reason = "window_list_incomplete"
		return o, false, nil
	}
	if r.Condition == "window_absent" {
		return o, len(matches) == 0, nil
	}
	if len(matches) != 1 {
		if len(matches) > 1 {
			o.Reason = "ambiguous_windows"
		}
		return o, false, nil
	}
	w := matches[0]
	o.Window = &w
	o.Reason = "window_observed"
	s.recordTarget(ctx, state, w, "background")
	if r.Element == nil {
		return o, true, nil
	}
	backend, ok := s.backend.(WindowBackend)
	if !ok {
		return o, false, backgroundUnsupported()
	}
	tree, err := backend.WindowTree(ctx, w, r.MaxNodes, r.MaxDepth)
	if err != nil {
		return o, false, err
	}
	after, err := s.backend.State(ctx)
	if err != nil {
		return o, false, err
	}
	current, ok := windowByID(after, w.ID)
	if !ok || current.PID != w.PID || current.Bounds != w.Bounds {
		return WaitObservation{Reason: "window_changed", Complete: false}, false, nil
	}
	count := 0
	var element Element
	for _, e := range tree.Elements {
		if matchesElement(e, *r.Element) {
			count++
			element = e
		}
	}
	o.Matches = count
	o.Complete = !tree.Truncated
	if tree.Truncated {
		o.Reason = "accessibility_tree_incomplete"
		return o, false, nil
	}
	if r.Condition == "element_absent" {
		o.Reason = "element_absence_checked"
		return o, count == 0, nil
	}
	if count != 1 {
		o.Reason = "element_missing"
		if count > 1 {
			o.Reason = "ambiguous_elements"
		}
		return o, false, nil
	}
	o.Reason = "element_observed"
	if (r.Condition == "element_enabled" || r.Condition == "element_disabled") && !element.EnabledKnown {
		o.Complete = false
		o.Reason = "element_enabled_state_unknown"
		return o, false, nil
	}
	switch r.Condition {
	case "element_enabled":
		return o, element.Enabled, nil
	case "element_disabled":
		return o, !element.Enabled, nil
	}
	return o, true, nil
}
func (s *Service) Wait(ctx context.Context, r WaitRequest) (core.Result, error) {
	ctx = taskContext(ctx, r.TaskID)
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = s.ready(); err != nil {
		return nil, err
	}
	r, err = normalizeWait(r)
	if err != nil {
		return nil, err
	}
	if r.Element != nil && !s.backend.Permissions().Accessibility {
		return nil, permissionError("accessibility")
	}
	ctx, release, err := s.controlled(ctx, "wait")
	if err != nil {
		return nil, err
	}
	defer release()
	start := time.Now()
	deadline := start.Add(time.Duration(*r.TimeoutMS) * time.Millisecond)
	observeCtx := ctx
	cancel := func() {}
	if *r.TimeoutMS > 0 {
		observeCtx, cancel = context.WithDeadline(ctx, deadline)
	}
	defer cancel()
	count := 0
	var previous Window
	stableSince := time.Time{}
	last := WaitObservation{Reason: "not_observed"}
	met := false
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		current, ready, e := s.waitObservation(observeCtx, r)
		count++
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if observeCtx.Err() != nil {
				last = WaitObservation{Reason: "observation_timeout", Complete: false}
				break
			}
			return nil, e
		}
		last = current
		if observeCtx.Err() != nil && *r.TimeoutMS > 0 {
			break
		}
		if r.Condition == "window_stable" {
			if !ready || current.Window == nil {
				stableSince = time.Time{}
			} else {
				if stableSince.IsZero() || previous != *current.Window {
					stableSince = time.Now()
					previous = *current.Window
				}
				ready = time.Since(stableSince) >= time.Duration(r.StableMS)*time.Millisecond
				if !ready {
					last.Reason = "waiting_for_window_stability"
				}
			}
		}
		if ready {
			met = true
			break
		}
		if *r.TimeoutMS == 0 || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(min(time.Duration(r.PollMS)*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	// 最后一次采样返回时也可能刚被用户取消；不能把取消竞态包装成 met/timeout。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.control.valid(ctx, s.control.epoch(ctx)) {
		return nil, controlError("DESKTOP_CONTROL_BLOCKED", "Condition observation was stopped before completion")
	}
	outcome := "timeout"
	if met {
		outcome = "met"
	}
	s.control.traceOutcome(ctx, "wait_"+outcome)
	return core.Result{"condition": r.Condition, "met": met, "condition_verified": met, "timed_out": !met, "outcome": outcome, "elapsed_ms": time.Since(start).Milliseconds(), "observations": count, "last_observation": last, "application_verified": false, "next_required_action": "desktop_snapshot", "input_dispatched": false}, nil
}
