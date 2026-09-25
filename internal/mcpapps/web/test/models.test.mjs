import test from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import { safeURL, records, text } from "../src/model.ts";
import { normalize as context } from "../src/views/context.ts";
import { normalize as task } from "../src/views/task.ts";
import { normalize as file } from "../src/views/file.ts";
import { normalize as artifact } from "../src/views/artifact.ts";
import { normalize as acp } from "../src/views/acp.ts";
import { normalize as recall } from "../src/views/recall.ts";
import { normalize as workflow } from "../src/views/workflow.ts";
import { normalize as dynamic } from "../src/views/dynamic.ts";

const fixture = {
  skills: [{ name: "one", description: "First" }],
  dynamic_mcp: [],
};
test("duplicate result projection emits only once", () => {
  const s = new Store(context, "zh-CN");
  let count = 0;
  s.subscribe(() => count++);
  for (let i = 0; i < 1000; i++)
    s.result({ structuredContent: { ...fixture, ignored: i } });
  assert.equal(count, 1);
  assert.equal(s.get().phase, "ready");
});
test("error and cancellation replace success instead of retaining stale results", () => {
  const s = new Store(context, "zh-CN");
  s.result({ structuredContent: fixture });
  s.result({ isError: true, structuredContent: { error: "failure" } });
  assert.equal(s.get().phase, "error");
  assert.equal(s.get().model, undefined);
  s.result({ structuredContent: fixture });
  s.phase("cancelled", "cancel");
  assert.equal(s.get().phase, "cancelled");
  assert.equal(s.get().model, undefined);
});
test("empty, content-only JSON and plain text have terminal representations", () => {
  const s = new Store(context, "en");
  s.result({ content: [] });
  assert.equal(s.get().phase, "empty");
  s.result({ content: [{ type: "text", text: JSON.stringify(fixture) }] });
  assert.equal(s.get().phase, "ready");
  assert.equal(s.get().model.title, "Capabilities");
  s.result({ content: [{ type: "text", text: "A text-only reply" }] });
  assert.equal(s.get().model.text, "A text-only reply");
});
test("locale changes reproject without keeping stale language", () => {
  const s = new Store(context, "zh-CN");
  s.result({ structuredContent: fixture });
  s.locale("en");
  assert.equal(s.get().model.title, "Capabilities");
  s.locale("zh-CN");
  assert.equal(s.get().model.title, "能力概览");
});
test("dispose removes subscriptions and ignores late replies", () => {
  const s = new Store(context, "en");
  let count = 0;
  s.subscribe(() => count++);
  s.dispose();
  s.result({ structuredContent: fixture });
  s.phase("ready");
  assert.equal(count, 0);
});
test("malformed renderer shows visible failure", () => {
  const s = new Store(() => {
    throw Error("bad payload");
  }, "en");
  s.result({ structuredContent: { x: 1 } });
  assert.equal(s.get().phase, "error");
});
test("URL schemes, credentials and oversize values are rejected", () => {
  for (const value of [
    "javascript:alert(1)",
    "data:text/html,x",
    "file:///etc/passwd",
    "https://user:pass@example.test",
    "https://x/" + "x".repeat(8200),
  ])
    assert.equal(safeURL(value), undefined);
  assert.equal(safeURL("https://example.test/a"), "https://example.test/a");
});
test("bounded projections and rendering preserve untrusted text", () => {
  assert.equal(
    records(Array.from({ length: 1000 }, () => ({ name: "x" }))).length,
    200,
  );
  assert.equal(text("x".repeat(4000)).length, 2000);
  const m = context({ skills: [{ name: "<script>alert(1)</script>" }] }, "en");
  assert.equal(m.groups[0].rows[0].title, "<script>alert(1)</script>");
});
test("all eight view projections support their business envelope", () => {
  assert.equal(context(fixture, "zh-CN").title, "能力概览");
  const m = task(
    {
      task_summary: {
        id: "a",
        title: "Work",
        step_count: 4,
        completed_step_count: 2,
        steps: [{ id: "1", title: "Step", status: "in_progress" }],
      },
    },
    "en",
  );
  assert.deepEqual(m.progress, { done: 2, total: 4 });
  assert.equal(m.id, "a");
  assert.equal(
    file({ dry_run: true, files_changed: 1, diff_preview: "+a" }, "en").title,
    "Change preview",
  );
  assert.equal(
    artifact({ filename: "file", url: "https://example.test" }, "en").link.url,
    "https://example.test/",
  );
  assert.equal(
    acp(
      {
        session: { id: "a" },
        messages: [
          { role: "system", content: "hidden" },
          { role: "assistant", content: "visible" },
        ],
      },
      "en",
    ).groups[0].rows.length,
    1,
  );
  assert.equal(
    recall({ recall: { id: "r", content: "memory" } }, "en").text,
    "memory",
  );
  assert.equal(workflow({ templates: [] }, "en").empty, true);
  assert.equal(
    dynamic({ result: { isError: true, content: [] } }, "en").tone,
    "error",
  );
});
test("progress clamps invalid and out-of-range counts", () => {
  assert.deepEqual(
    task({ task_summary: { step_count: 2, completed_step_count: 8 } }, "en")
      .progress,
    { done: 2, total: 2 },
  );
  assert.equal(
    task({ task_summary: { step_count: NaN } }, "en").progress,
    undefined,
  );
});
