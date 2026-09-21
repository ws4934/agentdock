// 仅供显式启用的 macOS 桌面验收；没有外部文件、网络或真实用户文档。
#import <Cocoa/Cocoa.h>

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
@property int clicks;
@property int scrolls;
@property int drags;
@property int releases;
-(void)save;
@end
@implementation FixtureCanvas
-(BOOL)acceptsFirstResponder{return YES;}
-(void)drawRect:(NSRect)dirtyRect{[[NSColor controlBackgroundColor] setFill];NSRectFill(self.bounds);[@"Isolated drag and scroll target" drawAtPoint:NSMakePoint(12,25) withAttributes:nil];}
-(void)mouseDown:(NSEvent *)event{[self.owner save];}
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
 NSDictionary *state=@{@"pid":@(NSProcessInfo.processInfo.processIdentifier),@"clicks":@(self.clicks),@"scrolls":@(self.scrolls),@"drags":@(self.drags),@"releases":@(self.releases),@"text":self.field.stringValue?:@"",@"field":[self pointFor:self.field],@"button":[self pointFor:self.button],@"canvas":[self pointFor:self.canvas]};
 NSData *json=[NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
 [json writeToFile:self.statePath atomically:YES];
}
-(void)clicked:(id)sender{self.clicks++;[self save];}
-(void)controlTextDidChange:(NSNotification *)notification{[self save];}
-(void)applicationDidFinishLaunching:(NSNotification *)notification{
 NSMenu *menu=[[NSMenu alloc]initWithTitle:@"Main"];
 NSMenuItem *item=[[NSMenuItem alloc]initWithTitle:@"Edit" action:NULL keyEquivalent:@""];
 NSMenu *edit=[[NSMenu alloc]initWithTitle:@"Edit"];
 [edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
 [menu addItem:item];[menu setSubmenu:edit forItem:item];NSApp.mainMenu=menu;
 self.window=[[NSWindow alloc]initWithContentRect:NSMakeRect(0,0,520,260) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable backing:NSBackingStoreBuffered defer:NO];
 self.window.title=@"AgentDock Computer Use Test";
 self.window.releasedWhenClosed=NO;
 self.field=[[NSTextField alloc]initWithFrame:NSMakeRect(30,190,340,28)];
 self.field.delegate=self;self.field.accessibilityLabel=@"Computer Use test input";
 self.button=[NSButton buttonWithTitle:@"Test Click" target:self action:@selector(clicked:)];self.button.frame=NSMakeRect(380,186,110,34);
 self.canvas=[[FixtureCanvas alloc]initWithFrame:NSMakeRect(30,30,460,120)];self.canvas.owner=self;
 [self.window.contentView addSubview:self.field];[self.window.contentView addSubview:self.button];[self.window.contentView addSubview:self.canvas];
 [self.window center];[self.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES];
 [self save];
}
@end
int main(int argc,const char **argv){
 @autoreleasepool{
  if(argc!=2)return 2;
  [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
  FixtureDelegate *delegate=[[FixtureDelegate alloc]init];delegate.statePath=[NSString stringWithUTF8String:argv[1]];NSApp.delegate=delegate;
  [NSApp run];
 }
 return 0;
}
