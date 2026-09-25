//go:build darwin

package jobrun

import (
	"golang.org/x/sys/unix"
	"strings"
)

func platformBootID() string {
	value, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
