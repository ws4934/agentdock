// 原生窗口桥接实现，包含于 native_darwin.m。窗口内指针坐标使用可选非公开系统符号；缺失时拒绝指针操作。
#import <dlfcn.h>
#ifndef AGENTDOCK_NATIVE_WINDOW_IMPL_H
#define AGENTDOCK_NATIVE_WINDOW_IMPL_H

static NSDictionary *ad_decode_object(const char *json) {
 if(!json)return nil;
 id value=[NSJSONSerialization JSONObjectWithData:[[NSString stringWithUTF8String:json] dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
 return [value isKindOfClass:NSDictionary.class]?value:nil;
}
static BOOL ad_window_matches_geometry(NSDictionary *target,BOOL geometry) {
 uint32_t window=[target[@"id"] unsignedIntValue];int pid=[target[@"pid"] intValue];
 if(!window||pid<=0)return NO;
 NSArray *info=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow,window));
 for(NSDictionary *entry in info){
  if([entry[(__bridge NSString *)kCGWindowNumber] unsignedIntValue]!=window)continue;
  CGRect bounds=CGRectZero;
  return [entry[(__bridge NSString *)kCGWindowOwnerPID] intValue]==pid && (!geometry || (CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)entry[(__bridge NSString *)kCGWindowBounds],&bounds) && [ad_rect(bounds) isEqual:target[@"bounds"]]));
 }
 return NO;
}

static BOOL ad_window_matches(NSDictionary *target){return ad_window_matches_geometry(target,YES);}

// AX 无公开且通用的 CGWindowID 属性；只接受几何和可用标题唯一匹配，拒绝猜测。
static AXUIElementRef ad_window_root(NSDictionary *target) {
 AXUIElementRef app=AXUIElementCreateApplication([target[@"pid"] intValue]);
 if(!app)return NULL;
 AXUIElementSetMessagingTimeout(app,0.10);
 CFArrayRef windows=NULL;CFIndex count=0;
 if(AXUIElementGetAttributeValueCount(app,kAXWindowsAttribute,&count)!=kAXErrorSuccess||count<=0||count>64){CFRelease(app);return NULL;}
 AXError error=AXUIElementCopyAttributeValues(app,kAXWindowsAttribute,0,count,&windows);
 CFRelease(app);
 if(error!=kAXErrorSuccess||!windows)return NULL;
 AXUIElementRef selected=NULL;
 CFAbsoluteTime deadline=CFAbsoluteTimeGetCurrent()+1.0;
 for(CFIndex i=0;i<CFArrayGetCount(windows);i++){
  if(CFAbsoluteTimeGetCurrent()>deadline){if(selected)CFRelease(selected);selected=NULL;break;}
  AXUIElementRef candidate=(AXUIElementRef)CFArrayGetValueAtIndex(windows,i);
  if(CFGetTypeID(candidate)!=AXUIElementGetTypeID())continue;
  AXUIElementSetMessagingTimeout(candidate,0.10);
  if(![ad_rect(ad_bounds(candidate)) isEqual:target[@"bounds"]])continue;
  NSString *title=target[@"title"];
  if(title.length>0&&![ad_label(candidate,kAXTitleAttribute) isEqual:title])continue;
  if(selected){CFRelease(selected);selected=NULL;break;}
  selected=(AXUIElementRef)CFRetain(candidate);
 }
 CFRelease(windows);return selected;
}
static int ad_window_guard(NSDictionary *target,BOOL keyboard,BOOL release) {
 if(!AXIsProcessTrusted())return 7;
 // 释放清理只校验原 PID/窗口身份，允许窗口移动或用户接管后仍释放原目标。
 if(!ad_window_matches_geometry(target,!release))return 8;
 int pid=[target[@"pid"] intValue];
 if(!release&&ad_frontmost()==pid)return 9;
 if(keyboard){
  if(IsSecureEventInputEnabled())return 3;
  AXUIElementRef root=ad_window_root(target);if(!root)return 11;
  AXUIElementRef app=AXUIElementCreateApplication(pid);AXUIElementSetMessagingTimeout(app,0.10);
  id focused=ad_attribute(app,kAXFocusedWindowAttribute);
  BOOL matches=focused&&CFGetTypeID((__bridge CFTypeRef)focused)==AXUIElementGetTypeID()&&CFEqual(root,(__bridge CFTypeRef)focused);
  CFRelease(app);CFRelease(root);if(!matches)return 10;
  if(!ad_window_matches(target))return 8;
  if(ad_frontmost()==pid)return 9;
  if(IsSecureEventInputEnabled())return 3;
 }
 return 0;
}
char *ad_window_tree(const char *window_json,int nodes,int depth) {
 @autoreleasepool{
  NSDictionary *target=ad_decode_object(window_json);
  if(!AXIsProcessTrusted()||!ad_window_matches(target))return ad_json(@{@"error":@"Window unavailable or Accessibility permission missing"});
  AXUIElementRef root=ad_window_root(target);
  if(!root)return ad_json(@{@"error":@"Target AX window is unavailable or ambiguous"});
  CFMutableSetRef seen=CFSetCreateMutable(NULL,0,&kCFTypeSetCallBacks);
  NSMutableArray *elements=[NSMutableArray array];BOOL truncated=NO;
  ad_walk(root,@[],elements,seen,nodes,depth,CFAbsoluteTimeGetCurrent()+3.0,&truncated);
  CFRelease(seen);CFRelease(root);
  return ad_json(@{@"elements":elements,@"truncated":truncated?@YES:@NO});
 }
}
static int ad_window_ax(NSDictionary *target,NSDictionary *input) {
 NSDictionary *expected=input[@"element"];
 NSArray *path=expected[@"path"]?:@[];
 if(!expected||path.count>16)return 4;
 AXUIElementRef node=ad_window_root(target);if(!node)return 11;
 for(NSNumber *index in path){
  AXUIElementSetMessagingTimeout(node,0.10);
  CFArrayRef children=NULL;
  AXError error=AXUIElementCopyAttributeValues(node,kAXChildrenAttribute,index.intValue,1,&children);
  CFRelease(node);node=NULL;
  if(error!=kAXErrorSuccess||!children||CFArrayGetCount(children)!=1){if(children)CFRelease(children);return 4;}
  CFTypeRef child=CFArrayGetValueAtIndex(children,0);
  if(CFGetTypeID(child)==AXUIElementGetTypeID())node=(AXUIElementRef)CFRetain(child);
  CFRelease(children);if(!node)return 4;
 }
 AXUIElementSetMessagingTimeout(node,0.10);
 NSDictionary *actual=ad_element(node,path);
 BOOL setValue=[input[@"action"] isEqual:@"set_value"];
 BOOL matches=[actual[@"role"] isEqual:expected[@"role"]]&&[actual[@"title"] isEqual:expected[@"title"]]&&[actual[@"description"] isEqual:expected[@"description"]]&&[actual[@"bounds"] isEqual:expected[@"bounds"]]&&[actual[@"enabled"] boolValue]&&[actual[setValue?@"value_settable":@"pressable"] boolValue];
 int guard=ad_window_guard(target,NO,NO);
 if(setValue&&IsSecureEventInputEnabled())guard=3;
 int result=guard;
 if(!result){
  // AppKit 的 AXPress 可能等待按钮动画；动作消息给独立的 1 秒预算，不重试。
  AXUIElementSetMessagingTimeout(node,1.0);
  AXError error=matches?(setValue?AXUIElementSetAttributeValue(node,kAXValueAttribute,(__bridge CFStringRef)(input[@"value"]?:@"")):AXUIElementPerformAction(node,kAXPressAction)):kAXErrorInvalidUIElement;
  result=error==kAXErrorSuccess?0:4;
 }
 CFRelease(node);return result;
}

