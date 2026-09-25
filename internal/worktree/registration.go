package worktree

import (
	"os"
	"path/filepath"
	"strings"
)

// Git's porcelain -z paths use Git separators, including on Windows. Compare
// complete worktree records using filesystem identity, not slash/case spelling
// or substrings from unrelated fields. The caller separately rejects a missing
// or redirected managed checkout before consulting this registration list.
func registeredWorktree(data []byte, target string) bool {
	wanted, err := os.Stat(target)
	if err != nil || !wanted.IsDir() {
		return false
	}
	for _, field := range strings.Split(string(data), "\x00") {
		if !strings.HasPrefix(field, "worktree ") {
			continue
		}
		path := filepath.FromSlash(strings.TrimPrefix(field, "worktree "))
		if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Clean(path), filepath.Clean(target)) {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && info.IsDir() && os.SameFile(wanted, info) {
			return true
		}
	}
	return false
}
