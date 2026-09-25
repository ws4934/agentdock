//go:build windows && (amd64 || arm64)

package desktopruntime

// VARIANT has an 8-byte tag/header and a 16-byte union on Win64. The union
// includes BRECORD's two pointers even when this call only uses scalar values.
// A 16-byte Go struct corrupts VARIANTARG array strides passed to IDispatch.
// Keep Val at offset 8 and reserve the complete ABI union, including results.
type variant struct {
	VT         uint16
	wReserved1 uint16
	wReserved2 uint16
	wReserved3 uint16
	Val        int64
	_          [8]byte
}
