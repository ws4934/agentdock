package desktop

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/tool/core"
)

// 启动独立于输入快照：应用尚未运行时没有可用于操作的窗口。
// 启动结果不授权输入，后续仍须重新观察选定窗口。
type LaunchRequest struct {
	BundleID string `json:"bundle_id,omitempty"`
	AppPath  string `json:"app_path,omitempty"`
	AppName  string `json:"app_name,omitempty"`
	Mode     string `json:"mode,omitempty"`
	WaitMS   *int   `json:"wait_ms,omitempty"`
}
type ApplicationTarget struct {
	BundleID string `json:"bundle_id"`
	AppPath  string `json:"app_path"`
	Name     string `json:"name"`
}
type ApplicationLaunch struct {
	Application         Application `json:"application"`
	AlreadyRunning      bool        `json:"already_running"`
	LaunchRequested     bool        `json:"launch_requested"`
	ActivationRequested bool        `json:"activation_requested"`
}
type ApplicationBackend interface {
	ResolveApplication(context.Context, LaunchRequest) (ApplicationTarget, error)
	LaunchApplication(context.Context, ApplicationTarget, string) (ApplicationLaunch, error)
}

var bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`)

func normalizeLaunch(r LaunchRequest) (LaunchRequest, error) {
	selectors := 0
	for _, s := range []string{r.BundleID, r.AppPath, r.AppName} {
		if s != "" {
			selectors++
		}
		if !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return r, invalid("application selector must be valid UTF-8 without control characters")
		}
	}
	if selectors != 1 {
		return r, invalid("provide exactly one of bundle_id, app_path or app_name")
	}
	if r.BundleID != "" && (!bundleIDPattern.MatchString(r.BundleID) || len(r.BundleID) > 255) {
		return r, invalid("bundle_id must be a dot-separated application identifier")
	}
	if r.AppPath != "" && (!strings.HasPrefix(r.AppPath, "/") || len(r.AppPath) > 4096 || !strings.EqualFold(filepath.Ext(r.AppPath), ".app")) {
		return r, invalid("app_path must be an absolute local .app bundle path, not a document, executable or URL")
	}
	if r.AppName != "" && (len(r.AppName) > 255 || r.AppName != strings.TrimSpace(r.AppName) || strings.ContainsAny(r.AppName, "/\\") || r.AppName == "." || r.AppName == "..") {
		return r, invalid("app_name must be an exact application name without path separators")
	}
	if r.Mode == "" {
		r.Mode = "background"
	}
	if r.Mode != "background" && r.Mode != "foreground" {
		return r, invalid("mode must be background or foreground")
	}
	if r.WaitMS == nil {
		n := 10000
		r.WaitMS = &n
	}
	if *r.WaitMS < 0 || *r.WaitMS > 30000 {
		return r, invalid("wait_ms must be 0..30000; it bounds window observation after launch")
	}
	return r, nil
}

func (s *Service) Launch(ctx context.Context, r LaunchRequest) (core.Result, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = s.ready(); err != nil {
		return nil, err
	}
	r, err = normalizeLaunch(r)
	if err != nil {
		return nil, err
	}
	ctx, release, controlErr := s.controlled(ctx, "launch")
	if controlErr != nil {
		return nil, controlErr
	}
	defer release()
	backend, ok := s.backend.(ApplicationBackend)
	if !ok {
		return nil, core.NewError("LAUNCH_UNSUPPORTED", "Native application launch is not available", "desktop")
	}
	if err = lockDesktop(ctx); err != nil {
		return nil, err
	}
	defer unlockDesktop()
	s.control.started(ctx)
	// 解析本身只读；失败不消耗旧观察，但一经尝试启动就使旧快照失效。
	target, err := backend.ResolveApplication(ctx, r)
	if err != nil {
		return nil, err
	}
	s.control.target(ctx, Window{}, r.Mode, Application{Name: target.Name, BundleID: target.BundleID})
	before, err := s.backend.State(ctx)
	if err != nil {
		return nil, err
	}
	if before.FrontmostPID <= 0 || len(before.Displays) == 0 {
		return nil, core.NewError("NO_DESKTOP_SESSION", "Application launch requires an interactive logged-in desktop", "desktop")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s.invalidate()
	start := s.now()
	launched, err := backend.LaunchApplication(ctx, target, r.Mode)
	outcome := "launched_or_reused"
	if err != nil {
		outcome = "failed_or_unknown"
	}
	slog.Info("desktop launch", "mode", r.Mode, "target_pid", launched.Application.PID, "outcome", outcome, "elapsed_ms", s.now().Sub(start).Milliseconds())
	if err != nil {
		return nil, err
	}
	if launched.Application.PID <= 0 || launched.Application.BundleID != target.BundleID {
		return nil, core.NewErrorDetails("DESKTOP_LAUNCH_FAILED", "Launch returned an invalid or mismatched application identity", "desktop", map[string]any{"may_have_launched": true, "retry_instruction": "Observe running applications; do not automatically relaunch."})
	}
	result := core.Result{"application": launched.Application, "app_path": target.AppPath, "mode": r.Mode, "already_running": launched.AlreadyRunning, "launch_requested": launched.LaunchRequested, "activation_requested": launched.ActivationRequested, "application_verified": false, "next_required_action": "desktop_snapshot", "foreground_fallback": false, "windows": []Window{}, "window_ready": false, "wait_timed_out": false, "observation_complete": false, "activation_observed": false, "background_interference": false}
	// 只等待和观察，绝不为产生窗口发送快捷键、打开文档或再次启动。
	deadline := time.Now().Add(time.Duration(*r.WaitMS) * time.Millisecond)
	var previousWindows []Window
	for {
		state, observeErr := s.backend.State(ctx)
		if observeErr != nil {
			if errors.Is(observeErr, context.Canceled) || errors.Is(observeErr, context.DeadlineExceeded) {
				return nil, core.NewErrorDetails("DESKTOP_LAUNCH_INCOMPLETE", "Application launch completed but observation was cancelled; observe before retrying", "desktop", map[string]any{"application": launched.Application, "may_have_launched": true})
			}
			result["observation_error"] = "Desktop observation unavailable; call desktop_snapshot again without relaunching."
			return result, nil
		}
		windows := make([]Window, 0)
		for _, w := range state.Windows {
			if w.PID == launched.Application.PID && w.ID > 0 && w.Bounds.Width > 0 && w.Bounds.Height > 0 {
				windows = append(windows, w)
			}
		}
		if len(windows) > 0 {
			s.recordTarget(ctx, state, windows[0], r.Mode)
		}
		active := state.FrontmostPID == launched.Application.PID
		result["windows"] = windows
		result["window_ready"] = len(windows) > 0
		result["observation_complete"] = true
		result["activation_observed"] = active
		result["foreground_changed"] = state.FrontmostPID != before.FrontmostPID
		result["background_interference"] = r.Mode == "background" && active && before.FrontmostPID != launched.Application.PID
		result["background_input_allowed"] = !active
		ready := len(windows) > 0 && (r.Mode != "foreground" || active) && (*r.WaitMS == 0 || reflect.DeepEqual(windows, previousWindows))
		previousWindows = append([]Window(nil), windows...)
		if ready || !time.Now().Before(deadline) {
			result["wait_timed_out"] = !ready && *r.WaitMS > 0
			return result, nil
		}
		timer := time.NewTimer(min(100*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, core.NewErrorDetails("DESKTOP_LAUNCH_INCOMPLETE", "Application may be running; window wait was cancelled", "desktop", map[string]any{"application": launched.Application, "may_have_launched": true, "retry_instruction": "Observe before considering another launch."})
		case <-timer.C:
		}
	}
}
