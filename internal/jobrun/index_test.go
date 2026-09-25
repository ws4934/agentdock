package jobrun

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJobPaginationRemainsStableAndScoped(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Add(-time.Hour)
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("job_%032x", i+1)
		dir := filepath.Join(s.Root, id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		created := now.Add(time.Duration(i) * time.Minute)
		r := Record{SchemaVersion: SchemaVersion, ID: id, TaskID: "one", CreatedAt: created, FinishedAt: &now, Status: "succeeded"}
		if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for pageNo := 0; pageNo < 5; pageNo++ {
		page, err := s.ListPage("one", 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range page.Jobs {
			if seen[job.ID] {
				t.Fatal("duplicated page item")
			}
			seen[job.ID] = true
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatal("no cursor progress")
		}
		cursor = page.NextCursor
		if _, err := s.ListPage("another", 2, cursor); err == nil {
			t.Fatal("cross-filter cursor accepted")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("lost jobs: %v", seen)
	}
}
func TestArchiveIsShardedAndNeverReplays(t *testing.T) {
	s := newTestStore(t)
	spec := fixtureSpec(t, t.TempDir(), "nothing")
	r, err := s.Start(t.Context(), "archived-once", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	if _, err = s.Archive(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Root, r.ID)); !os.IsNotExist(err) {
		t.Fatal("archive stayed in live history")
	}
	path, err := s.tombstonePath(r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(path, "record.json")); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Start(t.Context(), "archived-once", spec, os.Environ())
	if err != nil || again.ID != r.ID || !again.Archived {
		t.Fatalf("idempotency lost: %+v %v", again, err)
	}
	page, err := reopened.ListPage("", 1, "")
	if err != nil || len(page.Jobs) != 0 {
		t.Fatalf("archive counted as recent jobs: %+v %v", page, err)
	}
}

func TestIndexDetectsInPlaceRecordCorruption(t *testing.T) {
	s := newTestStore(t)
	id := "job_000000000000000000000000000000aa"
	dir := filepath.Join(s.Root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	r := Record{ID: id, SchemaVersion: SchemaVersion, CreatedAt: now, FinishedAt: &now, Status: "succeeded"}
	if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.index(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListPage("", 10, "")
	if err != nil || !page.Partial || len(page.Jobs) != 1 || !page.Jobs[0].StateUnavailable {
		t.Fatalf("corruption hidden: %+v %v", page, err)
	}
}

func BenchmarkJobHistoryObservation(b *testing.B) {
	s, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().Add(-time.Hour)
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("job_%032x", i+1)
		dir := filepath.Join(s.Root, ids[i])
		if err := os.Mkdir(dir, 0700); err != nil {
			b.Fatal(err)
		}
		created := now.Add(time.Duration(i) * time.Second)
		r := Record{SchemaVersion: SchemaVersion, ID: ids[i], CreatedAt: created, FinishedAt: &now, Status: "succeeded"}
		if err := writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.index(); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("page_five_warm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.ListPage("", 5, ""); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full_records_reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, id := range ids {
				if _, err := s.Status(id); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
