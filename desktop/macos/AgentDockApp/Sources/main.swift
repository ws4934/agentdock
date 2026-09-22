import AppKit
import Darwin
import Foundation

// 只读诊断在NSApplication/后台服务启动前退出，不能把探针进程当作正在运行的Core。
if CommandLine.arguments == [CommandLine.arguments[0], "--permission-diagnostics"] {
    struct Probe: Encodable {
        let identity: DesktopPermissionIdentity
        let checkedAt: String
        let states: [String: String]
    }
    let snapshot = DesktopPermissionChecker.snapshot()
    var states: [String: String] = [:]
    for kind in DesktopPermissionKind.allCases {
        let name: String
        switch kind {
        case .accessibility: name = "accessibility"
        case .screenRecording: name = "screen_recording"
        case .systemEventsAutomation: name = "system_events_automation"
        case .finderAutomation: name = "finder_automation"
        }
        states[name] = String(describing: snapshot[kind])
    }
    do {
        let data = try JSONEncoder().encode(Probe(identity: .current(), checkedAt: ISO8601DateFormatter().string(from: Date()), states: states))
        FileHandle.standardOutput.write(data); FileHandle.standardOutput.write(Data([10]))
        exit(0)
    } catch { exit(1) }
}

if CommandLine.arguments.contains("--unregister-background-services") {
    var failures: [String] = []
    MainActor.assumeIsolated {
        do {
            try ServiceController().unregisterManagedBackgroundServicesForUninstall()
        } catch {
            failures.append(error.localizedDescription)
        }
        do {
            try MenuLoginAgentController().unregisterForUninstall()
        } catch {
            failures.append(error.localizedDescription)
        }
    }
    if failures.isEmpty {
        exit(0)
    }
    let message = failures.joined(separator: "\n") + "\n"
    FileHandle.standardError.write(Data(message.utf8))
    exit(1)
}

MainActor.assumeIsolated {
    let application = NSApplication.shared
    ApplicationMenu.install()
    let delegate = AppDelegate()
    application.delegate = delegate
    application.setActivationPolicy(.accessory)
    application.run()
}
