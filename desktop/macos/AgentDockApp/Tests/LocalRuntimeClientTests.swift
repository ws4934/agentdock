import Foundation

@main struct LocalRuntimeClientTests {
    static func main() async throws {
        guard CommandLine.arguments.count == 2, let port = Int(CommandLine.arguments[1]) else { fatalError("fixture port required") }
        func config(_ host: String = "127.0.0.1") -> ServiceConfiguration {
            ServiceConfiguration(host: host, port: port, publicURL: nil, authToken: "synthetic-test-token", oauthPassword: "", logLevel: "info", mcpAppsEnabled: true, browserEnabled: false, browserCDPURL: "", browserReuseExistingCDP: false, acpEnabled: false, acpProfiles: [], acpDefaultProfile: "")
        }
        let client = LocalRuntimeClient()
        let health = try await client.get(configuration: config(), path: "/healthz")
        precondition(String(data: health, encoding: .utf8) == "health-without-token")
        let authenticated = try await client.get(configuration: config(), path: "/internal/runtime/tasks")
        precondition(String(data: authenticated, encoding: .utf8) == "authenticated")
        for host in ["example.test", "192.0.2.1"] {
            do { _ = try LocalRuntimeClient.endpoint(configuration: config(host), path: "/healthz"); preconditionFailure("remote host allowed") }
            catch LocalRuntimeError.invalidEndpoint {}
        }
        for path in ["/mcp", "/internal/runtime/tasks/../settings", "/internal/runtime/tasks/tsk_0123456789abcdef\n"] {
            do { _ = try LocalRuntimeClient.endpoint(configuration: config(), path: path); preconditionFailure("unexpected path accepted") }
            catch LocalRuntimeError.invalidEndpoint {}
        }
        for fixture in ["large", "large-stream"] {
            do { _ = try await client.get(configuration: config(), path: "/healthz", query: [.init(name: "fixture", value: fixture)]); preconditionFailure("oversize body accepted") }
            catch LocalRuntimeError.responseTooLarge {}
        }
        do { _ = try await client.get(configuration: config(), path: "/internal/runtime/tasks", query: [.init(name: "fixture", value: "redirect")]); preconditionFailure("redirect accepted") }
        catch LocalRuntimeError.httpStatus(let code) { precondition(code == 302) }
        do { _ = try await client.get(configuration: config(), path: "/healthz", query: [.init(name: "fixture", value: "slow")], timeout: 0.2); preconditionFailure("timeout not enforced") }
        catch is URLError {}
        print("Local runtime HTTP tests passed: local-only endpoints, authentication, no redirect, response bounds and timeout")
    }
}
