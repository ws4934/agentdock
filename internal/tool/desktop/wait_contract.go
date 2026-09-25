package desktop

import "github.com/uvwt/agentdock/internal/tool/contract"

func waitInputSchema(props map[string]any) map[string]any {
	props["pid"] = contract.BoundedInteger("Exact target application PID.", 1, 2147483647)
	props["window_id"] = contract.BoundedInteger64("Observed window ID, required for absence and element conditions.", 1, 4294967295)
	props["window_title"] = map[string]any{"type": "string", "maxLength": 1024}
	props["condition"] = enum("Observe only. Absence means not visible, not proof of process exit.", "window_exists", "window_absent", "window_stable", "element_exists", "element_absent", "element_enabled", "element_disabled")
	props["element"] = contract.InputObject(map[string]any{"role": contract.String("Exact AX role."), "title": map[string]any{"type": "string", "maxLength": 1024}, "description": map[string]any{"type": "string", "maxLength": 1024}})
	props["timeout_ms"] = contract.BoundedInteger("Observation wait, default 10000; zero checks once.", 0, 30000)
	props["poll_ms"] = contract.BoundedInteger("Observation interval, default 200.", 100, 1000)
	props["stable_ms"] = contract.BoundedInteger("Window stability duration, default 300.", 100, 5000)
	props["max_nodes"] = contract.BoundedInteger("AX node limit, default 200. Truncation cannot verify absence.", 1, 500)
	props["max_depth"] = contract.BoundedInteger("AX depth limit, default 8.", 1, 16)
	return contract.InputObject(props, "pid", "condition")
}
func taskInputSchema(props map[string]any) map[string]any {
	props["action"] = enum("Reserve, inspect or release an exclusive desktop task. Cannot resume a local stop.", "begin", "status", "end")
	props["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Human-readable task purpose, not authenticated identity."}
	schema := contract.InputObject(props, "action")
	schema["allOf"] = []map[string]any{when("begin", map[string]any{"required": []string{"title"}}), when("status", map[string]any{"required": []string{"task_id"}}), when("end", map[string]any{"required": []string{"task_id"}})}
	return schema
}
