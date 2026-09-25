//go:build darwin || linux

package process

import (
	"errors"
	"os/exec"
	"syscall"
)

func ConfigureManaged(cmd *exec.Cmd)                           { Configure(cmd) }
func AttachNamed(cmd *exec.Cmd, _ string) (*Controller, error) { return Attach(cmd) }

// 只观测，不按可能复用的 PID 发送终止信号；存活或权限不明都不能证明结束。
func ExecutionGone(pid int, _ string) bool {
	return pid > 0 && errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}
