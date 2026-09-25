// Package diagnostics records a bounded allowlisted lifecycle projection. It
// never accepts tool arguments, raw results, command lines, environment or logs.
package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Event struct {
	ID               string     `json:"id"`
	Tool             string     `json:"tool"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMS       int64      `json:"duration_ms,omitempty"`
	Status           string     `json:"status"`
	RPCSuccess       *bool      `json:"rpc_success,omitempty"`
	Code             string     `json:"code,omitempty"`
	ResponseDelivery string     `json:"response_delivery"`
}
type Recorder struct {
	mu      sync.Mutex
	events  []Event
	allowed map[string]bool
}

func New(tools []string) *Recorder {
	allowed := map[string]bool{}
	for _, t := range tools {
		allowed[t] = true
	}
	return &Recorder{allowed: allowed}
}
func (r *Recorder) Start(tool string) func(bool, string) {
	if !r.allowed[tool] {
		tool = "unknown_tool"
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	now := time.Now().UTC()
	r.mu.Lock()
	r.events = append(r.events, Event{ID: id, Tool: tool, StartedAt: now, Status: "running", ResponseDelivery: "not_observed"})
	if len(r.events) > 128 {
		copy(r.events, r.events[len(r.events)-128:])
		r.events = r.events[:128]
	}
	r.mu.Unlock()
	return func(ok bool, code string) {
		if len(code) > 64 {
			code = ""
		}
		for _, c := range code {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				code = ""
				break
			}
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for i := range r.events {
			e := &r.events[i]
			if e.ID == id && e.FinishedAt == nil {
				done := time.Now().UTC()
				e.FinishedAt = &done
				e.DurationMS = done.Sub(e.StartedAt).Milliseconds()
				e.Status = "completed"
				e.RPCSuccess = &ok
				e.Code = code
				return
			}
		}
	}
}
func (r *Recorder) Snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}
