package desktop

import (
	"encoding/hex"
	"strings"
)

// 请求体和执行期限的资源上限，不用于限制普通交互的操作种类。
const maxSequenceSteps = 256
const maxSequenceTimeoutMS = 300000

type SequenceCondition struct {
	Condition string          `json:"condition"`
	Element   ElementSelector `json:"element"`
	TimeoutMS *int            `json:"timeout_ms,omitempty"`
}
type SequenceStep struct {
	Action     string             `json:"action"`
	Element    ElementSelector    `json:"element,omitempty"`
	Text       *string            `json:"text,omitempty"`
	Point      *Point             `json:"point,omitempty"`
	Space      string             `json:"space,omitempty"`
	Button     string             `json:"button,omitempty"`
	ClickCount int                `json:"click_count,omitempty"`
	Path       []Point            `json:"path,omitempty"`
	DurationMS int                `json:"duration_ms,omitempty"`
	DeltaX     int                `json:"delta_x,omitempty"`
	DeltaY     int                `json:"delta_y,omitempty"`
	Key        string             `json:"key,omitempty"`
	Modifiers  []string           `json:"modifiers,omitempty"`
	After      *SequenceCondition `json:"after,omitempty"`
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

func hasSequenceSelector(s ElementSelector) bool {
	return s.Role != "" || s.Title != nil || s.Description != nil
}
func sequenceSelector(s ElementSelector) (ElementSelector, error) {
	if s.Role == "" && (s.Title == nil || *s.Title == "") && (s.Description == nil || *s.Description == "") {
		return s, invalid("element requires a nonempty role, title or description")
	}
	_, err := normalizeWait(WaitRequest{PID: 1, WindowID: 1, Condition: "element_exists", Element: &s})
	if s.Title != nil {
		v := *s.Title
		s.Title = &v
	}
	if s.Description != nil {
		v := *s.Description
		s.Description = &v
	}
	return s, err
}

// 连续与单次输入共用同一套动作参数和校验，不再另设 AXButton 白名单。
func (step SequenceStep) actionRequest() ActionRequest {
	r := ActionRequest{Action: step.Action, SnapshotID: "sequence", Point: step.Point, Space: step.Space,
		Button: step.Button, ClickCount: step.ClickCount, Path: step.Path, DurationMS: step.DurationMS,
		DeltaX: step.DeltaX, DeltaY: step.DeltaY, Key: step.Key, Modifiers: step.Modifiers}
	if hasSequenceSelector(step.Element) {
		r.ElementID = "resolved-at-execution"
	}
	if step.Text != nil {
		r.Text = *step.Text
	}
	return r
}

func normalizeSequence(r SequenceRequest) (SequenceRequest, error) {
	if _, err := hex.DecodeString(r.SnapshotID); err != nil || len(r.SnapshotID) != 32 || r.SnapshotID != strings.ToLower(r.SnapshotID) {
		return r, invalid("sequence requires a fresh snapshot_id")
	}
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 60000
	}
	if r.TimeoutMS < 100 || r.TimeoutMS > maxSequenceTimeoutMS || len(r.Steps) == 0 || len(r.Steps) > maxSequenceSteps {
		return r, invalid("sequence request exceeds its execution or size budget")
	}
	// 输入前校验整份请求，复制所有可变字段，防止后面的无效参数造成部分执行。
	r.Steps = append([]SequenceStep(nil), r.Steps...)
	for i := range r.Steps {
		step := &r.Steps[i]
		var err error
		if hasSequenceSelector(step.Element) {
			step.Element, err = sequenceSelector(step.Element)
			if err != nil {
				return r, err
			}
		}
		if step.Point != nil {
			point := *step.Point
			step.Point = &point
		}
		step.Path = append([]Point(nil), step.Path...)
		step.Modifiers = append([]string(nil), step.Modifiers...)
		if step.Text != nil {
			text := *step.Text
			step.Text = &text
		}
		action := step.actionRequest()
		if step.Action == "wait" {
			if step.DurationMS == 0 {
				step.DurationMS = 200
			}
			if step.DurationMS < 1 || step.DurationMS > 30000 || hasSequenceSelector(step.Element) || step.Text != nil {
				return r, invalid("wait requires only duration_ms and an optional after condition")
			}
			if step.Point != nil || step.Space != "" || step.Button != "" || step.ClickCount != 0 || len(step.Path) != 0 || step.DeltaX != 0 || step.DeltaY != 0 || step.Key != "" || len(step.Modifiers) != 0 {
				return r, invalid("wait does not accept input parameters")
			}
		} else {
			if step.Action == "activate" {
				return r, invalid("application activation requires a separate explicitly authorized call")
			}
			if step.Text != nil && step.Action != "type" && step.Action != "set_value" {
				return r, invalid("text only applies to type or set_value")
			}
			if (step.Action == "type" || step.Action == "set_value") && step.Text == nil {
				return r, invalid("text is required")
			}
			if step.Action == "scroll" && step.Point == nil {
				return r, invalid("background scroll requires a point")
			}
		}
		if step.Action != "wait" {
			if err = validateAction(action); err != nil {
				return r, err
			}
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
			if timeout < 0 || timeout > 30000 {
				return r, invalid("postcondition timeout must be 0..30000 ms")
			}
			after.TimeoutMS = &timeout
			step.After = &after
		}
	}
	return r, nil
}
