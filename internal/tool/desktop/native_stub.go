//go:build !darwin || !cgo

package desktop

import (
	"context"
	"github.com/uvwt/agentdock/internal/tool/core"
)

type nativeBackend struct{}

func NewBackend() Backend                      { return nativeBackend{} }
func (nativeBackend) Supported() bool          { return false }
func (nativeBackend) Permissions() Permissions { return Permissions{} }
func unsupported() error {
	return core.NewError("UNSUPPORTED_PLATFORM", "Computer Use requires macOS 14+ and a CGO-enabled native build", "platform")
}
func (nativeBackend) RequestPermission(string) error       { return unsupported() }
func (nativeBackend) State(context.Context) (State, error) { return State{}, unsupported() }
func (nativeBackend) Capture(context.Context, Display, int) (Capture, error) {
	return Capture{}, unsupported()
}
func (nativeBackend) Tree(context.Context, int, int, int) (Tree, error) { return Tree{}, unsupported() }
func (nativeBackend) Press(context.Context, int, Element) error         { return unsupported() }
func (nativeBackend) Activate(context.Context, int) error               { return unsupported() }
func (nativeBackend) Mouse(context.Context, int, string, Point, int, int, uint64) error {
	return unsupported()
}
func (nativeBackend) Scroll(context.Context, int, int, int) error    { return unsupported() }
func (nativeBackend) Key(context.Context, int, uint16, uint64) error { return unsupported() }
func (nativeBackend) Text(context.Context, int, []uint16) error      { return unsupported() }
