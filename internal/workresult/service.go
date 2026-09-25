// Package workresult separates live observations from immutable work deliveries.
// Human/model review remains an assertion; job evidence is recorded independently.
package workresult

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/sourceproof"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type TaskSource func(context.Context, string) (taskstate.Task, error)
type JobsSource func() (*jobrun.Store, error)
type Service struct {
	root      string
	tasks     TaskSource
	jobs      JobsSource
	artifacts publicartifacts.Store
}

func New(home string, tasks TaskSource, jobs JobsSource, artifacts publicartifacts.Store) *Service {
	return &Service{root: filepath.Join(home, "work-results"), tasks: tasks, jobs: jobs, artifacts: artifacts}
}

type Request struct {
	TaskID                 string   `json:"task_id,omitempty"`
	Workdir                string   `json:"workdir,omitempty"`
	ResultID               string   `json:"result_id,omitempty"`
	RequestID              string   `json:"request_id,omitempty"`
	ExpectedSourceRevision string   `json:"expected_source_revision,omitempty"`
	JobIDs                 []string `json:"job_ids,omitempty"`
	ArtifactIDs            []string `json:"artifact_ids,omitempty"`
	SourcePaths            []string `json:"source_paths,omitempty"`
}
type JobEvidence struct {
	Job       jobrun.Record `json:"job"`
	Freshness string        `json:"freshness"`
	Scope     string        `json:"scope"`
}
type Projection struct {
	Selection        Request                    `json:"selection"`
	SchemaVersion    int                        `json:"schema_version"`
	ResultID         string                     `json:"result_id,omitempty"`
	RequestHash      string                     `json:"request_hash,omitempty"`
	Frozen           bool                       `json:"frozen"`
	ObservedAt       time.Time                  `json:"observed_at"`
	Task             taskstate.Task             `json:"task"`
	Workdir          string                     `json:"workdir"`
	Source           sourceproof.Snapshot       `json:"source"`
	Changes          []string                   `json:"changes"`
	ChangesAvailable bool                       `json:"changes_available"`
	ChangesPartial   bool                       `json:"changes_partial"`
	Jobs             []JobEvidence              `json:"jobs"`
	JobsPartial      bool                       `json:"jobs_partial"`
	Artifacts        []publicartifacts.Metadata `json:"artifacts"`
	Validation       string                     `json:"validation"`
	ReviewAuthority  string                     `json:"review_authority"`
	ObservationOnly  bool                       `json:"observation_only"`
}

