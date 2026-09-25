import { type Normalizer, text, group, tr } from "../model";
export const normalize: Normalizer = (d, l) => {
  if (
    !["action", "candidates", "templates", "template", "items"].some((key) =>
      Object.hasOwn(d, key),
    )
  )
    throw new Error("Unexpected tool result shape");
  const list = d.candidates ?? d.templates ?? d.items;
  return {
    id: "workflow",
    title: tr(l, "工作流", "Workflows"),
    summary:
      text(d.recommendation_reason ?? d.summary ?? d.message) ||
      tr(l, "可用的工作流结果", "Available workflow results"),
    groups: [group("workflows", tr(l, "工作流", "Workflows"), list)],
    empty: Array.isArray(list) && !list.length,
  };
};
