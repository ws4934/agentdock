package task

import (
	"context"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type ReadRequest struct {
	Action     string `json:"action"`
	TaskID     string `json:"task_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Limit      *int   `json:"limit,omitempty"`
	IfRevision string `json:"if_revision,omitempty"`
}

// Read 不进入任何写入或 Evolution 分支；卡片更新只读取一个任务文件。
func (s *Service) Read(ctx context.Context, request ReadRequest) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch request.Action {
	case "get":
		return s.Manage(ctx, ManageRequest{Action: "get", TaskID: request.TaskID})
	case "list":
		status := taskstate.Status(request.Status)
		if status != "" && status != taskstate.StatusActive && status != taskstate.StatusBlocked && status != taskstate.StatusCompleted {
			return nil, toolErrorDetails("INVALID_STATUS", "unsupported task status filter", "validation", nil)
		}
		tasks, partial, err := s.tasks.ListPage(status, intValue(request.Limit, 50))
		if err != nil {
			return nil, taskToolError(err)
		}
		items := make([]map[string]any, 0, len(tasks))
		for _, task := range tasks {
			items = append(items, compactTaskListItem(task))
		}
		return Result{"action": "list", "tasks": items, "count": len(items), "partial": partial}, nil
	case "snapshot":
		task, err := s.tasks.Get(request.TaskID)
		if err != nil {
			return nil, taskToolError(err)
		}
		revision := taskstate.ManagementRevision(task)
		result := Result{"action": "snapshot", "task_id": task.ID, "revision": revision, "unchanged": request.IfRevision == revision}
		if request.IfRevision != revision {
			result["task_summary"] = compactTaskSummary(task)
		}
		return result, nil
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "task_read only accepts list, get or snapshot", "validation", nil)
	}
}
