import AppKit
import Foundation

@MainActor final class TaskCenterWindowController: NSWindowController, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate {
    private let service: ServiceController
    private let presentWindow: @MainActor (NSWindowController) -> Void
    private let client = LocalRuntimeClient()
    private let table = NSTableView()
    private let search = NSSearchField()
    private let filter = NSSegmentedControl(labels: [L10n.text("All"), L10n.text("In progress"), L10n.text("Blocked"), L10n.text("Completed"), L10n.text("Archived")], trackingMode: .selectOne, target: nil, action: nil)
    private let detail = NSTextView()
    private let titleField = NSTextField(labelWithString: L10n.text("Select a task"))
    private let stateField = NSTextField(labelWithString: "")
    private let notice = NSTextField(wrappingLabelWithString: "")
    private let selectionLabel = NSTextField(labelWithString: "")
    private let copyButton = NSButton(title: L10n.text("Copy continuation prompt"), target: nil, action: nil)
    private let refreshButton = NSButton(title: L10n.text("Refresh"), target: nil, action: nil)
    private let archiveButton = NSButton(title: L10n.text("Archive selected"), target: nil, action: nil)
    private let restoreButton = NSButton(title: L10n.text("Restore selected"), target: nil, action: nil)
    private let deleteButton = NSButton(title: L10n.text("Delete selected…"), target: nil, action: nil)
    private let cleanupButton = NSButton(title: L10n.text("Clear displayed completed…"), target: nil, action: nil)
    private var items: [TaskCenterItem] = []
    private var visible: [TaskCenterItem] = []
    private var selected: TaskCenterItem?
    private var listTask: Task<Void, Never>?
    private var detailTask: Task<Void, Never>?
    private var mutationTask: Task<Void, Never>?
    private var listGeneration = 0
    private var detailGeneration = 0
    private var selectionID: String?
    private var exactSelection = false
    private var reloadingTable = false
    private var listFresh = false
    private var managementBusy = false
    private var operationNotice = ""

