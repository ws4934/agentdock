package desktop

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// task_id 是随机能力令牌，仅 begin 返回给发起方，不通过全局status/本地预览公开。
// Stateless MCP 无可靠每轮会话ID；不能把模型提交的客户端名字当作认证身份。
type TaskRequest struct {
	Action string `json:"action"`
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title,omitempty"`
}
type ApplicationApproval struct {
	ID          string      `json:"id"`
	Application Application `json:"application"`
	Mode        string      `json:"mode"`
}
type taskContextKey struct{}

func taskContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, taskContextKey{}, id)
}
func taskToken(ctx context.Context) string { v, _ := ctx.Value(taskContextKey{}).(string); return v }
func opaqueID() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) RequireTaskScope() {
	s.control.mu.Lock()
	s.control.view.TaskRequired = true
	s.control.mu.Unlock()
}
func (m *controlSession) ownerValidLocked(ctx context.Context) error {
	id := taskToken(ctx)
	if m.taskKey == "" {
		if m.view.TaskRequired || id != "" {
			return controlError("DESKTOP_TASK_REQUIRED", "Begin an exclusive desktop_task before operating; preserve its private task_id")
		}
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(id), []byte(m.taskKey)) != 1 {
		return controlError("DESKTOP_TASK_OWNED", "Another desktop task owns this Core; do not copy another task's capability")
	}
	return nil
}
func (s *Service) Task(ctx context.Context, r TaskRequest) (core.Result, error) {
	ctx, finish, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer finish()
	if e = s.ready(); e != nil {
		return nil, e
	}
	m := s.control
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	switch r.Action {
	case "begin":
		if r.TaskID != "" || strings.TrimSpace(r.Title) == "" || len(r.Title) > 200 || !utf8.ValidString(r.Title) || strings.IndexFunc(r.Title, unicode.IsControl) >= 0 {
			return nil, invalid("begin requires a title of 1..200 UTF-8 bytes and no task_id")
		}
		if m.taskKey != "" || m.view.Active != 0 {
			return nil, controlError("DESKTOP_TASK_BUSY", "A desktop task is already reserved; wait for its owner or the local user to end it")
		}
		if m.view.CleanupFailed || m.view.Phase == "paused" || m.view.Phase == "stopped" || m.view.Phase == "closed" {
			return nil, controlError("DESKTOP_CONTROL_BLOCKED", "The local stop/pause latch must be cleared by the local user")
		}
		if m.view.Required && (m.owner == "" || m.now().Sub(m.heartbeat) >= monitorLease) {
			return nil, controlError("DESKTOP_MONITOR_UNAVAILABLE", "Open the local Computer Use monitor first")
		}
		token, e := opaqueID()
		if e != nil {
			return nil, e
		}
		display, e := opaqueID()
		if e != nil {
			return nil, e
		}
		m.blockLocked("completed", "task_reserved")
		m.view.StepMode = false
		m.stepBudget = 0
		m.taskKey = token
		m.view.TaskLabel = r.Title
		m.view.TaskReference = display[:16]
		m.grants = map[string]bool{}
		m.denials = map[string]bool{}
		m.view.PendingApplication = nil
		return core.Result{"action": "begin", "task_id": token, "task_reference": m.view.TaskReference, "title": r.Title, "next_required_action": "desktop_launch or desktop_snapshot", "application_approval": "local_only", "exclusive": true}, nil
	case "status", "end":
		if r.Title != "" || r.TaskID == "" {
			return nil, invalid("status/end requires task_id and no title")
		}
		if m.taskKey == "" || m.ownerValidLocked(taskContext(ctx, r.TaskID)) != nil {
			return nil, controlError("DESKTOP_TASK_OWNED", "Task capability is invalid or no longer active")
		}
		if r.Action == "end" {
			if m.view.Active != 0 {
				return nil, controlError("DESKTOP_TASK_BUSY", "Cancel or finish outstanding operations before ending the task")
			}
			m.clearTaskLocked()
			// 结束任务不能解除本机用户停止/暂停或清理失败状态。
			if m.view.Phase == "running" || m.view.Phase == "idle" || m.view.Phase == "completed" {
				m.blockLocked("completed", "task_ended")
			}
		}
		return core.Result{"action": r.Action, "control_session": m.snapshotLocked(), "ended": r.Action == "end"}, nil
	default:
		return nil, invalid("desktop_task action must be begin, status or end")
	}
}
func (m *controlSession) clearTaskLocked() {
	m.taskKey = ""
	m.grants = map[string]bool{}
	m.denials = map[string]bool{}
	m.view.PendingApplication = nil
	m.view.TaskLabel = ""
	m.view.TaskReference = ""
}
func applicationGrantKey(a Application, mode string) string {
	if a.Path != "" && a.BundleID != "" {
		return a.BundleID + "\x00" + a.Path + "\x00" + mode
	}
	return fmt.Sprintf("pid:%d\x00%s", a.PID, mode)
}
func protectedApplication(a Application) bool {
	id := strings.ToLower(a.BundleID)
	return a.PID == os.Getpid() || id == "com.uvwt.agentdock" || strings.HasPrefix(id, "com.uvwt.agentdock.") || id == "com.apple.systempreferences" || id == "com.apple.loginwindow" || id == "com.apple.securityagent"
}

