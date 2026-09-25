package task

import (
	"context"
	"github.com/uvwt/agentdock/internal/taskstate"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeTaskCenterPartialAndReadOnly(t *testing.T) {
	service, root := newTaskTestService(t)
	var id string
	for _, title := range []string{"first", "second", "third"} {
		result, err := service.manageTest(context.Background(), map[string]any{
			"action": "create", "title": title, "goal": "observe only", "project": "/synthetic/project",
			"completion_conditions": []string{"verified"}, "steps": []map[string]any{{"id": "verify", "title": "verify"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		id = result["task_id"].(string)
	}
	before, err := service.tasks.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.RuntimeTasks("active", 1)
	if err != nil {
		t.Fatal(err)
	}
	if page["count"] != 1 || page["partial"] != true || page["observation_only"] != true {
		t.Fatalf("unexpected page: %#v", page)
	}
	all, err := service.RuntimeTasks("", 200)
	if err != nil || all["count"] != 3 || all["partial"] != false {
		t.Fatalf("unexpected full page: %#v, %v", all, err)
	}
	if _, err := service.RuntimeTask(id); err != nil {
		t.Fatal(err)
	}
	after, _ := service.tasks.Get(id)
	if after.Status != taskstate.StatusActive || len(before.Events) != len(after.Events) || !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatal("read mutated task")
	}
	restarted, _ := newTaskTestServiceAt(t, root)
	if _, err := restarted.RuntimeTask(id); err != nil {
		t.Fatalf("resume across service restart: %v", err)
	}
	if _, err := restarted.RuntimeTask("../" + id); err == nil {
		t.Fatal("unsafe task id accepted")
	}
	if err := os.WriteFile(filepath.Join(service.tasks.Root(), "tsk_ffffffffffffffff.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	partial, err := service.RuntimeTasks("", 200)
	if err != nil || partial["count"] != 3 || partial["partial"] != true {
		t.Fatalf("unreadable task must mark partial: %#v, %v", partial, err)
	}
}
