import Foundation

// 注册、进程和健康是不同事实；时间线只保存枚举，绝不收集原始错误或配置。
enum ManagedServiceKind: String, Codable, Sendable { case core, tunnel }
enum ServiceRegistration: Equatable, Sendable { case enabled, approvalRequired, unregistered }
enum LifecycleCode: String, Codable, Sendable {
    case validating, registering, waitingRegistration = "waiting_registration"
    case waitingProcess = "waiting_process", connectionRefused = "connection_refused"
    case healthTimeout = "health_timeout", unexpectedHealth = "unexpected_health"
    case versionMismatch = "version_mismatch"
    case ready, processReady = "process_ready", repairing, stopping, stopped
    case approvalRequired = "approval_required", configurationInvalid = "configuration_invalid"
    case registrationFailed = "registration_failed", stopFailed = "stop_failed"
    case stateInvalid = "state_invalid", busy, cancelled

    var message: String {
        switch self {
        case .validating: return "Checking service configuration"
        case .registering: return "Registering the background service"
        case .waitingRegistration: return "Waiting for system registration"
        case .waitingProcess: return "Waiting for the background process"
        case .connectionRefused: return "Local port is not accepting connections"
        case .healthTimeout: return "Local health check timed out"
        case .unexpectedHealth: return "Unexpected local health response; check port conflicts"
        case .versionMismatch: return "Core version differs from the installed application"
        case .ready: return "Local Core is ready"
        case .processReady: return "Tunnel process is running; public access is not yet proven"
        case .repairing: return "Repairing stale registration once"
        case .stopping: return "Stopping the background service"
        case .stopped: return "Service is stopped"
        case .approvalRequired: return "Allow the background service in System Settings"
        case .configurationInvalid: return "Configuration is invalid; review connection settings"
        case .registrationFailed: return "Background registration failed; open diagnostics"
        case .stopFailed: return "Background service did not stop; open diagnostics"
        case .stateInvalid: return "Service intent could not be read or saved; open diagnostics"
        case .busy: return "A service operation is already in progress"
        case .cancelled: return "Service operation was cancelled"
        }
    }
}

struct LifecycleFailure: LocalizedError, Sendable {
    let service: ManagedServiceKind
    let code: LifecycleCode
    var errorDescription: String? { "\(service.rawValue): \(L10n.text(code.message)) [\(code.rawValue)]" }
}

struct LifecycleEvent: Codable, Sendable {
    let time: Date
    let service: ManagedServiceKind
    let code: LifecycleCode
    let attempt: Int
}
struct LifecycleSnapshot: Codable, Sendable {
    var schemaVersion = 1
    var desiredCore: Bool?
    var desiredTunnel: Bool?
    var events: [LifecycleEvent] = []
}

final class LifecycleJournal: @unchecked Sendable {
    private let lock = NSLock()
    private let url: URL
    private var value = LifecycleSnapshot()
    private var invalid = false

    init(url: URL) {
        self.url = url
        guard FileManager.default.fileExists(atPath: url.path) else { return }
        do {
            let info = try url.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey, .fileSizeKey])
            guard info.isRegularFile == true, info.isSymbolicLink != true,
                  (info.fileSize ?? Int.max) <= 64 * 1024 else { throw CocoaError(.fileReadCorruptFile) }
            let data = try Data(contentsOf: url)
            let loaded = try JSONDecoder().decode(LifecycleSnapshot.self, from: data)
            guard data.count <= 64 * 1024, loaded.schemaVersion == 1, loaded.events.count <= 64,
                  loaded.events.allSatisfy({ (0...2).contains($0.attempt) }) else { throw CocoaError(.fileReadCorruptFile) }
            value = loaded
        } catch { invalid = true }
    }

    func snapshot() -> LifecycleSnapshot {
        lock.lock(); defer { lock.unlock() }
        return value
    }
    func desired(_ kind: ManagedServiceKind) throws -> Bool? {
        lock.lock(); defer { lock.unlock() }
        guard !invalid else { throw LifecycleFailure(service: kind, code: .stateInvalid) }
        return kind == .core ? value.desiredCore : value.desiredTunnel
    }
    func resolve(_ kind: ManagedServiceKind, fallback: Bool) throws -> Bool {
        try desired(kind) ?? fallback
    }
    func setDesired(_ running: Bool, for kind: ManagedServiceKind) throws {
        lock.lock(); defer { lock.unlock() }
        // 不静默覆盖损坏文件，避免把未知的用户停止状态解释成允许运行。
        guard !invalid else { throw LifecycleFailure(service: kind, code: .stateInvalid) }
        var next = value
        if kind == .core { next.desiredCore = running } else { next.desiredTunnel = running }
        do { try save(next); value = next }
        catch { throw LifecycleFailure(service: kind, code: .stateInvalid) }
    }
    func record(_ kind: ManagedServiceKind, _ code: LifecycleCode, attempt: Int = 0) {
        lock.lock(); defer { lock.unlock() }
        if let last = value.events.last, last.service == kind, last.code == code, last.attempt == attempt { return }
        value.events.append(LifecycleEvent(time: Date(), service: kind, code: code, attempt: attempt))
        value.events = Array(value.events.suffix(64))
        if !invalid { try? save(value) }
    }
    func setIntents(core: Bool?, tunnel: Bool?) throws {
        lock.lock(); defer { lock.unlock() }
        guard !invalid else { throw LifecycleFailure(service: .core, code: .stateInvalid) }
        var next = value; next.desiredCore = core; next.desiredTunnel = tunnel
        do { try save(next); value = next }
        catch { throw LifecycleFailure(service: .core, code: .stateInvalid) }
    }
    private func save(_ next: LifecycleSnapshot) throws {
        let directory = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                                               attributes: [.posixPermissions: 0o700])
        let directoryInfo = try directory.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
        guard directoryInfo.isDirectory == true, directoryInfo.isSymbolicLink != true else { throw CocoaError(.fileWriteNoPermission) }
        if FileManager.default.fileExists(atPath: url.path) {
            let info = try url.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
            guard info.isRegularFile == true, info.isSymbolicLink != true else { throw CocoaError(.fileWriteNoPermission) }
        }
        let data = try JSONEncoder().encode(next)
        guard data.count <= 64 * 1024 else { throw CocoaError(.fileWriteOutOfSpace) }
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }
}

