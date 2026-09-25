import AppKit
import Foundation

@MainActor final class DiagnosticsWindowController: NSWindowController, NSWindowDelegate {
    private let service: ServiceController
    private let client = LocalRuntimeClient()
    private let text = NSTextView()
    private let coreState = ManagementUI.label("—", size: 17, weight: .semibold)
    private let registrationState = ManagementUI.label("—", size: 17, weight: .semibold)
    private let publicState = ManagementUI.label(L10n.text("Not checked"), size: 17, weight: .semibold)
    private let refreshButton = NSButton(title: L10n.text("Refresh diagnostics"), target: nil, action: nil)
    private let publicButton = NSButton(title: L10n.text("Check public access"), target: nil, action: nil)
    private let saveButton = NSButton(title: L10n.text("Export safe report"), target: nil, action: nil)
    private var task: Task<Void, Never>?
    private var publicTask: Task<Void, Never>?
    private var displayedReport: String?
    private var publicReport = ""
    private var generation = 0

    init(service: ServiceController) {
        self.service = service
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 790, height: 600),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        super.init(window: window)
        window.title = L10n.text("Connection diagnostics"); window.minSize = NSSize(width: 690, height: 460)
        window.isReleasedWhenClosed = false; window.delegate = self
        window.setFrameAutosaveName("AgentDockDiagnostics")
        let content = NSView(); window.contentView = content
        let heading = ManagementUI.column([
            ManagementUI.label(L10n.text("Connection diagnostics"), size: 24, weight: .semibold),
            ManagementUI.label(L10n.text("Registration, local readiness and public access are checked separately."), secondary: true)
        ], spacing: 5)
        let metrics = NSStackView(views: [
            ManagementUI.card(ManagementUI.column([ManagementUI.label(L10n.text("Local Core"), secondary: true), coreState])),
            ManagementUI.card(ManagementUI.column([ManagementUI.label(L10n.text("Background service"), secondary: true), registrationState])),
            ManagementUI.card(ManagementUI.column([ManagementUI.label(L10n.text("Public access"), secondary: true), publicState]))
        ])
        metrics.distribution = .fillEqually; metrics.spacing = 12
        let scroll = ManagementUI.textScroll(text)
        text.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        let reportCard = ManagementUI.card(scroll)
        refreshButton.target = self; refreshButton.action = #selector(refresh)
        publicButton.target = self; publicButton.action = #selector(checkPublic)
        saveButton.target = self; saveButton.action = #selector(exportReport); saveButton.isEnabled = false
        let buttons = NSStackView(views: [refreshButton, publicButton, NSView(), saveButton]); buttons.spacing = 12
        for view in [heading, metrics, reportCard, buttons] { view.translatesAutoresizingMaskIntoConstraints = false; content.addSubview(view) }
        NSLayoutConstraint.activate([
            heading.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 24),
            heading.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -24),
            heading.topAnchor.constraint(equalTo: content.topAnchor, constant: 22),
            metrics.leadingAnchor.constraint(equalTo: heading.leadingAnchor), metrics.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            metrics.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: 20),
            reportCard.leadingAnchor.constraint(equalTo: heading.leadingAnchor), reportCard.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            reportCard.topAnchor.constraint(equalTo: metrics.bottomAnchor, constant: 18),
            reportCard.bottomAnchor.constraint(equalTo: buttons.topAnchor, constant: -16),
            buttons.leadingAnchor.constraint(equalTo: heading.leadingAnchor), buttons.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            buttons.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -20)
        ])
    }
    required init?(coder: NSCoder) { nil }
    func present() { showWindow(nil); window?.makeKeyAndOrderFront(nil); refresh() }
    func windowWillClose(_ notification: Notification) {
        generation += 1; task?.cancel(); publicTask?.cancel(); task = nil; publicTask = nil
        displayedReport = nil; saveButton.isEnabled = false
    }
    @objc private func refresh() {
        generation += 1; let current = generation
        task?.cancel(); publicTask?.cancel(); publicTask = nil; publicReport = ""
        publicState.stringValue = L10n.text("Not checked")
        publicButton.isEnabled = ServiceConfiguration.load(from: service.paths.environment)?.publicMCPURL != nil
        refreshButton.isEnabled = false; saveButton.isEnabled = false
        coreState.stringValue = L10n.text("Checking…")
        registrationState.stringValue = L10n.text("Checking…")
        task = Task { [weak self] in
            guard let self else { return }
            defer { if generation == current { refreshButton.isEnabled = true; task = nil } }
            let facts = await service.localDiagnosticFacts()
            guard !Task.isCancelled, generation == current else { return }
            registrationState.stringValue = L10n.text(facts["core_registration"] ?? "unknown")
            let snapshot = service.lifecycleJournal.snapshot()
            let formatter = DateFormatter(); formatter.dateFormat = "HH:mm:ss"
            var local = [L10n.text("Local system observation"), Date().formatted(), ""]
            for key in ["configuration", "core_registration", "core_process", "tunnel_registration", "tunnel_process"] {
                local.append("\(L10n.text(key)): \(L10n.text(facts[key] ?? "unknown"))")
            }
            let intent = (try? service.lifecycleJournal.desired(.core)).map { $0 ? L10n.text("Run") : L10n.text("Stopped by user") } ?? L10n.text("Unknown")
            local += ["\(L10n.text("Saved Core intent")): \(intent)", "", L10n.text("Recent startup timeline")]
            if snapshot.events.isEmpty { local.append(L10n.text("No startup events recorded yet.")) }
            for event in snapshot.events.suffix(24) {
                local.append("\(formatter.string(from: event.time))  \(event.service.rawValue) · \(L10n.text(event.code.message)) [\(event.code.rawValue); \(event.attempt)]")
            }
            let localReport = local.joined(separator: "\n")
            do {
                guard let configuration = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let data = try await client.get(configuration: configuration, path: "/internal/runtime/diagnostics")
                let diagnostics = try SafeDiagnostics.decode(data)
                guard !Task.isCancelled, generation == current else { return }
                coreState.stringValue = AppVersion.matchesHealthVersion(diagnostics.build.version) ? L10n.text("Ready") : L10n.text("Version mismatch")
                showReport(localReport + "\n\n" + diagnostics.displayText())
            } catch {
                guard !Task.isCancelled, generation == current else { return }
                coreState.stringValue = L10n.text("Unavailable")
                showReport(localReport + "\n\n" + L10n.text("Core diagnostics are unavailable. The system observation and saved startup timeline above remain available without Core. No raw logs or credentials were read."))
            }
        }
    }
    private func showReport(_ report: String) {
        displayedReport = report; text.string = report + publicReport; saveButton.isEnabled = true
    }
    @objc private func checkPublic() {
        guard publicTask == nil, let url = ServiceConfiguration.load(from: service.paths.environment)?.publicMCPURL else { return }
        let current = generation
        publicButton.isEnabled = false; publicState.stringValue = L10n.text("Checking…")
        publicTask = Task { [weak self] in
            guard let self else { return }
            let result = await PublicEndpointChecker().check(publicMCPURL: url)
            guard !Task.isCancelled, generation == current else { return }
            publicState.stringValue = result.isReachable ? L10n.text("Reachable") : L10n.text("Unavailable")
            // 只导出布尔结论与数字，不能把远端 HTML/URL 或系统原始错误写进安全报告。
            publicReport = "\n\n\(L10n.text("Public health observation")): \(publicState.stringValue)"
            if let ms = result.latencyMilliseconds { publicReport += " · \(ms) ms" }
            publicReport += "\n" + L10n.text("Public health does not prove MCP authentication, response delivery or ChatGPT rendering.")
            text.string = (displayedReport ?? "") + publicReport
            publicButton.isEnabled = true; publicTask = nil
        }
    }
    @objc private func exportReport() {
        guard let report = displayedReport, let window else { return }
        let snapshot = report + publicReport
        let panel = NSSavePanel(); panel.nameFieldStringValue = "AgentDock-diagnostics.txt"
        panel.beginSheetModal(for: window) { result in
            guard result == .OK, let destination = panel.url else { return }
            do { try Data(snapshot.utf8).write(to: destination, options: .atomic) }
            catch { let alert = NSAlert(error: error); alert.beginSheetModal(for: window) }
        }
    }
}
