package taskstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var (
	ErrTaskConflict           = errors.New("task changed; refresh before managing it")
	ErrTaskNotCompleted       = errors.New("unfinished tasks can be archived, not deleted")
	managementRevisionPattern = regexp.MustCompile(`^tsk1:[a-f0-9]{64}$`)
)

// ManagementReference 精确限定用户看到并确认的记录；不是通配清理条件。
type ManagementReference struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type ManagementRequest struct {
	Action string                `json:"action"`
	Tasks  []ManagementReference `json:"tasks"`
}

func (r ManagementRequest) Validate() error {
	if r.Action != "archive" && r.Action != "restore" && r.Action != "delete" {
		return errors.New("unsupported task management action")
	}
	if len(r.Tasks) == 0 || len(r.Tasks) > 200 {
		return errors.New("select between 1 and 200 tasks")
	}
	seen := make(map[string]bool, len(r.Tasks))
	for _, task := range r.Tasks {
		if err := validateID(task.ID); err != nil {
			return err
		}
		if seen[task.ID] || !managementRevisionPattern.MatchString(task.Revision) {
			return errors.New("duplicate task or invalid task revision")
		}
		seen[task.ID] = true
	}
	return nil
}

func ManagementRevision(task Task) string {
	data, _ := json.Marshal(task)
	sum := sha256.Sum256(data)
	return "tsk1:" + hex.EncodeToString(sum[:])
}

func checkManagementRevision(task Task, expected string) error {
	if !managementRevisionPattern.MatchString(expected) || ManagementRevision(task) != expected {
		return ErrTaskConflict
	}
	return nil
}

// 归档只改变列表可见性。它不会取消执行、改变步骤或使验收自动通过；精确 ID 仍可读取。
func (s *Store) SetArchived(id, expectedRevision string, archived bool) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()
	task, err := s.loadLocked(id)
	if err != nil {
		return Task{}, err
	}
	if err := checkManagementRevision(task, expectedRevision); err != nil {
		return Task{}, err
	}
	if (task.ArchivedAt != nil) == archived {
		return task, nil
	}
	now := time.Now().UTC()
	task.ArchivedAt = nil
	if archived {
		task.ArchivedAt = &now
	}
	task.UpdatedAt = now
	if err := s.saveLocked(task); err != nil {
		return Task{}, err
	}
	return task, nil
}
