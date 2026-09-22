# macOS 已开启权限，但 AgentDock 显示不可用

## 本次确认的根因

在 `2cdbd66` 安装后的实际主机上，系统设置中的 AgentDock 屏幕录制开关为开，但菜单栏应用的 `CGPreflightScreenCaptureAccess()` 和 `AXIsProcessTrusted()` 返回 false。该应用签名完整且来自 `/Applications/AgentDock.app`，并非安装路径错了或包损坏。

本机 TCC 系统日志明确记录：

```text
Failed to match existing code requirement for subject com.uvwt.agentdock and service kTCCServiceScreenCapture
Failed to match existing code requirement for subject com.uvwt.agentdock and service kTCCServiceAccessibility
SecStaticCodeCheckValidity ... status: -67050
```

旧授权要求的代码哈希以 `6c0570f9...` / `89108dd0...` 开头，实际已安装应用为 `9600b683...` / `15ecb497...`。本机签名检查确认 `Signature=adhoc`、没有 TeamIdentifier，也没有可用的证书签名身份。

这是**旧授权与新构建代码身份不匹配**，不是简单的界面假红灯：系统仍拒绝当前进程。不要依据设置中的开关直接将工具权限标为 true，也不要通过其他进程代拍屏幕来绕过拒绝。

## 本轮代码修复

- 权限页在可见时每两秒重新检查，返回应用/权限窗口时立即刷新；关闭后停止计时器，丢弃旧回包。原先只有打开时和请求后0.8秒各检查一次，无法跟上用户在系统设置中的后续操作。
- 分别显示菜单栏应用与实际运行 Core 的权限。Core 通过已有本机 socket 的只读 `computeruse.permissions` 返回自身 PID、可执行路径、构建提交、检查时间和原生 API 结果，不启动临时 helper 来冒充服务。
- 诊断查询不申请权限、不截屏、不操作应用、不取得监视租约，也不解除暂停或停止。不接受额外参数，不作为新 MCP 变更工具公开。
- Core 断连、旧版不支持诊断或数据异常时显示“无法检查”，清除旧的已授权显示，不推断“未授权”。
- AX/屏幕录制 false 文案改为“当前未生效”，明确检查的是进程可用性，不是系统开关。Automation 仍按自身公开 API 结果显示。
- 通过公开 Security API 展示当前代码身份；ad-hoc 构建会提示旧授权可能失效，提供“定位当前应用”“定位 Core”“权限修复说明”。不会自行重置授权或降低签名校验。
- 构建脚本在 ad-hoc 签名时明确提示跨构建授权风险。没有偷偷创建/安装证书或伪造 Apple 签名。

## 用户恢复步骤

请在安装最终要使用的应用后执行，避免授权完又更换一份不同代码身份的包。

1. 保存任务，退出 AgentDock 菜单栏应用，并通过应用提供的入口停止相关服务；当前连接会暂时中断。
2. 打开“系统设置 → 隐私与安全性 → 录屏与系统录音”。选中旧的 AgentDock 条目，用 `−` 移除，然后用 `+` 添加 **`/Applications/AgentDock.app`** 并开启。
3. 对“辅助功能”中对应的旧 AgentDock 条目做同样的重新绑定。不要删除其他应用的权限，不要把 Downloads/DMG 中的另一份副本当作实际安装版。
4. 重新打开应用并启动 Core；在新权限页分别核对菜单栏和 Core 的实际结果。如果仅 Core 不可用，依据页面显示的实际可执行路径处理该进程，不能用菜单栏的绿色结果代替它。

macOS 可能要求退出重开或本机认证。这些步骤需要用户在系统界面完成。此次代码更改不会自动授予权限，也不能保证覆盖操作前的旧授权立即对新签名生效。

## 后续更新的稳定方案

ad-hoc 是本机临时签名，指定相同 Bundle ID 并不能让每次构建的代码要求保持相同。后续开发构建应持续使用同一 Apple Development 签名身份；对外分发使用同一 Developer ID Application 身份并按要求公证。现有 `AGENTDOCK_CODESIGN_IDENTITY` / keychain 配置已支持证书签名。

没有可用证书时仍可以制作本机测试包，但须明确提醒重新授权可能再次发生。不要用仅检查 Bundle ID 的宽松代码要求、修改 TCC 数据库、关闭 SIP/Gatekeeper 或改用另一个已授权应用来解决。

## 只读诊断

```sh
"/Applications/AgentDock.app/Contents/MacOS/AgentDock" --permission-diagnostics
```

该命令只输出**此次诊断进程**的身份与 API 结果，不启动菜单栏或后台服务、不请求系统权限。它不能代表一个已运行很久的菜单栏/Core 实例，也不会读取系统设置中的开关。

权限页的 Core 结果来自当前服务进程，不是上述命令。`computeruse.permissions` 是私有的本机只读方法，省略 params 或传空对象即可；它不应携带 controller ID 或操作参数。

## 验证与边界

正式回归覆盖：菜单栏和 Core 结果不同、未知不能冒充拒绝、断连不保留绿色状态、激活后刷新、可见定时刷新、关闭后不轮询、关闭/重开不接纳旧回包，以及只读 Core 查询不产生桌面操作/权限请求或控制状态变更。Swift 界面测试使用注入结果和专用后台窗口，不向系统实际授予权限。

此次读取了与 AgentDock 有关的系统诊断日志和签名信息，没有读取或修改 TCC 数据库。原始本机日志留在 `.git/computer-use/permission-fix/`，不加入 Git，不公开发布。

## 参考

- Apple DTS 对 ad-hoc 重构建身份变化的确认：https://developer.apple.com/forums/thread/819406
- Apple TN3127 Inside Code Signing: Requirements：https://developer.apple.com/documentation/technotes/tn3127-inside-code-signing-requirements
- Apple AXIsProcessTrusted：https://developer.apple.com/documentation/applicationservices/1459186-axisprocesstrusted
