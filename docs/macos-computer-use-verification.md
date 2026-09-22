# macOS Computer Use 验收记录

## 最新验收：主动启动应用与完整输入重验（2026-09-21）

基线：`a93aa29`。分支：`feat/native-macos-computer-use`。本轮新增 `desktop_launch`，同时重新执行此前因安全输入/桌面状态而未完成的后台和前台输入验收。**下表中的真实 GUI 测试全部实际执行通过，没有跳过；历史章节中的阻挡记录只描述当时状态，不是当前未完成项。**

仓库目标已通过 GitHub API 核实：`ws4934/agentdock` 是用户自己的 fork，上游为 `uvwt/agentdock`。代码交付目标为该 fork 的现有 PR #1（开发分支 → `ws4934/agentdock:main`），不向上游创建 PR。没有替换或重启当前正在服务连接的宿主。

### 本轮验证结果

环境：macOS 27.0 / Apple Silicon，Go 1.26.5，CLT SDK 26.5。辅助功能、屏幕录制权限已存在，测试期间 Secure Input 为 false；未修改 TCC、quarantine 或其他系统安全设置。日志和独立构建在 `.git/computer-use/launch/`，不提交或公开发布。

| 检查 | 实际结果与证据 |
| --- | --- |
| `TestNativeDesktopLaunchMCPFlow -count=2` | **连续两次真实 PASS**，耗时约 5.00 / 5.04 秒。日志 `launch-final.log`、退出码 `launch-final.exit=0`。真正通过 MCP 启动此前未运行的临时 `.app`，取得 PID 和稳定窗口、返回有效 JPEG/AX、赋值中文及 emoji、点击按钮，随后用 Bundle ID 复用同一 PID，并验证显式前台激活。 |
| `TestNativeBackgroundTwoApplications` | **完整真实 PASS**，约 4.96 秒。日志 `background.log`、退出码 0。覆盖遮挡截图、AX、坐标点击、Unicode、可用快捷键与禁用命令拒绝、移动、滚动、拖拽和释放，以及用户前台应用拒绝后台输入。 |
| `TestNativeInputIsolatedFixture` | **真实 PASS**，约 3.33 秒。前台显式激活、坐标/AX 点击、Unicode、Command+A、移动/滚动/拖拽/释放均验证。日志 `foreground.log`、退出码 0。 |
| `TestNativeApplicationResolution` | **只读真实 PASS**。系统 TextEdit 的精确名称、Bundle ID、绝对路径解析一致；非法应用路径/不存在标识和取消均被处理，没有启动 TextEdit。 |
| 原生只读观察、前台拒绝、MCP 图像 | **全部真实 PASS**。`TestNativeReadOnlyDesktop`、`TestNativeBackgroundForegroundRefusal`、`TestNativeDesktopMCPImage`，证据 `readonly.log`、退出码 0。 |
| 全仓库 race | `go test -race ./... -count=1 -timeout=3m` 通过，`race.exit=0`。 |
| 静态检查 | `go vet ./...` 通过，`vet.exit=0`。既有实时 Process Manager 原生边界的 deprecation 告警保持可见。 |
| 无 CGO 回归 | desktop / app / MCP 全部通过，`nocgo.exit=0`。 |
| macOS arm64 / amd64 原生构建 | 两个 CGO 构建通过，退出码均 0；只有 arm64 实机运行验证。 |
| Linux / Windows amd64 | 无 CGO 交叉构建通过，退出码均 0；桌面功能仍明确不支持这些平台。 |
| 工作区格式检查 | `git diff --check` 通过。全部长测试/构建使用 tmux detached，逐项检查日志和退出码。 |

### 真实启动/操作隔离检查

MCP 验收使用测试临时目录中的 `.app`，经 `NSWorkspace.openApplication` 启动，未给工具添加任意启动参数或环境变量入口。测试专用的状态路径写在临时应用自己的 Info.plist 内，清理只针对本次随机 Bundle ID 的进程。

后台启动和操作完成后，前台哨兵应用的激活状态、key window、first responder、文本、键盘/鼠标输入计数均未改变，自动化未进入全局鼠标事件流。测试最后才单独请求 `mode:foreground` 验证显式切换，同样复用原 PID。后台双应用重验还确认 0 外部鼠标移动时物理指针保持不变。

### 本轮修复与语义

