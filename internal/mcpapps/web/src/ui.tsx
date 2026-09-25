import { render, type ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import {
  type Data,
  type Group,
  type Locale,
  type Model,
  status,
  tr,
} from "./model";
import { type Store, type Snapshot } from "./store";

export interface Actions {
  reconnect(): void;
  open(url: string): Promise<void>;
  refresh?(request: Data): Promise<void>;
}
function Rows({ group, locale }: { group: Group; locale: Locale }) {
  const [limit, setLimit] = useState(12);
  return (
    <section class="group">
      <h3>
        {group.title}
        <span>{group.total ?? group.rows.length}</span>
      </h3>
      <ul>
        {group.rows.slice(0, limit).map((row) => (
          <li key={row.id}>
            <div class="row-heading">
              <strong>{row.title}</strong>
              {row.state && (
                <span class="row-state">{status(row.state, locale)}</span>
              )}
            </div>
            {row.description && <p>{row.description}</p>}
            {row.path && <code>{row.path}</code>}
          </li>
        ))}
      </ul>
      {limit < group.rows.length && (
        <button class="text-button" onClick={() => setLimit((n) => n + 20)}>
          {tr(locale, "显示更多", "Show more")}
        </button>
      )}
      {(group.total ?? 0) > group.rows.length && (
        <p class="note">
          {tr(
            locale,
            "仅展示有界预览；完整内容见工具输出。",
            "Bounded preview. See the tool output for complete content.",
          )}
        </p>
      )}
    </section>
  );
}
function DetailText({
  value,
  diff,
  locale,
}: {
  value: string;
  diff?: boolean;
  locale: Locale;
}) {
  const [limit, setLimit] = useState(80);
  const lines = value.split("\n");
  return (
    <section class="group">
      <h3>
        {tr(
          locale,
          diff ? "变更预览" : "详细结果",
          diff ? "Change preview" : "Details",
        )}
      </h3>
      <pre>
        {lines.slice(0, limit).map((line, i) => (
          <span
            key={i}
            class={
              diff
                ? line.startsWith("+")
                  ? "diff-add"
                  : line.startsWith("-")
                    ? "diff-del"
                    : ""
                : ""
            }
          >
            {line || " "}
            {"\n"}
          </span>
        ))}
      </pre>
      {lines.length > limit && (
        <button class="text-button" onClick={() => setLimit((n) => n + 100)}>
          {tr(locale, "显示更多行", "Show more lines")}
        </button>
      )}
    </section>
  );
}
function Card({
  model: m,
  locale: l,
  actions,
}: {
  model: Model;
  locale: Locale;
  actions: Actions;
}) {
  const [expanded, setExpanded] = useState(false),
    [selected, setSelected] = useState(""),
    [linkError, setLinkError] = useState(""),
    [refreshing, setRefreshing] = useState(false),
    [refreshError, setRefreshError] = useState("");
  const tabs = m.tabs ?? [],
    active = tabs.find((t) => t.id === selected) ?? tabs[0];
  const groups = active?.groups ?? m.groups ?? [];
  const details = !!(
    m.text ||
    m.summary.length > 200 ||
    m.diff ||
    groups.some((g) => g.rows.length) ||
    tabs.length
  );
  const expiry = m.link?.expires ? Date.parse(m.link.expires) : NaN;
  const expired = Number.isFinite(expiry) && Date.now() >= expiry;
  async function openLink() {
    if (!m.link) return;
    if (Number.isFinite(expiry) && Date.now() >= expiry) {
      setLinkError(
        tr(
          l,
          "链接已过期，请重新获取文件链接。",
          "This link has expired. Request a new file link.",
        ),
      );
      return;
    }
    try {
      await actions.open(m.link.url);
    } catch {
      setLinkError(
        tr(
          l,
          "宿主未能打开链接，请使用工具输出中的下载地址。",
          "The host could not open the link. Use the download URL in the tool output.",
        ),
      );
    }
  }
  async function refresh() {
    if (!m.refresh || !actions.refresh || refreshing) return;
    setRefreshing(true);
    setRefreshError("");
    try {
      await actions.refresh(m.refresh);
    } catch {
      setRefreshError(
        tr(
          l,
          "刷新失败，下方保留的是上次观察，不代表最新状态。",
          "Refresh failed. The previous observation below is not current.",
        ),
      );
    } finally {
      setRefreshing(false);
    }
  }
  return (
    <article class="card" data-tone={m.tone ?? "neutral"} data-entity={m.id}>
      <header>
        <div class="title-row">
          <span class="indicator" aria-hidden="true">
            {m.tone === "success" ? "✓" : m.tone === "error" ? "!" : "·"}
          </span>
          <h2>{m.title}</h2>
        </div>
        {details && (
          <button
            class="toggle"
            aria-expanded={expanded}
            aria-controls="details"
            onClick={() => setExpanded((v) => !v)}
          >
            {tr(
              l,
              expanded ? "收起" : "查看详情",
              expanded ? "Less" : "Details",
            )}
            <span aria-hidden="true">{expanded ? "⌃" : "⌄"}</span>
          </button>
        )}
      </header>
      <p class="summary">{m.summary}</p>
      {m.refresh && actions.refresh && (
        <button
          class="text-button refresh-result"
          disabled={refreshing}
          onClick={refresh}
        >
          {tr(
            l,
            refreshing ? "读取中…" : "刷新只读状态",
            refreshing ? "Reading…" : "Refresh read-only state",
          )}
        </button>
      )}
      {refreshError && (
        <p class="notice" role="alert">
          {refreshError}
        </p>
      )}
      {!!(m.state || m.metrics?.length) && (
        <div class="metrics">
          {m.state && <span class="state">{status(m.state, l)}</span>}
          {m.metrics?.map((v, i) => (
            <span key={i}>{v}</span>
          ))}
        </div>
      )}
      {m.progress && (
        <div class="progress-row">
          <progress
            value={m.progress.done}
            max={m.progress.total}
            aria-label={tr(l, "任务完成进度", "Task completion")}
          />
          <span>
            {m.progress.done} / {m.progress.total}
          </span>
        </div>
      )}
      {m.link && (
        <div class="actions">
          <button class="primary" disabled={expired} onClick={openLink}>
            {tr(
              l,
              expired ? "链接已过期" : "打开文件",
              expired ? "Link expired" : "Open file",
            )}
          </button>
          <span class="note">
            {tr(l, "临时下载链接", "Temporary download link")}
          </span>
        </div>
      )}
      {linkError && (
        <p role="alert" class="notice">
          {linkError}
        </p>
      )}
      {expanded && (
        <div id="details" class="details">
          {m.summary.length > 200 && <p class="full-summary">{m.summary}</p>}
          {!!tabs.length && (
            <>
              <label class="device-picker">
                {tr(l, "设备", "Device")}
                <select
                  value={active?.id}
                  onChange={(e) => setSelected(e.currentTarget.value)}
                >
                  {tabs.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.title}
                    </option>
                  ))}
                </select>
              </label>
              <p class="note">{active?.summary}</p>
            </>
          )}
          {groups
            .filter((g) => g.rows.length)
            .map((g) => (
              <Rows key={`${active?.id ?? ""}:${g.id}`} group={g} locale={l} />
            ))}
          {m.diff && <DetailText value={m.diff} diff locale={l} />}
          {m.text && <DetailText value={m.text} locale={l} />}
          {m.truncated && (
            <p class="note">
              {tr(
                l,
                "预览已截断，完整内容见工具输出。",
                "Preview truncated. See the tool output for the full result.",
              )}
            </p>
          )}
        </div>
      )}
    </article>
  );
}
function Feedback({
  snapshot: s,
  actions,
}: {
  snapshot: Snapshot;
  actions: Actions;
}) {
  const l = s.locale;
  if (s.model)
    return (
      <Card key={s.model.id} model={s.model} locale={l} actions={actions} />
    );
  const labels: Record<string, [string, string]> = {
    connecting: ["正在连接反馈组件", "Connecting to the host"],
    waiting: ["等待工具结果", "Waiting for the tool result"],
    error: ["未能显示结果", "Result unavailable"],
    cancelled: ["操作已取消", "Operation cancelled"],
    timeout: ["反馈连接超时", "Connection timed out"],
    empty: ["没有返回内容", "No content returned"],
  };
  const [zh, en] = labels[s.phase] ?? labels.empty;
  return (
    <article class="card notice-card" data-phase={s.phase}>
      <h2>{tr(l, zh, en)}</h2>
      <p class="summary" role={s.phase === "error" ? "alert" : "status"}>
        {s.message ||
          tr(
            l,
            "工具执行与反馈显示相互独立，可查看工具文本输出。",
            "Tool execution is independent of this view. The text output remains available.",
          )}
      </p>
      {["timeout", "error"].includes(s.phase) && (
        <button class="text-button" onClick={actions.reconnect}>
          {tr(l, "重新连接反馈", "Reconnect view")}
        </button>
      )}
    </article>
  );
}
export function mount(
  root: HTMLElement,
  store: Store,
  actions: Actions,
): () => void {
  // 仅首次挂载移除无脚本占位，后续结果由组件差量更新。
  root.replaceChildren();
  function Connected(): ComponentChildren {
    const [snapshot, setSnapshot] = useState(store.get());
    useEffect(() => {
      const stop = store.subscribe(() => setSnapshot(store.get()));
      setSnapshot(store.get());
      return stop;
    }, []);
    return <Feedback snapshot={snapshot} actions={actions} />;
  }
  render(<Connected />, root);
  return () => render(null, root);
}
