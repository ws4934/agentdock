import AppKit
import Foundation
import ServiceManagement

struct HealthPayload: Decodable {
    let ok: Bool
    let version: String
}

struct DesktopServiceStatusPayload: Decodable {
    let nexusConnected: Bool

    private enum CodingKeys: String, CodingKey {
        case nexusConnected = "nexus_connected"
    }
}

struct DesktopUpdateRegistrationState {
    let core: String
    let tunnel: String
}

enum NexusConnectionState: Equatable {
    case unconfigured
    case connected
    case disconnected
    case configurationError

    static func resolve(device: NexusDeviceStatus, connected: Bool) -> NexusConnectionState {
        if device.error != nil {
            return .configurationError
        }
        guard device.paired else {
            return .unconfigured
        }
        return connected ? .connected : .disconnected
    }
}

struct DesktopUpdateCheck: Decodable {
    let currentVersion: String?
    let latestVersion: String?
    let updateAvailable: Bool
    let message: String

    private enum CodingKeys: String, CodingKey {
        case currentVersion = "current_version"
        case latestVersion = "latest_version"
        case updateAvailable = "update_available"
        case message
    }

    static func decode(_ output: String) throws -> DesktopUpdateCheck {
        guard let data = output.data(using: .utf8) else {
            throw ValidationError(L10n.text("Unable to read the AgentDock update check result."))
        }
        do {
            return try JSONDecoder().decode(DesktopUpdateCheck.self, from: data)
        } catch {
            throw ValidationError(L10n.text("Unable to parse the AgentDock update check result."))
        }
    }
}

struct ServiceStatus {
    let installed: Bool
    let loaded: Bool
    let healthy: Bool
    let version: String?
    let configuration: ServiceConfiguration?
    let autostartEnabled: Bool
    let requiresApproval: Bool
    let migrationRequired: Bool
    let nexusConnection: NexusConnectionState

    static let missing = ServiceStatus(
        installed: false,
        loaded: false,
        healthy: false,
        version: nil,
        configuration: nil,
        autostartEnabled: false,
        requiresApproval: false,
        migrationRequired: false,
        nexusConnection: .unconfigured
    )
}

final class ServiceController: @unchecked Sendable {
    static let coreLabel = "com.uvwt.agentdock.core"
    static let tunnelLabel = "com.uvwt.agentdock.tunnel"
    static let corePlistName = "com.uvwt.agentdock.core.plist"
    static let tunnelPlistName = "com.uvwt.agentdock.tunnel.plist"

    let paths: AppPaths
    let lifecycleJournal: LifecycleJournal
    private let coreLifecycle: ManagedServiceLifecycle
    private let tunnelLifecycle: ManagedServiceLifecycle
    private let localNetwork = LocalRuntimeClient()

    init(paths: AppPaths = AppPaths()) {
        self.paths = paths
        let journal = LifecycleJournal(url: paths.appSupport.appendingPathComponent("service-lifecycle.json"))
        lifecycleJournal = journal
        coreLifecycle = ManagedServiceLifecycle(kind: .core, journal: journal)
        tunnelLifecycle = ManagedServiceLifecycle(kind: .tunnel, journal: journal)
    }

    func status() async -> ServiceStatus {
        let fileManager = FileManager.default
        let migrationRequired = LegacyDesktopRuntimeMigration.isPresent(paths: paths)
        let installed = fileManager.isExecutableFile(atPath: paths.binary.path)
            && fileManager.isExecutableFile(atPath: paths.cloudflared.path)
            && fileManager.fileExists(atPath: paths.coreSkillBundle.appendingPathComponent("manifest.json").path)
            && fileManager.fileExists(atPath: paths.environment.path)
        guard installed else { return .missing }

        let configuration = ServiceConfiguration.load(from: paths.environment)
        let nexusDevice = nexusDeviceStatus()
        let registration = coreService.status
        let requiresApproval = registration == .requiresApproval
        let enabled = registration == .enabled
        let registered = enabled || requiresApproval
        let loaded = enabled ? ((try? await runInBackground { self.isLoaded(label: Self.coreLabel) }) ?? false) : false
        let nexusConnected = loaded && nexusDevice.paired ? await fetchNexusConnected() : false

        guard loaded, let healthURL = configuration?.healthURL else {
            return ServiceStatus(
                installed: true,
                loaded: loaded,
                healthy: false,
                version: nil,
                configuration: configuration,
                autostartEnabled: registered,
                requiresApproval: requiresApproval,
                migrationRequired: migrationRequired,
                nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
            )
        }

        let health = await fetchHealth(url: healthURL)
        return ServiceStatus(
            installed: true,
            loaded: true,
            healthy: health?.ok == true,
            version: health?.version,
            configuration: configuration,
            autostartEnabled: registered,
            requiresApproval: requiresApproval,
            migrationRequired: migrationRequired,
            nexusConnection: .resolve(device: nexusDevice, connected: nexusConnected)
        )
    }