// 一次授权仅覆盖当前任务中的精确应用身份与模式。拒绝/停止后不重新弹窗逼迫用户。
func (m *controlSession) authorize(ctx context.Context, a Application, mode string) error {
	m.mu.Lock()
	if !m.view.TaskRequired && m.taskKey == "" {
		m.mu.Unlock()
		return nil
	}
	if e := m.ownerValidLocked(ctx); e != nil {
		m.mu.Unlock()
		return e
	}
	if protectedApplication(a) || a.PID > 0 && a.PID == m.controllerPID {
		m.mu.Unlock()
		return controlError("DESKTOP_PROTECTED_APPLICATION", "AgentDock's own control surface and sensitive system applications require manual operation")
	}
	if a.PID <= 0 && (a.Path == "" || a.BundleID == "") {
		m.mu.Unlock()
		return controlError("DESKTOP_APPLICATION_UNIDENTIFIED", "Cannot authorize an application without a stable bundle path or live PID")
	}
	key := applicationGrantKey(a, mode)
	if m.grants[key] {
		m.mu.Unlock()
		return nil
	}
	if m.denials[key] {
		m.mu.Unlock()
		return controlError("DESKTOP_APPLICATION_DENIED", "The local user denied this application for the current task")
	}
	id, e := opaqueID()
	if e != nil {
		m.mu.Unlock()
		return e
	}
	request := &ApplicationApproval{ID: id[:32], Application: a, Mode: mode}
	m.view.PendingApplication = request
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.view.PendingApplication == request {
			m.view.PendingApplication = nil
		}
		m.mu.Unlock()
	}()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		m.expireLocked()
		allowed, denied := m.grants[key], m.denials[key]
		valid := m.view.Phase == "running" && m.view.Epoch == m.epoch(ctx) && m.ownerValidLocked(ctx) == nil
		m.mu.Unlock()
		if ctx.Err() != nil || !valid {
			return controlError("DESKTOP_CONTROL_BLOCKED", "Application approval was cancelled locally")
		}
		if allowed {
			return nil
		}
		if denied {
			return controlError("DESKTOP_APPLICATION_DENIED", "The local user denied this application")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return controlError("DESKTOP_APPROVAL_REQUIRED", "Local application approval timed out; no input or capture was performed")
		case <-tick.C:
		}
	}
}
func (s *Service) authorizeWindow(ctx context.Context, state State, w Window, mode string) error {
	app := Application{PID: w.PID}
	found := false
	for _, a := range state.Applications {
		if a.PID == w.PID {
			app = a
			found = true
			break
		}
	}
	if !found && s.control.scopeRequired() {
		return controlError("DESKTOP_APPLICATION_UNIDENTIFIED", "The target application is absent from current process metadata; observe again instead of granting an unknown PID")
	}
	return s.control.authorize(ctx, app, mode)
}

func (m *controlSession) scopeRequired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.view.TaskRequired || m.taskKey != ""
}
