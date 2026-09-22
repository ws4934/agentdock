package desktop

import (
	"context"
	"time"
)

// 有界内存时间线不保存文本、快捷键、AX标签、图片或任务能力令牌。
type ControlEvent struct {
	Sequence  uint64 `json:"sequence"`
	Activity  string `json:"activity"`
	PID       int    `json:"pid"`
	WindowID  uint32 `json:"window_id"`
	Outcome   string `json:"outcome"`
	ElapsedMS int64  `json:"elapsed_ms"`
}
type operationTrace struct {
	event   ControlEvent
	started time.Time
}
type operationTraceKey struct{}

func traceFrom(ctx context.Context) *operationTrace {
	t, _ := ctx.Value(operationTraceKey{}).(*operationTrace)
	return t
}
func (m *controlSession) traceBeginLocked(ctx context.Context, activity string) context.Context {
	m.eventSequence++
	return context.WithValue(ctx, operationTraceKey{}, &operationTrace{event: ControlEvent{Sequence: m.eventSequence, Activity: activity, Outcome: "finished_unverified"}, started: m.now()})
}
func (m *controlSession) traceFinishLocked(ctx context.Context, interrupted bool) {
	t := traceFrom(ctx)
	if t == nil {
		return
	}
	event := t.event
	event.ElapsedMS = max(0, m.now().Sub(t.started).Milliseconds())
	if interrupted && event.Outcome == "finished_unverified" {
		event.Outcome = "cancelled"
	}
	if len(m.view.Events) >= 32 {
		copy(m.view.Events, m.view.Events[1:])
		m.view.Events = m.view.Events[:31]
	}
	m.view.Events = append(m.view.Events, event)
}
func (m *controlSession) traceOutcome(ctx context.Context, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := traceFrom(ctx); t != nil {
		t.event.Outcome = outcome
	}
}
