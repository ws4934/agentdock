package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestRuntimeTaskManagementAuthenticatedEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	cfg.AuthToken = "synthetic-management-token"
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := runtimeAPIHandler(runtime, cfg, auth.NewOAuthStore())
	store, err := taskstate.New(filepath.Join(cfg.AgentDockHome, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	create := func(title string, completed bool) taskstate.Task {
		task, err := store.Create(title, "synthetic management", []string{"verified"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if completed {
			if _, err = store.FinalReview(task.ID, taskstate.FinalReviewInput{Status: taskstate.FinalReviewPass, Summary: "verified", VerifiedFacts: []string{"fixture verified"}}); err != nil {
				t.Fatal(err)
			}
			task, err = store.Complete(task.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return task
	}
	call := func(method, path, body string, authenticated bool, want int) map[string]any {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, want, rec.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	request := func(action string, tasks ...taskstate.Task) string {
		refs := make([]taskstate.ManagementReference, 0, len(tasks))
		for _, task := range tasks {
			refs = append(refs, taskstate.ManagementReference{ID: task.ID, Revision: taskstate.ManagementRevision(task)})
		}
		b, _ := json.Marshal(taskstate.ManagementRequest{Action: action, Tasks: refs})
		return string(b)
	}
	active := create("active", false)
	finished := create("finished", true)
	hidden := create("unselected", true)
	call("POST", "/internal/runtime/tasks/manage", request("archive", active), false, 401)
	before, err := store.Get(active.ID)
	if err != nil || before.ArchivedAt != nil {
		t.Fatal("unauthorized mutation", err)
	}
	result := call("POST", "/internal/runtime/tasks/manage/", request("archive", active), true, 200)
	if result["changed"] != float64(1) {
		t.Fatal("POST body was not routed", result)
	}
	list := call("GET", "/internal/runtime/tasks?status=archived", "", true, 200)
	if list["count"] != float64(1) {
		t.Fatal("archive filter", list)
	}
	detail := call("GET", "/internal/runtime/tasks/"+active.ID, "", true, 200)
	if detail["revision"] == "" {
		t.Fatal("missing revision")
	}
	result = call("POST", "/internal/runtime/tasks/manage", request("restore", active), true, 200)
	if result["failed"] != float64(1) || !strings.Contains(toJSON(result), "TASK_CONFLICT") {
		t.Fatal("stale mutation", result)
	}
	active, err = store.Get(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	call("POST", "/internal/runtime/tasks/manage", request("restore", active), true, 200)
	active, _ = store.Get(active.ID)
	result = call("POST", "/internal/runtime/tasks/manage", request("delete", active, finished), true, 200)
	if result["changed"] != float64(1) || result["failed"] != float64(1) || !strings.Contains(toJSON(result), "TASK_NOT_COMPLETED") {
		t.Fatal("partial result hidden", result)
	}
	if _, err = store.Get(hidden.ID); err != nil {
		t.Fatal("unselected task deleted", err)
	}
	if _, err = store.Get(active.ID); err != nil {
		t.Fatal("active task deleted", err)
	}
	call("DELETE", "/internal/runtime/tasks/"+hidden.ID+"?revision="+url.QueryEscape(taskstate.ManagementRevision(active)), "", true, 409)
	call("DELETE", "/internal/runtime/tasks/"+hidden.ID, "", true, 400)
	call("DELETE", "/internal/runtime/tasks/"+hidden.ID+"?revision="+url.QueryEscape(taskstate.ManagementRevision(hidden)), "", true, 200)
	// 即使任务已完成，非终态关联执行仍阻止清理；转为终态后仅删除任务。
	busy := create("has-receipt", true)
	jobID := "job_11111111111111111111111111111111"
	jobDir := filepath.Join(cfg.AgentDockHome, "jobs", jobID)
	if err := os.MkdirAll(jobDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	job := jobrun.Record{SchemaVersion: jobrun.SchemaVersion, ID: jobID, TaskID: busy.ID, CreatedAt: old, Status: "outcome_unknown"}
	writeJob := func() {
		data, _ := json.Marshal(job)
		if err := os.WriteFile(filepath.Join(jobDir, "record.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJob()
	if err := os.WriteFile(filepath.Join(jobDir, "stdout.log"), []byte("preserved receipt log"), 0600); err != nil {
		t.Fatal(err)
	}
	result = call("POST", "/internal/runtime/tasks/manage", request("delete", busy), true, 200)
	if result["changed"] != float64(0) || !strings.Contains(toJSON(result), "TASK_JOBS_BUSY") {
		t.Fatal("unknown execution lost its task", result)
	}
	job.Status = "exited"
	job.FinishedAt = &old
	writeJob()
	result = call("POST", "/internal/runtime/tasks/manage", request("delete", busy), true, 200)
	if result["changed"] != float64(1) {
		t.Fatal("terminal job blocked cleanup", result)
	}
	if data, err := os.ReadFile(filepath.Join(jobDir, "stdout.log")); err != nil || string(data) != "preserved receipt log" {
		t.Fatal("execution log deleted", err)
	}
	if _, err := os.Stat(filepath.Join(jobDir, "record.json")); err != nil {
		t.Fatal("receipt deleted", err)
	}
	for _, body := range []string{`{"action":"archive","tasks":[]}`, request("archive", active) + ` {}`, strings.TrimSuffix(request("archive", active), "}") + `,"unknown":true}`, strings.Repeat("x", 65537)} {
		call("POST", "/internal/runtime/tasks/manage", body, true, 400)
	}
	call("GET", "/internal/runtime/tasks/manage", "", true, 405)
	call("DELETE", "/internal/runtime/tasks/manage", "", true, 405)
	call("POST", "/internal/runtime/tasks/"+active.ID, request("archive", active), true, 405)
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestUnauthenticatedInstallationStillRefusesTaskWrites(t *testing.T) {
	cfg := testConfig(t)
	cfg.AuthToken = ""
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := runtimeAPIHandler(runtime, cfg, auth.NewOAuthStore())
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		path := "/internal/runtime/tasks/manage"
		if method == http.MethodDelete {
			path = "/internal/runtime/tasks/tsk_0123456789abcdef"
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if rec.Code != 401 {
			t.Fatalf("unauthenticated write accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
}
