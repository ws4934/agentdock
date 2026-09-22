//go:build darwin && cgo && desktop_integration

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 专用测试socket和非激活面板；不连接已安装宿主，不产生系统输入。
func TestNativeComputerUseFaultRegressions(t *testing.T) {
	if os.Getenv("AGENTDOCK_DESKTOP_INPUT_TEST") != "1" {
		t.Skip("explicit local GUI fixture opt-in required")
	}
	root, err := os.MkdirTemp("/tmp", "adfault-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	swift := filepath.Join(repo, "desktop/macos/AgentDockApp")
	args := []string{"-swift-version", "5", "-parse-as-library"}
	for _, file := range []string{"Localization.swift", "ComputerUseTransport.swift", "ComputerUseEmergencyHotkey.swift", "ComputerUsePreview.swift", "ComputerUseMonitor.swift"} {
		args = append(args, filepath.Join(swift, "Sources", file))
	}
	binary := filepath.Join(root, "FaultHarness")
	args = append(args, filepath.Join(swift, "Tests/ComputerUseFaultHarness.swift"), "-o", binary)
	if out, e := exec.CommandContext(t.Context(), "swiftc", args...).CombinedOutput(); e != nil {
		t.Fatalf("compile safety fixture: %v\n%s", e, out)
	}
	out, e := exec.CommandContext(t.Context(), "python3", filepath.Join(repo, "scripts/test/test-computer-use-faults.py"), root, binary).CombinedOutput()
	if e != nil {
		t.Fatalf("stop-intent/tracking-mode regression: %v\n%s", e, out)
	}
	t.Log("verified failed stop delivery remains latched across reconnect and tracking-mode UI keeps heartbeats; no desktop input")
}
