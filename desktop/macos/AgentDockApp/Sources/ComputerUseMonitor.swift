import AppKit

@MainActor
private final class ComputerUseSurface: NSView {
    override var isOpaque: Bool { true }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill()
        bounds.fill()
    }
    override func viewDidChangeEffectiveAppearance() { super.viewDidChangeEffectiveAppearance(); needsDisplay = true }
}

// 保留NSButton的事件/辅助功能行为，非激活面板也使用可辨识的语义配色。
@MainActor
private final class ComputerUseButton: NSButton {
    enum Emphasis: Equatable { case neutral, primary, danger }
    var emphasis = Emphasis.neutral { didSet { needsDisplay = true } }
    override var intrinsicContentSize: NSSize {
        let text = (title as NSString).size(withAttributes: [.font: NSFont.systemFont(ofSize: 12, weight: .medium)])
        return NSSize(width: title.isEmpty ? 32 : ceil(text.width) + 24 + (image == nil ? 0 : 20), height: 32)
    }
    override func draw(_ dirtyRect: NSRect) {
        let textColor: NSColor
        let background: NSColor
        switch emphasis {
        case .primary: textColor = .white; background = .controlAccentColor
        case .danger: textColor = .systemRed; background = NSColor.systemRed.withAlphaComponent(0.10)
        case .neutral: textColor = .labelColor; background = .controlBackgroundColor
        }
        let opacity: CGFloat = isEnabled ? 1 : 0.4
        let shape = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 1), xRadius: 8, yRadius: 8)
        background.withAlphaComponent(background.alphaComponent * opacity * (cell?.isHighlighted == true ? 0.7 : 1)).setFill(); shape.fill()
        if emphasis == .neutral { NSColor.separatorColor.withAlphaComponent(0.22).setStroke(); shape.lineWidth = 0.5; shape.stroke() }
        let attrs: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 12, weight: .medium), .foregroundColor: textColor.withAlphaComponent(opacity)]
        let size = (title as NSString).size(withAttributes: attrs)
        let total = size.width + (image == nil ? 0 : title.isEmpty ? 14 : 20)
        var x = (bounds.width - total) / 2
        if let image {
            let config = NSImage.SymbolConfiguration(pointSize: 12, weight: .medium).applying(NSImage.SymbolConfiguration(paletteColors: [textColor]))
            (image.withSymbolConfiguration(config) ?? image).draw(in: NSRect(x: x, y: (bounds.height - 14) / 2, width: 14, height: 14), from: .zero, operation: .sourceOver, fraction: opacity, respectFlipped: true, hints: nil)
            x += 20
        }
        if !title.isEmpty { (title as NSString).draw(at: NSPoint(x: x, y: (bounds.height - size.height) / 2), withAttributes: attrs) }
    }
    override func viewDidChangeEffectiveAppearance() { super.viewDidChangeEffectiveAppearance(); needsDisplay = true }
}

@MainActor
final class ComputerUsePanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

@MainActor
private final class ComputerUseApprovalCard: NSStackView {
    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = NSColor.controlAccentColor.withAlphaComponent(0.07).cgColor
            layer?.borderColor = NSColor.separatorColor.withAlphaComponent(0.4).cgColor
        }
    }
}

