package jobrun

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// 仅缓存目录摘要，不缓存授权或完整证据。活动/损坏项重读；终态原子写入或
// 移动会改变目录元数据。最终选中记录仍通过 Status 取得真实结果。
type indexEntry struct {
	RecordStamp                     time.Time
	RecordSize                      int64
	ID, TaskID                      string
	CreatedAt                       time.Time
	Terminal, Archived, Unavailable bool
	Stamp                           time.Time
}

func (s *Store) index() ([]indexEntry, error) {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	root, err := os.Open(s.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dirs, err := root.ReadDir(10016)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(dirs) >= 10016 {
		return nil, errors.New("job registry directory exceeds bound; archive known terminal records")
	}
	next := make(map[string]indexEntry, len(dirs))
	entries := make([]indexEntry, 0, len(dirs))
	for _, entry := range dirs {
		id := entry.Name()
		if !idPattern.MatchString(id) {
			continue
		}
		info, statErr := entry.Info()
		recordInfo, recordErr := os.Lstat(filepath.Join(s.Root, id, "record.json"))
		cached, ok := s.indexCache[id]
		if !ok || !cached.Terminal || cached.Unavailable || statErr != nil || !info.IsDir() || !info.ModTime().Equal(cached.Stamp) || recordErr != nil || !recordInfo.Mode().IsRegular() || !recordInfo.ModTime().Equal(cached.RecordStamp) || recordInfo.Size() != cached.RecordSize {
			r, readErr := s.Status(id)
			cached = indexEntry{ID: id, TaskID: r.TaskID, CreatedAt: r.CreatedAt, Terminal: r.Terminal() && !r.OwnerAlive, Archived: r.Archived, Unavailable: r.StateUnavailable || readErr != nil}
			if statErr == nil {
				cached.Stamp = info.ModTime()
			}
			if recordErr == nil {
				cached.RecordStamp = recordInfo.ModTime()
				cached.RecordSize = recordInfo.Size()
			}
		}
		next[id] = cached
		entries = append(entries, cached)
	}
	s.indexCache = next
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	return entries, nil
}

type ListPage struct {
	Jobs       []Record `json:"jobs"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Partial    bool     `json:"partial"`
	HasMore    bool     `json:"has_more"`
}
type pageCursor struct {
	Version   int       `json:"v"`
	Root      string    `json:"r"`
	Task      string    `json:"t"`
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

func rootHash(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])
}
func (s *Store) ListPage(taskID string, limit int, cursor string) (ListPage, error) {
	page := ListPage{Jobs: []Record{}}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var after pageCursor
	if cursor != "" {
		if len(cursor) > 2048 {
			return page, errors.New("invalid job cursor")
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(data, &after) != nil || after.Version != 1 || after.Root != rootHash(s.Root) || after.Task != taskID || !idPattern.MatchString(after.ID) {
			return page, errors.New("job cursor does not match this query")
		}
	}
	entries, err := s.index()
	if err != nil {
		return page, err
	}
	for _, entry := range entries {
		if entry.Unavailable {
			page.Partial = true
		}
	}
	for _, entry := range entries {
		if entry.Archived || (taskID != "" && entry.TaskID != taskID) {
			continue
		}
		if cursor != "" && (entry.CreatedAt.After(after.CreatedAt) || (entry.CreatedAt.Equal(after.CreatedAt) && entry.ID <= after.ID)) {
			continue
		}
		if len(page.Jobs) >= limit {
			page.HasMore = true
			break
		}
		r, readErr := s.Status(entry.ID)
		if errors.Is(readErr, os.ErrNotExist) {
			page.Partial = true
			continue
		}
		if readErr != nil {
			r = Record{SchemaVersion: SchemaVersion, ID: entry.ID, Status: "state_unavailable", StateUnavailable: true, ObservationOnly: true}
			page.Partial = true
		}
		if r.Archived {
			continue
		}
		page.Jobs = append(page.Jobs, r)
		after = pageCursor{Version: 1, Root: rootHash(s.Root), Task: taskID, CreatedAt: entry.CreatedAt, ID: entry.ID}
	}
	if page.HasMore {
		data, _ := json.Marshal(after)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (s *Store) tombstonePath(id string, create bool) (string, error) {
	if !idPattern.MatchString(id) {
		return "", errors.New("invalid job_id")
	}
	base := filepath.Join(s.Root, "tombstones")
	shard := filepath.Join(base, id[4:6])
	for _, dir := range []string{base, shard} {
		if create {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return "", err
			}
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid job tombstone directory")
		}
	}
	return filepath.Join(shard, id), nil
}
