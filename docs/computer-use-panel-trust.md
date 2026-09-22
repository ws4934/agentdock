# Computer Use：浮窗与持久授权

## 使用

浮窗采用紧凑卡片：应用图标和名称、模式、任务/状态、进度以及暂停/单步/停止。箭头展开或收起预览，预览选择跨启动保留；减号收起到菜单栏，同一运行期间的新任务不会强制重新展开。首次未信任应用仍可显示审批卡片；信任后不再为每个任务点击允许。

“本次允许”只对本任务有效；“始终允许”跨任务和 Core 重启有效。菜单“已信任的应用”中的撤销入口会删除对应许可，并先暂停当前控制代次，避免正在执行的长序列继续使用旧许可。保存失败显示“更改未生效”，不假装许可已保存；用户停止后的恢复仍由本机操作。

## 持久策略

策略保存于 `AGENTDOCK_HOME/desktop/trusted-applications.json`，普通 macOS 安装对应 `~/.agentdock/desktop/trusted-applications.json`。绑定 **Bundle ID、应用路径、前台/后台模式**；不持久保存 PID、任务能力令牌、输入文本或画面。系统原生枚举/启动解析提供目标身份，应用路径或模式改变不继承其他目标的许可。只有临时 PID、无法识别应用包的目标仍使用本次许可。

文件采用私有权限和原子替换，启动时读取，执行时进行内存查找。本机控制协议新增 `approve_application_always` 和 `revoke_application`；这两个写入入口不注册为 MCP 工具，也不接受通过普通心跳代替审批。完整列表只提供给本机面板，不加入公开桌面状态。

配置文件损坏、权限过宽、符号链接或格式不正确时，不载入其许可，退回本次审批并记录诊断。这个设置不是 macOS 系统授权开关：不改 TCC，不扩大 Screen Recording/Accessibility 权限，不允许自动操作 AgentDock 自己的控制面板，也不替代用户对具体业务动作的确认。

策略由当前 Core 管理，不是跨账号/跨 OS 用户的授权系统，也不提供多 Core 进程间的实时策略同步。拥有本机同一 OS 用户权限、可运行任意终端命令的进程不在此授权层的隔离保证内。

## 性能改动

相同状态心跳不重建界面；菜单只在打开时刷新，应用图标只在目标改变时加载，状态栏使用固定图标避免频繁伸缩。预览的指针、边界和提示文字在值变化后才重绘。窗口改名和仅移动位置不再重建捕获流；窗口身份或尺寸变化仍会更新。

审批结果通过事件唤醒等待者，替代每 100 毫秒轮询。持久许可列表在变化时排序、缓存，正常点击不读写策略文件。预览收起时停止视频捕获；展开后仍沿用原有的帧率和有界队列，不通过无限堆积帧来换取流畅感。

系统参考：[ScreenCaptureKit minimumFrameInterval](https://developer.apple.com/documentation/screencapturekit/scstreamconfiguration/minimumframeinterval)、[queueDepth](https://developer.apple.com/documentation/screencapturekit/scstreamconfiguration/queuedepth)。

## 验证

`application_trust_test.go` 覆盖跨任务/重启、PID 改变、路径/模式隔离、撤销进行中的序列、单次许可不持久化、伪造请求、存储损坏和写入失败。`ComputerUsePanelTests.swift` 使用合成状态检查明暗、展开/收起、审批、长标签和重复心跳；可通过 `AGENTDOCK_PANEL_TEST_OUTPUT_DIR` 显式保存浮窗自身的布局截图，不截取用户桌面。

打包门禁同时执行真实监视器/传输代码的隔离 socket 故障测试，检查停止消息丢失和菜单跟踪期间的心跳。合成状态截图不等于真实计算器的操作或性能验收。
