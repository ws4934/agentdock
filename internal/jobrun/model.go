// Package jobrun owns explicitly detached, single-execution jobs. Core requests
// only submit and observe; an independently owned supervisor executes once.
package jobrun

import (
	"github.com/uvwt/agentdock/internal/sourceproof"
	"time"
)

const SchemaVersion = 1
const MaxLogBytes int64 = 8 << 20
const MaxRunning = 8

type ValidationSpec struct {
	Adapter     string   `json:"adapter"`
	SourcePaths []string `json:"source_paths,omitempty"`
}
type Spec struct {
	Command    string          `json:"command,omitempty"`
	Argv       []string        `json:"argv,omitempty"`
	Workdir    string          `json:"workdir"`
	TaskID     string          `json:"task_id,omitempty"`
	TimeoutMS  int64           `json:"timeout_ms"`
	Title      string          `json:"title,omitempty"`
	Validation *ValidationSpec `json:"validation,omitempty"`
}
type LogState struct {
	Total    int64 `json:"total_bytes"`
	Retained int64 `json:"retained_bytes"`
	Dropped  int64 `json:"dropped_bytes"`
}
type Evidence struct {
	Adapter        string               `json:"adapter"`
	Status         string               `json:"status"`
	Reason         string               `json:"reason,omitempty"`
	Tests          int                  `json:"tests"`
	Passed         int                  `json:"passed"`
	Failed         int                  `json:"failed"`
	Skipped        int                  `json:"skipped"`
	ReportComplete bool                 `json:"report_complete"`
	SourceBefore   sourceproof.Snapshot `json:"source_before"`
	SourceAfter    sourceproof.Snapshot `json:"source_after"`
}
type Record struct {
	BootID                string             `json:"boot_id,omitempty"`
	Execution             *ExecutionIdentity `json:"execution,omitempty"`
	StateUnavailable      bool               `json:"state_unavailable,omitempty"`
	ResolutionBasis       string             `json:"resolution_basis,omitempty"`
	SchemaVersion         int                `json:"schema_version"`
	ID                    string             `json:"job_id"`
	DefinitionHash        string             `json:"definition_hash"`
	Title                 string             `json:"title,omitempty"`
	Workdir               string             `json:"workdir"`
	TaskID                string             `json:"task_id,omitempty"`
	Kind                  string             `json:"kind"`
	Status                string             `json:"status"`
	CreatedAt             time.Time          `json:"created_at"`
	StartedAt             *time.Time         `json:"started_at,omitempty"`
	FinishedAt            *time.Time         `json:"finished_at,omitempty"`
	TimeoutMS             int64              `json:"timeout_ms"`
	ExitCode              *int               `json:"exit_code,omitempty"`
	Failure               string             `json:"failure,omitempty"`
	Stdout                LogState           `json:"stdout"`
	Stderr                LogState           `json:"stderr"`
	Evidence              *Evidence          `json:"evidence,omitempty"`
	Archived              bool               `json:"archived,omitempty"`
	OwnerAlive            bool               `json:"owner_alive"`
	CancellationRequested bool               `json:"cancellation_requested,omitempty"`
	ObservationOnly       bool               `json:"observation_only"`
}

func (r Record) Terminal() bool { return r.FinishedAt != nil && r.Status != "outcome_unknown" }

type LogChunk struct {
	JobID         string `json:"job_id"`
	Stream        string `json:"stream"`
	Offset        int64  `json:"offset"`
	NextOffset    int64  `json:"next_offset"`
	Data          string `json:"data"`
	Encoding      string `json:"encoding"`
	TotalBytes    int64  `json:"total_bytes"`
	RetainedBytes int64  `json:"retained_bytes"`
	DroppedBytes  int64  `json:"dropped_bytes"`
	Truncated     bool   `json:"truncated"`
	Terminal      bool   `json:"terminal"`
}