1. 现有 `activate` 只接受运行中 PID，无法启动关闭的应用；新增独立 `desktop_launch`，启动返回不含输入快照，后续必须重新观察窗口。
2. 新应用可能尚未被 LaunchServices 索引；Bundle ID 查询无结果时，只从实时运行实例取得其真实 Bundle URL，不伪造路径、不写入注册表。
3. 第一个窗口出现并不代表完成布局；正 `wait_ms` 预算要求连续两次元数据稳定，测试同时给独立应用初次动画留出短暂过渡。即便如此仍不宣称业务控件就绪。
4. 已运行的相同路径应用不再发送 reopen 事件，避免意外新建文档；多个实例/名称歧义明确拒绝，窗口等待或原生超时不触发自动重启。
5. 名称搜索采用精确文件名、固定根目录和深度/条目数限制；本地 `.app` 必须有有效 APPL 类型、Bundle ID 和可执行入口。拒绝 shell、文档、URL、任意参数和环境变量字段。

新增启动本身只依赖公开 NSWorkspace API；后台指针仍保留前轮已披露的非公开 `CGEventSetWindowLocation` 兼容风险。应用/Gatekeeper 可能自己显示界面，后台启动请求不等于独立桌面隔离；macOS/Intel/多显示器矩阵和所有第三方应用仍未逐一验收。菜单栏 Swift/DMG/ZIP 打包本轮未重新执行，不借用历史结果宣称新打包已通过。

---

## 历史记录：后台窗口初次交付（2026-09-21）

开发基线：`50b2c8d`。分支：`feat/native-macos-computer-use`。本轮增加默认后台窗口发现/观察、窗口定向输入和显式前台接管边界。代码与独立测试构建已经完成；**没有替换、安装或重启正在服务当前连接的 AgentDock**。

### 环境和证据位置

实际主机：macOS 27.0（26A428）、Apple Silicon，Go 1.26.5，Command Line Tools SDK 26.5。测试获得的辅助功能和屏幕录制权限来自既有系统授权，未修改 TCC 或关闭安全输入。

本轮日志、退出码、测试截图及独立构建保存在本地克隆的 `.git/computer-use/background/`，不会加入 Git，也没有发布为公开 Artifact。测试图像 `window.jpg` 来自本次创建的专用测试窗口；生产截图只经工具响应返回。

### 验证结论

| 检查 | 本轮实际结果 |
| --- | --- |
| `go test -race ./... -count=1 -timeout=3m` | 最终源码全仓库通过，`race.exit=0`。包含后台窗口绑定、默认发现不可授权输入、坐标范围、前台目标拒绝、取消释放、UTF-16、禁用/未知参数及 Runtime 契约测试。 |
| `go vet ./...` | 通过，`vet.exit=0`。既有 Process Manager 的 SDK deprecation 告警仍可见，没有隐藏。 |
| 无 CGO 回归 | `CGO_ENABLED=0 go test ./internal/tool/desktop ./internal/app ./internal/mcp -count=1` 通过，`nocgo.exit=0`。没有原生后端时明确报告不支持。 |
| macOS arm64 / amd64 原生构建 | 两个 CGO 构建均通过，`darwin-arm64.exit=0`、`darwin-amd64.exit=0`。只在 arm64 主机做了实际桌面输入验收。 |
| Linux / Windows amd64 构建 | 两个无 CGO 交叉构建均通过，`linux.exit=0`、`windows.exit=0`。不是在这些系统实现了桌面控制。 |
| 双应用后台实际操作 | **完整核心场景真实通过**，证据为 `gui-pass-initial.log` / `gui-pass-initial.exit=0`，当地时间 12:54，测试耗时 3.82 秒。下节列明具体范围。 |
| 真实只读观察 | `TestNativeReadOnlyDesktop` 通过、未跳过：显示器/窗口状态、AX、JPEG 解码。证据 `readonly.log`。 |
| 真实 MCP 图像传输 | `TestNativeDesktopMCPImage` 通过、未跳过：原生 JPEG 经 Runtime 和 MCP SDK 会话返回有效图像与元数据。证据 `readonly.log`。 |
| 后续真实输入重验 | **未完成最终重验，不计为通过**。后续双应用和前台输入测试因 `SecureInput:true` 跳过；元数据级原生前台拒绝测试因没有可用前台应用窗口跳过。证据 `gui.log`、`foreground.log`、`guards.log`。这些命令退出码为 0 仅代表测试框架正常结束，不代表输入验收通过。 |
| 本轮 Mac 菜单栏应用打包 | 未重新执行 Swift/DMG/ZIP 打包。本轮验证的是 Core 多平台构建，不挪用下文历史打包结果。 |
| 工作区检查 | `git diff --check` 通过。长测试与构建均由 tmux detached 执行，并检查日志及退出码。 |

