package task

import (
	"encoding/json"
	"strings"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func (s *Service) RuntimeTasks(status string, limit int) (Result, error) {
	statusFilter := taskstate.Status(strings.ToLower(strings.TrimSpace(status)))
	if statusFilter != "" && statusFilter != taskstate.StatusActive && statusFilter != taskstate.StatusBlocked && statusFilter != taskstate.StatusCompleted && statusFilter != "archived" {
		return nil, toolErrorDetails("INVALID_STATUS", "unsupported task status filter", "validation", map[string]any{"status": statusFilter, "allowed": []string{"active", "blocked", "completed", "archived"}})
	}
	if limit <= 0 {
		limit = 50
	}
	tasks, partial, err := s.tasks.ListPage(statusFilter, limit)
	if err != nil {
		return nil, taskToolError(err)
	}
	items := make([]map[string]any, 0, len(tasks))
	// 为原生客户端的 512 KiB 响应上限留出 envelope 空间；按字节截断仍明确标记部分列表。
	pageBytes := 0
	for _, task := range tasks {
		item := compactTaskListItem(task)
		item["revision"] = taskstate.ManagementRevision(task)
		item["archived_at"] = task.ArchivedAt
		item["project"] = task.Project
		item["created_at"] = task.CreatedAt
		item["event_count"] = len(task.Events)
		if task.CompletedAt != nil {
			item["completed_at"] = *task.CompletedAt
		}
		if len(task.SourceTemplates) > 0 {
			item["source_templates"] = task.SourceTemplates
		} else if task.Template != nil {
			// 旧任务仍可通过 Runtime API 查看原模板来源。
			item["source_templates"] = []taskstate.TemplateReference{{ID: task.Template.ID, Version: task.Template.Version, Hash: task.Template.Hash}}
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, taskToolError(err)
		}
		if pageBytes+len(encoded) > 480*1024 {
			partial = true
			continue
		}
		pageBytes += len(encoded) + 1
		items = append(items, item)
	}
	return Result{"ok": true, "source": "agentdock-api", "action": "list", "tasks": items, "count": len(items), "partial": partial, "observation_only": true}, nil
}

func (s *Service) RuntimeTask(id string) (Result, error) {
	task, err := s.tasks.Get(strings.TrimSpace(id))
	if err != nil {
		return nil, taskToolError(err)
	}
	return Result{"ok": true, "source": "agentdock-api", "action": "get", "task": task, "revision": taskstate.ManagementRevision(task)}, nil
}

func (s *Service) RuntimeTaskDelete(id, revision string) (Result, error) {
	task, err := s.tasks.Delete(strings.TrimSpace(id), revision)
	if err != nil {
		return nil, taskToolError(err)
	}
	return Result{
		"ok": true, "source": "agentdock-api", "action": "delete",
		"task_id": task.ID, "deleted_task": compactTaskSummary(task),
	}, nil
}

func (s *Service) RuntimeTaskArchive(id, revision string, archived bool) (Result, error) {
	task, err := s.tasks.SetArchived(id, revision, archived)
	if err != nil {
		return nil, taskToolError(err)
	}
	return Result{"ok": true, "task_id": task.ID, "revision": taskstate.ManagementRevision(task)}, nil
}
