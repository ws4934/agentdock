package jobrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskGuardScansBeyondPreviewAndRejectsUnknownJobs(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Add(-time.Hour)
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("job_%032x", i+1)
		dir := filepath.Join(s.Root, id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		r := Record{SchemaVersion: SchemaVersion, ID: id, TaskID: "selected", CreatedAt: now.Add(time.Duration(i) * time.Second), Status: "exited", FinishedAt: &now}
		if i == 0 {
			r.Status = "outcome_unknown"
			r.FinishedAt = nil
		}
		if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	err := s.WithTaskIdle(t.Context(), "selected", func() error { called = true; return nil })
	if !errors.Is(err, ErrTaskJobsBusy) || called {
		t.Fatalf("unknown job hidden by preview: %v called=%v", err, called)
	}
	if err = s.WithTaskIdle(t.Context(), "unrelated", func() error { return nil }); err != nil {
		t.Fatalf("unrelated known job blocked deletion: %v", err)
	}
}

func TestTaskGuardFailsClosedForUnattributedCorruption(t *testing.T) {
	s := newTestStore(t)
	dir := filepath.Join(s.Root, "job_00000000000000000000000000000001")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := s.WithTaskIdle(t.Context(), "selected", func() error { called = true; return nil })
	if !errors.Is(err, ErrTaskJobsUnavailable) || called {
		t.Fatalf("unsafe cleanup: %v called=%v", err, called)
	}
}

func TestTaskGuardHoldsAdmissionAndHonorsCancellation(t *testing.T) {
	s := newTestStore(t)
	err := s.WithTaskIdle(t.Context(), "selected", func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()
		guard, err := lock(ctx, filepath.Join(s.Root, "admission.lock"))
		if err == nil {
			guard.Release()
			t.Fatal("job admission was not serialized")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	if err := s.WithTaskIdle(ctx, "selected", func() error { called = true; return nil }); err == nil || called {
		t.Fatal("cancelled deletion proceeded", err)
	}
}
