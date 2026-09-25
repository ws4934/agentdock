import Foundation

// Only the typed, allowlisted endpoint is displayed. Never fall back to copying
// environment files, arbitrary logs, command output or private project files.
struct SafeDiagnostics: Decodable {
    struct Build: Decodable { let version: String; let commit: String; let platform: String }
    struct Layer: Decodable { let status: String; let reason: String }
    let schema_version: Int
    let observed_at: String
    let build: Build
    let layers: [String: Layer]
    let trace_mode: String
    let tool_count: Int
    let managed_jobs: [String: Int]
    let managed_jobs_partial: Bool
    let sensitive_data_included: Bool

    static func decode(_ data: Data) throws -> SafeDiagnostics {
        struct Envelope: Decodable { let diagnostics: SafeDiagnostics }
        guard data.count <= 256 * 1024 else { throw CocoaError(.fileReadTooLarge) }
        let result = try JSONDecoder().decode(Envelope.self, from: data).diagnostics
        guard result.schema_version == 1, !result.sensitive_data_included,
              result.layers.count <= 20, result.managed_jobs.count <= 20,
              (0...4096).contains(result.tool_count),
              result.managed_jobs.values.allSatisfy({ (0...10000).contains($0) }) else {
            throw CocoaError(.coderReadCorrupt)
        }
        return result
    }

    func displayText() -> String {
        let names: [(String, String)] = [
            ("core", "Core"), ("authentication", L10n.text("Authentication")),
            ("public_endpoint", L10n.text("Public address")), ("tunnel", L10n.text("Tunnel")),
            ("managed_jobs", L10n.text("Managed jobs")),
            ("response_delivery", L10n.text("Response delivery")),
            ("ui_rendering", L10n.text("Feedback rendering"))
        ]
        func bounded(_ value: String) -> String { String(value.prefix(200)) }
        var lines = ["AgentDock \(bounded(build.version)) · \(bounded(build.commit))", bounded(build.platform), ""]
        for (key, name) in names {
            guard let layer = layers[key] else { continue }
            lines.append("\(name): \(bounded(layer.status))")
            lines.append("  \(bounded(layer.reason))")
        }
        lines += ["", "\(L10n.text("Managed jobs")): \(managed_jobs.values.reduce(0,+))\(managed_jobs_partial ? "+" : "")", "\(L10n.text("Tools")): \(tool_count)", "", L10n.text("Execution completion does not prove client receipt or feedback rendering. Unknown states are not reported as healthy.")]
        return lines.joined(separator: "\n")
    }
}
