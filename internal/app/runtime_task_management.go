package app

import (
	"context"
	"errors"

	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func taskManagementError(err error) error {
	if errors.Is(err, jobrun.ErrTaskJobsBusy) {
		return &ToolError{Code: "TASK_JOBS_BUSY", Message: "task has live or non-terminal jobs", Category: "conflict"}
	}
	if errors.Is(err, jobrun.ErrTaskJobsUnavailable) {
		return &ToolError{Code: "TASK_JOBS_UNAVAILABLE", Message: "job state is unavailable; no task was deleted", Category: "conflict"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ToolError{Code: "TASK_MANAGEMENT_CANCELLED", Message: "request ended before this task was changed", Category: "conflict"}
	}
	return err
}

func (r *Runtime) deleteIdleTask(ctx context.Context, id, revision string) (Result, error) {
	request := taskstate.ManagementRequest{Action: "delete", Tasks: []taskstate.ManagementReference{{ID: id, Revision: revision}}}
	if err := request.Validate(); err != nil {
		return nil, &ToolError{Code: "INVALID_TASK_MANAGEMENT", Message: "invalid task management request", Category: "validation"}
	}
	jobs, err := r.command.Jobs()
	if err != nil {
		return nil, taskManagementError(jobrun.ErrTaskJobsUnavailable)
	}
	var result Result
	err = jobs.WithTaskIdle(ctx, id, func() error {
		var deleteErr error
		result, deleteErr = r.taskTools.RuntimeTaskDelete(id, revision)
		return deleteErr
	})
	return result, taskManagementError(err)
}

func (r *Runtime) RuntimeTaskManage(ctx context.Context, request taskstate.ManagementRequest) (Result, error) {
	// 先验证完整请求再写入；批量 IO 不是跨文件事务，逐项报告真实结果。
	if err := request.Validate(); err != nil {
		return nil, &ToolError{Code: "INVALID_TASK_MANAGEMENT", Message: "invalid task management request", Category: "validation"}
	}
	results := make([]map[string]any, 0, len(request.Tasks))
	changed := 0
	record := func(id string, err error) {
		item := map[string]any{"task_id": id, "ok": err == nil}
		if err == nil {
			changed++
		} else {
			code := "TASK_MANAGEMENT_FAILED"
			var toolErr *ToolError
			if errors.As(taskManagementError(err), &toolErr) {
				code = toolErr.Code
			}
			item["code"] = code
		}
		results = append(results, item)
	}
	apply := func(blocked map[string]error) error {
		for _, ref := range request.Tasks {
			err := ctx.Err()
			if err == nil {
				err = blocked[ref.ID]
			}
			if err == nil {
				if request.Action == "delete" {
					_, err = r.taskTools.RuntimeTaskDelete(ref.ID, ref.Revision)
				} else {
					_, err = r.taskTools.RuntimeTaskArchive(ref.ID, ref.Revision, request.Action == "archive")
				}
			}
			record(ref.ID, err)
		}
		return nil
	}
	if request.Action == "delete" {
		jobs, err := r.command.Jobs()
		if err == nil {
			ids := make([]string, 0, len(request.Tasks))
			for _, ref := range request.Tasks {
				ids = append(ids, ref.ID)
			}
			err = jobs.WithIdleTasks(ctx, ids, apply)
		} else {
			err = jobrun.ErrTaskJobsUnavailable
		}
		// 准入/索引检查失败时回调未执行，所有记录仍未更改。
		if err != nil {
			for _, ref := range request.Tasks {
				record(ref.ID, err)
			}
		}
	} else {
		_ = apply(nil)
	}
	return Result{"ok": true, "source": runtimeAPISource, "action": request.Action, "results": results, "changed": changed, "failed": len(results) - changed, "observation_only": false}, nil
}
