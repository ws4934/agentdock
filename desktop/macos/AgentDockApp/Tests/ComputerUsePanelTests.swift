import AppKit
import Foundation

@main
struct ComputerUsePanelTests {
    @MainActor static func main() throws {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let suite = "AgentDock.PanelTest." + UUID().uuidString
        let preferences = UserDefaults(suiteName: suite)!
        defer { preferences.removePersistentDomain(forName: suite) }
        let monitor = ComputerUseMonitor(runtimeRoot: URL(fileURLWithPath: "/tmp/agentdock-ui-test-no-server"), preferences: preferences)
        defer { monitor.shutdown() }
        func state(_ phase: String = "running", approval: Bool = false, session: String = "fixture-session", longTitle: Bool = false, task: String = "", operations: Int = 1, enabled: Bool = true, cleanup: Bool = false) throws -> ComputerUseState {
            var value: [String: Any] = [
                "enabled": enabled, "monitor_required": true, "monitor_connected": true,
                "session_id": session, "epoch": 1, "phase": phase, "activity": "sequence", "mode": "background",
                "application": ["pid": 0, "name": longTitle ? String(repeating: "Long application name ", count: 8) : "Calculator", "bundle_id": "test.synthetic", "app_path": "/System/Applications/Calculator.app"],
                "window": ["id": 0, "pid": 0, "title": "Synthetic preview", "bounds": ["x": 0, "y": 0, "width": 600, "height": 400]],
                "pointer_sequence": 0, "active_operations": operations, "reason": "", "can_resume": phase == "paused",
                "task_reference": task, "cleanup_failed": cleanup,
                "task_label": longTitle ? String(repeating: "Task title ", count: 15) : "Check the calculation", "sequence_step": 3, "sequence_total": 5,
                "trust_available": true,
                "trusted_applications": [["id": "fixture-trust", "application": ["pid": 0, "name": "Calculator", "bundle_id": "test.synthetic", "app_path": "/System/Applications/Calculator.app"], "mode": "background"]]
            ]
            if approval { value["pending_application"] = ["id": "fixture-approval", "rememberable": true, "application": ["pid": 0, "name": "Calculator", "bundle_id": "test.synthetic"], "mode": "background"] }
            return try JSONDecoder().decode(ComputerUseState.self, from: JSONSerialization.data(withJSONObject: value))
        }
        func views(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap { views($0) } }
        func checkLayout() {
            guard let root = monitor.panel.contentView else { preconditionFailure("missing content") }
            root.layoutSubtreeIfNeeded()
            for view in views(root) where view is NSTextField || view is NSButton {
                if view.isHiddenOrHasHiddenAncestor { continue }
                let bounds = view.convert(view.bounds, to: root)
                precondition(bounds.minX >= -2 && bounds.maxX <= root.bounds.width + 2, "horizontal overflow: \(view) \(bounds)")
                precondition(bounds.minY >= -2 && bounds.maxY <= root.bounds.height + 2, "vertical overflow: \(view) \(bounds)")
            }
            precondition(!monitor.panel.isKeyWindow && !monitor.panel.isMainWindow)
        }
        func save(_ name: String) throws {
            checkLayout()
            guard CommandLine.arguments.count > 1, let root = monitor.panel.contentView,
                  let bitmap = root.bitmapImageRepForCachingDisplay(in: root.bounds) else { return }
            root.cacheDisplay(in: root.bounds, to: bitmap)
            let directory = URL(fileURLWithPath: CommandLine.arguments[1])
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try bitmap.representation(using: .png, properties: [:])?.write(to: directory.appendingPathComponent(name))
        }
        // 历史会话和信任记录不能让控制图标常驻；真实任务与安全告警不能被隐藏。
        for phase in ["idle", "completed", "stopped", "closed"] {
            let inactive = try state(phase, operations: 0)
            precondition(!inactive.shouldShowStatusItem, "inactive history kept the control indicator visible")
        }
        for phase in ["running", "pausing", "stopping", "paused", "cleanup_failed"] {
            let active = try state(phase, operations: 0)
            precondition(active.shouldShowStatusItem, "active/safety state lost the control indicator")
        }
        let reserved = try state("idle", session: "", task: "owned-task", operations: 0)
        precondition(reserved.shouldShowStatusItem)
        let disabled = try state("idle", session: "", task: "owned-task", operations: 0, enabled: false)
        precondition(!disabled.shouldShowStatusItem)
        let cleanup = try state("closed", operations: 0, enabled: false, cleanup: true)
        precondition(cleanup.shouldShowStatusItem)
        let idle = try state("idle", session: "", operations: 0)
        monitor.apply(idle)
        precondition(!monitor.panel.isVisible)
        let trusted = monitor.trustedApplicationsMenuItem()
        precondition(trusted.isEnabled && trusted.submenu?.items.count == 1, "idle trust revocation entry disappeared")
        precondition(trusted.submenu?.items.first?.representedObject as? String == "fixture-trust")

        let normal = try state()
        monitor.panel.appearance = NSAppearance(named: .aqua)
        monitor.apply(normal)
        try save("panel-expanded-light.png")
        let initialRenders = monitor.renderCount
        let initialMenus = monitor.menuBuildCount
        for _ in 0..<1000 { monitor.apply(normal) }
        precondition(monitor.renderCount == initialRenders, "unchanged heartbeats rebuilt UI")
        precondition(monitor.menuBuildCount == initialMenus, "heartbeats rebuilt menus")
        let expandedHeight = monitor.panel.frame.height
        monitor.togglePreview()
        precondition(preferences.bool(forKey: "ComputerUsePreviewExpanded") == false)
        precondition(monitor.panel.frame.height < expandedHeight - 100, "compact mode did not shrink")
        try save("panel-compact-light.png")
        monitor.apply(try state(approval: true))
        try save("panel-approval-light.png")
        let buttons = views(monitor.panel.contentView!).compactMap { $0 as? NSButton }
        precondition(buttons.contains { $0.title == L10n.text("Always allow") && !$0.isHiddenOrHasHiddenAncestor })
        monitor.panel.appearance = NSAppearance(named: .darkAqua)
        try save("panel-approval-dark.png")
        monitor.apply(try state("paused", longTitle: true))
        try save("panel-long-labels-dark.png")
        monitor.apply(normal)
        monitor.collapse()
        monitor.apply(try state(session: "new-session"))
        precondition(!monitor.panel.isVisible, "new task ignored user's collapse choice")
        monitor.apply(try state("completed"))
        let restored = ComputerUseMonitor(runtimeRoot: URL(fileURLWithPath: "/tmp/agentdock-ui-test-no-server"), preferences: preferences)
        restored.apply(try state("completed"))
        precondition(restored.panel.frame.height < expandedHeight - 100, "preview preference not restored")
        restored.shutdown()
        print("Panel tests passed: light/dark/compact/approval/long labels; 1000 identical updates -> 0 extra renders and 0 menu rebuilds; no desktop input or capture")
    }
}
