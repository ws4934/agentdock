// LaunchServices / NSWorkspace 原生启动桥；不执行 shell，不传启动参数、文档或 URL。
static NSDictionary *ad_application_error(NSString *code,NSString *message,BOOL requested){
 return @{@"code":code,@"error":message,@"may_have_launched":requested?@YES:@NO};
}
// 仅用于即时实例识别；LaunchServices 对刚启动或未索引的 .app 可能尚无 bundle-id 查询结果。
static NSArray<NSRunningApplication *> *ad_live_launch_apps(void){
 NSMutableArray *apps=[NSMutableArray array];ProcessSerialNumber serial={0,kNoProcess};int visited=0;
 while(GetNextProcess(&serial)==noErr){
  if(++visited>4096)return nil;
  pid_t pid=0;if(GetProcessPID(&serial,&pid)!=noErr)continue;
  NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:pid];
  if(app&&!app.terminated&&app.bundleIdentifier.length>0&&app.bundleURL)[apps addObject:app];
 }
 return apps;
}
static NSDictionary *ad_resolve_app(NSDictionary *request){
 NSString *path=request[@"app_path"],*identifier=request[@"bundle_id"];
 NSURL *url=nil;
 if(path.length>0)url=[NSURL fileURLWithPath:path isDirectory:YES];
 else if(identifier.length>0){
  url=[NSWorkspace.sharedWorkspace URLForApplicationWithBundleIdentifier:identifier];
  if(!url){
   NSArray *apps=ad_live_launch_apps();if(!apps)return ad_application_error(@"APPLICATION_LOOKUP_INCOMPLETE",@"Running app enumeration exceeded its bounds",NO);
   for(NSRunningApplication *app in apps){
    if(![app.bundleIdentifier isEqual:identifier])continue;
    NSURL *candidate=app.bundleURL.URLByResolvingSymlinksInPath.URLByStandardizingPath;
    if(url&&![url.path isEqual:candidate.path])return ad_application_error(@"APPLICATION_AMBIGUOUS",@"Running apps with this identifier use different paths; specify app_path",NO);
    url=candidate;
   }
  }
 }
 if(!url||!url.fileURL)return ad_application_error(@"APPLICATION_NOT_FOUND",@"No local application was found",NO);
 url=url.URLByResolvingSymlinksInPath.URLByStandardizingPath;
 BOOL directory=NO;
 if(![url.pathExtension.lowercaseString isEqual:@"app"]||![NSFileManager.defaultManager fileExistsAtPath:url.path isDirectory:&directory]||!directory)return ad_application_error(@"INVALID_APPLICATION",@"The selected path is not an application bundle",NO);
 NSBundle *bundle=[NSBundle bundleWithURL:url];
 NSString *bundleID=bundle.bundleIdentifier;
 if(!bundle||bundleID.length==0||![bundle.infoDictionary[@"CFBundlePackageType"] isEqual:@"APPL"]||!bundle.executableURL||![NSFileManager.defaultManager isExecutableFileAtPath:bundle.executableURL.path])return ad_application_error(@"INVALID_APPLICATION",@"Application bundle metadata or executable is invalid",NO);
 if(identifier.length>0&&![identifier isEqual:bundleID])return ad_application_error(@"APPLICATION_IDENTITY_CHANGED",@"Resolved application does not match the expected bundle identifier",NO);
 return @{@"bundle_id":bundleID,@"app_path":url.path,@"name":bundle.infoDictionary[@"CFBundleDisplayName"]?:bundle.infoDictionary[@"CFBundleName"]?:url.lastPathComponent};
}
char *ad_resolve_application(const char *json){
 @autoreleasepool {
  NSDictionary *request=ad_decode_object(json);
  if(!request)return ad_json(ad_application_error(@"INVALID_ARGUMENT",@"Invalid launch selector",NO));
  return ad_json(ad_resolve_app(request));
 }
}
static NSDictionary *ad_application_result(NSRunningApplication *app,BOOL existed,BOOL requested,BOOL activate){
 return @{@"application":@{@"pid":@(app.processIdentifier),@"bundle_id":app.bundleIdentifier?:@"",@"name":app.localizedName?:@""},@"already_running":existed?@YES:@NO,@"launch_requested":requested?@YES:@NO,@"activation_requested":activate?@YES:@NO};
}
char *ad_launch_application(const char *json,int foreground,int timeout_ms){
 @autoreleasepool {
  NSDictionary *target=ad_decode_object(json);
  NSDictionary *resolved=ad_resolve_app(target);
  if(resolved[@"error"])return ad_json(resolved);
  if(![resolved[@"app_path"] isEqual:target[@"app_path"]]||![resolved[@"bundle_id"] isEqual:target[@"bundle_id"]])return ad_json(ad_application_error(@"APPLICATION_IDENTITY_CHANGED",@"The application changed after resolution; observe again",NO));
  NSURL *url=[NSURL fileURLWithPath:resolved[@"app_path"] isDirectory:YES];
  // 实时枚举避免 daemon 无 AppKit 主循环时 NSWorkspace runningApplications 缓存过期。
  NSRunningApplication *existing=nil;NSArray *running=ad_live_launch_apps();
  if(!running)return ad_json(ad_application_error(@"APPLICATION_LOOKUP_INCOMPLETE",@"Running application enumeration exceeded its bounds",NO));
  for(NSRunningApplication *candidate in running){
   if(![candidate.bundleIdentifier isEqual:resolved[@"bundle_id"]])continue;
   if(![candidate.bundleURL.URLByResolvingSymlinksInPath.URLByStandardizingPath.path isEqual:url.path])continue;
   if(existing)return ad_json(ad_application_error(@"APPLICATION_AMBIGUOUS",@"Multiple instances of this app are already running; observe and select an existing PID/window",NO));
   existing=candidate;
  }
  if(existing){
   BOOL activate=foreground&&ad_frontmost()!=existing.processIdentifier;
   if(activate&&![existing activateWithOptions:NSApplicationActivateIgnoringOtherApps])return ad_json(ad_application_error(@"DESKTOP_LAUNCH_FAILED",@"Existing application rejected explicit foreground activation",NO));
   // 默认后台复用不发送 reopen AppleEvent，避免无意新建文档或窗口。
   return ad_json(ad_application_result(existing,YES,NO,activate));
  }
  NSWorkspaceOpenConfiguration *configuration=NSWorkspaceOpenConfiguration.configuration;
  configuration.activates=foreground?YES:NO;
  configuration.hides=NO;configuration.hidesOthers=NO;
  configuration.createsNewApplicationInstance=NO;
  configuration.allowsRunningApplicationSubstitution=NO;
  configuration.addsToRecentItems=NO;
  // 不批准或绕过 Gatekeeper/TCC。Gatekeeper 仍可能显示系统 UI，超时只报告未知状态。
  configuration.promptsUserIfNeeded=NO;
  dispatch_semaphore_t done=dispatch_semaphore_create(0);
  __block NSDictionary *outcome=nil;
  [NSWorkspace.sharedWorkspace openApplicationAtURL:url configuration:configuration completionHandler:^(NSRunningApplication *app,NSError *error){
   if(error||!app)outcome=ad_application_error(@"DESKTOP_LAUNCH_FAILED",error.localizedDescription?:@"LaunchServices did not return a running application",YES);
   else if(app.terminated||app.processIdentifier<=0||![app.bundleIdentifier isEqual:resolved[@"bundle_id"]]||![app.bundleURL.URLByResolvingSymlinksInPath.URLByStandardizingPath.path isEqual:url.path])outcome=ad_application_error(@"APPLICATION_IDENTITY_CHANGED",@"LaunchServices returned a different or terminated application; inspect before retrying",YES);
   else outcome=ad_application_result(app,NO,YES,foreground!=0);
   dispatch_semaphore_signal(done);
  }];
  // ARC 保留回调状态。超时不取消系统启动请求，也不能自动再发一次。
  if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,(int64_t)timeout_ms*NSEC_PER_MSEC))!=0)return ad_json(ad_application_error(@"DESKTOP_LAUNCH_TIMEOUT",@"LaunchServices did not finish in time; the app may still start. Observe before retrying",YES));
  return ad_json(outcome?:ad_application_error(@"DESKTOP_LAUNCH_FAILED",@"Launch returned no result",YES));
 }
}