var resultID = regexp.MustCompile(`^result_[a-f0-9]{32}$`)
var requestID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func hash(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) Task(ctx context.Context, id string) (taskstate.Task, error) {
	if id == "" {
		return taskstate.Task{}, errors.New("task_id is required")
	}
	t, err := s.tasks(ctx, id)
	if err != nil {
		return t, err
	}
	if t.ID != id {
		return t, errors.New("task source returned a different task")
	}
	return t, nil
}
func (s *Service) Read(ctx context.Context, r Request) (Projection, error) {
	if r.ResultID != "" {
		if r.TaskID != "" || r.Workdir != "" || len(r.JobIDs) > 0 || len(r.ArtifactIDs) > 0 || len(r.SourcePaths) > 0 {
			return Projection{}, errors.New("frozen result_id reads do not accept live selection fields")
		}
		return s.readFrozen(ctx, r.ResultID)
	}
	if len(r.JobIDs) > 32 || len(r.ArtifactIDs) > 8 || len(r.SourcePaths) > 128 {
		return Projection{}, errors.New("work result selection exceeds limits")
	}
	task, err := s.Task(ctx, r.TaskID)
	if err != nil {
		return Projection{}, err
	}
	root, err := sourceproof.CanonicalRoot(r.Workdir)
	if err != nil {
		return Projection{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	source := sourceproof.Capture(ctx, root, r.SourcePaths)
	result := Projection{SchemaVersion: 1, ObservedAt: time.Now().UTC(), Task: task, Workdir: root, Source: source, Changes: []string{}, Jobs: []JobEvidence{}, Artifacts: []publicartifacts.Metadata{}, Validation: "not_verified", ReviewAuthority: "task.final_review is model/operator assertion, not machine execution proof", ObservationOnly: true}
	// A compact path/status map is not an LLM review and never means code is safe.
	if data, err := sourceproof.Git(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=normal"); err == nil {
		result.ChangesAvailable = true
		fields := strings.Split(string(data), "\x00")
		for _, field := range fields {
			if field == "" {
				continue
			}
			if len(result.Changes) >= 128 {
				result.ChangesPartial = true
				break
			}
			if len(field) > 1024 {
				field = field[:1024]
				result.ChangesPartial = true
			}
			result.Changes = append(result.Changes, field)
		}
	}
	jobs, err := s.jobs()
	if err != nil {
		return result, err
	}
	var records []jobrun.Record
	if len(r.JobIDs) > 0 {
		seen := map[string]bool{}
		for _, id := range r.JobIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			j, err := jobs.Status(id)
			if err != nil {
				return result, err
			}
			records = append(records, j)
		}
	} else {
		records, result.JobsPartial, err = jobs.List(r.TaskID, 32)
		if err != nil {
			return result, err
		}
	}
	result.Selection = Request{TaskID: r.TaskID, Workdir: root, JobIDs: append([]string(nil), r.JobIDs...), ArtifactIDs: append([]string(nil), r.ArtifactIDs...), SourcePaths: append([]string(nil), r.SourcePaths...)}
	hasProof := false
	hasFailure := false
	hasCurrentEvidence := false
	snapshots := map[string]sourceproof.Snapshot{hash(r.SourcePaths): source}
	for _, j := range records {
		if j.TaskID != r.TaskID || j.Workdir != root {
			return result, errors.New("selected job belongs to another task or project")
		}
		evidence := JobEvidence{Job: j, Freshness: "not_available", Scope: "not_applicable"}
		if e := j.Evidence; e != nil {
			evidence.Scope = "selected_paths"
			if len(e.SourceBefore.Scope) == 0 {
				evidence.Scope = "git_tracked_and_unignored_files"
			}
			k := hash(e.SourceBefore.Scope)
			fresh, ok := snapshots[k]
			if !ok {
				fresh = sourceproof.Capture(ctx, root, e.SourceBefore.Scope)
				snapshots[k] = fresh
			}
			evidence.Freshness = "unproven"
			if fresh.Complete && e.SourceBefore.Complete && e.SourceAfter.Complete {
				evidence.Freshness = "stale"
				if sourceproof.Matches(fresh, e.SourceAfter) && sourceproof.Matches(e.SourceBefore, e.SourceAfter) {
					evidence.Freshness = "current"
				}
			}
			if evidence.Freshness == "current" {
				hasCurrentEvidence = true
				if j.Status == "succeeded" && (e.Status == "passed" || e.Status == "process_passed") {
					hasProof = true
				}
				if j.Status == "failed" || e.Status == "failed" {
					hasFailure = true
				}
			}
		}
		result.Jobs = append(result.Jobs, evidence)
	}
	switch {
	case hasFailure:
		result.Validation = "current_failures_present"
	case hasProof:
		result.Validation = "current_evidence_available_not_coverage_guarantee"
	case hasCurrentEvidence:
		result.Validation = "inconclusive"
	case len(records) > 0:
		result.Validation = "no_current_validation_proof"
	}
	seen := map[string]bool{}
	for _, id := range r.ArtifactIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		meta, _, _, err := s.artifacts.ReadChunk(id, 0, 0)
		if err != nil {
			return result, err
		}
		result.Artifacts = append(result.Artifacts, meta)
	}
	return result, nil
}
func (s *Service) prepare() error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid work result state directory")
	}
	return securepath.EnsurePrivate(s.root)
}
func (s *Service) Freeze(ctx context.Context, r Request) (Projection, error) {
	if !requestID.MatchString(r.RequestID) || r.ResultID != "" {
		return Projection{}, errors.New("freeze requires a stable request_id and no result_id")
	}
	if r.ExpectedSourceRevision == "" {
		return Projection{}, errors.New("freeze requires expected_source_revision from a live result read")
	}
	if err := s.prepare(); err != nil {
		return Projection{}, err
	}
	deadline, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	guard, err := processlock.Acquire(deadline, filepath.Join(s.root, "write.lock"))
	if err != nil {
		return Projection{}, err
	}
	defer guard.Release()
	id := "result_" + hash([]string{r.TaskID, r.RequestID})[:32]
	key := hash(r)
	if old, err := s.readFrozen(ctx, id); err == nil {
		if old.RequestHash != key {
			return Projection{}, errors.New("RESULT_IDEMPOTENCY_CONFLICT")
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Projection{}, err
	}
	p, err := s.Read(ctx, r)
	if err != nil {
		return Projection{}, err
	}
	if p.Task.Status != taskstate.StatusCompleted {
		return Projection{}, errors.New("only completed tasks can be frozen; view active tasks through work_result_read")
	}
	if !p.Source.Complete || p.Source.Revision != r.ExpectedSourceRevision {
		return Projection{}, errors.New("SOURCE_REVISION_CONFLICT: source no longer matches the reviewed result")
	}
	if p.JobsPartial {
		return Projection{}, errors.New("job selection is partial; choose explicit job_ids")
	}
	for _, j := range p.Jobs {
		if !j.Job.Terminal() || j.Job.OwnerAlive {
			return Projection{}, errors.New("cannot freeze with unresolved or running jobs")
		}
	}
	// Fence the collection window. This is still an observation, not a filesystem lock.
	after := sourceproof.Capture(ctx, p.Workdir, r.SourcePaths)
	if !sourceproof.Matches(p.Source, after) {
		return Projection{}, errors.New("SOURCE_REVISION_CONFLICT: source changed while collecting delivery")
	}
	latest, err := s.Task(ctx, r.TaskID)
	if err != nil {
		return Projection{}, err
	}
	if !latest.UpdatedAt.Equal(p.Task.UpdatedAt) {
		return Projection{}, errors.New("TASK_CHANGED: read the task again")
	}
	p.ResultID = id
	p.RequestHash = key
	p.Frozen = true
	data, err := json.Marshal(p)
	if err != nil {
		return Projection{}, err
	}
	if len(data) > 2<<20 {
		return Projection{}, errors.New("frozen result exceeds 2 MiB limit")
	}
	if err = atomicfile.Write(filepath.Join(s.root, id+".json"), data, 0600); err != nil {
		return Projection{}, err
	}
	return p, nil
}
func (s *Service) readFrozen(ctx context.Context, id string) (Projection, error) {
	if !resultID.MatchString(id) {
		return Projection{}, errors.New("invalid result_id")
	}
	path := filepath.Join(s.root, id+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return Projection{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return Projection{}, errors.New("invalid result file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Projection{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		return Projection{}, err
	}
	if len(b) > 2<<20 {
		return Projection{}, errors.New("result size exceeded")
	}
	var p Projection
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if p.SchemaVersion != 1 || p.ResultID != id || !p.Frozen {
		return p, errors.New("unsupported or corrupt result")
	}
	// Existence/authority checks are refreshed; frozen observations never are.
	if _, err = s.Task(ctx, p.Task.ID); err != nil {
		return p, err
	}
	return p, nil
}
func (s *Service) List(ctx context.Context, taskID string) ([]string, bool, error) {
	if _, err := s.Task(ctx, taskID); err != nil {
		return nil, false, err
	}
	file, err := os.Open(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	entries, err := file.ReadDir(1000)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	partial := len(entries) == 1000
	ids := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !resultID.MatchString(id) {
			continue
		}
		p, err := s.readFrozen(ctx, id)
		if err != nil {
			return nil, false, fmt.Errorf("read frozen delivery: %w", err)
		}
		if p.Task.ID == taskID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, partial, nil
}
