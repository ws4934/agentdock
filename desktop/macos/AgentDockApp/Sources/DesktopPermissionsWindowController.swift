import AppKit
import Foundation

@MainActor
final class DesktopPermissionsWindowController: NSWindowController, NSWindowDelegate {
    private let contentStack = TopAlignedStackView()
    private var statusLabels: [DesktopPermissionKind: NSTextField] = [:]
    private var coreLabels: [DesktopPermissionKind: NSTextField] = [:]
    private let locateCoreButton = NSButton(title: L10n.text("Locate Core"), target: nil, action: nil)
    private let coreIdentityLabel = PermissionUI.detailLabel("")
    private let recoveryLabel = PermissionUI.detailLabel("")
    private let identity = DesktopPermissionIdentity.current()
    private let snapshotProvider: () -> DesktopPermissionSnapshot
    private let coreCheck: (@escaping @MainActor (Result<DesktopCorePermissionReport, Error>) -> Void) -> Void
    private var latestSnapshot = DesktopPermissionSnapshot(states: [:])
    private var timer: Timer?
    private var activationObserver: NSObjectProtocol?
    private var checkInFlight = false
    private var refreshGeneration = 0
    private var coreReport: DesktopCorePermissionReport?
    private lazy var fileAccessWindow = FileAccessPermissionsWindowController()