    func start(rememberIntent: Bool = true) async throws {
        try await coreLifecycle.start(lifecycleOperations(.core), rememberIntent: rememberIntent)
    }

    func stop(rememberIntent: Bool = true) async throws {
        try await coreLifecycle.stop(lifecycleOperations(.core), rememberIntent: rememberIntent)
    }

    func unregisterManagedBackgroundServicesForUninstall() throws {
        var failures: [String] = []
        do {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        } catch {
            failures.append("AgentDock Tunnel: \(error.localizedDescription)")
        }
        do {
            try unregister(service: coreService, label: Self.coreLabel)
        } catch {
            failures.append("AgentDock Core: \(error.localizedDescription)")
        }
        if !failures.isEmpty {
            throw ValidationError(failures.joined(separator: "\n"))
        }
    }

    func restart() async throws {
        try await coreLifecycle.start(lifecycleOperations(.core), restart: true)
    }

    func nexusDeviceStatus() -> NexusDeviceStatus {
        NexusDeviceStatus.load(from: paths.nexusDeviceIdentity)
    }

    func pairNexus(endpoint: String, pairingCode: String) async throws {
        let endpoint = endpoint.trimmingCharacters(in: .whitespacesAndNewlines)
        let pairingCode = pairingCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !endpoint.isEmpty, !pairingCode.isEmpty else {
            throw ValidationError(L10n.text("NexusDock address and one-time pairing code cannot be empty."))
        }
        let result = try await runInBackground {
            try runProcess(
                executable: self.paths.binary.path,
                arguments: ["nexus", "pair", "--endpoint", endpoint, "--code", pairingCode]
            )
        }
        guard result.status == 0 else {
            throw ValidationError(commandError(result.output, action: L10n.text("NexusDock pairing")))
        }
        try await restart()
    }

    func setAutostart(enabled: Bool) async throws {
        if enabled {
            try await start()
        } else {
            try await stop()
        }
    }

    func tunnelEnabled() -> Bool {
        let status = tunnelService.status
        return status == .enabled || status == .requiresApproval
    }

    // Validate only our non-secret pinned client configuration before changing
    // any running service. Official account/profile setup remains a user action.
    func validateSecureTunnelSetup() async throws {
        let configured = try? ManagedEnvironment.load(from: paths.environment).values["AGENTDOCK_HOME"]
        let stateHome = configured?.isEmpty == false ? configured! : paths.stateDirectory.path
        let result = try await runInBackground {
            try runProcess(executable: self.paths.binary.path,
                           arguments: ["secure-tunnel", "validate", "--home", stateHome])
        }
        guard result.status == 0 else {
            throw ValidationError(L10n.text("Secure MCP Tunnel is not configured or its client changed. Run agentdock secure-tunnel configure with an existing official profile before enabling this mode."))
        }
    }

    func configuredTunnelMode() throws -> TunnelMode {
        guard FileManager.default.fileExists(atPath: paths.tunnelEnvironment.path) else {
            return .local
        }
        let environment = try ManagedEnvironment.load(from: paths.tunnelEnvironment)
        let rawMode = environment.values["AGENTDOCK_TUNNEL_MODE"]?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased() ?? ""
        return TunnelMode(rawValue: rawMode) ?? .local
    }

