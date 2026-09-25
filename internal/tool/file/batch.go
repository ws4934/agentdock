package file

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// BatchReadRequest intentionally supports native file paths only. Individual
// blocks retain independent failures and a mapping back to original requests.
type BatchReadRequest struct {
	Requests      []ReadRange `json:"requests"`
	MaxTotalBytes int         `json:"max_total_bytes,omitempty"`
}
type ReadRange struct {
	Cursor               string `json:"cursor,omitempty"`
	Path                 string `json:"path"`
	StartLine            int    `json:"start_line,omitempty"`
	EndLine              int    `json:"end_line,omitempty"`
	ExpectedReadRevision string `json:"expected_read_revision,omitempty"`
}
type readPlan struct {
	cursor                  string
	path, display, revision string
	start, end              int
	indexes                 []int
}

func (s *Service) nativeReadPath(raw string) (string, string, error) {
	if raw == "" {
		return "", "", errors.New("path is required")
	}
	if strings.HasPrefix(raw, "skill://") {
		if s.resolveSkillResource == nil {
			return "", "", errors.New("Skill resources are not configured")
		}
		return s.resolveSkillResource(raw)
	}
	p, err := s.ws.ResolveExisting(raw)
	return p.Abs, p.Display, err
}

func (s *Service) ReadFiles(ctx context.Context, request BatchReadRequest) (Result, error) {
	if len(request.Requests) == 0 || len(request.Requests) > 16 {
		return nil, toolError("INVALID_ARGUMENT", "read_files requires 1..16 ranges", "validation")
	}
	budget := request.MaxTotalBytes
	if budget == 0 {
		budget = 262144
	}
	if budget < 1 || budget > 1<<20 {
		return nil, toolError("INVALID_ARGUMENT", "max_total_bytes must be 1..1048576", "validation")
	}
	plan := make([]readPlan, 0, len(request.Requests))
	blocks := make([]map[string]any, 0, len(request.Requests))
	partial := false
	for i, r := range request.Requests {
		if r.Cursor != "" && (r.StartLine != 0 || r.EndLine != 0) {
			return nil, toolError("INVALID_ARGUMENT", "cursor cannot be combined with line selection", "validation")
		}
		start := r.StartLine
		if start == 0 {
			start = 1
		}
		end := r.EndLine
		if end == 0 {
			end = start + 199
		}
		if start < 1 || end < start || end-start >= 400 {
			return nil, toolError("INVALID_ARGUMENT", "each range must contain 1..400 positive lines", "validation")
		}
		path, display, err := s.nativeReadPath(r.Path)
		if err != nil {
			blocks = append(blocks, map[string]any{"request_indexes": []int{i}, "path": r.Path, "error": err.Error()})
			partial = true
			continue
		}
		plan = append(plan, readPlan{cursor: r.Cursor, path: path, display: display, revision: r.ExpectedReadRevision, start: start, end: end, indexes: []int{i}})
	}
	sort.SliceStable(plan, func(i, j int) bool {
		a, b := plan[i], plan[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if a.revision != b.revision {
			return a.revision < b.revision
		}
		return a.start < b.start
	})
	merged := make([]readPlan, 0, len(plan))
	for _, r := range plan {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if last.cursor == "" && r.cursor == "" && last.path == r.path && last.revision == r.revision && r.start <= last.end+21 && max(last.end, r.end)-last.start < 400 {
				last.end = max(last.end, r.end)
				last.indexes = append(last.indexes, r.indexes...)
				continue
			}
		}
		merged = append(merged, r)
	}
	// Read a source file once for every adjacent group. Keep only one full-file
	// buffer at a time; a batch never retains 16 x the maximum input size.
	lastPath := ""
	var data []byte
	var readErr error
	var inputBytes int64
	readCount := 0
	truncated := false
	returnedBytes := 0
	for _, r := range merged {
		block := map[string]any{"path": r.display, "request_indexes": r.indexes, "requested_start_line": r.start, "requested_end_line": r.end}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if budget == 0 {
			block["error"] = "OUTPUT_BUDGET_EXHAUSTED"
			blocks = append(blocks, block)
			partial = true
			truncated = true
			continue
		}
		if lastPath != r.path {
			lastPath = r.path
			data = nil
			readErr = nil
			if inputBytes >= 128<<20 {
				readErr = errors.New("INPUT_BUDGET_EXHAUSTED")
			} else {
				read, err := readBoundedFile(r.path, min(int64(maxTextFileReadBytes), (128<<20)-inputBytes))
				readCount++
				inputBytes += read.BytesRead
				switch {
				case err != nil:
					readErr = err
				case read.Info.IsDir():
					readErr = errors.New("IS_DIRECTORY")
				case read.TooLarge:
					readErr = errors.New("INPUT_BUDGET_EXHAUSTED")
				case looksBinary(read.Data) || !utf8.Valid(read.Data):
					readErr = errors.New("ENCODING_UNSUPPORTED")
				default:
					data = read.Data
				}
			}
		}
		if readErr != nil {
			block["error"] = readErr.Error()
			blocks = append(blocks, block)
			partial = true
			continue
		}
		revision := readRevision(r.path, data)
		if err := checkReadRevision(r.path, data, true, r.revision); err != nil {
			block["error"] = "READ_REVISION_CONFLICT"
			block["current_revision"] = revision
			blocks = append(blocks, block)
			partial = true
			continue
		}
		content, meta, pageErr := readTextPage(data, revision, r.start, r.end, budget, r.cursor)
		if pageErr != nil {
			block["error"] = pageErr.Error()
			blocks = append(blocks, block)
			partial = true
			continue
		}
		if meta.RangeEndLine-meta.Start >= 400 {
			block["error"] = "CURSOR_RANGE_TOO_LARGE: this cursor spans more than 400 remaining lines; continue through read_file"
			blocks = append(blocks, block)
			partial = true
			continue
		}
		block["content"] = content
		block["start_line"] = meta.Start
		block["end_line"] = meta.End
		block["total_lines"] = meta.Total
		block["read_revision"] = revision
		block["truncated"] = meta.Truncated
		if meta.NextStartLine > 0 {
			block["next_start_line"] = meta.NextStartLine
			block["next_start_byte"] = meta.NextStartByte
			block["next_cursor"] = meta.NextCursor
		}
		if meta.TruncatedReason != "" {
			block["truncated_reason"] = meta.TruncatedReason
		}
		budget -= len(content)
		returnedBytes += len(content)
		truncated = truncated || meta.Truncated
		blocks = append(blocks, block)
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		return blocks[i]["request_indexes"].([]int)[0] < blocks[j]["request_indexes"].([]int)[0]
	})
	return Result{"blocks": blocks, "request_count": len(request.Requests), "block_count": len(blocks), "file_reads": readCount, "input_bytes": inputBytes, "returned_bytes": returnedBytes, "partial": partial, "truncated": truncated, "observation_only": true}, nil
}

