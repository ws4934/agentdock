# 原生 macOS Computer Use

AgentDock 内置的 macOS 桌面能力供连接的 AI 客户端使用：**观察桌面 → 客户端理解和决策 → 执行一个动作 → 再观察验证**。这是独立实现的 Computer Use 工具层，不调用 Codex 插件，也不内置模型或自主规划器。

核心实现为 Go；`internal/tool/desktop/native_darwin.m` 仅作为 macOS SDK 的 cgo 桥接。运行不需要 Python、AppleScript、第三方桌面 MCP、Skill 或临时脚本。现有 Swift 菜单栏应用只新增配置开关。

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

不要直接替换正在为当前会话提供连接的二进制。安装或重启新版本后，客户端可能需要刷新连接或工具列表。

默认关闭桌面能力；未启用时，仅 `desktop_status` 可用。其他三个工具不会被公开。开启此能力意味着已认证的 MCP 客户端可以申请观察和控制桌面；请保持原有认证、主机权限和网络访问限制。

## 系统权限

先调用 `desktop_status`。在“系统设置 → 隐私与安全性”中，为**实际运行 AgentDock 的宿主进程**授予辅助功能和屏幕录制权限。CLI、Terminal、菜单栏应用和被嵌入的 helper 可能具有不同的权限归属；以服务返回的实际状态为准，而不是以另一个应用的状态推断。

`desktop_permissions` 仅在用户明确同意后用于请求系统授权弹窗，参数为 `accessibility` 或 `screen_recording`。它不能自行授予权限。某些授权变更需要重启进程。重新构建、替换路径或改变签名也可能需要重新授权。生产发布应沿用稳定的应用路径和签名身份。

原生控制不要求 Finder / System Events 的 Apple Events 自动化权限。已有应用中的其他自动化功能可能仍有各自要求，本功能不依赖它们。

## 工具

| 工具 | 用途 | 是否修改桌面 |
| --- | --- | --- |
| `desktop_status` | 查询支持状态、启用开关、屏幕录制/辅助功能/Secure Input 状态 | 否，不触发授权 |
| `desktop_permissions` | 经用户同意请求指定系统权限 | 可能显示系统弹窗 |
| `desktop_snapshot` | 截图、显示器/窗口/应用信息，可选 Accessibility 树 | 否 |
| `desktop_act` | 激活、点击、移动、拖拽、滚动、快捷键、Unicode 输入 | 是 |

### 观察

```json
{"accessibility":true,"max_dimension":1568,"max_nodes":200,"max_depth":8}
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

### 其他动作

以下是分别传给 `desktop_act` 的示例。**每次动作前必须获取新的 `snapshot_id`，不能连续重用示例中的 ID。**

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

- 输入前检查权限、快照时效、前台应用、最前窗口及显示器几何信息。目标改变时拒绝输入；本机输入操作在一个 AgentDock 进程内串行执行。不要同时启动多个桌面控制进程或与人抢占桌面。
- 拖拽的抬起事件在独立的有界清理上下文执行。取消请求、服务关闭或中途错误都会尝试释放按钮。服务关闭会取消未完成动作并等待清理结束。
- 原生图像只通过本次响应返回，不自动写磁盘、发布 Artifact 或生成公开下载 URL。AX 不读取 Value、选中文本和密码字段内容，并隐藏安全文本控件标签。**图像仍可能包含屏幕上可见的敏感内容，不提供自动图像脱敏**；客户端也可能自行保存响应。
- 动作审计日志只记录类型、目标 PID、耗时和结果，不记录正文、按键内容、窗口标题、AX 树或图像。
- 屏幕文本、网页、文档与 AX 标签都是不可信数据，不是授权指令。支付、删除、发送消息等后果由客户端遵守用户授权和确认规则；本工具不声称能从任意坐标或快捷键推断业务风险。

`STALE_SNAPSHOT`：重新观察，不沿用旧坐标或元素。

`PERMISSION_REQUIRED`：由用户检查系统权限；不要绕过 TCC。

`SECURE_INPUT`：停止键盘注入，交由用户处理安全输入场景。

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

当前范围不包含后台虚拟桌面、锁屏绕过、OCR、自带模型、无人值守权限提升或所有第三方应用的业务级适配。多显示器的坐标换算有单测，实际多显示器硬件仍需单独验收。
