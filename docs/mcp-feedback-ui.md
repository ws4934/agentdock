# MCP Apps 反馈组件 v2

## 展示与执行分离

`internal/mcpapps` 提供九种独立、内容寻址的反馈资源：能力概览、任务进度、文件变更、外部 MCP、文件下载、记忆、工作流、编程会话和工作结果。保留资源不等于每次调用都挂载它。

自动 UI 绑定保留 `task_manage`、`work_result_freeze`、`work_result_show`、`file_publish` 和 `diagnostic_export`，并恢复 `file_edit`、`agentdock_context`、`mcp_tool_call`、`acp_session`、`workflow_template_manage`、`recall_write` 的已有操作卡片。文件变更不再依赖任务汇总或额外 show；新增、替换、补丁、移动、删除均随原调用携带反馈，预演标记尚未写入，无变更不显示成功色，错误由组件错误态展示。可选集成只有启用后才暴露相应工具与资源。

`task_update`、`task_read`、`work_result_read`、文件读取/搜索及执行状态轮询保持数据入口，不新增卡片。描述符、目录导出和调用结果使用同一份绑定表；不只删结果元数据，也不靠模型自觉补一次展示。卡片保留有界预览、差量更新和销毁清理；跨工具调用的多个 iframe 仍由宿主管理，不保证恢复更多操作卡片后总内存不增加。

`task_manage` 只负责 create/block/resume/complete；`task_update` 负责 checkpoint/final_review；`task_read` 提供 list/get/snapshot。三个公开请求与契约分别定义，复用同一个任务领域服务，没有额外任务数据库或旧动作兼容转发。高频更新不挂载 iframe。最终 `work_result_freeze` 保存并展示交付；`work_result_show` 用于明确重新打开。工作结果卡的完整源码/执行证据仍只手动刷新，不能把实时任务检查点当成机器验收证据。

## 有界自动更新（0.8.9）

有效任务编号的进度卡，在宿主开放 serverTools 且卡片可见、任务 active 时自动读取 `task_read snapshot`。它只读取一个任务文件，不扫描 Git、不读取完整日志、不运行命令。读取携带 `if_revision`，相同版本只返回 unchanged；不同版本返回有界步骤、摘要和阻塞信息。卡片始终注明保存进度不证明进程还活着。

首次读取延迟 1 秒，之后 5 秒；连续 3/10 次未变分别减频到 10/15 秒。一次最多一个在途请求和一个定时器，每次读取最多 10 秒，一次自动更新时段最多 15 分钟，随后提供继续更新按钮。手动暂停只暂停界面，不停止任务。隐藏、离开视口或断连时取消在途读取；恢复可见后重新观察。任务完成、受阻或归档后停止自动读取，可手动刷新。任何读取拒绝、超时或格式/身份错误均停止重试，显示上次观察，只有用户显式恢复才继续。

新宿主结果、任务身份或连接代次发生变化后，旧在途结果失效。销毁时清理定时器、AbortController、可见性监听及 IntersectionObserver。每个可见卡片的读取独立有界，不宣称所有 iframe 共享连接或已经实现全局唯一刷新者；宿主是否真正挂载/卸载仍需实际客户端验证。

## 滚动与任务入口（0.8.7）

任务进度和工作结果资源通过 `openai/ui.availableDisplayModes` 和 MCP Apps 初始化能力同时声明 inline/fullscreen/pip。组件以宿主返回的实际模式为准，不操作父页面 DOM，不承诺强制固定聊天消息。非 inline 由宿主提供固定视口，组件停止内容高度通知，在内部滚动并固定任务工具栏。

任务编号可选中复制，卡片支持只读刷新、按需展开续接提示，以及用户点击后发送 `ui/message`。发送前检查宿主 `message.text` 能力、当前任务身份与状态；超时、断线或拒绝不自动重发。宿主收到请求不代表模型已经执行。冻结交付和已完成任务不提供自动续接按钮。

跨会话的稳定入口在 Mac 菜单栏/管理面板的任务中心。任务中心从本地认证 API 读取已有任务与最多 10 条关联回执，明确标记部分列表、不可用状态和历史源码快照；没有凭据上屏或执行命令。聊天工具行属于 ChatGPT 宿主展示，不由 AgentDock 合并或隐藏。

## 源码与构建

```text
internal/mcpapps/
  apps.go                  Go embed 与资源查找
  apps_test.go             资源、摘要与包体预算校验
  browser_test.go          隔离 Chrome 行为与渲染性能
  live_progress_browser_test.go 自动只读更新、暂停/隐藏/终态和多实例回收
  memory_test.go           多实例、保留 iframe 与循环回收的堆内存检查
  assets/                 九份生成 HTML 与 manifest
  web/
    src/bridge.ts         官方 SDK、主题、尺寸、连接及资源销毁
    src/live-progress.ts  单请求有界更新调度器，与 DOM/协议解耦
    src/store.ts          有界展示模型、去重、语言切换及清理
    src/views/            独立视图适配器
    src/ui.tsx            共享布局与交互
    src/preview.ts        有界第三方 JSON 预览
    src/style.css         宿主语义颜色及响应式布局
    test/                 状态与投影行为测试
    build.mjs             单视图内联构建与内容哈希
```

依赖由 package-lock 固定：官方 MCP Apps SDK、Preact、TypeScript。没有 CDN、远程字体、额外路由或运行时 Node 服务。SDK 仍占独立卡片的大部分体积；源码共享不意味着不同 iframe 共享运行时对象。

