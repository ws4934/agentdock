import { type Locale, tr } from "./model";
import { type LiveSnapshot } from "./live-progress";

export function LiveControls({
  snapshot: s,
  locale: l,
  active,
  toggle,
}: {
  snapshot: LiveSnapshot;
  locale: Locale;
  active: boolean;
  toggle(): void;
}) {
  const labels = {
    live: ["自动更新中 · 只读", "Updating automatically · read-only"],
    paused: [
      "已暂停界面更新，任务执行不受影响",
      "View updates paused; task execution is unchanged",
    ],
    hidden: [
      "不在可见区域，已暂停更新",
      "Updates paused outside the visible area",
    ],
    stopped: [
      "任务已结束、受阻或归档，自动更新已停止",
      "Task completed, blocked or archived; automatic updates stopped",
    ],
    error: [
      "更新失败，已停止重试。当前显示的是上次观察。",
      "Update failed; retries stopped. Showing the previous observation.",
    ],
    expired: [
      "自动更新时段已结束，可继续更新",
      "Automatic update session ended; resume when needed",
    ],
    unavailable: [
      "当前连接未开放只读更新；可从任务中心重新查看",
      "Read-only updates unavailable on this connection; reopen Task center",
    ],
  };
  const label = labels[s.phase];
  const resume = ["paused", "error", "expired"].includes(s.phase);
  return (
    <section
      class="live-progress"
      data-live-state={s.phase}
      aria-label={tr(l, "进度更新", "Progress updates")}
    >
      <p class={s.phase === "error" ? "notice" : "note"} role="status">
        {tr(l, label[0], label[1])}
      </p>
      {s.lastConfirmed !== undefined && (
        <span class="note">
          {tr(l, "上次确认：", "Last confirmed: ")}
          {new Date(s.lastConfirmed).toLocaleTimeString(l, { hour12: false })}
        </span>
      )}
      {active && s.phase !== "unavailable" && (
        <button class="text-button live-toggle" onClick={toggle}>
          {tr(
            l,
            resume ? "继续更新" : "暂停更新",
            resume ? "Resume updates" : "Pause updates",
          )}
        </button>
      )}
    </section>
  );
}
