//go:build darwin && cgo

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework Carbon -framework ScreenCaptureKit -framework ImageIO
#include "native.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	"github.com/uvwt/agentdock/internal/tool/core"
)

type nativeBackend struct{}

func NewBackend() Backend             { return nativeBackend{} }
func (nativeBackend) Supported() bool { return C.ad_supported() != 0 }
func (nativeBackend) Permissions() Permissions {
	bits := int(C.ad_permissions())
	return Permissions{ScreenRecording: bits&1 != 0, Accessibility: bits&2 != 0, SecureInput: bits&4 != 0}
}
func nativeError(code C.int) error {
	if code == 0 {
		return nil
	}
	messages := map[int]string{1: "Native allocation or system call failed", 2: "Foreground application changed before input", 3: "Secure Input is active", 4: "Accessibility element changed, is unavailable, or rejects AXPress", 5: "ScreenCaptureKit capture failed or timed out", 6: "macOS 14+ is required", 7: "Accessibility permission is missing", 8: "Background window disappeared or changed identity/geometry", 9: "Target app is now in the foreground; background input refused", 10: "Target window is not the app focused window; refusing ambiguous keyboard routing", 11: "Target AX window is unavailable or ambiguous", 12: "Background pointer requires the optional macOS window-location bridge; it is unavailable, and foreground fallback is forbidden", 13: "The current keyboard layout cannot translate this shortcut key", 14: "Background menu shortcut is ambiguous or traversal exceeded its bounds; no key was sent", 15: "The matching background menu command is disabled; no key was sent and the application was not activated"}
	message := messages[int(code)]
	if message == "" {
		message = fmt.Sprintf("Native desktop error %d", int(code))
	}
	return core.NewError("NATIVE_DESKTOP_ERROR", message, "desktop")
}
func (nativeBackend) RequestPermission(permission string) error {
	kind := 0
	if permission == "screen_recording" {
		kind = 1
	}
	return nativeError(C.ad_request_permission(C.int(kind)))
}
func decodeNative(data *C.char, out any) error {
	if data == nil {
		return nativeError(1)
	}
	defer C.free(unsafe.Pointer(data))
	raw := []byte(C.GoString(data))
	var diagnostic struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &diagnostic); err != nil {
		return err
	}
	if diagnostic.Error != "" {
		return core.NewError("NATIVE_DESKTOP_ERROR", diagnostic.Error, "desktop")
	}
	return json.Unmarshal(raw, out)
}
func (nativeBackend) State(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	var state State
	err := decodeNative(C.ad_state(), &state)
	return state, err
}
func (nativeBackend) Tree(ctx context.Context, pid, nodes, depth int) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	var tree Tree
	err := decodeNative(C.ad_tree(C.int(pid), C.int(nodes), C.int(depth)), &tree)
	return tree, err
}
func (nativeBackend) Capture(ctx context.Context, display Display, dimension int) (Capture, error) {
	return nativeCapture(ctx, display.ID, 0, dimension)
}
func (nativeBackend) CaptureWindow(ctx context.Context, window Window, dimension int) (Capture, error) {
	return nativeCapture(ctx, window.ID, window.PID, dimension)
}
func nativeCapture(ctx context.Context, id uint32, pid int, dimension int) (Capture, error) {
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	timeout := 6 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	if timeout <= 0 {
		return Capture{}, context.DeadlineExceeded
	}
	var data *C.uchar
	var size C.size_t
	var width, height C.int
	var code C.int
	if pid > 0 {
		code = C.ad_capture_window(C.uint32_t(id), C.int(pid), C.int(dimension), C.int(max(1, timeout.Milliseconds())), &data, &size, &width, &height)
	} else {
		code = C.ad_capture(C.uint32_t(id), C.int(dimension), C.int(max(1, timeout.Milliseconds())), &data, &size, &width, &height)
	}
	if data != nil {
		defer C.free(unsafe.Pointer(data))
	}
	if err := nativeError(code); err != nil {
		return Capture{}, err
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if data == nil || size == 0 || size > 8<<20 {
		return Capture{}, nativeError(5)
	}
	return Capture{Data: C.GoBytes(unsafe.Pointer(data), C.int(size)), Width: int(width), Height: int(height)}, nil
}
func (nativeBackend) Press(ctx context.Context, pid int, e Element) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data := C.CString(string(encoded))
	defer C.free(unsafe.Pointer(data))
	return nativeError(C.ad_press(C.int(pid), data))
}
func (nativeBackend) Activate(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nativeError(C.ad_activate(C.int(pid)))
}
func (nativeBackend) Mouse(ctx context.Context, pid int, kind string, p Point, button, count int, flags uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	kinds := map[string]int{"click": 0, "move": 1, "down": 2, "up": 3, "drag": 4}
	kindID, ok := kinds[kind]
	if !ok {
		return invalid("unknown native mouse event")
	}
	return nativeError(C.ad_mouse(C.int(pid), C.int(kindID), C.double(p.X), C.double(p.Y), C.int(button), C.int(count), C.uint64_t(flags)))
}
func (nativeBackend) Scroll(ctx context.Context, pid, dx, dy int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nativeError(C.ad_scroll(C.int(pid), C.int(dx), C.int(dy)))
}
func (nativeBackend) Key(ctx context.Context, pid int, key uint16, flags uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nativeError(C.ad_key(C.int(pid), C.uint16_t(key), C.uint64_t(flags)))
}
func (nativeBackend) Text(ctx context.Context, pid int, text []uint16) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(text) == 0 {
		return nil
	}
	return nativeError(C.ad_text(C.int(pid), (*C.uint16_t)(unsafe.Pointer(&text[0])), C.size_t(len(text))))
}
