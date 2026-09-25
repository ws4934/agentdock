import AppKit
import Foundation

@MainActor final class TaskCenterWindowController: NSWindowController, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate {
    private let service: ServiceController
    private let client = LocalRuntimeClient()
    private let table = NSTableView()
    private let search = NSSearchField()
    private let filter = NSSegmentedControl(labels: [L10n.text("All"), L10n.text("In progress"), L10n.text("Blocked"), L10n.text("Completed")], trackingMode: .selectOne, target: nil, action: nil)
    private let detail = NSTextView()
    private let titleField = NSTextField(labelWithString: L10n.text("Select a task"))
    private let stateField = NSTextField(labelWithString: "")
    private let notice = NSTextField(wrappingLabelWithString: "")
    private let copyButton = NSButton(title: L10n.text("Copy continuation prompt"), target: nil, action: nil)
    private let refreshButton = NSButton(title: L10n.text("Refresh"), target: nil, action: nil)
    private var items: [TaskCenterItem] = []
    private var visible: [TaskCenterItem] = []
    private var selected: TaskCenterItem?
    private var listTask: Task<Void, Never>?
    private var detailTask: Task<Void, Never>?
    private var listGeneration = 0
    private var detailGeneration = 0
    private var selectionID: String?

    init(service: ServiceController) {
        self.service = service
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 900, height: 590),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        super.init(window: window)
        window.title = L10n.text("Task center"); window.minSize = NSSize(width: 740, height: 420)
        window.isReleasedWhenClosed = false; window.delegate = self
        window.setFrameAutosaveName("AgentDockTaskCenter")
        configureUI()
    }
    required init?(coder: NSCoder) { nil }
    func present() { showWindow(nil); window?.makeKeyAndOrderFront(nil); refresh() }
    func windowWillClose(_ notification: Notification) {
        listGeneration += 1; detailGeneration += 1
        listTask?.cancel(); detailTask?.cancel(); listTask = nil; detailTask = nil
    }

    private func configureUI() {
        guard let content = window?.contentView else { return }
        content.wantsLayer = true; content.layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        let heading = ManagementUI.column([
            ManagementUI.label(L10n.text("Task center"), size: 24, weight: .semibold),
            ManagementUI.label(L10n.text("A stable entry point across chats. Tasks stay on this device, not inside a message card."), secondary: true)
        ], spacing: 5)
        search.placeholderString = L10n.text("Filter tasks or enter an exact task ID")
        search.delegate = self; search.target = self; search.action = #selector(searchSubmitted)
        filter.selectedSegment = 0; filter.target = self; filter.action = #selector(refresh)
        refreshButton.target = self; refreshButton.action = #selector(refresh)
        copyButton.target = self; copyButton.action = #selector(copyContinuation); copyButton.isEnabled = false
        copyButton.bezelStyle = .rounded
        let tools = NSStackView(views: [filter, search, refreshButton]); tools.spacing = 10
        search.widthAnchor.constraint(greaterThanOrEqualToConstant: 180).isActive = true
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("task"))
        column.width = 280; table.addTableColumn(column); table.headerView = nil
        table.rowHeight = 66; table.delegate = self; table.dataSource = self
        table.style = .inset; table.allowsEmptySelection = true
        let list = NSScrollView(); list.hasVerticalScroller = true; list.autohidesScrollers = true
        list.documentView = table; list.borderType = .noBorder
        titleField.font = .systemFont(ofSize: 19, weight: .semibold); titleField.maximumNumberOfLines = 2
        titleField.lineBreakMode = .byTruncatingTail; stateField.textColor = .secondaryLabelColor
        let detailScroll = ManagementUI.textScroll(detail)
        let detailBody = ManagementUI.column([titleField, stateField, detailScroll, copyButton], spacing: 12)
        let detailCard = ManagementUI.card(detailBody)
        let split = NSSplitView(); split.isVertical = true; split.dividerStyle = .thin
        split.addArrangedSubview(list); split.addArrangedSubview(detailCard)
        list.widthAnchor.constraint(greaterThanOrEqualToConstant: 230).isActive = true
        list.widthAnchor.constraint(lessThanOrEqualToConstant: 370).isActive = true
        detailCard.widthAnchor.constraint(greaterThanOrEqualToConstant: 380).isActive = true
        notice.font = .systemFont(ofSize: 12); notice.textColor = .secondaryLabelColor
        let footer = ManagementUI.label(L10n.text("Copy the continuation prompt into a new chat. This panel never runs or replays a command."), size: 12, secondary: true)
        for view in [heading, tools, split, notice, footer] { view.translatesAutoresizingMaskIntoConstraints = false; content.addSubview(view) }
        NSLayoutConstraint.activate([
            heading.topAnchor.constraint(equalTo: content.topAnchor, constant: 22),
            heading.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 24),
            heading.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -24),
            tools.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: 18),
            tools.leadingAnchor.constraint(equalTo: heading.leadingAnchor), tools.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            split.topAnchor.constraint(equalTo: tools.bottomAnchor, constant: 16),
            split.leadingAnchor.constraint(equalTo: heading.leadingAnchor), split.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            split.bottomAnchor.constraint(equalTo: notice.topAnchor, constant: -12),
            notice.leadingAnchor.constraint(equalTo: heading.leadingAnchor), notice.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            notice.bottomAnchor.constraint(equalTo: footer.topAnchor, constant: -6),
            footer.leadingAnchor.constraint(equalTo: heading.leadingAnchor), footer.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            footer.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -18)
        ])
    }

    @objc private func refresh() {
        listGeneration += 1; let generation = listGeneration
        listTask?.cancel(); detailTask?.cancel(); detailGeneration += 1
        refreshButton.isEnabled = false; copyButton.isEnabled = false
        notice.stringValue = L10n.text("Reading saved tasks…")
        let status = ["", "active", "blocked", "completed"][max(0, filter.selectedSegment)]
        listTask = Task { [weak self] in
            guard let self else { return }
            defer { if generation == listGeneration { refreshButton.isEnabled = true; listTask = nil } }
            do {
                guard let config = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let data = try await client.get(configuration: config, path: "/internal/runtime/tasks", query: [URLQueryItem(name: "limit", value: "50"), URLQueryItem(name: "status", value: status)])
                let page = try TaskCenterPage.decode(data)
                guard !Task.isCancelled, generation == listGeneration else { return }
                items = page.tasks
                notice.stringValue = page.isPartial ? L10n.text("Partial list: showing the most recent tasks. Use filters or an exact task ID for older entries.") : L10n.format("%d saved tasks · read-only observation", items.count)
                applyFilter()
            } catch {
                guard !Task.isCancelled, generation == listGeneration else { return }
                notice.stringValue = L10n.text("Core is unavailable. Any tasks still displayed are an earlier observation, not current execution status.")
            }
        }
    }
    func controlTextDidChange(_ obj: Notification) { applyFilter() }
    @objc private func searchSubmitted() {
        let id = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if TaskCenterItem.validID(id) { loadDetail(id) }
    }
    private func applyFilter() {
        let query = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        visible = query.isEmpty ? items : items.filter { ($0.title + " " + $0.id + " " + ($0.project ?? "")).localizedCaseInsensitiveContains(query) }
        // reload 后相同索引不会保证触发 selection 通知；先清空选区再精确重选。
        table.deselectAll(nil)
        table.reloadData()
        if let id = selectionID, let index = visible.firstIndex(where: { $0.id == id }) {
            table.selectRowIndexes(IndexSet(integer: index), byExtendingSelection: false)
        } else if !visible.isEmpty {
            table.selectRowIndexes(IndexSet(integer: 0), byExtendingSelection: false)
        } else {
            table.deselectAll(nil); selected = nil; copyButton.isEnabled = false
            detailTask?.cancel(); detailGeneration += 1
            titleField.stringValue = L10n.text("Select a task"); stateField.stringValue = ""; detail.string = ""
        }
    }
    func numberOfRows(in tableView: NSTableView) -> Int { visible.count }
    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard visible.indices.contains(row) else { return nil }
        let item = visible[row]
        let title = ManagementUI.label(String(item.title.prefix(160)), size: 13, weight: .semibold)
        title.maximumNumberOfLines = 1; title.lineBreakMode = .byTruncatingTail
        let subtitle = ManagementUI.label(item.statusText + " · " + String((item.current_step?.title ?? item.id).prefix(100)), size: 11, secondary: true)
        subtitle.maximumNumberOfLines = 1; subtitle.lineBreakMode = .byTruncatingTail
        return ManagementUI.column([title, subtitle], spacing: 5)
    }
    func tableViewSelectionDidChange(_ notification: Notification) {
        guard visible.indices.contains(table.selectedRow) else { return }
        let item = visible[table.selectedRow]
        titleField.stringValue = String(item.title.prefix(500)); stateField.stringValue = item.statusText
        detail.string = item.detailText; selected = nil; copyButton.isEnabled = false
        loadDetail(item.id)
    }
    private func loadDetail(_ id: String) {
        guard TaskCenterItem.validID(id) else { return }
        selectionID = id; detailGeneration += 1; let generation = detailGeneration
        detailTask?.cancel(); copyButton.isEnabled = false
        detailTask = Task { [weak self] in
            guard let self else { return }
            do {
                guard let config = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let data = try await client.get(configuration: config, path: "/internal/runtime/tasks/" + id)
                let item = try TaskCenterPage.decodeTask(data, expectedID: id)
                guard !Task.isCancelled, generation == detailGeneration else { return }
                selected = item; titleField.stringValue = String(item.title.prefix(500)); stateField.stringValue = item.statusText
                detail.string = item.detailText; copyButton.isEnabled = true
            } catch {
                guard !Task.isCancelled, generation == detailGeneration else { return }
                notice.stringValue = L10n.text("The selected task could not be read. No operation was started.")
            }
        }
    }
    @objc private func copyContinuation() {
        guard let selected, !selected.continuationPrompt.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(selected.continuationPrompt, forType: .string)
        notice.stringValue = L10n.text("Continuation prompt copied. Paste it into ChatGPT; no task has been started by this panel.")
    }
}