    func reconcileTunnelRegistrationFromConfiguration() async throws {
        // 旧结构仍存在时必须先走迁移事务，不能在旁边提前注册第二套 Tunnel。
        guard !LegacyDesktopRuntimeMigration.isPresent(paths: paths) else { return }

        switch try configuredTunnelMode() {
        case .local:
            try await setTunnelEnabled(false, rememberIntent: false)
        case .quick, .named, .secure:
            try await setTunnelEnabled(lifecycleJournal.resolve(.tunnel, fallback: true), rememberIntent: false)
        }
    }

    func setTunnelEnabled(_ enabled: Bool, rememberIntent: Bool = true) async throws {
        if enabled {
            try await tunnelLifecycle.start(lifecycleOperations(.tunnel), rememberIntent: rememberIntent)
        } else {
            try await tunnelLifecycle.stop(lifecycleOperations(.tunnel), rememberIntent: rememberIntent)
        }
    }

    func restartTunnel() async throws {
        try await tunnelLifecycle.start(lifecycleOperations(.tunnel), restart: true)
    }

    func restoreBackgroundServiceRegistrations(coreEnabled: Bool, tunnelEnabled: Bool) throws {
        if try lifecycleJournal.resolve(.core, fallback: coreEnabled) {
            try restoreRegistration(service: coreService, label: Self.coreLabel, displayName: "AgentDock Core")
        } else {
            try unregister(service: coreService, label: Self.coreLabel)
        }
        if try lifecycleJournal.resolve(.tunnel, fallback: tunnelEnabled) {
            try restoreRegistration(service: tunnelService, label: Self.tunnelLabel, displayName: "AgentDock Tunnel")
        } else {
            try unregister(service: tunnelService, label: Self.tunnelLabel)
        }
    }

    func restoreBackgroundServiceRegistrationsForUpdate(
        coreEnabled: Bool,
        tunnelEnabled: Bool
    ) throws -> DesktopUpdateRegistrationState {
        let coreState = try restoreRegistrationForUpdate(
            service: coreService,
            label: Self.coreLabel,
            displayName: "AgentDock Core",
            expectedEnabled: lifecycleJournal.resolve(.core, fallback: coreEnabled)
        )
        let tunnelState: String
        do {
            tunnelState = try restoreRegistrationForUpdate(
                service: tunnelService,
                label: Self.tunnelLabel,
                displayName: "AgentDock Tunnel",
                expectedEnabled: lifecycleJournal.resolve(.tunnel, fallback: tunnelEnabled)
            )
        } catch {
            // Tunnel availability depends on ServiceManagement policy plus external/network state.
            // A broken Tunnel must not turn an otherwise healthy App/Core update into a rollback.
            // Report an explicit non-ready state to the Arbiter; it commits with a warning, then
            // AppDelegate's post-handoff reconciliation gets one more bounded recovery attempt.
            NSLog("AgentDock Tunnel registration could not be restored during update handoff: %@", error.localizedDescription)
            tunnelState = "unavailable"
        }
        return DesktopUpdateRegistrationState(core: coreState, tunnel: tunnelState)
    }

    func recoverBackgroundServicesAfterUpdate(coreEnabled: Bool, tunnelEnabled: Bool) async -> [String] {
        var warnings: [String] = []
        do {
            if try lifecycleJournal.resolve(.core, fallback: coreEnabled) { try await start(rememberIntent: false) }
            else { try await stop(rememberIntent: false) }
        } catch { warnings.append(error.localizedDescription) }
        do {
            try await setTunnelEnabled(lifecycleJournal.resolve(.tunnel, fallback: tunnelEnabled), rememberIntent: false)
        } catch { warnings.append(error.localizedDescription) }
        return warnings
    }

    func reregisterBackgroundServices(coreEnabled: Bool, tunnelEnabled: Bool) async throws -> [String] {
        try await runInBackground {
            try self.restoreBackgroundServiceRegistrations(coreEnabled: coreEnabled, tunnelEnabled: tunnelEnabled)
        }
        return await recoverBackgroundServicesAfterUpdate(coreEnabled: coreEnabled, tunnelEnabled: tunnelEnabled)
    }