### 双应用核心验收的真实范围

测试创建两个独立临时应用：后台目标与覆盖它的前台哨兵。只操作本次创建的目标，不对用户文档或真实应用发送测试输入。

通过的目标状态检查包括：被覆盖目标窗口的 520×292 JPEG 与窗口级 AX 树；AX 文本替换和清空；AXPress 按钮；窗口坐标按钮点击；中文与 emoji 输入；退格；已启用菜单的 Command 快捷键；后台移动事件；指定窗口坐标滚动；拖拽和抬起。

前台哨兵的应用激活状态、key window、first responder、文本、键盘和鼠标输入计数均未改变。只读 session 鼠标事件观察器发现 **0 个自动化进入全局鼠标流的事件**；该次通过运行的外部鼠标移动为 **0**，物理光标保持不变。观察器不监听键盘、不拦截或修改任何事件。

随后两次核心场景也完成了全部目标状态和隔离检查，但当时新增的“调用激活后立即验证前台保护”子测试没有等待异步激活完成，导致整条测试被判为失败。该失败完整保存在 `gui-activation-test-before-wait.log`，**不把其整体测试结果写为通过**。最终测试代码改为直接对已处于前台的哨兵做安全拒绝检查，不再依赖激活，也不切换用户焦点；此最终版本的完整真实输入重验受上述主机状态阻挡。对应服务层前台目标拒绝逻辑已有通过的单元回归。

### 验收发现的实现和兼容问题

- 裸 `CGEvent` 直接投递时缺少正确的远端窗口编号/窗口内坐标。改用 `NSEvent` 保留窗口编号，并隔离可检测的 `CGEventSetWindowLocation` 动态桥，随后实际坐标点击通过。
- AXPress 可能等待 AppKit 按钮动画；读取消息预算与动作预算分开，动作最多 1 秒。错误后不自动追加点击或重放。
- `NSEvent` 转成 `CGEvent` 可能重新推导字符；在最终键盘事件上显式保留 Unicode 和修饰键，随后中文/emoji 与退格通过。
- AppKit 在后台禁用了测试应用的“全选”菜单。因此 **后台 Command+A 没有被认定为可用**，而是验证其被明确拒绝且不激活应用。已启用的测试菜单快捷键实际通过；全文替换使用单独的 AX `set_value`。
- 自定义画布必须明确接受后台首次点击，测试画布实现了 `acceptsFirstMouse:`。拖拽通过说明窗口定向输入可用，不代表默认拒绝后台点击的第三方画布也兼容。没有偷偷附加 Command 修饰键或强行激活来通过测试。
- 最初只比较整段测试起止鼠标位置，不能区分用户移动与自动化干扰。后改为只读事件来源监测，外部移动单独计数；不能用早先一次外部移动掩盖后续无来源的指针变化。

### 发布与使用边界

后台路径不调用全局 `CGEventPost`、不移动真实指针、不主动激活应用、不改变系统焦点、不回退到前台输入。拖拽释放始终绑定原 PID/窗口；窗口移动后释放仍校验原身份，而不是向当前用户窗口释放。

完整后台指针依赖非公开的 **`CGEventSetWindowLocation`**，仅动态解析一个符号；`desktop_status.background_pointer` 明示可用性、私有接口依赖和升级风险。接口缺失时拒绝后台指针操作，不降级抢占前台；AX 语义操作和窗口截图不依赖该私有桥。符号存在不等于所有应用都兼容，macOS 升级后需重新验收。

这是共享登录会话中的窗口定向操作，不是虚拟机、独立 WindowServer 或完整隔离桌面。目标应用可以自行弹窗、激活或忽略后台事件；工具无法禁止应用自己的系统行为。菜单、IME、画布、多个显示器、其他 macOS 版本及 Intel 真机的业务级兼容性仍须分别验证。

本轮当前服务、启用开关、已安装 Skill 和系统安全设置保持原样；独立二进制仅供构建核验，没有覆盖运行中的宿主。完整使用说明见 [macos-computer-use.md](macos-computer-use.md)。

---

## 历史记录：首版前台控制

**以下结果属于之前的首版实现，不表示本轮重新执行了对应输入或应用打包验收。**

日期：2026-09-21。基线：`ad51001515a2b1b82baa31281970e0b9f67f28e9`。开发分支：`feat/native-macos-computer-use`。

## 实际环境