每份 HTML 上限 512 KiB，URI 为 `ui://agentdock/<view>/v2-<内容哈希>.html`。内容变化生成新 URI；进程内缓存构建产物，不在每次调用时重写模板。标准 MCP 与 Nexus bridge 使用同一资源及描述符，保留原始资源读取能力，不声称已完成真实 Nexus 部署联调。

## 生命周期

初始化有 8 秒时限；连接、等待、成功、空、错误、取消和超时均有明确状态。协议使用官方 SDK；只接受父窗口消息，错误不能保留旧成功状态。重连只恢复反馈连接，不重复业务工具。

模型只保留两个语言的有界投影，不保存巨型原始日志或附件。列表和详情按需展开；相同结果不触发 DOM 提交。尺寸通知按动画帧合并，静态卡片不轮询。dispose 清除模型、当前快照、比较键、订阅者、观察器和连接，销毁后不再接受订阅或晚到结果。测试宿主在清理临时用户目录前会优雅关闭 Chrome 并等待资源退出，避免强制取消与浏览器配置写入竞争。

收到 `ui/resource-teardown` 后，先完成协议回复，再清理组件并导航到固定 `about:blank`。这样宿主即使继续保留 iframe 元素，也不必保留整套 SDK 的执行环境；该导航不访问网络。pagehide 仅清理，不再次导航。若宿主禁止导航，组件清理仍完成；服务端减少高频 UI 绑定依然是主防线。

不能用“监听器归零”代替“整个执行环境已释放”。宿主不发送 teardown、仍保留活动卡片时，单个实例的 SDK 成本仍存在。组件无法强制宿主创建或销毁 iframe，也不能控制 ChatGPT 自身的历史记录、缓存和扩展内存。

## 响应体积与结果完整性

普通工具完整结果放在 `structuredContent`，包含原始源码/日志、完整错误、分页游标和读取版本；`content` 文本只提供最多 1024 字节的有界摘要。不能把摘要当作文件全文，也不能把进程未知或部分成功概括为成功。

动态 MCP 的原始文本、图像、音频和资源在外层 `content` 返回一次，结构化 result 使用 `content_location=mcp.content` 指明位置。转换不修改上游对象，其他公开结构化内容保留；上游结果级 `_meta` 不提升到模型可见数据，也不合并进本地 UI 元数据。显式读取旧动态视图资源时，归一化临时引用外层 content，不复制或长期保留它。

SDK CallToolResult 的内容联合类型单独解码，不为 SDK 转换额外解析整个 structuredContent；最终线协议预算另对整体结果进行序列化计量。默认 summary 模式消费方需要读取结构化结果。仅文本集成可显式配置 `AGENTDOCK_RESULT_TEXT_MODE=json`，完整 JSON 文本仍受整体响应预算限制。

## 安全与外观

保留现有主题、响应式布局、焦点、展开及设备选择行为。CSP 禁止外部连接、字体与脚本，不启用 unsafe-eval。不可信内容只按文本渲染；链接仅允许无嵌入凭据的 HTTP(S)，过期下载链接不能继续打开。第三方许可证仍嵌入产物。

## 验证

本机长验证按约定放入 tmux 会话，保存日志和退出码。会话内执行：

```sh
cd internal/mcpapps/web
npm run check
npm test
npm run build
npm run check:generated
cd ../../..
go test -race ./internal/app ./internal/mcp ./internal/mcpapps -count=1
go test -tags mcpapps_browser ./internal/mcpapps -run '^TestFeedbackBrowser' -count=1 -timeout=6m -v
```

Chrome 测试使用独立临时用户目录和本机合成宿主，不接管用户标签页、读取凭据或执行真实 MCP 业务操作。非标准 Chrome 路径用 `AGENTDOCK_UI_CHROME` 指定。

回归同时检查九种资源、异常握手、10 次重连、1000 次重复通知、显式只读刷新、主题/窄屏/焦点、注入防护、teardown 回复先于空白导航，以及 100 次真实 MCP 数据调用不附带 UI 元数据。内存测试在强制 GC 后比较 1/10/30/60/100 个实例、保留 iframe 的销毁和多轮移除；销毁后存活堆必须低于活动时的 25%，循环移除不能留下持续累积的执行环境。

2026-09-24 本机隔离实测：100 张活动工作结果卡片为 691.262 MiB JS 已用堆；销毁但保留 iframe 后 18.281 MiB；移除 iframe 后 0.576 MiB，三轮创建/移除后 0.595 MiB。该数字不是真实 ChatGPT 页面的总内存或公网表现。活动实例本身没有变成零成本，主要改进是按需创建和真正回收。

升级后刷新 AgentDock 工具定义并新开会话。历史响应和旧资源缓存不会被源码修改自动改写；安装前当前运行版不会改变。

## 官方参考

- https://developers.openai.com/plugins/build/chatgpt-ui （Separate data processing from UI rendering）
- https://apps.extensions.modelcontextprotocol.io/api/classes/app.App.html

## 分组与整体预算

工具元数据由 `internal/app/descriptor.go` 统一构建，MCP 服务和目录导出共用同一来源。`agentdock/group` 只表达分类，不自动创建 UI。新增 tool_catalog 无 UI，中间读取与展示仍然分离。

`AGENTDOCK_MAX_TOOL_RESULT_BYTES` 默认16 MiB，覆盖数据、文本、图像和本地 UI 元数据。超限可保存结果以一小时私有不可变资源交付，外层标记 RESULT_DEFERRED 并禁止重放原工具；读取完整资源后才可判断原始结果。不得为了省体积把隐藏元数据搬进 structuredContent，也不能把部分结果误报为成功。
