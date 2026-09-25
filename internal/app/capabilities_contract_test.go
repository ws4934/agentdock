package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/workresult"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "job-supervise" {
		if err := jobrun.Supervise(context.Background(), os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "capability-fixture" {
		fmt.Println("completed fixture")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func waitCapabilityJob(t *testing.T, r *Runtime, id string) jobrun.Record {
	t.Helper()
	jobs, err := r.command.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		record, err := jobs.Status(id)
		if err != nil {
			t.Fatal(err)
		}
		if record.Terminal() && !record.OwnerAlive {
			return record
		}
		if record.Status == "outcome_unknown" {
			t.Fatalf("unknown job %#v", record)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job not completed")
	return jobrun.Record{}
}
func TestRuntimeSuccessOutputsValidateNewCapabilities(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	root := r.ws.DefaultCWD()
	path := filepath.Join(root, "capability-source.txt")
	_ = os.WriteFile(path, []byte("needle\nsecond\n"), 0600)
	call := func(tool string, args map[string]any) Result {
		t.Helper()
		result, err := r.Call(t.Context(), tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		assertToolResultMatchestestOutputSchema(t, tool, result)
		return result
	}
	call("read_files", map[string]any{"requests": []map[string]any{{"path": path, "start_line": 1, "end_line": 2}}})
	call("search_and_read", map[string]any{"path": root, "query": "needle"})
	call("job_observe", map[string]any{"action": "list"})
	call("code_navigate", map[string]any{"action": "status"})
	call("worktree_manage", map[string]any{"action": "list"})
	created := call("task_manage", map[string]any{"action": "create", "title": "Capability fixture", "goal": "Validate the public tool outputs", "completion_conditions": []string{"Fixture process completed"}, "steps": []map[string]any{{"id": "verify", "title": "Verify fixture"}}})
	taskID := created["task_id"].(string)
	binary, _ := os.Executable()
	result := call("validation_run", map[string]any{"adapter": "process", "request_id": "schema-validation", "task_id": taskID, "workdir": root, "argv": []string{binary, "capability-fixture"}, "source_paths": []string{"capability-source.txt"}})
	id := result["job_id"].(string)
	record := waitCapabilityJob(t, r, id)
	if record.Evidence == nil || record.Evidence.Status != "process_passed" {
		t.Fatalf("%#v", record)
	}
	call("job_observe", map[string]any{"action": "status", "job_id": id})
	call("job_observe", map[string]any{"action": "logs", "job_id": id, "stream": "stdout", "offset": 0})
	call("job_observe", map[string]any{"action": "evidence", "job_id": id})
	call("job_control", map[string]any{"action": "cancel", "job_id": id})
	call("task_manage", map[string]any{"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": "Fixture completed"})
	call("task_manage", map[string]any{"action": "final_review", "task_id": taskID, "status": "pass", "summary": "Output fixture review", "verified": []string{"cond_01: fixture process completed"}})
	call("task_manage", map[string]any{"action": "complete", "task_id": taskID})
	request := map[string]any{"task_id": taskID, "workdir": root, "source_paths": []string{"capability-source.txt"}, "job_ids": []string{id}}
	live := call("work_result_read", request)["work_result"].(workresult.Projection)
	call("work_result_show", request)
	request["request_id"] = "freeze-schema"
	request["expected_source_revision"] = live.Source.Revision
	frozen := call("work_result_freeze", request)
	call("work_result_read", map[string]any{"result_id": frozen["result_id"]})
	// 模拟丢失终态的测试回执；真实进程身份已由本次 fixture 执行记录。
	record.Status = "running"
	record.FinishedAt = nil
	record.CreatedAt = time.Now().Add(-time.Hour)
	payload, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	jobs, jobsErr := r.command.Jobs()
	if jobsErr != nil {
		t.Fatal(jobsErr)
	}
	if err := os.WriteFile(filepath.Join(jobs.Root, id, "record.json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	abandoned := call("job_control", map[string]any{"action": "abandon", "job_id": id})
	if abandoned["status"] != "abandoned" {
		t.Fatalf("unexpected abandonment: %#v", abandoned)
	}
	call("job_control", map[string]any{"action": "archive", "job_id": id})
	call("runtime_diagnostics", map[string]any{})
	exported := call("diagnostic_export", map[string]any{})
	store := publicartifacts.New(r.cfg.AgentDockHome, "", 0)
	meta, data, err := store.Read(exported["artifact_id"].(string), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Private {
		t.Fatal("diagnostic export was public")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 3 {
		t.Fatal("unexpected diagnostic files")
	}
	expected := map[string]bool{"diagnostic-report.json": true, "diagnostic-report.md": true, "activity-safe.json": true}
	for _, file := range archive.File {
		if !expected[file.Name] {
			t.Fatal("arbitrary file in export")
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(stream)
		_ = stream.Close()
		if bytes.Contains(payload, []byte(path)) || bytes.Contains(payload, []byte("completed fixture")) {
			t.Fatal("project data or process output leaked into diagnostic bundle")
		}
	}
}
func TestDiagnosticsNeverExposeArgumentsOrResultBodies(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	secret := "FIXTURE_SUPER_SECRET_DATA"
	_, _ = r.Call(t.Context(), "not-a-real-tool", map[string]any{"secret": secret})
	_, _ = r.Call(t.Context(), "read_file", map[string]any{"path": secret})
	result, err := r.Call(t.Context(), "runtime_diagnostics", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "Bearer ") {
		t.Fatal("diagnostics leaked untrusted arguments or credentials")
	}
	if !strings.Contains(string(data), "not_observed") {
		t.Fatal("execution was misrepresented as response delivery")
	}
}
