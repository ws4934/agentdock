package workresult

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/sourceproof"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func testService(t *testing.T) (*Service, *taskstate.Task, *jobrun.Store, string) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	_ = os.WriteFile(filepath.Join(root, "source.go"), []byte("package example\n"), 0600)
	task := &taskstate.Task{ID: "fixture-task", Status: taskstate.StatusCompleted, UpdatedAt: time.Now().UTC(), Title: "Delivery"}
	jobs, err := jobrun.New(filepath.Join(home, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(home, func(_ context.Context, id string) (taskstate.Task, error) {
		if id != task.ID {
			return taskstate.Task{}, errors.New("unknown task")
		}
		return *task, nil
	}, func() (*jobrun.Store, error) { return jobs, nil }, publicartifacts.New(home, "", 0))
	return s, task, jobs, root
}
func TestFrozenDeliveryIsImmutableAndIdempotent(t *testing.T) {
	s, task, _, root := testService(t)
	request := Request{TaskID: task.ID, Workdir: root, SourcePaths: []string{"source.go"}}
	live, err := s.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if live.Validation != "not_verified" {
		t.Fatal("operator completion became validation")
	}
	request.RequestID = "final"
	request.ExpectedSourceRevision = live.Source.Revision
	frozen, err := s.Freeze(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.Frozen || frozen.ResultID == "" {
		t.Fatalf("%#v", frozen)
	}
	dataBefore, err := os.ReadFile(filepath.Join(s.root, frozen.ResultID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "source.go"), []byte("package later\n"), 0600)
	again, err := s.Freeze(t.Context(), request)
	if err != nil || again.ResultID != frozen.ResultID {
		t.Fatal("receipt recovery changed frozen delivery")
	}
	read, err := s.Read(t.Context(), Request{ResultID: frozen.ResultID})
	if err != nil {
		t.Fatal(err)
	}
	if read.Source.Revision != frozen.Source.Revision {
		t.Fatal("frozen read refreshed mutable source")
	}
	dataAfter, _ := os.ReadFile(filepath.Join(s.root, frozen.ResultID+".json"))
	if string(dataBefore) != string(dataAfter) {
		t.Fatal("immutable bytes changed")
	}
	request.RequestID = "another"
	if _, err = s.Freeze(t.Context(), request); err == nil {
		t.Fatal("stale source frozen")
	}
}
func TestFrozenDeliveryRejectsLiveTaskAndUnknownExecution(t *testing.T) {
	s, task, jobs, root := testService(t)
	request := Request{TaskID: task.ID, Workdir: root, SourcePaths: []string{"source.go"}, RequestID: "freeze"}
	live, err := s.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedSourceRevision = live.Source.Revision
	task.Status = taskstate.StatusActive
	if _, err = s.Freeze(t.Context(), request); err == nil {
		t.Fatal("active task frozen")
	}
	task.Status = taskstate.StatusCompleted
	id := "job_" + strings.Repeat("a", 32)
	dir := filepath.Join(jobs.Root, id)
	_ = os.Mkdir(dir, 0700)
	r := jobrun.Record{SchemaVersion: 1, ID: id, Status: "running", Workdir: root, TaskID: task.ID, CreatedAt: time.Now().Add(-time.Hour)}
	b, _ := json.Marshal(r)
	_ = os.WriteFile(filepath.Join(dir, "record.json"), b, 0600)
	if _, err = s.Freeze(t.Context(), request); err == nil {
		t.Fatal("unknown execution frozen as a confirmed delivery")
	}
}
func TestSourceBoundEvidenceAndTaskScope(t *testing.T) {
	s, task, jobs, root := testService(t)
	source := sourceproof.Capture(t.Context(), root, []string{"source.go"})
	now := time.Now().UTC()
	code := 0
	id := "job_" + strings.Repeat("b", 32)
	dir := filepath.Join(jobs.Root, id)
	_ = os.Mkdir(dir, 0700)
	r := jobrun.Record{SchemaVersion: 1, ID: id, Status: "succeeded", TaskID: task.ID, Workdir: root, CreatedAt: now, FinishedAt: &now, ExitCode: &code, Evidence: &jobrun.Evidence{Adapter: "go_test", Status: "passed", Tests: 1, Passed: 1, ReportComplete: true, SourceBefore: source, SourceAfter: source}}
	write := func() { b, _ := json.Marshal(r); _ = os.WriteFile(filepath.Join(dir, "record.json"), b, 0600) }
	write()
	request := Request{TaskID: task.ID, Workdir: root, JobIDs: []string{id}, SourcePaths: []string{"source.go"}}
	live, err := s.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Jobs) != 1 || live.Jobs[0].Freshness != "current" {
		t.Fatalf("%#v", live)
	}
	_ = os.WriteFile(filepath.Join(root, "source.go"), []byte("package changed\n"), 0600)
	live, err = s.Read(t.Context(), request)
	if err != nil || live.Jobs[0].Freshness != "stale" || strings.HasPrefix(live.Validation, "current_evidence") {
		t.Fatalf("%#v %v", live, err)
	}
	r.TaskID = "another-task"
	write()
	if _, err = s.Read(t.Context(), request); err == nil {
		t.Fatal("another task evidence was mixed into delivery")
	}
}
