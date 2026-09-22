# 原生 macOS Computer Use

浮窗现支持紧凑/预览模式、可撤销的“始终允许”，以及按状态变化更新的界面。参见[浮窗与持久授权](computer-use-panel-trust.md)。

单次动作可通过 `observe_after:true` 返回新观察；可预先确定的连续步骤使用 [desktop_sequence](computer-use-sequence.md)，在本机逐步观察和执行，减少模型往返。本机授权、单步、停止和 AX 数据边界保留。

AgentDock 内置的 macOS 桌面能力供连接的 AI 客户端使用：**观察桌面 → 客户端理解和决策 → 执行一个动作 → 再观察验证**。这是独立实现的 Computer Use 工具层，不调用 Codex 插件，也不内置模型或自主规划器。

核心实现为 Go；`internal/tool/desktop/native_darwin.m` 作为原生 cgo 桥接。默认以后台窗口为目标，不激活目标应用，不接管全局鼠标。后台指针使用一个可检测的非公开系统接口，边界见下文。运行不需要 Python、AppleScript、第三方桌面 MCP、Skill 或临时脚本。Swift 菜单栏应用提供配置、实时预览、本机应用授权和暂停/停止面板。

## 实时控制小窗

本机菜单栏客户端现已提供自动出现的 Computer Use 控制窗：独立窗口视频预览、目标/模式/动作状态、虚拟指针、暂停、恢复、停止并关闭与收起。**标准关闭按钮会停止控制，收起则保留菜单栏停止入口。** 核心确认清理完成前显示“正在停止”，不会先隐藏后继续输入。

启用桌面功能的正式宿主要求同版本本机控制窗在线；没有监视器时返回 `DESKTOP_MONITOR_UNAVAILABLE`。暂停或停止后，目标截图、启动与输入工具不能重新开启控制，只有本机恢复才允许新的快照。预览不消耗模型的 `snapshot_id`。完整交互、会话/断连规则、开发部署和验收见 [computer-use-monitor.md](computer-use-monitor.md)。

## 启用

需要 **macOS 14 或更新版本**、已登录且可交互的桌面，以及启用 CGO 的 AgentDock 构建。Linux、Windows、无 CGO 构建保留明确的不支持状态，不模拟成功。AgentDock 本体的 macOS 13 支持不变，但该系统不能使用此桌面功能。

菜单栏应用：在高级设置中勾选“启用原生 macOS 桌面控制”，然后应用并重启。设置持久保存到：

```text
~/Library/Application Support/AgentDock/agentdock.env
```

直接运行 Go 服务时，设置主机进程环境变量：

```sh
CGO_ENABLED=1 go build -trimpath -o bin/agentdock ./cmd/agentdock
AGENTDOCK_DESKTOP_ENABLED=true ./bin/agentdock --stdio
```

独立 CLI 示例也要求本机监视器：未指定 `AGENTDOCK_RUNTIME_ROOT` 时 socket 位于 `AGENTDOCK_HOME/desktop-runtime`，开发菜单栏进程需用 `AGENTDOCK_MONITOR_RUNTIME_ROOT` 指向同一目录。正式安装路径无需额外设置。不能只更新 Core 而沿用没有控制窗的旧菜单栏应用。

不要直接替换正在为当前会话提供连接的二进制。安装或重启新版本后，客户端可能需要刷新连接或工具列表。

默认关闭桌面能力；未启用时，仅 `desktop_status` 可用。其他桌面工具不会被公开。开启此能力意味着已认证的 MCP 客户端可以申请观察和控制桌面；请保持原有认证、主机权限和网络访问限制。

## 系统权限

系统开关已打开但当前进程仍不可用时，先核对[签名身份与权限恢复说明](macos-permission-recovery.md)。新版权限页自动刷新，并分别显示菜单栏应用和实际 Core 的 API 结果；不会依据开关或另一个进程的权限显示成功。

先调用 `desktop_status`。在“系统设置 → 隐私与安全性”中，为**实际运行 AgentDock 的宿主进程**授予辅助功能和屏幕录制权限。CLI、Terminal、菜单栏应用和被嵌入的 helper 可能具有不同的权限归属；以服务返回的实际状态为准，而不是以另一个应用的状态推断。

