import AppKit
import Carbon

// 只注册一个系统热键；不监听或保存用户键盘输入。注册冲突时保留普通停止入口。
@MainActor
final class ComputerUseEmergencyHotkey {
    private var hotkey: EventHotKeyRef?
    private var handler: EventHandlerRef?
    private let stop: () -> Void
    private(set) var available = false

    init(stop: @escaping () -> Void) {
        self.stop = stop
        var type = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let context = Unmanaged.passUnretained(self).toOpaque()
        let installed = InstallEventHandler(GetApplicationEventTarget(), { _, event, context in
            guard let event, let context else { return OSStatus(eventNotHandledErr) }
            var id = EventHotKeyID()
            let result = GetEventParameter(event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID), nil, MemoryLayout<EventHotKeyID>.size, nil, &id)
            guard result == noErr, id.signature == 0x41474453, id.id == 1 else { return OSStatus(eventNotHandledErr) }
            let owner = Unmanaged<ComputerUseEmergencyHotkey>.fromOpaque(context).takeUnretainedValue()
            RunLoop.main.perform(inModes: [.common, .eventTracking, .modalPanel]) { MainActor.assumeIsolated { owner.stop() } }
            return noErr
        }, 1, &type, context, &handler)
        guard installed == noErr else { return }
        let id = EventHotKeyID(signature: 0x41474453, id: 1)
        let modifiers = UInt32(controlKey | optionKey | cmdKey)
        available = RegisterEventHotKey(UInt32(kVK_F12), modifiers, id, GetApplicationEventTarget(), 0, &hotkey) == noErr
    }
    func shutdown() {
        if let hotkey { UnregisterEventHotKey(hotkey) }; hotkey = nil
        if let handler { RemoveEventHandler(handler) }; handler = nil
        available = false
    }
}
