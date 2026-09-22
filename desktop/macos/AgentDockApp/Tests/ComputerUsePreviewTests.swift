import AppKit
import ScreenCaptureKit

@main
struct ComputerUsePreviewTests {
    @MainActor static func main() async {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let target = ComputerUseWindow(id: 123, pid: 456, title: "Synthetic test target", bounds: ComputerUseRect(x: 0, y: 0, width: 500, height: 300))
        var attempts = 0
        var now = Date()
        let preview = ComputerUsePreview(permissionCheck: { true }, contentProvider: {
            attempts += 1
            throw NSError(domain: "AgentDockTestTransient", code: 1)
        })
        preview.clock = { now }
        preview.select(target)
        try? await Task.sleep(nanoseconds: 100_000_000)
        precondition(attempts == 1)
        preview.select(target)
        try? await Task.sleep(nanoseconds: 50_000_000)
        precondition(attempts == 1, "must respect retry backoff")
        for _ in 0..<6 {
            now = now.addingTimeInterval(10); preview.select(target)
            try? await Task.sleep(nanoseconds: 60_000_000)
        }
        precondition(attempts == 4, "same-target transient failures must retry, but remain bounded")
        preview.retryByUser(); preview.select(target)
        try? await Task.sleep(nanoseconds: 100_000_000)
        precondition(attempts == 5, "explicit local retry should reset the budget")
        preview.select(nil)

        for code in [SCStreamError.Code.userDeclined, .missingEntitlements, .userStopped] {
            var calls = 0
            let blocked = ComputerUsePreview(permissionCheck: { true }, contentProvider: {
                calls += 1; throw NSError(domain: SCStreamErrorDomain, code: code.rawValue)
            })
            blocked.clock = { now }; blocked.select(target)
            try? await Task.sleep(nanoseconds: 100_000_000)
            precondition(blocked.requiresUserRestart)
            now = now.addingTimeInterval(100)
            blocked.select(nil); blocked.select(target)
            try? await Task.sleep(nanoseconds: 100_000_000)
            precondition(calls == 1, "permission/user stop must survive target reset without automatic retry")
            blocked.retryByUser(); blocked.select(target)
            try? await Task.sleep(nanoseconds: 100_000_000)
            precondition(calls == 2)
            blocked.select(nil)
        }
        print("Computer Use preview recovery tests passed (no screen capture)")
    }
}
