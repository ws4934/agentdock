package file

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackRestoreNeverOverwritesConcurrentCreation(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "source")
	backup := filepath.Join(dir, "backup")
	if err := os.WriteFile(backup, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared := []preparedPatchFile{{file: stagedPatchFile{Abs: target, Display: target, OriginalExists: true}, backupPath: backup}}
	restore := func(from, to string) error {
		if err := os.WriteFile(to, []byte("concurrent"), 0600); err != nil {
			return err
		}
		return renameNoReplace(from, to)
	}
	err := rollbackPatch(prepared, nil, restore, errors.New("injected failure"))
	data, _ := os.ReadFile(target)
	old, _ := os.ReadFile(backup)
	if string(data) != "concurrent" || string(old) != "original" || !strings.Contains(err.Error(), backup) {
		t.Fatalf("target=%q backup=%q error=%v", data, old, err)
	}
}
func TestRollbackPreservesWritableWithdrawnInode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "source")
	backup := filepath.Join(dir, "backup")
	for p, c := range map[string]string{target: "patched", backup: "original"} {
		if err := os.WriteFile(p, []byte(c), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	updated := "patched"
	item := preparedPatchFile{file: stagedPatchFile{Abs: target, Display: target, Mode: 0600, OriginalExists: true, Content: &updated}, backupPath: backup, installed: true}
	_ = rollbackPatch([]preparedPatchFile{item}, nil, renameNoReplace, errors.New("injected"))
	if _, err = writer.WriteAt([]byte("external"), 0); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, ".agentdock-rollback-*", "withdrawn"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("%v %v", paths, err)
	}
	saved, _ := os.ReadFile(paths[0])
	restored, _ := os.ReadFile(target)
	if string(saved) != "external" || string(restored) != "original" {
		t.Fatalf("saved=%q restored=%q", saved, restored)
	}
}
