import { useEffect, useRef, useState } from "preact/hooks";
import { type Locale, type Model, tr } from "./model";
import { continuationPrompt, type DisplayMode, type Presentation } from "./presentation";

export interface WorkActions {
  displayMode?(mode: DisplayMode): Promise<DisplayMode>;
  continueTask?(id: string): Promise<void>;
}
export function WorkControls({ model, locale: l, presentation: p, actions }: {
  model: Model; locale: Locale; presentation: Presentation; actions: WorkActions;
}) {
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [manual, setManual] = useState(false);
  const [notice, setNotice] = useState("");
  const alive = useRef(true), locked = useRef(false);
  useEffect(() => () => { alive.current = false; }, []);
  if (!model.task) return null;
  const task = model.task;
  const prompt = continuationPrompt(task.id, l);
  const requestMode = async (mode: DisplayMode) => {
    if (locked.current || !actions.displayMode) return;
    locked.current = true; setBusy(true); setNotice("");
    try {
      const actual = await actions.displayMode(mode);
      if (alive.current && actual !== mode) setNotice(tr(l, "宿主选择了其他显示方式；任务状态不受影响。", "The host selected a different display mode. Task state is unchanged."));
    } catch {
      if (alive.current) setNotice(tr(l, "宿主未确认显示切换。可从 Mac 菜单栏打开任务中心，不必寻找旧卡片。", "The host did not confirm the mode change. Reopen Task center from the Mac menu bar instead of finding this card."));
    } finally { locked.current = false; if (alive.current) setBusy(false); }
  };
  const continueTask = async () => {
    if (locked.current || attempted || !actions.continueTask) return;
    locked.current = true; setBusy(true); setAttempted(true);
    try {
      await actions.continueTask(task.id);
      if (alive.current) setNotice(tr(l, "续接请求已交给宿主；请查看聊天回复。这不表示任务已执行。", "Continuation request accepted by the host. Check the chat reply; this does not prove task execution."));
    } catch {
      if (alive.current) setNotice(tr(l, "无法确认续接消息是否送达，请先查看聊天，避免重复发送。任务没有被卡片直接执行。", "Message delivery is unconfirmed. Check the chat before sending again. This card did not execute the task."));
    } finally { locked.current = false; if (alive.current) setBusy(false); }
  };
  return <section class="work-controls" aria-label={tr(l, "任务入口", "Task controls")}>
    <div class="work-controls-row">
      <code class="task-id">{task.id}</code>
      {p.mode !== "inline" && <button data-display="inline" disabled={busy} onClick={() => void requestMode("inline")}>{tr(l, "返回聊天", "Back to chat")}</button>}
      {p.modes.includes("pip") && p.mode !== "pip" && <button data-display="pip" disabled={busy} onClick={() => void requestMode("pip")}>{tr(l, "悬浮", "Float")}</button>}
      {p.modes.includes("fullscreen") && p.mode !== "fullscreen" && <button data-display="fullscreen" disabled={busy} onClick={() => void requestMode("fullscreen")}>{tr(l, "全屏", "Full screen")}</button>}
      {task.canContinue && p.canMessage && <button class="continue-task" disabled={busy || attempted} onClick={() => void continueTask()}>{tr(l, "继续分析", "Continue in chat")}</button>}
      <button class="resume-help" aria-expanded={manual} onClick={() => setManual(!manual)}>{tr(l, "跨会话续接", "Resume in another chat")}</button>
    </div>
    {notice && <p class="note" role="status">{notice}</p>}
    {manual && <div class="resume-help-content">
      <p class="note">{tr(l, "复制下面的提示到新聊天。Mac 菜单栏 → 任务中心始终可重新打开，无需翻找旧消息。", "Copy this prompt into a new chat. Reopen Task center from the Mac menu bar at any time; no old message is needed.")}</p>
      <textarea class="resume-prompt" aria-label={tr(l, "续接提示", "Continuation prompt")} readOnly value={prompt} rows={4} onFocus={e => e.currentTarget.select()} />
    </div>}
  </section>;
}
