package workresult

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func InputSchema(name string) (map[string]any, bool) {
	if name != "work_result_read" && name != "work_result_show" && name != "work_result_freeze" {
		return nil, false
	}
	str := toolcontract.String
	props := map[string]any{"task_id": str("Exact task to observe."), "workdir": str("Exact native project directory; job receipts must match this canonical path."), "job_ids": map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": str("Durable job ID; omit to select the most recent task jobs.")}, "artifact_ids": map[string]any{"type": "array", "maxItems": 8, "uniqueItems": true, "items": str("Immutable artifact ID.")}, "source_paths": map[string]any{"type": "array", "maxItems": 128, "items": str("Explicit source scope; omit for tracked and unignored Git files.")}}
	if name == "work_result_read" || name == "work_result_show" {
		props["result_id"] = str("Read a frozen delivery by exact ID instead of live task/project fields.")
		schema := toolcontract.InputObject(props)
		schema["oneOf"] = []map[string]any{{"required": []string{"result_id"}}, {"required": []string{"task_id", "workdir"}}}
		for _, field := range []string{"task_id", "workdir", "job_ids", "artifact_ids", "source_paths"} {
			toolcontract.ForbidTogether(schema, "result_id", field)
		}
		return schema, true
	}
	props["request_id"] = str("Stable idempotency key for this delivery.")
	props["expected_source_revision"] = str("Required source revision from the live work_result_read response. Freezing never reruns validation.")
	return toolcontract.InputObject(props, "request_id", "task_id", "workdir", "expected_source_revision"), true
}
func OutputSchema(name string) (map[string]any, bool) {
	if name != "work_result_read" && name != "work_result_show" && name != "work_result_freeze" {
		return nil, false
	}
	return toolcontract.OutputObject(map[string]any{"work_result": toolcontract.OpenObject("Live or frozen task/project/validation/delivery projection. Model review is not a machine validation result."), "result_id": toolcontract.String("Immutable delivery id, present after freeze."), "frozen": toolcontract.Boolean("Whether observations are immutable."), "observation_only": toolcontract.Boolean("No execution or project writes were performed.")}, "work_result", "frozen", "observation_only"), true
}
