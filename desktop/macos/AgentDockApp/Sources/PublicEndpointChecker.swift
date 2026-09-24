import Foundation

struct PublicEndpointCheckResult: Equatable, Sendable {
    let isReachable: Bool
    let message: String
    let latencyMilliseconds: Int?
}

final class PublicEndpointChecker: @unchecked Sendable {
    private let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    func check(publicMCPURL: URL) async -> PublicEndpointCheckResult {
        guard let healthURL = Self.healthURL(from: publicMCPURL) else {
            return PublicEndpointCheckResult(
                isReachable: false,
                message: L10n.text("Invalid public address"),
                latencyMilliseconds: nil
            )
        }

        var request = URLRequest(
            url: healthURL,
            cachePolicy: .reloadIgnoringLocalAndRemoteCacheData,
            timeoutInterval: 8
        )
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")

        let startedAt = Date()
        do {
            let (data, response) = try await session.data(for: request)
            let latency = max(0, Int(Date().timeIntervalSince(startedAt) * 1_000))
            guard let response = response as? HTTPURLResponse else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: L10n.text("The public address did not return an HTTP response"),
                    latencyMilliseconds: latency
                )
            }
            guard response.statusCode == 200 else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: Self.httpErrorMessage(statusCode: response.statusCode, body: data),
                    latencyMilliseconds: latency
                )
            }
            guard let payload = try? JSONDecoder().decode(PublicHealthPayload.self, from: data), payload.ok else {
                return PublicEndpointCheckResult(
                    isReachable: false,
                    message: L10n.text("The public health check returned invalid data"),
                    latencyMilliseconds: latency
                )
            }
            return PublicEndpointCheckResult(
                isReachable: true,
                message: L10n.text("Reachable"),
                latencyMilliseconds: latency
            )
        } catch {
            return PublicEndpointCheckResult(
                isReachable: false,
                message: Self.networkErrorMessage(error),
                latencyMilliseconds: nil
            )
        }
    }

    static func httpErrorMessage(statusCode: Int, body: Data) -> String {
        // 530 只是外层状态码。只提取有明确标记的 1xxx 错误，不展示远端 HTML。
        if statusCode == 530,
           let text = String(data: body.prefix(64 * 1024), encoding: .utf8),
           let regex = try? NSRegularExpression(pattern: #"(?:error(?:\s+code)?\s*[:=]?\s*|cf-error-code["']?\s*>\s*)(1\d{3})\b"#, options: .caseInsensitive),
           let match = regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
           let range = Range(match.range(at: 1), in: text), let code = Int(text[range]) {
            switch code {
            case 1033: return L10n.text("Cloudflare Tunnel disconnected (HTTP 530 / 1033)")
            case 1016: return L10n.text("Cloudflare origin DNS error (HTTP 530 / 1016)")
            default: return L10n.format("Cloudflare error %d (HTTP 530)", code)
            }
        }
        return L10n.format("Public address returned HTTP %d", statusCode)
    }

    static func healthURL(from publicMCPURL: URL) -> URL? {
        guard var components = URLComponents(url: publicMCPURL, resolvingAgainstBaseURL: false),
              components.scheme?.lowercased() == "https",
              components.host?.isEmpty == false,
              components.user == nil,
              components.password == nil else {
            return nil
        }
        components.path = "/healthz"
        components.query = nil
        components.fragment = nil
        return components.url
    }

    private static func networkErrorMessage(_ error: Error) -> String {
        guard let urlError = error as? URLError else {
            return error.localizedDescription
        }
        switch urlError.code {
        case .timedOut:
            return L10n.text("Public access timed out")
        case .cannotFindHost, .dnsLookupFailed:
            return L10n.text("Unable to resolve the public domain")
        case .cannotConnectToHost:
            return L10n.text("Unable to connect to the public address")
        case .networkConnectionLost:
            return L10n.text("Public connection was interrupted")
        case .notConnectedToInternet:
            return L10n.text("This Mac is not connected to the network")
        case .cancelled:
            return L10n.text("Check cancelled")
        default:
            return urlError.localizedDescription
        }
    }
}

private struct PublicHealthPayload: Decodable {
    let ok: Bool
}
