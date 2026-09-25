package file

import (
	"strings"

	"github.com/uvwt/agentdock/internal/textutil"

	"github.com/bmatcuk/doublestar/v4"
)

type textMeta struct {
	NextCursor      string `json:"next_cursor,omitempty"`
	NextStartByte   int    `json:"next_start_byte,omitempty"`
	NextByteOffset  int    `json:"-"`
	RangeEndLine    int    `json:"-"`
	Start           int    `json:"start_line"`
	End             int    `json:"end_line"`
	Total           int    `json:"total_lines"`
	NextStartLine   int    `json:"next_start_line,omitempty"`
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

func sliceText(content string, startLine, endLine, maxBytes int) (string, textMeta) {
	if startLine < 1 {
		startLine = 1
	}
	total := strings.Count(content, "\n") + 1
	if endLine <= 0 || endLine > total {
		endLine = total
	}
	if startLine > total {
		return "", textMeta{Start: startLine, End: endLine, Total: total}
	}
	if endLine < startLine {
		return "", textMeta{Start: startLine, End: endLine, Total: total, TruncatedReason: "invalid_range"}
	}
	start, stop := textRangeOffsets(content, startLine, endLine)
	return pageTextOffsets(content, start, stop, maxBytes, total, endLine)
}

// 直接扫描行边界，不为整个大文件创建 strings.Split 的行数组。
func textRangeOffsets(content string, startLine, endLine int) (int, int) {
	start, stop := 0, len(content)
	line := 1
	for i := 0; i < len(content); i++ {
		if content[i] != '\n' {
			continue
		}
		if line == endLine {
			stop = i
			break
		}
		line++
		if line == startLine {
			start = i + 1
		}
	}
	return start, stop
}
func pageTextOffsets(content string, start, stop, budget, total, endLine int) (string, textMeta) {
	startLine := strings.Count(content[:start], "\n") + 1
	selected := content[start:stop]
	meta := textMeta{Start: startLine, End: endLine, Total: total, RangeEndLine: endLine}
	if budget > 0 && len(selected) > budget {
		selected = textutil.SafeTruncateString(selected, budget).Text
		next := start + len(selected)
		meta.NextByteOffset = next
		meta.NextStartLine = strings.Count(content[:next], "\n") + 1
		lastNL := strings.LastIndexByte(content[:next], '\n')
		meta.NextStartByte = next - (lastNL + 1)
		meta.End = startLine + strings.Count(selected, "\n")
		if strings.HasSuffix(selected, "\n") {
			meta.End--
		}
		meta.Truncated = true
		meta.TruncatedReason = "max_bytes"
	}
	return selected, meta
}

func truncateString(value string, maxBytes int) string {
	return textutil.SafeTruncateString(value, maxBytes).Text
}

func contextAround(lines []string, index, n int) ([]string, []string) {
	if n <= 0 {
		return nil, nil
	}
	start := index - n
	if start < 0 {
		start = 0
	}
	end := index + n + 1
	if end > len(lines) {
		end = len(lines)
	}
	return append([]string(nil), lines[start:index]...), append([]string(nil), lines[index+1:end]...)
}

func looksBinary(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".reference", "node_modules", "target", "dist", "build", ".venv", "venv", ".tox", ".mypy_cache", ".pytest_cache", ".ruff_cache", "__pycache__":
		return true
	default:
		return false
	}
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok := globMatch(pattern, path); ok {
			return true
		}
	}
	return false
}

func globMatch(pattern, path string) bool {
	// 工作空间路径统一使用 slash。这里使用 doublestar 是为了避免继续维护
	// 自写 glob 逻辑；**/*.go、internal/**/*.go 这类常见代码检索模式由成熟库处理。
	path = strings.TrimPrefix(path, "./")
	pattern = strings.TrimPrefix(pattern, "./")
	ok, _ := doublestar.Match(pattern, path)
	return ok
}