type SearchReadRequest struct {
	Path           string   `json:"path,omitempty"`
	Query          string   `json:"query"`
	Regex          bool     `json:"regex,omitempty"`
	CaseSensitive  bool     `json:"case_sensitive,omitempty"`
	IncludeGlobs   []string `json:"include_globs,omitempty"`
	ExcludeGlobs   []string `json:"exclude_globs,omitempty"`
	IncludeHidden  bool     `json:"include_hidden,omitempty"`
	IncludeIgnored bool     `json:"include_ignored,omitempty"`
	ContextLines   int      `json:"context_lines,omitempty"`
	MaxMatches     int      `json:"max_matches,omitempty"`
	MaxTotalBytes  int      `json:"max_total_bytes,omitempty"`
}

func (s *Service) SearchAndRead(ctx context.Context, r SearchReadRequest) (Result, error) {
	limit := r.MaxMatches
	if limit == 0 {
		limit = 8
	}
	if limit < 1 || limit > 16 {
		return nil, toolError("INVALID_ARGUMENT", "max_matches must be 1..16", "validation")
	}
	lines := r.ContextLines
	if lines == 0 {
		lines = 20
	}
	if lines < 1 || lines > 80 {
		return nil, toolError("INVALID_ARGUMENT", "context_lines must be 1..80", "validation")
	}
	search, err := s.SearchText(ctx, SearchRequest{Path: r.Path, Query: r.Query, Regex: r.Regex, CaseSensitive: r.CaseSensitive, IncludeGlobs: r.IncludeGlobs, ExcludeGlobs: r.ExcludeGlobs, IncludeHidden: r.IncludeHidden, IncludeIgnored: r.IncludeIgnored, MaxResults: &limit})
	if err != nil {
		return nil, err
	}
	// Normalize only the bounded search results; no command text is interpreted.
	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	encoded, err := json.Marshal(search["matches"])
	if err != nil {
		return nil, err
	}
	var matches []match
	if err = json.Unmarshal(encoded, &matches); err != nil {
		return nil, err
	}
	requests := make([]ReadRange, 0, len(matches))
	for _, m := range matches {
		if len(requests) >= limit {
			break
		}
		path := m.Path
		// Search matches use Workspace.Relative, not DefaultCWD. Preserve that origin.
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.ws.Root(), filepath.FromSlash(path))
		}
		if m.Line < 1 {
			continue
		}
		requests = append(requests, ReadRange{Path: path, StartLine: max(1, m.Line-lines), EndLine: m.Line + lines})
	}
	if len(requests) == 0 {
		return Result{"search": search, "blocks": []any{}, "request_count": 0, "partial": search["partial"], "truncated": search["truncated"], "observation_only": true}, nil
	}
	result, err := s.ReadFiles(ctx, BatchReadRequest{Requests: requests, MaxTotalBytes: r.MaxTotalBytes})
	if err != nil {
		return nil, err
	}
	result["search"] = search
	result["partial"] = result["partial"] == true || search["partial"] == true
	result["truncated"] = result["truncated"] == true || search["truncated"] == true
	result["search_and_read_atomic"] = false
	result["note"] = "Search and read are separate observations. The read_revision guards the returned source, not the earlier match."
	return result, nil
}

// A native search file target has a directory working directory, not itself.
func searchWorkingDirectory(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return filepath.Dir(path)
	}
	return path
}

// Capped process output is still drained; the parseable prefix is marked partial.
type searchBoundedOutput struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *searchBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if n > remaining {
		b.truncated = true
		p = p[:max(remaining, 0)]
	}
	b.data = append(b.data, p...)
	return n, nil
}
