// 后台应用可能忽略菜单快捷键事件；在投递前优先匹配其公开 AX 菜单快捷键信息。
// 只读遍历不打开菜单；精确唯一匹配后执行一次 AXPress，失败不重试其他路径。
static AXUIElementRef ad_find_menu_shortcut(AXUIElementRef app,uint16_t key,uint64_t flags,BOOL *incomplete) {
 AXUIElementSetMessagingTimeout(app,0.10);
 id menu=ad_attribute(app,kAXMenuBarAttribute);
 if(!menu||CFGetTypeID((__bridge CFTypeRef)menu)!=AXUIElementGetTypeID())return NULL;
 NSMutableArray *queue=[NSMutableArray arrayWithObject:@{@"node":menu,@"depth":@0}];
 NSUInteger at=0;AXUIElementRef match=NULL;
 CFAbsoluteTime deadline=CFAbsoluteTimeGetCurrent()+1.5;
 UInt32 modifiers=0;
 if(flags&kCGEventFlagMaskShift)modifiers|=kAXMenuItemModifierShift;
 if(flags&kCGEventFlagMaskAlternate)modifiers|=kAXMenuItemModifierOption;
 if(flags&kCGEventFlagMaskControl)modifiers|=kAXMenuItemModifierControl;
 if(!(flags&kCGEventFlagMaskCommand))modifiers|=kAXMenuItemModifierNoCommand;
 UniChar chars[20];UniCharCount length=0;
 NSString *character=ad_shortcut_characters(key,0,chars,&length)?[NSString stringWithCharacters:chars length:length]:@"";
 while(at<queue.count){
  if(at>=256||CFAbsoluteTimeGetCurrent()>deadline){*incomplete=YES;break;}
  NSDictionary *entry=queue[at++];AXUIElementRef node=(__bridge AXUIElementRef)entry[@"node"];
  AXUIElementSetMessagingTimeout(node,0.05);
  NSString *role=ad_label(node,kAXRoleAttribute);
  if([role isEqualToString:(__bridge NSString *)kAXMenuItemRole]){
   id virtualKey=ad_attribute(node,kAXMenuItemCmdVirtualKeyAttribute);
   id nativeModifiers=ad_attribute(node,kAXMenuItemCmdModifiersAttribute);
   NSString *cmdChar=ad_label(node,kAXMenuItemCmdCharAttribute);
   BOOL sameKey=(cmdChar.length>0&&character.length>0&&[cmdChar.lowercaseString isEqual:character.lowercaseString])||(key!=0&&[virtualKey isKindOfClass:NSNumber.class]&&[virtualKey unsignedShortValue]==key);
   if(sameKey&&[nativeModifiers isKindOfClass:NSNumber.class]&&[nativeModifiers unsignedIntValue]==modifiers){
    if(match){*incomplete=YES;break;}
    match=(AXUIElementRef)CFRetain(node);
   }
  }
  CFIndex count=0;
  if(AXUIElementGetAttributeValueCount(node,kAXChildrenAttribute,&count)!=kAXErrorSuccess||count<=0)continue;
  int depth=[entry[@"depth"] intValue];
  if(depth>=8||count>(CFIndex)(256-queue.count)){*incomplete=YES;break;}
  CFArrayRef children=NULL;
  if(AXUIElementCopyAttributeValues(node,kAXChildrenAttribute,0,count,&children)!=kAXErrorSuccess||!children){*incomplete=YES;break;}
  for(id child in (__bridge NSArray *)children){if(CFGetTypeID((__bridge CFTypeRef)child)==AXUIElementGetTypeID())[queue addObject:@{@"node":child,@"depth":@(depth+1)}];}
  CFRelease(children);
 }
 if(*incomplete&&match){CFRelease(match);match=NULL;}
 return match;
}
static int ad_menu_shortcut(NSDictionary *target,NSDictionary *input) {
 uint64_t flags=[input[@"flags"] unsignedLongLongValue];
 if(!(flags&kCGEventFlagMaskCommand))return -1;
 AXUIElementRef app=AXUIElementCreateApplication([target[@"pid"] intValue]);if(!app)return 1;
 BOOL incomplete=NO;AXUIElementRef match=ad_find_menu_shortcut(app,[input[@"key_code"] unsignedShortValue],flags,&incomplete);CFRelease(app);
 if(incomplete)return 14;
 if(!match)return -1;
 if(![ad_attribute(match,kAXEnabledAttribute) boolValue]){CFRelease(match);return 15;}
 int guard=ad_window_guard(target,YES,NO);
 AXUIElementSetMessagingTimeout(match,1.0);
 int result=guard?guard:(AXUIElementPerformAction(match,kAXPressAction)==kAXErrorSuccess?0:4);
 CFRelease(match);return result;
}
