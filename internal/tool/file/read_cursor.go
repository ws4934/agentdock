package file

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// 游标只是定位数据，不授予路径访问权。revision 绑定整个文件和规范路径；
// 文件变化或把游标用于其他文件时拒绝，不以丢弃护栏方式继续。
type fileCursor struct {
	Version  int    `json:"v"`
	Revision string `json:"r"`
	Offset   int    `json:"b"`
	EndLine  int    `json:"e"`
}

func encodeReadCursor(revision string, offset, end int) string {
	b, _ := json.Marshal(fileCursor{1, revision, offset, end})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeReadCursor(raw string) (fileCursor, error) {
	var c fileCursor
	if len(raw) > 1024 {
		return c, errors.New("INVALID_CURSOR")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, errors.New("INVALID_CURSOR")
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil || c.Version != 1 || c.Offset < 0 || c.EndLine < 1 || !strings.HasPrefix(c.Revision, "read1:") {
		return c, errors.New("INVALID_CURSOR")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return c, errors.New("INVALID_CURSOR")
	}
	return c, nil
}
func validateReadRange(start, end int) error {
	if start < 1 || end < 0 || (end > 0 && end < start) {
		return toolError("INVALID_RANGE", "start_line must be positive and end_line must not precede it", "validation")
	}
	return nil
}
func readTextPage(data []byte, revision string, start, end, budget int, cursor string) (string, textMeta, error) {
	if err := validateReadRange(start, end); err != nil {
		return "", textMeta{}, err
	}
	content := string(data)
	var value string
	var meta textMeta
	if cursor == "" {
		value, meta = sliceText(content, start, end, budget)
	} else {
		c, err := decodeReadCursor(cursor)
		if err != nil {
			return "", meta, err
		}
		if c.Revision != revision {
			return "", meta, toolError("READ_REVISION_CONFLICT", "read cursor no longer matches this file", "validation")
		}
		total := strings.Count(content, "\n") + 1
		if c.EndLine > total || c.Offset > len(data) || (c.Offset < len(data) && !utf8.RuneStart(data[c.Offset])) {
			return "", meta, errors.New("INVALID_CURSOR")
		}
		_, stop := textRangeOffsets(content, 1, c.EndLine)
		if c.Offset > stop {
			return "", meta, errors.New("INVALID_CURSOR")
		}
		value, meta = pageTextOffsets(content, c.Offset, stop, budget, total, c.EndLine)
	}
	if meta.Truncated && value == "" {
		return "", meta, toolError("OUTPUT_BUDGET_TOO_SMALL", "increase max_bytes to fit at least one UTF-8 code point", "validation")
	}
	if meta.Truncated {
		meta.NextCursor = encodeReadCursor(revision, meta.NextByteOffset, meta.RangeEndLine)
	}
	return value, meta, nil
}
