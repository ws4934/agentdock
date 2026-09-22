import AppKit

// 显式原生验收用的独立进程；不启动真实安装的菜单栏应用、不修改它的设置。
@MainActor
final class MonitorFixtureDelegate: NSObject, NSApplicationDelegate {
    var monitor: ComputerUseMonitor!
    var timer: Timer?
    var lastCommand = ""
    let root: URL
    let report: URL
    let commands: URL
    init(root: URL, report: URL, commands: URL) { self.root = root; self.report = report; self.commands = commands }
    func applicationDidFinishLaunching(_ notification: Notification) {
        monitor = ComputerUseMonitor(runtimeRoot: root)
        // 仅测试允许捕获这个专用面板，正常面板仍使用 sharingType.none。
        monitor.panel.sharingType = .readOnly
        monitor.start()
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.sample() }
        }
    }
    func sample() {
        let command = (try? String(contentsOf: commands, encoding: .utf8)) ?? ""
        if !command.isEmpty && command != lastCommand {
            lastCommand = command
            switch command.split(separator: ":").last {
            case "pause", "resume": monitor.togglePause()
            case "approve": monitor.approveApplication()
            case "deny": monitor.denyApplication()
            case "step": monitor.nextStep()
            case "end_task": monitor.endTask()
            case "close": monitor.panel.performClose(nil)
            case "collapse": monitor.collapse()
            case "show": monitor.showPanel()
            case "screenshot":
                if let view = monitor.panel.contentView, let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
                    view.cacheDisplay(in: view.bounds, to: bitmap)
                    try? bitmap.representation(using: .png, properties: [:])?.write(to: root.appendingPathComponent("panel.png"))
                }
            case "quit": NSApp.terminate(nil)
            default: break
            }
        }
        let output: [String: Any] = [
            "pid": ProcessInfo.processInfo.processIdentifier,
            "phase": monitor.state?.phase ?? "unknown", "session_id": monitor.state?.session_id ?? "",
            "approval_id": monitor.state?.pending_application?.id ?? "",
            "can_resume": monitor.state?.can_resume ?? false,
            "connected": monitor.connected, "visible": monitor.panel.isVisible,
            "key_window": monitor.panel.isKeyWindow, "main_window": monitor.panel.isMainWindow,
            "active_app": NSApp.isActive, "frames": monitor.preview.frameCount,
            "window_id": monitor.panel.windowNumber
        ]
        if let data = try? JSONSerialization.data(withJSONObject: output) { try? data.write(to: report, options: .atomic) }
    }
    func applicationWillTerminate(_ notification: Notification) { timer?.invalidate(); monitor.shutdown() }
}

@main
struct MonitorFixtureMain {
    @MainActor static func main() {
        guard CommandLine.arguments.count == 4 else { exit(2) }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let delegate = MonitorFixtureDelegate(root: URL(fileURLWithPath: CommandLine.arguments[1]), report: URL(fileURLWithPath: CommandLine.arguments[2]), commands: URL(fileURLWithPath: CommandLine.arguments[3]))
        app.delegate = delegate
        withExtendedLifetime(delegate) { app.run() }
    }
}
