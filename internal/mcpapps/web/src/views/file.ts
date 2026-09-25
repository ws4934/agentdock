import { type Normalizer, text, number, group, tr } from "../model";
export const normalize: Normalizer = (d, l) => {
  if (
    !["action", "path", "affected_files", "diff_preview"].some((key) =>
      Object.hasOwn(d, key),
    )
  )
    throw new Error("Unexpected tool result shape");
  const preview = d.dry_run === true;
  const count = number(d.files_changed) || (d.changed === true ? 1 : 0);
  const diff = text(d.diff_preview, 20000);
  return {
    id: text(d.path ?? d.workdir) || "file",
    title: preview
      ? tr(l, "变更预览", "Change preview")
      : count
        ? tr(l, `已更新 ${count} 个文件`, `${count} files updated`)
        : tr(l, "文件操作结果", "File operation"),
    summary:
      text(d.summary) ||
      text(d.path ?? d.new_path) ||
      tr(l, "没有文件变更", "No file changes"),
    tone: preview ? "neutral" : "success",
    metrics: [
      `+${number(d.insertions)} −${number(d.deletions)}`,
      ...(preview ? [tr(l, "尚未写入", "Not written")] : []),
    ],
    diff,
    truncated:
      d.truncated === true || text(d.diff_preview, 20001).length > 20000,
    groups: [group("files", tr(l, "涉及文件", "Files"), d.affected_files)],
    empty: !count && !diff && !d.path,
  };
};
