package jobrun

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

var idPattern = regexp.MustCompile(`^job_[a-f0-9]{32}$`)
var requestPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type Store struct {
	Root       string
	executable string
}

func New(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("job root must be a real directory")
	}
	if err = securepath.EnsurePrivate(abs); err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Store{Root: abs, executable: binary}, nil
}
func regular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("state must be a regular file")
	}
	return nil
}
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0600)
}
func readJSON(path string, value any) error {
	if err := regular(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("job state exceeds limit")
	}
	return json.Unmarshal(data, value)
}
func (s *Store) directory(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", errors.New("invalid job_id")
	}
	path := filepath.Join(s.Root, id)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid job directory")
	}
	return path, nil
}
func lock(ctx context.Context, path string) (*processlock.Lock, error) {
	if _, err := os.Lstat(path); err == nil {
		if err = regular(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return processlock.Acquire(ctx, path)
}
func (s *Store) key() ([]byte, error) {
	path := filepath.Join(s.Root, "identity.key")
	if errors.Is(regular(path), os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := atomicfile.Write(path, key, 0600); err != nil {
			return nil, err
		}
	}
	if err := regular(path); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid job identity key")
	}
	return key, nil
}
func digest(key []byte, value any) string {
	b, _ := json.Marshal(value)
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// Start is idempotent on a caller-supplied request ID. A persisted but unconfirmed
// launch is never submitted again. Environment values only live in the process.
func (s *Store) Start(ctx context.Context, requestID string, spec Spec, env []string) (Record, error) {
	if !requestPattern.MatchString(requestID) {
		return Record{}, errors.New("request_id must be a stable 1..128 character identifier")
	}
	if (spec.Command == "") == (len(spec.Argv) == 0) {
		return Record{}, errors.New("provide exactly one command or argv")
	}
	if len(spec.Command) > 128<<10 || len(spec.Argv) > 128 || len(spec.Title) > 512 || len(spec.TaskID) > 128 {
		return Record{}, errors.New("job request exceeds bounds")
	}
	for _, a := range spec.Argv {
		if len(a) > 32768 || strings.ContainsRune(a, 0) {
			return Record{}, errors.New("invalid argv")
		}
	}
	if spec.TimeoutMS <= 0 || spec.TimeoutMS > 86400000 {
		return Record{}, errors.New("timeout_ms must be between 1 and 86400000")
	}
	root, err := sourceproof.CanonicalRoot(spec.Workdir)
	if err != nil {
		return Record{}, err
	}
	spec.Workdir = root
	if spec.Validation != nil {
		if err = validateAdapter(spec); err != nil {
			return Record{}, err
		}
	}
	if err = ctx.Err(); err != nil {
		return Record{}, err
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	guard, err := lock(deadline, filepath.Join(s.Root, "admission.lock"))
	if err != nil {
		return Record{}, err
	}
	defer guard.Release()
	key, err := s.key()
	if err != nil {
		return Record{}, err
	}
	id := "job_" + digest(key, requestID)[:32]
	environment := executionEnvironment(env)
	definition := digest(key, struct {
		Spec Spec
		Env  []string
	}{spec, environment})
	dir := filepath.Join(s.Root, id)
	if _, err := os.Lstat(dir); err == nil {
		old, err := s.Status(id)
		if err != nil {
			return Record{}, err
		}
		if old.StateUnavailable {
			return Record{}, errors.New("JOB_STATE_UNAVAILABLE: inspect the original receipt; execution is never replayed")
		}
		if old.DefinitionHash != definition {
			return Record{}, errors.New("JOB_IDEMPOTENCY_CONFLICT: request_id already identifies a different execution")
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	records, err := s.records()
	if err != nil {
		return Record{}, err
	}
	active := 0
	for _, r := range records {
		if !r.Terminal() && !r.Archived {
			active++
		}
	}
	if active >= MaxRunning {
		return Record{}, errors.New("managed job concurrency limit reached; inspect existing jobs")
	}
	if len(records) >= 10000 {
		return Record{}, errors.New("job history limit reached; administrative retention required")
	}
	// 完整的准入数据先写入不可见暂存目录，再原子发布；崩溃不留下半条公开记录。
	stage, err := os.MkdirTemp(s.Root, ".admission-")
	if err != nil {
		return Record{}, err
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = securepath.EnsurePrivate(stage); err != nil {
		return Record{}, err
	}
	kind := "command"
	if spec.Validation != nil {
		kind = "validation"
	}
	record := Record{SchemaVersion: SchemaVersion, ID: id, DefinitionHash: definition, Title: spec.Title, Workdir: root, TaskID: spec.TaskID, Kind: kind, Status: "starting", CreatedAt: time.Now().UTC(), TimeoutMS: spec.TimeoutMS, ObservationOnly: true, BootID: bootID()}
	if err = writeJSON(filepath.Join(stage, "request.json"), spec); err != nil {
		return Record{}, err
	}
	if err = writeJSON(filepath.Join(stage, "record.json"), record); err != nil {
		return Record{}, err
	}
	if err = writeJSON(filepath.Join(stage, "receipt.json"), record); err != nil {
		return Record{}, err
	}
	if err = os.Rename(stage, dir); err != nil {
		return Record{}, err
	}
	stage = ""
	child := exec.Command(s.executable, "job-supervise", s.Root, id)
	child.Env = environment
	child.Dir = s.Root
	detach(child)
	if err = child.Start(); err != nil {
		now := time.Now().UTC()
		record.Status = "launch_failed"
		record.Failure = "supervisor_start_failed"
		record.FinishedAt = &now
		if saveErr := writeJSON(filepath.Join(dir, "record.json"), record); saveErr != nil {
			return Record{}, errors.Join(err, saveErr)
		}
		return record, nil
	}
	go func() { _ = child.Wait() }()
	// Return the receipt promptly; startup confirmation is an observation, not a retry.
	return record, nil
}

func (s *Store) Status(id string) (Record, error) {
	dir, err := s.directory(id)
	if err != nil {
		return Record{}, err
	}
	r, err := readRecordState(dir, id)
	if err != nil {
		return Record{}, err
	}
	_, err = os.Stat(filepath.Join(dir, "cancel.request"))
	r.CancellationRequested = err == nil
	r.ObservationOnly = true
	ownerPath := filepath.Join(dir, "owner.lock")
	if _, err = os.Lstat(ownerPath); err == nil {
		if err = regular(ownerPath); err != nil {
			return Record{}, err
		}
	}
	owner, available, err := processlock.TryAcquire(ownerPath)
	if err != nil {
		return Record{}, err
	}
	if available {
		_ = owner.Release()
	}
	r.OwnerAlive = !available
	if !r.Terminal() && !r.OwnerAlive && time.Since(r.CreatedAt) > 10*time.Second {
		r.Status = "outcome_unknown"
		r.Failure = "supervisor_unavailable; execution is not replayed"
	}
	return r, nil
}
func (s *Store) records() ([]Record, error) {
	file, err := os.Open(s.Root)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(10016)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) >= 10016 {
		return nil, errors.New("job registry directory exceeds bound")
	}
	result := []Record{}
	for _, entry := range entries {
		if !idPattern.MatchString(entry.Name()) {
			continue
		}
		r, err := s.Status(entry.Name())
		if err != nil {
			// 隔离损坏项，其他任务仍可查询和提交。未知项保守占一个名额，且列表明确不完整。
			r = Record{SchemaVersion: SchemaVersion, ID: entry.Name(), Status: "state_unavailable", StateUnavailable: true, Failure: "job_state_unreadable; inspect this receipt without replay", ObservationOnly: true}
		}
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}
func (s *Store) List(taskID string, limit int) ([]Record, bool, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	all, err := s.records()
	if err != nil {
		return nil, false, err
	}
	result := []Record{}
	partial := false
	for _, r := range all {
		if r.StateUnavailable {
			partial = true
		}
		if r.Archived || (taskID != "" && r.TaskID != taskID) {
			continue
		}
		if len(result) >= limit {
			return result, true, nil
		}
		result = append(result, r)
	}
	return result, partial, nil
}
func (s *Store) Cancel(id string) (Record, error) {
	r, err := s.Status(id)
	if err != nil || r.Terminal() {
		return r, err
	}
	dir, err := s.directory(id)
	if err != nil {
		return Record{}, err
	}
	if err = atomicfile.Write(filepath.Join(dir, "cancel.request"), []byte("cancel\n"), 0600); err != nil {
		return Record{}, err
	}
	r.CancellationRequested = true
	return r, nil
}
func (s *Store) Archive(ctx context.Context, id string) (Record, error) {
	guard, err := lock(ctx, filepath.Join(s.Root, "admission.lock"))
	if err != nil {
		return Record{}, err
	}
	defer guard.Release()
	r, err := s.Status(id)
	if err != nil {
		return Record{}, err
	}
	if !r.Terminal() || r.OwnerAlive {
		return Record{}, errors.New("only confirmed terminal jobs can be archived")
	}
	dir, err := s.directory(id)
	if err != nil {
		return Record{}, err
	}
	r.Archived = true
	if err = writeJSON(filepath.Join(dir, "record.json"), r); err != nil {
		return Record{}, err
	}
	// Keep the idempotency tombstone. Reusing a request ID must never rerun effects.
	for _, name := range []string{"stdout.log", "stderr.log", "request.json", "validation.xml"} {
		if err = os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
	}
	return r, nil
}

func (s *Store) ReadLog(id, stream string, offset int64, maxBytes int) (LogChunk, error) {
	if stream != "stdout" && stream != "stderr" {
		return LogChunk{}, errors.New("stream must be stdout or stderr")
	}
	if offset < 0 || offset > MaxLogBytes {
		return LogChunk{}, errors.New("invalid log offset")
	}
	if maxBytes < 1 || maxBytes > 256<<10 {
		maxBytes = 65536
	}
	r, err := s.Status(id)
	if err != nil {
		return LogChunk{}, err
	}
	if r.Archived {
		return LogChunk{}, errors.New("job logs were archived")
	}
	state := r.Stdout
	if stream == "stderr" {
		state = r.Stderr
	}
	chunk := LogChunk{JobID: id, Stream: stream, Offset: offset, NextOffset: offset, Encoding: "utf-8", TotalBytes: state.Total, RetainedBytes: state.Retained, DroppedBytes: state.Dropped, Truncated: state.Dropped > 0, Terminal: r.Terminal()}
	if offset > state.Retained {
		return chunk, errors.New("offset exceeds retained log; reset to offset 0")
	}
	dir, _ := s.directory(id)
	path := filepath.Join(dir, stream+".log")
	if err = regular(path); errors.Is(err, os.ErrNotExist) && state.Retained == 0 {
		return chunk, nil
	} else if err != nil {
		return chunk, err
	}
	f, err := os.Open(path)
	if err != nil {
		return chunk, err
	}
	defer f.Close()
	b := make([]byte, min(int64(maxBytes), state.Retained-offset))
	n, err := f.ReadAt(b, offset)
	if err != nil && err != io.EOF {
		return chunk, err
	}
	chunk.Data = strings.ToValidUTF8(string(b[:n]), "�")
	if chunk.Data != string(b[:n]) {
		chunk.Encoding = "utf-8-lossy"
	}
	chunk.NextOffset = offset + int64(n)
	return chunk, nil
}
