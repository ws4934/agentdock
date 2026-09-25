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
		props["cursor"] = map[string]any{"type": "string", "maxLength": 2048, "description": "Opaque next_cursor from a prior list with the same task filter."}
	case "job_control":
		props = map[string]any{"action": map[string]any{"type": "string", "enum": []string{"cancel", "archive", "abandon"}}, "job_id": str("Cancel, archive, or explicitly abandon an unknown job only after its owned process group is proven gone. Never retries it or marks it successful.")}
		required = []string{"action", "job_id"}
	case "validation_run":
		props = map[string]any{"request_id": str("Stable idempotency key for this execution. Reuse after transport failure, not for a new run."), "workdir": str("Project root."), "task_id": str("Task to associate with the machine evidence."), "title": str("Short validation label."), "adapter": map[string]any{"type": "string", "enum": []string{"go_test", "junit", "process"}}, "packages": map[string]any{"type": "array", "maxItems": 64, "items": str("Relative Go package path.")}, "filter": str("Go test regex."), "race": toolcontract.Boolean("Go race detector."), "argv": map[string]any{"type": "array", "maxItems": 128, "items": str("Native argv. junit requires one {report} placeholder for a fresh private report file.")}, "source_paths": map[string]any{"type": "array", "maxItems": 128, "items": str("Explicit relative source paths; omit to fingerprint all tracked and unignored Git files.")}, "timeout_ms": integer("Detached validation timeout; defaults to 15 minutes.", 1, 86400000)}
		required = []string{"request_id", "adapter"}
	default:
		return nil, false
	}
	schema := toolcontract.InputObject(props, required...)
	switch name {
	case "job_observe":
		for _, a := range []string{"status", "evidence", "logs"} {
			toolcontract.RequireWhen(schema, "action", a, "job_id")
		}
		toolcontract.RequireWhen(schema, "action", "logs", "stream")
		toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"not": map[string]any{"required": []string{"action"}}}, "then": map[string]any{"required": []string{"job_id"}}})
	case "validation_run":
		for _, a := range []string{"junit", "process"} {
			toolcontract.RequireWhen(schema, "adapter", a, "argv")
		}
		props["argv"].(map[string]any)["minItems"] = 1
	}
	return schema, true
}

const jobrunMaxLog = 8 << 20

func managedOutput(name string) (map[string]any, bool) {
	switch name {
	case "job_observe", "job_control", "validation_run":
		schema := toolcontract.OutputObject(map[string]any{"job_id": toolcontract.String("Durable execution ID."), "status": toolcontract.String("Execution status, separate from validation evidence."), "job": toolcontract.OpenObject("Machine-generated execution receipt."), "jobs": toolcontract.ObjectArray("Job receipts."), "log": toolcontract.OpenObject("Log slice with independent offsets."), "evidence_freshness": toolcontract.String("current, stale, unproven or not_available; only computed for evidence reads.")})
		toolcontract.Variants(schema, []string{"job_id", "status", "job"}, []string{"jobs", "count", "truncated"}, []string{"log", "observation_only"})
		return schema, true
	}
	return nil, false
}
