import Foundation
import AppKit
import Darwin

struct ComputerUsePoint: Codable, Equatable { let x: Double; let y: Double }
struct ComputerUseRect: Codable, Equatable { let x: Double; let y: Double; let width: Double; let height: Double }
struct ComputerUseWindow: Codable, Equatable { let id: UInt32; let pid: Int32; let title: String; let bounds: ComputerUseRect }
struct ComputerUseApplication: Codable { let pid: Int32; let name: String; let bundle_id: String; let app_path: String? }
struct ComputerUseApproval: Decodable { let id: String; let application: ComputerUseApplication; let mode: String }
struct ComputerUseEvent: Decodable { let sequence: UInt64; let activity: String; let pid: Int32; let window_id: UInt32; let outcome: String; let elapsed_ms: Int64 }
struct ComputerUseState: Decodable {
    let recent_operations: [ComputerUseEvent]?
    let enabled: Bool
    let monitor_required: Bool
    let monitor_connected: Bool
    let session_id: String
    let epoch: UInt64
    let phase: String
    let activity: String
    let mode: String
    let application: ComputerUseApplication
    let window: ComputerUseWindow
    let pointer: ComputerUsePoint?
    let pointer_sequence: UInt64
    let active_operations: Int
    let reason: String
    let can_resume: Bool
    let cleanup_failed: Bool?
    let task_label: String?
    let task_reference: String?
    let pending_application: ComputerUseApproval?
    let step_mode: Bool?

    var isLive: Bool { ["running", "pausing", "stopping", "paused", "cleanup_failed"].contains(phase) }
    var isDraining: Bool { phase == "pausing" || phase == "stopping" }
    var shouldPreview: Bool { phase == "running" && window.id > 0 }
}

struct ComputerUseTransport {
    let socketPath: String
    let controllerID: String

    // 心跳在主RunLoop的公共/跟踪模式启动；IPC在工作线程执行，回包通过RunLoop投递。
    // 不依赖嵌套AppKit菜单循环期间可能暂停的Swift MainActor任务队列。
    func exchange(operation: String? = nil, sessionID: String = "", visibleID: String = "", stopSessionID: String = "", approvalID: String = "", pauseSessionID: String = "", completion: @escaping @MainActor (Result<ComputerUseState, Error>) -> Void) {
        DispatchQueue.global(qos: .userInitiated).async {
            let result = Result { try self.callSync(operation: operation, sessionID: sessionID, visibleID: visibleID, stopSessionID: stopSessionID, approvalID: approvalID, pauseSessionID: pauseSessionID) }
            RunLoop.main.perform(inModes: [.common, .eventTracking, .modalPanel]) {
                MainActor.assumeIsolated { completion(result) }
            }
        }
    }

    // 每次请求独立连接并发送 EOF。只有受本机文件权限保护的 Unix socket，无 HTTP 监听。
    func callSync(operation: String? = nil, sessionID: String = "", visibleID: String = "", stopSessionID: String = "", approvalID: String = "", pauseSessionID: String = "") throws -> ComputerUseState {
        var params: [String: Any] = ["controller_pid": ProcessInfo.processInfo.processIdentifier, "controller_id": controllerID, "session_id": sessionID, "visible_session_id": visibleID]
        if let operation { params["operation"] = operation }
        if !stopSessionID.isEmpty { params["stop_session_id"] = stopSessionID }
        if !approvalID.isEmpty { params["approval_id"] = approvalID }
        if !pauseSessionID.isEmpty { params["pause_session_id"] = pauseSessionID }
        return try requestSync(method: operation == nil ? "computeruse.poll" : "computeruse.command", params: params)
    }

    func requestSync<Value: Decodable>(method: String, params: [String: Any]) throws -> Value {
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw TransportError(message: "Local socket unavailable") }
        defer { Darwin.close(fd) }
        var timeout = timeval(tv_sec: 1, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        var noSignal: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &noSignal, socklen_t(MemoryLayout<Int32>.size))
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let path = Array(socketPath.utf8) + [0]
        guard path.count <= MemoryLayout.size(ofValue: address.sun_path) else { throw TransportError(message: "Local socket path too long") }
        withUnsafeMutableBytes(of: &address.sun_path) { target in target.copyBytes(from: path) }
        let connected = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0 else { throw TransportError(message: "Local Computer Use service unavailable") }
        let id = UUID().uuidString
        let payload = try JSONSerialization.data(withJSONObject: ["id": id, "method": method, "params": params])
        try payload.withUnsafeBytes { raw in
            var written = 0
            while written < raw.count {
                let count = Darwin.write(fd, raw.baseAddress!.advanced(by: written), raw.count - written)
                guard count > 0 else { throw TransportError(message: "Local control write failed") }
                written += count
            }
        }
        Darwin.shutdown(fd, SHUT_WR)
        var response = Data()
        var buffer = [UInt8](repeating: 0, count: 8192)
        while true {
            let count = Darwin.read(fd, &buffer, buffer.count)
            if count == 0 { break }
            guard count > 0, response.count + count <= 1_048_576 else { throw TransportError(message: "Local control response unavailable or oversized") }
            response.append(contentsOf: buffer.prefix(count))
        }
        let reply = try JSONDecoder().decode(DesktopControlReply<Value>.self, from: response)
        guard reply.id == id else { throw TransportError(message: "Local control response mismatch") }
        if let error = reply.error { throw TransportError(message: error.message) }
        guard let result = reply.result else { throw TransportError(message: "Local control response missing") }
        return result
    }
}

private struct DesktopControlReply<Value: Decodable>: Decodable {
    struct Failure: Decodable { let message: String }
    let id: String
    let result: Value?
    let error: Failure?
}
private struct TransportError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}
