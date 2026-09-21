// 仅供显式 GUI 验收的只读鼠标事件观察器；不监听键盘、不拦截或修改事件。
#import <ApplicationServices/ApplicationServices.h>
#import <unistd.h>

static CFMachPortRef fixtureMouseTap=NULL;
static long long fixtureOwnedMouseEvents=0,fixtureExternalMoves=0;
static int fixtureTargetPID=0;
static const int64_t fixtureBackgroundTag=0x4147444247494E50LL;
static CGEventRef fixtureObserveMouse(CGEventTapProxy proxy,CGEventType type,CGEventRef event,void *context){
 if(type==kCGEventTapDisabledByTimeout||type==kCGEventTapDisabledByUserInput)return event;
 int pid=(int)CGEventGetIntegerValueField(event,kCGEventSourceUnixProcessID);
 int64_t tag=CGEventGetIntegerValueField(event,kCGEventSourceUserData);
 BOOL owned=pid==getppid()||pid==fixtureTargetPID||tag==fixtureBackgroundTag;
 if(owned)fixtureOwnedMouseEvents++;
 else if(type==kCGEventMouseMoved||type==kCGEventLeftMouseDragged||type==kCGEventRightMouseDragged||type==kCGEventOtherMouseDragged)fixtureExternalMoves++;
 return event;
}
static void fixtureStartMouseObserver(void){
 if(![NSProcessInfo.processInfo.environment[@"AGENTDOCK_TEST_MONITOR_MOUSE"] isEqual:@"1"])return;
 fixtureTargetPID=[NSProcessInfo.processInfo.environment[@"AGENTDOCK_TEST_TARGET_PID"] intValue];
 CGEventMask mask=CGEventMaskBit(kCGEventMouseMoved)|CGEventMaskBit(kCGEventLeftMouseDragged)|CGEventMaskBit(kCGEventRightMouseDragged)|CGEventMaskBit(kCGEventOtherMouseDragged)|CGEventMaskBit(kCGEventLeftMouseDown)|CGEventMaskBit(kCGEventLeftMouseUp)|CGEventMaskBit(kCGEventRightMouseDown)|CGEventMaskBit(kCGEventRightMouseUp)|CGEventMaskBit(kCGEventOtherMouseDown)|CGEventMaskBit(kCGEventOtherMouseUp)|CGEventMaskBit(kCGEventScrollWheel);
 // Session + listenOnly 不要求 root，不触发系统权限弹窗；不可用时测试必须失败。
 fixtureMouseTap=CGEventTapCreate(kCGSessionEventTap,kCGHeadInsertEventTap,kCGEventTapOptionListenOnly,mask,fixtureObserveMouse,NULL);
 if(!fixtureMouseTap)return;
 CFRunLoopSourceRef source=CFMachPortCreateRunLoopSource(NULL,fixtureMouseTap,0);
 if(!source){CFRelease(fixtureMouseTap);fixtureMouseTap=NULL;return;}
 CFRunLoopAddSource(CFRunLoopGetMain(),source,kCFRunLoopCommonModes);CFRelease(source);
}
static NSDictionary *fixtureMouseSample(void){
 CGEventRef event=CGEventCreate(NULL);CGPoint cursor=event?CGEventGetLocation(event):CGPointZero;if(event)CFRelease(event);
 return @{@"tap_active":fixtureMouseTap&&CGEventTapIsEnabled(fixtureMouseTap)?@YES:@NO,@"owned_global_events":@(fixtureOwnedMouseEvents),@"external_moves":@(fixtureExternalMoves),@"cursor":@{@"x":@(cursor.x),@"y":@(cursor.y)}};
}
