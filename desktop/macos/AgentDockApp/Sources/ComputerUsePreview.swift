import AppKit
import ScreenCaptureKit
import CoreImage
import CoreMedia

// 主线程繁忙时只保留最新一帧，避免预览帧排队占满内存。
private final class ComputerUseFrameSlot: @unchecked Sendable {
    private let lock = NSLock()
    private var latest: (CGImage, SCStream)?
    private var scheduled = false
    func put(_ image: CGImage, _ stream: SCStream) -> Bool {
        lock.lock(); defer { lock.unlock() }
        latest = (image, stream)
        if scheduled { return false }; scheduled = true; return true
    }
    func take() -> (CGImage, SCStream)? {
        lock.lock(); defer { lock.unlock() }
        let result = latest; latest = nil; scheduled = false; return result
    }
}

// 预览独立于 MCP 截图：仅本机内存中的目标窗口视频，不消耗 snapshot_id。
@MainActor
final class ComputerUsePreview: NSObject, SCStreamOutput, SCStreamDelegate {
    private var stream: SCStream?
    private var revision = 0
    private var selected: ComputerUseWindow?
    private var starting = false
    private var attempts = 0
    private var streamStarted = Date.distantPast
    private var retryAt = Date.distantPast
    private(set) var requiresUserRestart = false
    private let permissionCheck: () -> Bool
    private let contentProvider: () async throws -> SCShareableContent
    var clock: () -> Date = Date.init

    init(permissionCheck: @escaping () -> Bool = { CGPreflightScreenCaptureAccess() }, contentProvider: @escaping () async throws -> SCShareableContent = { try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true) }) {
        self.permissionCheck = permissionCheck; self.contentProvider = contentProvider
        super.init()
    }
    func retryByUser() { requiresUserRestart = false; attempts = 0; retryAt = .distantPast }
    private func failed(_ error: Error?) {
        starting = false
        if !permissionCheck() { requiresUserRestart = true }
        if let error = error as NSError?, error.domain == SCStreamErrorDomain {
            // 用户停止/拒绝和缺少权限属于显式边界，不能按瞬时网络错误自动恢复。
            if error.code == SCStreamError.Code.userDeclined.rawValue || error.code == SCStreamError.Code.missingEntitlements.rawValue { requiresUserRestart = true }
            if #available(macOS 14.0, *), error.code == SCStreamError.Code.userStopped.rawValue { requiresUserRestart = true }
        }
        retryAt = clock().addingTimeInterval(min(4, pow(2, Double(max(0, attempts - 1))) * 0.5))
    }
    private let outputQueue = DispatchQueue(label: "agentdock.computer-use.preview", qos: .utility)
    nonisolated private let frameSlot = ComputerUseFrameSlot()
    nonisolated private let imageContext = CIContext(options: [.cacheIntermediates: false])
    var onFrame: ((CGImage) -> Void)?
    var onState: ((String) -> Void)?
    private(set) var lastFrame = Date.distantPast
    private(set) var frameCount = 0

    static func sameCaptureTarget(_ a: ComputerUseWindow?, _ b: ComputerUseWindow?) -> Bool {
        guard let a, let b else { return a == nil && b == nil }
        return a.id == b.id && a.pid == b.pid && a.bounds.width == b.bounds.width && a.bounds.height == b.bounds.height
    }

    func select(_ target: ComputerUseWindow?) {
        if !Self.sameCaptureTarget(target, selected) { attempts = 0; retryAt = .distantPast }
        else if target == nil || stream != nil || starting || attempts >= 4 || clock() < retryAt || requiresUserRestart { return }
        selected = target
        if target != nil && requiresUserRestart { return }
        revision += 1
        let token = revision
        let old = stream
        stream = nil
        lastFrame = .distantPast
        starting = target != nil
        if target != nil { attempts += 1 }
        Task { [weak self] in
            try? await old?.stopCapture()
            guard let self, self.revision == token, let target, target.id > 0 else { return }
            // 不弹出权限请求。缺权限时由现有的权限设置流程让用户主动授权。
            guard self.permissionCheck() else { self.failed(nil); self.onState?(L10n.text("Preview needs Screen Recording permission.")); return }
            do {
                let content = try await self.contentProvider()
                guard self.revision == token else { return }
                guard let window = content.windows.first(where: { $0.windowID == target.id && $0.owningApplication?.processID == target.pid }) else {
                    self.failed(nil); self.onState?(L10n.text("Target window is unavailable.")); return
                }
                let config = SCStreamConfiguration()
                let ratio = min(1, 960 / max(window.frame.width, window.frame.height))
                config.width = max(1, Int(window.frame.width * ratio))
                config.height = max(1, Int(window.frame.height * ratio))
                config.minimumFrameInterval = CMTime(value: 1, timescale: 10)
                config.queueDepth = 3
                config.showsCursor = false
                config.capturesAudio = false
                if #available(macOS 14.0, *) { config.ignoreShadowsSingleWindow = true }
                let next = SCStream(filter: SCContentFilter(desktopIndependentWindow: window), configuration: config, delegate: self)
                try next.addStreamOutput(self, type: .screen, sampleHandlerQueue: self.outputQueue)
                self.stream = next; self.streamStarted = self.clock()
                self.onState?(L10n.text("Waiting for live preview…"))
                try await next.startCapture()
                if self.revision == token { self.starting = false }
                if self.revision != token { try? await next.stopCapture() }
            } catch {
                guard self.revision == token else { return }
                self.stream = nil
                self.failed(error)
                self.onState?(L10n.text("Live preview is unavailable. Control can still be stopped."))
            }
        }
    }

    nonisolated func stream(_ stream: SCStream, didStopWithError error: Error) {
        Task { @MainActor [weak self] in
            guard let self, self.stream === stream else { return }
            self.stream = nil
            self.failed(error)
            self.onState?(L10n.text("Live preview stopped. Control can still be stopped."))
        }
    }

    nonisolated func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, sampleBuffer.isValid,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let status = attachments.first?[.status] as? Int, status == SCFrameStatus.complete.rawValue,
              let buffer = sampleBuffer.imageBuffer else { return }
        let image = CIImage(cvPixelBuffer: buffer)
        guard let frame = imageContext.createCGImage(image, from: image.extent) else { return }
        guard frameSlot.put(frame, stream) else { return }
        Task { @MainActor [weak self, frameSlot] in
            guard let (latest, source) = frameSlot.take(), let self, self.stream === source else { return }
            self.lastFrame = Date()
            if self.clock().timeIntervalSince(self.streamStarted) >= 2 { self.attempts = 0 }
            self.frameCount += 1
            self.onFrame?(latest)
        }
    }
}

