# MCP Apps 反馈组件 v2

## 展示与执行分离

`internal/mcpapps` 提供九种独立、内容寻址的反馈资源：能力概览、任务进度、文件变更、外部 MCP、文件下载、记忆、工作流、编程会话和工作结果。保留资源不等于每次调用都挂载它。

自动 UI 绑定仅保留 `work_result_show`、`file_publish` 和 `diagnostic_export`。`task_manage`、`file_edit`、`agentdock_context`、`mcp_tool_call`、`acp_session`、工作流/记忆操作以及 `work_result_read`、`work_result_freeze` 都只返回数据。描述符和调用结果使用同一份绑定表，不允许只删结果元数据、却仍由旧描述符触发卡片。

中间检查用 `work_result_read`；显式可视化或最终交付用 `work_result_show`。展示工具与读取工具共享选择校验、任务/源码范围和权限逻辑，不重复执行命令。卡片内刷新仍只调用 `work_result_read`，不会重新挂载另一张卡片。不要每个检查点都调用 show。

## 源码与构建

```text
internal/mcpapps/
  apps.go                  Go embed 与资源查找
  apps_test.go             资源、摘要与包体预算校验
  browser_test.go          隔离 Chrome 行为与渲染性能
  memory_test.go           多实例、保留 iframe 与循环回收的堆内存检查
  assets/                 九份生成 HTML 与 manifest
  web/
    src/bridge.ts         官方 SDK、主题、尺寸、连接及资源销毁
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
go test -tags mcpapps_browser ./internal/mcpapps -run '^TestFeedbackBrowser' -count=1 -timeout=4m -v
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
