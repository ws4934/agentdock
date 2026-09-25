package worktree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/sourceproof"
)

type gitIdentity struct{ directory, common string }

func gitDirectory(ctx context.Context, root, flag string) (string, error) {
	data, err := sourceproof.Git(ctx, root, "rev-parse", "--path-format=absolute", flag)
	if err != nil {
		return "", err
	}
	path := filepath.FromSlash(strings.TrimSpace(string(data)))
	if !filepath.IsAbs(path) {
		return "", errors.New("Git returned a nonabsolute metadata directory")
	}
	return sourceproof.CanonicalRoot(path)
}
func readGitIdentity(ctx context.Context, root string) (gitIdentity, error) {
	common, err := gitDirectory(ctx, root, "--git-common-dir")
	if err != nil {
		return gitIdentity{}, err
	}
	dir, err := gitDirectory(ctx, root, "--git-dir")
	if err != nil {
		return gitIdentity{}, err
	}
	return gitIdentity{dir, common}, nil
}
func sameDirectory(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil || !first.IsDir() {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && second.IsDir() && os.SameFile(first, second)
}

// 注册列表中的路径不是仓库身份。核对公共 Git 目录、独立管理目录和双向 gitfile。
func verifyGitIdentity(ctx context.Context, r Record) error {
	source, err := readGitIdentity(ctx, r.Project)
	if err != nil {
		return err
	}
	checkout, err := readGitIdentity(ctx, r.Path)
	if err != nil {
		return err
	}
	if !sameDirectory(source.common, checkout.common) || sameDirectory(source.directory, checkout.directory) {
		return errors.New("managed checkout belongs to another Git repository")
	}
	if r.CommonDir != "" && !sameDirectory(r.CommonDir, checkout.common) {
		return errors.New("Git common directory changed")
	}
	if r.GitDir != "" && !sameDirectory(r.GitDir, checkout.directory) {
		return errors.New("worktree administrative directory changed")
	}
	if !sameDirectory(filepath.Dir(checkout.directory), filepath.Join(checkout.common, "worktrees")) {
		return errors.New("checkout is not a registered linked worktree")
	}
	pointer := filepath.Join(r.Path, ".git")
	info, err := os.Lstat(pointer)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("worktree .git is not a regular pointer file")
	}
	backlink := filepath.Join(checkout.directory, "gitdir")
	backInfo, err := os.Lstat(backlink)
	if err != nil || !backInfo.Mode().IsRegular() || backInfo.Size() > 16384 {
		return errors.New("invalid worktree backlink")
	}
	file, err := os.Open(backlink)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 16385))
	_ = file.Close()
	if readErr != nil || len(data) > 16384 {
		return errors.New("unreadable worktree backlink")
	}
	target := filepath.FromSlash(strings.TrimSpace(string(data)))
	if !filepath.IsAbs(target) {
		target = filepath.Join(checkout.directory, target)
	}
	back, err := os.Stat(target)
	if err != nil || !back.Mode().IsRegular() || !os.SameFile(info, back) {
		return errors.New("Git backlink points at a different worktree")
	}
	return nil
}
