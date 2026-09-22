package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/tool/core"
)

const monitorLease = 3 * time.Second
const monitorIdle = 60 * time.Second

type ControlState struct {
	Enabled         bool        `json:"enabled"`
	Required        bool        `json:"monitor_required"`
	Connected       bool        `json:"monitor_connected"`
	ID              string      `json:"session_id"`
	Epoch           uint64      `json:"epoch"`
	Phase           string      `json:"phase"`
	Activity        string      `json:"activity"`
	Mode            string      `json:"mode"`
	Application     Application `json:"application"`
	Window          Window      `json:"window"`
	Pointer         *Point      `json:"pointer,omitempty"`
	PointerSequence uint64      `json:"pointer_sequence"`
	Active          int         `json:"active_operations"`
	Reason          string      `json:"reason"`
	CanResume       bool        `json:"can_resume"`
}

type ControlRequest struct {
	ControllerID     string `json:"controller_id"`
	SessionID        string `json:"session_id,omitempty"`
	VisibleSessionID string `json:"visible_session_id,omitempty"`
	Operation        string `json:"operation,omitempty"`
}

type controlEpochKey struct{}
type controlActivityKey struct{}
type controlSession struct {
	mu        sync.Mutex
	view      ControlState
	context   context.Context
	cancel    context.CancelFunc
	owner     string
	heartbeat time.Time
	visible   string
	updated   time.Time
	now       func() time.Time
}

func newControlSession(enabled bool) *controlSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &controlSession{view: ControlState{Enabled: enabled, Phase: "idle", Epoch: 1, Activity: "waiting"}, context: ctx, cancel: cancel, now: time.Now}
}
func (m *controlSession) snapshotLocked() ControlState {
	m.expireLocked()
	v := m.view
	v.Connected = m.owner != "" && m.now().Sub(m.heartbeat) < monitorLease
	v.CanResume = (v.Phase == "paused" || v.Phase == "stopped") && v.Active == 0 && v.Connected
	if v.Active > 0 {
		if v.Phase == "paused" {
			v.Phase = "pausing"
		}
		if v.Phase == "stopped" || v.Phase == "closed" {
			v.Phase = "stopping"
		}
	}
	if v.Pointer != nil {
		p := *v.Pointer
		v.Pointer = &p
	}
	return v
}
func (m *controlSession) status() ControlState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}
func (m *controlSession) blockLocked(phase, reason string) {
	m.view.Phase = phase
	m.view.Reason = reason
	m.view.Epoch++
	m.view.Pointer = nil
	m.visible = ""
	m.cancel()
}
func (m *controlSession) expireLocked() {
	if m.view.Phase != "running" {
		return
	}
	if m.view.Required && m.now().Sub(m.heartbeat) >= monitorLease {
		m.blockLocked("paused", "monitor_disconnected")
		return
	}
	if m.view.Active == 0 && !m.updated.IsZero() && m.now().Sub(m.updated) >= monitorIdle {
		m.blockLocked("completed", "idle_timeout")
	}
}
func (m *controlSession) require(ctx context.Context) {
	m.mu.Lock()
	if m.view.Required {
		m.mu.Unlock()
		return
	}
	m.view.Required = true
	m.mu.Unlock()
	go func() {
		timer := time.NewTicker(200 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				m.mu.Lock()
				m.expireLocked()
				m.mu.Unlock()
			}
		}
	}()
}
func (m *controlSession) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockLocked("closed", "runtime_closed")
}
func controlError(code, message string) error {
	return core.NewErrorDetails(code, message, "desktop", map[string]any{"local_user_action_required": true, "retry_instruction": "Use the local Computer Use panel. Never retry input or resume control automatically."})
}

// acquire 将排队和执行中的操作都绑定到同一取消代次；停止不需要等待 desktopGate。
func (m *controlSession) acquire(ctx context.Context, activity string) (context.Context, func(), error) {
	m.mu.Lock()
	m.expireLocked()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, nil, err
	}
	if m.view.Phase == "paused" || m.view.Phase == "stopped" || m.view.Phase == "closed" {
		m.mu.Unlock()
		return nil, nil, controlError("DESKTOP_CONTROL_BLOCKED", "Desktop control was paused or stopped locally; only the local user can resume")
	}
	if m.view.Required && (m.owner == "" || m.now().Sub(m.heartbeat) >= monitorLease) {
		m.mu.Unlock()
		return nil, nil, controlError("DESKTOP_MONITOR_UNAVAILABLE", "Open the AgentDock menu-bar application before using Computer Use")
	}
	if m.view.Phase != "running" {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			m.mu.Unlock()
			return nil, nil, err
		}
		m.cancel()
		m.context, m.cancel = context.WithCancel(context.Background())
		m.view.ID = hex.EncodeToString(id)
		m.view.Epoch++
		m.view.Phase = "running"
		m.view.Reason = ""
		m.view.Window = Window{}
		m.view.Application = Application{}
		m.view.Pointer = nil
		m.visible = ""
	}
	epoch := m.view.Epoch
	id := m.view.ID
	required := m.view.Required
	operation, cancel := context.WithCancel(context.WithValue(context.WithValue(ctx, controlActivityKey{}, activity), controlEpochKey{}, epoch))
	stop := context.AfterFunc(m.context, cancel)
	m.view.Active++
	if m.view.Active == 1 {
		m.view.Activity = activity
	}
	m.updated = m.now()
	m.mu.Unlock()
	var once sync.Once
	finish := func() {
		once.Do(func() {
			interrupted := operation.Err() != nil
			stop()
			cancel()
			m.mu.Lock()
			defer m.mu.Unlock()
			m.view.Active--
			m.updated = m.now()
			if interrupted && m.view.Phase == "running" {
				m.blockLocked("paused", "operation_cancelled")
			}
			if m.view.Active == 0 {
				m.view.Activity = "waiting"
			}
		})
	}
	if required {
		// 在首次本地窗口/菜单指示器确实显示之前，不开始捕获、启动或输入。
		deadline := time.NewTimer(monitorLease)
		defer deadline.Stop()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			m.mu.Lock()
			ready := m.view.Epoch == epoch && m.visible == id && m.now().Sub(m.heartbeat) < monitorLease
			m.mu.Unlock()
			if ready {
				break
			}
			select {
			case <-operation.Done():
				finish()
				return nil, nil, controlError("DESKTOP_CONTROL_BLOCKED", "Desktop control cancelled before the local monitor acknowledged it")
			case <-deadline.C:
				m.mu.Lock()
				if m.view.Epoch == epoch {
					m.blockLocked("paused", "monitor_not_visible")
				}
				m.mu.Unlock()
				finish()
				return nil, nil, controlError("DESKTOP_MONITOR_UNAVAILABLE", "The local control indicator did not become visible")
			case <-tick.C:
			}
		}
	}
	if err := operation.Err(); err != nil {
		finish()
		return nil, nil, err
	}
	return operation, finish, nil
}

