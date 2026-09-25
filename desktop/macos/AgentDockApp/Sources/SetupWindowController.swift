import AppKit
import Foundation

private enum QuickTunnelRefreshState {
    case idle
    case refreshing
    case failed
}

@MainActor
final class SetupWindowController: NSWindowController, NSWindowDelegate {
    private lazy var installer = InstallerRunner(service: service)
    private let publicEndpointChecker = PublicEndpointChecker()
    private let service: ServiceController
    private let menuLoginAgent: MenuLoginAgentController
    private let onChanged: () -> Void
    private let onUpdateRequested: () -> Void

    private let titleLabel = NSTextField(labelWithString: "AgentDock")
    private let subtitleLabel = NSTextField(labelWithString: L10n.text("Local MCP service and public access management"))
    private let stateLabel = NSTextField(labelWithString: L10n.text("Not installed"))
    private let nexusStateLabel = NSTextField(labelWithString: L10n.text("Not configured"))

    // 文档内容不足一屏时保持顶部对齐，把剩余空间自然留在底部。
    private let scrollDocumentView = TopAlignedDocumentView()
    private let contentStack = TopAlignedStackView()
    private let serviceSection = NSStackView()
    private let navigation = NSSegmentedControl(labels: [L10n.text("Overview"), L10n.text("Connection settings")], trackingMode: .selectOne, target: nil, action: nil)
    private let tasksButton = NSButton(title: L10n.text("Task center"), target: nil, action: nil)
    private var overviewCard: NSView?
    private var connectionCard: NSView?
    private let localAddress = NSTextField(labelWithString: L10n.text("Not installed"))
    private let publicAddress = NSTextField(labelWithString: L10n.text("Disabled"))
    private let publicCheckStatus = NSTextField(labelWithString: "")
    private let publicTestButton = NSButton(title: L10n.text("Test"), target: nil, action: nil)
    private let publicCopyButton = NSButton(title: L10n.text("Copy"), target: nil, action: nil)
    private let authToken = NSTextField(labelWithString: L10n.text("Not generated"))
    private let oauthPassword = NSTextField(labelWithString: L10n.text("Not generated"))
    private let authReveal = NSButton(title: L10n.text("Show"), target: nil, action: nil)
    private let oauthReveal = NSButton(title: L10n.text("Show"), target: nil, action: nil)
    private let startStopButton = NSButton(title: L10n.text("Start service"), target: nil, action: nil)
    private let restartButton = NSButton(title: L10n.text("Restart"), target: nil, action: nil)
    private let updateButton = NSButton(title: L10n.text("Check for updates"), target: nil, action: nil)

    private let publicMode = NSSegmentedControl(
        labels: [L10n.text("Local only"), L10n.text("Temporary address"), L10n.text("Custom domain"), L10n.text("Private tunnel")],
        trackingMode: .selectOne,
        target: nil,
        action: nil
    )
    private let modeDescription = NSTextField(wrappingLabelWithString: "")
    private let namedFields = NSStackView()
    private let serverURLField = NSTextField(string: "")
    private let tunnelTokenField = NSSecureTextField(string: "")

    private let progress = NSProgressIndicator()
    private let statusLabel = NSTextField(wrappingLabelWithString: "")
    private let applyButton = NSButton(title: L10n.text("Configure and enable"), target: nil, action: nil)
    private let permissionsButton = NSButton(title: L10n.text("Check permissions"), target: nil, action: nil)
    private let advancedButton = NSButton(title: L10n.text("Advanced settings"), target: nil, action: nil)
    private let logsButton = NSButton(title: L10n.text("Open logs"), target: nil, action: nil)
    private let diagnosticsButton = NSButton(title: L10n.text("Diagnostics"), target: nil, action: nil)
    private lazy var diagnosticsWindow = DiagnosticsWindowController(service: service)
    private lazy var taskCenterWindow = TaskCenterWindowController(service: service)
    @objc private func openDiagnosticsPressed() { diagnosticsWindow.present() }
    @objc func presentTaskCenter() { taskCenterWindow.present() }
    func presentDiagnostics() { diagnosticsWindow.present() }

    private var currentStatus = ServiceStatus.missing
    private var initialMode: TunnelMode = .local
    private var initialServerURL = ""
    private var authTokenValue = ""
    private var oauthPasswordValue = ""
    private var authVisible = false
    private var oauthVisible = false
    private var isBusy = false
    private var isUpdateInProgress = false
    private var externalOperationInProgress = false
    private var quickTunnelRefreshState: QuickTunnelRefreshState = .idle

    private var migrationRequired: Bool {
        currentStatus.migrationRequired
    }

    private var controlsLocked: Bool {
        isBusy || isUpdateInProgress || externalOperationInProgress
    }

