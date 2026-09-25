package app

import (
	"context"

	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

func taskManageToolSpecs() []ToolSpec {
	return []ToolSpec{{Name: "task_manage", Contract: taskToolContract, Title: "Manage task lifecycle", Description: "Create, resume, block or complete a recoverable task and automatically present its progress card. Use task_update for checkpoints/final review and task_read for observation; those do not create additional cards. This changes saved task state, not process execution.", Annotations: mutatingToolAnnotations(false, false), Handler: typedToolHandler("task_manage", func(ctx context.Context, r *Runtime, request tooltask.LifecycleRequest) (Result, error) {
		return r.taskTools.Manage(ctx, request.Command())
	})},
		{Name: "task_update", Contract: taskToolContract, Title: "Save task progress", Description: "Save checkpoint progress or final_review without creating a card. Existing progress cards read these changes. Final review never completes steps or certifies tests automatically. Use task_manage for create/resume/block/complete.", Annotations: mutatingToolAnnotations(false, false), Handler: typedToolHandler("task_update", func(ctx context.Context, r *Runtime, request tooltask.UpdateRequest) (Result, error) {
			return r.taskTools.Manage(ctx, request.Command())
		})},
		{Name: "task_read", Contract: taskToolContract, Title: "Read task progress", Description: "Read saved tasks without writes or card creation. get returns full state; list returns bounded summaries; snapshot reads one compact task with optional if_revision for card updates. Does not scan projects, read logs or execute jobs.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("task_read", func(ctx context.Context, r *Runtime, request tooltask.ReadRequest) (Result, error) {
			return r.taskTools.Read(ctx, request)
		})},
	}
}

func workflowToolSpecs() []ToolSpec {
	return []ToolSpec{{Name: "workflow_template_manage", Contract: canonicalToolContract, Title: "Manage workflow templates", Description: "List, get, get multiple, publish, retire, or match AgentDock workflow templates. publish validates and activates a complete immutable template version; get_many requires the model to compose the returned templates before task creation.", Availability: requiresNexus, Handler: typedToolHandler("workflow_template_manage", func(ctx context.Context, r *Runtime, request tooltask.WorkflowRequest) (Result, error) {
		return r.taskTools.WorkflowManage(ctx, request)
	})}}
}
