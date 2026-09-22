package desktop

import "github.com/uvwt/agentdock/internal/tool/contract"

const ToolSequence = "desktop_sequence"

func sequenceSelectorSchema() map[string]any {
	s := contract.InputObject(map[string]any{
		"role":        map[string]any{"type": "string", "maxLength": 1024},
		"title":       map[string]any{"type": "string", "maxLength": 1024},
		"description": map[string]any{"type": "string", "maxLength": 1024},
	})
	alternatives := []map[string]any{}
	for _, name := range []string{"role", "title", "description"} {
		alternatives = append(alternatives, map[string]any{"required": []string{name}, "properties": map[string]any{name: map[string]any{"minLength": 1}}})
	}
	s["anyOf"] = alternatives
	return s
}

func sequenceInputSchema(props map[string]any) map[string]any {
	props["snapshot_id"] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$", "description": "Fresh snapshot binding this action batch to its approved background window."}
	props["timeout_ms"] = contract.BoundedInteger("Batch budget, default 60000 ms; cancellation stops remaining input.", 100, maxSequenceTimeoutMS)
	// 复用单次动作参数，避免连续操作维护另一套坐标、键盘和文本约定。
	single, _ := InputSchema(ToolAct)
	stepProps := single["properties"].(map[string]any)
	for _, key := range []string{"task_id", "snapshot_id", "observe_after", "pid", "element_id"} {
		delete(stepProps, key)
	}
	stepProps["action"] = enum("Action to execute in order.", "click", "move", "drag", "scroll", "key", "type", "set_value", "wait")
	stepProps["element"] = sequenceSelectorSchema()
	stepProps["duration_ms"] = contract.BoundedInteger("Drag duration (up to 2000 ms), or explicit wait (up to 30000 ms; default 200). No automatic delay between actions.", 1, 30000)
	stepProps["after"] = contract.InputObject(map[string]any{
		"condition":  enum("Optional read-only condition after this action.", "element_exists", "element_absent", "element_enabled", "element_disabled"),
		"element":    sequenceSelectorSchema(),
		"timeout_ms": contract.BoundedInteger("Condition wait, default 2000 ms; zero checks once. Input is never retried.", 0, 30000),
	}, "condition", "element")
	step := contract.InputObject(stepProps, "action")
	step["allOf"] = []map[string]any{
		when("click", map[string]any{"oneOf": []map[string]any{{"required": []string{"point"}}, {"required": []string{"element"}}}}),
		when("move", map[string]any{"required": []string{"point"}}),
		when("drag", map[string]any{"required": []string{"path"}}),
		when("scroll", map[string]any{"required": []string{"point"}}),
		when("key", map[string]any{"required": []string{"key"}}),
		when("type", map[string]any{"required": []string{"text"}, "properties": map[string]any{"text": map[string]any{"minLength": 1}}}),
		when("set_value", map[string]any{"required": []string{"text", "element"}}),
	}
	props["steps"] = map[string]any{"type": "array", "minItems": 1, "maxItems": maxSequenceSteps, "items": step, "description": "Ordered actions using point coordinates or optional AX selectors. Normal steps run without extra model calls; final observation is returned. Stop and review on input errors or local pause/stop."}
	return contract.InputObject(props, "snapshot_id", "steps")
}

func sequenceOutputSchema() map[string]any {
	return contract.OutputObject(map[string]any{
		"mode":                 enum("Execution mode.", "background"),
		"outcome":              enum("Batch outcome, not application-level confirmation.", "completed", "interrupted", "single_step"),
		"total_steps":          contract.Integer("Number of requested steps."),
		"dispatched_steps":     contract.Integer("Actions with confirmed dispatch; waits do not send input."),
		"completed_steps":      contract.Integer("Actions or waits completed, including explicitly requested postconditions; not business success."),
		"steps":                contract.ObjectArray("Per-step progress without input text or AX labels."),
		"failed_step":          contract.Integer("One-based interrupted step; zero means final observation failed."),
		"failure_stage":        contract.String("Interrupted stage."),
		"error":                contract.OpenObject("Error code and partial-input boundary; never replay completed or uncertain input."),
		"elapsed_ms":           contract.Integer("Elapsed execution time."),
		"snapshot_consumed":    contract.Boolean("Original snapshot cannot be reused."),
		"foreground_fallback":  contract.Boolean("Always false."),
		"application_verified": contract.Boolean("Always false; review the final observation."),
		"observation":          contract.OpenObject("Final window snapshot. Local single-step returns evidence without an action token."),
		"observation_status":   enum("Final observation availability.", "captured", "unavailable"),
		"next_required_action": contract.String("Review observation or follow local stop instructions."),
	}, "mode", "outcome", "total_steps", "dispatched_steps", "completed_steps", "steps", "elapsed_ms", "snapshot_consumed", "foreground_fallback", "application_verified", "observation_status", "next_required_action")
}