@MainActor
final class ComputerUseMonitor: NSObject, NSWindowDelegate, NSMenuDelegate {
    let panel: ComputerUsePanel
    let preview = ComputerUsePreview()
    private let canvas = ComputerUseCanvas()
    private let applicationIcon = NSImageView()
    private let activityIcon = NSImageView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let modeLabel = NSTextField(labelWithString: "")
    private let stateLabel = NSTextField(labelWithString: "")
    private let progressLabel = NSTextField(labelWithString: "")
    private let taskLabel = NSTextField(labelWithString: "")
    private let approvalLabel = NSTextField(wrappingLabelWithString: "")
    private let errorLabel = NSTextField(wrappingLabelWithString: "")
    private let pauseButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let stopButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let collapseButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let previewButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let approveButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let alwaysApproveButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let denyButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let stepButton = ComputerUseButton(title: "", target: nil, action: nil)
    private let approvalBox = ComputerUseApprovalCard()
    private let contentStack = NSStackView()
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let transport: ComputerUseTransport
    private let preferences: UserDefaults
    private var previewExpanded: Bool
    private var timer: Timer?
    private var busy = false
    private var menuTracking = false
    private var emergencyHotkey: ComputerUseEmergencyHotkey?
    private var ended = false
    private var collapsed = false
    private var closeRequested = false
    private var stopIntentID: String?
    private var pauseIntentID: String?
    private var pending: (operation: String, id: String, approval: String, trust: String)?
    private var lastRendered: ComputerUseState?
    private var lastIconPath = ""
    private var layoutKey = ""
    private(set) var state: ComputerUseState?
    private(set) var connected = false
    private(set) var renderCount = 0
    private(set) var menuBuildCount = 0

    init(runtimeRoot: URL, preferences: UserDefaults = .standard) {
        self.preferences = preferences
        previewExpanded = preferences.object(forKey: "ComputerUsePreviewExpanded") as? Bool ?? true
        transport = ComputerUseTransport(socketPath: runtimeRoot.appendingPathComponent("control.sock").path, controllerID: UUID().uuidString)
        panel = ComputerUsePanel(contentRect: NSRect(x: 0, y: 0, width: 400, height: 354), styleMask: [.titled, .closable, .nonactivatingPanel], backing: .buffered, defer: false)
        super.init()
        panel.title = L10n.text("Computer Use")
        panel.titlebarAppearsTransparent = true
        panel.hasShadow = true
        panel.identifier = NSUserInterfaceItemIdentifier("AgentDockComputerUseMonitor")
        panel.delegate = self
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.sharingType = .none
        panel.isMovableByWindowBackground = true
        configureViews()
        statusItem.isVisible = false
        statusItem.button?.image = NSImage(systemSymbolName: "cursorarrow.rays", accessibilityDescription: L10n.text("Computer Use"))
        rebuildMenu()
        preview.onFrame = { [weak self] image in
            guard let self, self.connected, self.state?.shouldPreview == true, self.previewExpanded, !self.collapsed else { return }
            self.canvas.image = image
            self.canvas.message = ""
        }
        preview.onState = { [weak self] message in
            self?.canvas.image = nil
            self?.canvas.message = message
        }
        if let screen = NSScreen.main {
            panel.setFrameTopLeftPoint(NSPoint(x: screen.visibleFrame.maxX - panel.frame.width - 22, y: screen.visibleFrame.maxY - 28))
        }
    }

