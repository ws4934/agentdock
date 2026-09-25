package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchReadCoalescesAndPreservesRevisions(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	var text strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&text, "line %03d\n", i)
	}
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte(text.String()), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReadFiles(t.Context(), BatchReadRequest{Requests: []ReadRange{{Path: "sample.txt", StartLine: 5, EndLine: 10}, {Path: path, StartLine: 9, EndLine: 15}, {Path: path, StartLine: 5, EndLine: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	blocks := result["blocks"].([]map[string]any)
	if len(blocks) != 1 || result["file_reads"] != 1 || len(blocks[0]["request_indexes"].([]int)) != 3 {
		t.Fatalf("%#v", result)
	}
	read, err := service.ReadFile(t.Context(), ReadRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if read["read_revision"] != blocks[0]["read_revision"] {
		t.Fatal("batch and ordinary revision disagree")
	}
	if _, err = service.Edit(t.Context(), EditRequest{Action: "replace", Path: path, Old: "line 005", New: "line FIVE", ExpectedReadRevision: read["read_revision"].(string)}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Edit(t.Context(), EditRequest{Action: "replace", Path: path, Old: "line 006", New: "line SIX", ExpectedReadRevision: read["read_revision"].(string)}); err == nil {
		t.Fatal("stale edit succeeded")
	}
	after, _ := os.ReadFile(path)
	if strings.Contains(string(after), "SIX") {
		t.Fatal("conflicting edit wrote bytes")
	}
}
func TestBatchReadBudgetAndPartialErrors(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	_ = os.WriteFile(filepath.Join(root, "one"), []byte(strings.Repeat("long line\n", 300)), 0600)
	result, err := service.ReadFiles(t.Context(), BatchReadRequest{MaxTotalBytes: 20, Requests: []ReadRange{{Path: "one", StartLine: 1, EndLine: 100}, {Path: "missing"}, {Path: "one", StartLine: 180, EndLine: 200}}})
	if err != nil {
		t.Fatal(err)
	}
	if result["partial"] != true || result["truncated"] != true || result["returned_bytes"].(int) > 20 {
		t.Fatalf("%#v", result)
	}
	if _, err = service.ReadFiles(t.Context(), BatchReadRequest{Requests: []ReadRange{{Path: "one", StartLine: 1, EndLine: 401}}}); err == nil {
		t.Fatal("unbounded range accepted")
	}
	result, err = service.ReadFiles(t.Context(), BatchReadRequest{Requests: []ReadRange{{Path: "one", ExpectedReadRevision: "read1:stale"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result["partial"] != true {
		t.Fatal("stale snapshot not rejected")
	}
}
func TestGuardedMoveDeleteAndStructuredPatch(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	_ = os.WriteFile(filepath.Join(root, "a"), []byte("alpha\n"), 0600)
	_ = os.WriteFile(filepath.Join(root, "b"), []byte("beta\n"), 0600)
	revision := func(path string) string {
		r, err := service.ReadFile(t.Context(), ReadRequest{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		return r["read_revision"].(string)
	}
	a, b := revision("a"), revision("b")
	if _, err := service.Edit(t.Context(), EditRequest{Action: "move", Path: "a", NewPath: "b", Overwrite: true, ExpectedReadRevision: a}); err == nil {
		t.Fatal("unguarded destination overwrite accepted")
	}
	if _, err := service.Edit(t.Context(), EditRequest{Action: "move", Path: "a", NewPath: "b", Overwrite: true, ExpectedReadRevision: a, ExpectedDestinationRevision: b}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "b")); string(data) != "alpha\n" {
		t.Fatal("move failed")
	}
	if _, err := service.Edit(t.Context(), EditRequest{Action: "delete", Path: "b", ExpectedReadRevision: b}); err == nil {
		t.Fatal("stale delete accepted")
	}
	b = revision("b")
	patch := "*** Begin Patch\n*** Update File: b\n@@\n-alpha\n+updated\n*** Add File: c\n+new\n*** End Patch"
	if _, err := service.Edit(t.Context(), EditRequest{Action: "patch", Workdir: root, Patch: patch, ExpectedRevisions: map[string]string{"b": b}}); err == nil {
		t.Fatal("partial patch guard accepted")
	}
	if _, err := service.Edit(t.Context(), EditRequest{Action: "patch", Workdir: root, Patch: patch, ExpectedRevisions: map[string]string{"b": b, "c": "absent"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Edit(t.Context(), EditRequest{Action: "delete", Path: "b", ExpectedReadRevision: revision("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "b")); !os.IsNotExist(err) {
		t.Fatal("guarded delete did not remove file")
	}
}
func TestSearchAndReadUsesSameCanonicalSource(t *testing.T) {
	service, root := newCodeToolsRuntime(t)
	_ = os.WriteFile(filepath.Join(root, "source.txt"), []byte("one\nneedle\nthree\nneedle\nend\n"), 0600)
	result, err := service.SearchAndRead(context.Background(), SearchReadRequest{Path: root, Query: "needle", ContextLines: 2})
	if err != nil {
		t.Fatal(err)
	}
	blocks := result["blocks"].([]map[string]any)
	if len(blocks) != 1 || result["file_reads"] != 1 || result["search_and_read_atomic"] != false {
		t.Fatalf("%#v", result)
	}
	if !strings.Contains(blocks[0]["content"].(string), "needle") || blocks[0]["read_revision"] == "" {
		t.Fatal("missing bounded read/revision")
	}
	// A leading '-' is always a search pattern, never a ripgrep option.
	result, err = service.SearchAndRead(t.Context(), SearchReadRequest{Path: filepath.Join(root, "source.txt"), Query: "--hidden"})
	if err != nil {
		t.Fatal(err)
	}
	if result["request_count"] != 0 {
		t.Fatal("pattern was interpreted as a command option")
	}
}
