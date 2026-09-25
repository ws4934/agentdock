//go:build windows

package jobrun

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

func platformBootID() string {
	// BootIdentifier 位于结构前16字节，不使用可能被校时改变的启动时间。
	var data [64]byte
	var length uint32
	if err := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&data[0]), uint32(len(data)), &length); err != nil || length < 16 {
		return ""
	}
	var zero [16]byte
	if *(*[16]byte)(unsafe.Pointer(&data[0])) == zero {
		return ""
	}
	return fmt.Sprintf("%x", data[:16])
}
