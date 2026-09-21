package desktop

import (
	"context"
	"github.com/uvwt/agentdock/internal/tool/core"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf16"
)

func mapWindowPoint(p Point, space string, obs *observed) (Point, error) {
	if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
		return Point{}, invalid("coordinates must be finite")
	}
	b := obs.window.Bounds
	if space == "image" {
		if obs.image.Width <= 0 || obs.image.Height <= 0 || p.X < 0 || p.Y < 0 || p.X >= float64(obs.image.Width) || p.Y >= float64(obs.image.Height) {
			return Point{}, invalid("coordinates outside the captured window image")
		}
		p = Point{b.X + p.X*b.Width/float64(obs.image.Width), b.Y + p.Y*b.Height/float64(obs.image.Height)}
	}
	if p.X < b.X || p.Y < b.Y || p.X >= b.X+b.Width || p.Y >= b.Y+b.Height {
		return Point{}, invalid("coordinates outside the selected window")
	}
	return p, nil
}

func (s *Service) actBackground(ctx context.Context, obs *observed, r ActionRequest) (result core.Result, err error) {
	if obs.window.ID == 0 {
		return nil, invalid("discovery snapshots cannot authorize input; select a window_id first")
	}
	if r.Action == "activate" {
		return nil, core.NewError("BACKGROUND_ACTION_UNSUPPORTED", "activate requires an explicitly requested foreground snapshot", "desktop")
	}
	if r.Action == "scroll" && r.Point == nil {
		return nil, invalid("background scroll requires a point inside the selected window; the user's cursor is never used")
	}
	backend, ok := s.backend.(WindowBackend)
	if !ok {
		return nil, backgroundUnsupported()
	}
	current, err := s.backend.State(ctx)
	if err != nil {
		return nil, err
	}
	if !sameBackgroundTarget(obs, current) {
		s.invalidate()
		return nil, stale("Target window disappeared, moved, resized, or changed owner")
	}
	if current.FrontmostPID == obs.window.PID {
		s.invalidate()
		return nil, core.NewError("TARGET_IN_USE", "The target application is in the foreground; background input will not compete with the user", "desktop")
	}
	if r.Action == "set_value" && s.backend.Permissions().SecureInput {
		return nil, core.NewError("SECURE_INPUT", "Secure Input is active; text modification is unavailable", "permission")
	}
	flags, err := modifierFlags(r.Modifiers)
	if err != nil {
		return nil, err
	}
	input := WindowInput{Action: r.Action, Flags: flags, Count: max(1, r.ClickCount), DeltaX: r.DeltaX, DeltaY: r.DeltaY, KeyCode: keyCodes[strings.ToLower(r.Key)], Value: r.Text}
	if r.Button == "right" {
		input.Button = 1
	}
	if r.Button == "middle" {
		input.Button = 2
	}
	if r.Point != nil {
		input.Point, err = mapWindowPoint(*r.Point, r.Space, obs)
		if err != nil {
			return nil, err
		}
	}
	path := make([]Point, len(r.Path))
	for i, p := range r.Path {
		path[i], err = mapWindowPoint(p, r.Space, obs)
		if err != nil {
			return nil, err
		}
	}
	if r.ElementID != "" {
		element, ok := obs.elements[r.ElementID]
		if !ok || !element.Enabled || r.Action == "click" && !element.Pressable || r.Action == "set_value" && !element.ValueSettable {
			return nil, invalid("element is unavailable or does not support the requested AX operation")
		}
		input.Element = &element
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s.invalidate()
	started := s.now()
	defer func() {
		outcome := "dispatched"
		if err != nil {
			outcome = "failed_or_partial"
		}
		slog.Info("desktop action", "mode", "background", "action", r.Action, "target_pid", obs.window.PID, "window_id", obs.window.ID, "outcome", outcome, "elapsed_ms", s.now().Sub(started).Milliseconds())
		if err != nil {
			err = core.NewErrorDetails("DESKTOP_ACTION_FAILED", err.Error(), "desktop", map[string]any{"mode": "background", "action": r.Action, "may_have_dispatched": true, "foreground_fallback": false, "retry_instruction": "Observe again; do not automatically replay or activate the target."})
		}
	}()
	dispatch := func(c context.Context, in WindowInput) error { return backend.WindowInput(c, obs.window, in) }
	switch r.Action {
	case "type":
		units := utf16.Encode([]rune(r.Text))
		for len(units) > 0 {
			if err = ctx.Err(); err != nil {
				break
			}
			n := min(20, len(units))
			if n < len(units) && units[n-1] >= 0xD800 && units[n-1] <= 0xDBFF {
				n--
			}
			input.Units = units[:n]
			input.Value = ""
			if err = dispatch(ctx, input); err != nil {
				break
			}
			units = units[n:]
		}
	case "drag":
		err = dragSequence(ctx, path, r.DurationMS, func(c context.Context, kind string, p Point) error {
			in := input
			in.Action = kind
			in.Point = p
			return dispatch(c, in)
		})
	default:
		err = dispatch(ctx, input)
	}
	if err != nil {
		return nil, err
	}
	// 不恢复或移动用户焦点；应用自行激活时报告干扰，下一次动作也会被拒绝。
	after, err := s.backend.State(ctx)
	if err != nil {
		return nil, err
	}
	if after.FrontmostPID == obs.window.PID {
		return nil, core.NewError("BACKGROUND_INTERFERENCE", "The application activated itself; stopped without restoring or taking over user focus", "desktop")
	}
	return core.Result{"mode": "background", "target_window": obs.window, "action": r.Action, "event_dispatched": true, "application_verified": false, "foreground_fallback": false, "next_required_action": "desktop_snapshot", "snapshot_consumed": true}, nil
}

// 每次鼠标按下都有独立的有界抬起清理；后台闭包始终保留原 PID/window。
func dragSequence(ctx context.Context, path []Point, duration int, mouse func(context.Context, string, Point) error) (err error) {
	if duration == 0 {
		duration = 500
	}
	pos := path[0]
	if err = mouse(ctx, "down", pos); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		releaseErr := mouse(cleanup, "up", pos)
		if err == nil {
			err = releaseErr
		}
	}()
	steps := max(len(path)-1, duration/16)
	for i := 1; i <= steps; i++ {
		timer := time.NewTimer(time.Duration(duration) * time.Millisecond / time.Duration(steps))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		progress := float64(i) * float64(len(path)-1) / float64(steps)
		segment := min(int(progress), len(path)-2)
		fraction := progress - float64(segment)
		pos = Point{path[segment].X + (path[segment+1].X-path[segment].X)*fraction, path[segment].Y + (path[segment+1].Y-path[segment].Y)*fraction}
		if err = mouse(ctx, "drag", pos); err != nil {
			return err
		}
	}
	return nil
}