    func openBackgroundItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }

    func waitForTunnelProcess(timeout: TimeInterval = 10) async -> Bool {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                continuation.resume(returning: self.waitForStableLaunchdProcess(
                    label: Self.tunnelLabel,
                    timeout: timeout
                ))
            }
        }
    }

    func isLoaded() -> Bool {
        isLoaded(label: Self.coreLabel)
    }

    func waitForHealth(configuration: ServiceConfiguration, timeout: TimeInterval = 30) async -> Bool {
        let deadline = ContinuousClock.now.advanced(by: .seconds(timeout))
        while !Task.isCancelled && ContinuousClock.now < deadline {
            let code = await coreReadiness(configuration: configuration)
            if code == .ready { return true }
            if code == .unexpectedHealth || code == .configurationInvalid { return false }
            do { try await Task.sleep(for: .milliseconds(250)) } catch { return false }
        }
        return false
    }

    func reconcileBackgroundServicesOnLaunch() async throws {
        guard !LegacyDesktopRuntimeMigration.isPresent(paths: paths),
              FileManager.default.fileExists(atPath: paths.environment.path) else { return }
        let desired = try lifecycleJournal.desired(.core)
        let enabled = try await runInBackground { self.coreService.status == .enabled }
        // 没有意图文件的旧安装只继承 enabled；不猜测已停止服务应当自动运行。
        guard desired ?? enabled else { return }
        try await start()
        try await reconcileTunnelRegistrationFromConfiguration()
    }

    func lifecycleOperationInProgress() async -> Bool {
        let core = await coreLifecycle.isOperating()
        let tunnel = await tunnelLifecycle.isOperating()
        return core || tunnel
    }

    // Core 离线时仍可观察系统注册/进程，不读取日志或返回环境变量。
    func localDiagnosticFacts() async -> [String: String] {
        (try? await runInBackground {
            func registration(_ service: SMAppService) -> String {
                switch service.status {
                case .enabled: return "enabled"
                case .requiresApproval: return "requires_approval"
                case .notRegistered, .notFound: return "not_registered"
                @unknown default: return "unknown"
                }
            }
            return [
                "configuration": ServiceConfiguration.load(from: self.paths.environment) == nil ? "invalid_or_missing" : "readable",
                "core_registration": registration(self.coreService),
                "core_process": self.launchdProcessID(label: Self.coreLabel) == nil ? "not_observed" : "running",
                "tunnel_registration": registration(self.tunnelService),
                "tunnel_process": self.launchdProcessID(label: Self.tunnelLabel) == nil ? "not_observed" : "running"
            ]
        }) ?? [:]
    }

    private func lifecycleOperations(_ kind: ManagedServiceKind) -> LifecycleOperations {
        let label = kind == .core ? Self.coreLabel : Self.tunnelLabel
        let plist = kind == .core ? Self.corePlistName : Self.tunnelPlistName
        let displayName = kind == .core ? "AgentDock Core" : "AgentDock Tunnel"
        return LifecycleOperations(
            validate: { [self] in
                try await runInBackground {
                    try self.validateServiceManagementReadiness()
                    try self.validateBundledServiceDefinition(plistName: plist, displayName: displayName)
                    guard let configuration = ServiceConfiguration.load(from: self.paths.environment) else {
                        throw LifecycleFailure(service: kind, code: .configurationInvalid)
                    }
                    _ = try LocalRuntimeClient.endpoint(configuration: configuration, path: "/healthz")
                    if kind == .tunnel { _ = try ManagedEnvironment.load(from: self.paths.tunnelEnvironment) }
                }
            },
            registration: { [self] in
                (try? await runInBackground {
                    let state = (kind == .core ? self.coreService : self.tunnelService).status
                    if state == .enabled { return ServiceRegistration.enabled }
                    if state == .requiresApproval { return ServiceRegistration.approvalRequired }
                    return ServiceRegistration.unregistered
                }) ?? .unregistered
            },
            register: { [self] in
                try await runInBackground {
                    try self.register(service: kind == .core ? self.coreService : self.tunnelService,
                                      plistName: plist, displayName: displayName)
                }
            },
            unregister: { [self] in
                try await runInBackground {
                    try self.unregister(service: kind == .core ? self.coreService : self.tunnelService, label: label)
                }
            },
            observe: { [self] in
                let pid = try? await runInBackground { self.launchdProcessID(label: label) }
                guard let pid else { return LifecycleObservation(code: .waitingProcess) }
                if kind == .tunnel { return LifecycleObservation(code: .processReady, processID: pid) }
                guard let configuration = ServiceConfiguration.load(from: paths.environment) else { return LifecycleObservation(code: .configurationInvalid) }
                return LifecycleObservation(code: await coreReadiness(configuration: configuration), processID: pid)
            }
        )
    }

    func update(onProgress: @escaping (UpdateProgressEvent) -> Void) async throws -> String {
        try validateServiceManagementReadiness()

        let check = try await runInBackground {
            let result = try runProcess(
                executable: self.paths.binary.path,
                arguments: ["update", "--check"],
                environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path]
            )
            guard result.status == 0 else {
                throw ValidationError(self.commandError(result.output, action: L10n.text("Check for updates")))
            }
            return try DesktopUpdateCheck.decode(result.output)
        }
        guard check.updateAvailable else {
            // 没有 pending update result 时这只能是上一次未完成流程留下的临时状态。
            DesktopUpdateServiceState.remove(at: paths.updateServiceState)
            onProgress(.local(
                type: .completed,
                currentVersion: check.currentVersion,
                targetVersion: check.latestVersion
            ))
            return check.message
        }

        let currentStatus = await status()
        let serviceState = DesktopUpdateServiceState(
            coreEnabled: currentStatus.autostartEnabled,
            tunnelEnabled: tunnelEnabled()
        )
        try serviceState.write(to: paths.updateServiceState)

        let output: String
        do {
            try await setTunnelEnabled(false, rememberIntent: false)
            try await stop(rememberIntent: false)
            output = try await runInBackground {
                let result = try runUpdateProcess(
                    executable: self.paths.binary.path,
                    arguments: ["update", "--progress-json"],
                    environment: ["AGENTDOCK_DESKTOP_APP_PATH": self.paths.appBundle.path],
                    outputURL: self.paths.updateLog,
                    onProgress: onProgress
                )
                guard result.status == 0 else {
                    throw ValidationError(self.commandError(result.output, action: L10n.text("Update")))
                }
                return result.output.trimmingCharacters(in: .whitespacesAndNewlines)
            }
        } catch {
            let updateError = error
            do {
                let recoveryWarnings = try await reregisterBackgroundServices(
                    coreEnabled: serviceState.coreEnabled,
                    tunnelEnabled: serviceState.tunnelEnabled
                )
                if !recoveryWarnings.isEmpty {
                    NSLog("AgentDock 更新失败后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
                }
                DesktopUpdateServiceState.remove(at: paths.updateServiceState)
            } catch {
                throw ValidationError(L10n.format(
                    "The update was not applied, and restoring background services also failed: %@; %@",
                    updateError.localizedDescription,
                    error.localizedDescription
                ))
            }
            throw updateError
        }

        // 真正的 App 替换会终止旧 GUI，并由新版 App 根据 update-result.json 恢复服务。
        // 能执行到这里说明更新进程正常返回但没有完成 GUI handoff，因此旧 GUI 必须自己收尾。
        do {
            let recoveryWarnings = try await reregisterBackgroundServices(
                coreEnabled: serviceState.coreEnabled,
                tunnelEnabled: serviceState.tunnelEnabled
            )
            if !recoveryWarnings.isEmpty {
                NSLog("AgentDock 更新返回后后台服务仍在启动：%@", recoveryWarnings.joined(separator: "；"))
            }
            DesktopUpdateServiceState.remove(at: paths.updateServiceState)
        } catch {
            throw ValidationError(L10n.format(
                "The update process returned, but restoring background services failed: %@",
                error.localizedDescription
            ))
        }
        return output
    }

    func openLogs() {
        try? FileManager.default.createDirectory(at: paths.logs, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.logs)
    }

    func openConfiguration() {
        try? FileManager.default.createDirectory(at: paths.appSupport, withIntermediateDirectories: true)
        NSWorkspace.shared.open(paths.appSupport)
    }

    private var coreService: SMAppService {
        SMAppService.agent(plistName: Self.corePlistName)
    }

    private var tunnelService: SMAppService {
        SMAppService.agent(plistName: Self.tunnelPlistName)
    }

    private func register(service: SMAppService, plistName: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        switch service.status {
        case .enabled:
            return
        case .requiresApproval:
            throw ValidationError(L10n.format(
                "%@ is registered, but you need to allow it to run in the background in System Settings → General → Login Items & Extensions.",
                displayName
            ))
        case .notRegistered, .notFound:
            // SMAppService 在服务首次 register 前可能返回 .notFound，即使 Bundle 内 plist
            // 已经存在。定义是否完整由上面的 Bundle 文件校验负责，不用 status 猜测。
            try service.register()
        @unknown default:
            throw ValidationError(L10n.format("Unable to determine background service status for %@.", displayName))
        }
        if service.status == .requiresApproval {
            throw ValidationError(L10n.format(
                "%@ requires permission to run in the background in System Settings → General → Login Items & Extensions.",
                displayName
            ))
        }
        // 首次注册状态可能尚未传播；由统一协调器有界等待，不立即误报失败。
    }

    private func unregister(service: SMAppService, label: String) throws {
        switch service.status {
        case .notRegistered, .notFound:
            return
        case .enabled, .requiresApproval:
            try service.unregister()
            guard waitUntilUnregistered(service: service, label: label, timeout: 5) else {
                throw ValidationError(L10n.format(
                    "Background service %@ did not converge to the unregistered state.",
                    label
                ))
            }
        @unknown default:
            return
        }
    }

    private func restoreRegistration(service: SMAppService, label: String, displayName: String) throws {
        try validateServiceManagementReadiness()
        let plistName = label == Self.coreLabel ? Self.corePlistName : Self.tunnelPlistName
        try validateBundledServiceDefinition(plistName: plistName, displayName: displayName)
        try unregister(service: service, label: label)
        try register(service: service, plistName: plistName, displayName: displayName)
    }

    private func restoreRegistrationForUpdate(
        service: SMAppService,
        label: String,
        displayName: String,
        expectedEnabled: Bool
    ) throws -> String {
        guard expectedEnabled else {
            try unregister(service: service, label: label)
            return "disabled"
        }
        do {
            try restoreRegistration(service: service, label: label, displayName: displayName)
        } catch {
            // requiresApproval reflects user/system policy. It is a commit warning, not evidence
            // that the newly installed App Bundle is invalid.
            guard service.status == .requiresApproval else { throw error }
        }
        let deadline = ContinuousClock.now.advanced(by: .seconds(5))
        while Self.isUnregistered(service.status), ContinuousClock.now < deadline {
            Thread.sleep(forTimeInterval: 0.1)
        }
        switch service.status {
        case .enabled:
            return "enabled"
        case .requiresApproval:
            return "requires_approval"
        case .notRegistered, .notFound:
            throw ValidationError(L10n.format(
                "%@ background registration did not become available after the update.",
                displayName
            ))
        @unknown default:
            throw ValidationError(L10n.format(
                "%@ background registration returned an unknown state after the update.",
                displayName
            ))
        }
    }

    private var serviceDomain: String { "gui/\(getuid())" }

    func validatePersistentAppLocation() throws {
        let path = paths.appBundle.resolvingSymlinksInPath().path
        if path == "/Volumes" || path.hasPrefix("/Volumes/") {
            throw ValidationError(L10n.text("Move AgentDock to the Applications folder before enabling the background service."))
        }
    }

    func validateServiceManagementReadiness() throws {
        try validatePersistentAppLocation()
        if LegacyDesktopRuntimeMigration.isPresent(paths: paths) {
            throw ValidationError(L10n.text("A legacy AgentDock background layout was detected. Apply the current settings in the main panel to complete migration first."))
        }
    }

    func validateBundledServiceDefinition(plistName: String, displayName: String) throws {
        let plist = paths.appBundle
            .appendingPathComponent("Contents/Library/LaunchAgents", isDirectory: true)
            .appendingPathComponent(plistName)
        guard let values = try? plist.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey]),
              values.isRegularFile == true,
              values.isSymbolicLink != true else {
            throw ValidationError(L10n.format(
                "AgentDock.app is missing the background service definition for %@. Reinstall the application.",
                displayName
            ))
        }
    }

    private func isLoaded(label: String) -> Bool {
        (try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ).status) == 0
    }

    private func launchdProcessID(label: String) -> Int? {
        guard let result = try? runProcess(
            executable: "/bin/launchctl",
            arguments: ["print", "\(serviceDomain)/\(label)"]
        ), result.status == 0 else { return nil }
        for rawLine in result.output.split(whereSeparator: \.isNewline) {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            guard line.hasPrefix("pid = "),
                  let pid = Int(line.dropFirst("pid = ".count)),
                  pid > 0 else { continue }
            return pid
        }
        return nil
    }

    private func waitForStableLaunchdProcess(label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var previousPID: Int?
        var stableChecks = 0
        while Date() < deadline {
            if let pid = launchdProcessID(label: label) {
                if pid == previousPID {
                    stableChecks += 1
                } else {
                    previousPID = pid
                    stableChecks = 1
                }
                if stableChecks >= 4 { return true }
            } else {
                previousPID = nil
                stableChecks = 0
            }
            Thread.sleep(forTimeInterval: 0.25)
        }
        return false
    }

    private func waitUntilUnregistered(service: SMAppService, label: String, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !isLoaded(label: label), Self.isUnregistered(service.status) { return true }
            Thread.sleep(forTimeInterval: 0.1)
        }
        return !isLoaded(label: label) && Self.isUnregistered(service.status)
    }

    static func isUnregistered(_ status: SMAppService.Status) -> Bool {
        status == .notRegistered || status == .notFound
    }

    private func coreReadiness(configuration: ServiceConfiguration) async -> LifecycleCode {
        do {
            let data = try await localNetwork.get(configuration: configuration, path: "/healthz", timeout: 1.5)
            guard let payload = try? JSONDecoder().decode(HealthPayload.self, from: data),
                  payload.ok else { return .unexpectedHealth }
            guard AppVersion.matchesHealthVersion(payload.version) else { return .versionMismatch }
            return .ready
        } catch let error as URLError {
            return error.code == .cannotConnectToHost ? .connectionRefused : .healthTimeout
        } catch LocalRuntimeError.invalidEndpoint { return .configurationInvalid }
        catch { return .unexpectedHealth }
    }

    private func fetchNexusConnected() async -> Bool {
        do {
            let result = try await runInBackground {
                try runProcess(
                    executable: self.paths.binary.path,
                    arguments: ["service", "status", "--runtime-root", self.paths.appSupport.path]
                )
            }
            guard result.status == 0,
                  let data = result.output.data(using: .utf8),
                  let payload = try? JSONDecoder().decode(DesktopServiceStatusPayload.self, from: data) else {
                return false
            }
            return payload.nexusConnected
        } catch {
            return false
        }
    }

    private func fetchHealth(url: URL) async -> HealthPayload? {
        do {
            guard let configuration = ServiceConfiguration.load(from: paths.environment),
                  configuration.healthURL == url else { return nil }
            let data = try await localNetwork.get(configuration: configuration, path: "/healthz", timeout: 2.5)
            return try JSONDecoder().decode(HealthPayload.self, from: data)
        } catch {
            return nil
        }
    }

    private func commandError(_ output: String, action: String) -> String {
        let message = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return message.isEmpty ? L10n.format("AgentDock %@ failed.", action) : message
    }

    func runInBackground<T>(_ operation: @escaping () throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                do { continuation.resume(returning: try operation()) }
                catch { continuation.resume(throwing: error) }
            }
        }
    }
}
