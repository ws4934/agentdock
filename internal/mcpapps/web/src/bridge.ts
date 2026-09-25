import { App } from "@modelcontextprotocol/ext-apps/app-with-deps";
import {
  type Normalizer,
  type Locale,
  object,
  text,
  safeURL,
  tr,
} from "./model";
import { Store } from "./store";
import { mount } from "./ui";
import {
  hostPresentation,
  inlinePresentation,
  continuationPrompt,
  isDisplayMode,
} from "./presentation";
import { LiveProgress } from "./live-progress";

export function start(view: string, normalize: Normalizer): void {
  const taskView = view === "work_result" || view === "task_progress";
  const root = document.getElementById("content")!;
  const locale = (value: unknown): Locale =>
    /^(zh|zh-cn|zh-sg|zh-hans(?:-.*)?)$/i.test(text(value)) ? "zh-CN" : "en";
  const store = new Store(normalize, locale(navigator.language));
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  let app: App | undefined,
    generation = 0,
    connected = false,
    ended = false,
    frame = 0,
    lastHeight = 0,
    hostTheme = "";
  let observer: ResizeObserver | undefined;
  let displayBusy = false;
  let continuationAttempted: string | undefined;
  let inViewport = false;
  let intersection: IntersectionObserver | undefined;
  let hostResultRevision = 0;
  const live =
    view === "task_progress"
      ? new LiveProgress(readTaskSnapshot, (state) => store.setLive(state))
      : undefined;
  function syncLive(): void {
    if (!live || ended) return;
    const snapshot = store.get(),
      target = snapshot.model?.live;
    const serverTools = app?.getHostCapabilities()?.serverTools;
    const supported =
      connected && typeof serverTools === "object" && serverTools !== null;
    const visible =
      document.visibilityState === "visible" &&
      (inViewport ||
        (snapshot.presentation?.mode !== undefined &&
          snapshot.presentation.mode !== "inline"));
    live.configure(
      target?.id ?? "",
      target?.active === true,
      supported,
      visible,
    );
  }
  async function readTaskSnapshot(signal: AbortSignal): Promise<boolean> {
    const target = store.get().model?.live;
    if (!target || !connected || !app || ended)
      throw new Error("task snapshot unavailable");
    const connection = generation,
      hostResult = hostResultRevision;
    const args = {
      action: "snapshot",
      task_id: target.id,
      ...(target.revision ? { if_revision: target.revision } : {}),
    };
    const result = await app.callServerTool(
      { name: "task_read", arguments: args },
      { timeout: 10000, signal },
    );
    if (
      signal.aborted ||
      ended ||
      generation !== connection ||
      hostResult !== hostResultRevision
    )
      throw new DOMException("Superseded", "AbortError");
    const d = object(result.structuredContent);
    if (
      result.isError ||
      d.error ||
      d.action !== "snapshot" ||
      d.task_id !== target.id ||
      !/^tsk1:[a-f0-9]{64}$/.test(text(d.revision)) ||
      typeof d.unchanged !== "boolean"
    )
      throw new Error("invalid task snapshot");
    if (d.unchanged === true) {
      if (!target.revision || d.revision !== target.revision)
        throw new Error("unproven unchanged task");
      return false;
    }
    const task = object(d.task_summary);
    if (
      task.id !== target.id ||
      task.revision !== d.revision ||
      !["active", "blocked", "completed"].includes(text(task.status))
    )
      throw new Error("task snapshot scope changed");
    store.result(result);
    if (store.get().phase !== "ready")
      throw new Error("invalid progress projection");
    size();
    return true;
  }
  const variables = new Set([
    "--font-sans",
    "--color-text-primary",
    "--color-text-secondary",
    "--color-background-primary",
    "--color-background-secondary",
    "--color-border-primary",
  ]);
  function theme() {
    document.documentElement.dataset.theme =
      hostTheme || (media.matches ? "dark" : "light");
  }
  function context(value: unknown) {
    const c = object(value);
    if (c.theme === "light" || c.theme === "dark") hostTheme = c.theme;
    if (c.locale || c.language) store.locale(locale(c.locale || c.language));
    for (const [key, value] of Object.entries(
      object(object(c.styles).variables),
    )) {
      if (
        variables.has(key) &&
        typeof value === "string" &&
        value.length < 500 &&
        !/url\s*\(/i.test(value)
      )
        document.documentElement.style.setProperty(key, value);
    }
    theme();
    document.documentElement.lang = store.get().locale;
    if (taskView) {
      const presentation = hostPresentation(
        c,
        app?.getHostCapabilities(),
        store.get().presentation,
      );
      store.setPresentation(presentation);
      document.documentElement.dataset.displayMode = presentation.mode;
    }
  }
  function size() {
    if (!connected || ended || frame) return;
    if (
      store.get().presentation?.mode &&
      store.get().presentation?.mode !== "inline"
    )
      return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      if (!connected || ended) return;
      if (
        store.get().presentation?.mode &&
        store.get().presentation?.mode !== "inline"
      )
        return;
      const height = Math.ceil(root.getBoundingClientRect().height);
      if (height > 0 && height !== lastHeight) {
        lastHeight = height;
        void app?.sendSizeChanged({ height }).catch(() => {});
      }
    });
  }
  async function reconnect() {
    const id = ++generation;
    connected = false;
    syncLive();
    if (taskView) {
      store.setPresentation(inlinePresentation());
      document.documentElement.dataset.displayMode = "inline";
    }
    lastHeight = 0;
    if (frame) {
      cancelAnimationFrame(frame);
      frame = 0;
    }
    await app?.close();
    if (ended || id !== generation) return;
    store.phase("connecting");
    // SDK 负责协议校验和握手；尺寸观察由本组件管理，销毁时明确回收。
    const next = new App(
      { name: "agentdock-" + view, version: "2.0.0" },
      taskView
        ? { availableDisplayModes: ["inline", "fullscreen", "pip"] }
        : {},
      { autoResize: false },
    );
    app = next;
    const current = () => !ended && id === generation;
    next.ontoolinput = () => {
      if (current() && store.get().phase === "connecting")
        store.phase("waiting");
    };
    next.ontoolresult = (result) => {
      if (current()) {
        hostResultRevision++;
        live?.invalidate();
        store.result(result);
        size();
      }
    };
    next.ontoolcancelled = (event) => {
      if (current()) store.phase("cancelled", text(event.reason) || undefined);
    };
    next.onhostcontextchanged = (value) => {
      if (current()) context(value);
    };
    next.onerror = () => {
      if (current() && connected)
        store.phase(
          "error",
          tr(
            store.get().locale,
            "反馈消息无效，请查看工具文本输出。",
            "Invalid view message. See the tool text output.",
          ),
        );
    };
    next.onclose = () => {
      if (current() && connected) {
        connected = false;
        store.phase(
          "error",
          tr(
            store.get().locale,
            "反馈连接已关闭。",
            "The view connection was closed.",
          ),
        );
      }
    };
    next.onteardown = async () => {
      if (current())
        setTimeout(() => {
          if (!current()) return;
          dispose();
          // 协议回复先完成；宿主即使保留 iframe，也释放整套 SDK 执行环境。
          // 固定空白文档不访问网络；pagehide 只清理，不再次导航。
          try {
            window.location.replace("about:blank");
          } catch {
            /* 受限宿主仍已完成组件清理。 */
          }
        }, 0);
      return {};
    };
    try {
      await next.connect(undefined, { timeout: 8000 });
      if (!current()) {
        await next.close();
        return;
      }
      connected = true;
      context(next.getHostContext());
      if (store.get().phase === "connecting") store.phase("waiting");
      syncLive();
      size();
    } catch (error) {
      if (!current()) return;
      connected = false;
      const message = error instanceof Error ? error.message : "";
      store.phase(
        /timeout|timed out/i.test(message) ? "timeout" : "error",
        tr(
          store.get().locale,
          "未收到有效的宿主连接。重连只恢复反馈，不会再次执行工具。",
          "No valid host connection. Reconnecting restores the view without re-running tools.",
        ),
      );
    }
  }
  const unmount = mount(root, store, {
    reconnect: () => {
      void reconnect();
    },
    displayMode: async (mode) => {
      if (
        !taskView ||
        !connected ||
        !app ||
        ended ||
        displayBusy ||
        !isDisplayMode(mode) ||
        !store.get().presentation?.modes.includes(mode)
      )
        throw new Error("display mode unavailable");
      const current = generation;
      displayBusy = true;
      try {
        const result = await app.requestDisplayMode(
          { mode },
          { timeout: 8000 },
        );
        if (ended || current !== generation)
          throw new Error("display response expired");
        context({ displayMode: result.mode });
        lastHeight = 0;
        size();
        return result.mode;
      } finally {
        displayBusy = false;
      }
    },
    continueTask: async (id) => {
      const task = store.get().model?.task;
      if (
        !taskView ||
        !connected ||
        !app ||
        ended ||
        !store.get().presentation?.canMessage ||
        !task?.canContinue ||
        task.id !== id ||
        continuationAttempted === id
      )
        throw new Error("continuation unavailable");
      // 先标记尝试；断线或超时也不自动重放用户消息。
      continuationAttempted = id;
      const current = generation;
      const result = await app.sendMessage(
        {
          role: "user",
          content: [
            { type: "text", text: continuationPrompt(id, store.get().locale) },
          ],
        },
        { timeout: 8000 },
      );
      if (ended || current !== generation || result.isError)
        throw new Error("continuation not confirmed");
    },
    refresh: async (request) => {
      if (view === "task_progress" && live) {
        if (
          request.action !== "snapshot" ||
          request.task_id !== store.get().model?.live?.id ||
          Object.keys(request).some((k) => k !== "action" && k !== "task_id")
        )
          throw new Error("invalid task refresh scope");
        await live.refresh();
        return;
      }
      if (view !== "work_result" || !connected || !app || ended)
        throw new Error("readonly refresh unavailable");
      const current = generation;
      const initialSnapshot = store.get();
      const allowed = new Set([
        "task_id",
        "workdir",
        "job_ids",
        "artifact_ids",
        "source_paths",
      ]);
      if (
        Object.keys(request).some((k) => !allowed.has(k)) ||
        typeof request.task_id !== "string" ||
        typeof request.workdir !== "string"
      )
        throw new Error("invalid refresh scope");
      const result = await app.callServerTool(
        { name: "work_result_read", arguments: request },
        { timeout: 30000 },
      );
      if (ended || current !== generation) return;
      // Do not overwrite a newer host result (or another selected delivery) with
      // an older in-flight refresh response from the same connection.
      if (store.get() !== initialSnapshot)
        throw new Error("refresh was superseded");
      if (result.isError) throw new Error("readonly refresh failed");
      const projection = object(object(result.structuredContent).work_result);
      if (
        object(projection.task).id !== request.task_id ||
        projection.workdir !== request.workdir ||
        projection.frozen === true
      )
        throw new Error("refresh scope changed");
      store.result(result);
      size();
    },
    open: async (url) => {
      if (!connected || !app || !safeURL(url))
        throw new Error("link unavailable");
      const result = await app.openLink({ url }, { timeout: 8000 });
      if (result.isError) throw new Error("host rejected link");
    },
    toggleLive: () => live?.toggle(),
  });
  const unsubscribe = store.subscribe(() => {
    size();
    syncLive();
  });
  if (live) {
    document.addEventListener("visibilitychange", syncLive);
    if (typeof IntersectionObserver !== "undefined") {
      intersection = new IntersectionObserver((entries) => {
        inViewport = entries.some((entry) => entry.isIntersecting);
        syncLive();
      });
      intersection.observe(root);
    }
  }
  observer = new ResizeObserver(size);
  observer.observe(root);
  function dispose() {
    if (ended) return;
    ended = true;
    generation++;
    connected = false;
    live?.dispose();
    intersection?.disconnect();
    document.removeEventListener("visibilitychange", syncLive);
    observer?.disconnect();
    if (frame) cancelAnimationFrame(frame);
    media.removeEventListener("change", theme);
    window.removeEventListener("pagehide", dispose);
    unsubscribe();
    store.dispose();
    unmount();
    void app?.close();
    app = undefined;
  }
  media.addEventListener("change", theme);
  window.addEventListener("pagehide", dispose, { once: true });
  theme();
  document.documentElement.lang = store.get().locale;
  void reconnect();
}
