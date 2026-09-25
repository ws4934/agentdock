package taskstate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestArchiveIsReversibleAndDoesNotChangeExecutionProgress(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Create("fixture", "keep progress", []string{"verified"}, []TaskStepInput{{ID: "step", Title: "step"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetArchived(task.ID, ManagementRevision(task), true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || archived.Status != task.Status || !reflect.DeepEqual(task.Steps, archived.Steps) || !reflect.DeepEqual(task.Events, archived.Events) {
		t.Fatal("archive changed progress or failed")
	}
	same, err := s.SetArchived(task.ID, ManagementRevision(archived), true)
	if err != nil || !same.UpdatedAt.Equal(archived.UpdatedAt) {
		t.Fatal("idempotent archive changed revision", err)
	}
	for _, status := range []Status{"", StatusActive} {
		list, partial, err := s.ListPage(status, 200)
		if err != nil || partial || len(list) != 0 {
			t.Fatalf("archive leaked into %q: %v %v", status, list, err)
		}
	}
	list, partial, err := s.ListPage("archived", 200)
	if err != nil || partial || len(list) != 1 || list[0].Status != StatusActive {
		t.Fatal("archive filter lost original state", err)
	}
	reopened, err := New(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	observed, err := reopened.Get(task.ID)
	if err != nil || observed.ArchivedAt == nil {
		t.Fatal("exact-ID recovery lost", err)
	}
	advanced, err := reopened.Checkpoint(task.ID, "step", StepCompleted, "progress still works")
	if err != nil || advanced.ArchivedAt == nil {
		t.Fatal("archive stopped progress", err)
	}
	if _, err := s.SetArchived(task.ID, ManagementRevision(archived), false); !errors.Is(err, ErrTaskConflict) {
		t.Fatal("stale restore accepted", err)
	}
	restored, err := s.SetArchived(task.ID, ManagementRevision(advanced), false)
	if err != nil || restored.ArchivedAt != nil || restored.Steps[0].Status != StepCompleted {
		t.Fatal("restore failed", err)
	}
}

func TestTaskDeletionRequiresCompletedStateAndCurrentRevision(t *testing.T) {
	s, _ := New(t.TempDir())
	task, err := s.Create("fixture", "guard deletion", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Delete(task.ID, ManagementRevision(task)); !errors.Is(err, ErrTaskNotCompleted) {
		t.Fatal("active task deleted", err)
	}
	task, err = s.Block(task.ID, "waiting")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetArchived(task.ID, ManagementRevision(task), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Delete(task.ID, ManagementRevision(archived)); !errors.Is(err, ErrTaskNotCompleted) {
		t.Fatal("archived unfinished task deleted", err)
	}
	task, err = s.Resume(task.ID, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "fixture verified", VerifiedFacts: []string{"verified"}}); err != nil {
		t.Fatal(err)
	}
	task, err = s.Complete(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"", ManagementRevision(archived), "tsk1:" + strings.Repeat("0", 64)} {
		if _, err = s.Delete(task.ID, revision); !errors.Is(err, ErrTaskConflict) {
			t.Fatal("stale/unversioned delete accepted", err)
		}
	}
	if _, err = s.Delete(task.ID, ManagementRevision(task)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(task.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatal("task not deleted", err)
	}
}

func TestManagementRevisionCompareAndSwapAcrossStores(t *testing.T) {
	root := t.TempDir()
	a, _ := New(root)
	b, _ := New(root)
	task, err := a.Create("fixture", "CAS", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			_, err := s.SetArchived(task.ID, ManagementRevision(task), true)
			results <- err
		}(s)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrTaskConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("lost CAS: %d success %d conflict", success, conflict)
	}
}

func TestManagementRejectsMismatchedFileIdentity(t *testing.T) {
	s, _ := New(t.TempDir())
	task, err := s.Create("fixture", "identity", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), task.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	other := "tsk_0123456789abcdef"
	if err = os.WriteFile(filepath.Join(s.Root(), other+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(other); err == nil {
		t.Fatal("mismatched identity accepted")
	}
	_, partial, err := s.ListPage("", 200)
	if err != nil || !partial {
		t.Fatal("corrupt identity not marked partial", err)
	}
}

func TestManagementRequestBoundsAndIdentity(t *testing.T) {
	ref := ManagementReference{ID: "tsk_0123456789abcdef", Revision: "tsk1:" + strings.Repeat("a", 64)}
	if err := (ManagementRequest{Action: "archive", Tasks: []ManagementReference{ref}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []ManagementRequest{
		{Action: "delete"}, {Action: "complete", Tasks: []ManagementReference{ref}},
		{Action: "archive", Tasks: []ManagementReference{ref, ref}},
		{Action: "archive", Tasks: []ManagementReference{{ID: "../" + ref.ID, Revision: ref.Revision}}},
		{Action: "archive", Tasks: []ManagementReference{{ID: ref.ID, Revision: ""}}},
		{Action: "archive", Tasks: make([]ManagementReference, 201)},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid management accepted: %+v", request)
		}
	}
}
