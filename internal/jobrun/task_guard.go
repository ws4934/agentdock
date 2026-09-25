package jobrun

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

var ErrTaskJobsBusy = errors.New("task has live or non-terminal jobs")
var ErrTaskJobsUnavailable = errors.New("job state is unavailable; task deletion is unsafe")

func (s *Store) WithTaskIdle(ctx context.Context, taskID string, remove func() error) error {
	return s.WithIdleTasks(ctx, []string{taskID}, func(blocked map[string]error) error {
		if err := blocked[taskID]; err != nil {
			return err
		}
		return remove()
	})
}

// WithIdleTasks 与准入共用锁；批量清理只扫描一次完整索引，不能把十条预览当成全量。
// 回调只可删除任务元数据，不得发起 Job 或修改其日志、回执。归档 Job 仍保留防重放凭据。
func (s *Store) WithIdleTasks(ctx context.Context, taskIDs []string, apply func(map[string]error) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	guard, err := lock(ctx, filepath.Join(s.Root, "admission.lock"))
	if err != nil {
		return err
	}
	defer guard.Release()
	selected := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		selected[id] = true
	}
	entries, err := s.index()
	if err != nil {
		return ErrTaskJobsUnavailable
	}
	blocked := make(map[string]error)
	for _, entry := range entries {
		// 无法归属的损坏记录不能被视为与这些任务无关。
		if entry.Unavailable {
			return ErrTaskJobsUnavailable
		}
		if !selected[entry.TaskID] {
			continue
		}
		record, err := s.Status(entry.ID)
		if err != nil || record.StateUnavailable {
			return ErrTaskJobsUnavailable
		}
		if record.OwnerAlive || !record.Terminal() {
			blocked[entry.TaskID] = ErrTaskJobsBusy
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return apply(blocked)
}
