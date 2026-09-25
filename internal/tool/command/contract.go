package command

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolExecCommand    = "exec_command"
	ToolSessionObserve = "session_observe"
	ToolSessionAct     = "session_act"
)

func InputSchema(name string) (map[string]any, bool) {
	if schema, ok := managedInput(name); ok {
		return schema, true
	}
	stringProp := toolcontract.String
	boolProp := toolcontract.Boolean
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string

	switch name {
	case ToolExecCommand:
		props["cmd"] = stringProp("Command to run.")
		props["workdir"] = stringProp(WorkdirDescription())
		AddRuntimeProperties(props)
		props["skill"] = stringProp("Optional active Skill context. When workdir is omitted, the command runs from the active installed Skill root and loads that Skill isolated environment.")
		props["skill_env"] = stringProp("Optional Skill name whose isolated environment is loaded without changing workdir. Kept for environment-only compatibility.")
		props["env"] = map[string]any{"type": "object", "description": "Explicit command environment values. These override the selected Skill environment.", "additionalProperties": map[string]any{"type": "string"}}
		props["timeout_ms"] = boundedIntProp("Timeout in milliseconds. Must be positive and is capped at 86400000.", 1, 86400000)
		props["execution_mode"] = map[string]any{"type": "string", "description": "auto/sync/async are Core-owned sessions. managed explicitly starts a non-interactive detached job that survives Core restart; requires request_id and is observed through job_observe.", "enum": []string{"auto", "sync", "async", "managed"}}
		props["request_id"] = stringProp("Required stable idempotency key for managed execution. Reuse to recover the same receipt after a network error.")
		props["task_id"] = stringProp("Optional task associated with a managed execution.")
		props["title"] = stringProp("Short managed-job label, not a command dump.")
		props["yield_time_ms"] = boundedIntProp("Foreground wait threshold for execution_mode=auto. Defaults to 5000 and is capped at 30000 milliseconds.", 0, 30000)
		props["max_output_bytes"] = boundedIntProp("Combined stdout/stderr raw-byte budget. Defaults to 65536; capped at 4194304.", 1, MaxOutputBytes)
		props["stdin"] = stringProp("Initial stdin.")
		props["tty"] = boolProp("Keep stdin open.")
		required = []string{"cmd"}
	case ToolSessionObserve:
		props["stdout_offset"] = toolcontract.BoundedInteger64("Independent absolute stdout byte offset from stdout_next_offset. Omit to read from retained start.", 0, 9223372036854775807)
		props["stderr_offset"] = toolcontract.BoundedInteger64("Independent absolute stderr byte offset from stderr_next_offset. Reads never consume another observer output.", 0, 9223372036854775807)
		props["action"] = map[string]any{"type": "string", "description": "Read-only session action.", "enum": []string{"list", "status"}}
		props["session_id"] = stringProp("Session id returned by exec_command, required for status.")
		props["max_output_bytes"] = boundedIntProp("Combined stdout/stderr raw-byte budget. Defaults to 65536; capped at 4194304.", 1, MaxOutputBytes)
	case ToolSessionAct:
		props["action"] = map[string]any{"type": "string", "description": "Mutating session action.", "enum": []string{"write", "kill", "kill_all"}}
		props["session_id"] = stringProp("Session id returned by exec_command, required for write/kill.")
		props["chars"] = stringProp("Characters to write when action=write.")
		props["max_output_bytes"] = boundedIntProp("Combined stdout/stderr raw-byte budget. Defaults to 65536; capped at 4194304.", 1, MaxOutputBytes)
	default:
		return nil, false
	}
	schema := toolcontract.InputObject(props, required...)
	switch name {
	case ToolExecCommand:
		toolcontract.RequireWhen(schema, "execution_mode", "managed", "request_id")
	case ToolSessionObserve:
		toolcontract.RequireWhen(schema, "action", "status", "session_id")
	case ToolSessionAct:
		toolcontract.Require(schema, "action")
		toolcontract.RequireWhen(schema, "action", "write", "session_id", "chars")
		toolcontract.RequireWhen(schema, "action", "kill", "session_id")
	}
	return schema, true
}

func OutputSchema(name string) (map[string]any, bool) {
	if schema, ok := managedOutput(name); ok {
		return schema, true
	}
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	props := map[string]any{
		"stdout_base64": stringProp("Exact raw bytes when UTF-8 text is lossy; offsets count original bytes."), "stderr_base64": stringProp("Exact raw stderr bytes when UTF-8 text is lossy."),
		"stdout_offset": intProp("Returned absolute stdout offset."), "stderr_offset": intProp("Returned absolute stderr offset."), "stdout_next_offset": intProp("Next stdout byte offset; independent per observer."), "stderr_next_offset": intProp("Next stderr byte offset; independent per observer."),
		"sessions":         arrayProp("Command session summaries returned by list or bulk session actions."),
		"count":            intProp("Command session count when a list or bulk action returns multiple sessions."),
		"session_id":       stringProp("Command session id."),
		"status":           stringProp("Session status."),
		"runtime":          stringProp("Command runtime when reported by the host, such as windows or wsl."),
		"wsl_distribution": stringProp("WSL distribution selected for the command when explicitly configured."),
		"workdir":          stringProp("Logical command working directory in the selected runtime."),
		"stdout":           stringProp("Captured stdout segment."),
		"stderr":           stringProp("Captured stderr segment."),
		"command_ok":       boolProp("Whether a completed command exited successfully. Omitted while the command is still running."),
		"command_error":    stringProp("Command process error when execution did not succeed."),
		"exit_code":        intProp("Process exit code, when available."),
		"elapsed_ms":       intProp("Session elapsed milliseconds."),
		"timed_out":        boolProp("Whether the command timed out."),
	}
	switch name {
	case ToolExecCommand:
		props["session_reason"] = stringProp("Why exec_command returned a session instead of a completed result.")
		props["observe_after_ms"] = intProp("Suggested delay before inspecting the returned session.")
	case ToolSessionObserve, ToolSessionAct:
	default:
		return nil, false
	}
	schema := toolcontract.OutputObject(props)
	switch name {
	case ToolExecCommand:
		toolcontract.Variants(schema, []string{"session_id", "status", "stdout", "stderr", "stdout_next_offset", "stderr_next_offset"}, []string{"job_id", "status", "job"})
	default:
		toolcontract.Variants(schema, []string{"sessions", "count"}, []string{"session_id", "status", "stdout", "stderr", "stdout_next_offset", "stderr_next_offset"})
	}
	return schema, true
}
