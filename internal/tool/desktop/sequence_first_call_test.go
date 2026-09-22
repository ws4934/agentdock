package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 所有窗口、输入和控制器均为内存模拟；不连接本机socket，不替用户批准真实应用。
func firstSequenceRun(t *testing.T, trustPath, decision string, execute bool, expectedApprovals int32) {
	t.Helper()
	b := sequenceFixture()
	b.state.Applications[1] = Application{PID: 20, Name: "Fixture", BundleID: "test.sequence.first", Path: filepath.Join(filepath.Dir(trustPath), "Fixture.app")}
	s := New(true, b)
	defer s.Close()
	if err := s.ConfigureApplicationTrust(trustPath); err != nil {
		t.Fatal(err)
	}
	s.RequireTaskScope()
	localPoll(t, s, "")
	s.RequireMonitor()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	monitor, stopMonitor := context.WithCancel(ctx)
	done := make(chan error, 1)
	var approvals atomic.Int32
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitor.Done():
				done <- nil
				return
			case <-ticker.C:
				view := s.control.status()
				view, err := s.LocalControl(ControlRequest{ControllerID: testController, VisibleSessionID: view.ID}, true)
				if err == nil && view.PendingApplication != nil {
					approvals.Add(1)
					_, err = s.LocalControl(ControlRequest{ControllerID: testController, SessionID: view.ID, Operation: decision, ApprovalID: view.PendingApplication.ID}, false)
				}
				if err != nil {
					done <- err
					return
				}
			}
		}
	}()
	defer func() {
		stopMonitor()
		if err := <-done; err != nil {
			t.Error("mock monitor failed", err)
		}
	}()
	token := beginDesktopTask(t, s)
	no := false
	snapshot, err := s.Snapshot(ctx, SnapshotRequest{TaskID: token, WindowID: 9, Accessibility: true, Screenshot: &no})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.inputs) != 0 || len(b.events) != 0 {
		t.Fatal("observation or application approval emitted input")
	}
	if execute {
		// 没有调用 Act，没有预热/空操作。第一个输入请求就是五步完整计划。
		result, err := s.Sequence(ctx, SequenceRequest{TaskID: token, SnapshotID: snapshot["snapshot_id"].(string), Steps: seqSteps()})
		if err != nil || result["outcome"] != "completed" || result["dispatched_steps"] != 5 || result["completed_steps"] != 5 || result["foreground_fallback"] != false {
			t.Fatalf("first batch failed: %v, %#v", err, result)
		}
		if len(b.inputs) != 5 || len(b.events) != 0 {
			t.Fatal("hidden warmup or fallback input", len(b.inputs), len(b.events))
		}
		for i, label := range []string{"1", "2", "+", "3", "="} {
			if b.inputs[i].Element == nil || b.inputs[i].Element.Title != label {
				t.Fatal("unexpected first-batch input", i)
			}
		}
	}
	if approvals.Load() != expectedApprovals {
		t.Fatal("unexpected application approvals", approvals.Load(), expectedApprovals)
	}
	if _, err := s.Task(ctx, TaskRequest{Action: "end", TaskID: token}); err != nil {
		t.Fatal(err)
	}
}

func TestSequenceFirstInputWithMonitorAndTaskApproval(t *testing.T) {
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			firstSequenceRun(t, filepath.Join(t.TempDir(), "trust.json"), "approve_application", true, 1)
		})
	}
}

func TestSequenceFirstInputAfterRestartWithRememberedApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	// 第一个实例只保存模拟用户许可，完全不发输入；新实例直接执行完整首批次。
	firstSequenceRun(t, path, "approve_application_always", false, 1)
	firstSequenceRun(t, path, "deny_application", true, 0)
}
