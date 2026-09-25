import { type Normalizer, object, text, group, tr, status } from "../model";
export const normalize: Normalizer = (d, l) => {
  if (
    !["recall", "card", "results", "recall_action", "written", "path"].some(
      (key) => Object.hasOwn(d, key),
    )
  )
    throw new Error("Unexpected tool result shape");
  const r = Object.keys(object(d.recall)).length
    ? object(d.recall)
    : Object.keys(object(d.card)).length
      ? object(d.card)
      : d;
  const preview = d.dry_run === true || d.written === false;
  const path = text(d.path ?? r.path);
  const warnings = Array.isArray(d.warnings)
    ? d.warnings
        .slice(0, 10)
        .map((v) => text(v, 200))
        .join(" · ")
    : "";
  return {
    id: text(r.id ?? d.recall_id ?? path) || "recall",
    title: text(r.title) || tr(l, "记忆结果", "Memory result"),
    summary:
      text(d.summary ?? r.summary ?? d.message) ||
      tr(
        l,
        preview
          ? "记忆变更预览，尚未写入"
          : d.written === true
            ? "记忆已更新"
            : "已收到记忆操作结果",
        preview
          ? "Memory change preview; not written"
          : d.written === true
            ? "Memory updated"
            : "Memory operation received",
      ),
    state: status(r.status, l),
    tone: preview ? "neutral" : "success",
    text: [text(r.content ?? r.body, 16000), warnings]
      .filter(Boolean)
      .join("\n\n"),
    diff: text(d.diff, 20000),
    truncated: d.truncated === true,
    metrics: [
      path,
      text(object(r.frontmatter).type ?? r.card_type ?? d.recall_target),
    ].filter(Boolean),
    groups: [group("results", tr(l, "记录", "Entries"), d.results)],
    empty: Array.isArray(d.results) && !d.results.length,
  };
};
