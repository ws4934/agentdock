//go:build darwin && cgo

// 这里只封装 macOS SDK；策略、快照生命周期、参数约束及动作序列由 Go 管理。
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <Carbon/Carbon.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <ImageIO/ImageIO.h>
#import <math.h>
#import <unistd.h>
#import "native.h"

static char *ad_json(id value) {
 NSData *data = [NSJSONSerialization dataWithJSONObject:value options:0 error:nil];
 if (!data) return NULL;
 char *out = malloc(data.length + 1);
 if (out) { memcpy(out, data.bytes, data.length); out[data.length] = 0; }
 return out;
}
static NSDictionary *ad_rect(CGRect rect) {
 return @{ @"x":@(rect.origin.x), @"y":@(rect.origin.y), @"width":@(rect.size.width), @"height":@(rect.size.height) };
}
static int ad_frontmost(void) {
 // Go daemon 不运行 AppKit 主循环，不能依赖 NSWorkspace 的 KVO 缓存。
 if (AXIsProcessTrusted()) {
  AXUIElementRef system=AXUIElementCreateSystemWide();
  AXUIElementSetMessagingTimeout(system,0.10);
  CFTypeRef focused=NULL;pid_t pid=0;
  AXError error=AXUIElementCopyAttributeValue(system,kAXFocusedApplicationAttribute,&focused);
  if(error==kAXErrorSuccess&&focused&&CFGetTypeID(focused)==AXUIElementGetTypeID())AXUIElementGetPid((AXUIElementRef)focused,&pid);
  if(focused)CFRelease(focused);CFRelease(system);
  if(pid>0)return (int)pid;
 }
 // 焦点过渡期间 AX 可能暂时为空；实时 Process Manager 查询不依赖 KVO 缓存。
 // 输入入口仍独立要求 AXIsProcessTrusted，绝不通过回退绕过系统授权。
 ProcessSerialNumber serial={0,kNoProcess};pid_t pid=0;
 if(GetFrontProcess(&serial)==noErr)GetProcessPID(&serial,&pid);
 return (int)pid;
}
static int ad_guard(int pid, bool keyboard) {
 if (!AXIsProcessTrusted()) return 7;
 if (pid > 0 && ad_frontmost() != pid) return 2;
 if (keyboard && IsSecureEventInputEnabled()) return 3;
 return 0;
}
int ad_supported(void) { if (@available(macOS 14.0, *)) return 1; return 0; }
int ad_permissions(void) {
 @autoreleasepool { return (CGPreflightScreenCaptureAccess()?1:0) | (AXIsProcessTrusted()?2:0) | (IsSecureEventInputEnabled()?4:0); }
}
int ad_request_permission(int permission) {
 @autoreleasepool {
  if (permission == 1) CGRequestScreenCaptureAccess();
  else { NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt:@YES}; AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options); }
  return 0;
 }
}
char *ad_state(void) {
 @autoreleasepool {
  uint32_t count=0;
  if (CGGetActiveDisplayList(0,NULL,&count)!=kCGErrorSuccess || count==0 || count>64) return ad_json(@{@"error":@"No active display; an unlocked local desktop session is required"});
  CGDirectDisplayID ids[64];
  if (CGGetActiveDisplayList(64,ids,&count)!=kCGErrorSuccess) return NULL;
  NSMutableArray *displays=[NSMutableArray array];
  for(uint32_t i=0;i<count;i++) {
   CGDisplayModeRef mode=CGDisplayCopyDisplayMode(ids[i]);
   size_t width=mode?CGDisplayModeGetPixelWidth(mode):CGDisplayPixelsWide(ids[i]);
   size_t height=mode?CGDisplayModeGetPixelHeight(mode):CGDisplayPixelsHigh(ids[i]);
   [displays addObject:@{@"id":@(ids[i]),@"bounds":ad_rect(CGDisplayBounds(ids[i])),@"pixel_width":@(width),@"pixel_height":@(height),@"main":CGDisplayIsMain(ids[i])?@YES:@NO}];
   if(mode)CGDisplayModeRelease(mode);
  }
  NSMutableArray *windows=[NSMutableArray array];BOOL windowsTruncated=NO;
  NSArray *nativeWindows=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));
  for(NSDictionary *w in nativeWindows) {
   if([w[(__bridge NSString *)kCGWindowLayer] intValue]!=0)continue;
   CGRect bounds=CGRectZero;
   if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(__bridge NSString *)kCGWindowBounds],&bounds))continue;
   if(bounds.size.width<=0||bounds.size.height<=0)continue;
   [windows addObject:@{@"id":w[(__bridge NSString *)kCGWindowNumber]?:@0,@"pid":w[(__bridge NSString *)kCGWindowOwnerPID]?:@0,@"title":w[(__bridge NSString *)kCGWindowName]?:@"",@"bounds":ad_rect(bounds)}];
   if(windows.count>=256){windowsTruncated=YES;break;}
  }
  NSMutableArray *applications=[NSMutableArray array];
  // Process Manager 提供实时 GUI 进程集合；NSWorkspace 数组可能停留在启动时。
  ProcessSerialNumber serial={0,kNoProcess};
  for(int visited=0;visited<4096&&GetNextProcess(&serial)==noErr;visited++) {
   pid_t pid=0;if(GetProcessPID(&serial,&pid)!=noErr)continue;
   NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:pid];
   if(!app||app.activationPolicy==NSApplicationActivationPolicyProhibited||app.terminated)continue;
   [applications addObject:@{@"pid":@(pid),@"name":app.localizedName?:@"",@"bundle_id":app.bundleIdentifier?:@"",@"app_path":app.bundleURL.URLByResolvingSymlinksInPath.URLByStandardizingPath.path?:@""}];
   if(applications.count>=256)break;
  }
  CGEventRef cursorEvent=CGEventCreate(NULL);CGPoint cursor=cursorEvent?CGEventGetLocation(cursorEvent):CGPointZero;if(cursorEvent)CFRelease(cursorEvent);
  return ad_json(@{@"cursor":@{@"x":@(cursor.x),@"y":@(cursor.y)},@"windows_truncated":windowsTruncated?@YES:@NO,@"frontmost_pid":@(ad_frontmost()),@"displays":displays,@"windows":windows,@"applications":applications});
 }
}
static int ad_capture_target(uint32_t display,uint32_t window,int pid,int dimension,int timeout_ms,unsigned char **out,size_t *size,int *width,int *height) {
 @autoreleasepool {
  if (@available(macOS 14.0,*)) {
   if(!CGPreflightScreenCaptureAccess())return 5;
   dispatch_semaphore_t done=dispatch_semaphore_create(0);
   __block NSData *encoded=nil;
   __block int actualWidth=0,actualHeight=0;
   // ARC 保留异步回调的所有状态；超时不释放仍可能被回调访问的内存。
   [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content,NSError *error) {
    if(error||!content){dispatch_semaphore_signal(done);return;}
    SCContentFilter *filter=nil;double sourceWidth=0,sourceHeight=0;
    if(window) {
     SCWindow *selected=nil;
     for(SCWindow *candidate in content.windows)if(candidate.windowID==window&&candidate.owningApplication.processID==pid){selected=candidate;break;}
     if(!selected){dispatch_semaphore_signal(done);return;}
     filter=[[SCContentFilter alloc] initWithDesktopIndependentWindow:selected];
     sourceWidth=selected.frame.size.width;sourceHeight=selected.frame.size.height;
    } else {
     SCDisplay *selected=nil;
     for(SCDisplay *candidate in content.displays)if(candidate.displayID==display){selected=candidate;break;}
     if(!selected){dispatch_semaphore_signal(done);return;}
     filter=[[SCContentFilter alloc] initWithDisplay:selected excludingWindows:@[]];
     sourceWidth=selected.width;sourceHeight=selected.height;
    }
    if(sourceWidth<=0||sourceHeight<=0){dispatch_semaphore_signal(done);return;}
    SCStreamConfiguration *configuration=[[SCStreamConfiguration alloc] init];
    double ratio=fmin(1.0,(double)dimension/fmax(sourceWidth,sourceHeight));
    configuration.width=MAX(1,(size_t)llround(sourceWidth*ratio));
    configuration.height=MAX(1,(size_t)llround(sourceHeight*ratio));
    configuration.showsCursor=window?NO:YES;
    configuration.ignoreShadowsSingleWindow=YES;
    [SCScreenshotManager captureImageWithFilter:filter configuration:configuration completionHandler:^(CGImageRef image,NSError *captureError) {
     if(image&&!captureError) {
      NSMutableData *data=[NSMutableData data];
      CGImageDestinationRef destination=CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data,CFSTR("public.jpeg"),1,NULL);
      if(destination) {
       NSDictionary *properties=@{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality:@0.78};
       CGImageDestinationAddImage(destination,image,(__bridge CFDictionaryRef)properties);
       if(CGImageDestinationFinalize(destination)) {encoded=data;actualWidth=(int)CGImageGetWidth(image);actualHeight=(int)CGImageGetHeight(image);}
       CFRelease(destination);
      }
     }
     dispatch_semaphore_signal(done);
    }];
   }];
   if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,(int64_t)timeout_ms*NSEC_PER_MSEC))!=0)return 5;
   if(!encoded||encoded.length==0||encoded.length>8*1024*1024)return 5;
   *out=malloc(encoded.length);if(!*out)return 1;
   memcpy(*out,encoded.bytes,encoded.length);*size=encoded.length;*width=actualWidth;*height=actualHeight;
   return 0;
  }
  return 6;
 }
}
int ad_capture(uint32_t display,int dimension,int timeout_ms,unsigned char **out,size_t *size,int *width,int *height) {
 return ad_capture_target(display,0,0,dimension,timeout_ms,out,size,width,height);
}
int ad_capture_window(uint32_t window,int pid,int dimension,int timeout_ms,unsigned char **out,size_t *size,int *width,int *height) {
 return ad_capture_target(0,window,pid,dimension,timeout_ms,out,size,width,height);
}
static id ad_attribute(AXUIElementRef element,CFStringRef name) {
 CFTypeRef value=NULL;
 if(AXUIElementCopyAttributeValue(element,name,&value)!=kAXErrorSuccess)return nil;
 return CFBridgingRelease(value);
}
static NSString *ad_label(AXUIElementRef element,CFStringRef name) {
 id value=ad_attribute(element,name);
 if(![value isKindOfClass:NSString.class])return @"";
 NSString *text=value;
 return text.length>512?[text substringWithRange:[text rangeOfComposedCharacterSequencesForRange:NSMakeRange(0,512)]]:text;
}
static CGRect ad_bounds(AXUIElementRef element) {
 CGPoint point=CGPointZero;CGSize size=CGSizeZero;
 CFTypeRef pos=NULL,sz=NULL;
 if(AXUIElementCopyAttributeValue(element,kAXPositionAttribute,&pos)==kAXErrorSuccess&&pos&&CFGetTypeID(pos)==AXValueGetTypeID())AXValueGetValue((AXValueRef)pos,kAXValueCGPointType,&point);
 if(AXUIElementCopyAttributeValue(element,kAXSizeAttribute,&sz)==kAXErrorSuccess&&sz&&CFGetTypeID(sz)==AXValueGetTypeID())AXValueGetValue((AXValueRef)sz,kAXValueCGSizeType,&size);
 if(pos)CFRelease(pos);if(sz)CFRelease(sz);
 return CGRectMake(point.x,point.y,size.width,size.height);
}
static NSDictionary *ad_element(AXUIElementRef element,NSArray *path) {
 NSString *role=ad_label(element,kAXRoleAttribute);
 NSString *subrole=ad_label(element,kAXSubroleAttribute);
 BOOL secure=[subrole isEqualToString:(__bridge NSString *)kAXSecureTextFieldSubrole];
 CFArrayRef names=NULL;BOOL pressable=NO;
 if(AXUIElementCopyActionNames(element,&names)==kAXErrorSuccess&&names){pressable=CFArrayContainsValue(names,CFRangeMake(0,CFArrayGetCount(names)),kAXPressAction);CFRelease(names);}
 id enabled=ad_attribute(element,kAXEnabledAttribute);
 Boolean settable=false;
 if(!secure&&([role isEqualToString:(__bridge NSString *)kAXTextFieldRole]||[role isEqualToString:(__bridge NSString *)kAXTextAreaRole]))AXUIElementIsAttributeSettable(element,kAXValueAttribute,&settable);
 // 从不读取 AXValue、选中文本或密码；安全文本框的标签也隐藏。
 return @{@"role":role,@"title":secure?@"[redacted]":ad_label(element,kAXTitleAttribute),@"description":secure?@"":ad_label(element,kAXDescriptionAttribute),@"bounds":ad_rect(ad_bounds(element)),@"enabled":([enabled isKindOfClass:NSNumber.class]&&[enabled boolValue])?@YES:@NO,@"enabled_known":[enabled isKindOfClass:NSNumber.class]?@YES:@NO,@"pressable":(pressable&&!secure)?@YES:@NO,@"value_settable":settable?@YES:@NO,@"path":path};
}
static void ad_walk(AXUIElementRef node,NSArray *path,NSMutableArray *output,CFMutableSetRef seen,int nodes,int depth,CFAbsoluteTime deadline,BOOL *truncated) {
 if(output.count>=(NSUInteger)nodes||CFAbsoluteTimeGetCurrent()>deadline){*truncated=YES;return;}
 if(CFSetContainsValue(seen,node))return;
 CFSetAddValue(seen,node);
 AXUIElementSetMessagingTimeout(node,0.10);
 [output addObject:ad_element(node,path)];
 CFIndex count=0;
 if(AXUIElementGetAttributeValueCount(node,kAXChildrenAttribute,&count)!=kAXErrorSuccess||count<=0)return;
 if((int)path.count>=depth){*truncated=YES;return;}
 CFIndex limit=MIN(count,nodes-(int)output.count);
 if(limit<=0){*truncated=YES;return;}
 CFArrayRef children=NULL;
 if(AXUIElementCopyAttributeValues(node,kAXChildrenAttribute,0,limit,&children)!=kAXErrorSuccess||!children)return;
 for(CFIndex i=0;i<CFArrayGetCount(children);i++) {
  if(output.count>=(NSUInteger)nodes||CFAbsoluteTimeGetCurrent()>deadline){*truncated=YES;break;}
  CFTypeRef child=CFArrayGetValueAtIndex(children,i);
  if(CFGetTypeID(child)!=AXUIElementGetTypeID())continue;
  ad_walk((AXUIElementRef)child,[path arrayByAddingObject:@(i)],output,seen,nodes,depth,deadline,truncated);
 }
 if(limit<count)*truncated=YES;
 CFRelease(children);
}
char *ad_tree(int pid,int nodes,int depth) {
 @autoreleasepool {
  if(ad_guard(pid,false)!=0)return ad_json(@{@"error":@"Accessibility permission or foreground target is unavailable"});
  AXUIElementRef root=AXUIElementCreateApplication(pid);if(!root)return NULL;
  CFMutableSetRef seen=CFSetCreateMutable(NULL,0,&kCFTypeSetCallBacks);
  NSMutableArray *elements=[NSMutableArray array];BOOL truncated=NO;
  ad_walk(root,@[],elements,seen,nodes,depth,CFAbsoluteTimeGetCurrent()+3.0,&truncated);
  CFRelease(seen);CFRelease(root);
  return ad_json(@{@"elements":elements,@"truncated":truncated?@YES:@NO});
 }
}
int ad_press(int pid,const char *element_json) {
 @autoreleasepool {
  int guard=ad_guard(pid,false);if(guard)return guard;
  NSDictionary *expected=[NSJSONSerialization JSONObjectWithData:[[NSString stringWithUTF8String:element_json] dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
  if(![expected isKindOfClass:NSDictionary.class])return 4;
  NSArray *path=expected[@"path"]?:@[];
  if(path.count>16)return 4;
  AXUIElementRef node=AXUIElementCreateApplication(pid);if(!node)return 1;
  for(NSNumber *index in path) {
   AXUIElementSetMessagingTimeout(node,0.10);
   CFArrayRef children=NULL;
   AXError err=AXUIElementCopyAttributeValues(node,kAXChildrenAttribute,index.intValue,1,&children);
   CFRelease(node);node=NULL;
   if(err!=kAXErrorSuccess||!children||CFArrayGetCount(children)!=1){if(children)CFRelease(children);return 4;}
   CFTypeRef child=CFArrayGetValueAtIndex(children,0);
   if(CFGetTypeID(child)==AXUIElementGetTypeID())node=(AXUIElementRef)CFRetain(child);
   CFRelease(children);if(!node)return 4;
  }
  AXUIElementSetMessagingTimeout(node,0.10);
  NSDictionary *actual=ad_element(node,path);
  BOOL matches=[actual[@"role"] isEqual:expected[@"role"]]&&[actual[@"title"] isEqual:expected[@"title"]]&&[actual[@"description"] isEqual:expected[@"description"]]&&[actual[@"bounds"] isEqual:expected[@"bounds"]]&&[actual[@"enabled"] boolValue]&&[actual[@"pressable"] boolValue];
  guard=ad_guard(pid,false);
  int result=guard?guard:(!matches?4:(AXUIElementPerformAction(node,kAXPressAction)==kAXErrorSuccess?0:4));
  CFRelease(node);return result;
 }
}
int ad_activate(int pid) {
 @autoreleasepool {
  int guard=ad_guard(0,false);if(guard)return guard;
  NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:pid];
  if(!app||app.terminated)return 4;
  return [app activateWithOptions:NSApplicationActivateIgnoringOtherApps]?0:4;
 }
}
static CGEventType ad_mouse_type(int kind,int button) {
 if(kind==1)return kCGEventMouseMoved;
 if(kind==3)return button==0?kCGEventLeftMouseUp:(button==1?kCGEventRightMouseUp:kCGEventOtherMouseUp);
 if(kind==4)return button==0?kCGEventLeftMouseDragged:(button==1?kCGEventRightMouseDragged:kCGEventOtherMouseDragged);
 return button==0?kCGEventLeftMouseDown:(button==1?kCGEventRightMouseDown:kCGEventOtherMouseDown);
}
int ad_mouse(int pid,int kind,double x,double y,int button,int count,uint64_t flags) {
 @autoreleasepool {
  int guard=ad_guard(pid,false);if(guard)return guard;
  CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return 1;
  if(kind==0) {
   for(int i=1;i<=count;i++) {
    guard=ad_guard(pid,false);if(guard){CFRelease(source);return guard;}
    CGEventRef down=CGEventCreateMouseEvent(source,ad_mouse_type(2,button),CGPointMake(x,y),(CGMouseButton)button);
    CGEventRef up=CGEventCreateMouseEvent(source,ad_mouse_type(3,button),CGPointMake(x,y),(CGMouseButton)button);
    if(!down||!up){if(down)CFRelease(down);if(up)CFRelease(up);CFRelease(source);return 1;}
    CGEventSetFlags(down,(CGEventFlags)flags);CGEventSetFlags(up,(CGEventFlags)flags);
    CGEventSetIntegerValueField(down,kCGMouseEventClickState,i);CGEventSetIntegerValueField(up,kCGMouseEventClickState,i);
    CGEventPost(kCGHIDEventTap,down);CGEventPost(kCGHIDEventTap,up);CFRelease(down);CFRelease(up);
    if(i<count)usleep(50000);
   }
  } else {
   CGEventRef event=CGEventCreateMouseEvent(source,ad_mouse_type(kind,button),CGPointMake(x,y),(CGMouseButton)button);
   if(!event){CFRelease(source);return 1;}
   CGEventSetFlags(event,(CGEventFlags)flags);CGEventPost(kCGHIDEventTap,event);CFRelease(event);
  }
  CFRelease(source);return 0;
 }
}
int ad_scroll(int pid,int dx,int dy) {
 @autoreleasepool {
  int guard=ad_guard(pid,false);if(guard)return guard;
  CGEventRef event=CGEventCreateScrollWheelEvent(NULL,kCGScrollEventUnitPixel,2,dy,dx);if(!event)return 1;
  CGEventPost(kCGHIDEventTap,event);CFRelease(event);return 0;
 }
}
static int ad_keyboard(int pid,uint16_t key,uint64_t flags,const uint16_t *text,size_t length) {
 int guard=ad_guard(pid,true);if(guard)return guard;
 CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return 1;
 CGEventRef down=CGEventCreateKeyboardEvent(source,key,true),up=CGEventCreateKeyboardEvent(source,key,false);
 if(!down||!up){if(down)CFRelease(down);if(up)CFRelease(up);CFRelease(source);return 1;}
 CGEventSetFlags(down,(CGEventFlags)flags);CGEventSetFlags(up,0);
 if(text&&length){CGEventKeyboardSetUnicodeString(down,length,text);CGEventKeyboardSetUnicodeString(up,length,text);}
 CGEventPost(kCGHIDEventTap,down);CGEventPost(kCGHIDEventTap,up);
 CFRelease(down);CFRelease(up);CFRelease(source);return 0;
}
int ad_key(int pid,uint16_t key,uint64_t flags) {@autoreleasepool{return ad_keyboard(pid,key,flags,NULL,0);}}
int ad_text(int pid,const uint16_t *text,size_t length) {@autoreleasepool{return ad_keyboard(pid,0,0,text,length);}}

#include "native_window_impl.h"

#include "native_launch_impl.h"