@MainActor
final class ComputerUseCanvas: NSView {
    var image: CGImage? { didSet { if image !== oldValue { needsDisplay = true } } }
    var point: ComputerUsePoint? { didSet { if point != oldValue { needsDisplay = true } } }
    var targetBounds: ComputerUseRect? { didSet { if targetBounds != oldValue { needsDisplay = true } } }
    var message = "" { didSet { if message != oldValue { needsDisplay = true } } }
    override var isFlipped: Bool { true }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.controlBackgroundColor.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 12, yRadius: 12).fill()
        guard let image else {
            let centered = NSMutableParagraphStyle(); centered.alignment = .center
            let attrs: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor, .paragraphStyle: centered]
            let symbol = NSImage(systemSymbolName: "cursorarrow.motionlines", accessibilityDescription: nil)?.withSymbolConfiguration(NSImage.SymbolConfiguration(paletteColors: [.tertiaryLabelColor]))
            symbol?.draw(in: NSRect(x: bounds.midX - 14, y: bounds.midY - 30, width: 28, height: 28), from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
            (message as NSString).draw(in: NSRect(x: 18, y: bounds.midY + 8, width: bounds.width - 36, height: 40), withAttributes: attrs)
            return
        }
        let ratio = min(bounds.width / CGFloat(image.width), bounds.height / CGFloat(image.height))
        let size = NSSize(width: CGFloat(image.width) * ratio, height: CGFloat(image.height) * ratio)
        let rect = NSRect(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2, width: size.width, height: size.height)
        NSImage(cgImage: image, size: size).draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
        if let point, let targetBounds, targetBounds.width > 0, targetBounds.height > 0 {
            let x = rect.minX + (point.x - targetBounds.x) * rect.width / targetBounds.width
            let y = rect.minY + (point.y - targetBounds.y) * rect.height / targetBounds.height
            if rect.contains(NSPoint(x: x, y: y)) {
                NSColor.systemOrange.setStroke()
                let ring = NSBezierPath(ovalIn: NSRect(x: x - 7, y: y - 7, width: 14, height: 14)); ring.lineWidth = 2; ring.stroke()
                NSColor.white.setFill(); NSBezierPath(ovalIn: NSRect(x: x - 2, y: y - 2, width: 4, height: 4)).fill()
            }
        }
    }
}
