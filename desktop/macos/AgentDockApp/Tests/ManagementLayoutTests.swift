import AppKit
import Foundation

// 合成窗口仅在自身离屏视图中渲染；不启动 AppDelegate、服务、权限弹窗或桌面输入。
@main struct ManagementLayoutTests {
    @MainActor static func main() throws {
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("AgentDockLayout-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let paths = AppPaths(home: root, appBundle: root.appendingPathComponent("Fixture.app"))
        let service = ServiceController(paths: paths)
        let defaultsName = "AgentDockLayoutTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: defaultsName)!
        defer { defaults.removePersistentDomain(forName: defaultsName) }
        let setup = SetupWindowController(service: service, menuLoginAgent: MenuLoginAgentController(defaults: defaults), onChanged: {}, onUpdateRequested: {})
        let config = ServiceConfiguration(host: "127.0.0.1", port: 19876, publicURL: nil, authToken: "fixture-hidden", oauthPassword: "fixture-hidden", logLevel: "info", mcpAppsEnabled: true, browserEnabled: false, browserCDPURL: "", browserReuseExistingCDP: false, acpEnabled: false, acpProfiles: [], acpDefaultProfile: "")
        let status = ServiceStatus(installed: true, loaded: false, healthy: false, version: nil, configuration: config, autostartEnabled: false, requiresApproval: false, migrationRequired: false, nexusConnection: .unconfigured)
        setup.update(status: status)
        let center = TaskCenterWindowController(service: service)
        let diagnostics = DiagnosticsWindowController(service: service)
        let output = ProcessInfo.processInfo.environment["AGENTDOCK_NATIVE_SCREENSHOTS"].map { URL(fileURLWithPath: $0) }
        if let output { try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true) }
        func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }
        func render(_ controller: NSWindowController, name: String) throws {
            guard let window = controller.window, let view = window.contentView else { preconditionFailure("missing window") }
            window.setContentSize(NSSize(width: 800, height: 610))
            // 离屏内容没有 NSWindow 绘制的底色；显式合成测试背景以保留黑色文字。
            view.wantsLayer = true
            view.layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
            view.layoutSubtreeIfNeeded()
            for control in descendants(view).compactMap({ $0 as? NSButton }) where !control.isHiddenOrHasHiddenAncestor {
                let frame = control.convert(control.bounds, to: view)
                precondition(frame.width > 0 && frame.height > 0, "zero-size control \(control.title)")
                // 滚动内容可位于视口外；固定区域必须留在窗口中。
                if control.enclosingScrollView == nil {
                    precondition(frame.minX >= -1 && frame.maxX <= view.bounds.width + 1, "horizontal clipping \(control.title): \(frame)")
                }
            }
            if let output, let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
                view.cacheDisplay(in: view.bounds, to: bitmap)
                guard let data = bitmap.representation(using: .png, properties: [:]) else { preconditionFailure("render failed") }
                try data.write(to: output.appendingPathComponent(name + ".png"))
            }
            print("PASS native layout \(name)")
        }
        try render(setup, name: "management-overview")
        if let nav = descendants(setup.window!.contentView!).compactMap({ $0 as? NSSegmentedControl }).first(where: { $0.segmentCount == 2 }) {
            nav.selectedSegment = 1
            _ = nav.sendAction(nav.action, to: nav.target)
            try render(setup, name: "management-connection")
        } else { preconditionFailure("management navigation missing") }
        setup.setExternalOperationInProgress(true)
        let controls = descendants(setup.window!.contentView!).compactMap { $0 as? NSButton }
        precondition(controls.first(where: { $0.title == L10n.text("Task center") })?.isEnabled == true)
        setup.setExternalOperationInProgress(false)
        try render(center, name: "task-center")
        try render(diagnostics, name: "diagnostics-offline")
        precondition(!FileManager.default.fileExists(atPath: paths.environment.path))
        precondition(!FileManager.default.fileExists(atPath: paths.appSupport.appendingPathComponent("service-lifecycle.json").path))
        print("Native management layout tests passed without starting services or reading user files")
    }
}
