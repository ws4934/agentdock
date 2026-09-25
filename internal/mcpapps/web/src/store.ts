import {
  type Data,
  type Locale,
  type Model,
  type Normalizer,
  object,
  resultText,
  text,
  tr,
} from "./model";

export type Phase =
  | "connecting"
  | "waiting"
  | "ready"
  | "empty"
  | "error"
  | "cancelled"
  | "timeout";
export interface Snapshot {
  phase: Phase;
  locale: Locale;
  model?: Model;
  message?: string;
  presentation?: Presentation;
  live?: LiveSnapshot;
}

import { type Presentation } from "./presentation";
import { type LiveSnapshot } from "./live-progress";

// 收敛为有界的展示模型；重复快照不触发订阅者，不比较整份业务结果。
export class Store {
  private snapshot: Snapshot;
  private key = "";
  private listeners = new Set<() => void>();
  private models: Partial<Record<Locale, Model>> = {};
  private ended = false;
  private presentation?: Presentation;
  private live?: LiveSnapshot;
  constructor(
    private normalize: Normalizer,
    locale: Locale,
  ) {
    this.snapshot = { phase: "connecting", locale };
  }
  get = (): Snapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.ended) return () => {};
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  private set(next: Snapshot): void {
    if (this.ended) return;
    if (this.presentation) next = { ...next, presentation: this.presentation };
    if (this.live) next = { ...next, live: this.live };
    const key = JSON.stringify(next);
    if (this.key === key) return;
    this.key = key;
    this.snapshot = next;
    for (const listener of this.listeners) listener();
  }
  phase(phase: Phase, message?: string): void {
    this.models = {};
    this.set({ phase, locale: this.snapshot.locale, message });
  }
  setPresentation(presentation: Presentation): void {
    if (this.ended) return;
    this.presentation = presentation;
    this.set({ ...this.snapshot, presentation });
  }
  locale(locale: Locale): void {
    if (this.ended) return;
    if (locale === this.snapshot.locale) return;
    this.set({ ...this.snapshot, locale, model: this.models[locale] });
  }
  setLive(live: LiveSnapshot): void {
    if (this.ended) return;
    this.live = live;
    this.set({ ...this.snapshot, live });
  }
  result(envelope: unknown): void {
    if (this.ended) return;
    const e = object(envelope),
      l = this.snapshot.locale;
    let d = object(e.structuredContent);
    const content =
      !Object.keys(d).length || e.isError === true || d.error
        ? resultText(e.content, 20000)
        : "";
    if (e.isError === true || d.error || object(d.result).isError === true) {
      const err = object(d.error);
      this.phase(
        "error",
        text(err.message ?? d.error) ||
          content ||
          tr(
            l,
            "工具执行失败，未重试操作。",
            "The tool failed. No operation was retried.",
          ),
      );
      return;
    }
    // 只解析有长度上限的纯文本 JSON；无法解析时提供文本结果，而非永远等待。
    if (!Object.keys(d).length && content) {
      try {
        d = object(JSON.parse(content));
      } catch {
        /* 保留普通文本结果。 */
      }
    }
    // MCP 线协议的大 content 只在 envelope 存一次。旧资源显式读取时，在
    // 归一化的短暂窗口提供同一个引用，不复制或长期保留原始正文。
    const remote = object(d.result);
    if (remote.content_location === "mcp.content")
      d = { ...d, result: { ...remote, content: e.content } };
    try {
      const project = (locale: Locale): Model =>
        Object.keys(d).length
          ? this.normalize(d, locale)
          : {
              id: "fallback",
              title: tr(locale, "工具结果", "Tool result"),
              summary: content
                ? tr(locale, "已收到文本结果", "Text result received")
                : tr(locale, "没有返回内容", "No content returned"),
              text: content,
              empty: !content,
            };
      const model = project(l);
      // 只保存两个语言的有界视图模型，不保留可能包含巨型日志或附件的原始结果。
      this.models = {
        [l]: model,
        [l === "zh-CN" ? "en" : "zh-CN"]: project(
          l === "zh-CN" ? "en" : "zh-CN",
        ),
      };
      this.set({ phase: model.empty ? "empty" : "ready", locale: l, model });
    } catch {
      this.phase(
        "error",
        tr(
          l,
          "结果格式无法展示，请查看工具文本输出。",
          "Unable to display this result. See the tool text output.",
        ),
      );
    }
  }
  dispose(): void {
    this.ended = true;
    this.presentation = undefined;
    this.live = undefined;
    this.models = {};
    this.snapshot = { phase: "empty", locale: this.snapshot.locale };
    this.key = "";
    this.listeners.clear();
  }
}
