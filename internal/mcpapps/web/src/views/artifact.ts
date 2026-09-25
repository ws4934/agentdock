import { type Normalizer, safeURL, text, number, tr } from "../model";
export const normalize: Normalizer = (d, l) => {
  if (!["artifact_id", "filename", "url"].some((key) => Object.hasOwn(d, key)))
    throw new Error("Unexpected tool result shape");
  const url = safeURL(d.url),
    size = number(d.size_bytes);
  const expires = text(d.expires_at);
  const privateDelivery = d.delivery === "private";
  return {
    id: text(d.artifact_id) || "artifact",
    title: text(d.filename) || tr(l, "文件已准备好", "File ready"),
    summary: [
      text(d.mime_type),
      size
        ? size / 1024 / 1024 >= 1
          ? `${(size / 1024 / 1024).toFixed(1)} MB`
          : `${Math.ceil(size / 1024)} KB`
        : "",
    ]
      .filter(Boolean)
      .join(" · "),
    tone: d.resource_readable === false ? "warning" : "success",
    metrics: privateDelivery
      ? [
          tr(
            l,
            d.resource_readable === false
              ? "文件超过认证资源读取上限"
              : "通过工具返回的认证文件资源打开",
            d.resource_readable === false
              ? "File exceeds authenticated resource read limit"
              : "Open the authenticated file resource in the tool result",
          ),
        ]
      : [],
    link: url ? { url, expires } : undefined,
    groups: [
      {
        id: "metadata",
        title: tr(l, "文件信息", "File information"),
        rows: [
          { id: "hash", title: "SHA-256", description: text(d.sha256) },
          {
            id: "expires",
            title: tr(l, "链接有效期", "Link expires"),
            description: expires,
          },
        ].filter((r) => r.description),
      },
    ],
    empty: !url && !d.filename,
  };
};
