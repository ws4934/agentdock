import AppKit
import Foundation
import CoreFoundation

// 加载生产 ComputerUseMonitor / Transport；仅连接测试 socket，不连接运行中的 AgentDock。
@MainActor
final class AuditDelegate: NSObject, NSApplicationDelegate {
 let root: URL
 let mode: String
 var monitor: ComputerUseMonitor!
 init(root:URL,mode:String){self.root=root;self.mode=mode}
 func applicationDidFinishLaunching(_ notification:Notification){
  monitor=ComputerUseMonitor(runtimeRoot:root);monitor.start()
  Task { await runAudit() }
 }
 func runAudit() async {
  let deadline=Date().addingTimeInterval(5)
  while Date()<deadline && monitor.state?.phase != "running" {try? await Task.sleep(nanoseconds:50_000_000)}
  guard monitor.state?.phase=="running" else {write(["setup_failed":true]);NSApp.terminate(nil);return}
  try? await Task.sleep(nanoseconds:500_000_000)
  if mode=="stop"{
   monitor.stopAndClose()
   try? await Task.sleep(nanoseconds:4_500_000_000)
   write(["scenario":"one failed stop followed by healthy polls","phase":monitor.state?.phase ?? "nil","connected":monitor.connected,"visible":monitor.panel.isVisible,"key":monitor.panel.isKeyWindow,"expected":"stopped or fail-closed paused","passed":monitor.state?.phase=="stopped" && monitor.connected])
  }else{
   // 与菜单跟踪/拖动窗口相同的 run-loop mode；不生成任何鼠标或键盘事件。
   let keepAlive=Timer(timeInterval:0.1,repeats:true){_ in}
   RunLoop.main.add(keepAlive,forMode:.eventTracking)
   runTrackingLoop()
   keepAlive.invalidate()
   try? await Task.sleep(nanoseconds:1_000_000_000)
   write(["scenario":"4.2 seconds in eventTracking run loop","phase":monitor.state?.phase ?? "nil","reason":monitor.state?.reason ?? "nil","connected":monitor.connected,"expected":"running while visible local UI remains healthy","passed":monitor.state?.phase=="running" && monitor.connected])
  }
  monitor.shutdown();NSApp.terminate(nil)
 }
 func write(_ output:[String:Any]){let data=try! JSONSerialization.data(withJSONObject:output,options:[.sortedKeys]);try? data.write(to:root.appendingPathComponent("result.json"),options:.atomic)}
 func runTrackingLoop() { CFRunLoopRunInMode(CFRunLoopMode(rawValue: RunLoop.Mode.eventTracking.rawValue as CFString),4.2,false) }
}
@main
struct Main {
 @MainActor static func main(){
  guard CommandLine.arguments.count==3 else{exit(2)}
  let app=NSApplication.shared;app.setActivationPolicy(.accessory)
  let delegate=AuditDelegate(root:URL(fileURLWithPath:CommandLine.arguments[1]),mode:CommandLine.arguments[2]);app.delegate=delegate
  withExtendedLifetime(delegate){app.run()}
 }
}
