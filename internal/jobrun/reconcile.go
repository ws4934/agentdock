package jobrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/fs/processlock"
	processcontrol "github.com/uvwt/agentdock/internal/process"
)

var bootID = sync.OnceValue(platformBootID)

type ExecutionIdentity struct {
	JobID  string `json:"job_id"`
	BootID string `json:"boot_id"`
	PID    int    `json:"pid"`
	Group  string `json:"group"`
}

func executionGroup(id string) string { return "Local\\AgentDock-" + id }

// 可变状态损坏时只使用不可变的初始回执恢复身份，不恢复成功结论。
func readRecordState(dir, id string) (Record, error) {
	var r Record
	err := readJSON(filepath.Join(dir, "record.json"), &r)
	if err != nil || r.SchemaVersion != SchemaVersion || r.ID != id {
		r = Record{}
		if err := readJSON(filepath.Join(dir, "receipt.json"), &r); err != nil || r.SchemaVersion != SchemaVersion || r.ID != id {
			return Record{}, errors.New("job state and admission receipt unavailable; execution must not be replayed")
		}
		r.Status = "outcome_unknown"
		r.StateUnavailable = true
		r.Failure = "mutable_record_unavailable; initial_identity_only"
		r.FinishedAt = nil
		r.Evidence = nil
	}
	var execution ExecutionIdentity
	if readJSON(filepath.Join(dir, "execution.json"), &execution) == nil && validExecution(r, execution) {
		r.Execution = &execution
	}
	return r, nil
}
func validExecution(r Record, e ExecutionIdentity) bool {
	return e.JobID == r.ID && e.PID > 0 && e.Group == executionGroup(r.ID) && e.BootID == r.BootID
}

// Abandon 是显式放弃未知结果，不是重试、成功或测试通过。持有准入和 owner
// 锁到写入终态，防止一个迟到的 supervisor 在核对后重新取得执行权。
func (s *Store) Abandon(ctx context.Context, id string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	guard, err := lock(ctx, filepath.Join(s.Root, "admission.lock"))
	if err != nil {
		return Record{}, err
	}
	defer guard.Release()
	dir, err := s.directory(id)
	if err != nil {
		return Record{}, err
	}
	ownerPath := filepath.Join(dir, "owner.lock")
	if err := regular(ownerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	owner, available, err := processlock.TryAcquire(ownerPath)
	if err != nil {
		return Record{}, err
	}
	if !available {
		return Record{}, errors.New("job still has a supervisor; cancel and observe it first")
	}
	defer owner.Release()
	r, err := readRecordState(dir, id)
	if err != nil {
		// 旧版本的半条记录也保留原 ID；缺少进程身份时只能经后续启动代际证明释放。
		r = Record{SchemaVersion: SchemaVersion, ID: id, Status: "state_unavailable", StateUnavailable: true, ObservationOnly: true}
	}
	r.OwnerAlive = false
	if r.Status == "abandoned" {
		return r, nil
	}
	if r.Terminal() {
		return Record{}, errors.New("confirmed terminal jobs do not need abandonment")
	}
	if time.Since(r.CreatedAt) < 10*time.Second {
		return Record{}, errors.New("job launch window is still open")
	}
	basis := ""
	switch {
	case r.BootID != "" && bootID() != "" && r.BootID != bootID():
		basis = "previous_boot_ended"
	case !r.StateUnavailable && r.StartedAt == nil && (r.Status == "starting" || r.Status == "preparing"):
		basis = "command_not_started"
	case r.Execution != nil && validExecution(r, *r.Execution) && processcontrol.ExecutionGone(r.Execution.PID, r.Execution.Group):
		basis = "owned_process_group_ended"
	default:
		var recoveryErr error
		basis, recoveryErr = observeRecoveryBoot(dir, id)
		if recoveryErr != nil {
			return Record{}, recoveryErr
		}
	}
	now := time.Now().UTC()
	r.Status = "abandoned"
	r.FinishedAt = &now
	r.StateUnavailable = false
	r.ResolutionBasis = basis
	r.Failure = "explicitly_abandoned; execution_outcome_unverified"
	if r.Evidence != nil {
		r.Evidence.Status = "inconclusive"
		r.Evidence.Reason = "execution_abandoned_without_outcome_proof"
	}
	if err = writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
		return Record{}, fmt.Errorf("persist abandonment: %w", err)
	}
	return r, nil
}

// 没有旧进程身份时，不根据文件时间或操作者布尔断言猜测退出。首次显式
// abandon 只保存当前启动代际；后续显式调用观察到真正重启，才有退出证据。
func observeRecoveryBoot(dir, id string) (string, error) {
	current := bootID()
	if current == "" {
		return "", errors.New("PROCESS_EXIT_UNPROVEN: OS boot identity unavailable; no capacity released")
	}
	type observation struct {
		JobID      string    `json:"job_id"`
		BootID     string    `json:"boot_id"`
		ObservedAt time.Time `json:"observed_at"`
	}
	path := filepath.Join(dir, "recovery-observation.json")
	var old observation
	err := readJSON(path, &old)
	if err == nil {
		if old.JobID != id || old.BootID == "" || old.ObservedAt.IsZero() {
			return "", errors.New("invalid recovery observation; no capacity released")
		}
		if old.BootID != current {
			return "recovery_observation_before_boot", nil
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(path, observation{JobID: id, BootID: current, ObservedAt: time.Now().UTC()}); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	return "", errors.New("PROCESS_EXIT_UNPROVEN: boot observation saved, no capacity released; after an operator-controlled OS reboot explicitly repeat abandon with this job_id; never replay the command")
}
