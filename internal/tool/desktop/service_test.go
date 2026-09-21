package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/uvwt/agentdock/internal/tool/core"
)

type fakeBackend struct {
	state       State
	permissions Permissions
	supported   bool
	events      []string
	points      []Point
	text        []uint16
	tree        Tree
	fail        string
	mouseHook   func(string)
}

func fixtureBackend() *fakeBackend {
	return &fakeBackend{supported: true, permissions: Permissions{ScreenRecording: true, Accessibility: true}, state: State{FrontmostPID: 10, Displays: []Display{{ID: 1, Main: true, Bounds: Rect{Width: 1440, Height: 900}, PixelWidth: 2880, PixelHeight: 1800}, {ID: 2, Bounds: Rect{X: -1920, Y: -200, Width: 1920, Height: 1080}, PixelWidth: 1920, PixelHeight: 1080}}, Windows: []Window{{ID: 7, PID: 10, Bounds: Rect{Width: 1000, Height: 800}}}, Applications: []Application{{PID: 10}, {PID: 20}}}, tree: Tree{Elements: []Element{{Role: "AXButton", Title: "Save", Enabled: true, Pressable: true, Path: []int{0, 1}}}}}
}
func (b *fakeBackend) Supported() bool          { return b.supported }
func (b *fakeBackend) Permissions() Permissions { return b.permissions }
func (b *fakeBackend) RequestPermission(p string) error {
	b.events = append(b.events, "permission:"+p)
	return nil
}
func (b *fakeBackend) State(context.Context) (State, error) { return b.state, nil }
func (b *fakeBackend) Capture(context.Context, Display, int) (Capture, error) {
	return Capture{Data: []byte("fake-image"), Width: 720, Height: 450}, nil
}
func (b *fakeBackend) Tree(context.Context, int, int, int) (Tree, error) { return b.tree, nil }
func (b *fakeBackend) Press(context.Context, int, Element) error {
	b.events = append(b.events, "press")
	return nil
}
func (b *fakeBackend) Activate(_ context.Context, pid int) error {
	b.events = append(b.events, "activate")
	b.state.FrontmostPID = pid
	return nil
}
func (b *fakeBackend) Mouse(_ context.Context, _ int, kind string, p Point, _, _ int, _ uint64) error {
	b.events = append(b.events, kind)
	b.points = append(b.points, p)
	if b.mouseHook != nil {
		b.mouseHook(kind)
	}
	if b.fail == kind {
		return errors.New("injected failure")
	}
	return nil
}
func (b *fakeBackend) Scroll(context.Context, int, int, int) error {
	b.events = append(b.events, "scroll")
	return nil
}
func (b *fakeBackend) Key(context.Context, int, uint16, uint64) error {
	b.events = append(b.events, "key")
	return nil
}
func (b *fakeBackend) Text(_ context.Context, _ int, text []uint16) error {
	b.events = append(b.events, "text")
	b.text = append(b.text, text...)
	return nil
}
func observe(t *testing.T, s *Service) string {
	t.Helper()
	r, e := s.Snapshot(t.Context(), SnapshotRequest{Accessibility: true})
	if e != nil {
		t.Fatal(e)
	}
	return r["snapshot_id"].(string)
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *core.ToolError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestStatusNeverRequestsPermission(t *testing.T) {
	b := fixtureBackend()
	s := New(false, b)
	r, e := s.Status(t.Context())
	if e != nil || r["enabled"] != false || len(b.events) != 0 {
		t.Fatalf("%v %v %v", r, e, b.events)
	}
	_, e = s.Snapshot(t.Context(), SnapshotRequest{})
	requireCode(t, e, "DESKTOP_DISABLED")
}
func TestUnsupportedAndPermissions(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	b.supported = false
	_, err := s.Snapshot(t.Context(), SnapshotRequest{})
	requireCode(t, err, "UNSUPPORTED_PLATFORM")
	b.supported = true
	b.permissions.ScreenRecording = false
	_, err = s.Snapshot(t.Context(), SnapshotRequest{})
	requireCode(t, err, "PERMISSION_REQUIRED")
	no := false
	b.permissions.Accessibility = false
	_, err = s.Snapshot(t.Context(), SnapshotRequest{Screenshot: &no, Accessibility: true})
	requireCode(t, err, "PERMISSION_REQUIRED")
	r, err := s.Snapshot(t.Context(), SnapshotRequest{Screenshot: &no})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: r["snapshot_id"].(string)})
	requireCode(t, err, "PERMISSION_REQUIRED")
	if len(b.events) > 0 {
		t.Fatal("permission failures must not dispatch events")
	}
}
func TestSnapshotMetadataAndPrivatePaths(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	r, e := s.Snapshot(t.Context(), SnapshotRequest{Accessibility: true})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), `"path"`) {
		t.Fatal("AX path leaked")
	}
	image := r["image"].(map[string]any)
	if image["screen_points_per_pixel_x"] != float64(2) {
		t.Fatal(image)
	}
	if r["_mcp_image_base64"] == nil {
		t.Fatal("MCP image missing")
	}
}
func TestImageAndNegativeDisplayCoordinates(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	id := observe(t, s)
	_, err := s.Act(t.Context(), ActionRequest{Action: "click", SnapshotID: id, Point: &Point{100, 50}, Space: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if b.points[0] != (Point{200, 100}) {
		t.Fatal(b.points)
	}
	id = observe(t, s)
	_, err = s.Act(t.Context(), ActionRequest{Action: "move", SnapshotID: id, Point: &Point{-500, -100}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Point{{1440, 100}, {0, -1}, {-2000, 0}, {math.NaN(), 0}, {math.Inf(1), 0}} {
		id = observe(t, s)
		_, err = s.Act(t.Context(), ActionRequest{Action: "click", SnapshotID: id, Point: &p})
		requireCode(t, err, "INVALID_ARGUMENT")
	}
}
func TestImageCoordinatesRequireCapturedImage(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	no := false
	r, e := s.Snapshot(t.Context(), SnapshotRequest{Screenshot: &no})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "click", SnapshotID: r["snapshot_id"].(string), Point: &Point{1, 1}, Space: "image"})
	requireCode(t, e, "INVALID_ARGUMENT")
}
func TestSnapshotExpiryConsumptionAndTargetChanges(t *testing.T) {
	t.Run("one use", func(t *testing.T) {
		b := fixtureBackend()
		s := New(true, b)
		id := observe(t, s)
		a := ActionRequest{Action: "key", Key: "s", SnapshotID: id, Modifiers: []string{"cmd"}}
		r, e := s.Act(t.Context(), a)
		if e != nil || r["application_verified"] != false {
			t.Fatalf("%v %v", r, e)
		}
		_, e = s.Act(t.Context(), a)
		requireCode(t, e, "STALE_SNAPSHOT")
		if len(b.events) != 1 {
			t.Fatal(b.events)
		}
	})
	t.Run("expired", func(t *testing.T) {
		b := fixtureBackend()
		s := New(true, b)
		now := time.Now()
		s.now = func() time.Time { return now }
		id := observe(t, s)
		now = now.Add(snapshotTTL + time.Second)
		_, e := s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
		requireCode(t, e, "STALE_SNAPSHOT")
	})
	for _, change := range []string{"pid", "window", "display"} {
		t.Run(change, func(t *testing.T) {
			b := fixtureBackend()
			s := New(true, b)
			id := observe(t, s)
			switch change {
			case "pid":
				b.state.FrontmostPID = 20
			case "window":
				b.state.Windows = append([]Window{}, b.state.Windows...)
				b.state.Windows[0].ID = 8
			case "display":
				b.state.Displays = append([]Display{}, b.state.Displays...)
				b.state.Displays[0].Bounds.Width = 1280
			}
			_, e := s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
			requireCode(t, e, "STALE_SNAPSHOT")
			if len(b.events) != 0 {
				t.Fatal(b.events)
			}
		})
	}
}
func TestAXPressAndStaleElement(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	id := observe(t, s)
	_, e := s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "not-found", SnapshotID: id})
	requireCode(t, e, "INVALID_ARGUMENT")
	_, e = s.Act(t.Context(), ActionRequest{Action: "click", ElementID: "e1", SnapshotID: id})
	if e != nil || len(b.events) != 1 || b.events[0] != "press" {
		t.Fatalf("%v %v", e, b.events)
	}
}
func TestUnicodeChunkingAndSecureInput(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	id := observe(t, s)
	text := strings.Repeat("a", 19) + "🚀你好"
	_, e := s.Act(t.Context(), ActionRequest{Action: "type", Text: text, SnapshotID: id})
	if e != nil {
		t.Fatal(e)
	}
	if string(utf16.Decode(b.text)) != text || len(b.events) != 2 {
		t.Fatalf("%v %v", b.text, b.events)
	}
	id = observe(t, s)
	b.permissions.SecureInput = true
	_, e = s.Act(t.Context(), ActionRequest{Action: "type", Text: "secret", SnapshotID: id})
	requireCode(t, e, "SECURE_INPUT")
}
func TestDragReleaseOnFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "success"} {
		t.Run(mode, func(t *testing.T) {
			b := fixtureBackend()
			s := New(true, b)
			id := observe(t, s)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "failure" {
				b.fail = "drag"
			}
			if mode == "cancel" {
				b.mouseHook = func(kind string) {
					if kind == "down" {
						cancel()
					}
				}
			}
			_, e := s.Act(ctx, ActionRequest{Action: "drag", SnapshotID: id, Path: []Point{{10, 10}, {30, 30}}, DurationMS: 32})
			if mode != "success" {
				requireCode(t, e, "DESKTOP_ACTION_FAILED")
			} else if e != nil {
				t.Fatal(e)
			}
			if b.events[0] != "down" || b.events[len(b.events)-1] != "up" {
				t.Fatal(b.events)
			}
			_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
			requireCode(t, e, "STALE_SNAPSHOT")
		})
	}
}
func TestCancelledWaitNeverDispatches(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	id := observe(t, s)
	desktopGate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e := s.Act(ctx, ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	unlockDesktop()
	if !errors.Is(e, context.Canceled) || len(b.events) != 0 {
		t.Fatalf("%v %v", e, b.events)
	}
}
func TestPartialFailureConsumesSnapshot(t *testing.T) {
	b := fixtureBackend()
	b.fail = "click"
	s := New(true, b)
	id := observe(t, s)
	_, e := s.Act(t.Context(), ActionRequest{Action: "click", Point: &Point{10, 10}, SnapshotID: id})
	requireCode(t, e, "DESKTOP_ACTION_FAILED")
	var typed *core.ToolError
	errors.As(e, &typed)
	if typed.Details["may_have_dispatched"] != true {
		t.Fatal(typed)
	}
	_, e = s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, e, "STALE_SNAPSHOT")
}
func TestValidateActionRejectsAmbiguousArguments(t *testing.T) {
	cases := []ActionRequest{
		{Action: "click"}, {Action: "click", Point: &Point{}, ElementID: "e1"}, {Action: "click", ElementID: "e1", Modifiers: []string{"cmd"}},
		{Action: "key", Key: "a", Text: "unexpected"}, {Action: "key", Key: "unknown"}, {Action: "key", Key: "a", Modifiers: []string{"cmd", "command"}},
		{Action: "type", Text: "a\x00b"}, {Action: "type", Text: strings.Repeat("x", 4097)}, {Action: "type", Text: string([]byte{255})},
		{Action: "scroll"}, {Action: "scroll", DeltaY: 2001}, {Action: "drag", Path: []Point{{}}}, {Action: "drag", Path: []Point{{}, {1, 1}}, DurationMS: 2001},
		{Action: "activate", PID: -1}, {Action: "future"}, {Action: "move", Point: &Point{}, Space: "retina"},
	}
	for i, c := range cases {
		c.SnapshotID = "snapshot"
		if err := validateAction(c); err == nil {
			t.Fatalf("case %d accepted: %+v", i, c)
		}
	}
}
func TestAllActionKinds(t *testing.T) {
	cases := []ActionRequest{{Action: "activate", PID: 20}, {Action: "click", Point: &Point{1, 1}, ClickCount: 2}, {Action: "click", ElementID: "e1"}, {Action: "move", Point: &Point{1, 1}}, {Action: "scroll", DeltaY: -100}, {Action: "key", Key: "enter"}, {Action: "type", Text: "abc"}, {Action: "drag", Path: []Point{{1, 1}, {10, 10}}, DurationMS: 16}}
	for _, c := range cases {
		t.Run(c.Action, func(t *testing.T) {
			b := fixtureBackend()
			s := New(true, b)
			c.SnapshotID = observe(t, s)
			r, e := s.Act(t.Context(), c)
			if e != nil || r["event_dispatched"] != true {
				t.Fatalf("%v %v", r, e)
			}
		})
	}
}

func TestCloseCancelsDragAndRefusesNewInput(t *testing.T) {
	b := fixtureBackend()
	s := New(true, b)
	id := observe(t, s)
	started := make(chan struct{})
	done := make(chan error, 1)
	b.mouseHook = func(kind string) {
		if kind == "down" {
			close(started)
		}
	}
	go func() {
		_, err := s.Act(context.Background(), ActionRequest{Action: "drag", SnapshotID: id, Path: []Point{{1, 1}, {2, 2}}, DurationMS: 2000})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("drag did not start")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("shutdown did not cancel drag")
	}
	if b.events[len(b.events)-1] != "up" {
		t.Fatal(b.events)
	}
	_, err := s.Act(t.Context(), ActionRequest{Action: "key", Key: "a", SnapshotID: id})
	requireCode(t, err, "RUNTIME_CLOSING")
	_, err = s.Snapshot(t.Context(), SnapshotRequest{})
	requireCode(t, err, "RUNTIME_CLOSING")
}
