//go:build darwin && cgo && desktop_integration

package desktop

import (
	"context"
	"path/filepath"
	"testing"
)

// 只解析系统已有应用，不启动、不激活、不申请任何系统权限。
func TestNativeApplicationResolution(t *testing.T) {
	backend := NewBackend()
	if !backend.Supported() {
		t.Skip("native macOS backend required")
	}
	apps := backend.(ApplicationBackend)
	byID, err := apps.ResolveApplication(t.Context(), LaunchRequest{BundleID: "com.apple.TextEdit"})
	if err != nil {
		t.Fatal(err)
	}
	if byID.BundleID != "com.apple.TextEdit" || !filepath.IsAbs(byID.AppPath) {
		t.Fatalf("invalid resolved identity: %+v", byID)
	}
	byName, err := apps.ResolveApplication(t.Context(), LaunchRequest{AppName: "TextEdit"})
	if err != nil {
		t.Fatal(err)
	}
	byPath, err := apps.ResolveApplication(t.Context(), LaunchRequest{AppPath: byID.AppPath})
	if err != nil {
		t.Fatal(err)
	}
	if byName.BundleID != byID.BundleID || byPath.AppPath != byID.AppPath {
		t.Fatal("application selectors disagree")
	}
	_, err = apps.ResolveApplication(t.Context(), LaunchRequest{AppPath: "/bin/sh"})
	requireCode(t, err, "INVALID_APPLICATION")
	_, err = apps.ResolveApplication(t.Context(), LaunchRequest{BundleID: "test.agentdock.not-installed.resolution"})
	requireCode(t, err, "APPLICATION_NOT_FOUND")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = apps.ResolveApplication(ctx, LaunchRequest{BundleID: "com.apple.TextEdit"}); err == nil {
		t.Fatal("cancelled resolution continued")
	}
	t.Log("verified exact app name, bundle ID and bundle path resolution; no application launched")
}
