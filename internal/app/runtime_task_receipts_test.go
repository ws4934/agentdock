package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/jobrun"
	"github.com/uvwt/agentdock/internal/sourceproof"
)

func TestTaskReceiptProjectionIsBoundedAndHistorical(t *testing.T) {
	exit := 0
	now := time.Now().UTC()
	record := jobrun.Record{ID: "job-synthetic", Title: strings.Repeat("示例", 200), Status: "exited", Kind: "validation", ExitCode: &exit, Failure: "private-diagnostic-not-for-panel", Evidence: &jobrun.Evidence{Status: "passed", SourceAfter: sourceproof.Snapshot{Revision: "src1:synthetic", Head: "synthetic-head", CapturedAt: now}}}
	receipt := taskReceipt(record)
	if len([]rune(receipt.Title)) != 160 || receipt.SourceRevision != "src1:synthetic" || receipt.SourceObservedAt == nil || !receipt.SourceObservedAt.Equal(now) {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), record.Failure) || strings.Contains(string(data), "source_freshness") {
		t.Fatalf("raw diagnostics or fabricated freshness exposed: %s", data)
	}
}

func TestTaskReceiptReadDoesNotCreateJobStore(t *testing.T) {
	root := t.TempDir()
	rt := &Runtime{cfg: config.Config{AgentDockHome: root}}
	result := Result{}
	rt.addTaskReceipts(result, "tsk_0123456789abcdef")
	if result["jobs_available"] != true || result["jobs_partial"] != false || len(result["job_receipts"].([]runtimeTaskReceipt)) != 0 {
		t.Fatalf("unexpected empty receipts: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs")); !os.IsNotExist(err) {
		t.Fatalf("read created state: %v", err)
	}
}