`desktop_permissions` 仅在用户明确同意后用于请求系统授权弹窗，参数为 `accessibility` 或 `screen_recording`。它不能自行授予权限。某些授权变更需要重启进程。重新构建、替换路径或改变签名也可能需要重新授权。生产发布应沿用稳定的应用路径和签名身份。

原生控制不要求 Finder / System Events 的 Apple Events 自动化权限。已有应用中的其他自动化功能可能仍有各自要求，本功能不依赖它们。

## 工具

| 工具 | 用途 | 是否修改桌面 |
| --- | --- | --- |
| `desktop_task` | 声明、检查和结束当前 Core 的独占任务，返回私有 task_id | 任务控制状态 |
| `desktop_wait` | 等待窗口或 AX 条件，不发送输入、不生成动作快照 | 否 |
| `desktop_sequence` | 在一个后台窗口内连续点击、输入、按键、滚动或拖拽，按需读取 AX，结束后返回观察 | 是 |
| `desktop_status` | 查询支持状态、启用开关、屏幕录制/辅助功能/Secure Input 状态 | 否，不触发授权 |
| `desktop_permissions` | 经用户同意请求指定系统权限 | 可能显示系统弹窗 |
| `desktop_launch` | 按应用名、Bundle ID 或绝对 .app 路径启动/复用应用，默认后台；有界等待窗口 | 是，应用/系统可能显示界面 |
| `desktop_snapshot` | 默认只列出窗口；选择窗口后截图和可选 AX 树；显式前台观察 | 否 |
| `desktop_act` | 对快照绑定目标点击、移动、拖拽、滚动、按键、输入或 AX 全量文本替换；激活仅限显式前台模式 | 是 |

## 独占任务和应用授权

正式宿主现在要求先向 `desktop_task` 传入 `{"action":"begin","title":"本次桌面任务的目的"}`，保存返回的私有 `task_id`。**下文简写的应用/快照/动作示例均须额外携带本任务的 `task_id`；只有状态查询和无目标元数据发现例外。** 新应用或切换前台模式必须由本机浮窗批准，不能由模型自批，也不能以系统辅助功能权限代替本次任务授权。

任务完成且没有活动操作后，调用 `desktop_task` 的 `end` 并带回令牌。结束任务不能清除本机停止或清理失败状态；失去令牌时由本机用户停止并释放任务。`desktop_wait` 支持窗口存在/不可见/稳定、控件存在/缺失/启用/禁用，超时和截断不等于成功，不能据此自动重放输入。包含完整 task_id 的调用示例、权限范围和单步语义见 [computer-use-reliability.md](computer-use-reliability.md)。

## 主动打开应用再操作

新增 `desktop_launch`，不再要求用户先手动打开应用。它独立于 `desktop_act`，因为尚未启动的应用没有可绑定的窗口；`desktop_act.action:activate` 仍只用于显式切换已经运行的应用。

下面三种选择器**只传一个**，示例应用必须已安装：

```json
{"app_name":"Safari"}
{"bundle_id":"com.apple.Safari","mode":"background"}
{"app_path":"/System/Applications/TextEdit.app","wait_ms":5000}
```

`app_name` 精确匹配应用包文件名（不含或包含 `.app` 均可，大小写不敏感），仅检查 `/Applications`、`~/Applications`、`/System/Applications`、`/System/Library/CoreServices/Applications` 及其三层以内普通子目录；不进入 `.app` 包内部，不做模糊匹配。找到多个同名应用时返回候选路径，必须明确选择。非标准目录或本地化名称不能解析时，使用准确 `bundle_id` 或 `app_path`。

`bundle_id` 通过系统 LaunchServices 解析；系统尚未索引但已有运行实例时，使用该实例的真实 Bundle URL。`.app` 路径必须是本地绝对路径，原生层检查应用包类型、Bundle ID 和可执行入口。仅打开应用，不接受 shell 命令、任意启动参数、环境变量、文档或 URL，也不会下载应用、移除 quarantine 或绕过 Gatekeeper/TCC。

