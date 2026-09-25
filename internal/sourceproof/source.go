// Package sourceproof records bounded observations of source bytes. A matching
// before/after fence is evidence of observed stability, not filesystem isolation.
package sourceproof

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
)

const MaxFiles = 20000
const MaxBytes int64 = 256 << 20

type Snapshot struct {
	Root       string    `json:"root"`
	Revision   string    `json:"revision,omitempty"`
	Head       string    `json:"head,omitempty"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
	Complete   bool      `json:"complete"`
	Reason     string    `json:"reason,omitempty"`
	Scope      []string  `json:"scope,omitempty"`
	CapturedAt time.Time `json:"captured_at"`
	Method     string    `json:"method"`
}

// CanonicalRoot never treats the filesystem root as a project.
func CanonicalRoot(raw string) (string, error) {
	root, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || filepath.Dir(root) == root {
		return "", errors.New("a project directory is required")
	}
	return root, nil
}

func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

type boundedOutput struct {
	data     []byte
	max      int
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remain := b.max - len(b.data)
	if len(p) > remain {
		b.overflow = true
		p = p[:max(0, remain)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

// Git observations do not invoke diff drivers, hooks, fsmonitor or lazy fetches.
func Git(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	base := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.untrackedCache=false", "-C", root}
	filters, err := disabledFilters(ctx, base)
	if err != nil {
		return nil, err
	}
	base = append(base, filters...)
	command := exec.CommandContext(ctx, "git", append(base, args...)...)
	command.Env = GitEnvironment()
	out := &boundedOutput{max: 8 << 20}
	command.Stdout = out
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("git observation failed: %w", err)
	}
	if out.overflow {
		return nil, errors.New("git observation exceeded output limit")
	}
	return out.data, nil
}

func sourcePaths(ctx context.Context, root string, scope []string) ([]string, string, error) {
	paths := map[string]bool{}
	head := ""
	if len(scope) == 0 {
		data, err := Git(ctx, root, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, "", errors.New("non-Git projects require explicit source_paths")
		}
		top, err := CanonicalRoot(strings.TrimSpace(string(data)))
		if err != nil || top != root {
			return nil, "", errors.New("use the Git project root or explicit source_paths")
		}
		data, err = Git(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if err != nil {
			return nil, "", err
		}
		for _, name := range strings.Split(string(data), "\x00") {
			if name != "" {
				paths[filepath.FromSlash(name)] = true
			}
		}
		if h, e := Git(ctx, root, "rev-parse", "--verify", "HEAD"); e == nil {
			head = strings.TrimSpace(string(h))
		}
	} else {
		if len(scope) > 128 {
			return nil, "", errors.New("at most 128 source_paths")
		}
		for _, name := range scope {
			if !filepath.IsLocal(name) || name == "." {
				return nil, "", errors.New("source_paths must be local relative paths, not the whole directory")
			}
			full := filepath.Join(root, name)
			err := filepath.WalkDir(full, func(path string, d os.DirEntry, err error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil || !filepath.IsLocal(rel) {
					return errors.New("source path escaped project")
				}
				if d.IsDir() {
					if d.Name() == ".git" {
						return errors.New("Git metadata is not a source scope")
					}
					return nil
				}
				paths[rel] = true
				if len(paths) > MaxFiles {
					return errors.New("source file limit exceeded")
				}
				return nil
			})
			if err != nil {
				return nil, "", err
			}
		}
	}
	if len(paths) > MaxFiles {
		return nil, "", errors.New("source file limit exceeded")
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		if !filepath.IsLocal(path) {
			return nil, "", errors.New("invalid source path")
		}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, head, nil
}

func Capture(ctx context.Context, raw string, scope []string) Snapshot {
	snapshot := Snapshot{Root: raw, Scope: append([]string(nil), scope...), CapturedAt: time.Now().UTC(), Method: "bounded-before-after-content-and-metadata"}
	root, err := CanonicalRoot(raw)
	if err != nil {
		snapshot.Reason = "project_unavailable"
		return snapshot
	}
	snapshot.Root = root
	paths, head, err := sourcePaths(ctx, root, scope)
	if err != nil {
		snapshot.Reason = err.Error()
		return snapshot
	}
	snapshot.Head = head
	if len(paths) == 0 {
		snapshot.Reason = "no_source_files"
		return snapshot
	}
	hash := sha256.New()
	_, _ = fmt.Fprintln(hash, head)
	for _, name := range paths {
		if ctx.Err() != nil {
			snapshot.Reason = "source_observation_cancelled"
			return snapshot
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(hash, "deleted:%s\x00", name)
			snapshot.Files++
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			snapshot.Reason = "source_contains_unreadable_or_nonregular_entry"
			return snapshot
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil || !Within(root, real) {
			snapshot.Reason = "source_escapes_project"
			return snapshot
		}
		if info.Size() > MaxBytes-snapshot.Bytes {
			snapshot.Reason = "source_byte_limit_exceeded"
			return snapshot
		}
		file, err := os.Open(path)
		if err != nil {
			snapshot.Reason = "source_unreadable"
			return snapshot
		}
		contents := sha256.New()
		n, readErr := io.Copy(contents, io.LimitReader(file, info.Size()+1))
		after, statErr := file.Stat()
		_ = file.Close()
		if readErr != nil || statErr != nil || n != info.Size() || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || info.Mode() != after.Mode() {
			snapshot.Reason = "source_changed_during_observation"
			return snapshot
		}
		// Include metadata to conservatively detect writes that restore the same bytes.
		entry, _ := json.Marshal([]any{name, info.Mode().String(), info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(contents.Sum(nil))})
		_, _ = hash.Write(entry)
		snapshot.Files++
		snapshot.Bytes += n
	}
	again, againHead, err := sourcePaths(ctx, root, scope)
	if err != nil || againHead != head || strings.Join(paths, "\x00") != strings.Join(again, "\x00") {
		snapshot.Reason = "source_set_changed_during_observation"
		return snapshot
	}
	snapshot.Complete = true
	snapshot.Revision = "src1:" + hex.EncodeToString(hash.Sum(nil))
	return snapshot
}

func Matches(a, b Snapshot) bool {
	return a.Complete && b.Complete && a.Root == b.Root && a.Revision != "" && a.Revision == b.Revision
}

// Git routing/configuration environment must not redirect an observation to a
// different repository or inject configuration, hooks, or an SSH command.
func GitEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+6)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_PROTOCOL_FROM_USER=0")
}

// Even git status may invoke clean/process filters. Enumerate names without
// running project programs, then neutralize all filter directions per command.
var filterKey = regexp.MustCompile(`^filter\.[A-Za-z0-9_.-]+\.(clean|smudge|process|required)$`)

func disabledFilters(ctx context.Context, base []string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", append(append([]string(nil), base...), "config", "--includes", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)...)
	command.Env = GitEnvironment()
	out := &boundedOutput{max: 16384}
	command.Stdout = out
	command.Stderr = io.Discard
	err := command.Run()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("read Git filter names: %w", err)
		}
	}
	if out.overflow {
		return nil, errors.New("too many Git filters")
	}
	seen := map[string]bool{}
	flags := []string{}
	for _, key := range strings.Fields(string(out.data)) {
		if !filterKey.MatchString(key) {
			return nil, errors.New("unsupported Git filter name")
		}
		prefix := key[:strings.LastIndexByte(key, '.')]
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		if len(seen) > 64 {
			return nil, errors.New("too many Git filters")
		}
		flags = append(flags, "-c", prefix+".clean=", "-c", prefix+".smudge=", "-c", prefix+".process=", "-c", prefix+".required=false")
	}
	return flags, nil
}
