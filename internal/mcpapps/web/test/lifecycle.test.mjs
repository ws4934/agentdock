import test from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import { normalize } from "../src/views/context.ts";
import { jsonPreview } from "../src/preview.ts";

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