默认 `mode:background`，向系统请求不激活目标、不隐藏其他应用。应用已经运行时复用完全相同路径的实例，不重新发送 reopen 事件，不无意新建文档。多个同路径实例会报歧义，改用观察工具选定 PID/窗口。只有用户明确同意切换前台时才使用：

```json
{"app_name":"Safari","mode":"foreground"}
```

启动返回 `application.pid`、`app_path`、`already_running`、`launch_requested`、`activation_requested`、`activation_observed` 和 `windows`。原生启动调用最多等待 8 秒；`wait_ms` 是启动返回后额外等待窗口的预算，默认 10000、最大 30000 毫秒，0 表示只观察一次。正预算下要求两次窗口元数据稳定；这不保证窗口内部所有控件/动画都已就绪。

`window_ready:false`、`wait_timed_out:true` 可能只是应用没有普通窗口、启动缓慢或窗口尚未稳定，**不是重新启动应用的指令**。窗口等待失败仍保留已取得的 PID；取消、系统弹窗或启动超时后可能有晚到的进程，先观察再决定，不自动重发启动请求。

完整调用顺序：

```text
desktop_launch(app_name / bundle_id / app_path)
    → 从返回的 application.pid、windows 选择目标
    → desktop_snapshot(window_id, pid, accessibility:true)
    → desktop_act(snapshot_id, ...)
    → desktop_snapshot(...) 再观察验证
```

启动结果**没有 `snapshot_id`**，不能直接拿它授权输入；每次启动尝试也会使旧快照失效。多个窗口不能盲选。前台启动后需显式使用前台快照操作，不能借后台路径竞争用户正在使用的应用。

`activates:false` 只限制 AgentDock 向系统发出的启动请求，不能禁止应用自身或 Gatekeeper 显示界面。`background_interference:true` 表示原本不在前台的目标变成了前台；工具会报告而不是抢回焦点或继续隐式输入。应用若没有窗口，工具不会为制造窗口而发送快捷键或打开用户文件。

