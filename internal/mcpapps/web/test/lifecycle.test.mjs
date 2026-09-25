import test from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import { normalize } from "../src/views/context.ts";
import { jsonPreview } from "../src/preview.ts";
import { normalize as normalizeDynamic } from "../src/views/dynamic.ts";

test("a mismatched tool envelope does not fabricate an empty capability result", () => {
  const store = new Store(normalize, "en");
  store.result({ structuredContent: { command_ok: true, exit_code: 0 } });
  assert.equal(store.get().phase, "error");
  assert.equal(store.get().model, undefined);
});
test("locale changes during cancellation cannot resurrect a previous success", () => {
  const store = new Store(normalize, "en");
  store.result({ structuredContent: { skills: [{ name: "test" }] } });
  store.phase("cancelled");
  store.locale("zh-CN");
  assert.equal(store.get().phase, "cancelled");
  assert.equal(store.get().model, undefined);
});
test("third-party JSON preview bounds text and survives recursive values", () => {
  const value = {
    large: "x".repeat(1000000),
    items: Array.from({ length: 10000 }, (_, id) => ({ id })),
  };
  value.self = value;
  const preview = jsonPreview(value);
  assert.ok(preview.length <= 18000);
  assert.ok(preview.includes("[…]"));
});

test("disposed stores release snapshots and reject new subscriptions", () => {
  const store = new Store(normalize, "en");
  store.result({ structuredContent: { skills: [{ name: "test" }] } });
  assert.ok(store.get().model);
  store.dispose();
  assert.equal(store.get().model, undefined);
  let calls = 0;
  store.subscribe(() => calls++);
  store.result({ structuredContent: { skills: [{ name: "later" }] } });
  store.phase("ready");
  assert.equal(calls, 0);
  assert.equal(store.get().phase, "empty");
});

test("dynamic MCP renders relocated outer content without retaining its envelope", () => {
  const store = new Store(normalizeDynamic, "en");
  const envelope = {
    content: [{ type: "text", text: "exact-upstream-result" }],
    structuredContent: {
      name: "fixture:read",
      result: {
        content_location: "mcp.content",
        structuredContent: { answer: 42 },
      },
    },
  };
  store.result(envelope);
  assert.equal(store.get().phase, "ready");
  assert.ok(
    JSON.stringify(store.get().model).includes("exact-upstream-result"),
  );
  assert.equal(envelope.structuredContent.result.content, undefined);
});
