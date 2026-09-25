package worktree

import (
	"github.com/uvwt/agentdock/internal/sourceproof"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacedGitIdentityIsUnavailable(t *testing.T) {
	root, commit := fixtureGit(t)
	service := New(t.TempDir())
	r, err := service.Create(t.Context(), Request{RequestID: "review-identity", Project: root, BaseCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	// Only replace the synthetic checkout's Git pointer, never the user's repo.
	if err = os.Remove(filepath.Join(r.Path, ".git")); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if _, err := sourceproof.Git(t.Context(), r.Path, args...); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "--quiet")
	run("config", "user.email", "review@example.invalid")
	run("config", "user.name", "Review fixture")
	if err = os.WriteFile(filepath.Join(r.Path, "unrelated.txt"), []byte("different repository"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "unrelated.txt")
	run("commit", "-qm", "unrelated fixture history")
	observed, err := service.Status(t.Context(), r.ID)
	t.Logf("WORKTREE_IDENTITY status=%s changed_head=%t error=%v", observed.Status, observed.CurrentHead != commit, err)
	if err == nil && observed.Status == "ready" {
		t.Error("stale parent registration accepted an unrelated Git repository as the managed checkout")
	}
}