// macOS 的公开事件构造器不填充远端窗口坐标；将非公开依赖限制在一个可检测入口。
typedef void (*ADSetWindowLocation)(CGEventRef,CGPoint);
static ADSetWindowLocation ad_window_location_function(void) {
 static ADSetWindowLocation function=NULL;static dispatch_once_t once;
 dispatch_once(&once,^{function=(ADSetWindowLocation)dlsym(RTLD_DEFAULT,"CGEventSetWindowLocation");});
 return function;
}
int ad_background_pointer_supported(void){return ad_window_location_function()!=NULL;}
static BOOL ad_set_window_location(CGEventRef event,NSDictionary *target,CGPoint point){
 ADSetWindowLocation function=ad_window_location_function();if(!function)return NO;
 NSDictionary *bounds=target[@"bounds"];
 CGEventSetLocation(event,point);
 function(event,CGPointMake(point.x-[bounds[@"x"] doubleValue],point.y-[bounds[@"y"] doubleValue]));
 return YES;
}
static void ad_post_window(int pid,uint32_t window,CGEventRef event) {
 // 只发给明确授权的目标进程，不写入全局 HID/session 事件流。
 CGEventSetIntegerValueField(event,kCGMouseEventWindowUnderMousePointer,window);
 CGEventSetIntegerValueField(event,kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent,window);
 CGEventSetIntegerValueField(event,kCGEventSourceUserData,0x4147444247494E50LL);
 CGEventPostToPid(pid,event);
}
// NSEvent 的公开构造器绑定 windowNumber 和窗口内坐标；裸 CGEvent 的窗口编号为 0。
static CGEventRef ad_window_mouse_event(NSDictionary *target,CGEventSourceRef source,int kind,int button,CGPoint point,int count,uint64_t flags) {
 NSDictionary *b=target[@"bounds"];
 NSPoint local=NSMakePoint(point.x-[b[@"x"] doubleValue],[b[@"height"] doubleValue]-(point.y-[b[@"y"] doubleValue]));
 NSEvent *native=[NSEvent mouseEventWithType:(NSEventType)ad_mouse_type(kind,button) location:local modifierFlags:(NSEventModifierFlags)flags timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:[target[@"id"] integerValue] context:nil eventNumber:0 clickCount:count pressure:(kind==2||kind==4)?1.0:0.0];
 CGEventRef event=native.CGEvent?CGEventCreateCopy(native.CGEvent):NULL;
 if(event){CGEventSetSource(event,source);if(!ad_set_window_location(event,target,point)){CFRelease(event);return NULL;}CGEventSetIntegerValueField(event,kCGMouseEventButtonNumber,button);}
 return event;
}
// 直接投递跳过 WindowServer 的键码→字符阶段，快捷键必须显式携带当前布局的字符。
static BOOL ad_shortcut_characters(uint16_t key,uint64_t flags,UniChar *text,UniCharCount *length) {
 TISInputSourceRef source=TISCopyCurrentKeyboardLayoutInputSource();
 CFDataRef data=source?TISGetInputSourceProperty(source,kTISPropertyUnicodeKeyLayoutData):NULL;
 if(!data){if(source)CFRelease(source);source=TISCopyCurrentASCIICapableKeyboardLayoutInputSource();data=source?TISGetInputSourceProperty(source,kTISPropertyUnicodeKeyLayoutData):NULL;}
 if(!data){if(source)CFRelease(source);return NO;}
 const UCKeyboardLayout *layout=(const UCKeyboardLayout *)CFDataGetBytePtr(data);
 UInt32 modifiers=0,dead=0;
 if(flags&kCGEventFlagMaskShift)modifiers|=shiftKey;
 if(flags&kCGEventFlagMaskAlternate)modifiers|=optionKey;
 OSStatus status=UCKeyTranslate(layout,key,kUCKeyActionDown,(modifiers>>8)&0xff,LMGetKbdType(),kUCKeyTranslateNoDeadKeysBit,&dead,20,length,text);
 CFRelease(source);return status==noErr&&*length>0;
}
#include "native_shortcut_impl.h"
static int ad_window_keyboard(NSDictionary *target,NSDictionary *input) {
 int pid=[target[@"pid"] intValue];uint16_t key=[input[@"key_code"] unsignedShortValue];uint64_t flags=[input[@"flags"] unsignedLongLongValue];
 UniChar text[20],plain[20];UniCharCount length=0,plainLength=0;
 if([input[@"action"] isEqual:@"type"]){
  NSArray *units=input[@"units"];if(units.count==0||units.count>20)return 1;
  length=units.count;for(NSUInteger i=0;i<units.count;i++)text[i]=[units[i] unsignedShortValue];
  memcpy(plain,text,length*sizeof(UniChar));plainLength=length;
 }else{
  if(!ad_shortcut_characters(key,flags,text,&length)||!ad_shortcut_characters(key,0,plain,&plainLength))return 13;
 }
 NSString *characters=[NSString stringWithCharacters:text length:length],*unmodified=[NSString stringWithCharacters:plain length:plainLength];
 NSEvent *nativeDown=[NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:(NSEventModifierFlags)flags timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:[target[@"id"] integerValue] context:nil characters:characters charactersIgnoringModifiers:unmodified isARepeat:NO keyCode:key];
 NSEvent *nativeUp=[NSEvent keyEventWithType:NSEventTypeKeyUp location:NSZeroPoint modifierFlags:0 timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:[target[@"id"] integerValue] context:nil characters:characters charactersIgnoringModifiers:unmodified isARepeat:NO keyCode:key];
 CGEventRef down=nativeDown.CGEvent?CGEventCreateCopy(nativeDown.CGEvent):NULL,up=nativeUp.CGEvent?CGEventCreateCopy(nativeUp.CGEvent):NULL;
 if(!down||!up){if(down)CFRelease(down);if(up)CFRelease(up);return 1;}
 CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source){CFRelease(down);CFRelease(up);return 1;}
 CGEventSetSource(down,source);CGEventSetSource(up,source);
 // NSEvent 转 CGEvent 会重新推导字符；在最终事件上保留 Unicode 和修饰键。
 CGEventKeyboardSetUnicodeString(down,length,text);CGEventKeyboardSetUnicodeString(up,length,text);
 CGEventSetFlags(down,(CGEventFlags)flags);CGEventSetFlags(up,0);
 CGEventSetIntegerValueField(down,kCGEventSourceUserData,0x4147444247494E50LL);
 CGEventSetIntegerValueField(up,kCGEventSourceUserData,0x4147444247494E50LL);
 int guard=ad_window_guard(target,YES,NO);
 if(!guard){CGEventPostToPid(pid,down);CGEventPostToPid(pid,up);}
 CFRelease(down);CFRelease(up);CFRelease(source);return guard;
}
int ad_window_input(const char *request_json) {
 @autoreleasepool {
  NSDictionary *request=ad_decode_object(request_json);
  NSDictionary *target=request[@"window"],*input=request[@"input"];
  NSString *action=input[@"action"];
  if(!target||!input||!action)return 1;
  BOOL keyboard=[action isEqual:@"key"]||[action isEqual:@"type"];
  BOOL release=[action isEqual:@"up"];
  int guard=ad_window_guard(target,keyboard,release);if(guard)return guard;
  if(input[@"element"])return ad_window_ax(target,input);
  int pid=[target[@"pid"] intValue];uint32_t window=[target[@"id"] unsignedIntValue];
  if([action isEqual:@"key"]){int menu=ad_menu_shortcut(target,input);if(menu>=0)return menu;}
  if(keyboard)return ad_window_keyboard(target,input);
  if(!ad_background_pointer_supported())return 12;
  uint64_t flags=[input[@"flags"] unsignedLongLongValue];
  CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return 1;
  CGPoint point=CGPointMake([input[@"point"][@"x"] doubleValue],[input[@"point"][@"y"] doubleValue]);
  if([action isEqual:@"scroll"]){
   CGEventRef wheel=CGEventCreateScrollWheelEvent(source,kCGScrollEventUnitPixel,2,[input[@"delta_y"] intValue],[input[@"delta_x"] intValue]);
   CGEventRef event=ad_window_mouse_event(target,source,1,0,point,0,0);
   if(!event||!wheel){if(event)CFRelease(event);if(wheel)CFRelease(wheel);CFRelease(source);return 1;}
   CGEventSetType(event,kCGEventScrollWheel);
   CGEventField integers[]={kCGScrollWheelEventDeltaAxis1,kCGScrollWheelEventDeltaAxis2,kCGScrollWheelEventPointDeltaAxis1,kCGScrollWheelEventPointDeltaAxis2,kCGScrollWheelEventIsContinuous};
   for(size_t i=0;i<sizeof(integers)/sizeof(integers[0]);i++)CGEventSetIntegerValueField(event,integers[i],CGEventGetIntegerValueField(wheel,integers[i]));
   CGEventSetDoubleValueField(event,kCGScrollWheelEventFixedPtDeltaAxis1,CGEventGetDoubleValueField(wheel,kCGScrollWheelEventFixedPtDeltaAxis1));
   CGEventSetDoubleValueField(event,kCGScrollWheelEventFixedPtDeltaAxis2,CGEventGetDoubleValueField(wheel,kCGScrollWheelEventFixedPtDeltaAxis2));
   ad_post_window(pid,window,event);CFRelease(wheel);CFRelease(event);CFRelease(source);return 0;
  }
  int kind=-1;
  if([action isEqual:@"click"])kind=0;
  else if([action isEqual:@"move"])kind=1;
  else if([action isEqual:@"down"])kind=2;
  else if([action isEqual:@"up"])kind=3;
  else if([action isEqual:@"drag"])kind=4;
  if(kind<0){CFRelease(source);return 1;}
  int button=[input[@"button"] intValue];
  if(kind==0){
   int count=[input[@"count"] intValue];if(count<1||count>3){CFRelease(source);return 1;}
   for(int i=1;i<=count;i++){
    guard=ad_window_guard(target,NO,NO);if(guard){CFRelease(source);return guard;}
    CGEventRef down=ad_window_mouse_event(target,source,2,button,point,i,flags);
    CGEventRef up=ad_window_mouse_event(target,source,3,button,point,i,flags);
    if(!down||!up){if(down)CFRelease(down);if(up)CFRelease(up);CFRelease(source);return 1;}
    CGEventSetFlags(down,(CGEventFlags)flags);CGEventSetFlags(up,(CGEventFlags)flags);
    CGEventSetIntegerValueField(down,kCGMouseEventClickState,i);CGEventSetIntegerValueField(up,kCGMouseEventClickState,i);
    ad_post_window(pid,window,down);ad_post_window(pid,window,up);CFRelease(down);CFRelease(up);
    if(i<count)usleep(50000);
   }
  }else{
   CGEventRef event=ad_window_mouse_event(target,source,kind,button,point,1,flags);
   if(!event){CFRelease(source);return 1;}
   CGEventSetFlags(event,release?0:(CGEventFlags)flags);ad_post_window(pid,window,event);CFRelease(event);
  }
  CFRelease(source);return 0;
 }
}
#endif
