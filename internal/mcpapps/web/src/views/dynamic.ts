import {
  type Normalizer,
  object,
  text,
  records,
  resultText,
  group,
  tr,
} from "../model";
import { jsonPreview } from "../preview";
export const normalize: Normalizer = (d, l) => {
  if (!["result"].some((key) => Object.hasOwn(d, key)))
    throw new Error("Unexpected tool result shape");
  const r = object(d.result),
    body = resultText(r.content) || text(r.message);
  return {
    id: text(d.name) || "mcp",
    title: text(d.name) || "MCP",
    summary:
      r.isError === true
        ? tr(l, "外部工具执行失败", "External tool failed")
        : tr(l, "已收到工具结果", "Tool result received"),
    tone: r.isError === true ? "error" : "success",
    text: body || (r.structuredContent ? jsonPreview(r.structuredContent) : ""),
    truncated: body.length >= 18000 || !!r.structuredContent,
    groups: [
      group(
        "attachments",
        tr(l, "附加内容", "Attachments"),
        records(r.content)
          .filter((p) => p.type !== "text")
          .map((p) => ({
            name: p.name ?? p.uri ?? p.type,
            description: p.mimeType,
          })),
      ),
    ],
    empty: !body && !r.structuredContent && !records(r.content).length,
  };
};
