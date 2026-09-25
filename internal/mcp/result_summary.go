package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxResultSummaryBytes = 1024

// structuredContent 是完整、可验证的结果；文本仅提供有界导航，绝不再次复制
// 大段代码/日志。不能用「成功」概括混合或未知结果，原始错误和状态保持完整。
func resultSummary(name string, value any) string {
	result := asMap(value)
	parts := []string{boundedSummary(name, 128) + " result"}
	for _, key := range []string{"action", "status", "outcome", "command_ok", "exit_code", "code", "error", "summary"} {
		var content string
		switch v := result[key].(type) {
		case string:
			content = v
		case bool, int, int64, float64:
			content = fmt.Sprint(v)
		default:
			continue
		}
		if content != "" {
			parts = append(parts, key+"="+boundedSummary(content, 320))
		}
	}
	const tail = ". Full result: structuredContent."
	return boundedSummary(strings.Join(parts, "; "), maxResultSummaryBytes-len(tail)) + tail
}
func boundedSummary(s string, limit int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= limit {
		return s
	}
	if limit < 3 {
		return ""
	}
	end := limit - 3
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "..."
}
