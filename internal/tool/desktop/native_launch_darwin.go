//go:build darwin && cgo

package desktop

/*
#include "native.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"time"
	"unsafe"

	"github.com/uvwt/agentdock/internal/tool/core"
)

func decodeApplication(data *C.char, out any) error {
	if data == nil {
		return nativeError(1)
	}
	defer C.free(unsafe.Pointer(data))
	raw := []byte(C.GoString(data))
	var diagnostic struct {
		Code            string `json:"code"`
		Error           string `json:"error"`
		MayHaveLaunched bool   `json:"may_have_launched"`
	}
	if err := json.Unmarshal(raw, &diagnostic); err != nil {
		return err
	}
	if diagnostic.Error != "" {
		return core.NewErrorDetails(diagnostic.Code, diagnostic.Error, "desktop", map[string]any{"may_have_launched": diagnostic.MayHaveLaunched, "foreground_fallback": false, "retry_instruction": "Observe running applications and windows before considering another launch; never automatically retry or switch to foreground."})
	}
	return json.Unmarshal(raw, out)
}
func (nativeBackend) ResolveApplication(ctx context.Context, r LaunchRequest) (ApplicationTarget, error) {
	if err := ctx.Err(); err != nil {
		return ApplicationTarget{}, err
	}
	if r.AppName != "" {
		path, err := lookupApplicationName(ctx, r.AppName, applicationRoots())
		if err != nil {
			return ApplicationTarget{}, err
		}
		r.AppPath = path
		r.AppName = ""
	}
	data, err := json.Marshal(r)
	if err != nil {
		return ApplicationTarget{}, err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	var target ApplicationTarget
	err = decodeApplication(C.ad_resolve_application(input), &target)
	if err == nil {
		err = ctx.Err()
	}
	return target, err
}
func (nativeBackend) LaunchApplication(ctx context.Context, target ApplicationTarget, mode string) (ApplicationLaunch, error) {
	if err := ctx.Err(); err != nil {
		return ApplicationLaunch{}, err
	}
	timeout := 8 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	if timeout <= 0 {
		return ApplicationLaunch{}, context.DeadlineExceeded
	}
	data, err := json.Marshal(target)
	if err != nil {
		return ApplicationLaunch{}, err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	foreground := 0
	if mode == "foreground" {
		foreground = 1
	}
	var result ApplicationLaunch
	err = decodeApplication(C.ad_launch_application(input, C.int(foreground), C.int(max(1, timeout.Milliseconds()))), &result)
	if err == nil && ctx.Err() != nil {
		err = core.NewErrorDetails("DESKTOP_LAUNCH_INCOMPLETE", "Application may be running; caller cancelled while LaunchServices was completing", "desktop", map[string]any{"may_have_launched": true, "application": result.Application, "foreground_fallback": false})
	}
	return result, err
}
