import { object, type Locale, tr } from "./model";

export type DisplayMode = "inline" | "fullscreen" | "pip";
export interface Presentation {
  mode: DisplayMode;
  modes: DisplayMode[];
  canMessage: boolean;
}
export const inlinePresentation = (): Presentation => ({ mode: "inline", modes: ["inline"], canMessage: false });
export function isDisplayMode(value: unknown): value is DisplayMode {
  return value === "inline" || value === "fullscreen" || value === "pip";
}
export function hostPresentation(context: unknown, capabilities: unknown, previous = inlinePresentation()): Presentation {
  const c = object(context), caps = object(capabilities);
  const modes = Array.isArray(c.availableDisplayModes)
    ? Array.from(new Set<DisplayMode>(["inline", ...c.availableDisplayModes.filter(isDisplayMode)]))
    : previous.modes;
  const message = object(caps.message).text;
  return {
    mode: isDisplayMode(c.displayMode) ? c.displayMode : previous.mode,
    modes,
    canMessage: message !== null && typeof message === "object" && !Array.isArray(message),
  };
}
export function taskID(value: unknown): string | undefined {
  return typeof value === "string" && value.length === 20 && /^tsk_[a-f0-9]{16}$/.test(value) ? value : undefined;
}
export function continuationPrompt(id: string, locale: Locale): string {
  if (!taskID(id)) throw new Error("invalid task identity");
  return tr(locale,
    `请续接 AgentDock 任务 ${id}。先读取当前任务状态与已有 Job 回执，核对项目和源码版本，只继续原授权范围内尚未完成的工作。不要重放已完成或执行状态未知的操作；任务已完成时仅汇报结果，不重新执行。`,
    `Continue AgentDock task ${id}. First read its current task state and existing job receipts. Verify the project and source revision, then continue only unfinished work within the original approved scope. Do not replay completed or unknown executions. If the task is completed, report its result instead of rerunning it.`);
}
