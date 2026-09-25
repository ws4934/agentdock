//go:build windows

package process

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"unsafe"
)

var jobKernel = windows.NewLazySystemDLL("kernel32.dll")
var openNamedJob = jobKernel.NewProc("OpenJobObjectW")
var createNamedJob = jobKernel.NewProc("CreateJobObjectW")
var resumeManagedProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func ConfigureManaged(cmd *exec.Cmd) {
	Configure(cmd)
	// 先加入命名 Job，再恢复本次新子进程，消除启动与 Assign 之间的后代逃逸窗口。
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

func AttachNamed(cmd *exec.Cmd, name string) (*Controller, error) {
	if err := resumeManagedProcess.Find(); err != nil {
		return nil, err
	}
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("managed process unavailable")
	}
	controller, err := attachNamedPID(cmd.Process.Pid, name)
	if err != nil {
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = controller.Terminate()
		_ = controller.Close()
		return nil, err
	}
	status, _, _ := resumeManagedProcess.Call(uintptr(process))
	_ = windows.CloseHandle(process)
	if int32(status) < 0 {
		_ = controller.Terminate()
		_ = controller.Close()
		return nil, fmt.Errorf("resume managed process: NTSTATUS %#x", status)
	}
	return controller, nil
}

func ownedJob(name string) (windows.Handle, error) {
	if name != "" {
		if err := createNamedJob.Find(); err != nil {
			return 0, err
		}
	}
	if name == "" {
		return windows.CreateJobObject(nil, nil)
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, last := createNamedJob.Call(0, uintptr(unsafe.Pointer(ptr)))
	if h == 0 {
		return 0, last
	}
	if errors.Is(last, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(windows.Handle(h))
		return 0, errors.New("managed Job Object already exists")
	}
	return windows.Handle(h), nil
}

func ExecutionGone(_ int, name string) bool {
	if err := openNamedJob.Find(); err != nil {
		return false
	}
	if name == "" {
		return false
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	h, _, last := openNamedJob.Call(0x0004, 0, uintptr(unsafe.Pointer(ptr))) // JOB_OBJECT_QUERY
	if h == 0 {
		return errors.Is(last, windows.ERROR_FILE_NOT_FOUND)
	}
	defer windows.CloseHandle(windows.Handle(h))
	var accounting struct {
		Times                                 [4]int64
		PageFaults, Total, Active, Terminated uint32
	}
	err = windows.QueryInformationJobObject(windows.Handle(h), windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
	return err == nil && accounting.Active == 0
}
