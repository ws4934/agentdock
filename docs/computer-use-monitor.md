# Computer Use 实时控制窗

## 目标与范围

本设计采用“用户可观察、随时暂停/停止、默认不抢占前台”的 Computer Use 交互方式，不声称复制 Codex 的内部实现，也不把关闭按钮的行为归因于未公开的产品细节。基于现有应用启动、窗口定向操作及原生 macOS 菜单栏客户端，新增真实窗口视频预览和核心会话控制。

这是**一个 Core 进程共享的桌面控制会话**，不是按模型 turn 自动识别的任务系统。停止会阻断该进程所有后续桌面输入和目标窗口观察，直到本机用户恢复；终端、浏览器专用工具、模型推理等不在此停止范围内。它不是虚拟桌面或新的系统安全沙箱。会话状态在该 Core 进程内，不是跨进程持久化的任务授权系统。

## 用户体验

第一次启动应用、观察目标窗口或发送动作时，原生小窗自动出现。小窗和菜单栏显示真实的运行/等待/暂停/停止阶段、目标应用与前后台模式。标题和标签只作为数据显示，不执行屏幕内容中的指令。

| 控件或状态 | 行为 |
| --- | --- |
| 实时预览 | 仅选定 PID + window ID 的窗口画面，默认上限 10 帧/秒、最长边 960；不采音频、不录像、不落盘。 |
| 虚拟指针 | 将工具已校验的逻辑点映射到目标窗口缩略图，不移动真实鼠标，也不显示输入正文。 |
| 暂停 | 先撤销当前代次的操作许可，取消排队/进行中的动作，再等待有界清理；期间显示“正在暂停”。 |
| 恢复并重新观察 | 只能从本机控制入口发起；待旧操作清理完毕才允许，生成新会话代次，旧快照失效。不会重放被取消的操作。 |
| 停止并关闭 / 标准关闭按钮 | 撤销输入并清理；只有 Core 返回 `stopped`、活动操作为 0 后才隐藏窗口。不会退出目标应用或整个 AgentDock。 |
| 收起 | 关闭视频流并收起窗口，但保留可见菜单栏状态、心跳及停止入口；不是停止。 |
| 菜单栏 | 可重新显示窗口、暂停/恢复或停止。用户停止后，菜单栏仍保留明确的本机恢复入口。 |
| 等待下一步 | 表示没有正在执行的桌面工具；不伪装成“模型思考”。60 秒无桌面活动后结束普通会话并关闭预览。 |
| 连接异常 | 立即停止本机预览；Core 监视租约过期后暂停。断线重连不会自动解除暂停。停止未确认时界面不显示“已停止”。 |

窗口采用 `NSPanel` + `nonactivatingPanel`，不可成为 key/main window。它显示在当前空间和全屏应用的辅助窗口层，不主动激活 AgentDock 或目标应用。不会强制重新摆放用户拖动的小窗。

## 两条独立画面链路

```text
模型：desktop_snapshot → 一次性 snapshot_id → desktop_act → 再观察

本机用户：选定窗口 → ScreenCaptureKit SCStream → 最新一帧 → NSPanel
```

预览不调用 `desktop_snapshot`，不更新模型快照、不占用输入串行锁，也不逐帧发送给模型。主线程繁忙时仅保留最新一帧和一个待处理回调，避免无界积压。目标变化会停止旧流，异步返回的旧帧按流身份丢弃。暂停、停止、收起、断连时停止预览并清空画面。

只捕获明确绑定的窗口，不因无法找到目标而回退到整屏。前台控制模式也只预览已观察到的目标窗口。应用最小化、静止、权限不足或捕获流失败时，显示相应状态，不将旧画面冒充正在更新。视频失败不影响本地停止按钮。

窗口画面仍可能包含用户敏感内容；没有自动图像脱敏。模型显式调用的截图仍按照原有工具权限返回给客户端。正常控制窗不保存截图；测试专用开关可保存由测试进程创建的窗口图片。

## 核心会话状态与停止语义

`internal/tool/desktop/control_session.go` 是状态事实来源。

```text
idle/completed --首次受控操作--> running --空闲60秒--> completed
running --暂停/监视器断连/请求取消--> pausing --> paused
running/paused --停止--> stopping --> stopped
paused/stopped --本机恢复且活动数为0--> idle（新代次）
任意状态 --Core关闭--> closed（先完成清理）
```

`pausing` / `stopping` 是活动操作未归零时的可见状态；内部立即拒绝新操作。停止不获取可能被拖拽持有的全局输入锁，因而不需要等拖拽自然结束才请求取消。原来的后台拖拽清理始终绑定原 PID/窗口；释放完成前不会返回最终停止状态。

观察快照记录会话 `epoch`。恢复或新的会话都会改变代次；即使旧快照还在 30 秒有效期内，也不能继续操作。停止/暂停后重新调用目标截图、启动或输入工具不能恢复会话；无参数的窗口元数据发现不会解除锁存。授权弹窗请求也受同一停止状态约束。

