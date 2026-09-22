# Computer Use：连续动作流

`desktop_sequence` 在一次调用中连续执行一组动作，最后返回窗口画面。普通操作不需要在每一步等待模型，也不强制为每一步填写控件名或后置条件。

支持 `click`（含双击和鼠标按钮）、`move`、`drag`、`scroll`、`key`、`type`、`set_value` 和显式 `wait`。除 `wait` 外，参数、文本校验、坐标换算和实际输入复用 `desktop_act` 的实现。

## 使用

先取得当前任务已授权后台窗口的快照，再提交步骤。下面的坐标仅为协议示例，实际调用须使用自己观察到的窗口位置：

```json
{
  "task_id": "<本任务令牌>",
  "snapshot_id": "<最新窗口快照ID>",
  "steps": [
    {"action":"click","point":{"x":120,"y":80},"space":"image"},
    {"action":"type","text":"hello"},
    {"action":"key","key":"enter"},
    {"action":"wait","duration_ms":200},
    {"action":"scroll","point":{"x":120,"y":80},"space":"image","delta_y":-300}
  ]
}
```

控件定位是可选路径：`{"action":"click","element":{"title":"Next"}}`。只需一个非空的 role、title 或 description；在当前树内唯一匹配且支持 AXPress 的控件即可，不再限定 AXButton。`set_value` 使用同样的选择器及 text 替换字段全文。前一步改变控件顺序后会重新定位，不沿用旧 element ID。

普通坐标、输入、按键和滚动不遍历 AX 树。只有控件定位或显式 `after` 条件按需读取；初始快照无需预先启用 accessibility。最终图像和可选 AX 元数据仍遵循初始快照设置，不扩大 AXValue、密码和选中文本的数据读取范围。

`after` 可等待 element_exists / element_absent / element_enabled / element_disabled。默认 2 秒，timeout_ms 可设 0–30000；它只观察，不重复输入。没有声明 wait 或 after 时不插入固定延迟。

## 执行与结果

中间只做目标状态检查及必要 AX 观察，不编码或传输逐步图像；最后一次必要观察合并最终截图。窗口标题更新、同应用其他窗口变化或出现普通弹层不再一律中断。目标身份、几何和前后台归属仍核对；新的窗口需要单独选定，绝不暗中改投其他窗口或抢占用户前台。

`completed_steps` 记录已经执行完的动作或等待，并包含显式声明的后置条件；`dispatched_steps` 不计只读等待。它们不是业务成功证明，`application_verified` 仍为 false。最终截图失败会保留已执行数量，不要求重跑前面动作。

暂停、停止、监视器断联、权限失效及输入清理故障沿用已有控制链路；本机单步最多允许一个实际输入，前置只读等待不消耗这一步。出现输入错误时返回已执行进度及不确定步骤，不自动重放。付款、发送、删除等业务确认仍由调用方按用户授权处理，不能以应用访问许可代替。

请求有宽松的资源预算：默认执行预算 60 秒，可指定至 300 秒；最多 256 个动作，避免超大请求或永久占用。它们不是交互白名单，通常无需手工调整。原生调用可能在取消后才返回，但不能因此继续发送后续输入。

## 验证

`sequence_test.go` 覆盖授权、停止、断联、条件、部分执行与故障；`sequence_stream_test.go` 覆盖混合输入、零 AX 普通动作、精确图像坐标继承、超过 16 步、通用 AX 控件、参数预校验、可取消等待和拖拽清理。Runtime/MCP 测试校验扩展参数与最终图像传输。

构建、测试日志和版本号以本轮交付记录为准；内存后端测试不代表已经在所有真实应用上测量过延迟或完成业务验收。
