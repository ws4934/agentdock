package jobrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncompleteAdmissionDoesNotPoisonRegistry(t *testing.T) {
	s := newTestStore(t)
	bad := filepath.Join(s.Root, "job_00000000000000000000000000000001")
	if err := os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	records, partial, err := s.List("", 10)
	if err != nil || !partial || len(records) != 1 || !records[0].StateUnavailable {
		t.Fatalf("%+v partial=%v err=%v", records, partial, err)
	}
	_, partial, err = s.List("unrelated-task", 10)
	if err != nil || !partial {
		t.Fatalf("filtered list hid uncertainty: %v %v", partial, err)
	}
	r, err := s.Start(t.Context(), "unrelated", fixtureSpec(t, t.TempDir(), "nothing"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if await(t, s, r.ID).Status != "succeeded" {
		t.Fatal("unrelated execution failed")
	}
	for _, name := range []string{"request.json", "receipt.json", "record.json"} {
		if _, err := os.Stat(filepath.Join(s.Root, r.ID, name)); err != nil {
			t.Fatal(err)
		}
	}
	stages, _ := filepath.Glob(filepath.Join(s.Root, ".admission-*"))
	if len(stages) != 0 {
		t.Fatalf("uncommitted staging: %v", stages)
	}
}

func TestAbandonPreviousBootRestoresCapacityWithoutClaimingSuccess(t *testing.T) {
	if bootID() == "" {
		t.Skip("OS boot identity unavailable")
	}
	s := newTestStore(t)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < MaxRunning; i++ {
		id := fmt.Sprintf("job_%032x", i+1)
		dir := filepath.Join(s.Root, id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		r := Record{SchemaVersion: SchemaVersion, ID: id, Status: "running", StartedAt: &old, CreatedAt: old, BootID: "synthetic-previous-boot", Evidence: &Evidence{Status: "passed"}}
		if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
			t.Fatal(err)
		}
	}
	spec := fixtureSpec(t, t.TempDir(), "nothing")
	if _, err := s.Start(t.Context(), "new", spec, os.Environ()); err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("limit not enforced: %v", err)
	}
	for i := 0; i < MaxRunning; i++ {
		r, err := s.Abandon(t.Context(), fmt.Sprintf("job_%032x", i+1))
		if err != nil || r.Status != "abandoned" || !r.Terminal() || r.Evidence.Status != "inconclusive" || r.ResolutionBasis != "previous_boot_ended" {
			t.Fatalf("%+v %v", r, err)
		}
	}
	r, err := s.Start(t.Context(), "new", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, r.ID)
}

func TestAbandonRequiresProcessEvidence(t *testing.T) {
	s := newTestStore(t)
	id := "job_00000000000000000000000000000009"
	dir := filepath.Join(s.Root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	r := Record{SchemaVersion: SchemaVersion, ID: id, Status: "running", StartedAt: &old, CreatedAt: old, BootID: bootID()}
	if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Abandon(t.Context(), id); err == nil {
		t.Fatal("unknown process state was abandoned")
	}
	observed, err := s.Status(id)
	if err != nil || observed.Status != "outcome_unknown" {
		t.Fatalf("%+v %v", observed, err)
	}
}

func TestAbandonObservesExitAndKeepsIdempotency(t *testing.T) {
	s := newTestStore(t)
	spec := fixtureSpec(t, t.TempDir(), "nothing")
	r, err := s.Start(t.Context(), "lost-outcome", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	if r.Execution == nil {
		t.Fatal("missing process receipt")
	}
	r.Status = "running"
	r.FinishedAt = nil
	r.CreatedAt = time.Now().Add(-time.Hour)
	if err := writeJSON(filepath.Join(s.Root, r.ID, "record.json"), r); err != nil {
		t.Fatal(err)
	}
	done, err := s.Abandon(t.Context(), r.ID)
	if err != nil || done.Status != "abandoned" || done.ResolutionBasis != "owned_process_group_ended" {
		t.Fatalf("%+v %v", done, err)
	}
	again, err := s.Start(t.Context(), "lost-outcome", spec, os.Environ())
	if err != nil || again.ID != r.ID || again.Status != "abandoned" {
		t.Fatalf("execution replayed: %+v %v", again, err)
	}
	if _, err := s.Archive(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonCannotRaceLiveSupervisor(t *testing.T) {
	s := newTestStore(t)
	r, err := s.Start(t.Context(), "live", fixtureSpec(t, t.TempDir(), "wait"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.Cancel(r.ID); await(t, s, r.ID) })
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		r, err = s.Status(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.OwnerAlive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !r.OwnerAlive {
		t.Fatal("supervisor did not start")
	}
	if _, err := s.Abandon(t.Context(), r.ID); err == nil || !strings.Contains(err.Error(), "supervisor") {
		t.Fatalf("live owner accepted: %v", err)
	}
}

func TestCorruptMutableRecordNeverResurrectsSuccess(t *testing.T) {
	s := newTestStore(t)
	spec := fixtureSpec(t, t.TempDir(), "nothing")
	r, err := s.Start(t.Context(), "corrupt", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	dir := filepath.Join(s.Root, r.ID)
	if err = os.WriteFile(filepath.Join(dir, "record.json"), []byte(`{"status":"succeeded","exit_code":0,"unexpected":`), 0600); err != nil {
		t.Fatal(err)
	}
	observed, err := s.Status(r.ID)
	if err != nil || !observed.StateUnavailable || observed.Status != "outcome_unknown" || observed.ExitCode != nil || observed.Evidence != nil {
		t.Fatalf("%+v %v", observed, err)
	}
	if _, err = s.Start(t.Context(), "corrupt", spec, os.Environ()); err == nil {
		t.Fatal("corrupted state replayed")
	}
	var receipt Record
	if err = readJSON(filepath.Join(dir, "receipt.json"), &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.CreatedAt = time.Now().Add(-time.Hour)
	if err = writeJSON(filepath.Join(dir, "receipt.json"), receipt); err != nil {
		t.Fatal(err)
	}
	done, err := s.Abandon(t.Context(), r.ID)
	if err != nil || done.Status != "abandoned" || done.ExitCode != nil || done.Evidence != nil {
		t.Fatalf("%+v %v", done, err)
	}
}
