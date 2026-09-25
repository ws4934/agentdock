package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/uvwt/agentdock/internal/jobrun"
)

// 本机任务中心只观察关联回执；不读日志、不重新抓取源码、不恢复或重放执行。
type runtimeTaskReceipt struct {
	ID               string     `json:"job_id"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	Kind             string     `json:"kind"`
	OwnerAlive       bool       `json:"owner_alive"`
	ExitCode         *int       `json:"exit_code,omitempty"`
	ValidationStatus string     `json:"validation_status,omitempty"`
	SourceRevision   string     `json:"source_revision,omitempty"`
	SourceHead       string     `json:"source_head,omitempty"`
	SourceObservedAt *time.Time `json:"source_observed_at,omitempty"`
}

func taskReceipt(record jobrun.Record) runtimeTaskReceipt {
	title := []rune(record.Title)
	if len(title) > 160 {
		title = title[:160]
	}
	receipt := runtimeTaskReceipt{ID: record.ID, Title: string(title), Status: record.Status, Kind: record.Kind, OwnerAlive: record.OwnerAlive, ExitCode: record.ExitCode}
	if evidence := record.Evidence; evidence != nil {
		receipt.ValidationStatus = evidence.Status
		receipt.SourceRevision = evidence.SourceAfter.Revision
		receipt.SourceHead = evidence.SourceAfter.Head
		receipt.SourceObservedAt = &evidence.SourceAfter.CapturedAt
	}
	return receipt
}

func (r *Runtime) addTaskReceipts(result Result, id string) {
	result["job_receipts"] = []runtimeTaskReceipt{}
	result["jobs_available"] = false
	result["jobs_partial"] = false
	result["observation_only"] = true
	// 未使用过 managed jobs 的安装不因一次只读访问而创建执行存储。
	if _, err := os.Stat(filepath.Join(r.cfg.AgentDockHome, "jobs")); os.IsNotExist(err) {
		result["jobs_available"] = true
		return
	}
	jobs, err := r.command.Jobs()
	if err != nil {
		return
	}
	page, err := jobs.ListPage(id, 10, "")
	if err != nil {
		return
	}
	receipts := make([]runtimeTaskReceipt, 0, len(page.Jobs))
	partial := page.HasMore || page.Partial
	for _, record := range page.Jobs {
		if record.TaskID != id {
			partial = true
			continue
		}
		receipts = append(receipts, taskReceipt(record))
	}
	result["job_receipts"] = receipts
	result["jobs_available"] = true
	result["jobs_partial"] = partial
}
