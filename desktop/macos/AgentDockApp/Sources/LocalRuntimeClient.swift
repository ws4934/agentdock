import Foundation

private final class LocalRuntimeSessionDelegate: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}

enum LocalRuntimeError: Error { case invalidEndpoint, responseTooLarge, httpStatus(Int) }

// 原生管理面板只访问固定 loopback API，令牌不进入 URL、日志、剪贴板或导出。
final class LocalRuntimeClient: @unchecked Sendable {
    private let delegate = LocalRuntimeSessionDelegate()
    private let session: URLSession
    init() {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.connectionProxyDictionary = [:]
        configuration.httpShouldSetCookies = false
        configuration.httpCookieStorage = nil
        configuration.urlCache = nil
        session = URLSession(configuration: configuration, delegate: delegate, delegateQueue: nil)
    }
    deinit { session.invalidateAndCancel() }

    static func endpoint(configuration: ServiceConfiguration, path: String, query: [URLQueryItem] = []) throws -> URL {
        let allowed = path == "/healthz" || path == "/internal/runtime/diagnostics" || path == "/internal/runtime/tasks"
            || path.range(of: #"^/internal/runtime/tasks/tsk_[a-f0-9]{16}\z"#, options: .regularExpression) != nil
        guard allowed, let local = configuration.localMCPURL,
              var url = URLComponents(url: local, resolvingAgainstBaseURL: false),
              url.scheme == "http", ["localhost", "127.0.0.1", "::1", "[::1]"].contains(url.host ?? ""),
              url.user == nil, url.password == nil else { throw LocalRuntimeError.invalidEndpoint }
        url.path = path; url.queryItems = query.isEmpty ? nil : query; url.fragment = nil
        guard let endpoint = url.url else { throw LocalRuntimeError.invalidEndpoint }
        return endpoint
    }

    func get(configuration: ServiceConfiguration, path: String, query: [URLQueryItem] = [], timeout: TimeInterval = 5) async throws -> Data {
        let endpoint = try Self.endpoint(configuration: configuration, path: path, query: query)
        var request = URLRequest(url: endpoint, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: timeout)
        if path != "/healthz", !configuration.authToken.isEmpty {
            request.setValue("Bearer \(configuration.authToken)", forHTTPHeaderField: "Authorization")
        }
        let (bytes, response) = try await session.bytes(for: request)
        defer { bytes.task.cancel() }
        guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
            throw LocalRuntimeError.httpStatus((response as? HTTPURLResponse)?.statusCode ?? 0)
        }
        let maximum = path == "/healthz" ? 4096 : 512 * 1024
        guard response.expectedContentLength <= Int64(maximum) else { throw LocalRuntimeError.responseTooLarge }
        var data = Data()
        for try await byte in bytes {
            try Task.checkCancellation()
            guard data.count < maximum else { throw LocalRuntimeError.responseTooLarge }
            data.append(byte)
        }
        return data
    }
}
