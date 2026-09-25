package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistrationUsesExactGitPathAndFilesystemIdentity(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "with space")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.ToSlash(target)
	for _, tc := range []struct {
		name, data string
		want       bool
	}{
		{"git separators", "worktree " + gitPath + "\x00HEAD deadbeef\x00detached\x00\x00", true},
		{"native separators", "worktree " + target + "\x00", true},
		{"unrelated field", "branch worktree " + gitPath + "\x00", false},
		{"path prefix", "worktree " + gitPath + "-other\x00", false},
		{"path suffix", "worktree " + gitPath + "/child\x00", false},
		{"relative path", "worktree with space\x00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := registeredWorktree([]byte(tc.data), target); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if registeredWorktree([]byte("worktree "+gitPath+"\x00"), target) {
		t.Fatal("missing checkout accepted")
	}
}
