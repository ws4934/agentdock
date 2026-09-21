# macOS Computer Use 验收记录

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
