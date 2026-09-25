package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/sourceproof"
)

func fixtureGit(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	run := func(args ...string) string {
		data, err := sourceproof.Git(t.Context(), root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	run("init", "--quiet")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	_ = os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0600)
	run("add", "--", "file.txt")
	run("commit", "-qm", "base")
	return root, run("rev-parse", "HEAD")
}
func TestExplicitWorktreeIsIsolatedAndIdempotent(t *testing.T) {
	root, commit := fixtureGit(t)
	home := t.TempDir()
	service := New(home)
	_ = os.WriteFile(filepath.Join(root, "file.txt"), []byte("user dirty bytes\n"), 0600)
	request := Request{RequestID: "one", Project: root, BaseCommit: commit, Label: "isolated test"}
	r, err := service.Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "ready" || !r.SourceDirty || r.Dirty == nil || *r.Dirty {
		t.Fatalf("%#v", r)
	}
	source, _ := os.ReadFile(filepath.Join(root, "file.txt"))
	copy, _ := os.ReadFile(filepath.Join(r.Path, "file.txt"))
	if string(source) != "user dirty bytes\n" || string(copy) != "base\n" {
		t.Fatalf("source=%q copy=%q", source, copy)
	}
	again, err := service.Create(t.Context(), request)
	if err != nil || again.ID != r.ID {
		t.Fatalf("idempotency %#v %v", again, err)
	}
	request.Label = "different"
	if _, err = service.Create(t.Context(), request); err == nil {
		t.Fatal("changed request reused")
	}
	list, partial, err := service.List(t.Context())
	if err != nil || partial || len(list) != 1 {
		t.Fatalf("%#v %v %v", list, partial, err)
	}
	// Cleanup is explicit in the test; production never auto-deletes a worktree.
	_, _ = sourceproof.Git(t.Context(), root, "worktree", "remove", "--", r.Path)
}
func TestGitReferencesAndEnvironmentCannotRedirect(t *testing.T) {
	root, commit := fixtureGit(t)
	service := New(t.TempDir())
	if _, err := service.Create(t.Context(), Request{RequestID: "bad", Project: root, BaseCommit: "HEAD"}); err == nil {
		t.Fatal("branch expression accepted")
	}
	t.Setenv("GIT_DIR", "/missing/project")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", "/not/trusted")
	r, err := service.Create(t.Context(), Request{RequestID: "safe", Project: root, BaseCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentHead != commit {
		t.Fatal("Git environment changed project")
	}
	_, _ = sourceproof.Git(t.Context(), root, "worktree", "remove", "--", r.Path)
}
func TestCheckoutDoesNotRunRepositoryFiltersOrHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture is Unix-specific")
	}
	root, _ := fixtureGit(t)
	marker := filepath.Join(t.TempDir(), "unexpected-execution")
	script := filepath.Join(root, "side-effect.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho ran >> '"+marker+"'\ncat\n"), 0700)
	_, err := sourceproof.Git(t.Context(), root, "config", "filter.bad.smudge", script)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sourceproof.Git(t.Context(), root, "config", "filter.bad.required", "true")
	_ = os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("file.txt filter=bad\n"), 0600)
	_, _ = sourceproof.Git(t.Context(), root, "add", "--", ".gitattributes")
	_, _ = sourceproof.Git(t.Context(), root, "commit", "-qm", "filter fixture")
	hook := filepath.Join(root, ".git", "hooks", "post-checkout")
	_ = os.WriteFile(hook, []byte("#!/bin/sh\necho hook >> '"+marker+"'\n"), 0700)
	head, err := sourceproof.Git(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	service := New(t.TempDir())
	r, err := service.Create(t.Context(), Request{RequestID: "filters", Project: root, BaseCommit: strings.TrimSpace(string(head))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("checkout executed a repository filter or hook")
	}
	_, _ = sourceproof.Git(t.Context(), root, "worktree", "remove", "--", r.Path)
}
