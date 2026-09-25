package jobrun

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/uvwt/agentdock/internal/sourceproof"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "job-launch-fixture" {
		s, err := New(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		binary, _ := os.Executable()
		r, err := s.Start(context.Background(), "core-restart", Spec{Argv: []string{binary, "job-fixture", "await-release", os.Args[3]}, Workdir: os.Args[3], TimeoutMS: 15000}, os.Environ())
		if err != nil || writeJSON(filepath.Join(os.Args[3], "receipt.json"), r) != nil {
			os.Exit(3)
		}
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	if len(os.Args) == 4 && os.Args[1] == "job-supervise" {
		if err := Supervise(context.Background(), os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		os.Exit(0)
	}
	if len(os.Args) >= 3 && os.Args[1] == "job-fixture" {
		switch os.Args[2] {
		case "await-release":
			marker := filepath.Join(os.Args[3], "started-once")
			file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(4)
			}
			_ = file.Close()
			for i := 0; i < 1000; i++ {
				if _, err := os.Stat(filepath.Join(os.Args[3], "release")); err == nil {
					fmt.Println("completed after launcher exit")
					os.Exit(0)
				}
				time.Sleep(10 * time.Millisecond)
			}
			os.Exit(5)
		case "write":
			f, err := os.OpenFile(os.Args[3], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(2)
			}
			_, _ = f.WriteString("once\n")
			_ = f.Close()
			fmt.Print("hello 世界\n")
			time.Sleep(180 * time.Millisecond)
			fmt.Fprint(os.Stderr, "done\n")
		case "wait":
			fmt.Println("waiting")
			time.Sleep(10 * time.Second)
		case "noisy":
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", int(MaxLogBytes)+100))
		case "junit":
			_ = os.WriteFile(os.Args[3], []byte(`<testsuites><testsuite tests="2"><testcase name="ok"/><testcase name="skip"><skipped/></testcase></testsuite></testsuites>`), 0600)
		case "mutate":
			_ = os.WriteFile(os.Args[3], []byte("changed"), 0600)
		case "nothing":
		default:
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func fixtureSpec(t *testing.T, root, mode string, args ...string) Spec {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Argv: append([]string{binary, "job-fixture", mode}, args...), Workdir: root, TimeoutMS: 15000}
}
func await(t *testing.T, s *Store, id string) Record {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.Status(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Terminal() && !r.OwnerAlive {
			return r
		}
		if r.Status == "outcome_unknown" {
			t.Fatalf("unconfirmed execution: %#v", r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Record{}
}
func TestIndependentObserversAndRestart(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	spec := fixtureSpec(t, root, "write", marker)
	ctx, cancel := context.WithCancel(t.Context())
	r, err := s.Start(ctx, "same-request", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	reopened, err := New(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	final := await(t, reopened, r.ID)
	if final.Status != "succeeded" || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("%#v", final)
	}
	a, err := reopened.ReadLog(r.ID, "stdout", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	b, err := reopened.ReadLog(r.ID, "stdout", 0, 5)
	if err != nil || a.Data != b.Data || a.NextOffset != 5 {
		t.Fatalf("a=%#v b=%#v err=%v", a, b, err)
	}
	again, err := s.Start(t.Context(), "same-request", spec, os.Environ())
	if err != nil || again.ID != r.ID {
		t.Fatalf("repeat %v", err)
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "once\n" {
		t.Fatalf("executed repeatedly: %q", data)
	}
	spec.Title = "changed"
	if _, err = s.Start(t.Context(), "same-request", spec, os.Environ()); err == nil {
		t.Fatal("changed request silently reused")
	}
	if _, err = s.Archive(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	spec.Title = ""
	again, err = s.Start(t.Context(), "same-request", spec, os.Environ())
	if err != nil || !again.Archived {
		t.Fatalf("archive lost tombstone: %#v %v", again, err)
	}
}
func TestConcurrentIdempotentStart(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	marker := filepath.Join(root, "count")
	spec := fixtureSpec(t, root, "write", marker)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { r, err := s.Start(t.Context(), "parallel", spec, os.Environ()); ids <- r.ID; errs <- err })
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate receipt")
		}
		id = got
	}
	await(t, s, id)
	data, _ := os.ReadFile(marker)
	if string(data) != "once\n" {
		t.Fatal("duplicate execution")
	}
}
func TestCancelAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			spec := fixtureSpec(t, t.TempDir(), "wait")
			if mode == "timeout" {
				spec.TimeoutMS = 180
			}
			r, err := s.Start(t.Context(), mode, spec, os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				if _, err = s.Cancel(r.ID); err != nil {
					t.Fatal(err)
				}
			}
			r = await(t, s, r.ID)
			want := "cancelled"
			if mode == "timeout" {
				want = "timed_out"
			}
			if r.Status != want {
				t.Fatalf("%s != %s", r.Status, want)
			}
		})
	}
}
func TestBoundedLogs(t *testing.T) {
	s := newTestStore(t)
	r, err := s.Start(t.Context(), "noisy", fixtureSpec(t, t.TempDir(), "noisy"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	if r.Stdout.Retained != MaxLogBytes || r.Stdout.Dropped != 100 {
		t.Fatalf("%#v", r.Stdout)
	}
	chunk, err := s.ReadLog(r.ID, "stdout", MaxLogBytes-10, 100)
	if err != nil || len(chunk.Data) != 10 || !chunk.Truncated {
		t.Fatalf("%#v %v", chunk, err)
	}
}
func TestUnknownNeverRelaunched(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	spec := fixtureSpec(t, root, "nothing")
	r, err := s.Start(t.Context(), "dead", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	r.Status = "running"
	r.FinishedAt = nil
	r.CreatedAt = time.Now().Add(-time.Hour)
	_ = writeJSON(filepath.Join(s.Root, r.ID, "record.json"), r)
	again, err := s.Start(t.Context(), "dead", spec, os.Environ())
	if err != nil || again.Status != "outcome_unknown" {
		t.Fatalf("%#v %v", again, err)
	}
	if _, err = s.Archive(t.Context(), r.ID); err == nil {
		t.Fatal("unknown state erased")
	}
}
func TestEnvironmentNotPersisted(t *testing.T) {
	s := newTestStore(t)
	r, err := s.Start(t.Context(), "private", fixtureSpec(t, t.TempDir(), "nothing"), append(os.Environ(), "AGENTDOCK_TEST_PRIVATE=do-not-persist-this"))
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, r.ID)
	_ = filepath.Walk(s.Root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), "do-not-persist-this") {
				t.Errorf("secret persisted in %s", path)
			}
		}
		return nil
	})
}
func TestJUnitAndSourceFreshness(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	_ = os.WriteFile(source, []byte("original"), 0600)
	spec := fixtureSpec(t, root, "junit", "{report}")
	spec.Validation = &ValidationSpec{Adapter: "junit", SourcePaths: []string{"source.txt"}}
	r, err := s.Start(t.Context(), "junit", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	if r.Evidence == nil || r.Evidence.Status != "passed" || r.Evidence.Tests != 2 || r.Evidence.Skipped != 1 {
		t.Fatalf("%#v", r.Evidence)
	}
	_ = os.WriteFile(source, []byte("later change"), 0600)
	fresh := sourceproof.Capture(t.Context(), root, []string{"source.txt"})
	if sourceproof.Matches(fresh, r.Evidence.SourceAfter) {
		t.Fatal("stale proof accepted")
	}
	spec = fixtureSpec(t, root, "mutate", source)
	spec.Validation = &ValidationSpec{Adapter: "process", SourcePaths: []string{"source.txt"}}
	r, err = s.Start(t.Context(), "mutation", spec, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	r = await(t, s, r.ID)
	if r.Evidence.Status != "stale" {
		t.Fatalf("%#v", r.Evidence)
	}
}
func TestZeroTestsIncompleteReports(t *testing.T) {
	root := t.TempDir()
	before := sourceproof.Snapshot{Complete: true, Root: root, Revision: "source"}
	spec := Spec{Validation: &ValidationSpec{Adapter: "go_test"}}
	for _, obs := range []testObservation{{Complete: true}, {Tests: 1, Skipped: 1, Complete: true}, {Tests: 3, Passed: 3, Complete: false}} {
		if e := finishEvidence(spec, 0, obs, before, before); e.Status != "inconclusive" {
			t.Fatalf("%#v", e)
		}
	}
	path := filepath.Join(root, "report.xml")
	_ = os.WriteFile(path, []byte(`<testsuite tests="10"><testcase/></testsuite>`), 0600)
	if readJUnit(path).Complete {
		t.Fatal("wrong report count trusted")
	}
	_ = os.WriteFile(path, []byte(`<!DOCTYPE foo><testsuite tests="1"><testcase/></testsuite>`), 0600)
	if readJUnit(path).Complete {
		t.Fatal("doctype accepted")
	}
}
func TestGoEventCollection(t *testing.T) {
	g := &goCollector{}
	events := []map[string]any{{"Action": "run", "Package": "p", "Test": "TestOne"}, {"Action": "pass", "Package": "p", "Test": "TestOne"}, {"Action": "pass", "Package": "p"}}
	for _, e := range events {
		b, _ := json.Marshal(e)
		for _, c := range append(b, '\n') {
			_, _ = g.Write([]byte{c})
		}
	}
	r := g.result()
	if !r.Complete || r.Passed != 1 {
		t.Fatalf("%#v", r)
	}
	_, _ = g.Write([]byte("not-json\n"))
	if g.result().Complete {
		t.Fatal("invalid stream accepted")
	}
}
