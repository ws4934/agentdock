import AppKit
import Foundation

private final class DiagnosticSessionDelegate: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}

@MainActor
final class DiagnosticsWindowController: NSWindowController, NSWindowDelegate {
    private let service: ServiceController
    private let text = NSTextView()
    private let refreshButton = NSButton(title: L10n.text("Refresh diagnostics"), target: nil, action: nil)
    private let saveButton = NSButton(title: L10n.text("Export safe report"), target: nil, action: nil)
    private var task: Task<Void, Never>?
    private var displayedReport: String?
    private var generation = 0
    private let networkDelegate = DiagnosticSessionDelegate()
    private lazy var session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.connectionProxyDictionary = [:]
        return URLSession(configuration: configuration, delegate: networkDelegate, delegateQueue: nil)
    }()

    init(service: ServiceController) {
        self.service = service
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 620, height: 460),
                              styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        super.init(window: window)
        window.title = L10n.text("Connection diagnostics")
        window.isReleasedWhenClosed = false; window.delegate = self
        let content = NSView(); window.contentView = content
        let scroll = NSScrollView(); scroll.hasVerticalScroller = true; scroll.borderType = .noBorder
        text.isEditable = false; text.isSelectable = true; text.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        text.textContainerInset = NSSize(width: 12, height: 12); text.autoresizingMask = [.width]
        text.isVerticallyResizable = true; text.isHorizontallyResizable = false
        text.textContainer?.widthTracksTextView = true; scroll.documentView = text
        refreshButton.target = self; refreshButton.action = #selector(refresh)
        saveButton.target = self; saveButton.action = #selector(exportReport); saveButton.isEnabled = false
        let buttons = NSStackView(views: [refreshButton, saveButton]); buttons.spacing = 12
        for view in [scroll, buttons] { view.translatesAutoresizingMaskIntoConstraints = false; content.addSubview(view) }
        NSLayoutConstraint.activate([
            scroll.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 12),
            scroll.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -12),
            scroll.topAnchor.constraint(equalTo: content.topAnchor, constant: 12),
            scroll.bottomAnchor.constraint(equalTo: buttons.topAnchor, constant: -12),
            buttons.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 18),
            buttons.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -16)
        ])
    }
    required init?(coder: NSCoder) { nil }
    func present() { window?.center(); showWindow(nil); window?.makeKeyAndOrderFront(nil); refresh() }
    func windowWillClose(_ notification: Notification) {
        generation += 1; task?.cancel(); task = nil; displayedReport = nil; saveButton.isEnabled = false
    }
    @objc private func refresh() {
        generation += 1; let current = generation
        task?.cancel(); displayedReport = nil; saveButton.isEnabled = false
        refreshButton.isEnabled = false; text.string = L10n.text("Reading local diagnostics…")
        task = Task { [weak self] in
            guard let self else { return }
            defer { if generation == current { refreshButton.isEnabled = true; task = nil } }
            do {
                guard let configuration = ServiceConfiguration.load(from: service.paths.environment),
                      let local = configuration.localMCPURL,
                      var url = URLComponents(url: local, resolvingAgainstBaseURL: false),
                      ["localhost", "127.0.0.1", "::1", "[::1]"].contains(url.host ?? "") else { throw CocoaError(.fileReadUnknown) }
                url.path = "/internal/runtime/diagnostics"; url.query = nil; url.fragment = nil
                guard let endpoint = url.url else { throw CocoaError(.fileReadUnknown) }
                var request = URLRequest(url: endpoint, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 5)
                if !configuration.authToken.isEmpty { request.setValue("Bearer \(configuration.authToken)", forHTTPHeaderField: "Authorization") }
                let (data, response) = try await session.data(for: request)
                guard !Task.isCancelled, generation == current else { return }
                guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw CocoaError(.fileReadUnknown) }
                let report = try SafeDiagnostics.decode(data).displayText()
                text.string = report; displayedReport = report; saveButton.isEnabled = true
            } catch {
                if !Task.isCancelled && generation == current {
                    text.string = L10n.text("Local Core diagnostics are unavailable. Confirm the service is running. No logs, credentials or project files were read.")
                }
            }
        }
    }
    @objc private func exportReport() {
        guard let report = displayedReport, let window else { return }
        let panel = NSSavePanel(); panel.nameFieldStringValue = "AgentDock-diagnostics.txt"
        panel.beginSheetModal(for: window) { result in
            guard result == .OK, let destination = panel.url else { return }
            do { try Data(report.utf8).write(to: destination, options: .atomic) }
            catch { let alert = NSAlert(error: error); alert.beginSheetModal(for: window) }
        }
    }
}
