import assert from "node:assert/strict";
import test from "node:test";
import { normalize } from "../src/views/file.ts";
import { Store } from "../src/store.ts";

for (const [action, zh, en] of [
  ["add", "已新增", "added"],
  ["replace", "已更新", "updated"],
  ["patch", "已更新", "updated"],
  ["move", "已移动", "moved"],
  ["delete", "已删除", "deleted"],
]) {
  test(`automatic ${action} card keeps its action, paths and bounded diff`, () => {
    const data = {
      action,
      path: "src/example.go",
      files_changed: 1,
      changed: true,
      diff_preview: "-before\n+after",
      affected_files: [{ path: "src/example.go", operation: action }],
    };
    const model = normalize(data, "zh-CN");
    assert.ok(model.title.startsWith(zh));
    assert.equal(model.tone, "success");
    assert.equal(model.diff, data.diff_preview);
    assert.equal(model.groups[0].rows[0].path, data.path);
    assert.ok(normalize(data, "en").title.includes(en));
    const preview = normalize({ ...data, dry_run: true }, "zh-CN");
    assert.equal(preview.title, "变更预览");
    assert.equal(preview.tone, "neutral");
    assert.ok(preview.metrics.includes("尚未写入"));
  });
}

test("no-op file edit never claims files were written", () => {
  const model = normalize(
    { action: "replace", path: "note.txt", files_changed: 0, changed: false },
    "zh-CN",
  );
  assert.equal(model.title, "没有文件变更");
  assert.equal(model.tone, "neutral");
});

test("errors replace file success, duplicates do not update DOM state and disposal releases data", () => {
  const store = new Store(normalize, "en");
  let updates = 0;
  store.subscribe(() => updates++);
  const envelope = {
    structuredContent: {
      action: "patch",
      files_changed: 2,
      diff_preview: "+line\n".repeat(10000),
    },
  };
  store.result(envelope);
  const first = updates;
  for (let i = 0; i < 100; i++) store.result(envelope);
  assert.equal(updates, first);
  assert.ok(store.get().model.diff.length <= 20000);
  assert.equal(store.get().model.truncated, true);
  store.result({
    isError: true,
    structuredContent: {
      error: "File changed; reread required",
      code: "CONFLICT",
    },
  });
  assert.equal(store.get().phase, "error");
  assert.equal(store.get().model, undefined);
  store.dispose();
  store.result(envelope);
  assert.equal(store.get().model, undefined);
});
