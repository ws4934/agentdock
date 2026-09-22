package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputerUseUIKeepsControlsWithoutPersistentExplanations(t *testing.T) {
	root := filepath.Join("..", "..", "desktop", "macos", "AgentDockApp", "Sources")
	monitor, err := os.ReadFile(filepath.Join(root, "ComputerUseMonitor.swift"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(monitor)
	for _, removed := range []string{"detailLabel", "Live window preview · local only", "Frame unchanged · window may be static or minimized", "Stops desktop control only, not other agent tools."} {
		if strings.Contains(text, removed) {
			t.Fatalf("redundant monitor copy restored: %s", removed)
		}
	}
	for _, required := range []string{"Foreground control can affect the desktop.", "Confirm manual cleanup", "Stop not yet confirmed", "approvalBox.isHidden", "taskLabel.isHidden = taskLabel.stringValue.isEmpty", "windowShouldClose", "stopAndClose()"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing safety or UI behavior: %s", required)
		}
	}
	permissions, err := os.ReadFile(filepath.Join(root, "DesktopPermissionsWindowController.swift"))
	if err != nil {
		t.Fatal(err)
	}
	text = string(permissions)
	for _, removed := range []string{"let intro =", "let filesDetail =", "let appManagementDetail =", "let detail = PermissionUI.detailLabel(kind.detail)"} {
		if strings.Contains(text, removed) {
			t.Fatalf("redundant permission copy restored: %s", removed)
		}
	}
	for _, required := range []string{"title.toolTip = kind.detail", "DesktopPermissionPresentation.coreState", "Permission recovery…", "DesktopPermissionChecker.request(kind)", "RunLoop.main.add(next, forMode: .common)"} {
		if !strings.Contains(text, required) {
			t.Fatalf("permission diagnostics removed: %s", required)
		}
	}
}
