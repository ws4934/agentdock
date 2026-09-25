package jobrun

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestManagedExecutionSurvivesLauncherProcessDeath(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	binary, _ := os.Executable()
	launcher := exec.Command(binary, "job-launch-fixture", s.Root, root)
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "release"), []byte("release"), 0600)
		_ = launcher.Process.Kill()
	})
	var receipt Record
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if readJSON(filepath.Join(root, "receipt.json"), &receipt) == nil {
			if _, err := os.Stat(filepath.Join(root, "started-once")); err == nil {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	if receipt.ID == "" {
		t.Fatal("launcher did not persist receipt")
	}
	if _, err := os.Stat(filepath.Join(root, "started-once")); err != nil {
		t.Fatal("command did not start before launcher death")
	}
	if err := launcher.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = launcher.Wait()
	// A genuinely new store, after the submitting OS process no longer exists.
	reopened, err := New(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := reopened.Status(receipt.ID)
	if err != nil || !r.OwnerAlive || r.Terminal() {
		t.Fatalf("detached execution lost: %#v %v", r, err)
	}
	spec := Spec{Argv: []string{binary, "job-fixture", "await-release", root}, Workdir: root, TimeoutMS: 15000}
	again, err := reopened.Start(context.Background(), "core-restart", spec, os.Environ())
	if err != nil || again.ID != receipt.ID {
		t.Fatalf("receipt recovery failed: %#v %v", again, err)
	}
	if err = os.WriteFile(filepath.Join(root, "release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	r = await(t, reopened, receipt.ID)
	if r.Status != "succeeded" {
		t.Fatalf("after restart: %#v", r)
	}
	chunk, err := reopened.ReadLog(r.ID, "stdout", 0, 1024)
	if err != nil || chunk.Data != "completed after launcher exit\n" {
		t.Fatalf("lost terminal output: %#v %v", chunk, err)
	}
}

func TestJobIdempotencyUsesEffectiveEnvironment(t *testing.T) {
	a := executionEnvironment([]string{"Z=1", "A=old", "A=new"})
	b := executionEnvironment([]string{"A=new", "Z=1"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("duplicate effective environment: %q %q", a, b)
	}
	if reflect.DeepEqual(a, executionEnvironment([]string{"Z=1", "A=new", "A=old"})) {
		t.Fatal("last-wins order was discarded")
	}
	s := newTestStore(t)
	spec := fixtureSpec(t, t.TempDir(), "nothing")
	env := append(os.Environ(), "AD_FIXTURE_ENV=old", "AD_FIXTURE_ENV=new")
	r, err := s.Start(t.Context(), "env-key", spec, env)
	if err != nil {
		t.Fatal(err)
	}
	await(t, s, r.ID)
	if _, err = s.Start(t.Context(), "env-key", spec, append(os.Environ(), "AD_FIXTURE_ENV=new")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(t.Context(), "env-key", spec, append(os.Environ(), "AD_FIXTURE_ENV=new", "AD_FIXTURE_ENV=old")); err == nil {
		t.Fatal("changed effective environment reused an execution")
	}
}

func TestActualGoValidationCountsAndFailure(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go executable unavailable")
	}
	s := newTestStore(t)
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/validation\n\ngo 1.22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package validation
import "testing"
func TestPass(t *testing.T) {}
func TestSkipped(t *testing.T) { t.Skip("fixture") }
func TestFails(t *testing.T) { t.Fatal("expected fixture failure") }
`
	if err = os.WriteFile(filepath.Join(root, "validation_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, filter, want string
		passed, failed     int
	}{
		{"passed", "Test(Pass|Skipped)$", "passed", 1, 0},
		{"failed", "TestFails$", "failed", 0, 1},
		{"zero", "DoesNotExist", "inconclusive", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{Argv: []string{goBin, "test", "-json", "-count=1", "-run", tc.filter, "."}, Workdir: root, TimeoutMS: 60000, Validation: &ValidationSpec{Adapter: "go_test", SourcePaths: []string{"go.mod", "validation_test.go"}}}
			r, err := s.Start(t.Context(), "go-"+tc.name, spec, append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off"))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(60 * time.Second)
			for time.Now().Before(deadline) {
				r, err = s.Status(r.ID)
				if err != nil {
					t.Fatal(err)
				}
				if r.Terminal() && !r.OwnerAlive {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if r.Evidence == nil || r.Evidence.Status != tc.want || r.Evidence.Passed != tc.passed || r.Evidence.Failed != tc.failed {
				b, _ := json.Marshal(r)
				t.Fatalf("evidence mismatch: %s", b)
			}
		})
	}
}