struct LifecycleObservation: Sendable {
    let code: LifecycleCode
    var processID: Int? = nil
}

struct LifecycleOperations: Sendable {
    var validate: @Sendable () async throws -> Void
    var registration: @Sendable () async -> ServiceRegistration
    var register: @Sendable () async throws -> Void
    var unregister: @Sendable () async throws -> Void
    // ready 对 Tunnel 只表示宿主进程存在，不证明云端握手或公网就绪。
    var observe: @Sendable () async -> LifecycleObservation
}

// Actor 在 await 处会重入，所以必须有显式互斥门。冲突请求不排队重放。
actor ManagedServiceLifecycle {
    let kind: ManagedServiceKind
    let journal: LifecycleJournal
    private var operating = false
    private let attemptTimeout: Duration
    private let pollInterval: Duration
    init(kind: ManagedServiceKind, journal: LifecycleJournal,
         attemptTimeout: Duration = .seconds(15), pollInterval: Duration = .milliseconds(250)) {
        self.kind = kind; self.journal = journal
        self.attemptTimeout = attemptTimeout; self.pollInterval = pollInterval
    }
    func isOperating() -> Bool { operating }

    func start(_ operations: LifecycleOperations, restart: Bool = false, rememberIntent: Bool = true) async throws {
        guard !operating else { throw LifecycleFailure(service: kind, code: .busy) }
        operating = true; defer { operating = false }
        do {
            try Task.checkCancellation()
            journal.record(kind, .validating)
            do { try await operations.validate() }
            catch is CancellationError { throw CancellationError() }
            catch { throw LifecycleFailure(service: kind, code: .configurationInvalid) }
            if rememberIntent { try journal.setDesired(true, for: kind) }
            if await operations.registration() == .approvalRequired {
                throw LifecycleFailure(service: kind, code: .approvalRequired)
            }
            for attempt in 1...2 {
                try Task.checkCancellation()
                if restart || attempt == 2 {
                    journal.record(kind, .repairing, attempt: attempt)
                    do { try await operations.unregister() }
                    catch is CancellationError { throw CancellationError() }
                    catch { throw LifecycleFailure(service: kind, code: .stopFailed) }
                }
                if await operations.registration() != .enabled {
                    journal.record(kind, .registering, attempt: attempt)
                    do { try await operations.register() }
                    catch is CancellationError { throw CancellationError() }
                    catch {
                        let code: LifecycleCode = await operations.registration() == .approvalRequired ? .approvalRequired : .registrationFailed
                        throw LifecycleFailure(service: kind, code: code)
                    }
                }
                let deadline = ContinuousClock.now.advanced(by: attemptTimeout)
                var last: LifecycleCode = .waitingProcess
                var stable = 0
                var previousPID: Int?
                repeat {
                    try Task.checkCancellation()
                    let registration = await operations.registration()
                    if registration == .approvalRequired { throw LifecycleFailure(service: kind, code: .approvalRequired) }
                    let observation = registration == .enabled ? await operations.observe() : LifecycleObservation(code: .waitingRegistration)
                    try Task.checkCancellation()
                    last = observation.code
                    if last == .unexpectedHealth || last == .configurationInvalid {
                        throw LifecycleFailure(service: kind, code: last)
                    }
                    if last == .ready || last == .processReady {
                        stable = observation.processID == previousPID ? stable + 1 : 1
                        previousPID = observation.processID
                        if stable >= (kind == .tunnel ? 4 : 1) {
                            journal.record(kind, kind == .core ? .ready : .processReady, attempt: attempt)
                            return
                        }
                    } else { stable = 0; previousPID = nil; journal.record(kind, last, attempt: attempt) }
                    // 合法但过期的 Core 一次重新注册即可更新；不等待满超时。
                    if last == .versionMismatch { break }
                    try await Task.sleep(for: pollInterval)
                } while ContinuousClock.now < deadline
                if attempt == 2 {
                    // PID 持续变动不算就绪，失败记录不得携带 process_ready 成功码。
                    throw LifecycleFailure(service: kind, code: last == .processReady ? .waitingProcess : last)
                }
            }
        } catch is CancellationError {
            journal.record(kind, .cancelled)
            throw CancellationError()
        } catch {
            let failure = error as? LifecycleFailure ?? LifecycleFailure(service: kind, code: .registrationFailed)
            journal.record(kind, failure.code)
            throw failure
        }
    }

    func stop(_ operations: LifecycleOperations, rememberIntent: Bool = true) async throws {
        guard !operating else { throw LifecycleFailure(service: kind, code: .busy) }
        operating = true; defer { operating = false }
        try Task.checkCancellation()
        if rememberIntent { try journal.setDesired(false, for: kind) }
        journal.record(kind, .stopping)
        do { try await operations.unregister(); journal.record(kind, .stopped) }
        catch is CancellationError { journal.record(kind, .cancelled); throw CancellationError() }
        catch { journal.record(kind, .stopFailed); throw LifecycleFailure(service: kind, code: .stopFailed) }
    }
}
