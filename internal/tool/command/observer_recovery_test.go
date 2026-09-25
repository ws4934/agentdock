package command

import (
	"runtime"
	"testing"
	"time"
)

func TestTerminalStatusCanBeRepeatedAfterLostResponse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	svc, _ := newCommandTestService(t)
	r, err := svc.execArgs(t.Context(), map[string]any{"cmd": "printf SYNTHETIC_OUTPUT", "execution_mode": "async", "timeout_ms": 2000})
	if err != nil {
		t.Fatal(err)
	}
	id := r["session_id"].(string)
	s, ok := svc.sessions.Get(id)
	if !ok {
		t.Fatal("receipt missing")
	}
	select {
	case <-s.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture not done")
	}
	args := map[string]any{"action": "status", "session_id": id, "stdout_offset": 0, "stderr_offset": 0}
	first, err := svc.observeArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.observeArgs(args)
	if err != nil || again["stdout"] != first["stdout"] || first["stdout"] != "SYNTHETIC_OUTPUT" {
		t.Fatalf("%+v %+v %v", first, again, err)
	}
}
func TestSynchronousReceiptRemainsReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	svc, _ := newCommandTestService(t)
	r, err := svc.execArgs(t.Context(), map[string]any{"cmd": "printf SYNCHRONOUS", "execution_mode": "sync", "timeout_ms": 2000})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := svc.observeArgs(map[string]any{"action": "status", "session_id": r["session_id"]})
	if err != nil || repeated["stdout"] != "SYNCHRONOUS" {
		t.Fatalf("%+v %v", repeated, err)
	}
}
