import AppKit

// 菜单栏 App 通常不在前台。仅 orderFront 不会激活应用，看起来就像菜单没有响应。
@MainActor enum ManagementWindowPresenter {
    static func present(_ controller: NSWindowController, activate: @MainActor () -> Void = { NSApp.activate(ignoringOtherApps: true) }) {
        guard let window = controller.window else { return }
        if window.isMiniaturized { window.deminiaturize(nil) }
        if !NSScreen.screens.contains(where: { $0.visibleFrame.intersects(window.frame) }) { window.center() }
        controller.showWindow(nil)
        activate()
        window.makeKeyAndOrderFront(nil)
    }
}