    init(snapshotProvider: @escaping () -> DesktopPermissionSnapshot = DesktopPermissionChecker.snapshot,
         coreCheck: @escaping (@escaping @MainActor (Result<DesktopCorePermissionReport, Error>) -> Void) -> Void = { DesktopPermissionClient().check($0) }) {
        self.snapshotProvider = snapshotProvider
        self.coreCheck = coreCheck
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 840, height: 750),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = L10n.text("AgentDock Permission Check")
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 810, height: 560)
        window.center()
        super.init(window: window)
        window.delegate = self
        configureUI()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func present() {
        startMonitoring()
        showWindow(nil)
        refresh()
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func configureUI() {
        guard let contentView = window?.contentView else { return }

        let scrollView = NSScrollView()
        scrollView.hasVerticalScroller = true
        scrollView.autohidesScrollers = true
        scrollView.drawsBackground = false
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        contentView.addSubview(scrollView)

        contentStack.orientation = .vertical
        contentStack.alignment = .leading
        contentStack.spacing = 12
        contentStack.edgeInsets = NSEdgeInsets(top: 22, left: 26, bottom: 22, right: 26)
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        scrollView.documentView = contentStack

        let title = NSTextField(labelWithString: L10n.text("System permissions"))
        title.font = .systemFont(ofSize: 22, weight: .semibold)
        let intro = PermissionUI.detailLabel(
            L10n.text("This page reports access for the menu app and running Core separately, not the switches in System Settings. It refreshes while open and when you return from Settings. Grant only the permissions you need.")
        )
        intro.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(title)
        contentStack.addArrangedSubview(intro)
        contentStack.addArrangedSubview(PermissionUI.separator())

        let appIdentity = PermissionUI.detailLabel(L10n.text("Menu app · current process") + " · PID " + String(identity.processID) + "\n" + identity.appPath)
        appIdentity.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(appIdentity)
        recoveryLabel.widthAnchor.constraint(equalToConstant: 750).isActive = true
        recoveryLabel.textColor = .secondaryLabelColor
        contentStack.addArrangedSubview(recoveryLabel)

        for kind in DesktopPermissionKind.allCases {
            contentStack.addArrangedSubview(makePermissionRow(kind))
            contentStack.addArrangedSubview(PermissionUI.separator())
        }

        let coreTitle = NSTextField(labelWithString: L10n.text("Core · automation process"))
        coreTitle.font = .systemFont(ofSize: 15, weight: .semibold)
        locateCoreButton.target = self; locateCoreButton.action = #selector(locateCore)
        locateCoreButton.bezelStyle = .rounded; locateCoreButton.isEnabled = false
        let coreHeader = NSStackView(views: [coreTitle, NSView(), locateCoreButton])
        coreHeader.orientation = .horizontal; coreHeader.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(coreHeader)
        coreIdentityLabel.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(coreIdentityLabel)
        for kind in [DesktopPermissionKind.accessibility, .screenRecording] {
            let label = NSTextField(labelWithString: kind.title)
            let status = PermissionUI.statusLabel()
            coreLabels[kind] = status
            let row = NSStackView(views: [label, NSView(), status])
            row.orientation = .horizontal; row.widthAnchor.constraint(equalToConstant: 750).isActive = true
            contentStack.addArrangedSubview(row)
        }
        contentStack.addArrangedSubview(PermissionUI.separator())

        let appManagementTitle = NSTextField(labelWithString: L10n.text("App Management"))
        appManagementTitle.font = .systemFont(ofSize: 13, weight: .medium)
        let appManagementDetail = PermissionUI.detailLabel(
            L10n.text("Used to update AgentDock or manage other applications.")
        )
        appManagementDetail.widthAnchor.constraint(equalToConstant: 380).isActive = true
        let appManagementText = NSStackView(views: [appManagementTitle, appManagementDetail])
        appManagementText.orientation = .vertical
        appManagementText.alignment = .leading
        appManagementText.spacing = 3
        let appManagementButton = NSButton(
            title: L10n.text("Open App Management settings"),
            target: self,
            action: #selector(openAppManagementSettings)
        )
        appManagementButton.bezelStyle = .rounded
        let appManagementRow = NSStackView(views: [appManagementText, NSView(), appManagementButton])
        appManagementRow.orientation = .horizontal
        appManagementRow.alignment = .centerY
        appManagementRow.spacing = 8
        appManagementRow.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(appManagementRow)
        contentStack.addArrangedSubview(PermissionUI.separator())

        let filesTitle = NSTextField(labelWithString: L10n.text("Files and Folders"))
        filesTitle.font = .systemFont(ofSize: 15, weight: .semibold)
        let filesDetail = PermissionUI.detailLabel(
            L10n.text("Check whether AgentDock can access Desktop, Documents, Downloads, and other folders you select.")
        )
        filesDetail.widthAnchor.constraint(equalToConstant: 750).isActive = true
        let filesButton = NSButton(title: L10n.text("Check file access…"), target: self, action: #selector(openFileAccess))
        filesButton.bezelStyle = .rounded
        let filesRow = NSStackView(views: [filesTitle, NSView(), filesButton])
        filesRow.orientation = .horizontal
        filesRow.alignment = .centerY
        filesRow.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(filesRow)
        contentStack.addArrangedSubview(filesDetail)

        let refreshButton = NSButton(title: L10n.text("Refresh"), target: self, action: #selector(refreshPressed))
        refreshButton.bezelStyle = .rounded
        let locateButton = NSButton(title: L10n.text("Locate this app"), target: self, action: #selector(locateApp))
        let helpButton = NSButton(title: L10n.text("Permission recovery…"), target: self, action: #selector(showRecovery))
        locateButton.bezelStyle = .rounded; helpButton.bezelStyle = .rounded
        let footer = NSStackView(views: [locateButton, helpButton, NSView(), refreshButton])
        footer.orientation = .horizontal
        footer.widthAnchor.constraint(equalToConstant: 750).isActive = true
        contentStack.addArrangedSubview(footer)

        NSLayoutConstraint.activate([
            scrollView.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            scrollView.topAnchor.constraint(equalTo: contentView.topAnchor),
            scrollView.bottomAnchor.constraint(equalTo: contentView.bottomAnchor),
            contentStack.widthAnchor.constraint(equalTo: scrollView.contentView.widthAnchor),
        ])
    }

    private func makePermissionRow(_ kind: DesktopPermissionKind) -> NSView {
        let title = NSTextField(labelWithString: kind.title)
        title.font = .systemFont(ofSize: 13, weight: .medium)
        let detail = PermissionUI.detailLabel(kind.detail)
        detail.widthAnchor.constraint(equalToConstant: 390).isActive = true
        let text = NSStackView(views: [title, detail])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 3

        let status = PermissionUI.statusLabel()
        status.widthAnchor.constraint(equalToConstant: 100).isActive = true
        statusLabels[kind] = status

        let request = NSButton(title: L10n.text("Request access"), target: self, action: #selector(requestPermission(_:)))
        request.bezelStyle = .rounded
        request.tag = kind.rawValue
        let settings = NSButton(title: L10n.text("Open settings"), target: self, action: #selector(openPermissionSettings(_:)))
        settings.bezelStyle = .rounded
        settings.tag = kind.rawValue

        let row = NSStackView(views: [text, NSView(), status, request, settings])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        row.widthAnchor.constraint(equalToConstant: 750).isActive = true
        return row
    }

    private func refresh() {
        guard window?.isVisible == true, window?.isMiniaturized != true else { return }
        let snapshot = snapshotProvider()
        latestSnapshot = snapshot
        for kind in DesktopPermissionKind.allCases {
            let state = snapshot[kind]
            guard let label = statusLabels[kind] else { continue }
            label.stringValue = DesktopPermissionPresentation.title(state, kind: kind)
            switch state {
            case .granted:
                PermissionUI.applyColor(to: label, granted: true)
            case .notGranted:
                PermissionUI.applyColor(to: label, granted: false)
            case .notDetermined:
                PermissionUI.applyColor(to: label, granted: nil, attention: true)
            case .unavailable:
                PermissionUI.applyColor(to: label, granted: nil)
            }
        }
        renderRecovery()
        refreshCore()
    }

    func startMonitoring() {
        guard timer == nil else { return }
        refreshGeneration += 1
        coreReport = nil
        coreIdentityLabel.stringValue = L10n.text("Checking…")
        for label in coreLabels.values {
            label.stringValue = DesktopPermissionState.unavailable.title
            PermissionUI.applyColor(to: label, granted: nil)
        }
        locateCoreButton.isEnabled = false
        let next = Timer(timeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.refresh() }
        }
        timer = next
        RunLoop.main.add(next, forMode: .common)
        activationObserver = NotificationCenter.default.addObserver(forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.refresh() }
        }
    }

    func windowDidBecomeKey(_ notification: Notification) { refresh() }
    func windowWillClose(_ notification: Notification) {
        timer?.invalidate(); timer = nil
        if let activationObserver { NotificationCenter.default.removeObserver(activationObserver) }
        activationObserver = nil
        refreshGeneration += 1; checkInFlight = false; coreReport = nil
    }

    private func refreshCore() {
        guard !checkInFlight else { return }
        checkInFlight = true
        let generation = refreshGeneration
        coreCheck { [weak self] result in
            guard let self, generation == self.refreshGeneration else { return }
            self.checkInFlight = false
            guard self.window?.isVisible == true else { return }
            switch result {
            case .success(let report):
                self.coreReport = report
                self.coreIdentityLabel.toolTip = report.checked_at
                self.coreIdentityLabel.stringValue = "PID \(report.process_id) · \(report.build.commit)\n\(report.executable_path)"
            case .failure:
                // 新旧版本混用、socket断开等都表示未知，不能显示未授权或沿用旧的绿色结果。
                self.coreReport = nil
                self.coreIdentityLabel.stringValue = L10n.text("Core status is unavailable. Start the matching Core version; menu app access is not proof of Core access.")
            }
            self.locateCoreButton.isEnabled = self.coreReport?.executable_path.isEmpty == false
            for kind in [DesktopPermissionKind.accessibility, .screenRecording] {
                guard let label = self.coreLabels[kind] else { continue }
                let state = DesktopPermissionPresentation.coreState(self.coreReport, kind: kind)
                label.stringValue = DesktopPermissionPresentation.title(state, kind: kind)
                PermissionUI.applyColor(to: label, granted: state == .unavailable ? nil : state == .granted)
            }
            self.renderRecovery()
        }
    }

    private func renderRecovery() {
        recoveryLabel.isHidden = !DesktopPermissionPresentation.needsRecovery(latestSnapshot, core: coreReport)
        recoveryLabel.stringValue = DesktopPermissionPresentation.recovery(adHoc: identity.adHoc)
    }

    @objc private func locateCore() {
        guard let report = coreReport else { return }
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: report.executable_path)])
    }
    @objc private func locateApp() { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: identity.appPath)]) }
    @objc private func showRecovery() {
        let alert = NSAlert()
        alert.messageText = L10n.text("System switch on, but access not active?")
        alert.informativeText = DesktopPermissionPresentation.recovery(adHoc: identity.adHoc) + "\n\n" + identity.appPath + "\n" + identity.identifier + "\n" + (identity.codeHash ?? "")
        alert.addButton(withTitle: L10n.text("OK"))
        if let window { alert.beginSheetModal(for: window) }
    }

    @objc private func refreshPressed() {
        refresh()
    }

    @objc private func requestPermission(_ sender: NSButton) {
        guard let kind = DesktopPermissionKind(rawValue: sender.tag) else { return }
        DesktopPermissionChecker.request(kind)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in
            self?.refresh()
        }
    }

    @objc private func openPermissionSettings(_ sender: NSButton) {
        guard let kind = DesktopPermissionKind(rawValue: sender.tag) else { return }
        DesktopPermissionChecker.openSettings(for: kind)
    }

    @objc private func openFileAccess() {
        fileAccessWindow.present()
    }

    @objc private func openAppManagementSettings() {
        DesktopPermissionChecker.openAppManagementSettings()
    }
}
