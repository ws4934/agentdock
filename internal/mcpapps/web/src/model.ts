// 工具结果是不可信数据：只投影界面使用的有界字段，不渲染 HTML 或执行工具。
export type Data = Record<string, unknown>;
export type Locale = "zh-CN" | "en";
export type Tone = "neutral" | "success" | "warning" | "error";
export interface Row {
  id: string;
  title: string;
  description?: string;
  state?: string;
  path?: string;
}
export interface Group {
  id: string;
  title: string;
  rows: Row[];
  total?: number;
}
export interface Model {
  id: string;
  title: string;
  summary: string;
  state?: string;
  tone?: Tone;
  metrics?: string[];
  groups?: Group[];
  text?: string;
  diff?: string;
  truncated?: boolean;
  progress?: { done: number; total: number };
  link?: { url: string; expires?: string };
  tabs?: { id: string; title: string; summary: string; groups: Group[] }[];
  empty?: boolean;
}
export type Normalizer = (data: Data, locale: Locale) => Model;
export const object = (value: unknown): Data =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Data)
    : {};
export const records = (value: unknown, limit = 200): Data[] =>
  Array.isArray(value)
    ? (value
        .slice(0, limit)
        .filter(
          (x) => x !== null && typeof x === "object" && !Array.isArray(x),
        ) as Data[])
    : [];
export const text = (value: unknown, limit = 2000): string =>
  typeof value === "string"
    ? value.slice(0, limit)
    : typeof value === "number" || typeof value === "boolean"
      ? String(value)
      : "";
export const number = (value: unknown): number =>
  typeof value === "number" && Number.isFinite(value)
    ? Math.max(0, Math.floor(value))
    : 0;
export const tr = (locale: Locale, zh: string, en: string): string =>
  locale === "zh-CN" ? zh : en;
export const length = (value: unknown): number =>
  Array.isArray(value) ? value.length : 0;
export function rows(value: unknown): Row[] {
  return records(value).map((r, i) => ({
    id: text(r.id ?? r.name ?? r.path) + ":" + i,
    title: text(r.title ?? r.name ?? r.text ?? r.id ?? r.path, 400) || "—",
    description: text(r.description ?? r.summary ?? r.reason),
    state: text(r.status ?? r.phase ?? r.operation),
    path: text(r.file ?? r.path ?? r.cwd, 1000),
  }));
}
export function group(id: string, title: string, value: unknown): Group {
  return { id, title, rows: rows(value), total: length(value) };
}
export function safeURL(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 8192) return;
  try {
    const url = new URL(value);
    if (
      ["https:", "http:"].includes(url.protocol) &&
      !url.username &&
      !url.password
    )
      return url.href;
  } catch {
    /* 无效地址不生成可点击操作。 */
  }
}
export function status(value: unknown, locale: Locale): string {
  const raw = text(value, 100);
  const labels: Record<string, string> = {
    ready: "就绪",
    idle: "空闲",
    running: "运行中",
    active: "进行中",
    pending: "待处理",
    in_progress: "进行中",
    completed: "已完成",
    success: "成功",
    pass: "通过",
    error: "失败",
    failed: "失败",
    blocked: "已阻塞",
    paused: "已暂停",
    cancelled: "已取消",
    stopped: "已停止",
    closed: "已关闭",
    online: "在线",
    offline: "离线",
    unavailable: "不可用",
    enabled: "已启用",
    disabled: "已停用",
    configured: "已配置",
    created: "已创建",
    dispatched: "已发送",
    execute: "执行中",
    closeout: "收尾中",
    exited: "已退出",
  };
  return locale === "zh-CN"
    ? labels[raw] || raw.replaceAll("_", " ")
    : raw.replaceAll("_", " ");
}
export function tone(value: unknown): Tone {
  const raw = text(value);
  if (["error", "failed", "blocked", "cleanup_failed"].includes(raw))
    return "error";
  if (["completed", "success", "pass", "ready"].includes(raw)) return "success";
  if (
    ["paused", "pending", "cancelled", "offline", "unavailable"].includes(raw)
  )
    return "warning";
  return "neutral";
}
export function resultText(value: unknown, limit = 18000): string {
  if (!Array.isArray(value)) return "";
  let result = "";
  for (const part of value.slice(0, 50)) {
    const p = object(part);
    if (p.type === "text")
      result +=
        (result ? "\n" : "") + text(p.text, Math.max(0, limit - result.length));
    if (result.length >= limit) break;
  }
  return result.slice(0, limit);
}
