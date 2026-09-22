package desktop

import "github.com/uvwt/agentdock/internal/tool/contract"

const ToolSequence = "desktop_sequence"

func sequenceSelectorSchema() map[string]any {
	s := contract.InputObject(map[string]any{
		"role":        map[string]any{"type": "string", "minLength": 1, "maxLength": 1024},
		"title":       map[string]any{"type": "string", "maxLength": 1024},
		"description": map[string]any{"type": "string", "maxLength": 1024},
	}, "role")
	s["anyOf"] = []map[string]any{
		{"required": []string{"title"}, "properties": map[string]any{"title": map[string]any{"minLength": 1}}},
		{"required": []string{"description"}, "properties": map[string]any{"description": map[string]any{"minLength": 1}}},
	}
	return s
}

func sequenceInputSchema(props map[string]any) map[string]any {
	props["snapshot_id"] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$", "description": "Fresh background AX snapshot binding the entire sequence to one approved window; consumed once."}
	props["timeout_ms"] = contract.BoundedInteger("Total execution/observation budget, default 10000 ms. Native calls may finish after cancellation; no further input is sent.", 100, 30000)
	after := contract.InputObject(map[string]any{
		"condition":  enum("Only this exact predicate is verified, not business completion.", "element_exists", "element_absent", "element_enabled", "element_disabled"),
		"element":    sequenceSelectorSchema(),
		"timeout_ms": contract.BoundedInteger("Read-only polling budget after this input, default 2000 ms; zero observes once. Never retries the input.", 0, 5000),
	}, "condition", "element")
	step := contract.InputObject(map[string]any{
		"action":  enum("One authorized semantic action, never coordinates, keys, app switching or arbitrary code.", "click", "set_value"),
		"element": sequenceSelectorSchema(),
		"text":    map[string]any{"type": "string", "maxLength": 4096, "description": "Full replacement text for set_value; empty clears the field. Not allowed for click."},
		"after":   after,
	}, "action", "element")
	step["allOf"] = []map[string]any{
		when("set_value", map[string]any{"required": []string{"text"}}),
		when("click", map[string]any{"not": map[string]any{"required": []string{"text"}}}),
	}
	props["steps"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": step, "description": "Preauthorized deterministic steps in one window. Resolve each selector against a fresh complete AX tree. Stop at ambiguity, modal/context change, failed predicate or local stop. Break the plan where user confirmation or new reasoning is required."}
	return contract.InputObject(props, "snapshot_id", "steps")
}

func sequenceOutputSchema() map[string]any {
	return contract.OutputObject(map[string]any{
		"mode":                 enum("Only background is supported.", "background"),
		"outcome":              enum("Completed means the declared sequence ended, not business success.", "completed", "interrupted", "single_step"),
		"total_steps":          contract.Integer("Number of requested steps."),
		"dispatched_steps":     contract.Integer("Steps with confirmed event dispatch; failed/partial input is reported separately."),
		"completed_steps":      contract.Integer("Steps dispatched and observed, including any declared postcondition."),
		"steps":                contract.ObjectArray("Per-step index, action, dispatch and postcondition results; no input text or AX labels."),
		"failed_step":          contract.Integer("One-based interrupted step; zero means final observation failed after all steps."),
		"failure_stage":        contract.String("Stage that interrupted execution."),
		"error":                contract.OpenObject("Error code and partial-input boundary. Never automatically replay a sequence."),
		"elapsed_ms":           contract.Integer("Elapsed execution time."),
		"snapshot_consumed":    contract.Boolean("Original snapshot is consumed; never reuse it."),
		"foreground_fallback":  contract.Boolean("Always false."),
		"application_verified": contract.Boolean("Always false; inspect the final result."),
		"observation":          contract.OpenObject("Final same-window snapshot. Local single-step returns evidence without a usable action token."),
		"observation_status":   enum("Whether a final observation was returned.", "captured", "unavailable"),
		"next_required_action": contract.String("Review observation, or stop and observe again; never replay completed input."),
	}, "mode", "outcome", "total_steps", "dispatched_steps", "completed_steps", "steps", "elapsed_ms", "snapshot_consumed", "foreground_fallback", "application_verified", "observation_status", "next_required_action")
}
