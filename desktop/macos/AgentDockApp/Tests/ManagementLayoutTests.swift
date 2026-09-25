import AppKit
import Foundation

// 合成窗口仅在自身离屏视图中渲染；不启动 AppDelegate、服务、权限弹窗或桌面输入。
@main struct ManagementLayoutTests {
    @MainActor static func main() async throws {
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
        var taskPresentations = 0, diagnosticPresentations = 0
        let center = TaskCenterWindowController(service: service, presentWindow: { _ in taskPresentations += 1 })
        let diagnostics = DiagnosticsWindowController(service: service, presentWindow: { _ in diagnosticPresentations += 1 })
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
        for controller in [center as NSWindowController, diagnostics as NSWindowController] {
            var activated = false
            ManagementWindowPresenter.present(controller, activate: { activated = true })
            precondition(activated, "management presentation failed to activate the application")
            controller.close()
        }
        center.present(); center.close(); center.present(); center.close()
        diagnostics.present(); diagnostics.close(); diagnostics.present(); diagnostics.close()
        precondition(taskPresentations == 2 && diagnosticPresentations == 2, "close/reopen lost window routing")
        let managementButtons = descendants(center.window!.contentView!).compactMap { $0 as? NSButton }
        for id in ["task.archive", "task.restore", "task.delete", "task.cleanup"] {
            guard let button = managementButtons.first(where: { $0.identifier?.rawValue == id }) else { preconditionFailure("missing management control " + id) }
            precondition(!button.isEnabled, "unread/offline tasks must not be writable")
        }
        precondition(!FileManager.default.fileExists(atPath: paths.environment.path))
        precondition(!FileManager.default.fileExists(atPath: paths.appSupport.appendingPathComponent("service-lifecycle.json").path))
        if let port = ProcessInfo.processInfo.environment["AGENTDOCK_UI_FIXTURE_PORT"].flatMap(Int.init) {
            let uiPaths = AppPaths(home: root.appendingPathComponent("ui"), appBundle: paths.appBundle)
            try FileManager.default.createDirectory(at: uiPaths.appSupport, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            try Data("AGENTDOCK_HOST=127.0.0.1\nAGENTDOCK_PORT=\(port)\nAGENTDOCK_AUTH_TOKEN=synthetic-ui-token\n".utf8).write(to: uiPaths.environment, options: .atomic)
            let ui = TaskCenterWindowController(service: ServiceController(paths: uiPaths), presentWindow: { _ in })
            let view = ui.window!.contentView!
            let table = descendants(view).compactMap { $0 as? NSTableView }.first!
            let filter = descendants(view).compactMap { $0 as? NSSegmentedControl }.first!
            func button(_ id: String) -> NSButton { descendants(view).compactMap { $0 as? NSButton }.first { $0.identifier?.rawValue == id }! }
            func wait(_ label: String, until condition: () -> Bool) async throws {
                for _ in 0..<500 {
                    if condition() { return }
                    try await Task.sleep(nanoseconds: 10_000_000)
                }
                preconditionFailure("timeout waiting for synthetic UI: " + label)
            }
            func confirm(_ id: String, accept: Bool) async throws {
                button(id).performClick(nil)
                guard let sheet = ui.window?.attachedSheet else { preconditionFailure("confirmation sheet missing") }
                precondition(!ui.windowShouldClose(ui.window!), "write/confirmation can be closed mid-flight")
                precondition(!button(id).isEnabled, "duplicate management request allowed")
                if id == "task.delete" || id == "task.cleanup" {
                    let buttons = descendants(sheet.contentView!).compactMap { $0 as? NSButton }
                    precondition(buttons.first { $0.title == L10n.text("Delete") }?.keyEquivalent == "")
                    precondition(buttons.first { $0.title == L10n.text("Cancel") }?.keyEquivalent == "\r")
                }
                ui.window!.endSheet(sheet, returnCode: accept ? .alertFirstButtonReturn : .alertSecondButtonReturn)
                try await wait("management request settles", until: { ui.windowShouldClose(ui.window!) })
            }
            ui.present()
            try await wait("initial tasks", until: { table.numberOfRows == 3 && button("task.copy").isEnabled })
            table.selectRowIndexes(IndexSet([0, 2]), byExtendingSelection: false)
            precondition(button("task.delete").isEnabled && button("task.archive").isEnabled)
            table.selectRowIndexes(IndexSet([0, 1]), byExtendingSelection: false)
            precondition(!button("task.delete").isEnabled && button("task.archive").isEnabled)
            try render(ui, name: "task-center-populated")
            try await confirm("task.archive", accept: false)
            precondition(table.numberOfRows == 3 && button("task.archive").isEnabled)
            try await confirm("task.archive", accept: true)
            try await wait("archived selections hidden", until: { table.numberOfRows == 1 && button("task.copy").isEnabled })
            filter.selectedSegment = 4; _ = filter.sendAction(filter.action, to: filter.target)
            try await wait("archived filter", until: { table.numberOfRows == 2 && button("task.restore").isEnabled })
            table.selectRowIndexes(IndexSet([0, 1]), byExtendingSelection: false)
            precondition(!button("task.delete").isEnabled && button("task.restore").isEnabled)
            try render(ui, name: "task-center-archived")
            try await confirm("task.restore", accept: true)
            try await wait("restored archive empty", until: { table.numberOfRows == 0 })
            filter.selectedSegment = 0; _ = filter.sendAction(filter.action, to: filter.target)
            try await wait("restored tasks visible", until: { table.numberOfRows == 3 && button("task.cleanup").isEnabled })
            try await confirm("task.cleanup", accept: true)
            try await wait("only unfinished task kept", until: { table.numberOfRows == 1 && button("task.copy").isEnabled })
            precondition(!button("task.delete").isEnabled && !button("task.cleanup").isEnabled)
            let uiConfig = ServiceConfiguration.load(from: uiPaths.environment)!
            let statsData = try await LocalRuntimeClient().get(configuration: uiConfig, path: "/healthz", query: [.init(name: "fixture", value: "ui-stats")])
            let stats = try JSONSerialization.jsonObject(with: statsData) as! [String: Any]
            precondition(stats["writes"] as? Int == 3, "confirmation cancel or refresh repeated writes")
            precondition(stats["ids"] as? [String] == ["tsk_0000000000000022"], "cleanup removed unfinished task")
            ui.close(); ui.present()
            try await wait("reopen after mutations", until: { table.numberOfRows == 1 && button("task.copy").isEnabled })
            ui.close()
            precondition(!FileManager.default.fileExists(atPath: uiPaths.appSupport.appendingPathComponent("service-lifecycle.json").path))
            print("PASS native task interactions: multi-selection, confirmation cancellation, archive, restore, completed cleanup, single writes and reopen")
        }
        print("Native management layout tests passed without starting services or reading user files")
    }
}
