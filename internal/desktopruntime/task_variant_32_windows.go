//go:build windows && 386

package desktopruntime

// On Win32 both the scalar value and BRECORD's two pointers occupy 8 bytes.
// No trailing zero-length padding field: that would change Go struct size.
type variant struct {
	VT         uint16
	wReserved1 uint16
	wReserved2 uint16
	wReserved3 uint16
	Val        int64
}