// 获取输入串行锁后才显示真正执行的动作，排队请求不能覆盖进行中的动作标签。
func (m *controlSession) started(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view.Phase == "running" && m.view.Epoch == m.epoch(ctx) {
		if activity, ok := ctx.Value(controlActivityKey{}).(string); ok {
			m.view.Activity = activity
		}
	}
}
func (m *controlSession) epoch(ctx context.Context) uint64 {
	v, _ := ctx.Value(controlEpochKey{}).(uint64)
	return v
}
func (m *controlSession) valid(ctx context.Context, epoch uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	return ctx.Err() == nil && m.view.Phase == "running" && m.view.Epoch == epoch
}
func (m *controlSession) target(ctx context.Context, w Window, mode string, app Application) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view.Epoch != m.epoch(ctx) || m.view.Phase != "running" {
		return
	}
	if m.view.Window.ID != w.ID || m.view.Window.PID != w.PID {
		m.view.Pointer = nil
	}
	m.view.Window = w
	m.view.Mode = mode
	m.view.Application = app
}
func (m *controlSession) pointer(ctx context.Context, p Point) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.view.Epoch == m.epoch(ctx) && m.view.Phase == "running" {
		m.view.Pointer = &p
		m.view.PointerSequence++
	}
}

var controllerIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{16,80}$`)

// local 仅经本机受权限保护的 socket 调用，绝不注册为 MCP 工具。
func (m *controlSession) local(r ControlRequest, poll bool) (ControlState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	if !controllerIDPattern.MatchString(r.ControllerID) {
		return ControlState{}, invalid("invalid local controller ID")
	}
	if m.view.Phase == "closed" {
		return m.snapshotLocked(), nil
	}
	if m.owner != "" && m.owner != r.ControllerID && m.now().Sub(m.heartbeat) < monitorLease {
		return ControlState{}, controlError("DESKTOP_MONITOR_BUSY", "Another local Computer Use panel owns the control lease")
	}
	if poll {
		if r.Operation != "" {
			return ControlState{}, invalid("poll cannot mutate the control state")
		}
		m.owner = r.ControllerID
		m.heartbeat = m.now()
		m.visible = r.VisibleSessionID
		return m.snapshotLocked(), nil
	}
	if m.owner != r.ControllerID || m.now().Sub(m.heartbeat) >= monitorLease {
		return ControlState{}, controlError("DESKTOP_MONITOR_UNAVAILABLE", "Local controller lease expired")
	}
	if r.SessionID != m.view.ID {
		return ControlState{}, stale("Local control command belongs to another session")
	}
	switch r.Operation {
	case "pause":
		if m.view.Phase == "running" {
			m.blockLocked("paused", "user_paused")
		}
	case "stop":
		m.blockLocked("stopped", "user_stopped")
	case "resume":
		if m.view.Active != 0 {
			return ControlState{}, controlError("DESKTOP_CONTROL_DRAINING", "Wait for pending input cleanup before resuming")
		}
		if m.view.Phase != "paused" && m.view.Phase != "stopped" {
			return ControlState{}, invalid("only a paused or stopped session can resume")
		}
		m.cancel()
		m.context, m.cancel = context.WithCancel(context.Background())
		m.view.Epoch++
		m.view.Phase = "idle"
		m.view.Reason = ""
		m.view.Pointer = nil
		m.visible = ""
	default:
		return ControlState{}, invalid("local operation must be pause, resume or stop")
	}
	return m.snapshotLocked(), nil
}

func (s *Service) RequireMonitor() { s.control.require(s.lifetime) }
func (s *Service) LocalControl(r ControlRequest, poll bool) (ControlState, error) {
	return s.control.local(r, poll)
}
func (s *Service) controlled(ctx context.Context, activity string) (context.Context, func(), error) {
	return s.control.acquire(ctx, activity)
}
func (s *Service) recordTarget(ctx context.Context, state State, w Window, mode string) {
	app := Application{PID: w.PID}
	for _, a := range state.Applications {
		if a.PID == w.PID {
			app = a
			break
		}
	}
	s.control.target(ctx, w, mode, app)
}
