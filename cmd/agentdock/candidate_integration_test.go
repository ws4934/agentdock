//go:build candidate_integration && (darwin || linux)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the actually packaged Core via stdio. Every path and runtime state
// belongs to this test; never connect to the user's running production service.
func TestCandidateWorkflowAcrossCoreExit(t *testing.T) {
	core := os.Getenv("AGENTDOCK_CANDIDATE_CORE")
	if core == "" {
		t.Skip("set AGENTDOCK_CANDIDATE_CORE to an explicitly built candidate")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "state")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"go.mod": "module example.invalid/candidate\n\ngo 1.22\n", "main.go": "package candidate\nfunc Sum(a, b int) int { return a+b }\n", "main_test.go": "package candidate\nimport \"testing\"\nfunc TestSum(t *testing.T) { if Sum(1,2)!=3 {t.Fatal(\"bad sum\")} }\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "TMPDIR=" + os.TempDir(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "AGENTDOCK_HOME=" + home, "AGENTDOCK_DEFAULT_DIR=" + project, "AGENTDOCK_MCP_APPS_ENABLED=true", "AGENTDOCK_BROWSER_ENABLED=false", "AGENTDOCK_DESKTOP_ENABLED=false", "AGENTDOCK_LOG_LEVEL=error"}
	// Reuse the compiler's ordinary cache, not production application state.
	if cache, err := exec.Command("go", "env", "GOCACHE").Output(); err == nil {
		env = append(env, "GOCACHE="+strings.TrimSpace(string(cache)))
	}
	connect := func() *mcpsdk.ClientSession {
		t.Helper()
		cmd := exec.Command(core, "--stdio")
		cmd.Dir = project
		cmd.Env = env
		client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "candidate-package-smoke", Version: "1"}, nil)
		s, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	var session *mcpsdk.ClientSession
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %#v %v", name, r, err)
		}
		b, err := json.Marshal(r.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		v := map[string]any{}
		if err = json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	session = connect()
	defer func() {
		if session != nil {
			session.Close()
		}
	}()
	if session.InitializeResult().ServerInfo.Version != "0.8.5" {
		t.Fatal("candidate version differs")
	}
	task := call("task_manage", map[string]any{"action": "create", "title": "Signed candidate workflow", "goal": "Exercise candidate services", "completion_conditions": []string{"Execution and proof verified"}, "steps": []map[string]any{{"id": "verify", "title": "Verify candidate"}}})
	taskID := task["task_id"].(string)
	request := map[string]any{"cmd": "printf 'before-core-exit\\n'; sleep 2; printf 'after-core-exit\\n'", "workdir": project, "execution_mode": "managed", "request_id": "candidate-recovery", "task_id": taskID, "timeout_ms": 15000}
	receipt := call("exec_command", request)
	job := receipt["job_id"].(string)
	for i := 0; i < 150; i++ {
		r := call("job_observe", map[string]any{"action": "status", "job_id": job})
		j := r["job"].(map[string]any)
		if j["status"] == "running" && j["owner_alive"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	session = nil
	// A second genuine Core instance recovers the same persisted job, not a clone.
	session = connect()
	if recovered := call("exec_command", request); recovered["job_id"] != job {
		t.Fatal("Core restart created another execution")
	}
	wait := func(id string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			r := call("job_observe", map[string]any{"action": "status", "job_id": id})
			j := r["job"].(map[string]any)
			if j["finished_at"] != nil && j["owner_alive"] == false {
				return j
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("candidate job did not finish")
		return nil
	}
	if r := wait(job); r["status"] != "succeeded" {
		t.Fatalf("managed execution: %#v", r)
	}
	for i := 0; i < 2; i++ {
		log := call("job_observe", map[string]any{"action": "logs", "job_id": job, "stream": "stdout", "offset": 0})
		if log["log"].(map[string]any)["data"] != "before-core-exit\nafter-core-exit\n" {
			t.Fatal("lost or consumed logs")
		}
	}
	paths := []string{"go.mod", "main.go", "main_test.go"}
	validation := call("validation_run", map[string]any{"request_id": "candidate-test", "adapter": "go_test", "workdir": project, "task_id": taskID, "packages": []string{"."}, "source_paths": paths, "timeout_ms": 60000})
	proofID := validation["job_id"].(string)
	if r := wait(proofID); r["status"] != "succeeded" || r["evidence"].(map[string]any)["status"] != "passed" {
		t.Fatalf("real Go evidence: %#v", r)
	}
	navigation := call("code_navigate", map[string]any{"action": "document_symbols", "project": project, "path": "main.go"})
	if navigation["status"] != "ready" || len(navigation["items"].([]any)) == 0 {
		t.Fatalf("bundled provider unavailable: %#v", navigation)
	}
	artifact := call("file_publish", map[string]any{"path": filepath.Join(project, "main.go"), "delivery": "private"})
	read, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: artifact["resource_uri"].(string)})
	if err != nil || len(read.Contents) != 1 || !bytes.Equal(read.Contents[0].Blob, []byte(files["main.go"])) {
		t.Fatalf("private candidate artifact: %v", err)
	}
	call("task_manage", map[string]any{"action": "checkpoint", "task_id": taskID, "completed_step_ids": []string{"verify"}, "summary": "Candidate verified"})
	call("task_manage", map[string]any{"action": "final_review", "task_id": taskID, "status": "pass", "summary": "Candidate fixture", "verified": []string{"cond_01: real execution, private resource and gopls verified"}})
	call("task_manage", map[string]any{"action": "complete", "task_id": taskID})
	selection := map[string]any{"task_id": taskID, "workdir": project, "job_ids": []string{job, proofID}, "artifact_ids": []string{artifact["artifact_id"].(string)}, "source_paths": paths}
	live := call("work_result_read", selection)["work_result"].(map[string]any)
	selection["request_id"] = "candidate-delivery"
	selection["expected_source_revision"] = live["source"].(map[string]any)["revision"]
	frozen := call("work_result_freeze", selection)["work_result"].(map[string]any)
	if frozen["frozen"] != true {
		t.Fatal("candidate delivery not frozen")
	}
	if err = os.WriteFile(filepath.Join(project, "main.go"), []byte("package candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	historical := call("work_result_read", map[string]any{"result_id": frozen["result_id"]})["work_result"].(map[string]any)
	if historical["source"].(map[string]any)["revision"] != frozen["source"].(map[string]any)["revision"] {
		t.Fatal("historical snapshot changed")
	}
	t.Log("packaged Core restart, idempotent Job, independent logs, actual Go proof, bundled gopls, private resource and immutable delivery passed")
}
