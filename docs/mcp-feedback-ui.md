# MCP Apps 反馈组件 v2

## 实现范围

AgentDock 0.8.4 使用仓库内的 `internal/mcpapps` 提供反馈资源，不再把 UI 修改写进 Go 模块缓存，也不需要修改其他项目的共享协议包。MCP 业务数据格式保持原样。

八个视图分别构建：能力概览（包括设备列表）、任务进度、文件变更、外部 MCP 结果、文件下载、记忆结果、工作流、编程会话。基础通信使用官方 `@modelcontextprotocol/ext-apps` 2.0.0；展示使用 Preact 10.29.8 与 TypeScript，构建依赖由 `package-lock.json` 固定。

```
internal/mcpapps/
  apps.go                  Go embed 与资源查找
  apps_test.go             不可变资源/摘要/预算校验
  browser_test.go           隔离 Chrome 行为与性能测试（显式 build tag）
  assets/                  提交到 Git 的八份 HTML 与 manifest
  web/
    src/bridge.ts          SDK 生命周期、主题、尺寸及销毁
    src/store.ts           展示状态、去重与语言切换
    src/model.ts           有界的字段投影
    src/preview.ts         第三方 JSON 预览上限
    src/ui.tsx             共享卡片、详情与列表
    src/views/             八种独立数据适配器
    src/style.css          宿主语义颜色与响应式样式
    test/                  状态/投影/安全边界测试
    build.mjs              独立入口、内联资源、内容哈希
```

每个 HTML 包含自己需要的视图及 SDK，不加载 CDN、远程字体、额外脚本、路由或 Node 运行服务。源码共享组件不等于每张卡片加载全部业务视图。

## 生命周期与错误边界

- 连接中、等待结果、成功、空结果、错误、取消、连接超时均有可见表示。初始化有 8 秒期限。
- 使用 SDK 注册通知和验证协议消息。只接受父窗口来源；无效结果不会被解释为“零项能力”。
- 工具错误/取消会清除旧成功内容。纯文本结果或可解析的有界 JSON 文本提供降级展示，不再一直等待。
- `重新连接反馈` 只重新握手；不调用写文件、命令或任何业务工具。宿主未重新发送结果时保持明确的等待说明，用户仍能查看工具文本。
- 业务状态与展开、设备选择、焦点分开。相同实体的结果更新不重建整张卡片；同一结果重复到达不产生 DOM 提交。
- 详情首次展开才创建；列表先显示 12 项，按需增加；长文本/差异预览有上限。
- 保存两个语言的有界视图模型，不保存原始日志/附件响应。第三方结构化内容采用深度、节点、字符串和输出长度受限的预览。
- 静态卡片不轮询、不持续播放动画。尺寸观察只绑定内容根节点；变化按动画帧合并且同高不重复发送。销毁/重连明确回收监听器、观察器、定时任务和连接。

SDK 无法强制宿主创建 iframe。宿主完全不挂载时，工具已有的文本与结构化输出继续可用；不能把“工具成功”当作“UI 已显示”。也不假设不同工具调用会复用一张卡片。

## Go 服务与缓存

构建结果的 URI 为 `ui://agentdock/<view>/v2-<内容哈希>.html`，同内容稳定、内容变更时换 URI。HTML 在进程启动时加载一次，不在每次资源读取时重新替换整份模板。

工具发现、标准调用结果及 `Server.Invoke` 桥接结果使用同一个 URI，错误结果也保留绑定。工作流从仅 `match` 结果绑定改成工具级发现，对其他操作也提供结果视图。命令、会话轮询、普通读取等高频工具仍然不额外绑定卡片。

桥接资源的 renderer contract 延续共享数据协议，资源地址由本地 manifest 决定，不再用旧的固定 URI 推断新资源类型。测试覆盖 AgentDock 的能力声明、资源读取、调用 envelope 和独立功能开关。未对一个真实部署的 NexusDock 服务器做端到端升级验证。

升级后需要刷新客户端的 AgentDock 工具定义并使用新会话。仍然缓存旧 URI 的客户端需要刷新，不依靠重新启动 Core 来刷新平台缓存。

## 安全与外观

CSP 保持禁止外部连接和资源，仅允许构建时内联脚本/样式。不加入 `unsafe-eval`。SDK 使用其默认无 JIT 解析配置；第三方内容只作为文本进入组件，不能成为 HTML、脚本或 CSS。链接只允许无内嵌凭据的 HTTP(S)，过期下载链接不能重新触发打开。

字体以 14px 主标题和 12–13px 次要内容为主，窄屏换行而不缩到 9px。使用宿主主题变量，避免重复品牌和开发者术语；未启用模块不挤占默认摘要。详情与主要状态保留可读、可聚焦的操作入口。

第三方依赖许可证保留在 `web/THIRD_PARTY_NOTICES.txt` 并嵌入每个 HTML；相同许可证去重保留所有对应包名。

## 验证命令

```sh
cd internal/mcpapps/web
npm ci --no-audit --no-fund
npm run check
npm test
npm run build
npm run check:generated
cd ../../..
go test ./internal/mcp ./internal/mcpapps
go test -tags mcpapps_browser ./internal/mcpapps -run TestFeedbackBrowser -v -count=1 -timeout=3m
```

非 macOS 或未按标准位置安装 Chrome 时，设置 `AGENTDOCK_UI_CHROME`。浏览器测试使用独立临时用户目录和本机 httptest 宿主，不读取用户的浏览器标签，不执行真实桌面输入或业务工具。`AGENTDOCK_UI_SCREENSHOTS` 可指定合成页面截图输出目录。

CI 检查类型、13 项状态/投影用例、格式、生成一致性、Go 协议边界及真实浏览器行为，不再靠匹配 CSS/JavaScript 字符串来证明 UI 正常。

最终浏览器回归在本机 macOS/arm64 上覆盖八个视图、握手失败/超时、错误/取消/空/纯文本、10 次重连、1,000 次相同结果、设备选择、展开和焦点、240px/2 倍缩放、非父窗口通知、恶意文本和 URL、到期链接及销毁。最后一轮 40 个任务更新样本的 DOM 提交 P95 为 1.3ms；同页八类卡片并行冷挂载约 952ms，闲置后不再发送消息，全部销毁后监听器、观察器、计时器和动画帧计数归零。这些是本机合成宿主的计算与本地加载时间，不是公网时延或真实 ChatGPT 的挂载耗时。

包体权衡必须明确：原始共享模板约 63KB；采用完整官方 SDK 后，每视图 HTML 约 0.5MB（含第三方许可证），构建上限 512KiB。压缩体积仅是构建指标，不代表 MCP 链路一定启用压缩。此实现优化了无效渲染、闲置活动、状态可靠性和可维护性，没有宣称比旧手写模板更小。

完整 Go、race、vet 和 macOS App/DMG/ZIP 验证的最终结果保留在 Git 忽略的 `dist/validation/mcp-ui-v2/`。真实 ChatGPT 中未热替换当前服务；安装新包并刷新工具定义之后仍需宿主联调。

## 官方参考

- MCP Apps App API：https://apps.extensions.modelcontextprotocol.io/api/classes/app.App.html
- OpenAI 组件资源与缓存：https://developers.openai.com/plugins/build/chatgpt-ui
