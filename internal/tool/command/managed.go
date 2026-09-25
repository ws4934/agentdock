package command

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/sourceproof"
)

func (s *Service) Jobs() (*jobrun.Store, error) {
	s.jobOnce.Do(func() { s.jobs, s.jobErr = jobrun.New(filepath.Join(s.config().AgentDockHome, "jobs")) })
	return s.jobs, s.jobErr
}
func managedResult(record jobrun.Record) Result {
	return Result{"job_id": record.ID, "status": record.Status, "job": record, "observation_only": true, "observe_after_ms": 1000}
}
func (s *Service) startManaged(ctx context.Context, r ExecRequest, inv commandInvocation, timeout time.Duration) (Result, error) {
	if r.TTY || r.Stdin != "" || inv.build != nil {
		return nil, toolError("INVALID_ARGUMENT", "managed jobs require native non-interactive execution without stdin; use normal sessions for WSL/TTY", "validation")
	}
	if _, err := s.commandContext(); err != nil {
		return nil, err
	}
	jobs, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	record, err := jobs.Start(ctx, r.RequestID, jobrun.Spec{Command: r.Cmd, Workdir: inv.workdir, TimeoutMS: timeout.Milliseconds(), TaskID: r.TaskID, Title: r.Title}, inv.env)
	if err != nil {
		return nil, toolError("MANAGED_JOB_REJECTED", err.Error(), "validation")
	}
	return managedResult(record), nil
}

type JobObserveRequest struct {
	Action   string `json:"action,omitempty"`
	JobID    string `json:"job_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (s *Service) ObserveJob(ctx context.Context, r JobObserveRequest) (Result, error) {
	jobs, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	switch r.Action {
	case "list":
		records, partial, err := jobs.List(r.TaskID, r.Limit)
		return Result{"jobs": records, "count": len(records), "truncated": partial, "observation_only": true}, err
	case "logs":
		chunk, err := jobs.ReadLog(r.JobID, r.Stream, r.Offset, r.MaxBytes)
		return Result{"log": chunk, "observation_only": true}, err
	case "", "status", "evidence":
		record, err := jobs.Status(r.JobID)
		if err != nil {
			return nil, err
		}
		result := managedResult(record)
		if r.Action == "evidence" {
			freshness := "not_available"
			if e := record.Evidence; e != nil {
				snapshot := sourceproof.Capture(ctx, record.Workdir, e.SourceBefore.Scope)
				freshness = "unproven"
				if snapshot.Complete {
					freshness = "stale"
					if sourceproof.Matches(e.SourceBefore, e.SourceAfter) && sourceproof.Matches(snapshot, e.SourceAfter) {
						freshness = "current"
					}
				}
				result["current_source"] = snapshot
			}
			result["evidence_freshness"] = freshness
		}
		return result, nil
	default:
		return nil, toolError("INVALID_ARGUMENT", "unsupported job observation action", "validation")
	}
}

type JobControlRequest struct {
	Action string `json:"action"`
	JobID  string `json:"job_id"`
}

func (s *Service) ControlJob(ctx context.Context, r JobControlRequest) (Result, error) {
	jobs, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	var record jobrun.Record
	switch r.Action {
	case "cancel":
		record, err = jobs.Cancel(r.JobID)
	case "archive":
		record, err = jobs.Archive(ctx, r.JobID)
	default:
		return nil, toolError("INVALID_ARGUMENT", "action must be cancel or archive", "validation")
	}
	if err != nil {
		return nil, err
	}
	return managedResult(record), nil
}

type ValidationRequest struct {
	RequestID   string   `json:"request_id"`
	Workdir     string   `json:"workdir,omitempty"`
	TaskID      string   `json:"task_id,omitempty"`
	Title       string   `json:"title,omitempty"`
	Adapter     string   `json:"adapter"`
	Packages    []string `json:"packages,omitempty"`
	Filter      string   `json:"filter,omitempty"`
	Race        bool     `json:"race,omitempty"`
	Argv        []string `json:"argv,omitempty"`
	SourcePaths []string `json:"source_paths,omitempty"`
	TimeoutMS   int64    `json:"timeout_ms,omitempty"`
}

func (s *Service) RunValidation(ctx context.Context, r ValidationRequest) (Result, error) {
	if _, err := s.commandContext(); err != nil {
		return nil, err
	}
	workdir, err := s.ws.ResolveExisting(r.Workdir)
	if err != nil {
		return nil, err
	}
	argv := r.Argv
	switch r.Adapter {
	case "go_test":
		if len(r.Argv) > 0 {
			return nil, errors.New("go_test uses typed packages/filter/race, not argv")
		}
		argv = []string{"go", "test", "-json", "-count=1"}
		if r.Race {
			argv = append(argv, "-race")
		}
		if r.Filter != "" {
			if len(r.Filter) > 1000 {
				return nil, errors.New("filter too long")
			}
			argv = append(argv, "-run", r.Filter)
		}
		if len(r.Packages) == 0 {
			r.Packages = []string{"./..."}
		}
		if len(r.Packages) > 64 {
			return nil, errors.New("too many packages")
		}
		for _, p := range r.Packages {
			if p != "." && !strings.HasPrefix(p, "./") {
				return nil, errors.New("packages must be relative to the project")
			}
			for _, part := range strings.Split(p, "/") {
				if part == ".." || strings.ContainsAny(part, "\x00\\ ") {
					return nil, errors.New("invalid package path")
				}
			}
			argv = append(argv, p)
		}
	case "junit", "process":
		if len(r.Packages) > 0 || r.Filter != "" || r.Race {
			return nil, errors.New("packages/filter/race are specific to go_test")
		}
		if len(argv) == 0 {
			return nil, errors.New("argv is required")
		}
	default:
		return nil, errors.New("adapter must be go_test, junit or process")
	}
	env, err := s.InternalCommandEnv(nil)
	if err != nil {
		return nil, err
	}
	jobs, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 900000
	}
	record, err := jobs.Start(ctx, r.RequestID, jobrun.Spec{Argv: argv, Workdir: workdir.Abs, TaskID: r.TaskID, Title: r.Title, TimeoutMS: r.TimeoutMS, Validation: &jobrun.ValidationSpec{Adapter: r.Adapter, SourcePaths: r.SourcePaths}}, env)
	if err != nil {
		return nil, err
	}
	return managedResult(record), nil
}
