import AppKit

@MainActor
final class ComputerUsePanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

@MainActor
final class ComputerUseMonitor: NSObject, NSWindowDelegate, NSMenuDelegate {
    let panel: ComputerUsePanel
    let preview = ComputerUsePreview()
    private let canvas = ComputerUseCanvas()
    private let titleLabel = NSTextField(labelWithString: "")
    private let stateLabel = NSTextField(labelWithString: "")
    private let detailLabel = NSTextField(labelWithString: "")
    private let pauseButton = NSButton(title: "", target: nil, action: nil)
    private let stopButton = NSButton(title: "", target: nil, action: nil)
    private let collapseButton = NSButton(title: "", target: nil, action: nil)
    private let taskLabel = NSTextField(labelWithString: "")
    private let approvalLabel = NSTextField(wrappingLabelWithString: "")
    private let approvalBox = NSStackView()
    private let approveButton = NSButton(title: "", target: nil, action: nil)
    private let denyButton = NSButton(title: "", target: nil, action: nil)
    private let stepButton = NSButton(title: "", target: nil, action: nil)
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let transport: ComputerUseTransport
    private var timer: Timer?
    private var busy = false
    private var menuTracking = false
    private var emergencyHotkey: ComputerUseEmergencyHotkey?
    private var ended = false
    private var collapsed = false
    private var closeRequested = false
    private var stopIntentID: String?
    private var pauseIntentID: String?
    private var pending: (operation: String, id: String, approval: String)?
    private(set) var state: ComputerUseState?
    private(set) var connected = false

    init(runtimeRoot: URL) {
        transport = ComputerUseTransport(socketPath: runtimeRoot.appendingPathComponent("control.sock").path, controllerID: UUID().uuidString)
        panel = ComputerUsePanel(contentRect: NSRect(x: 0, y: 0, width: 440, height: 440), styleMask: [.titled, .closable, .nonactivatingPanel], backing: .buffered, defer: false)
        super.init()
        panel.title = L10n.text("Computer Use")
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
        preview.onFrame = { [weak self] image in
            guard let self, self.connected, self.state?.shouldPreview == true, !self.collapsed else { return }
            self.canvas.image = image
            self.detailLabel.stringValue = L10n.text("Live window preview · local only")
        }
        preview.onState = { [weak self] message in
            self?.canvas.image = nil
            self?.canvas.message = message
            self?.detailLabel.stringValue = message
        }
        if let screen = NSScreen.main {
            panel.setFrameTopLeftPoint(NSPoint(x: screen.visibleFrame.maxX - panel.frame.width - 22, y: screen.visibleFrame.maxY - 28))
        }
    }