原生启动使用公开 `NSWorkspace.openApplication` / `OpenConfiguration` API；与后台指针的可选非公开坐标桥无关。参考 Apple 文档：[NSWorkspace](https://developer.apple.com/documentation/appkit/nsworkspace)、[OpenConfiguration](https://developer.apple.com/documentation/appkit/nsworkspace/openconfiguration)。

## 后台窗口优先的工作流

`desktop_snapshot` 的默认模式是 `background`。先无参数调用，返回 `state.windows`、PID、显示器等元数据；此时 `observation_only:true`，不截图、不读 AX，而且**不能拿发现快照去发送输入**。不要把默认模式误认为自动操作当前前台窗口。

从实际返回的窗口中选择目标，再调用（示例 ID 必须替换成实际值）：

```json
{"window_id":123,"pid":456,"accessibility":true,"max_dimension":1568}
```

`pid` 可省略；提供时用于核对窗口所有者。默认返回目标窗口 JPEG，`screenshot:false` 可关闭图像。截图使用 ScreenCaptureKit 的独立窗口过滤器，目标被其他窗口覆盖时仍截取目标内容，不截取覆盖它的应用，也不显示真实鼠标。窗口最小化、关闭、不可见空间或系统无法提供目标时会失败；不会擅自恢复、移动或激活它。AX 树只从匹配的窗口开始，不遍历整台桌面。

每个动作前重新获取窗口快照，使用新 `snapshot_id`。动作继承快照的模式与目标，调用者不能在 `desktop_act` 中偷偷切换 PID、窗口或前后台模式。背景模式会核对窗口 ID、PID、几何和显示器；目标应用成为用户前台应用时返回 `TARGET_IN_USE`，让用户优先操作。

```json
{"action":"click","snapshot_id":"<最新ID>","element_id":"e4"}
{"action":"click","snapshot_id":"<最新ID>","space":"image","point":{"x":240,"y":180}}
{"action":"set_value","snapshot_id":"<最新ID>","element_id":"e7","text":"后台填写文本"}
{"action":"set_value","snapshot_id":"<最新ID>","element_id":"e7","text":""}
{"action":"type","snapshot_id":"<最新ID>","text":"你好 🚀"}
{"action":"key","snapshot_id":"<最新ID>","key":"a","modifiers":["command"]}
{"action":"scroll","snapshot_id":"<最新ID>","space":"image","point":{"x":200,"y":180},"delta_y":-200}
```

`set_value` 是**替换完整文本值**（空字符串表示清空），不是模拟逐字输入；要求元素 `value_settable:true`，仅允许非安全文本框/文本区域。应用可能不会为 AX 赋值发送与打字相同的业务通知，因此仍须观察验证。它不读取原值或剪贴板。

`type`/`key` 要求绑定窗口与该应用自己的 AX focused window 一致，否则拒绝，避免输入跑到同一应用的另一窗口。Command 快捷键优先在有界的只读 AX 菜单遍历中匹配键码/字符和修饰键，唯一匹配且已启用时执行一次 AXPress；菜单被应用禁用、发现歧义或遍历不完整即拒绝。后台 AppKit 可能禁用依赖 key window 的命令（例如全选），不能把它当作可用快捷键或擅自激活应用。没有菜单匹配时才使用定向键盘事件，不会在菜单操作失败后追加一次按键。文字输入和非菜单按键直接投递给目标进程。不会为通过检查而修改系统焦点。不同应用对后台快捷键、IME、菜单和画布的支持可能不同；返回 `event_dispatched` 并不能证明控件采纳了事件。

后台坐标只允许落在绑定窗口内。`space:image` 是该窗口图片像素，按 `image.screen_bounds` 换算。滚动必须带 `point`，绝不读取真实鼠标位置作为滚动目标；`move` 只向应用发送定向移动事件，不挪动全局指针。拖拽结束和取消清理仍绑定原 PID/窗口。

### 原生实现与兼容边界

后台 AXPress/AXSetValue 和窗口截图使用公开 API。定向鼠标使用 `NSEvent` 绑定窗口编号，通过 `CGEventPostToPid` 直接投递，不走全局 HID/session 输入流。macOS 对远端窗口事件的坐标需要非公开的 `CGEventSetWindowLocation`：本实现将动态符号查询限制为单一桥接入口，不注入其他进程、不修改系统权限，也不使用私有激活协议。

`desktop_status.background_pointer` 返回 `available`、`requires_private_api:true`、符号名称和稳定性说明。符号缺失时后台指针明确失败，AX 语义操作仍可用，**绝不回退到全局点击或激活应用**。符号存在只代表接口可调用，并非第三方应用兼容保证；每次 macOS 升级、签名或原生构建变化都应重新运行双应用验收。这项能力不是第二个 WindowServer、虚拟桌面或虚拟机。

第三方控件可能拒绝后台首次点击（AppKit 的 `acceptsFirstMouse:`），或依赖真实系统按键/鼠标状态；不能声称任意画布、游戏和 IME 都兼容。实现不会偷偷附加 Command 修饰键改变点击语义。验收中的自定义画布明确声明接受后台点击，用来验证拖拽投递，而不是证明所有画布都支持。

后台模式不能禁止目标应用自己弹窗、激活、显示菜单或在业务逻辑中影响系统。检测到它变为前台后会报告干扰并停止，而不是抢回原焦点。浏览器任务优先使用已有隔离浏览器会话；需要完整环境隔离的通用桌面任务应使用独立桌面环境。

### 前台观察（显式接管）

```json
{"mode":"foreground","accessibility":true,"max_dimension":1568,"max_nodes":200,"max_depth":8}
```

将上述参数传给 `desktop_snapshot`。截图默认开启，直接返回标准 MCP JPEG image content，并附带文本和结构化坐标元数据；无需再调用 `view_image`。不需要图像时设置 `screenshot:false`。AX 树默认关闭，需要时显式开启。

截图最大边默认 1568、可选 128–2048 像素；节点上限默认 200、最多 500；深度默认 8、最多 16。AX 遍历同时受时间预算及每次消息超时限制，触及预算会返回 `tree_truncated:true`。CaptureKit 请求也有有界超时。

结果中的 `snapshot_id` 是随机、单次使用的标识，30 秒过期。新的快照替换旧快照。每次尝试发送动作都会消费快照，即使操作中途失败也不能重放。

### 坐标与控件点击

屏幕坐标使用 **全局逻辑点**：主显示器左上角为原点，其他显示器可能有负坐标。图像坐标则使用该快照返回的 JPEG 像素，不要把 Retina 的物理像素、逻辑点和缩略图像素混用。

```json
{
  "action":"click",
  "snapshot_id":"<刚取得的32位十六进制ID>",
  "space":"image",
  "point":{"x":240,"y":180}
}
```

对于指定显示器，内部转换为：

```text
screen_x = screen_bounds.x + image_x * screen_bounds.width / image.width
screen_y = screen_bounds.y + image_y * screen_bounds.height / image.height
```

`space:"screen"` 直接使用逻辑点。坐标必须位于已观察到的显示器范围内。

AX 中可点击的控件优先按 `element_id` 操作：

```json
{"action":"click","snapshot_id":"<最新ID>","element_id":"e12"}
```

元素 ID 只在对应快照中有效。Go 层不公开原生 AX 地址；原生调用前会重新解析并核验元素角色、标题、描述、位置、启用状态和 AXPress 能力。不支持 AX 的画布可以使用截图坐标操作。元素点击不接受鼠标按钮、修饰键或多击参数。

### 前台模式的其他动作

以下示例必须使用 `mode:foreground` 获取的快照。该模式会使用真实鼠标和前台焦点，仅在用户明确同意接管时使用；后台失败不构成切换授权。以下是分别传给 `desktop_act` 的示例。**每次动作前必须获取新的 `snapshot_id`，不能连续重用示例中的 ID。**

```json
{"action":"activate","snapshot_id":"<最新ID>","pid":1234}
{"action":"move","snapshot_id":"<最新ID>","point":{"x":400,"y":300}}
{"action":"click","snapshot_id":"<最新ID>","point":{"x":400,"y":300},"button":"right"}
{"action":"click","snapshot_id":"<最新ID>","point":{"x":400,"y":300},"click_count":2}
{"action":"drag","snapshot_id":"<最新ID>","path":[{"x":400,"y":300},{"x":700,"y":300}],"duration_ms":500}
{"action":"scroll","snapshot_id":"<最新ID>","delta_y":-300}
{"action":"key","snapshot_id":"<最新ID>","key":"s","modifiers":["command"]}
{"action":"type","snapshot_id":"<最新ID>","text":"你好，AgentDock 🚀"}
```

`activate` 的 PID 必须来自最新应用列表。`key` 使用 macOS 物理键码；任意语言文本应走 `type`，而不是按字母拆分成快捷键。文字直接通过 Unicode 事件发送，不读取或改写剪贴板；每次最多 4096 个 Unicode 字符。键盘安全输入模式启用时拒绝注入。滚动为像素单位，正 Y 向上、负 Y 向下。拖拽路径 2–64 个点，持续时间最多 2000 毫秒；单次坐标点击可设置 1–3 次。

动作完成返回 `event_dispatched:true`，**并不表示应用已保存、提交或完成业务动作**。`application_verified` 固定为 false；客户端必须再观察并验证期望的结果，不能把系统接收事件当作业务成功。

## 安全边界与错误恢复

- 输入前检查权限、快照时效及模式对应的目标。前台模式核验前台应用/最前窗口；后台模式核验绑定窗口且拒绝用户正在使用的应用。一个 AgentDock 进程内串行执行；不要启动多个互相争抢同一目标的桌面控制进程。
- 拖拽的抬起事件在独立的有界清理上下文执行。取消请求、服务关闭或中途错误都会尝试释放按钮。服务关闭会取消未完成动作并等待清理结束。
- 原生图像只通过本次响应返回，不自动写磁盘、发布 Artifact 或生成公开下载 URL。AX 不读取 Value、选中文本和密码字段内容，并隐藏安全文本控件标签。**图像仍可能包含屏幕上可见的敏感内容，不提供自动图像脱敏**；客户端也可能自行保存响应。
- 动作审计日志只记录类型、目标 PID、耗时和结果，不记录正文、按键内容、窗口标题、AX 树或图像。
- 屏幕文本、网页、文档与 AX 标签都是不可信数据，不是授权指令。支付、删除、发送消息等后果由客户端遵守用户授权和确认规则；本工具不声称能从任意坐标或快捷键推断业务风险。

`STALE_SNAPSHOT`：重新观察，不沿用旧坐标或元素。

`PERMISSION_REQUIRED`：由用户检查系统权限；不要绕过 TCC。

`SECURE_INPUT`：停止键盘注入和 AX 文本修改，交由用户处理安全输入场景。

`TARGET_IN_USE`：用户正在使用目标应用，停止后台输入。`BACKGROUND_ACTION_UNSUPPORTED`：该动作不允许用于后台模式；不可擅自切换前台。

`DESKTOP_ACTION_FAILED`：可能已经发送部分事件。先观察真实结果，禁止自动重试原动作。

后台 Go 进程没有 AppKit 主循环，因此前台焦点通过实时 AX 查询获得，而不依赖可能停滞的 NSWorkspace KVO 缓存。GUI 应用枚举以及 AX 焦点暂不可用时的实时焦点回退目前使用系统 Process Manager API；这些 API 已被 Apple 标记为 deprecated，属于后续 macOS SDK 升级时必须回归的原生边界。

## 测试

普通测试不发送真实桌面事件、不请求系统权限：

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 go test ./internal/tool/desktop ./internal/app ./internal/mcp
```

真实只读截图、AX 和 MCP 图像传输：

```sh
go test -tags desktop_integration ./internal/tool/desktop ./internal/mcp \
  -run 'TestNative(ReadOnlyDesktop|DesktopMCPImage)$' -v -count=1
```

显式的输入验收会临时打开独立测试窗口，只对其进行输入，然后关闭它并尝试恢复原前台应用。不要同时手动切换窗口或运行其他桌面自动化：

```sh
AGENTDOCK_DESKTOP_INPUT_TEST=1 go test -tags desktop_integration \
  ./internal/tool/desktop -run TestNativeInputIsolatedFixture -v -count=1
```

缺少支持或系统权限时这些集成测试会说明原因并跳过；跳过不等于通过真实桌面验收。预计超过 90 秒的全量测试或构建应放入 tmux detached session，并检查日志和退出码。

双应用后台验收（与前台输入验收串行运行，不要同时切换测试窗口）：

```sh
AGENTDOCK_DESKTOP_INPUT_TEST=1 go test -tags desktop_integration \
  ./internal/tool/desktop -run TestNativeBackgroundTwoApplications -v -count=1
```

该测试创建后台目标和覆盖它的前台哨兵，核查真实目标状态、窗口截图、前台窗口、first responder 及键盘/鼠标泄漏。只读鼠标事件观察器不监听键盘、不修改或拦截事件；外部鼠标移动单独计数，自动化进入全局鼠标流或无外部事件的光标变化会失败。只有显式测试环境变量指定路径时才保存测试图像/日志；正常工具不保存截图。

当前范围不包含后台虚拟桌面、锁屏绕过、OCR、自带模型、无人值守权限提升或所有第三方应用的业务级适配。多显示器的坐标换算有单测，实际多显示器硬件仍需单独验收。

真实应用启动和 MCP 操作验收（只创建并操作临时测试 .app；与其他 GUI 输入测试串行执行）：

```sh
AGENTDOCK_DESKTOP_INPUT_TEST=1 go test -tags desktop_integration \
  ./internal/mcp -run TestNativeDesktopLaunchMCPFlow -v -count=1
```

该测试通过真正的 MCP `desktop_launch` 启动未运行 `.app`，检查窗口 JPEG/AX、文本填写和按钮点击，再验证相同 PID 复用与显式前台激活；不会启动 Safari/TextEdit 或打开真实用户文档。`TestNativeApplicationResolution` 只解析系统 TextEdit 的名称/Bundle ID/路径，不启动它。
