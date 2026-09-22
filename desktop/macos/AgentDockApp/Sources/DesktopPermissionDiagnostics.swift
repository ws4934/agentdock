import Foundation
import Security

struct DesktopCorePermissionReport: Decodable {
    struct Permissions: Decodable {
        let accessibility: Bool
        let screen_recording: Bool
        let secure_input: Bool
    }
    struct Build: Decodable { let version: String; let commit: String }
    let process_id: Int32
    let executable_path: String
    let checked_at: String
    let enabled: Bool
    let supported: Bool
    let permissions: Permissions
    let build: Build
}

// 读取当前进程的公开代码签名信息，不读取TCC数据库、不推测设置页面的开关状态。
struct DesktopPermissionIdentity: Encodable {
    let processID: Int32
    let appPath: String
    let executablePath: String
    let identifier: String
    let team: String?
    let codeHash: String?
    let adHoc: Bool?

    static func current() -> DesktopPermissionIdentity {
        var code: SecCode?
        var staticCode: SecStaticCode?
        var information: CFDictionary?
        var fields: [String: Any] = [:]
        if SecCodeCopySelf([], &code) == errSecSuccess, let code,
           SecCodeCopyStaticCode(code, [], &staticCode) == errSecSuccess, let staticCode,
           SecCodeCopySigningInformation(staticCode, SecCSFlags(rawValue: kSecCSSigningInformation), &information) == errSecSuccess {
            fields = information as? [String: Any] ?? [:]
        }
        let flags = fields[kSecCodeInfoFlags as String] as? NSNumber
        let hash = fields[kSecCodeInfoUnique as String] as? Data
        return DesktopPermissionIdentity(
            processID: ProcessInfo.processInfo.processIdentifier,
            appPath: Bundle.main.bundleURL.resolvingSymlinksInPath().path,
            executablePath: Bundle.main.executableURL?.resolvingSymlinksInPath().path ?? "",
            identifier: fields[kSecCodeInfoIdentifier as String] as? String ?? Bundle.main.bundleIdentifier ?? "",
            team: fields[kSecCodeInfoTeamIdentifier as String] as? String,
            codeHash: hash?.map { String(format: "%02x", $0) }.joined(),
            adHoc: flags.map { ($0.uint32Value & SecCodeSignatureFlags.adhoc.rawValue) != 0 }
        )
    }
}

// 屏幕录制/辅助功能的false只表示当前调用进程不可用，不等于用户没有打开开关。
enum DesktopPermissionPresentation {
    static func title(_ state: DesktopPermissionState, kind: DesktopPermissionKind) -> String {
        if state == .notGranted && (kind == .accessibility || kind == .screenRecording) {
            return L10n.text("Not active")
        }
        return state.title
    }
    static func coreState(_ report: DesktopCorePermissionReport?, kind: DesktopPermissionKind) -> DesktopPermissionState {
        guard let report, report.supported else { return .unavailable }
        switch kind {
        case .accessibility: return report.permissions.accessibility ? .granted : .notGranted
        case .screenRecording: return report.permissions.screen_recording ? .granted : .notGranted
        default: return .unavailable
        }
    }
    static func needsRecovery(_ snapshot: DesktopPermissionSnapshot, core: DesktopCorePermissionReport?) -> Bool {
        if snapshot[.accessibility] == .notGranted || snapshot[.screenRecording] == .notGranted { return true }
        guard let core, core.supported else { return false }
        return !core.permissions.accessibility || !core.permissions.screen_recording
    }
    static func recovery(adHoc: Bool?) -> String {
        let general = L10n.text("If the system switch is on but access is not active, quit and reopen the affected app or Core. After replacing a build, remove the old AgentDock entry and add this installed app again in the affected permission page. Recheck after reopening; enabling a switch does not prove this process can use it.")
        guard adHoc == true else { return general }
        return L10n.text("This is an ad-hoc signed build. Rebuilding changes its code identity, so macOS may reject a previous permission grant. Stable certificate signing is needed to preserve identity across updates.") + "\n\n" + general
    }
}

// 主菜单和权限页必须连接同一个Core，不能用另一个临时子进程替代它的权限。
struct DesktopPermissionClient {
    let runtimeRoot: URL
    init(runtimeRoot: URL? = nil) {
        self.runtimeRoot = runtimeRoot ?? ProcessInfo.processInfo.environment["AGENTDOCK_MONITOR_RUNTIME_ROOT"].map { URL(fileURLWithPath: $0) }
            ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/AgentDock")
    }
    func check(_ completion: @escaping @MainActor (Result<DesktopCorePermissionReport, Error>) -> Void) {
        DispatchQueue.global(qos: .utility).async {
            let transport = ComputerUseTransport(socketPath: runtimeRoot.appendingPathComponent("control.sock").path, controllerID: "")
            let result = Result { try transport.requestSync(method: "computeruse.permissions", params: [:]) as DesktopCorePermissionReport }
            RunLoop.main.perform(inModes: [.common, .eventTracking, .modalPanel]) {
                MainActor.assumeIsolated { completion(result) }
            }
        }
    }
}