    private func configureViews() {
        let content = ComputerUseSurface()
        panel.contentView = content
        titleLabel.font = .systemFont(ofSize: 15, weight: .semibold)
        titleLabel.lineBreakMode = .byTruncatingTail
        modeLabel.font = .systemFont(ofSize: 10, weight: .medium)
        modeLabel.textColor = .secondaryLabelColor
        stateLabel.font = .systemFont(ofSize: 12)
        stateLabel.textColor = .secondaryLabelColor
        stateLabel.lineBreakMode = .byTruncatingTail
        progressLabel.font = .monospacedDigitSystemFont(ofSize: 11, weight: .semibold)
        progressLabel.textColor = .secondaryLabelColor
        taskLabel.font = .systemFont(ofSize: 12)
        taskLabel.textColor = .secondaryLabelColor
        taskLabel.lineBreakMode = .byTruncatingTail
        taskLabel.isHidden = true
        applicationIcon.imageScaling = .scaleProportionallyUpOrDown
        activityIcon.image = NSImage(systemSymbolName: "circle.fill", accessibilityDescription: nil)
        let heading = NSStackView(views: [titleLabel, modeLabel])
        heading.orientation = .vertical; heading.alignment = .leading; heading.spacing = 3
        let header = NSStackView(views: [applicationIcon, heading, NSView(), previewButton, collapseButton])
        header.orientation = .horizontal; header.spacing = 10
        let status = NSStackView(views: [activityIcon, stateLabel, NSView(), progressLabel])
        status.orientation = .horizontal; status.spacing = 7
        stateLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        configureButton(previewButton, symbol: "chevron.up", title: "", action: #selector(togglePreview))
        configureButton(collapseButton, symbol: "minus", title: "", action: #selector(collapse))
        collapseButton.toolTip = L10n.text("Collapse")
        collapseButton.setAccessibilityLabel(L10n.text("Collapse"))
        configureButton(pauseButton, symbol: "pause.fill", title: L10n.text("Pause"), action: #selector(togglePause))
        configureButton(stepButton, symbol: "forward.end.fill", title: "", action: #selector(nextStep))
        stepButton.toolTip = L10n.text("Next step")
        stepButton.setAccessibilityLabel(L10n.text("Next step"))
        configureButton(stopButton, symbol: "stop.fill", title: L10n.text("Stop"), action: #selector(stopAndClose))
        stopButton.contentTintColor = .systemRed
        stopButton.emphasis = .danger
        stopButton.toolTip = L10n.text("Stop and close")
        configureButton(approveButton, symbol: "", title: L10n.text("Allow once"), action: #selector(approveApplication))
        configureButton(alwaysApproveButton, symbol: "checkmark.shield", title: L10n.text("Always allow"), action: #selector(alwaysApproveApplication))
        alwaysApproveButton.bezelColor = .controlAccentColor
        alwaysApproveButton.emphasis = .primary
        configureButton(denyButton, symbol: "xmark", title: "", action: #selector(denyApplication))
        denyButton.toolTip = L10n.text("Deny")
        denyButton.setAccessibilityLabel(L10n.text("Deny"))
        approvalLabel.font = .systemFont(ofSize: 12, weight: .medium)
        approvalBox.orientation = .vertical; approvalBox.alignment = .leading; approvalBox.spacing = 10
        approvalBox.edgeInsets = NSEdgeInsets(top: 12, left: 12, bottom: 12, right: 12)
        approvalBox.wantsLayer = true; approvalBox.layer?.cornerRadius = 12; approvalBox.layer?.borderWidth = 0.5
        approvalBox.viewDidChangeEffectiveAppearance()
        approvalBox.addArrangedSubview(approvalLabel)
        let decisions = NSStackView(views: [denyButton, NSView(), approveButton, alwaysApproveButton])
        decisions.orientation = .horizontal; decisions.spacing = 6
        approvalBox.addArrangedSubview(decisions); approvalBox.isHidden = true
        errorLabel.font = .systemFont(ofSize: 11); errorLabel.textColor = .systemRed; errorLabel.isHidden = true
        canvas.wantsLayer = true; canvas.layer?.cornerRadius = 12; canvas.layer?.masksToBounds = true
        let controls = NSStackView(views: [pauseButton, stepButton, NSView(), stopButton])
        controls.orientation = .horizontal; controls.spacing = 8
        contentStack.orientation = .vertical; contentStack.alignment = .leading; contentStack.spacing = 10
        contentStack.detachesHiddenViews = true
        for view in [header, taskLabel, status, approvalBox, errorLabel, canvas, controls] { contentStack.addArrangedSubview(view) }
        content.addSubview(contentStack); contentStack.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            contentStack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            contentStack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            contentStack.topAnchor.constraint(equalTo: content.topAnchor, constant: 14),
            applicationIcon.widthAnchor.constraint(equalToConstant: 36), applicationIcon.heightAnchor.constraint(equalToConstant: 36),
            activityIcon.widthAnchor.constraint(equalToConstant: 7), activityIcon.heightAnchor.constraint(equalToConstant: 7),
            header.heightAnchor.constraint(equalToConstant: 40), status.heightAnchor.constraint(equalToConstant: 18),
            canvas.heightAnchor.constraint(equalToConstant: 196), controls.heightAnchor.constraint(equalToConstant: 32),
            approvalLabel.widthAnchor.constraint(equalTo: contentStack.widthAnchor, constant: -24),
            decisions.widthAnchor.constraint(equalTo: contentStack.widthAnchor, constant: -24)
        ])
        for view in [header, taskLabel, status, approvalBox, errorLabel, canvas, controls] {
            view.widthAnchor.constraint(equalTo: contentStack.widthAnchor).isActive = true
        }
        updateLayout()
    }
    private func configureButton(_ button: NSButton, symbol: String, title: String, action: Selector) {
        button.target = self; button.action = action; button.title = title
        button.bezelStyle = .rounded; button.controlSize = .regular
        if !symbol.isEmpty { button.image = NSImage(systemSymbolName: symbol, accessibilityDescription: title.isEmpty ? nil : title) }
        button.imagePosition = title.isEmpty ? .imageOnly : .imageLeading
        button.setContentHuggingPriority(.required, for: .horizontal)
    }
    private func updateLayout() {
        canvas.isHidden = !previewExpanded
        previewButton.image = NSImage(systemSymbolName: previewExpanded ? "chevron.up" : "chevron.down", accessibilityDescription: nil)
        previewButton.toolTip = L10n.text(previewExpanded ? "Hide preview" : "Show preview")
        previewButton.setAccessibilityLabel(previewButton.toolTip)
        let key = "\(previewExpanded)-\(approvalBox.isHidden)-\(taskLabel.isHidden)-\(errorLabel.isHidden)-\(approvalLabel.stringValue)"
        guard key != layoutKey else { return }
        layoutKey = key
        panel.contentView?.layoutSubtreeIfNeeded()
        let top = panel.frame.maxY
        panel.setContentSize(NSSize(width: 400, height: max(136, contentStack.fittingSize.height + 28)))
        var frame = panel.frame; frame.origin.y = top - frame.height
        if let screen = panel.screen { frame.origin.y = max(screen.visibleFrame.minY, frame.origin.y) }
        panel.setFrame(frame, display: true)
    }

    func start() {
        guard timer == nil else { return }
        emergencyHotkey = ComputerUseEmergencyHotkey { [weak self] in self?.stopAndClose() }
        tick()
        timer = Timer(timeInterval: 0.4, repeats: true) { [weak self] _ in MainActor.assumeIsolated { self?.tick() } }
        if let timer { RunLoop.main.add(timer, forMode: .common) }
    }
    func shutdown() {
        ended = true; emergencyHotkey?.shutdown(); emergencyHotkey = nil; timer?.invalidate(); timer = nil; preview.select(nil)
        if let state, state.isLive { _ = try? transport.callSync(operation: "stop", sessionID: state.session_id) }
        panel.orderOut(nil); NSStatusBar.system.removeStatusItem(statusItem)
    }
    private func tick() {
        guard !busy, !ended else { return }
        busy = true
        let command = pending; pending = nil
        let visibleID = (panel.isVisible || collapsed && statusItem.isVisible) ? (state?.session_id ?? "") : ""
        transport.exchange(operation: command?.operation, sessionID: command?.id ?? "", visibleID: visibleID, stopSessionID: stopIntentID ?? "", approvalID: command?.approval ?? "", pauseSessionID: pauseIntentID ?? "", trustID: command?.trust ?? "") { [weak self] result in
            guard let self, !self.ended else { return }
            switch result {
            case .success(let response):
                self.connected = true
                if response.session_id == self.pauseIntentID && ["paused", "stopped", "closed", "cleanup_failed"].contains(response.phase) && response.active_operations == 0 { self.pauseIntentID = nil }
                if response.session_id == self.stopIntentID && ["stopped", "closed", "cleanup_failed"].contains(response.phase) && response.active_operations == 0 { self.stopIntentID = nil }
                self.apply(response)
            case .failure(let error):
                if let command, command.operation != "pause" && command.operation != "stop" {
                    self.errorLabel.stringValue = L10n.text("Change not applied")
                    self.errorLabel.toolTip = error.localizedDescription
                    self.errorLabel.isHidden = false
                    self.updateLayout()
                }
                self.lastRendered = nil
                self.connected = false; self.preview.select(nil); self.canvas.image = nil
                self.canvas.message = L10n.text("Connection lost. The core pauses when the monitor lease expires.")
                self.pauseButton.isEnabled = false
                if self.closeRequested { self.stateLabel.stringValue = L10n.text("Stop not yet confirmed") }
            }
            self.busy = false
            // 只有暂停/停止意图会随心跳重试，授权和业务输入不重放。
        }
    }
    // 内部状态入口也供隔离UI测试使用；真实状态只来自本机IPC。
    func apply(_ next: ComputerUseState) {
        let previous = state
        let changed = next.session_id != previous?.session_id
        state = next
        if changed { if stopIntentID == nil { closeRequested = false }; canvas.image = nil }
        if next.pending_application != nil && next.pending_application?.id != previous?.pending_application?.id { collapsed = false }
        let phase: String
        switch next.phase {
        case "running": phase = next.pending_application != nil ? L10n.text("Waiting for application approval") : next.active_operations > 0 ? activityLabel(next.activity) : L10n.text("Waiting for next action")
        case "pausing": phase = L10n.text("Pausing · releasing input…")
        case "stopping": phase = L10n.text("Stopping · releasing input…")
        case "paused": phase = next.reason == "monitor_disconnected" ? L10n.text("Paused after monitor disconnected") : L10n.text("Paused")
        case "cleanup_failed": phase = L10n.text("Input cleanup failed · manual check required")
        case "stopped", "closed": phase = L10n.text("Stopped")
        default: phase = L10n.text("Ready for a new task")
        }
        if lastRendered != next {
            lastRendered = next; renderCount += 1
            titleLabel.stringValue = next.application.name.isEmpty ? L10n.text("Computer Use") : next.application.name
            let path = next.application.app_path ?? ""
            if path != lastIconPath || applicationIcon.image == nil {
                lastIconPath = path
                applicationIcon.image = path.isEmpty ? NSImage(systemSymbolName: "cursorarrow.motionlines", accessibilityDescription: nil) : NSWorkspace.shared.icon(forFile: path)
            }
            modeLabel.stringValue = L10n.text(next.mode == "foreground" ? "Foreground" : "Background")
            taskLabel.stringValue = next.task_label ?? ""; taskLabel.toolTip = next.task_label
            taskLabel.isHidden = taskLabel.stringValue.isEmpty
            approvalBox.isHidden = next.pending_application == nil
            if let request = next.pending_application {
                approvalLabel.stringValue = request.application.name + " · " + L10n.text(request.mode == "foreground" ? "Foreground" : "Background")
                approvalLabel.toolTip = request.application.app_path ?? request.application.bundle_id
                if request.mode == "foreground" { approvalLabel.stringValue += "\n" + L10n.text("Foreground control can affect the desktop.") }
            }
            approveButton.isEnabled = stopIntentID == nil && next.pending_application != nil
            denyButton.isEnabled = approveButton.isEnabled
            alwaysApproveButton.isHidden = next.pending_application?.rememberable != true
            alwaysApproveButton.isEnabled = approveButton.isEnabled
            stepButton.isEnabled = next.can_resume && next.cleanup_failed != true && stopIntentID == nil
            stateLabel.stringValue = phase
            activityIcon.contentTintColor = next.phase == "running" && next.pending_application == nil ? .systemGreen : next.cleanup_failed == true ? .systemRed : .systemOrange
            progressLabel.stringValue = (next.sequence_total ?? 0) > 0 ? "\(next.sequence_step ?? 0)/\(next.sequence_total ?? 0)" : ""
            pauseButton.title = next.cleanup_failed == true ? L10n.text("Confirm manual cleanup") : (next.can_resume ? L10n.text("Resume") : L10n.text("Pause"))
            pauseButton.image = NSImage(systemSymbolName: next.can_resume ? "play.fill" : "pause.fill", accessibilityDescription: nil)
            pauseButton.toolTip = L10n.text("Resume with fresh observation")
            pauseButton.isEnabled = stopIntentID == nil && (next.can_resume || next.phase == "running" || next.phase == "cleanup_failed" && next.active_operations == 0)
            stopButton.isEnabled = next.isLive && !next.isDraining
            collapseButton.isEnabled = next.isLive
            statusItem.isVisible = next.shouldShowStatusItem
            statusItem.button?.title = ""
            statusItem.button?.toolTip = titleLabel.stringValue + " · " + phase
            updateLayout()
        }
        if next.phase == "cleanup_failed" { closeRequested = false; collapsed = false }
        if next.isLive && !collapsed && !closeRequested && !panel.isVisible { panel.orderFrontRegardless() }
        if !next.shouldShowStatusItem || next.phase == "stopped" || next.phase == "completed" || next.phase == "closed" { panel.orderOut(nil) }
        let target = connected && previewExpanded && !collapsed && !closeRequested && next.shouldPreview ? next.window : nil
        // UI没有变化时仍可按原有有限重试策略恢复预览。
        preview.select(target)
        if target == nil { canvas.image = nil; canvas.message = phase }
        canvas.targetBounds = next.window.bounds
        canvas.point = next.pointer
        if stopIntentID != nil { stateLabel.stringValue = L10n.text("Stop not yet confirmed") }
    }
    private func outcomeLabel(_ value: String) -> String {
        switch value {
        case "sequence_completed": return L10n.text("Sequence complete")
        case "sequence_interrupted": return L10n.text("Sequence interrupted")
        case "dispatched": return L10n.text("Input dispatched")
        case "wait_met": return L10n.text("Condition verified")
        case "wait_timeout": return L10n.text("Condition timed out")
        case "cleanup_failed": return L10n.text("Input cleanup failed")
        case "cancelled": return L10n.text("Cancelled")
        case "failed_or_partial": return L10n.text("Failed or partially executed")
        default: return L10n.text("Finished · result not verified")
        }
    }
    private func activityLabel(_ value: String) -> String {
        switch value {
        case "sequence": return L10n.text("Sequence")
        case "launch": return L10n.text("Opening application")
        case "permissions": return L10n.text("Waiting for permission")
        case "wait": return L10n.text("Waiting for a verified condition")
        case "snapshot": return L10n.text("Observing window")
        case "type", "set_value", "key": return L10n.text("Entering input")
        case "click": return L10n.text("Clicking")
        case "drag": return L10n.text("Dragging")
        case "scroll": return L10n.text("Scrolling")
        default: return L10n.text("Operating window")
        }
    }
    private func rebuildMenu() {
        guard !menuTracking else { return }
        menuBuildCount += 1
        let menu = statusItem.menu ?? NSMenu(); menu.removeAllItems(); menu.delegate = self
        let show = menu.addItem(withTitle: L10n.text("Show Computer Use"), action: #selector(showPanel), keyEquivalent: ""); show.target = self
        let pause = menu.addItem(withTitle: pauseButton.title, action: #selector(togglePause), keyEquivalent: ""); pause.target = self; pause.isEnabled = pauseButton.isEnabled
        let stop = menu.addItem(withTitle: L10n.text("Stop desktop control"), action: #selector(stopAndClose), keyEquivalent: ""); stop.target = self; stop.isEnabled = state?.isLive == true
        let retry = menu.addItem(withTitle: L10n.text("Retry preview"), action: #selector(retryPreview), keyEquivalent: ""); retry.target = self
        menu.addItem(trustedApplicationsMenuItem())
        let end = menu.addItem(withTitle: L10n.text("End and release desktop task"), action: #selector(endTask), keyEquivalent: ""); end.target = self; end.isEnabled = state?.hasDesktopTask == true && state?.active_operations == 0 && state?.cleanup_failed != true
        let hotkey = menu.addItem(withTitle: L10n.text("Emergency stop: Control-Option-Command-F12"), action: nil, keyEquivalent: ""); hotkey.isEnabled = false
        hotkey.toolTip = emergencyHotkey?.available == true ? L10n.text("Emergency shortcut registered") : L10n.text("Shortcut unavailable; use Stop and close")
        if let events = state?.recent_operations, !events.isEmpty {
            menu.addItem(.separator())
            for event in events.suffix(6).reversed() {
                let label = activityLabel(event.activity) + " · " + outcomeLabel(event.outcome) + " · " + String(event.elapsed_ms) + " ms"
                let item = menu.addItem(withTitle: label, action: nil, keyEquivalent: ""); item.isEnabled = false
            }
        }
        menu.autoenablesItems = false; statusItem.menu = menu
    }
    func menuWillOpen(_ menu: NSMenu) { rebuildMenu(); menuTracking = true }
    func menuDidClose(_ menu: NSMenu) { menuTracking = false }
    // 主 AgentDock 菜单也提供撤销入口，空闲时不必为了管理信任常驻第二个图标。
    func trustedApplicationsMenuItem() -> NSMenuItem {
        let submenu = NSMenu()
        submenu.autoenablesItems = false
        for entry in state?.trusted_applications ?? [] {
            let item = submenu.addItem(withTitle: entry.application.name + " · " + L10n.text(entry.mode == "foreground" ? "Foreground" : "Background") + " — " + L10n.text("Revoke"), action: #selector(revokeApplication(_:)), keyEquivalent: "")
            item.target = self; item.representedObject = entry.id; item.toolTip = entry.application.app_path
        }
        let item = NSMenuItem(title: L10n.text("Trusted applications"), action: nil, keyEquivalent: "")
        item.submenu = submenu; item.isEnabled = !submenu.items.isEmpty
        return item
    }
    private func command(_ operation: String, trustID: String = "") {
        guard let state else { return }
        if stopIntentID != nil && operation != "stop" { return }
        pending = (operation, state.session_id, (operation == "approve_application" || operation == "approve_application_always" || operation == "deny_application") ? state.pending_application?.id ?? "" : "", trustID)
        errorLabel.isHidden = true; lastRendered = nil; updateLayout()
        pauseButton.isEnabled = false
        if operation == "pause" { pauseIntentID = state.session_id }
        if operation == "stop" { pauseIntentID = nil; stopIntentID = state.session_id; closeRequested = true; stateLabel.stringValue = L10n.text("Stopping · releasing input…"); preview.select(nil); canvas.image = nil }
        tick()
    }
    @objc func togglePause() {
        if state?.cleanup_failed == true {
            let alert = NSAlert()
            alert.messageText = L10n.text("Confirm manual cleanup")
            alert.informativeText = L10n.text("Check that all mouse buttons and keys are released. This acknowledges manual cleanup; it does not resume the task.")
            alert.addButton(withTitle: L10n.text("I checked the input state")); alert.addButton(withTitle: L10n.text("Cancel"))
            if alert.runModal() == .alertFirstButtonReturn { command("acknowledge_cleanup") }
        } else if let state { command(state.can_resume ? "resume" : "pause") }
    }
    @objc func nextStep() { command("step") }
    @objc func approveApplication() { command("approve_application") }
    @objc func alwaysApproveApplication() { command("approve_application_always") }
    @objc private func revokeApplication(_ item: NSMenuItem) {
        guard let id = item.representedObject as? String else { return }
        command("revoke_application", trustID: id)
    }
    @objc func togglePreview() {
        previewExpanded.toggle(); preferences.set(previewExpanded, forKey: "ComputerUsePreviewExpanded")
        updateLayout(); if let state { apply(state) }
    }
    @objc func denyApplication() { command("deny_application") }
    @objc func endTask() { command("end_task") }
    @objc func retryPreview() { preview.retryByUser(); if let state { apply(state) } }
    @objc func stopAndClose() { command("stop") }
    @objc func collapse() { collapsed = true; preview.select(nil); canvas.image = nil; panel.orderOut(nil) }
    @objc func showPanel() { collapsed = false; panel.orderFrontRegardless(); if let state { apply(state) } }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if state?.isLive == true { stopAndClose(); return false }
        return true
    }
}
