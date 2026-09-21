package desktop

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

type fakeWindowBackend struct {
	*fakeBackend
	captured []Window
	inputs   []WindowInput
	targets  []Window
	hook     func(context.Context, WindowInput) error
}

func windowFixture() *fakeWindowBackend {
	b := fixtureBackend()
	b.state.Windows = append(b.state.Windows, Window{ID: 9, PID: 20, Bounds: Rect{X: 200, Y: 150, Width: 500, Height: 300}})
	b.tree.Elements = append(b.tree.Elements, Element{Role: "AXTextField", Enabled: true, ValueSettable: true, Path: []int{1}})
	return &fakeWindowBackend{fakeBackend: b}
}
func (b *fakeWindowBackend) CaptureWindow(ctx context.Context, w Window, n int) (Capture, error) {
	b.captured = append(b.captured, w)
	return b.Capture(ctx, Display{}, n)
}
func (b *fakeWindowBackend) WindowTree(_ context.Context, w Window, _, _ int) (Tree, error) {
	return b.tree, nil
}
func (b *fakeWindowBackend) WindowInput(ctx context.Context, w Window, in WindowInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.inputs = append(b.inputs, in)
	b.targets = append(b.targets, w)
	if b.hook != nil {
		return b.hook(ctx, in)
	}
	return nil
}
func observeWindow(t *testing.T, s *Service) string {
	t.Helper()
	r, e := s.Snapshot(t.Context(), SnapshotRequest{WindowID: 9, PID: 20, Accessibility: true})
	if e != nil {
		t.Fatal(e)
	}
	return r["snapshot_id"].(string)
}
func TestBackgroundDefaultDiscoveryCannotAuthorizeInput(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	r, e := s.Snapshot(t.Context(), SnapshotRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if r["mode"] != "background" || r["observation_only"] != true || r["image"] != nil || len(b.captured) != 0 {
		t.Fatal(r)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: r["snapshot_id"].(string)})
	requireCode(t, e, "INVALID_ARGUMENT")
	if len(b.events)+len(b.inputs) != 0 {
		t.Fatal("discovery dispatched input")
	}
}
func TestBackgroundWindowCoordinatesAndUserAppSwitch(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	b.state.FrontmostPID = 30 // 用户在其他应用间切换不应使独立后台目标失效。
	r, e := s.Act(t.Context(), ActionRequest{Action: "click", SnapshotID: id, Point: &Point{100, 50}, Space: "image"})
	if e != nil || r["mode"] != "background" || r["foreground_fallback"] != false {
		t.Fatalf("%v %v", r, e)
	}
	want := Point{200 + 100*500.0/720, 150 + 50*300.0/450}
	if b.targets[0].ID != 9 || b.targets[0].PID != 20 || b.inputs[0].Point != want || len(b.events) != 0 {
		t.Fatalf("%+v %+v", b.targets, b.inputs)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, e, "STALE_SNAPSHOT")
}
func TestBackgroundTargetGuards(t *testing.T) {
	for _, change := range []string{"foreground", "closed", "pid", "moved", "resized", "display"} {
		t.Run(change, func(t *testing.T) {
			b := windowFixture()
			s := New(true, b)
			defer s.Close()
			id := observeWindow(t, s)
			b.state.Windows = append([]Window{}, b.state.Windows...)
			b.state.Displays = append([]Display{}, b.state.Displays...)
			code := "STALE_SNAPSHOT"
			switch change {
			case "foreground":
				b.state.FrontmostPID = 20
				code = "TARGET_IN_USE"
			case "closed":
				b.state.Windows = b.state.Windows[:1]
			case "pid":
				b.state.Windows[1].PID = 21
			case "moved":
				b.state.Windows[1].Bounds.X++
			case "resized":
				b.state.Windows[1].Bounds.Width++
			case "display":
				b.state.Displays[0].Bounds.Width++
			}
			_, e := s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
			requireCode(t, e, code)
			if len(b.inputs)+len(b.events) != 0 {
				t.Fatal("stale/foreground target received input")
			}
		})
	}
}
func TestBackgroundRejectsAmbiguityAndFallback(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	yes := true
	for _, r := range []SnapshotRequest{{Mode: "automatic"}, {DisplayID: 1}, {PID: 20}, {Accessibility: true}, {Screenshot: &yes}, {WindowID: 99}, {WindowID: 9, PID: 10}, {Mode: "foreground", WindowID: 9}} {
		_, e := s.Snapshot(t.Context(), r)
		requireCode(t, e, "INVALID_ARGUMENT")
	}
	for _, a := range []ActionRequest{{Action: "activate", PID: 20}, {Action: "scroll", DeltaY: 10}, {Action: "click", Point: &Point{199, 151}}, {Action: "click", Point: &Point{700, 200}}, {Action: "click", Point: &Point{math.NaN(), 0}}, {Action: "set_value", ElementID: "e1", Text: "no"}} {
		a.SnapshotID = observeWindow(t, s)
		_, e := s.Act(t.Context(), a)
		if e == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
	legacy := New(true, b.fakeBackend)
	defer legacy.Close()
	_, e := legacy.Snapshot(t.Context(), SnapshotRequest{WindowID: 9})
	requireCode(t, e, "BACKGROUND_UNSUPPORTED")
	if len(b.inputs)+len(b.events) != 0 {
		t.Fatal("rejected request fell back to global input")
	}
}
func TestBackgroundUnicodeValueAndDispatchErrors(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	text := strings.Repeat("a", 19) + "🚀你好"
	_, e := s.Act(t.Context(), ActionRequest{Action: "type", Text: text, SnapshotID: observeWindow(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	var units []uint16
	for _, in := range b.inputs {
		units = append(units, in.Units...)
	}
	if string(utf16.Decode(units)) != text || len(b.inputs) != 2 {
		t.Fatal(b.inputs)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "set_value", ElementID: "e2", Text: "", SnapshotID: observeWindow(t, s)})
	if e != nil {
		t.Fatal(e)
	}
	if b.inputs[2].Action != "set_value" || b.inputs[2].Value != "" {
		t.Fatal(b.inputs)
	}
	id := observeWindow(t, s)
	b.permissions.SecureInput = true
	_, e = s.Act(t.Context(), ActionRequest{Action: "set_value", ElementID: "e2", Text: "secret", SnapshotID: id})
	requireCode(t, e, "SECURE_INPUT")
	b.permissions.SecureInput = false
	b.hook = func(context.Context, WindowInput) error { return errors.New("application refused") }
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, e, "DESKTOP_ACTION_FAILED")
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, e, "STALE_SNAPSHOT")
	if len(b.events) != 0 {
		t.Fatal("fallback into global input")
	}
}
func TestBackgroundDragCancellationStaysBound(t *testing.T) {
	b := windowFixture()
	s := New(true, b)
	defer s.Close()
	id := observeWindow(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// 模拟按下成功后请求取消；抬起使用新上下文且仍定向原窗口。
	b.hook = func(c context.Context, in WindowInput) error {
		if in.Action == "down" {
			cancel()
			return nil
		}
		return c.Err()
	}
	_, e := s.Act(ctx, ActionRequest{Action: "drag", SnapshotID: id, Path: []Point{{250, 200}, {300, 220}}, DurationMS: 100})
	requireCode(t, e, "DESKTOP_ACTION_FAILED")
	if len(b.inputs) != 2 || b.inputs[0].Action != "down" || b.inputs[1].Action != "up" {
		t.Fatal(b.inputs)
	}
	for _, w := range b.targets {
		if w.ID != 9 || w.PID != 20 {
			t.Fatal("release target escaped", w)
		}
	}
	if len(b.events) != 0 {
		t.Fatal("background cleanup used a global release")
	}
}
func TestBackgroundNativeNeverUsesGlobalFallback(t *testing.T) {
	for _, file := range []string{"native_window_impl.h", "native_shortcut_impl.h"} {
		data, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		for _, forbidden := range []string{"CGEventPost(", "ad_activate(", "activateWithOptions", "CGWarpMouseCursorPosition", "kAXRaiseAction"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatal(file + " contains " + forbidden)
			}
		}
	}

}