- macOS 27.0，build 26A428，Apple Silicon / darwin arm64。
- Go 1.26.5，Command Line Tools SDK 26.5。
- 本机辅助功能、屏幕录制权限已获系统授权；测试没有修改 TCC、自动批准权限或使用 Codex/AppleScript/Python 桌面桥。

## 通过的检查

| 检查 | 结果及范围 |
| --- | --- |
| `go test -race ./... -count=1 -timeout=3m` | 全仓库通过，退出码 0；包括取消/关闭时释放鼠标、快照失效、目标变化、Unicode、输入与输出契约。 |
| `go vet ./...` | 退出码 0。macOS 原生边界存在下述 SDK deprecation 告警，未将其隐藏或当成运行失败。 |
| `TestNativeInputIsolatedFixture` | **真实执行通过**：激活测试应用、坐标点击、AXPress、中文及 emoji 输入、Command+A 后替换、鼠标移动、滚动、拖拽、抬起。只操作本次创建的隔离窗口，结束后清理进程并尝试恢复原前台应用。 |
| `TestNativeReadOnlyDesktop` | **真实执行通过，没有跳过**：系统权限查询、显示器/窗口/应用状态、JPEG 解码及有界 Accessibility 树。 |
| `TestNativeDesktopMCPImage` | **真实执行通过，没有跳过**：原生截图经过 Runtime 和 MCP SDK 会话返回有效 JPEG，以及文本/结构化坐标元数据。 |
| `scripts/test/test-macos-app.sh` | 通过：中英文本地化、配置默认关闭/持久化启用、原有配置测试、Swift 应用编译、原生 CGO Core 打包、签名验证、DMG/ZIP 测试。测试产物使用 ad-hoc 签名，不等于 Developer ID 公证发布。 |
| macOS arm64 + amd64 | CGO 原生构建通过；实际桌面输入运行验收在 arm64 主机完成，未声称在 Intel 硬件运行过。 |
| `CGO_ENABLED=0` | desktop/app/mcp 测试通过，缺少原生后端明确报告 unsupported。 |
| Linux amd64、Windows amd64 | 交叉构建通过，桌面能力返回不支持；并未在这些系统实现桌面控制。 |
| `git diff --check` | 通过。 |

全量测试和构建通过 tmux detached session 执行并检查日志及退出码。日志保存在本次本地克隆的 `.git/computer-use/`，不包含在 Git 提交内。最终串行验收顺序为隔离窗口输入 → 只读观察 → MCP 图像传输 → 全量 race → vet → 本机原生构建，最终退出码为 0。

## 验收中发现并修复的问题

1. Objective-C 数值装箱使部分布尔值变成 JSON 数字；改为显式 CFBoolean/NSNumber 布尔对象，并通过真实原生解码验收。
2. 无 AppKit 主循环的 Go 后台进程可能读取到 NSWorkspace 缓存的旧前台应用；改为实时 AX 焦点查询，并使用系统实时进程查询处理焦点过渡与应用枚举。
3. GUI 测试窗口启动、关闭和前台恢复有短暂过渡期。只读快照可在目标变化时重新观察；**不重放可能已经发送的输入动作**。
4. 原发布流程和 Mac 应用打包测试将 Core 编译为 `CGO_ENABLED=0`；已同步改为 macOS 原生构建，其他平台保留非 CGO 路径。
5. MCP 工具数量和 Mac 设置布局的原契约测试需要随新增功能同步更新，已修正并完成全仓库回归。

## 未覆盖及已知边界

- 没有实际多显示器硬件、Intel Mac、macOS 14/15/26 系统矩阵验收；多显示器负坐标和像素换算目前由单元测试覆盖。
- 没有逐一适配所有第三方应用。AX 不完整的画布需视觉坐标操作；Secure Input、锁屏/非交互会话不在可绕过范围。
- GUI 进程枚举和焦点回退使用 Apple 已标记 deprecated 的 Process Manager API，以避免依赖后台进程中的 NSWorkspace KVO 缓存。后续 SDK 升级需回归或替换该原生边界。
- `event_dispatched` 只是事件发送状态，不是保存、支付或提交成功。业务结果必须由客户端重新观察确认；屏幕内容不能授权后果性操作。
- 输入序列只在一个 AgentDock 进程内串行化，未提供跨进程锁或独占人机桌面会话。
- 未改动或重启正在服务当前连接的已安装 AgentDock；没有自动安装新二进制、发布正式 Release、覆盖旧 Skill 或修改当前主机的启用开关。

完整使用方法和错误恢复见 [macos-computer-use.md](macos-computer-use.md)。
