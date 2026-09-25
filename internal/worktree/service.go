// Package worktree creates explicitly requested, detached Git worktrees. This
// isolates edits, not process authority, and never changes the original workdir.
package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/sourceproof"
)

type Service struct{ root string }

func New(home string) *Service {
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return &Service{root: filepath.Join(home, "worktrees")}
}

type Request struct {
	Action     string `json:"action"`
	RequestID  string `json:"request_id,omitempty"`
	WorktreeID string `json:"worktree_id,omitempty"`
	Project    string `json:"project,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
	Label      string `json:"label,omitempty"`
}
type Record struct {
	ID          string    `json:"worktree_id"`
	Project     string    `json:"project"`
	Path        string    `json:"path"`
	BaseCommit  string    `json:"base_commit"`
	Label       string    `json:"label,omitempty"`
	Status      string    `json:"status"`
	SourceDirty bool      `json:"source_dirty"`
	CreatedAt   time.Time `json:"created_at"`
	Failure     string    `json:"failure,omitempty"`
	CurrentHead string    `json:"current_head,omitempty"`
	Dirty       *bool     `json:"dirty,omitempty"`
	Scope       string    `json:"scope"`
}

var idRE = regexp.MustCompile(`^wt_[0-9a-f]{32}$`)
var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var keyRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
var filterKeyRE = regexp.MustCompile(`^filter\.[A-Za-z0-9_.-]+\.(smudge|process|required)$`)

func (s *Service) prepare() error {
	for _, path := range []string{s.root, filepath.Join(s.root, "records"), filepath.Join(s.root, "checkouts")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed worktree root must be a real directory")
		}
		if err = securepath.EnsurePrivate(path); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) load(id string) (Record, error) {
	if !idRE.MatchString(id) {
		return Record{}, errors.New("invalid worktree_id")
	}
	path := filepath.Join(s.root, "records", id+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return Record{}, errors.New("invalid worktree metadata")
	}
	file, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err = json.Unmarshal(b, &record); err != nil {
		return record, err
	}
	if record.ID != id || record.Path != filepath.Join(s.root, "checkouts", id) {
		return record, errors.New("worktree metadata scope mismatch")
	}
	return record, nil
}
func (s *Service) save(r Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.root, "records", r.ID+".json"), data, 0600)
}

func (s *Service) Create(ctx context.Context, r Request) (Record, error) {
	if !keyRE.MatchString(r.RequestID) || !commitRE.MatchString(r.BaseCommit) || len(r.Label) > 256 {
		return Record{}, errors.New("create requires a stable request_id, an exact local 40-hex commit and a short label")
	}
	root, err := sourceproof.CanonicalRoot(r.Project)
	if err != nil {
		return Record{}, err
	}
	top, err := sourceproof.Git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return Record{}, err
	}
	canonical, err := sourceproof.CanonicalRoot(strings.TrimSpace(string(top)))
	if err != nil || canonical != root {
		return Record{}, errors.New("project must be the exact Git worktree root")
	}
	resolved, err := sourceproof.Git(ctx, root, "rev-parse", "--verify", r.BaseCommit+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolved)) != r.BaseCommit {
		return Record{}, errors.New("base_commit must identify a locally available commit, not a tag or expression")
	}
	if err = s.prepare(); err != nil {
		return Record{}, err
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	guard, err := processlock.Acquire(deadline, filepath.Join(s.root, "write.lock"))
	if err != nil {
		return Record{}, err
	}
	defer guard.Release()
	sum := sha256.Sum256([]byte(root + "\x00" + r.RequestID))
	id := "wt_" + hex.EncodeToString(sum[:16])
	old, err := s.load(id)
	if err == nil {
		if old.Project != root || old.BaseCommit != r.BaseCommit || old.Label != r.Label {
			return Record{}, errors.New("WORKTREE_IDEMPOTENCY_CONFLICT")
		}
		return s.Status(ctx, id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	data, err := sourceproof.Git(ctx, root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return Record{}, err
	}
	record := Record{ID: id, Project: root, Path: filepath.Join(s.root, "checkouts", id), BaseCommit: r.BaseCommit, Label: r.Label, Status: "creating", SourceDirty: len(data) > 0, CreatedAt: time.Now().UTC(), Scope: "edit_isolation_not_security_sandbox"}
	if _, err = os.Lstat(record.Path); err == nil {
		return Record{}, errors.New("worktree destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	if err = s.save(record); err != nil {
		return Record{}, err
	}
	// Git checkout filters and hooks can execute arbitrary repository-configured
	// programs. Disable every configured filter while creating the source snapshot.
	flags, err := checkoutSafety(ctx, root)
	if err != nil {
		record.Status = "failed"
		record.Failure = "unsafe_checkout_configuration"
		_ = s.save(record)
		return record, err
	}
	args := append(flags, "worktree", "add", "--detach", "--", record.Path, r.BaseCommit)
	_, err = sourceproof.Git(ctx, root, args...)
	if err != nil {
		record.Status = "outcome_unknown"
		record.Failure = "worktree_creation_incomplete; inspect rather than retrying"
		_ = s.save(record)
		return record, err
	}
	record.Status = "ready"
	if err = s.save(record); err != nil {
		return record, err
	}
	return s.Status(ctx, id)
}
func checkoutSafety(ctx context.Context, root string) ([]string, error) {
	flags := []string{"-c", "protocol.allow=never", "-c", "submodule.recurse=false", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}
	data, err := sourceproof.Git(ctx, root, "config", "--includes", "--name-only", "--get-regexp", `^filter\..*\.(smudge|process|required)$`)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, key := range strings.Fields(string(data)) {
		if !filterKeyRE.MatchString(key) {
			return nil, errors.New("unsupported filter configuration")
		}
		prefix := key[:strings.LastIndexByte(key, '.')]
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		flags = append(flags, "-c", prefix+".smudge=", "-c", prefix+".process=", "-c", prefix+".required=false")
	}
	if len(seen) > 64 {
		return nil, errors.New("too many Git filters")
	}
	return flags, nil
}
func (s *Service) Status(ctx context.Context, id string) (Record, error) {
	record, err := s.load(id)
	if err != nil {
		return record, err
	}
	if record.Status != "ready" {
		return record, nil
	}
	path, err := sourceproof.CanonicalRoot(record.Path)
	if err != nil || path != record.Path {
		record.Status = "unavailable"
		record.Failure = "managed_path_missing_or_redirected"
		return record, nil
	}
	data, err := sourceproof.Git(ctx, record.Project, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return record, err
	}
	if !registeredWorktree(data, record.Path) {
		record.Status = "unavailable"
		record.Failure = "worktree_registration_missing"
		return record, nil
	}
	head, err := sourceproof.Git(ctx, path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return record, err
	}
	record.CurrentHead = strings.TrimSpace(string(head))
	status, err := sourceproof.Git(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return record, err
	}
	dirty := len(status) > 0
	record.Dirty = &dirty
	return record, nil
}
func (s *Service) List(ctx context.Context) ([]Record, bool, error) {
	file, err := os.Open(filepath.Join(s.root, "records"))
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	entries, err := file.ReadDir(201)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	partial := len(entries) > 200
	if partial {
		entries = entries[:200]
	}
	records := []Record{}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !idRE.MatchString(id) {
			continue
		}
		r, err := s.load(id)
		if err != nil {
			return nil, false, fmt.Errorf("invalid managed worktree metadata: %w", err)
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, partial, nil
}
