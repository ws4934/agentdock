package jobrun

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/sourceproof"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

type cappedLog struct {
	mu      sync.Mutex
	file    *os.File
	state   LogState
	failure error
}

func newLog(path string) (*cappedLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = securepath.EnsurePrivate(path); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &cappedLog{file: f}, nil
}
func (l *cappedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	l.state.Total += int64(n)
	kept := p[:min(int64(len(p)), MaxLogBytes-l.state.Retained)]
	if len(kept) > 0 {
		written, err := l.file.Write(kept)
		l.state.Retained += int64(written)
		if err != nil {
			l.failure = err
		}
	}
	l.state.Dropped = l.state.Total - l.state.Retained
	// Drain all output even after retention is full; never deadlock a noisy child.
	return n, nil
}
func (l *cappedLog) snapshot() (LogState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, errors.Join(l.failure, l.file.Sync())
}

// Supervise executes exactly once after claiming the durable record. It must be
// entered directly by the binary, before Core initialization or service startup.
func Supervise(ctx context.Context, root, id string) error {
	store, err := New(root)
	if err != nil {
		return err
	}
	dir, err := store.directory(id)
	if err != nil {
		return err
	}
	owner, claimed, err := processlock.TryAcquire(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return err
	}
	if !claimed {
		return errors.New("job already has a supervisor")
	}
	defer owner.Release()
	var record Record
	if err = readJSON(filepath.Join(dir, "record.json"), &record); err != nil {
		return err
	}
	if record.ID != id || record.SchemaVersion != SchemaVersion || record.Status != "starting" || record.StartedAt != nil {
		return errors.New("job is already claimed; execution will not be replayed")
	}
	finish := func(state, reason string) error {
		now := time.Now().UTC()
		record.Status = state
		record.Failure = reason
		record.FinishedAt = &now
		record.OwnerAlive = false
		if record.Evidence != nil && (state == "cancelled" || state == "timed_out" || state == "outcome_unknown") {
			record.Evidence.Status = "inconclusive"
			record.Evidence.Reason = "execution_not_confirmed_successfully_completed"
		}
		return writeJSON(filepath.Join(dir, "record.json"), record)
	}
	if time.Since(record.CreatedAt) > 10*time.Second || (record.BootID != "" && bootID() != "" && record.BootID != bootID()) {
		return finish("launch_expired", "supervisor missed launch deadline; no command was run")
	}
	var spec Spec
	if err = readJSON(filepath.Join(dir, "request.json"), &spec); err != nil {
		return finish("launch_failed", "invalid_execution_request")
	}
	if spec.TimeoutMS <= 0 || spec.TimeoutMS > 86400000 || spec.Workdir != record.Workdir {
		return finish("launch_failed", "invalid_execution_request")
	}
	if spec.Validation != nil {
		if err = validateAdapter(spec); err != nil {
			return finish("launch_failed", "invalid_validation_request")
		}
	}
	record.Status = "preparing"
	record.OwnerAlive = true
	if err = writeJSON(filepath.Join(dir, "record.json"), record); err != nil {
		return err
	}
	execution, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
	defer cancel()
	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watcherDone:
				return
			case <-execution.Done():
				return
			case <-ticker.C:
				if _, err := os.Lstat(filepath.Join(dir, "cancel.request")); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	if _, err = os.Lstat(filepath.Join(dir, "cancel.request")); err == nil {
		cancel()
	}
	var before sourceproof.Snapshot
	if spec.Validation != nil {
		before = sourceproof.Capture(execution, spec.Workdir, spec.Validation.SourcePaths)
		if !before.Complete {
			record.Evidence = &Evidence{Adapter: spec.Validation.Adapter, Status: "inconclusive", Reason: "source_state_unproven", SourceBefore: before}
			return finish("validation_not_started", "source_state_unproven")
		}
	}
	if execution.Err() != nil {
		return finish("cancelled", "cancelled_before_command_start")
	}
	stdout, err := newLog(filepath.Join(dir, "stdout.log"))
	if err != nil {
		return finish("launch_failed", "stdout_unavailable")
	}
	defer stdout.file.Close()
	stderr, err := newLog(filepath.Join(dir, "stderr.log"))
	if err != nil {
		return finish("launch_failed", "stderr_unavailable")
	}
	defer stderr.file.Close()
	var command *exec.Cmd
	if spec.Command != "" {
		command = session.ShellCommand(execution, spec.Command)
	} else {
		argv := append([]string(nil), spec.Argv...)
		if len(argv) == 0 {
			return finish("launch_failed", "empty_command")
		}
		for i := range argv {
			argv[i] = strings.ReplaceAll(argv[i], "{report}", filepath.Join(dir, "validation.xml"))
		}
		command = exec.Command(argv[0], argv[1:]...)
	}
	command.Dir = spec.Workdir
	command.Env = os.Environ()
	collector := &goCollector{}
	var out io.Writer = stdout
	if spec.Validation != nil && spec.Validation.Adapter == "go_test" {
		out = io.MultiWriter(stdout, collector)
	}
	now := time.Now().UTC()
	record.StartedAt = &now
	record.Status = "running"
	if err = writeJSON(filepath.Join(dir, "record.json"), record); err != nil {
		return err
	}
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		code, err := session.RunOwnedTracked(execution, command, out, stderr, executionGroup(id), func(pid int) error {
			identity := ExecutionIdentity{JobID: id, BootID: record.BootID, PID: pid, Group: executionGroup(id)}
			return writeJSON(filepath.Join(dir, "execution.json"), identity)
		})
		done <- outcome{code, err}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	persistFailed := false
	var result outcome
running:
	for {
		select {
		case result = <-done:
			break running
		case <-ticker.C:
			var outErr, errErr error
			record.Stdout, outErr = stdout.snapshot()
			record.Stderr, errErr = stderr.snapshot()
			if outErr != nil || errErr != nil || writeJSON(filepath.Join(dir, "record.json"), record) != nil {
				persistFailed = true
				cancel()
			}
		}
	}
	var outErr, errErr error
	record.Stdout, outErr = stdout.snapshot()
	record.Stderr, errErr = stderr.snapshot()
	persistFailed = persistFailed || outErr != nil || errErr != nil
	record.ExitCode = &result.code
	var identity ExecutionIdentity
	if readJSON(filepath.Join(dir, "execution.json"), &identity) == nil && validExecution(record, identity) {
		record.Execution = &identity
	}
	if spec.Validation != nil {
		observation := testObservation{}
		switch spec.Validation.Adapter {
		case "go_test":
			observation = collector.result()
		case "junit":
			observation = readJUnit(filepath.Join(dir, "validation.xml"))
		}
		proofCtx, stopProof := context.WithTimeout(context.Background(), 30*time.Second)
		after := sourceproof.Capture(proofCtx, spec.Workdir, spec.Validation.SourcePaths)
		stopProof()
		record.Evidence = finishEvidence(spec, result.code, observation, before, after)
	}
	switch {
	case errors.Is(result.err, session.ErrProcessUndrained):
		return finish("outcome_unknown", "process_cleanup_unconfirmed")
	case persistFailed:
		return finish("outcome_unknown", "execution_log_persistence_failed")
	case errors.Is(execution.Err(), context.DeadlineExceeded):
		return finish("timed_out", "execution_timeout")
	case execution.Err() != nil:
		return finish("cancelled", "execution_cancelled")
	case result.err != nil || result.code != 0:
		return finish("failed", "process_exit_nonzero_or_launch_failed")
	default:
		return finish("succeeded", "")
	}
}
