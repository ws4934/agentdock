package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

// 持久信任仅由本机控制器修改；不将任务令牌、PID或输入内容写入磁盘。
type TrustedApplication struct {
	ID          string      `json:"id"`
	Application Application `json:"application"`
	Mode        string      `json:"mode"`
}
type applicationTrustFile struct {
	Version      int                  `json:"version"`
	Applications []TrustedApplication `json:"applications"`
}

func trustedApplication(a Application, mode string) (TrustedApplication, error) {
	if !filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || !strings.EqualFold(filepath.Ext(a.Path), ".app") || a.BundleID == "" || len(a.Path) > 4096 || len(a.BundleID) > 255 || strings.ContainsRune(a.Path+a.BundleID, 0) || protectedApplication(a) || (mode != "background" && mode != "foreground") {
		return TrustedApplication{}, invalid("persistent approval requires an identifiable application bundle and explicit mode")
	}
	a.PID = 0
	sum := sha256.Sum256([]byte(applicationGrantKey(a, mode)))
	return TrustedApplication{ID: hex.EncodeToString(sum[:]), Application: a, Mode: mode}, nil
}

// 配置只在启动时加载一次，正常操作使用内存查找，不在每次点击时访问磁盘。
func (s *Service) ConfigureApplicationTrust(path string) error {
	m := s.control
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view.Active != 0 || m.taskKey != "" {
		return invalid("configure application trust before starting desktop tasks")
	}
	if !filepath.IsAbs(path) {
		return invalid("application trust path must be absolute")
	}
	entries := map[string]TrustedApplication{}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 1<<20 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return fmt.Errorf("application trust file must be private, regular and bounded")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(info, actual) {
			return fmt.Errorf("application trust file changed while opening")
		}
		raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 {
			return fmt.Errorf("cannot read bounded application trust file")
		}
		var saved applicationTrustFile
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&saved); err != nil {
			return err
		}
		if decoder.Decode(new(any)) != io.EOF || saved.Version != 1 || len(saved.Applications) > 256 {
			return fmt.Errorf("invalid application trust file")
		}
		for _, entry := range saved.Applications {
			canonical, err := trustedApplication(entry.Application, entry.Mode)
			if err != nil || entry.ID != canonical.ID || entry.Application.PID != 0 {
				return fmt.Errorf("invalid persistent application identity")
			}
			key := applicationGrantKey(entry.Application, entry.Mode)
			if _, exists := entries[key]; exists {
				return fmt.Errorf("duplicate persistent application identity")
			}
			entries[key] = canonical
		}
	}
	m.trustPath, m.trusted = path, entries
	m.trustList = sortedTrust(entries)
	return nil
}

func sortedTrust(entries map[string]TrustedApplication) []TrustedApplication {
	list := make([]TrustedApplication, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}
func (m *controlSession) saveTrustLocked(entries map[string]TrustedApplication) error {
	if m.trustPath == "" {
		return invalid("persistent application approval is unavailable")
	}
	if len(entries) > 256 {
		return invalid("too many trusted applications")
	}
	if info, err := os.Lstat(m.trustPath); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular application trust file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := json.Marshal(applicationTrustFile{Version: 1, Applications: sortedTrust(entries)})
	if err != nil {
		return err
	}
	if err = atomicfile.Write(m.trustPath, raw, 0600); err != nil {
		return err
	}
	m.trusted = entries
	m.trustList = sortedTrust(entries)
	return nil
}
func (m *controlSession) rememberApplicationLocked(a Application, mode string) error {
	entry, err := trustedApplication(a, mode)
	if err != nil {
		return err
	}
	entries := make(map[string]TrustedApplication, len(m.trusted)+1)
	for key, value := range m.trusted {
		entries[key] = value
	}
	entries[applicationGrantKey(a, mode)] = entry
	return m.saveTrustLocked(entries)
}
func (m *controlSession) forgetApplicationLocked(id string) error {
	entries := make(map[string]TrustedApplication, len(m.trusted))
	keyToRemove := ""
	for key, value := range m.trusted {
		if value.ID == id {
			keyToRemove = key
		} else {
			entries[key] = value
		}
	}
	if keyToRemove == "" {
		return stale("Trusted application is no longer listed")
	}
	// 撤销时先取消当前控制代次，正在运行的长序列不能继续使用旧的许可。
	m.blockLocked("paused", "application_trust_revoked")
	if err := m.saveTrustLocked(entries); err != nil {
		return err
	}
	delete(m.grants, keyToRemove)
	m.denials[keyToRemove] = true
	m.notifyApprovalLocked()
	return nil
}
func (m *controlSession) applicationAllowedLocked(key string) bool {
	if m.denials[key] {
		return false
	}
	if m.grants[key] {
		return true
	}
	_, ok := m.trusted[key]
	return ok
}
func (m *controlSession) notifyApprovalLocked() {
	if m.approvalWake != nil {
		close(m.approvalWake)
	}
	m.approvalWake = make(chan struct{})
}