    init(service: ServiceController, presentWindow: @escaping @MainActor (NSWindowController) -> Void = { ManagementWindowPresenter.present($0) }) {
        self.service = service
        self.presentWindow = presentWindow
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 960, height: 650),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        super.init(window: window)
        window.title = L10n.text("Task center"); window.minSize = NSSize(width: 800, height: 500)
        window.isReleasedWhenClosed = false; window.delegate = self
        window.center(); window.setFrameAutosaveName("AgentDockTaskCenter")
        configureUI(); updateManagementControls()
    }
    required init?(coder: NSCoder) { nil }
    func present() { presentWindow(self); if !managementBusy { refresh() } }
    func windowShouldClose(_ sender: NSWindow) -> Bool { !managementBusy }
    func windowWillClose(_ notification: Notification) {
        listGeneration += 1; detailGeneration += 1; listFresh = false
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
        archiveButton.target = self; archiveButton.action = #selector(archiveSelected)
        restoreButton.target = self; restoreButton.action = #selector(restoreSelected)
        deleteButton.target = self; deleteButton.action = #selector(deleteSelected)
        cleanupButton.target = self; cleanupButton.action = #selector(cleanupCompleted)
        for (button, id) in [(archiveButton, "task.archive"), (restoreButton, "task.restore"), (deleteButton, "task.delete"), (cleanupButton, "task.cleanup"), (copyButton, "task.copy")] {
            button.bezelStyle = .rounded; button.identifier = NSUserInterfaceItemIdentifier(id)
        }
        deleteButton.toolTip = L10n.text("Unfinished tasks can be archived, not deleted.")
        cleanupButton.toolTip = L10n.text("Only the completed tasks in this displayed, filtered list are selected. Older or hidden tasks are not affected.")
        let tools = NSStackView(views: [filter, search, refreshButton]); tools.spacing = 10
        search.widthAnchor.constraint(greaterThanOrEqualToConstant: 180).isActive = true
        selectionLabel.font = .systemFont(ofSize: 12); selectionLabel.textColor = .secondaryLabelColor
        let management = NSStackView(views: [selectionLabel, NSView(), archiveButton, restoreButton, deleteButton, cleanupButton]); management.spacing = 10
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("task"))
        column.width = 280; table.addTableColumn(column); table.headerView = nil
        table.rowHeight = 66; table.delegate = self; table.dataSource = self
        table.style = .inset; table.allowsEmptySelection = true; table.allowsMultipleSelection = true
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
        notice.maximumNumberOfLines = 4; notice.lineBreakMode = .byTruncatingTail
        let footer = ManagementUI.label(L10n.text("Archive only hides a task; it does not stop execution. Deletion removes completed task records, not job receipts, logs or delivered files."), size: 12, secondary: true)
        for view in [heading, tools, management, split, notice, footer] { view.translatesAutoresizingMaskIntoConstraints = false; content.addSubview(view) }
        NSLayoutConstraint.activate([
            heading.topAnchor.constraint(equalTo: content.topAnchor, constant: 22),
            heading.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 24),
            heading.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -24),
            tools.topAnchor.constraint(equalTo: heading.bottomAnchor, constant: 18),
            tools.leadingAnchor.constraint(equalTo: heading.leadingAnchor), tools.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            management.topAnchor.constraint(equalTo: tools.bottomAnchor, constant: 12),
            management.leadingAnchor.constraint(equalTo: heading.leadingAnchor), management.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            split.topAnchor.constraint(equalTo: management.bottomAnchor, constant: 14),
            split.leadingAnchor.constraint(equalTo: heading.leadingAnchor), split.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            split.bottomAnchor.constraint(equalTo: notice.topAnchor, constant: -12),
            notice.leadingAnchor.constraint(equalTo: heading.leadingAnchor), notice.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            notice.bottomAnchor.constraint(equalTo: footer.topAnchor, constant: -6),
            footer.leadingAnchor.constraint(equalTo: heading.leadingAnchor), footer.trailingAnchor.constraint(equalTo: heading.trailingAnchor),
            footer.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -18)
        ])
    }

    @objc private func refresh() {
        guard !managementBusy else { return }
        listGeneration += 1; let generation = listGeneration
        listTask?.cancel(); detailTask?.cancel(); detailGeneration += 1
        listFresh = false; selected = nil; copyButton.isEnabled = false
        refreshButton.isEnabled = false; updateManagementControls()
        showNotice(L10n.text("Reading saved tasks…"))
        let statuses = ["", "active", "blocked", "completed", "archived"]
        let status = statuses.indices.contains(filter.selectedSegment) ? statuses[filter.selectedSegment] : ""
        listTask = Task { [weak self] in
            guard let self else { return }
            defer { if generation == listGeneration { refreshButton.isEnabled = true; listTask = nil; updateManagementControls() } }
            do {
                guard let config = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let data = try await client.get(configuration: config, path: "/internal/runtime/tasks", query: [URLQueryItem(name: "limit", value: "200"), URLQueryItem(name: "status", value: status)])
                let page = try TaskCenterPage.decode(data)
                guard !Task.isCancelled, generation == listGeneration else { return }
                items = page.tasks; listFresh = true
                showNotice(page.isPartial ? L10n.text("Partial list: showing the most recent tasks. Use filters or an exact task ID for older entries.") : L10n.format("%d saved tasks", items.count))
                applyFilter()
            } catch {
                guard !Task.isCancelled, generation == listGeneration else { return }
                showNotice(L10n.text("Core is unavailable. Any tasks still displayed are an earlier observation, not current execution status."))
            }
        }
    }
    private func showNotice(_ text: String) {
        notice.stringValue = [operationNotice, text].filter { !$0.isEmpty }.joined(separator: "\n")
        notice.toolTip = notice.stringValue
    }
    func controlTextDidChange(_ obj: Notification) { if !managementBusy { applyFilter() } }
    @objc private func searchSubmitted() {
        guard !managementBusy else { return }
        let id = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if TaskCenterItem.validID(id) {
            reloadingTable = true; table.deselectAll(nil); reloadingTable = false
            exactSelection = true; loadDetail(id)
        }
    }
    private var selection: [TaskCenterItem] {
        if exactSelection { return selected.map { [$0] } ?? [] }
        let rows = table.selectedRowIndexes.compactMap { visible.indices.contains($0) ? visible[$0] : nil }
        if rows.count == 1, let selected, selected.id == rows[0].id { return [selected] }
        return rows
    }
    private func applyFilter() {
        let previous = Set(selection.map(\.id))
        let query = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        visible = query.isEmpty ? items : items.filter { ($0.title + " " + $0.id + " " + ($0.project ?? "")).localizedCaseInsensitiveContains(query) }
        exactSelection = false; selected = nil
        reloadingTable = true
        table.deselectAll(nil); table.reloadData()
        var indexes = IndexSet(visible.indices.filter { previous.contains(visible[$0].id) })
        if indexes.isEmpty && !visible.isEmpty { indexes.insert(0) }
        table.selectRowIndexes(indexes, byExtendingSelection: false)
        reloadingTable = false; selectionChanged()
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
        guard !reloadingTable else { return }
        exactSelection = false; selectionChanged()
    }
    private func selectionChanged() {
        detailTask?.cancel(); detailGeneration += 1; selected = nil; selectionID = nil; copyButton.isEnabled = false
        let selectedItems = selection
        updateManagementControls()
        if selectedItems.count == 1, let item = selectedItems.first {
            titleField.stringValue = String(item.title.prefix(500)); stateField.stringValue = item.statusText
            detail.string = item.detailText
            if listFresh { loadDetail(item.id) }
        } else {
            titleField.stringValue = selectedItems.isEmpty ? L10n.text("Select a task") : L10n.format("%d tasks selected", selectedItems.count)
            stateField.stringValue = selectedItems.isEmpty ? "" : L10n.text("Use Command-click or Shift-click to select multiple tasks.")
            detail.string = selectedItems.map { String($0.title.prefix(160)) + "\n" + $0.id + " · " + $0.statusText }.joined(separator: "\n\n")
        }
    }
    private func loadDetail(_ id: String) {
        guard TaskCenterItem.validID(id) else { return }
        selectionID = id; selected = nil; detailGeneration += 1; let generation = detailGeneration
        detailTask?.cancel(); copyButton.isEnabled = false; updateManagementControls()
        detailTask = Task { [weak self] in
            guard let self else { return }
            defer { if generation == detailGeneration { detailTask = nil; updateManagementControls() } }
            do {
                guard let config = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let data = try await client.get(configuration: config, path: "/internal/runtime/tasks/" + id)
                let item = try TaskCenterPage.decodeTask(data, expectedID: id)
                guard !Task.isCancelled, generation == detailGeneration else { return }
                selected = item; titleField.stringValue = String(item.title.prefix(500)); stateField.stringValue = item.statusText
                detail.string = item.detailText; copyButton.isEnabled = !managementBusy
            } catch {
                guard !Task.isCancelled, generation == detailGeneration else { return }
                showNotice(L10n.text("The selected task could not be read. No operation was started."))
            }
        }
    }
    private func updateManagementControls() {
        let selectedItems = selection
        let ready = !managementBusy && (exactSelection ? selected != nil : listFresh)
        let valid = ready && !selectedItems.isEmpty && selectedItems.allSatisfy { $0.managementReference != nil }
        selectionLabel.stringValue = L10n.format("%d selected", selectedItems.count)
        archiveButton.isHidden = filter.selectedSegment == 4 && !exactSelection
        restoreButton.isHidden = !archiveButton.isHidden && !selectedItems.contains { $0.isArchived }
        archiveButton.isEnabled = valid && selectedItems.allSatisfy { !$0.isArchived }
        restoreButton.isEnabled = valid && selectedItems.allSatisfy { $0.isArchived }
        deleteButton.isEnabled = valid && selectedItems.allSatisfy { $0.canDelete }
        cleanupButton.isEnabled = !managementBusy && listFresh && visible.contains { $0.canDelete }
        filter.isEnabled = !managementBusy; search.isEnabled = !managementBusy
        table.isEnabled = !managementBusy
        copyButton.isEnabled = !managementBusy && selected != nil && selected?.id == selectionID
        if managementBusy { refreshButton.isEnabled = false }
    }
    @objc private func archiveSelected() { confirmManagement("archive", tasks: selection.filter { !$0.isArchived }) }
    @objc private func restoreSelected() { confirmManagement("restore", tasks: selection.filter { $0.isArchived }) }
    @objc private func deleteSelected() { confirmManagement("delete", tasks: selection) }
    @objc private func cleanupCompleted() { confirmManagement("delete", tasks: visible.filter { $0.canDelete }) }
    private func confirmManagement(_ action: String, tasks: [TaskCenterItem]) {
        guard !managementBusy, let window, !tasks.isEmpty,
              (exactSelection ? selected != nil : listFresh),
              action != "delete" || tasks.allSatisfy({ $0.canDelete }) else { return }
        let request = TaskCenterManagementRequest(action: action, tasks: tasks.compactMap(\.managementReference))
        guard request.tasks.count == tasks.count, (try? request.validate()) != nil else { return }
        // 确认框绑定的是当时的 ID + 版本；刷新、过滤和后台变化不能扩大写入范围。
        managementBusy = true; updateManagementControls()
        let alert = NSAlert(); alert.alertStyle = action == "delete" ? .warning : .informational
        let title = action == "delete" ? L10n.format("Delete %d completed task records?", tasks.count) :
            (action == "archive" ? L10n.format("Archive %d tasks?", tasks.count) : L10n.format("Restore %d tasks?", tasks.count))
        alert.messageText = title
        alert.informativeText = action == "delete" ? L10n.text("This cannot be undone. Only the selected task records are removed. Job receipts, execution logs, frozen deliveries and files are kept. Running or unknown jobs prevent deletion.") :
            L10n.text("Archive only changes list visibility. Progress, exact-ID access and job execution are unchanged; archived tasks can be restored here.")
        alert.addButton(withTitle: action == "delete" ? L10n.text("Delete") : (action == "archive" ? L10n.text("Archive") : L10n.text("Restore")))
        alert.addButton(withTitle: L10n.text("Cancel"))
        alert.beginSheetModal(for: window) { [weak self] response in
            guard let self else { return }
            if response == .alertFirstButtonReturn { self.performManagement(request) }
            else { self.managementBusy = false; self.refreshButton.isEnabled = true; self.updateManagementControls() }
        }
        // NSAlert 在展示时会重新分配默认键，必须在创建 sheet 后绑定取消操作。
        if action == "delete" {
            alert.buttons[0].keyEquivalent = ""
            alert.buttons[1].keyEquivalent = "\r"
            alert.window.defaultButtonCell = alert.buttons[1].cell as? NSButtonCell
        }
    }
    private func performManagement(_ request: TaskCenterManagementRequest) {
        mutationTask = Task { [weak self] in
            guard let self else { return }
            defer {
                managementBusy = false; mutationTask = nil; selected = nil; selectionID = nil
                refresh()
            }
            do {
                guard let config = ServiceConfiguration.load(from: service.paths.environment) else { throw LocalRuntimeError.invalidEndpoint }
                let result = try await client.manageTasks(configuration: config, request: request)
                operationNotice = result.summary
            } catch {
                // 超时不等于未执行；只刷新观察结果，绝不自动重放写请求。
                operationNotice = L10n.text("The operation result could not be confirmed. The list will be refreshed; no write request will be retried automatically.")
            }
        }
    }
    @objc private func copyContinuation() {
        guard !managementBusy, let selected, selected.id == selectionID, !selected.continuationPrompt.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(selected.continuationPrompt, forType: .string)
        operationNotice = ""
        showNotice(L10n.text("Continuation prompt copied. Paste it into ChatGPT; no task has been started by this panel."))
    }
}
