package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uvwt/agentdock/internal/tool/core"
)

func applicationRoots() []string {
	roots := []string{"/Applications", "/System/Applications", "/System/Library/CoreServices/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	return roots
}

// 精确匹配 .app 文件名，不用模糊匹配或 shell；有歧义或目录扫描不完整时拒绝猜测。
// 不深入 .app 包内，限制目录数/条目数/深度，避免扫描插件和无限符号链接。
func lookupApplicationName(ctx context.Context, name string, roots []string) (string, error) {
	type directory struct {
		path  string
		depth int
	}
	queue := make([]directory, 0, len(roots))
	for _, root := range roots {
		queue = append(queue, directory{root, 0})
	}
	base := name
	if strings.EqualFold(filepath.Ext(base), ".app") {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	matches := map[string]bool{}
	visited := map[string]bool{}
	entries := 0
	incomplete := func() error {
		return core.NewError("APPLICATION_LOOKUP_INCOMPLETE", "Application lookup exceeded its bounds or encountered an unreadable directory; specify bundle_id or app_path", "desktop")
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		dir := queue[0]
		queue = queue[1:]
		canonical, err := filepath.EvalSymlinks(dir.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", incomplete()
		}
		if visited[canonical] {
			continue
		}
		visited[canonical] = true
		if len(visited) > 512 {
			return "", incomplete()
		}
		f, err := os.Open(canonical)
		if err != nil {
			return "", incomplete()
		}
		children, err := f.ReadDir(4097 - entries)
		_ = f.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return "", incomplete()
		}
		entries += len(children)
		if entries > 4096 {
			return "", incomplete()
		}
		for _, child := range children {
			if strings.HasPrefix(child.Name(), ".") {
				continue
			}
			path := filepath.Join(canonical, child.Name())
			if strings.EqualFold(filepath.Ext(child.Name()), ".app") {
				n := strings.TrimSuffix(child.Name(), filepath.Ext(child.Name()))
				if strings.EqualFold(n, base) {
					real, e := filepath.EvalSymlinks(path)
					if e != nil {
						return "", incomplete()
					}
					matches[real] = true
				}
				continue
			}
			// 只搜索应用目录内的三层普通子目录；其他位置使用明确路径或 bundle id。
			if child.IsDir() && dir.depth < 2 {
				queue = append(queue, directory{path, dir.depth + 1})
			}
		}
	}
	paths := make([]string, 0, len(matches))
	for path := range matches {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", core.NewError("APPLICATION_NOT_FOUND", "No exact app filename found in standard application folders; specify bundle_id or app_path", "desktop")
	}
	if len(paths) > 1 {
		return "", core.NewErrorDetails("APPLICATION_AMBIGUOUS", "More than one application has this name; select an explicit app_path", "desktop", map[string]any{"candidates": paths[:min(len(paths), 20)]})
	}
	return paths[0], nil
}