    var hasActiveServiceOperation: Bool {
        isBusy || (advancedSettings?.hasActiveServiceOperation ?? false)
    }
    private var displayedPublicMCPURL: URL?
    private var lastCheckedPublicMCPURL: URL?
    private var activePublicCheckURL: URL?
    private var publicCheckTask: Task<Void, Never>?

    private var advancedSettings: AdvancedSettingsWindowController?
    private lazy var permissionsWindow = DesktopPermissionsWindowController()

    init(
        service: ServiceController,
        menuLoginAgent: MenuLoginAgentController,
        onChanged: @escaping () -> Void,
        onUpdateRequested: @escaping () -> Void
    ) {
        self.service = service
        self.menuLoginAgent = menuLoginAgent
        self.onChanged = onChanged
        self.onUpdateRequested = onUpdateRequested
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 790, height: 620),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AgentDock"
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 700, height: 460)
        window.setFrameAutosaveName("AgentDockManagement")
        window.center()
        super.init(window: window)
        window.delegate = self
        configureUI()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func present(status: ServiceStatus) {
        if controlsLocked { refreshServiceStatus(status) } else { update(status: status) }
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func update(status: ServiceStatus) {
        guard !isBusy else { currentStatus = status; return }
        currentStatus = status
        authVisible = false
        oauthVisible = false
        statusLabel.isHidden = true
        quickTunnelRefreshState = .idle
        cancelPublicCheck(clearLastResult: true)
        setBusy(false)

        if status.installed {
            titleLabel.stringValue = "AgentDock"
            subtitleLabel.stringValue = L10n.text("Local MCP service and public access management")
            applyButton.title = L10n.text("Apply changes")
            advancedButton.isEnabled = !controlsLocked
            logsButton.isEnabled = true
            serviceSection.isHidden = false
            updateServiceSection(status)
            selectCurrentMode(configuration: status.configuration)
        } else {
            titleLabel.stringValue = L10n.text("Set up AgentDock")
            subtitleLabel.stringValue = L10n.text("Configure the local service and allow AgentDock to run in the background")
            stateLabel.stringValue = L10n.text("● Not configured")
            stateLabel.textColor = .secondaryLabelColor
            applyButton.title = L10n.text("Configure and enable")
            applyButton.isEnabled = true
            advancedButton.isEnabled = false
            logsButton.isEnabled = false
            serviceSection.isHidden = true
            navigation.selectedSegment = 1
            authTokenValue = ""
            oauthPasswordValue = ""
            select(mode: .local)
        }
        updateNexusState(status.nexusConnection)
        refreshCredentialFields()
        refreshChangeState()
        navigationChanged()
        updateWindowHeight()
    }

    func setExternalOperationInProgress(_ value: Bool) {
        externalOperationInProgress = value
        setBusy(isBusy)
        advancedSettings?.setUpdateInProgress(value || isUpdateInProgress)
    }

    @objc private func navigationChanged() {
        overviewCard?.isHidden = navigation.selectedSegment == 1
        connectionCard?.isHidden = navigation.selectedSegment != 1
        applyButton.isHidden = navigation.selectedSegment != 1
    }

    func setUpdateInProgress(_ inProgress: Bool) {
        isUpdateInProgress = inProgress
        setBusy(isBusy)
        advancedSettings?.setUpdateInProgress(inProgress)
        if inProgress {
            showStatus(L10n.text("Updating AgentDock…"), isError: false)
        } else if statusLabel.stringValue == L10n.text("Updating AgentDock…") {
            statusLabel.isHidden = true
        }
    }

    func refreshServiceStatus(_ status: ServiceStatus) {
        let installationChanged = currentStatus.installed != status.installed
        currentStatus = status
        if installationChanged {
            update(status: status)
            return
        }
        guard status.installed else { return }
        updateServiceSection(status)
        updateNexusState(status.nexusConnection)
        refreshCredentialFields()
    }

    func presentPermissions() {
        permissionsWindow.present()
    }

    func windowDidResignKey(_ notification: Notification) {
        authVisible = false
        oauthVisible = false
        refreshCredentialFields()
    }

    private func configureUI() {
        guard let contentView = window?.contentView else { return }

        let scrollView = NSScrollView()
        scrollView.hasVerticalScroller = true
        scrollView.autohidesScrollers = true
        scrollView.drawsBackground = false
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        contentView.addSubview(scrollView)

        scrollDocumentView.translatesAutoresizingMaskIntoConstraints = false
        scrollView.documentView = scrollDocumentView

        contentStack.orientation = .vertical
        contentStack.alignment = .leading
        contentStack.spacing = 12
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        scrollDocumentView.addSubview(contentStack)

        titleLabel.font = .systemFont(ofSize: 25, weight: .semibold)
        subtitleLabel.textColor = .secondaryLabelColor
        stateLabel.alignment = .right
        stateLabel.font = .systemFont(ofSize: 13, weight: .medium)
        stateLabel.lineBreakMode = .byTruncatingMiddle
        stateLabel.setContentCompressionResistancePriority(.fittingSizeCompression, for: .horizontal)
        nexusStateLabel.alignment = .left
        nexusStateLabel.font = .systemFont(ofSize: 12, weight: .regular)
        nexusStateLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        nexusStateLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

        let headerText = NSStackView(views: [titleLabel, subtitleLabel])
        headerText.orientation = .vertical
        headerText.alignment = .leading
        headerText.spacing = 3
        let header = NSStackView(views: [headerText, NSView(), stateLabel])
        header.orientation = .horizontal
        header.alignment = .centerY

        for field in [localAddress, publicAddress, authToken, oauthPassword] {
            field.isSelectable = true
            field.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
            field.setContentHuggingPriority(.defaultLow, for: .horizontal)
            field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        for field in [localAddress, authToken, oauthPassword] {
            field.lineBreakMode = .byTruncatingMiddle
        }
        publicAddress.lineBreakMode = .byCharWrapping
        publicAddress.maximumNumberOfLines = 2
        publicAddress.setContentCompressionResistancePriority(.fittingSizeCompression, for: .horizontal)
        publicAddress.cell?.wraps = true

        publicCheckStatus.font = .systemFont(ofSize: 11.5)
        publicCheckStatus.textColor = .secondaryLabelColor
        publicCheckStatus.isHidden = true
        publicCheckStatus.lineBreakMode = .byTruncatingTail
        publicCheckStatus.setContentHuggingPriority(.defaultLow, for: .horizontal)
        publicCheckStatus.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        publicTestButton.bezelStyle = .inline
        publicTestButton.target = self
        publicTestButton.action = #selector(testPublicAddressPressed)
        publicCopyButton.bezelStyle = .inline
        publicCopyButton.target = self
        publicCopyButton.action = #selector(copyPublicAddress)

        authReveal.bezelStyle = .inline
        authReveal.target = self
        authReveal.action = #selector(toggleAuthToken)
        oauthReveal.bezelStyle = .inline
        oauthReveal.target = self
        oauthReveal.action = #selector(toggleOAuthPassword)

        serviceSection.orientation = .vertical
        serviceSection.alignment = .leading
        serviceSection.spacing = 8
        serviceSection.addArrangedSubview(sectionTitle(L10n.text("Connection information")))
        addFullWidth(valueRow(title: L10n.text("Local MCP"), field: localAddress, actions: [copyButton(#selector(copyLocalAddress))]), to: serviceSection)
        addFullWidth(valueRow(title: L10n.text("Public MCP"), field: publicAddress, actions: [publicTestButton, publicCopyButton]), to: serviceSection)
        addFullWidth(valueDetailRow(publicCheckStatus), to: serviceSection)
        addFullWidth(valueRow(title: "Nexus", field: nexusStateLabel, actions: []), to: serviceSection)
        addFullWidth(valueRow(title: "Bearer Token", field: authToken, actions: [authReveal, copyButton(#selector(copyAuthToken))]), to: serviceSection)
        addFullWidth(valueRow(title: L10n.text("OAuth password"), field: oauthPassword, actions: [oauthReveal, copyButton(#selector(copyOAuthPassword))]), to: serviceSection)

        startStopButton.target = self
        startStopButton.action = #selector(startStopPressed)
        restartButton.target = self
        restartButton.action = #selector(restartPressed)
        updateButton.target = self
        updateButton.action = #selector(updatePressed)
        let serviceActions = NSStackView(views: [startStopButton, restartButton, updateButton, NSView()])
        serviceActions.orientation = .horizontal
        serviceActions.spacing = 8
        addFullWidth(serviceActions, to: serviceSection)

        publicMode.target = self
        publicMode.action = #selector(modeChanged)
        publicMode.segmentStyle = .rounded
        publicMode.setContentHuggingPriority(.defaultLow, for: .horizontal)
        modeDescription.textColor = .secondaryLabelColor
        modeDescription.font = .systemFont(ofSize: 12)

        serverURLField.placeholderString = "https://mini.example.com"
        tunnelTokenField.placeholderString = L10n.text("Paste Cloudflare Tunnel Token")
        for field in [serverURLField, tunnelTokenField] {
            field.setContentHuggingPriority(.defaultLow, for: .horizontal)
            field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        serverURLField.target = self
        serverURLField.action = #selector(configurationEdited)
        tunnelTokenField.target = self
        tunnelTokenField.action = #selector(configurationEdited)

        namedFields.orientation = .vertical
        namedFields.alignment = .leading
        namedFields.spacing = 7
        addFullWidth(formRow(title: L10n.text("Public address"), control: serverURLField), to: namedFields)
        addFullWidth(formRow(title: "Tunnel Token", control: tunnelTokenField), to: namedFields)

        progress.style = .spinning
        progress.controlSize = .small
        progress.isDisplayedWhenStopped = false
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byWordWrapping
        statusLabel.maximumNumberOfLines = 3
        statusLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        statusLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        statusLabel.isHidden = true

        diagnosticsButton.bezelStyle = .inline
        diagnosticsButton.target = self
        diagnosticsButton.action = #selector(openDiagnosticsPressed)
        logsButton.bezelStyle = .inline
        logsButton.target = self
        logsButton.action = #selector(openLogsPressed)
        permissionsButton.bezelStyle = .inline
        permissionsButton.target = self
        permissionsButton.action = #selector(openPermissionsPressed)
        advancedButton.bezelStyle = .inline
        advancedButton.target = self
        advancedButton.action = #selector(openAdvancedPressed)
        applyButton.bezelStyle = .rounded
        applyButton.keyEquivalent = "\r"
        applyButton.target = self
        applyButton.action = #selector(applyPressed)

        let footerTools = NSStackView(views: [logsButton, permissionsButton, advancedButton, NSView(), applyButton])
        footerTools.spacing = 14
        let statusRow = NSStackView(views: [progress, statusLabel]); statusRow.spacing = 8
        let footer = ManagementUI.column([statusRow, footerTools], spacing: 9)
        navigation.selectedSegment = 0; navigation.target = self; navigation.action = #selector(navigationChanged)
        tasksButton.target = self; tasksButton.action = #selector(presentTaskCenter)
        tasksButton.bezelStyle = .rounded; diagnosticsButton.bezelStyle = .rounded
        let navigationRow = NSStackView(views: [navigation, NSView(), tasksButton, diagnosticsButton])
        navigationRow.spacing = 12
        let fixedHeader = ManagementUI.column([header, navigationRow], spacing: 20)
        let publicSection = ManagementUI.column([
            sectionTitle(L10n.text("Public access")), publicMode, modeDescription, namedFields,
            ManagementUI.label(L10n.text("Applying connection settings may restart services. Existing credentials are kept unless explicitly replaced."), size: 12, secondary: true)
        ], spacing: 16)
        let overview = ManagementUI.card(serviceSection)
        let connection = ManagementUI.card(publicSection)
        overviewCard = overview; connectionCard = connection
        addFullWidth(overview, to: contentStack)
        addFullWidth(connection, to: contentStack)
        addFullWidth(ManagementUI.label(L10n.text("Use Task center to return to work from any chat. Use Diagnostics to distinguish local startup from public connectivity."), size: 12, secondary: true), to: contentStack)
        fixedHeader.translatesAutoresizingMaskIntoConstraints = false
        footer.translatesAutoresizingMaskIntoConstraints = false
        contentView.addSubview(fixedHeader); contentView.addSubview(footer)
        navigationChanged()

        NSLayoutConstraint.activate([
            fixedHeader.leadingAnchor.constraint(equalTo: contentView.leadingAnchor, constant: 28),
            fixedHeader.trailingAnchor.constraint(equalTo: contentView.trailingAnchor, constant: -28),
            fixedHeader.topAnchor.constraint(equalTo: contentView.topAnchor, constant: 24),
            footer.leadingAnchor.constraint(equalTo: fixedHeader.leadingAnchor),
            footer.trailingAnchor.constraint(equalTo: fixedHeader.trailingAnchor),
            footer.bottomAnchor.constraint(equalTo: contentView.bottomAnchor, constant: -20),
            scrollView.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            scrollView.topAnchor.constraint(equalTo: fixedHeader.bottomAnchor, constant: 8),
            scrollView.bottomAnchor.constraint(equalTo: footer.topAnchor, constant: -8),
            scrollDocumentView.widthAnchor.constraint(equalTo: scrollView.contentView.widthAnchor),
            contentStack.leadingAnchor.constraint(equalTo: scrollDocumentView.leadingAnchor, constant: 28),
            contentStack.trailingAnchor.constraint(equalTo: scrollDocumentView.trailingAnchor, constant: -28),
            contentStack.topAnchor.constraint(equalTo: scrollDocumentView.topAnchor, constant: 24),
            contentStack.bottomAnchor.constraint(equalTo: scrollDocumentView.bottomAnchor, constant: -22),
        ])
    }

    private func updateServiceSection(_ status: ServiceStatus) {
        if migrationRequired {
            stateLabel.stringValue = L10n.text("● Migration required")
            stateLabel.textColor = .systemOrange
        } else if status.healthy {
            if AppVersion.matchesHealthVersion(status.version) {
                stateLabel.stringValue = L10n.format("● Running normally · %@", AppVersion.current)
                stateLabel.textColor = .systemGreen
            } else {
                stateLabel.stringValue = L10n.format(
                    "● Version mismatch · AgentDock %@ · Core %@",
                    AppVersion.current,
                    AppVersion.display(status.version)
                )
                stateLabel.textColor = .systemRed
            }
        } else if status.requiresApproval {
            stateLabel.stringValue = L10n.text("● Background permission required")
            stateLabel.textColor = .systemOrange
        } else if status.loaded {
            stateLabel.stringValue = L10n.text("● Service error")
            stateLabel.textColor = .systemRed
        } else {
            stateLabel.stringValue = L10n.text("● Stopped")
            stateLabel.textColor = .secondaryLabelColor
        }

        let configuration = status.configuration
        localAddress.stringValue = configuration?.localMCPURL?.absoluteString ?? L10n.text("Configuration unavailable")
        renderPublicAddress(configuration?.publicMCPURL, automaticallyCheck: true)
        authTokenValue = configuration?.authToken ?? ""
        oauthPasswordValue = configuration?.oauthPassword ?? ""
        startStopButton.title = migrationRequired
            ? L10n.text("Waiting for migration")
            : (status.requiresApproval ? L10n.text("Open background settings") : (status.loaded ? L10n.text("Stop service") : L10n.text("Start service")))
        if !controlsLocked {
            startStopButton.isEnabled = status.installed && !migrationRequired
            restartButton.isEnabled = status.installed && !status.requiresApproval && !migrationRequired
            updateButton.isEnabled = status.installed && !migrationRequired
        }
    }

    private func updateNexusState(_ state: NexusConnectionState) {
        switch state {
        case .connected:
            nexusStateLabel.stringValue = L10n.text("Connected")
        case .disconnected:
            nexusStateLabel.stringValue = L10n.text("Disconnected")
        case .unconfigured:
            nexusStateLabel.stringValue = L10n.text("Not configured")
        case .configurationError:
            nexusStateLabel.stringValue = L10n.text("Configuration error")
        }
    }

    private func renderPublicAddress(_ publicMCPURL: URL?, automaticallyCheck: Bool) {
        switch quickTunnelRefreshState {
        case .refreshing:
            cancelPublicCheck(clearLastResult: true)
            displayedPublicMCPURL = nil
            publicAddress.stringValue = L10n.text("Generating a new address…")
            publicCheckStatus.stringValue = L10n.text("The old address is hidden; waiting for a new temporary public address")
            publicCheckStatus.textColor = .secondaryLabelColor
            publicCheckStatus.isHidden = false
            refreshPublicActions()
        case .failed:
            cancelPublicCheck(clearLastResult: true)
            displayedPublicMCPURL = nil
            publicAddress.stringValue = L10n.text("No new address generated")
            publicCheckStatus.stringValue = L10n.text("Refresh failed; the old address is not shown as the new address")
            publicCheckStatus.textColor = .systemRed
            publicCheckStatus.isHidden = false
            refreshPublicActions()
        case .idle:
            setDisplayedPublicMCPURL(publicMCPURL, automaticallyCheck: automaticallyCheck)
        }
    }

    private func setDisplayedPublicMCPURL(_ publicMCPURL: URL?, automaticallyCheck: Bool) {
        let addressChanged = displayedPublicMCPURL != publicMCPURL
        if addressChanged {
            cancelPublicCheck(clearLastResult: true)
        }
        displayedPublicMCPURL = publicMCPURL
        publicAddress.stringValue = publicMCPURL?.absoluteString ?? ((try? service.configuredTunnelMode()) == .secure ? L10n.text("Private tunnel · remote delivery unconfirmed") : L10n.text("Disabled"))

        guard let publicMCPURL else {
            publicCheckStatus.stringValue = ""
            publicCheckStatus.isHidden = true
            refreshPublicActions()
            return
        }

        refreshPublicActions()
        if automaticallyCheck,
           activePublicCheckURL != publicMCPURL,
           lastCheckedPublicMCPURL != publicMCPURL {
            beginPublicCheck(publicMCPURL, automatic: true)
        }
    }

    private func beginQuickTunnelRefresh() {
        quickTunnelRefreshState = .refreshing
        renderPublicAddress(nil, automaticallyCheck: false)
    }

    private func markQuickTunnelRefreshFailed() {
        quickTunnelRefreshState = .failed
        renderPublicAddress(nil, automaticallyCheck: false)
    }

    private func beginPublicCheck(_ publicMCPURL: URL, automatic: Bool) {
        guard quickTunnelRefreshState == .idle else { return }
        if automatic, lastCheckedPublicMCPURL == publicMCPURL { return }

        cancelPublicCheck(clearLastResult: !automatic)
        activePublicCheckURL = publicMCPURL
        publicCheckStatus.stringValue = L10n.text("Checking public access…")
        publicCheckStatus.textColor = .secondaryLabelColor
        publicCheckStatus.isHidden = false
        refreshPublicActions()

        publicCheckTask = Task { [weak self] in
            guard let self else { return }
            let maximumAttempts = automatic ? 3 : 1
            var finalResult: PublicEndpointCheckResult?

            for attempt in 1...maximumAttempts {
                if Task.isCancelled { return }
                if maximumAttempts > 1 {
                    publicCheckStatus.stringValue = L10n.format(
                        "Checking public access (%d/%d)…",
                        attempt,
                        maximumAttempts
                    )
                }
                let result = await publicEndpointChecker.check(publicMCPURL: publicMCPURL)
                finalResult = result
                if result.isReachable || attempt == maximumAttempts { break }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }

            guard !Task.isCancelled, let finalResult else { return }
            finishPublicCheck(finalResult, for: publicMCPURL)
        }
    }

    private func finishPublicCheck(_ result: PublicEndpointCheckResult, for publicMCPURL: URL) {
        guard quickTunnelRefreshState == .idle,
              activePublicCheckURL == publicMCPURL,
              displayedPublicMCPURL == publicMCPURL else {
            return
        }

        activePublicCheckURL = nil
        publicCheckTask = nil
        lastCheckedPublicMCPURL = publicMCPURL
        let latency = result.latencyMilliseconds.map { " · \($0) ms" } ?? ""
        publicCheckStatus.stringValue = "● \(result.message)\(latency)"
        publicCheckStatus.textColor = result.isReachable ? .systemGreen : .systemRed
        publicCheckStatus.isHidden = false
        refreshPublicActions()
    }

    private func cancelPublicCheck(clearLastResult: Bool) {
        publicCheckTask?.cancel()
        publicCheckTask = nil
        activePublicCheckURL = nil
        if clearLastResult {
            lastCheckedPublicMCPURL = nil
        }
    }

    private func refreshPublicActions() {
        let hasAddress = quickTunnelRefreshState == .idle && displayedPublicMCPURL != nil
        publicCopyButton.isEnabled = hasAddress
        publicTestButton.title = activePublicCheckURL == nil ? L10n.text("Test") : L10n.text("Checking")
        publicTestButton.isEnabled = hasAddress && activePublicCheckURL == nil && !controlsLocked
    }

    private func selectCurrentMode(configuration: ServiceConfiguration?) {
        if (try? service.configuredTunnelMode()) == .secure {
            initialMode = .secure; initialServerURL = ""; select(mode: .secure); return
        }
        guard let publicURL = configuration?.publicURL, !publicURL.isEmpty else {
            initialMode = .local
            initialServerURL = ""
            select(mode: .local)
            return
        }
        if publicURL.contains(".trycloudflare.com") {
            initialMode = .quick
            initialServerURL = ""
            select(mode: .quick)
        } else {
            initialMode = .named
            initialServerURL = publicURL
            serverURLField.stringValue = publicURL
            select(mode: .named)
        }
        tunnelTokenField.stringValue = ""
    }

    private func select(mode: TunnelMode) {
        publicMode.selectedSegment = segment(for: mode)
        modeDescription.stringValue = mode.detail
        namedFields.isHidden = mode != .named
        if mode == .named, currentStatus.installed, initialMode == .named {
            tunnelTokenField.placeholderString = L10n.text("Leave blank to keep the existing Tunnel Token")
        } else {
            tunnelTokenField.placeholderString = L10n.text("Paste Cloudflare Tunnel Token")
        }
        updateWindowHeight()
    }

    private func segment(for mode: TunnelMode) -> Int {
        switch mode {
        case .local: return 0
        case .quick: return 1
        case .named: return 2
        case .secure: return 3
        }
    }

    private var selectedMode: TunnelMode {
        switch publicMode.selectedSegment {
        case 1: return .quick
        case 2: return .named
        case 3: return .secure
        default: return .local
        }
    }

    @objc private func modeChanged() {
        select(mode: selectedMode)
        refreshChangeState()
    }

    @objc private func configurationEdited() { refreshChangeState() }

    @objc private func applyPressed() {
        guard !controlsLocked, !hasActiveServiceOperation else { return }
        let request = InstallRequest(
            mode: selectedMode,
            serverURL: serverURLField.stringValue,
            tunnelToken: tunnelTokenField.stringValue
        )
        do {
            _ = try request.validatedServerURL()
            _ = try request.validatedTunnelToken()
        } catch {
            showStatus(error.localizedDescription, isError: true)
            return
        }

        let refreshingQuickTunnel = currentStatus.installed && initialMode == .quick && selectedMode == .quick
        if refreshingQuickTunnel {
            // 生成过程中立即隐藏旧地址，避免用户把旧地址误认为本次生成结果。
            beginQuickTunnelRefresh()
        }
        setBusy(true)
        showStatus(
            refreshingQuickTunnel ? L10n.text("Generating a new temporary public address…") : L10n.text("Validating and applying AgentDock configuration…"),
            isError: false
        )
        Task {
            do {
                let result = try await installer.run(request: request)
                let resultPublicMCPURL: URL?
                if result.publicMCPURL.isEmpty {
                    resultPublicMCPURL = nil
                } else if let parsedURL = URL(string: result.publicMCPURL) {
                    resultPublicMCPURL = parsedURL
                } else {
                    throw ValidationError(L10n.text("The installer returned an invalid public MCP address."))
                }

                authTokenValue = result.authToken
                oauthPasswordValue = result.oauthPassword
                localAddress.stringValue = result.localMCPURL
                quickTunnelRefreshState = .idle
                setDisplayedPublicMCPURL(resultPublicMCPURL, automaticallyCheck: true)
                tunnelTokenField.stringValue = ""
                authVisible = false
                oauthVisible = false
                refreshCredentialFields()
                showStatus(
                    refreshingQuickTunnel
                        ? L10n.text("A new temporary public address was generated; checking public access automatically.")
                        : L10n.format("AgentDock %@ is configured and running normally.", result.version),
                    isError: false
                )
                setBusy(false)
                initialMode = selectedMode
                initialServerURL = selectedMode == .named ? (try? request.validatedServerURL()) ?? "" : ""
                refreshChangeState()
                onChanged()
            } catch {
                if refreshingQuickTunnel {
                    markQuickTunnelRefreshFailed()
                }
                setBusy(false)
                showStatus(error.localizedDescription, isError: true)
            }
        }
    }

    @objc private func startStopPressed() {
        guard !controlsLocked, !hasActiveServiceOperation else { return }
        if currentStatus.requiresApproval {
            service.openBackgroundItemsSettings()
            return
        }
        performServiceAction(
            inProgress: currentStatus.loaded ? L10n.text("Stopping AgentDock…") : L10n.text("Starting AgentDock…"),
            completed: currentStatus.loaded ? L10n.text("AgentDock stopped.") : L10n.text("AgentDock started.")
        ) {
            if self.currentStatus.loaded { try await self.service.stop() }
            else { try await self.service.start() }
        }
    }

    @objc private func restartPressed() {
        performServiceAction(
            inProgress: L10n.text("Restarting AgentDock…"),
            completed: L10n.text("AgentDock restarted.")
        ) { try await self.service.restart() }
    }

    @objc private func updatePressed() {
        guard !isUpdateInProgress else { return }
        onUpdateRequested()
    }

    private func performServiceAction(
        inProgress: String,
        completed: String,
        operation: @escaping () async throws -> Void
    ) {
        guard !controlsLocked, !hasActiveServiceOperation else { return }
        setBusy(true)
        showStatus(inProgress, isError: false)
        Task {
            do {
                try await operation()
                setBusy(false)
                showStatus(completed, isError: false)
                onChanged()
            } catch {
                setBusy(false)
                showStatus(error.localizedDescription, isError: true)
            }
        }
    }

    @objc private func openPermissionsPressed() { permissionsWindow.present() }
    @objc private func openAdvancedPressed() {
        guard !controlsLocked else { return }
        if advancedSettings == nil {
            advancedSettings = AdvancedSettingsWindowController(
                service: service,
                menuLoginAgent: menuLoginAgent,
                onChanged: onChanged
            )
        }
        advancedSettings?.present(status: currentStatus)
    }
    @objc private func openLogsPressed() { service.openLogs() }

    @objc private func testPublicAddressPressed() {
        guard let displayedPublicMCPURL else { return }
        beginPublicCheck(displayedPublicMCPURL, automatic: false)
    }

    @objc private func copyLocalAddress(_ sender: NSButton) { copy(localAddress.stringValue, button: sender) }
    @objc private func copyPublicAddress(_ sender: NSButton) {
        copy(displayedPublicMCPURL?.absoluteString ?? "", button: sender)
    }
    @objc private func copyAuthToken(_ sender: NSButton) { copy(authTokenValue, button: sender) }
    @objc private func copyOAuthPassword(_ sender: NSButton) { copy(oauthPasswordValue, button: sender) }

    @objc private func toggleAuthToken() {
        authVisible.toggle()
        refreshCredentialFields()
    }

    @objc private func toggleOAuthPassword() {
        oauthVisible.toggle()
        refreshCredentialFields()
    }

    private func refreshCredentialFields() {
        authToken.stringValue = displayedSecret(authTokenValue, visible: authVisible, empty: L10n.text("Not generated"))
        oauthPassword.stringValue = displayedSecret(oauthPasswordValue, visible: oauthVisible, empty: L10n.text("Disabled"))
        authReveal.title = authVisible ? L10n.text("Hide") : L10n.text("Show")
        oauthReveal.title = oauthVisible ? L10n.text("Hide") : L10n.text("Show")
    }

    private func displayedSecret(_ value: String, visible: Bool, empty: String) -> String {
        guard !value.isEmpty else { return empty }
        return visible ? value : String(repeating: "•", count: min(max(value.count, 12), 24))
    }

    private func copy(_ value: String, button: NSButton) {
        guard !value.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
        let original = button.title
        button.title = L10n.text("Copied")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            button.title = original
        }
    }

    private func refreshChangeState() {
        guard currentStatus.installed else {
            applyButton.title = L10n.text("Configure and enable")
            applyButton.isEnabled = !controlsLocked
            return
        }

        if migrationRequired {
            applyButton.title = L10n.text("Migrate and enable")
            applyButton.isEnabled = !controlsLocked
            return
        }

        let refreshingQuickTunnel = initialMode == .quick && selectedMode == .quick
        applyButton.title = refreshingQuickTunnel ? L10n.text("Regenerate temporary address") : L10n.text("Apply changes")

        let serverChanged = selectedMode == .named
            && serverURLField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                != initialServerURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let changed = refreshingQuickTunnel
            || selectedMode != initialMode
            || serverChanged
            || !tunnelTokenField.stringValue.isEmpty
        applyButton.isEnabled = changed && !controlsLocked
    }

    private func setBusy(_ busy: Bool) {
        isBusy = busy
        let locked = controlsLocked
        for control in [publicMode, serverURLField, tunnelTokenField, startStopButton, restartButton, updateButton, advancedButton] {
            control.isEnabled = !locked && (control !== advancedButton || currentStatus.installed)
        }
        if !locked && migrationRequired {
            startStopButton.isEnabled = false
            restartButton.isEnabled = false
            updateButton.isEnabled = false
        }
        // 查看日志在更新期间仍然安全，保留给用户排查长时间操作。
        logsButton.isEnabled = !busy && currentStatus.installed
        if busy {
            applyButton.isEnabled = false
            progress.startAnimation(nil)
        } else {
            progress.stopAnimation(nil)
            refreshChangeState()
        }
        refreshPublicActions()
    }

    private func showStatus(_ message: String, isError: Bool) {
        statusLabel.stringValue = message
        statusLabel.textColor = isError ? .systemRed : .secondaryLabelColor
        statusLabel.isHidden = message.isEmpty
    }

    private func updateWindowHeight() {
        // 不在切换连接模式或后台刷新时改变用户调整的窗口大小。
        contentStack.needsLayout = true
    }

    private func sectionTitle(_ title: String) -> NSTextField {
        let label = NSTextField(labelWithString: title)
        label.font = .systemFont(ofSize: 14, weight: .semibold)
        return label
    }

    private func separator() -> NSBox {
        let box = NSBox()
        box.boxType = .separator
        return box
    }

    private func formRow(title: String, control: NSView) -> NSView {
        let label = NSTextField(labelWithString: title)
        label.textColor = .secondaryLabelColor
        label.widthAnchor.constraint(equalToConstant: 96).isActive = true
        let row = NSStackView(views: [label, control])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 10
        return row
    }

    private func valueDetailRow(_ detail: NSTextField) -> NSView {
        let spacer = NSView()
        spacer.widthAnchor.constraint(equalToConstant: 102).isActive = true
        let row = NSStackView(views: [spacer, detail])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 0
        return row
    }

    private func valueRow(title: String, field: NSTextField, actions: [NSButton]) -> NSView {
        let label = NSTextField(labelWithString: title)
        label.textColor = .secondaryLabelColor
        label.widthAnchor.constraint(equalToConstant: 94).isActive = true
        let row = NSStackView(views: [label, field] + actions)
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        return row
    }

    private func addFullWidth(_ view: NSView, to stack: NSStackView, widthAdjustment: CGFloat = 0) {
        stack.addArrangedSubview(view)
        view.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: widthAdjustment).isActive = true
    }

    private func copyButton(_ action: Selector) -> NSButton {
        let button = NSButton(title: L10n.text("Copy"), target: self, action: action)
        button.bezelStyle = .inline
        return button
    }
}
