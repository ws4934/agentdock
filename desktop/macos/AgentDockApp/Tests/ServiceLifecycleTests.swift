import Foundation

private actor Driver {
    var registration: ServiceRegistration = .enabled
    var code: LifecycleCode = .waitingProcess
    var repairedCode: LifecycleCode = .ready
    var invalid = false
    var delayedRegistration = 0
    var registrations = 0
    var removals = 0
    var observations = 0
    var changingPID = false
    func configure(code: LifecycleCode = .waitingProcess, repaired: LifecycleCode = .ready,
                   registration: ServiceRegistration = .enabled, invalid: Bool = false,
                   delayed: Int = 0, changingPID: Bool = false) {
        self.code = code; repairedCode = repaired; self.registration = registration
        self.invalid = invalid; delayedRegistration = delayed; self.changingPID = changingPID
    }
    func validate() throws { if invalid { throw CocoaError(.fileReadCorruptFile) } }
    func state() -> ServiceRegistration {
        if registrations > 0 && delayedRegistration > 0 {
            delayedRegistration -= 1; return .unregistered
        }
        return registration
    }
    func register() { registrations += 1; registration = .enabled; code = repairedCode }
    func unregister() { removals += 1; registration = .unregistered }
    func observe() -> LifecycleObservation {
        observations += 1
        return LifecycleObservation(code: code, processID: changingPID ? observations : 42)
    }
    func counts() -> (Int, Int, Int) { (registrations, removals, observations) }
    nonisolated var operations: LifecycleOperations {
        LifecycleOperations(validate: { try await self.validate() }, registration: { await self.state() },
                            register: { await self.register() }, unregister: { await self.unregister() },
                            observe: { await self.observe() })
    }
}

@main struct ServiceLifecycleTests {
    static func main() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("AgentDockLifecycle-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        func make(_ name: String, kind: ManagedServiceKind = .core, timeout: Duration = .milliseconds(300)) -> (ManagedServiceLifecycle, LifecycleJournal, Driver) {
            let journal = LifecycleJournal(url: root.appendingPathComponent(name + ".json"))
            return (ManagedServiceLifecycle(kind: kind, journal: journal, attemptTimeout: timeout, pollInterval: .milliseconds(5)), journal, Driver())
        }
        let (stale, journal, driver) = make("stale")
        try await stale.start(driver.operations)
        let counts = await driver.counts()
        precondition(counts.0 == 1 && counts.1 == 1)
        precondition(journal.snapshot().events.last?.code == .ready)
        let persisted = LifecycleJournal(url: root.appendingPathComponent("stale.json"))
        let desired = try persisted.desired(.core)
        precondition(desired == true)
        try await stale.stop(driver.operations)
        let stopped = try journal.desired(.core); precondition(stopped == false)
        try await stale.start(driver.operations, rememberIntent: false)
        let retained = try journal.desired(.core); precondition(retained == false)
        let stopWins = try journal.resolve(.core, fallback: true); precondition(!stopWins)
        try journal.setDesired(true, for: .core)
        let runWins = try journal.resolve(.core, fallback: false); precondition(runWins)
        try journal.setIntents(core: nil, tunnel: nil)
        let legacyEnabled = try journal.resolve(.core, fallback: true); precondition(legacyEnabled)
        let legacyStopped = try journal.resolve(.core, fallback: false); precondition(!legacyStopped)
        print("PASS stale registration repair, durable stop intent and transient starts")