    private func configureViews() {
        let content = NSView()
        panel.contentView = content
        titleLabel.font = .systemFont(ofSize: 14, weight: .semibold)
        titleLabel.lineBreakMode = .byTruncatingTail
        stateLabel.font = .systemFont(ofSize: 12, weight: .medium)
        stateLabel.textColor = .secondaryLabelColor
        detailLabel.font = .systemFont(ofSize: 11)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        pauseButton.target = self; pauseButton.action = #selector(togglePause)
        stopButton.target = self; stopButton.action = #selector(stopAndClose)
        collapseButton.target = self; collapseButton.action = #selector(collapse)
        stopButton.title = L10n.text("Stop and close")
        collapseButton.title = L10n.text("Collapse")
        for button in [pauseButton, stopButton, collapseButton] { button.bezelStyle = .rounded }
        let header = NSStackView(views: [titleLabel, collapseButton]); header.orientation = .horizontal
        header.distribution = .fill; header.spacing = 8
        titleLabel.setContentHuggingPriority(.defaultLow, for: .horizontal)
        taskLabel.font = .systemFont(ofSize: 11); taskLabel.lineBreakMode = .byTruncatingTail
        approveButton.title = L10n.text("Allow for this task"); approveButton.target = self; approveButton.action = #selector(approveApplication)
        denyButton.title = L10n.text("Deny"); denyButton.target = self; denyButton.action = #selector(denyApplication)
        stepButton.title = L10n.text("Next step"); stepButton.target = self; stepButton.action = #selector(nextStep); stepButton.bezelStyle = .rounded
        approvalBox.orientation = .vertical; approvalBox.alignment = .leading; approvalBox.spacing = 6
        approvalBox.addArrangedSubview(approvalLabel)
        let decisions = NSStackView(views: [approveButton, denyButton]); decisions.orientation = .horizontal
        approvalBox.addArrangedSubview(decisions); approvalBox.isHidden = true
        let controls = NSStackView(views: [pauseButton, stepButton, stopButton]); controls.orientation = .horizontal; controls.distribution = .fillEqually; controls.spacing = 8
        let stack = NSStackView(views: [header, taskLabel, stateLabel, approvalBox, canvas, detailLabel, controls])
        stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 9
        content.addSubview(stack); stack.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14), stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 12), stack.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -12),
            header.widthAnchor.constraint(equalTo: stack.widthAnchor), canvas.widthAnchor.constraint(equalTo: stack.widthAnchor),
            canvas.heightAnchor.constraint(greaterThanOrEqualToConstant: 160), controls.widthAnchor.constraint(equalTo: stack.widthAnchor), detailLabel.widthAnchor.constraint(equalTo: stack.widthAnchor)
        ])
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
        if let state, state.isLive {
            // 最多等待一次本地 socket 超时；即便失败，核心也会在租约到期后取消控制。
            _ = try? transport.callSync(operation: "stop", sessionID: state.session_id)
        }
        panel.orderOut(nil); NSStatusBar.system.removeStatusItem(statusItem)
    }
    private func tick() {
        guard !busy, !ended else { return }
        busy = true
        let command = pending; pending = nil
        let visibleID = (panel.isVisible || collapsed && statusItem.isVisible) ? (state?.session_id ?? "") : ""
        transport.exchange(operation: command?.operation, sessionID: command?.id ?? "", visibleID: visibleID, stopSessionID: stopIntentID ?? "", approvalID: command?.approval ?? "", pauseSessionID: pauseIntentID ?? "") { [weak self] result in
            guard let self, !self.ended else { return }
            switch result {
            case .success(let response):
                self.connected = true
                if response.session_id == self.pauseIntentID && ["paused", "stopped", "closed", "cleanup_failed"].contains(response.phase) && response.active_operations == 0 { self.pauseIntentID = nil }
                if response.session_id == self.stopIntentID && ["stopped", "closed", "cleanup_failed"].contains(response.phase) && response.active_operations == 0 { self.stopIntentID = nil }
                self.apply(response)
            case .failure:
                self.connected = false; self.preview.select(nil); self.canvas.image = nil
                self.canvas.message = L10n.text("Connection lost. The core pauses when the monitor lease expires.")
                self.detailLabel.stringValue = self.canvas.message
                self.pauseButton.isEnabled = false
                // 暂停也通过心跳保留幂等意图，不在租约过期后反复发送无效命令。
                if self.closeRequested { self.stateLabel.stringValue = L10n.text("Stop not yet confirmed") }
            }
            self.busy = false
            // 安全意图由下一次公共模式心跳重试，绝不重放输入或自动重复恢复。
        }
    }
    private func apply(_ next: ComputerUseState) {
        let changed = next.session_id != state?.session_id
        state = next
        if changed { collapsed = false; if stopIntentID == nil { closeRequested = false }; canvas.image = nil }
        titleLabel.stringValue = next.application.name.isEmpty ? L10n.text("Computer Use") : next.application.name
        taskLabel.stringValue = next.task_label ?? ""
        taskLabel.toolTip = next.task_label
        approvalBox.isHidden = next.pending_application == nil
        if let request = next.pending_application {
            let identity = request.application.app_path ?? request.application.bundle_id
            approvalLabel.stringValue = L10n.text("Application access requires your approval") + "\n" + request.application.name + " · " + request.mode
            approvalLabel.toolTip = identity
            approvalLabel.stringValue += "\n" + (request.application.bundle_id.isEmpty ? "PID " + String(request.application.pid) : request.application.bundle_id)
            if request.mode == "foreground" { approvalLabel.stringValue += "\n" + L10n.text("Foreground control can affect the desktop.") }
        }
        approveButton.isEnabled = stopIntentID == nil && next.pending_application != nil
        denyButton.isEnabled = approveButton.isEnabled
        stepButton.isEnabled = next.can_resume && next.cleanup_failed != true && stopIntentID == nil
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
        stateLabel.stringValue = phase + (next.mode == "foreground" ? " · " + L10n.text("Foreground control") : " · " + L10n.text("Background window"))
        pauseButton.title = next.cleanup_failed == true ? L10n.text("Confirm manual cleanup") : (next.can_resume ? L10n.text("Resume") : L10n.text("Pause"))
        pauseButton.toolTip = L10n.text("Resume with fresh observation")
        pauseButton.isEnabled = stopIntentID == nil && (next.can_resume || next.phase == "running" || next.phase == "cleanup_failed" && next.active_operations == 0)
        stopButton.isEnabled = next.isLive && !next.isDraining
        collapseButton.isEnabled = next.isLive
        statusItem.isVisible = next.enabled && (!next.session_id.isEmpty || !(next.task_reference ?? "").isEmpty) && (next.phase != "completed" || !(next.task_reference ?? "").isEmpty)
        statusItem.button?.title = " " + phase
        statusItem.button?.image = NSImage(systemSymbolName: "cursorarrow.rays", accessibilityDescription: L10n.text("Computer Use"))
        rebuildMenu()
        if next.phase == "cleanup_failed" { closeRequested = false; collapsed = false }
        if next.isLive && !collapsed && !closeRequested { panel.orderFrontRegardless() }
        if next.phase == "stopped" || next.phase == "completed" || next.phase == "closed" { panel.orderOut(nil) }
        let target = connected && !collapsed && !closeRequested && next.shouldPreview ? next.window : nil
        preview.select(target)
        if target == nil { canvas.image = nil; canvas.message = phase; detailLabel.stringValue = L10n.text("Stops desktop control only, not other agent tools.") }
        canvas.targetBounds = next.window.bounds
        canvas.point = next.pointer
        if stopIntentID != nil { stateLabel.stringValue = L10n.text("Stop not yet confirmed") }
        if target != nil && preview.lastFrame != .distantPast && Date().timeIntervalSince(preview.lastFrame) > 2 {
            detailLabel.stringValue = L10n.text("Frame unchanged · window may be static or minimized")
        }
    }
    private func outcomeLabel(_ value: String) -> String {
        switch value {
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
        let menu = NSMenu(); menu.delegate = self
        let show = menu.addItem(withTitle: L10n.text("Show Computer Use"), action: #selector(showPanel), keyEquivalent: ""); show.target = self
        let pause = menu.addItem(withTitle: pauseButton.title, action: #selector(togglePause), keyEquivalent: ""); pause.target = self; pause.isEnabled = pauseButton.isEnabled
        let stop = menu.addItem(withTitle: L10n.text("Stop desktop control"), action: #selector(stopAndClose), keyEquivalent: ""); stop.target = self; stop.isEnabled = state?.isLive == true
        let retry = menu.addItem(withTitle: L10n.text("Retry preview"), action: #selector(retryPreview), keyEquivalent: ""); retry.target = self
        let end = menu.addItem(withTitle: L10n.text("End and release desktop task"), action: #selector(endTask), keyEquivalent: ""); end.target = self; end.isEnabled = state?.active_operations == 0 && state?.cleanup_failed != true
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
    func menuWillOpen(_ menu: NSMenu) { menuTracking = true }
    func menuDidClose(_ menu: NSMenu) { menuTracking = false; rebuildMenu() }
    private func command(_ operation: String) {
        guard let state else { return }
        if stopIntentID != nil && operation != "stop" { return }
        pending = (operation, state.session_id, (operation == "approve_application" || operation == "deny_application") ? state.pending_application?.id ?? "" : "")
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
