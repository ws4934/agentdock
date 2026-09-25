package session

import (
	"testing"
	"time"
)

func TestIndependentCursorsAndRetainedWindow(t *testing.T) {
	s := &Session{ID: "fixture", StartedAt: time.Now(), exitCode: -1}
	_, _ = sessionOutputWriter{session: s}.Write([]byte("ABCDEFGHIJK"))
	zero := int64(0)
	first, err := s.SnapshotAt("running", 5, &zero, &zero)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SnapshotAt("running", 5, &zero, &zero)
	if err != nil || first.Stdout != "ABCDE" || again.Stdout != first.Stdout || first.StdoutNextOffset != 5 {
		t.Fatalf("%+v %+v %v", first, again, err)
	}
	next, err := s.SnapshotAt("running", 100, &first.StdoutNextOffset, &zero)
	if err != nil || next.Stdout != "FGHIJK" {
		t.Fatalf("%+v %v", next, err)
	}
	future := int64(1000)
	if _, err = s.SnapshotAt("running", 5, &future, &zero); err == nil {
		t.Fatal("future cursor accepted")
	}
	s.mu.Lock()
	s.stdoutDroppedBytes = 3
	s.mu.Unlock()
	if _, err = s.SnapshotAt("running", 5, &zero, &zero); err == nil {
		t.Fatal("expired cursor silently skipped")
	}
}
func TestSnapshotSharesRawBudgetAcrossStreams(t *testing.T) {
	s := &Session{ID: "fixture", StartedAt: time.Now(), exitCode: -1}
	_, _ = sessionOutputWriter{session: s}.Write([]byte("123456789"))
	_, _ = sessionOutputWriter{session: s, stderr: true}.Write([]byte("abcdefghi"))
	snap := s.Snapshot("running", 8)
	if snap.Stdout != "1234" || snap.Stderr != "abcd" || snap.StdoutNextOffset+snap.StderrNextOffset > 8 {
		t.Fatalf("%+v", snap)
	}
}

func TestCompletedMemoryBudgetPreservesActiveAndNewest(t *testing.T) {
	store := NewStore()
	old := &Session{ID: "old", completed: true, FinishedAt: time.Now().Add(-time.Hour)}
	newest := &Session{ID: "new", completed: true, FinishedAt: time.Now()}
	active := &Session{ID: "active"}
	for _, s := range []*Session{old, newest, active} {
		_, _ = s.stdout.WriteString("12345678")
		store.Add(s)
	}
	if n := store.PruneCompletedBytes(8); n != 1 {
		t.Fatalf("removed %d", n)
	}
	if _, ok := store.Get("old"); ok {
		t.Fatal("old result kept over budget")
	}
	for _, id := range []string{"new", "active"} {
		if _, ok := store.Get(id); !ok {
			t.Fatalf("lost %s", id)
		}
	}
}
