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

export function start(view: string, normalize: Normalizer): void {
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
  }
  function size() {
    if (!connected || ended || frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      if (!connected || ended) return;
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
      {},
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
      if (current()) setTimeout(dispose, 0);
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
    open: async (url) => {
      if (!connected || !app || !safeURL(url))
        throw new Error("link unavailable");
      const result = await app.openLink({ url }, { timeout: 8000 });
      if (result.isError) throw new Error("host rejected link");
    },
  });
  const unsubscribe = store.subscribe(size);
  observer = new ResizeObserver(size);
  observer.observe(root);
  function dispose() {
    if (ended) return;
    ended = true;
    generation++;
    connected = false;
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
