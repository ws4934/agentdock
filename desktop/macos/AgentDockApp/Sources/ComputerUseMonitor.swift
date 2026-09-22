import AppKit

@MainActor
final class ComputerUsePanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

@MainActor
final class ComputerUseMonitor: NSObject, NSWindowDelegate {
    let panel: ComputerUsePanel
    let preview = ComputerUsePreview()
    private let canvas = ComputerUseCanvas()
    private let titleLabel = NSTextField(labelWithString: "")
    private let stateLabel = NSTextField(labelWithString: "")
    private let detailLabel = NSTextField(labelWithString: "")
    private let pauseButton = NSButton(title: "", target: nil, action: nil)
    private let stopButton = NSButton(title: "", target: nil, action: nil)
    private let collapseButton = NSButton(title: "", target: nil, action: nil)
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let transport: ComputerUseTransport
    private var timer: Timer?
    private var busy = false
    private var ended = false
    private var collapsed = false
    private var closeRequested = false
    private var pending: (operation: String, id: String)?
    private(set) var state: ComputerUseState?
    private(set) var connected = false

    init(runtimeRoot: URL) {
        transport = ComputerUseTransport(socketPath: runtimeRoot.appendingPathComponent("control.sock").path, controllerID: UUID().uuidString)
        panel = ComputerUsePanel(contentRect: NSRect(x: 0, y: 0, width: 400, height: 350), styleMask: [.titled, .closable, .nonactivatingPanel], backing: .buffered, defer: false)
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
            panel.setFrameTopLeftPoint(NSPoint(x: screen.visibleFrame.maxX - 422, y: screen.visibleFrame.maxY - 28))
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
        let controls = NSStackView(views: [pauseButton, stopButton]); controls.orientation = .horizontal; controls.distribution = .fillEqually; controls.spacing = 8
        let stack = NSStackView(views: [header, stateLabel, canvas, detailLabel, controls])
        stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 9
        content.addSubview(stack); stack.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14), stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 12), stack.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -12),
            header.widthAnchor.constraint(equalTo: stack.widthAnchor), canvas.widthAnchor.constraint(equalTo: stack.widthAnchor),
            canvas.heightAnchor.constraint(greaterThanOrEqualToConstant: 190), controls.widthAnchor.constraint(equalTo: stack.widthAnchor), detailLabel.widthAnchor.constraint(equalTo: stack.widthAnchor)
        ])
    }

    func start() {
        guard timer == nil else { return }
        tick()
        timer = Timer.scheduledTimer(withTimeInterval: 0.4, repeats: true) { [weak self] _ in Task { @MainActor in self?.tick() } }
    }
    func shutdown() {
        ended = true; timer?.invalidate(); timer = nil; preview.select(nil)
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
        Task { [weak self] in
            guard let self else { return }
            do {
                let response = try await self.transport.call(operation: command?.operation, sessionID: command?.id ?? "", visibleID: visibleID)
                guard !self.ended else { return }
                self.connected = true
                self.apply(response)
            } catch {
                guard !self.ended else { return }
                self.connected = false; self.preview.select(nil); self.canvas.image = nil
                self.canvas.message = L10n.text("Connection lost. The core pauses when the monitor lease expires.")
                self.detailLabel.stringValue = self.canvas.message
                self.pauseButton.isEnabled = false
                if self.closeRequested { self.stateLabel.stringValue = L10n.text("Stop not yet confirmed") }
            }
            self.busy = false
            if self.pending != nil { self.tick() }
        }
    }
    private func apply(_ next: ComputerUseState) {
        let changed = next.session_id != state?.session_id
        state = next
        if changed { collapsed = false; closeRequested = false; canvas.image = nil }
        titleLabel.stringValue = next.application.name.isEmpty ? L10n.text("Computer Use") : next.application.name
        let phase: String
        switch next.phase {
        case "running": phase = next.active_operations > 0 ? activityLabel(next.activity) : L10n.text("Waiting for next action")
        case "pausing": phase = L10n.text("Pausing · releasing input…")
        case "stopping": phase = L10n.text("Stopping · releasing input…")
        case "paused": phase = next.reason == "monitor_disconnected" ? L10n.text("Paused after monitor disconnected") : L10n.text("Paused")
        case "stopped", "closed": phase = L10n.text("Stopped")
        default: phase = L10n.text("Ready for a new task")
        }
        stateLabel.stringValue = phase + (next.mode == "foreground" ? " · " + L10n.text("Foreground control") : " · " + L10n.text("Background window"))
        pauseButton.title = next.can_resume ? L10n.text("Resume") : L10n.text("Pause")
        pauseButton.toolTip = L10n.text("Resume with fresh observation")
        pauseButton.isEnabled = next.can_resume || next.phase == "running"
        stopButton.isEnabled = next.isLive && !next.isDraining
        collapseButton.isEnabled = next.isLive
        statusItem.isVisible = next.enabled && !next.session_id.isEmpty && next.phase != "completed"
        statusItem.button?.title = " " + phase
        statusItem.button?.image = NSImage(systemSymbolName: "cursorarrow.rays", accessibilityDescription: L10n.text("Computer Use"))
        rebuildMenu()
        if next.isLive && !collapsed && !closeRequested { panel.orderFrontRegardless() }
        if next.phase == "stopped" || next.phase == "completed" || next.phase == "closed" { panel.orderOut(nil) }
        let target = connected && !collapsed && !closeRequested && next.shouldPreview ? next.window : nil
        preview.select(target)
        if target == nil { canvas.image = nil; canvas.message = phase; detailLabel.stringValue = L10n.text("Stops desktop control only, not other agent tools.") }
        canvas.targetBounds = next.window.bounds
        canvas.point = next.pointer
        if target != nil && preview.lastFrame != .distantPast && Date().timeIntervalSince(preview.lastFrame) > 2 {
            detailLabel.stringValue = L10n.text("Frame unchanged · window may be static or minimized")
        }
    }
    private func activityLabel(_ value: String) -> String {
        switch value {
        case "launch": return L10n.text("Opening application")
        case "permissions": return L10n.text("Waiting for permission")
        case "snapshot": return L10n.text("Observing window")
        case "type", "set_value", "key": return L10n.text("Entering input")
        case "click": return L10n.text("Clicking")
        case "drag": return L10n.text("Dragging")
        case "scroll": return L10n.text("Scrolling")
        default: return L10n.text("Operating window")
        }
    }
    private func rebuildMenu() {
        let menu = NSMenu()
        let show = menu.addItem(withTitle: L10n.text("Show Computer Use"), action: #selector(showPanel), keyEquivalent: ""); show.target = self
        let pause = menu.addItem(withTitle: pauseButton.title, action: #selector(togglePause), keyEquivalent: ""); pause.target = self; pause.isEnabled = pauseButton.isEnabled
        let stop = menu.addItem(withTitle: L10n.text("Stop desktop control"), action: #selector(stopAndClose), keyEquivalent: ""); stop.target = self; stop.isEnabled = state?.isLive == true
        menu.autoenablesItems = false; statusItem.menu = menu
    }
    private func command(_ operation: String) {
        guard let state else { return }
        if pending?.operation == "stop" && operation != "stop" { return }
        pending = (operation, state.session_id)
        pauseButton.isEnabled = false
        if operation == "stop" { closeRequested = true; stateLabel.stringValue = L10n.text("Stopping · releasing input…"); preview.select(nil); canvas.image = nil }
        tick()
    }
    @objc func togglePause() { if let state { command(state.can_resume ? "resume" : "pause") } }
    @objc func stopAndClose() { command("stop") }
    @objc func collapse() { collapsed = true; preview.select(nil); canvas.image = nil; panel.orderOut(nil) }
    @objc func showPanel() { collapsed = false; panel.orderFrontRegardless(); if let state { apply(state) } }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if state?.isLive == true { stopAndClose(); return false }
        return true
    }
}
