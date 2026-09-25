//go:build windows

package jobrun

import (
	"os/exec"
	"syscall"
)

// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP. The supervisor does not belong to
// the Core's process-tree controller; its own child is owned by a Windows Job.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8 | 0x200, HideWindow: true}
}
