# Computer Use：本机连续步骤

`desktop_sequence` 将已经确定的连续控件操作放到一次工具调用中执行。模型提交计划，本机逐步观察和执行，在完成或中断时返回结果；不再在每个数字按钮之间等待模型往返。

与 `desktop_act(observe_after:true)` 的区别：后者仍是一次输入后返回模型；sequence 在本机读取下一步所需的 AX 状态并继续预定步骤。原有单次工具保留给需要重新理解画面、跨窗口或不能通过 AX 定位的操作。

## 范围

- 每次 1–16 步，总预算默认 10 秒、最多 30 秒；系统原生调用可能在取消后才返回，但后续输入不得继续。
- 要求同一任务中已授权应用的有效后台 AX 快照。PID、窗口、几何和窗口标题/集合变化即中断；不能在步骤内切换应用或窗口。
- 支持 AXButton 的 `click` 和可赋值文本控件的 `set_value`。元素必须给出精确 role 及非空 title 或 description；不接受旧 element_id、坐标、键盘快捷键、脚本、循环或分支。
- 每步输入前使用完整有界 AX 树唯一定位。缺失、歧义、禁用、未知启用状态、树截断或新模态界面均停止。输入后重新观察；原生输入还会重新核验元素身份。
- 中间只读取原有 AX 元数据，不读取 AXValue。最后按原快照的截图设置返回一幅窗口图像；`screenshot:false` 不会被升级。

这不是通用业务风险分类器。调用方必须只提交用户已授权的确定步骤；付款、发送、删除等需要额外确认的动作应拆出计划，不能因为具有应用访问许可而跳过业务确认。窗口内容和 AX 标签仍是不可信数据，不是授权指令。

## 示例

先 `desktop_task` 声明任务、由本机批准应用，再用 `desktop_snapshot(window_id, accessibility:true)` 读取真实控件。以下标签仅为示例，必须替换为该快照实际提供的 title/description：

```json
{
  "task_id": "<本任务令牌>",
  "snapshot_id": "<最新后台AX快照ID>",
  "steps": [
    {"action":"click","element":{"role":"AXButton","title":"1"}},
    {"action":"click","element":{"role":"AXButton","title":"2"}},
    {"action":"click","element":{"role":"AXButton","title":"+"}},
    {"action":"click","element":{"role":"AXButton","title":"3"}},
    {"action":"click","element":{"role":"AXButton","title":"="}}
  ]
}
```

这五步只需要一次 sequence 调用；初始发现、应用授权和初始快照不包含在该计数中。动作后图像通过标准 MCP image content 返回，供模型核验最终显示。`application_verified` 仍为 false，执行完计划不等于已确认计算结果。

异步界面可为某步指定 `after`：

```json
"after": {
  "condition": "element_enabled",
  "element": {"role":"AXButton","title":"Next"},
  "timeout_ms": 2000
}
```

支持 element_exists / element_absent / element_enabled / element_disabled。条件等待只重新观察，不重复点击。默认 2 秒，最多 5 秒，0 表示只检查一次；首次动作后观察也计入正等待预算。一个已存在控件的存在谓词只证明它存在，不证明前一动作造成了变化或完成了业务。

## 中断与控制

返回 `total_steps`、`dispatched_steps`、`completed_steps` 和不含输入正文的逐步记录。completed_steps 表示输入投递后观察成功，并满足声明的后置条件；未声明条件时不代表应用确认了动作。

输入失败可能已部分投递。sequence 返回 `outcome:interrupted`、`failed_step`、`failure_stage` 与 `error.may_have_dispatched`，不自动重试整段或当前步骤。完整执行但最终截图失败时，保留已完成数量，failed_step 为 0。中断不提供新动作令牌，必须重新观察后决定剩余工作；不能依据“重试”按钮机械重跑已完成输入。

本机控制窗只增加 `2/5` 这样的进度。暂停、停止、断联与清理故障仍走原有控制代次和取消链路，不等待整段完成才生效。本机“下一步”最多执行第一个输入，随后暂停，返回观察证据但清空动作令牌；恢复后重新观察并重新提交尚未执行的步骤，没有持久化自动续跑队列。

## 本轮验证记录

`go test ./internal/tool/desktop ./internal/app ./internal/mcp -run 'TestSequence|TestDesktopSequence' -count=1 -v` 已通过，当时覆盖：五步一次调用、每步控件重排、图像次数、文本处理、整段参数预校验、控件/窗口/权限变化、部分投递、单步/暂停/停止、条件等待与过期样本、Runtime schema、MCP 图像及部分进度。

其后补充了任务授权撤回、监视器失联、清理锁存及异步观察停止四项回归，并保留清理失败事件类型。这些后补测试和完整最终版本尚未运行验证：全量回归启动调用被上层执行安全判定拦截，未生成运行日志，未通过其他路径重试。当前不能声称全仓 race、Swift/DMG 构建或真实计算器连续输入验收通过。

此文档描述已实现的接口，不代表连接中的已安装 Core 已升级。此次不替换现有运行程序，也不将旧 `313b247` 安装包当成包含 sequence 的新版本。
