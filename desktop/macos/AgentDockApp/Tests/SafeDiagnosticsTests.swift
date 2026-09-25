import Foundation

@main
struct SafeDiagnosticsTests {
    static func main() throws {
        let base: [String: Any] = [
            "schema_version": 1, "observed_at": "2026-09-25T00:00:00Z",
            "build": ["version": "0.8.5", "commit": "fixture", "platform": "darwin/arm64"],
            "layers": ["core": ["status": "ready", "reason": "local_runtime_responded"], "ui_rendering": ["status": "unknown", "reason": "host_not_observed"]],
            "trace_mode": "metadata_only", "tool_count": 35, "managed_jobs": ["running": 1],
            "managed_jobs_partial": false, "sensitive_data_included": false,
            "unexpected_config": "DO-NOT-EXPORT-ME"
        ]
        func data(_ values: [String: Any]) throws -> Data { try JSONSerialization.data(withJSONObject: ["diagnostics": values]) }
        let value = try SafeDiagnostics.decode(data(base))
        precondition(value.displayText().contains("unknown"))
        precondition(!value.displayText().contains("DO-NOT-EXPORT-ME"))
        for replacement: [String: Any] in [
            ["schema_version": 999], ["sensitive_data_included": true],
            ["tool_count": -1], ["managed_jobs": ["running": Int.max]],
            ["managed_jobs": ["running": -1]]
        ] {
            var bad = base
            for (key, value) in replacement { bad[key] = value }
            do { _ = try SafeDiagnostics.decode(data(bad)); preconditionFailure("invalid safe report accepted") } catch { }
        }
        do { _ = try SafeDiagnostics.decode(Data(repeating: 32, count: 262145)); preconditionFailure("oversize report accepted") } catch { }
        print("Safe diagnostic projection tests passed: unknown states, bounds, schema, sensitive flag and unknown-field exclusion")
    }
}
