// 仅供显式启用的 macOS 桌面验收；没有外部文件、网络或真实用户文档。
#import <Cocoa/Cocoa.h>
#include "mouse_observer.h"

@class FixtureDelegate;
@interface FixtureCanvas : NSView
@property(nonatomic,weak) FixtureDelegate *owner;
@end
@interface FixtureDelegate : NSObject <NSApplicationDelegate,NSTextFieldDelegate>
@property(strong) NSWindow *window;
@property(strong) NSTextField *field;
@property(strong) NSButton *button;
@property(strong) FixtureCanvas *canvas;
@property(copy) NSString *statePath;
@property BOOL background;
@property int keyEvents;
@property unsigned short lastKeyCode;
@property NSUInteger lastKeyLength;
@property NSUInteger lastKeyFlags;
@property int mouseEvents;
@property int lastEventWindow;
@property NSPoint lastEventLocation;
@property int clicks;
@property int scrolls;
@property int drags;
@property int downs;
@property int moves;
@property int releases;
-(void)save;
@end
@implementation FixtureCanvas
-(BOOL)acceptsFirstResponder{return YES;}
// 这个验收画布明确支持后台首次点击；真实第三方控件可以拒绝，工具不能强行绕过。
-(BOOL)acceptsFirstMouse:(NSEvent *)event{return YES;}
-(void)drawRect:(NSRect)dirtyRect{[(self.owner.background?[NSColor colorWithSRGBRed:0.1 green:0.3 blue:0.8 alpha:1]:[NSColor colorWithSRGBRed:0.8 green:0.2 blue:0.1 alpha:1]) setFill];NSRectFill(self.bounds);[@"Isolated drag and scroll target" drawAtPoint:NSMakePoint(12,25) withAttributes:nil];}
-(void)mouseDown:(NSEvent *)event{self.owner.downs++;[self.owner save];}
-(void)mouseDragged:(NSEvent *)event{self.owner.drags++;[self.owner save];}
-(void)mouseUp:(NSEvent *)event{self.owner.releases++;[self.owner save];}
-(void)scrollWheel:(NSEvent *)event{self.owner.scrolls++;[self.owner save];}
@end
@implementation FixtureDelegate
-(NSDictionary *)pointFor:(NSView *)view {
 NSRect frame=[view.window convertRectToScreen:[view convertRect:view.bounds toView:nil]];
 CGFloat top=NSMaxY(NSScreen.screens.firstObject.frame);
 return @{@"x":@(NSMidX(frame)),@"y":@(top-NSMidY(frame))};
}
-(void)save {
 NSDictionary *state=@{@"menu_enabled":@([NSApp.mainMenu.itemArray.firstObject.submenu.itemArray.firstObject isEnabled]),@"selection_length":@([(NSTextView *)self.field.currentEditor selectedRange].length),@"mouse_observer":fixtureMouseSample(),@"pid":@(NSProcessInfo.processInfo.processIdentifier),@"window_id":@(self.window.windowNumber),@"active":@(NSApp.active),@"key_window":@(self.window.keyWindow),@"responder":NSStringFromClass(self.window.firstResponder.class)?:@"",@"key_events":@(self.keyEvents),@"last_key_code":@(self.lastKeyCode),@"last_key_length":@(self.lastKeyLength),@"last_key_flags":@(self.lastKeyFlags),@"mouse_events":@(self.mouseEvents),@"last_event_window":@(self.lastEventWindow),@"last_event_location":@{@"x":@(self.lastEventLocation.x),@"y":@(self.lastEventLocation.y)},@"clicks":@(self.clicks),@"scrolls":@(self.scrolls),@"drags":@(self.drags),@"downs":@(self.downs),@"moves":@(self.moves),@"releases":@(self.releases),@"text":self.field.stringValue?:@"",@"field":[self pointFor:self.field],@"button":[self pointFor:self.button],@"canvas":[self pointFor:self.canvas]};
 NSData *json=[NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
 [json writeToFile:self.statePath atomically:YES];
}
-(void)clicked:(id)sender{self.clicks++;[self save];}
-(void)controlTextDidChange:(NSNotification *)notification{[self save];}
-(void)applicationDidFinishLaunching:(NSNotification *)notification{
 fixtureStartMouseObserver();
 NSMenu *menu=[[NSMenu alloc]initWithTitle:@"Main"];
 NSMenuItem *item=[[NSMenuItem alloc]initWithTitle:@"Edit" action:NULL keyEquivalent:@""];
 NSMenu *edit=[[NSMenu alloc]initWithTitle:@"Edit"];
 [edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
 NSMenuItem *counter=[edit addItemWithTitle:@"Count Shortcut" action:@selector(clicked:) keyEquivalent:@"k"];counter.target=self;
 [menu addItem:item];[menu setSubmenu:edit forItem:item];NSApp.mainMenu=menu;
 self.window=[[NSWindow alloc]initWithContentRect:NSMakeRect(0,0,520,260) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO];
 self.window.title=self.background?@"AgentDock Background Target":@"AgentDock Foreground Sentinel";
 self.window.releasedWhenClosed=NO;self.window.acceptsMouseMovedEvents=YES;
 self.field=[[NSTextField alloc]initWithFrame:NSMakeRect(30,190,340,28)];
 self.field.delegate=self;self.field.accessibilityLabel=@"Computer Use test input";
 self.button=[NSButton buttonWithTitle:@"Test Click" target:self action:@selector(clicked:)];self.button.frame=NSMakeRect(380,186,110,34);
 self.canvas=[[FixtureCanvas alloc]initWithFrame:NSMakeRect(30,30,460,120)];self.canvas.owner=self;
 [self.window.contentView addSubview:self.field];[self.window.contentView addSubview:self.button];[self.window.contentView addSubview:self.canvas];
 [self.window center];
 if(self.background){[self.window orderBack:nil];[self.window makeKeyWindow];}
 else{[self.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES];}
 [self.window makeFirstResponder:self.field];
 [NSEvent addLocalMonitorForEventsMatchingMask:(NSEventMaskMouseMoved|NSEventMaskKeyDown|NSEventMaskLeftMouseDown|NSEventMaskRightMouseDown|NSEventMaskOtherMouseDown|NSEventMaskScrollWheel) handler:^NSEvent *(NSEvent *event){
  if(event.type==NSEventTypeMouseMoved){if(self.background)self.moves++;}else if(event.type==NSEventTypeKeyDown){self.keyEvents++;self.lastKeyCode=event.keyCode;self.lastKeyLength=event.characters.length;self.lastKeyFlags=event.modifierFlags;}else{self.mouseEvents++;self.lastEventWindow=(int)event.windowNumber;self.lastEventLocation=event.locationInWindow;}
  [self save];return event;
 }];
 [NSTimer scheduledTimerWithTimeInterval:0.05 repeats:YES block:^(NSTimer *timer){[self save];}];
 [self save];
}
@end
int main(int argc,const char **argv){
 @autoreleasepool{
  if(argc<2||argc>3)return 2;
  [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
  FixtureDelegate *delegate=[[FixtureDelegate alloc]init];delegate.statePath=[NSString stringWithUTF8String:argv[1]];delegate.background=argc==3&&strcmp(argv[2],"--background")==0;NSApp.delegate=delegate;
  [NSApp run];
 }
 return 0;
}
