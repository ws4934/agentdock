import AppKit
import Foundation

@main
struct DesktopPermissionDiagnosticsTests {
    @MainActor static func main() throws {
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        let denied = DesktopPermissionSnapshot(states: [.accessibility: .notGranted, .screenRecording: .notGranted])
        let granted = DesktopPermissionSnapshot(states: [.accessibility: .granted, .screenRecording: .granted])
        let coreJSON = #"{"process_id":4321,"executable_path":"/fixture/AgentDock.app/Contents/Helpers/agentdock","checked_at":"2026-09-21T00:00:00Z","enabled":true,"supported":true,"permissions":{"accessibility":false,"screen_recording":true,"secure_input":false},"build":{"version":"0.8.3","commit":"fixture"}}"#
        let report = try JSONDecoder().decode(DesktopCorePermissionReport.self, from: Data(coreJSON.utf8))
        precondition(DesktopPermissionPresentation.coreState(report, kind: .accessibility) == .notGranted)
        precondition(DesktopPermissionPresentation.coreState(report, kind: .screenRecording) == .granted)
        precondition(DesktopPermissionPresentation.coreState(nil, kind: .screenRecording) == .unavailable)
        precondition(DesktopPermissionPresentation.needsRecovery(granted, core: report))
        precondition(!DesktopPermissionPresentation.needsRecovery(granted, core: nil))
        precondition(DesktopPermissionPresentation.title(.notGranted, kind: .screenRecording) != DesktopPermissionState.notGranted.title)
        precondition(DesktopPermissionPresentation.recovery(adHoc: true) != DesktopPermissionPresentation.recovery(adHoc: false))

        var snapshots = 0
        var current = denied
        var callbacks: [@MainActor (Result<DesktopCorePermissionReport, Error>) -> Void] = []
        let controller = DesktopPermissionsWindowController(snapshotProvider: {
            snapshots += 1; return current
        }, coreCheck: { callbacks.append($0) })
        // 专用后台窗口，不激活应用、不请求权限、不发送桌面输入。
        controller.startMonitoring()
        controller.window?.orderBack(nil)
        controller.windowDidBecomeKey(Notification(name: NSWindow.didBecomeKeyNotification))
        precondition(snapshots > 0 && callbacks.count == 1)
        func labels() -> [String] {
            func text(_ view: NSView) -> [String] {
                (view as? NSTextField).map { [$0.stringValue] } ?? view.subviews.flatMap { text($0) }
            }
            return controller.window?.contentView.map { text($0) } ?? []
        }
        callbacks.removeFirst()(.success(report))
        precondition(labels().contains(where: { $0.contains("PID 4321") }))
        current = granted
        let previous = snapshots
        NotificationCenter.default.post(name: NSApplication.didBecomeActiveNotification, object: app)
        precondition(snapshots > previous && callbacks.count == 1)
        callbacks.removeFirst()(.failure(NSError(domain: "fixture", code: 1)))
        precondition(!labels().contains(where: { $0.contains("PID 4321") }))
        precondition(labels().contains(L10n.text("Unable to check")))
        let timerBaseline = snapshots
        RunLoop.main.run(until: Date().addingTimeInterval(2.3))
        precondition(snapshots > timerBaseline && callbacks.count == 1)
        let late = callbacks.removeFirst()
        controller.window?.close()
        late(.success(report))
        let closedCount = snapshots
        RunLoop.main.run(until: Date().addingTimeInterval(2.3))
        precondition(snapshots == closedCount)
        controller.startMonitoring(); controller.window?.orderBack(nil)
        controller.windowDidBecomeKey(Notification(name: NSWindow.didBecomeKeyNotification))
        precondition(callbacks.count == 1)
        callbacks.removeFirst()(.success(report))
        precondition(labels().contains(where: { $0.contains("PID 4321") }))
        if let destination = ProcessInfo.processInfo.environment["AGENTDOCK_PERMISSION_TEST_IMAGE"], let view = controller.window?.contentView,
           let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
            view.cacheDisplay(in: view.bounds, to: bitmap)
            try bitmap.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: destination))
        }
        controller.window?.close()
        precondition(!controller.window!.isKeyWindow)
        print("permission identity, separate Core state, activation refresh, timer, failure clearing and close/reopen regressions passed; no permission grants or screen capture")
    }
}
