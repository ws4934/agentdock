import Foundation

struct TaskCenterItem: Decodable {
    struct Step: Decodable { let id: String; let title: String; let status: String }
    struct Receipt: Decodable {
        let job_id: String; let title: String; let status: String; let owner_alive: Bool
        var exit_code: Int?
        var validation_status: String?
        var source_revision: String?
        var source_head: String?
        var source_observed_at: String?
    }
    let id: String
    let title: String
    let status: String
    var goal: String?
    var project: String?
    var summary: String?
    var blocker: String?
    var updated_at: String?
    var revision: String?
    var archived_at: String?
    var step_count: Int?
    var completed_step_count: Int?
    var steps: [Step]?
    var current_step: Step?
    var job_receipts: [Receipt]?
    var jobs_available: Bool?
    var jobs_partial: Bool?

    static func validID(_ id: String) -> Bool {
        id.utf8.count == 20 && id.range(of: #"^tsk_[a-f0-9]{16}$"#, options: .regularExpression) != nil
    }
    var isArchived: Bool { archived_at != nil }
    var canDelete: Bool { status == "completed" && managementReference != nil }
    var managementReference: TaskCenterManagementRequest.Reference? {
        guard let revision, TaskCenterManagementRequest.validRevision(revision) else { return nil }
        return .init(id: id, revision: revision)
    }
    func validate() throws {
        guard Self.validID(id), title.count <= 4096, (steps?.count ?? 0) <= 12,
              ["active", "blocked", "completed"].contains(status),
              revision == nil || TaskCenterManagementRequest.validRevision(revision!) else { throw CocoaError(.coderReadCorrupt) }
    }
    // 只插入严格校验的 ID，不把任务标题、项目文件或任意历史文本变成指令。
    var continuationPrompt: String {
        guard Self.validID(id) else { return "" }
        return L10n.format("Continue AgentDock task %@. First read its current task state and existing job receipts. Verify the project and source revision, then continue only unfinished work within the original approved scope. Do not replay completed or unknown executions. If the task is completed, report its result instead of rerunning it.", id)
    }
    var statusText: String {
        let prefix = isArchived ? L10n.text("Archived") + " · " : ""
        return prefix + progressText
    }
    private var progressText: String {
        switch status {
        case "active": return L10n.text("In progress")
        case "blocked": return L10n.text("Blocked")
        case "completed": return L10n.text("Completed")
        default: return L10n.text("Unknown")
        }
    }
    var detailText: String {
        let pending = (steps ?? []).map { step in
            let symbol = step.status == "completed" ? "✓" : (step.status == "in_progress" ? "→" : "○")
            return "\(symbol) \(String(step.title.prefix(500)))"
        }.joined(separator: "\n")
        return [id, String((project ?? "").prefix(2048)), String((goal ?? "").prefix(4000)),
                String((summary ?? "").prefix(4000)), String((blocker ?? "").prefix(2000)), pending,
                L10n.text("Task progress is a saved checkpoint, not proof that a process is still running. Continue only after checking job receipts."), receiptText]
            .filter { !$0.isEmpty }.joined(separator: "\n\n")
    }

    private var receiptText: String {
        guard let receipts = job_receipts else { return "" }
        var lines = [L10n.text("Job receipts (historical; recheck before continuing)")]
        if jobs_available != true || jobs_partial == true { lines.append(L10n.text("Receipt list is partial or unavailable.")) }
        if receipts.isEmpty && jobs_available == true { lines.append(L10n.text("No managed-job receipts recorded.")) }
        for receipt in receipts.prefix(10) {
            lines.append("\(String(receipt.job_id.prefix(100))) · \(String(receipt.title.prefix(160)))\n\(String(receipt.status.prefix(64))) · owner_alive=\(receipt.owner_alive)")
            if let exit = receipt.exit_code { lines.append("exit_code=\(exit)") }
            if let validation = receipt.validation_status { lines.append("validation=\(String(validation.prefix(80)))") }
            for source in [receipt.source_revision, receipt.source_head, receipt.source_observed_at].compactMap({ $0 }) where !source.isEmpty {
                lines.append(String(source.prefix(160)))
            }
        }
        return lines.joined(separator: "\n")
    }
}

struct TaskCenterPage: Decodable {
    let ok: Bool
    let tasks: [TaskCenterItem]
    let partial: Bool?
    static func decode(_ data: Data) throws -> TaskCenterPage {
        guard data.count <= 512 * 1024 else { throw CocoaError(.fileReadTooLarge) }
        let value = try JSONDecoder().decode(Self.self, from: data)
        guard value.ok, value.tasks.count <= 200,
              Set(value.tasks.map(\.id)).count == value.tasks.count else { throw CocoaError(.coderReadCorrupt) }
        try value.tasks.forEach { try $0.validate() }
        return value
    }
    var isPartial: Bool { partial ?? (tasks.count >= 50) }
    static func decodeTask(_ data: Data, expectedID: String) throws -> TaskCenterItem {
        struct Envelope: Decodable {
            let ok: Bool; let task: TaskCenterItem
            var revision: String?
            var job_receipts: [TaskCenterItem.Receipt]?
            var jobs_available: Bool?
            var jobs_partial: Bool?
        }
        guard data.count <= 512 * 1024 else { throw CocoaError(.fileReadTooLarge) }
        let value = try JSONDecoder().decode(Envelope.self, from: data)
        guard value.ok, value.task.id == expectedID, (value.job_receipts?.count ?? 0) <= 10 else { throw CocoaError(.coderReadCorrupt) }
        try value.task.validate()
        var item = value.task
        item.revision = value.revision ?? item.revision
        try item.validate()
        item.job_receipts = value.job_receipts ?? []
        item.jobs_available = value.jobs_available
        item.jobs_partial = value.jobs_partial
        return item
    }
}


struct TaskCenterManagementRequest: Encodable {
    struct Reference: Encodable { let id: String; let revision: String }
    let action: String
    let tasks: [Reference]
    static func validRevision(_ value: String) -> Bool {
        value.utf8.count == 69 && value.range(of: #"^tsk1:[a-f0-9]{64}$"#, options: .regularExpression) != nil
    }
    func validate() throws {
        guard ["archive", "restore", "delete"].contains(action), !tasks.isEmpty, tasks.count <= 200,
              Set(tasks.map(\.id)).count == tasks.count,
              tasks.allSatisfy({ TaskCenterItem.validID($0.id) && Self.validRevision($0.revision) }) else { throw CocoaError(.coderReadCorrupt) }
    }
}

struct TaskCenterManagementResult: Decodable {
    struct Outcome: Decodable { let task_id: String; let ok: Bool; var code: String? }
    let ok: Bool
    let action: String
    let results: [Outcome]
    let changed: Int
    let failed: Int
    static func decode(_ data: Data, request: TaskCenterManagementRequest) throws -> Self {
        guard data.count <= 512 * 1024 else { throw CocoaError(.fileReadTooLarge) }
        let value = try JSONDecoder().decode(Self.self, from: data)
        guard value.ok, value.action == request.action, value.results.count == request.tasks.count,
              Set(value.results.map(\.task_id)) == Set(request.tasks.map(\.id)),
              value.changed == value.results.filter({ $0.ok }).count, value.failed == value.results.filter({ !$0.ok }).count else {
            throw CocoaError(.coderReadCorrupt)
        }
        return value
    }
    var summary: String {
        var lines = [L10n.format("%d tasks updated · %d not changed", changed, failed)]
        for result in results.filter({ !$0.ok }).prefix(8) {
            let reason: String
            switch result.code {
            case "TASK_CONFLICT": reason = L10n.text("Task changed. Refresh and select it again.")
            case "TASK_NOT_COMPLETED": reason = L10n.text("Unfinished tasks can be archived, not deleted.")
            case "TASK_JOBS_BUSY": reason = L10n.text("A related job is running or has an unknown outcome.")
            case "TASK_JOBS_UNAVAILABLE": reason = L10n.text("Job state could not be verified; deletion was refused.")
            case "TASK_NOT_FOUND": reason = L10n.text("Task no longer exists.")
            default: reason = L10n.text("Task could not be changed. Refresh before trying again.")
            }
            lines.append(result.task_id + ": " + reason)
        }
        return lines.joined(separator: "\n")
    }
}