        for (name, code, expected) in [("malformed", LifecycleCode.unexpectedHealth, LifecycleCode.unexpectedHealth),
                                      ("invalid", LifecycleCode.waitingProcess, LifecycleCode.configurationInvalid),
                                      ("approval", LifecycleCode.waitingProcess, LifecycleCode.approvalRequired)] {
            let (service, _, driver) = make(name)
            await driver.configure(code: code, registration: name == "approval" ? .approvalRequired : .enabled, invalid: name == "invalid")
            do { try await service.start(driver.operations); preconditionFailure("must fail \(name)") }
            catch let failure as LifecycleFailure { precondition(failure.code == expected) }
            let counts = await driver.counts(); precondition(counts.0 == 0 && counts.1 == 0)
            print("PASS no blind retry for \(name)")
        }
        let (version, _, old) = make("version")
        await old.configure(code: .versionMismatch)
        try await version.start(old.operations)
        let versionCounts = await old.counts(); precondition(versionCounts.0 == 1 && versionCounts.1 == 1)
        print("PASS version mismatch repairs once")

        // 本用例检查异步传播而非超时；给真实原子落盘留出调度窗口。
        // 下面 broken/rotating 用例继续用 300ms 验证重试上界。
        let (delayed, _, first) = make("delayed", timeout: .seconds(5))
        await first.configure(registration: .unregistered, delayed: 3)
        try await delayed.start(first.operations)
        let delayedCounts = await first.counts(); precondition(delayedCounts.0 == 1 && delayedCounts.1 == 0, "unexpected propagation repair: \(delayedCounts)")
        print("PASS asynchronous registration propagation")

        let (broken, brokenJournal, dead) = make("broken")
        await dead.configure(repaired: .connectionRefused)
        do { try await broken.start(dead.operations); preconditionFailure("must fail bounded recovery") }
        catch let failure as LifecycleFailure { precondition(failure.code == .connectionRefused) }
        let brokenCounts = await dead.counts(); precondition(brokenCounts.0 == 1 && brokenCounts.1 == 1)
        precondition(brokenJournal.snapshot().events.count < 20)
        print("PASS bounded recovery and deduplicated diagnostics")

        let (tunnel, _, rotating) = make("rotating", kind: .tunnel)
        await rotating.configure(code: .processReady, repaired: .processReady, changingPID: true)
        do { try await tunnel.start(rotating.operations); preconditionFailure("crash loop is not stable") }
        catch is LifecycleFailure {}
        let (stableTunnel, _, stableDriver) = make("stable-tunnel", kind: .tunnel)
        await stableDriver.configure(code: .processReady)
        try await stableTunnel.start(stableDriver.operations)
        let stableCounts = await stableDriver.counts(); precondition(stableCounts.2 >= 4 && stableCounts.1 == 0)
        print("PASS tunnel PID stability without claiming cloud readiness")

        let (busy, _, slow) = make("busy")
        let running = Task { try await busy.start(slow.operations) }
        while !(await busy.isOperating()) { await Task.yield() }
        do { try await busy.start(slow.operations); preconditionFailure("duplicate accepted") }
        catch let failure as LifecycleFailure { precondition(failure.code == .busy) }
        running.cancel()
        do { try await running.value; preconditionFailure("cancel ignored") } catch is CancellationError {}
        let busyState = await busy.isOperating(); precondition(!busyState)
        print("PASS concurrent operation rejection and cancellation")

        let corruptURL = root.appendingPathComponent("corrupt.json")
        try Data("not-json".utf8).write(to: corruptURL)
        let corrupt = LifecycleJournal(url: corruptURL)
        do { _ = try corrupt.desired(.core); preconditionFailure("corrupt intent accepted") }
        catch let failure as LifecycleFailure { precondition(failure.code == .stateInvalid) }
        let linkURL = root.appendingPathComponent("link.json")
        try FileManager.default.createSymbolicLink(at: linkURL, withDestinationURL: corruptURL)
        do { try LifecycleJournal(url: linkURL).setDesired(true, for: .core); preconditionFailure("symlink accepted") }
        catch is LifecycleFailure {}
        for index in 0..<200 { journal.record(.core, index % 2 == 0 ? .ready : .stopped) }
        precondition(journal.snapshot().events.count == 64)
        print("PASS corrupt/symlink state rejected, bounded journal")
        print("Service lifecycle tests passed (11 scenarios)")
    }
}
