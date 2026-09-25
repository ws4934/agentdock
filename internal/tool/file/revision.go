package file

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// A read revision binds the exact full-file bytes to the canonical source path.
// Slice length does not weaken the file-level precondition.
func readRevision(path string, data []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return fmt.Sprintf("read1:%x", h.Sum(nil))
}
func checkReadRevision(path string, data []byte, exists bool, expected string) error {
	if expected == "" {
		return nil
	}
	actual := "absent"
	if exists {
		actual = readRevision(path, data)
	}
	if expected != actual {
		return toolErrorDetails("READ_REVISION_CONFLICT", "file no longer matches the observed revision; read current source before editing", "conflict", map[string]any{"path": path, "current_revision": actual})
	}
	return nil
}

func (s *Service) editGuardedPath(r EditRequest) (Result, error) {
	if r.Action != "delete" && r.Action != "move" {
		return nil, toolError("INVALID_ARGUMENT", "guarded path operation must be delete or move", "validation")
	}
	src, err := s.ws.ResolveExisting(r.Path)
	if err != nil {
		return nil, err
	}
	read, err := readBoundedFile(src.Abs, maxTextFileReadBytes)
	if err != nil {
		return nil, err
	}
	if read.TooLarge || !read.Info.Mode().IsRegular() {
		return nil, toolError("REVISION_UNAVAILABLE", "guarded operations require a regular file within the text size limit", "validation")
	}
	if r.ExpectedReadRevision == "" {
		return nil, toolError("INVALID_ARGUMENT", "expected_read_revision is required for a guarded move/delete", "validation")
	}
	if err = checkReadRevision(src.Abs, read.Data, true, r.ExpectedReadRevision); err != nil {
		return nil, err
	}
	staged := map[string]stagedPatchFile{src.Abs: {Abs: src.Abs, Display: src.Display, Mode: read.Info.Mode().Perm(), Original: read.Data, OriginalExists: true}}
	result := Result{"action": r.Action, "path": src.Display, "dry_run": r.DryRun, "changed": true, "summary": r.Action + " " + src.Display}
	if r.Action == "move" {
		if r.NewPath == "" {
			return nil, toolError("INVALID_ARGUMENT", "new_path required", "validation")
		}
		dest, err := s.ws.ResolveForWrite(r.NewPath)
		if err != nil {
			return nil, err
		}
		if src.Abs == dest.Abs {
			result["changed"] = false
			return result, nil
		}
		if dest.Exists && !r.Overwrite {
			return nil, toolError("FILE_EXISTS", "destination exists", "conflict")
		}
		var original []byte
		if dest.Exists {
			if r.ExpectedDestinationRevision == "" {
				return nil, toolError("INVALID_ARGUMENT", "guarded overwrite requires expected_destination_revision", "validation")
			}
			d, err := readBoundedFile(dest.Abs, maxTextFileReadBytes)
			if err != nil {
				return nil, err
			}
			if d.TooLarge || !d.Info.Mode().IsRegular() {
				return nil, toolError("REVISION_UNAVAILABLE", "destination is not a bounded regular file", "validation")
			}
			original = d.Data
		}
		if err = checkReadRevision(dest.Abs, original, dest.Exists, r.ExpectedDestinationRevision); err != nil {
			return nil, err
		}
		content := string(read.Data)
		staged[dest.Abs] = stagedPatchFile{Abs: dest.Abs, Display: dest.Display, Content: &content, Mode: read.Info.Mode().Perm(), Original: original, OriginalExists: dest.Exists}
		result["new_path"] = dest.Display
	}
	if !r.DryRun {
		if err = commitStagedPatch(staged); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Service) verifyPatchRevisions(base string, staged map[string]stagedPatchFile, guards map[string]string) error {
	if len(guards) == 0 {
		return nil
	}
	if len(guards) > 128 {
		return toolError("INVALID_ARGUMENT", "too many patch revisions", "validation")
	}
	checked := map[string]bool{}
	for path, revision := range guards {
		if strings.TrimSpace(revision) == "" {
			return toolError("INVALID_ARGUMENT", "empty patch revision", "validation")
		}
		raw, err := patchPathInBase(base, path)
		if err != nil {
			return err
		}
		p, err := s.ws.ResolveForWrite(raw)
		if err != nil {
			return err
		}
		file, ok := staged[p.Abs]
		if !ok {
			return toolError("INVALID_ARGUMENT", "revision supplied for a path not touched by the patch", "validation")
		}
		if err = checkReadRevision(p.Abs, file.Original, file.OriginalExists, revision); err != nil {
			return err
		}
		checked[p.Abs] = true
	}
	if len(checked) != len(staged) {
		return toolError("INVALID_ARGUMENT", "guarded patch requires revisions for every touched path; use absent for additions", "validation")
	}
	return nil
}

func ensureRegular(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return toolError("NOT_REGULAR_FILE", "text tools require regular files", "validation")
	}
	return nil
}
