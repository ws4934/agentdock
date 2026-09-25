package command

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func managedInput(name string) (map[string]any, bool) {
	str := toolcontract.String
	integer := toolcontract.BoundedInteger
	props := map[string]any{}
	required := []string{}
	switch name {
	case "job_observe":
		props = map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "list", "logs", "evidence"}}, "job_id": str("Durable job receipt id."), "task_id": str("Filter job list by task id."), "stream": map[string]any{"type": "string", "enum": []string{"stdout", "stderr"}}, "offset": integer("Explicit byte offset. Reading never advances another observer's cursor.", 0, int(jobrunMaxLog)), "max_bytes": integer("Maximum log bytes returned.", 1, 256<<10), "limit": integer("Maximum records returned, with explicit truncation.", 1, 200)}
	case "job_control":
		props = map[string]any{"action": map[string]any{"type": "string", "enum": []string{"cancel", "archive"}}, "job_id": str("Cancel one execution or archive a confirmed terminal job. Does not retry it.")}
		required = []string{"action", "job_id"}
	case "validation_run":
		props = map[string]any{"request_id": str("Stable idempotency key for this execution. Reuse after transport failure, not for a new run."), "workdir": str("Project root."), "task_id": str("Task to associate with the machine evidence."), "title": str("Short validation label."), "adapter": map[string]any{"type": "string", "enum": []string{"go_test", "junit", "process"}}, "packages": map[string]any{"type": "array", "maxItems": 64, "items": str("Relative Go package path.")}, "filter": str("Go test regex."), "race": toolcontract.Boolean("Go race detector."), "argv": map[string]any{"type": "array", "maxItems": 128, "items": str("Native argv. junit requires one {report} placeholder for a fresh private report file.")}, "source_paths": map[string]any{"type": "array", "maxItems": 128, "items": str("Explicit relative source paths; omit to fingerprint all tracked and unignored Git files.")}, "timeout_ms": integer("Detached validation timeout; defaults to 15 minutes.", 1, 86400000)}
		required = []string{"request_id", "adapter"}
	default:
		return nil, false
	}
	return toolcontract.InputObject(props, required...), true
}

const jobrunMaxLog = 8 << 20

func managedOutput(name string) (map[string]any, bool) {
	switch name {
	case "job_observe", "job_control", "validation_run":
		return toolcontract.OutputObject(map[string]any{"job_id": toolcontract.String("Durable execution ID."), "status": toolcontract.String("Execution status, separate from validation evidence."), "job": toolcontract.OpenObject("Machine-generated execution receipt."), "jobs": toolcontract.ObjectArray("Job receipts."), "log": toolcontract.OpenObject("Log slice with independent offsets."), "evidence_freshness": toolcontract.String("current, stale, unproven or not_available; only computed for evidence reads.")}), true
	}
	return nil, false
}
