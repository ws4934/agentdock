//go:build linux

package jobrun

import (
	"os"
	"strings"
)

func platformBootID() string {
	value, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
