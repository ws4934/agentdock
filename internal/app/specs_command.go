package app

import (
	"context"

	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
)

func commandToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "job_observe", Contract: commandToolContract, Title: "Observe durable jobs", Description: "Read durable job state, explicit-offset logs and source freshness. Survives Core restart; never retries an execution.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("job_observe", func(ctx context.Context, r *Runtime, request toolcommand.JobObserveRequest) (Result, error) {
			return r.command.ObserveJob(ctx, request)
		})},
		{Name: "job_control", Contract: commandToolContract, Title: "Control durable jobs", Description: "Request cancellation or archive a confirmed terminal job. Cancellation is not confirmed until a terminal receipt exists.", Annotations: mutatingToolAnnotations(true, false), Handler: typedToolHandler("job_control", func(ctx context.Context, r *Runtime, request toolcommand.JobControlRequest) (Result, error) {
			return r.command.ControlJob(ctx, request)
		})},
		{Name: "validation_run", Contract: commandToolContract, Title: "Run evidenced validation", Description: "Run one detached validation and record actual tests and before/after source fingerprints. go_test is typed; junit accepts argv with {report}; process success never claims tests ran. Inspect through job_observe action=evidence.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("validation_run", func(ctx context.Context, r *Runtime, request toolcommand.ValidationRequest) (Result, error) {
			if request.TaskID != "" {
				if _, err := r.workResults.Task(ctx, request.TaskID); err != nil {
					return nil, err
				}
			}
			return r.command.RunValidation(ctx, request)
		})},
		{Name: "exec_command", Contract: commandToolContract, Title: "Run command", Description: toolcommand.Description(), Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("exec_command", func(ctx context.Context, r *Runtime, request toolcommand.ExecRequest) (Result, error) {
			if request.ExecutionMode == "managed" && request.TaskID != "" {
				if _, err := r.workResults.Task(ctx, request.TaskID); err != nil {
					return nil, err
				}
			}
			return r.command.Exec(ctx, request)
		})},
		{Name: "session_observe", Contract: commandToolContract, Title: "Observe command sessions", Description: "List or inspect command sessions through a read-only session tool.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("session_observe", func(_ context.Context, r *Runtime, request toolcommand.SessionObserveRequest) (Result, error) {
			return r.command.Observe(request)
		})},
		{Name: "session_act", Contract: commandToolContract, Title: "Act on command sessions", Description: "Write to or stop command sessions through a mutating session tool.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("session_act", func(_ context.Context, r *Runtime, request toolcommand.SessionActRequest) (Result, error) {
			return r.command.Act(request)
		})},
	}
}
