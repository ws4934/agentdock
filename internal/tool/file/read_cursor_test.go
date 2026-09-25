package file

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadCursorPreservesLongLinesAndUTF8(t *testing.T) {
	for _, input := range []string{"ABCDEFGHIJKLMNO\nSECOND", "中文字符🙂\n第二行\n", strings.Repeat("x", 1000), "a\nb\nc"} {
		for _, budget := range []int{4, 5, 8, 17} {
			data := []byte(input)
			revision := readRevision("/fixture", data)
			cursor := ""
			got := ""
			for i := 0; i < 1000; i++ {
				value, meta, err := readTextPage(data, revision, 1, 0, budget, cursor)
				if err != nil {
					t.Fatal(err)
				}
				if !utf8.ValidString(value) {
					t.Fatal("split UTF8")
				}
				got += value
				if !meta.Truncated {
					break
				}
				if meta.NextCursor == "" || meta.NextCursor == cursor || value == "" {
					t.Fatal("continuation made no progress")
				}
				cursor = meta.NextCursor
			}
			if got != input {
				t.Fatalf("budget=%d got=%q want=%q", budget, got, input)
			}
		}
	}
}
func TestReadCursorRejectsStaleAndForeignFile(t *testing.T) {
	data := []byte("ABCDEFGHIJK")
	_, meta, err := readTextPage(data, readRevision("/a", data), 1, 0, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{readRevision("/b", data), readRevision("/a", []byte("changed"))} {
		if _, _, err = readTextPage(data, revision, 1, 0, 4, meta.NextCursor); err == nil {
			t.Fatal("foreign revision accepted")
		}
	}
}
func TestReverseRangeNeverPanics(t *testing.T) {
	if _, _, err := readTextPage([]byte("a\nb\nc"), "fixture", 3, 1, 100, ""); err == nil {
		t.Fatal("reverse range accepted")
	}
	_, _ = sliceText("a\nb\nc", 3, 1, 100)
}
