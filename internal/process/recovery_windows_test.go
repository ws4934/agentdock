//go:build windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

func TestManagedNamedJobLifecycle(t *testing.T) {
	if os.Getenv("AD_NAMED_JOB_FIXTURE") == "1" {
		if err := os.WriteFile(os.Getenv("AD_NAMED_JOB_MARKER"), []byte("started"), 0600); err != nil {
			os.Exit(3)
		}
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command(binary, "-test.run=^TestManagedNamedJobLifecycle$")
	cmd.Env = append(os.Environ(), "AD_NAMED_JOB_FIXTURE=1", "AD_NAMED_JOB_MARKER="+marker)
	ConfigureManaged(cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	// CREATE_SUSPENDED 的进程不能在加入 Job 前运行测试代码。
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child ran before ownership: %v", err)
	}
	name := fmt.Sprintf("Local\\AgentDock-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	owner, err := AttachNamed(cmd, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Terminate(); _ = owner.Close() })
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if _, err = os.Stat(marker); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("managed child did not resume")
	}
	if ExecutionGone(cmd.Process.Pid, name) {
		t.Fatal("live named job considered gone")
	}
	if duplicate, err := ownedJob(name); err == nil {
		_ = duplicate
		t.Fatal("existing Job identity reused")
	}
	if err = owner.Terminate(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !ExecutionGone(cmd.Process.Pid, name) {
		t.Fatal("ended owned group not observable")
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	if !ExecutionGone(cmd.Process.Pid, name) {
		t.Fatal("released Job Object not observable")
	}
}
func TestJobAccountingNativeLayout(t *testing.T) {
	var info struct {
		Times                                 [4]int64
		PageFaults, Total, Active, Terminated uint32
	}
	if unsafe.Sizeof(info) != 48 || unsafe.Offsetof(info.Active) != 40 {
		t.Fatalf("unexpected native accounting layout: size=%d active=%d", unsafe.Sizeof(info), unsafe.Offsetof(info.Active))
	}
}