已提交给 macOS 或应用的事件不可撤销。原生有界调用可能尚未返回，因此取消不是“零延迟回滚”。尤其是已交给系统的应用启动，可能在等待取消后才完成；控制窗不会杀掉用户应用来模拟撤销。

## 本地通信与断连保护

复用 `internal/desktopcontrol` 的本机 Unix socket，目录/套接字采用既有 0700/0600 权限；不新建公网预览服务，不向 MCP 注册恢复控制方法。

本地接口：

- `computeruse.poll`：本机控制器发送随机 `controller_id` 和实际显示的 `visible_session_id`，更新监视心跳并取状态。
- `computeruse.command`：包含当前 `session_id`，操作仅为 `pause`、`stop`、`resume`。拒绝过期会话、其他活动控制器和未知参数。

正式 `agentdock` 宿主启用桌面功能后要求本机控制器在线；首次动作还需等可见小窗/菜单指示器确认。没有监视器时返回 `DESKTOP_MONITOR_UNAVAILABLE`，不会静默执行。每 0.4 秒轮询，心跳超过 3 秒触发核心暂停检查；正在原生调用中的操作按原有有界超时完成清理。返回中的 `control_session` 可通过 `desktop_status` 查询，但不含本地控制器标识、输入正文或 AX 路径。

同一 Core 只接受一个活动本机控制器租约。协议限制属于该服务的控制边界，不能被当作对拥有同一主机用户权限的任意进程的防护；现有 shell 工具权限和系统授权仍需独立管理。

## 部署

必须同时更新 Core 与菜单栏应用。正常安装的 Core 使用 `AGENTDOCK_RUNTIME_ROOT` 下的 `control.sock`，菜单栏客户端使用其 `Application Support/AgentDock` 目录。

独立开发服务（包括 `--stdio`）未设置 runtime root 时，使用 `AGENTDOCK_HOME/desktop-runtime/control.sock`。开发者启动相应菜单栏构建时，可用 `AGENTDOCK_MONITOR_RUNTIME_ROOT` 指向同一个目录。路径需满足 macOS Unix socket 长度限制。启动时会拒绝夺取仍在使用的 socket；只回收确认失效的端点。不要让两个 Core 共用一个 socket，也不要替换正在提供当前连接的二进制。

这是有意的安全变化：只有旧 Core 或旧菜单栏应用时不能得到完整监视握手。桌面功能仍默认关闭；未启用桌面能力的普通 CLI/HTTP 工作流不要求控制窗。嵌入式 Runtime/后端单测不自动安装 UI，使用单独的显式监督测试覆盖正式入口行为。

本轮不绕过锁屏、Secure Input、屏幕录制或辅助功能权限，不自动请求或授予预览权限；菜单栏应用的实际权限归属需由用户核验。退出菜单栏应用会尝试发送停止，异常退出则由 Core 租约检测兜底。

## 验收

普通单测覆盖：先显示再执行、暂停/停止锁存、恢复使旧快照失效、拖拽清理前不确认停止、断连取消、错误/旧会话/外来控制器拒绝、本地控制不暴露为 MCP、监视状态不泄露输入文本、空闲结束不解除用户停止。

显式原生端到端验收：

```sh
AGENTDOCK_DESKTOP_INPUT_TEST=1 go test -tags desktop_integration \
  ./internal/tool/desktop -run '^TestNativeComputerUseMonitor$' -count=1 -v -timeout=120s
```

测试编译和运行专用 Swift 面板进程、后台动画目标和前台哨兵，使用真实 ScreenCaptureKit、Unix socket 及 Go 核心。验证至少三帧实时预览、不改变模型快照、非前台/key/main 窗口、收起后保留监视心跳、暂停恢复、真实关闭按钮取消拖拽并释放、面板进程死亡后的暂停锁存。只读鼠标来源观察器区分硬件移动与合成事件，不监听键盘，也不拦截输入。测试不会启动安装中的 AgentDock 或操作真实用户文档。

超过 90 秒的完整回归、构建、打包使用 tmux detached，检查日志和退出码。没有系统权限而跳过的测试不算真实验收通过。当前验收结果见 [macos-computer-use-verification.md](macos-computer-use-verification.md)。

## 公开资料参考

- [OpenAI Computer Use 使用与停止/接管说明](https://developers.openai.com/codex/app/computer-use)
- [Apple nonactivatingPanel](https://developer.apple.com/documentation/appkit/nswindow/stylemask-swift.struct/nonactivatingpanel)
- [Apple ScreenCaptureKit 捕获示例](https://developer.apple.com/documentation/screencapturekit/capturing-screen-content-in-macos)

实时预览和面板不增加私有 API。现有后台指针的 `CGEventSetWindowLocation` 兼容边界仍独立存在，需要跟随 macOS 升级回归。
