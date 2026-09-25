import assert from "node:assert/strict";
import test from "node:test";
import { LiveProgress, liveLeaseMS } from "../src/live-progress.ts";
import { normalize } from "../src/views/task.ts";
import { Store } from "../src/store.ts";

function fixture(read = async () => false) {
  let now = 0,
    next = 0,
    calls = 0;
  const timers = new Map(),
    states = [];
  const time = {
    now: () => now,
    set: (fn, delay) => {
      const id = ++next;
      timers.set(id, { fn, due: now + delay });
      return id;
    },
    clear: (id) => timers.delete(id),
  };
  const live = new LiveProgress(
    async (signal) => {
      calls++;
      return read(signal);
    },
    (s) => states.push(s),
    time,
  );
  const flush = async () => {
    for (let i = 0; i < 8; i++) await Promise.resolve();
  };
  const tick = async (ms) => {
    const end = now + ms;
    for (;;) {
      const entry = [...timers].sort((a, b) => a[1].due - b[1].due)[0];
      if (!entry || entry[1].due > end) break;
      now = entry[1].due;
      timers.delete(entry[0]);
      entry[1].fn();
      await flush();
    }
    now = end;
    await flush();
  };
  return {
    live,
    timers,
    states,
    tick,
    flush,
    calls: () => calls,
    state: () => states.at(-1),
  };
}

test("automatic compact reads use one timer, slow down unchanged results and stop at the lease", async () => {
  const f = fixture();
  f.live.configure("task", true, true, true);
  assert.equal(f.timers.size, 1);
  await f.tick(999);
  assert.equal(f.calls(), 0);
  await f.tick(1);
  assert.equal(f.calls(), 1);
  await f.tick(10000);
  assert.equal(f.calls(), 3);
  await f.tick(9999);
  assert.equal(f.calls(), 3);
  await f.tick(1);
  assert.equal(f.calls(), 4);
  await f.tick(liveLeaseMS);
  assert.equal(f.state().phase, "expired");
  assert.equal(f.timers.size, 0);
  const count = f.calls();
  await f.tick(liveLeaseMS);
  assert.equal(f.calls(), count);
  assert.ok(count < 70, `too many reads in a lease: ${count}`);
  f.live.toggle();
  await f.tick(0);
  assert.equal(f.calls(), count + 1);
  f.live.dispose();
  assert.equal(f.timers.size, 0);
});

test("hidden, paused, blocked, completed and unavailable states do not poll", async () => {
  const f = fixture();
  f.live.configure("task", true, true, false);
  await f.tick(30000);
  assert.equal(f.calls(), 0);
  assert.equal(f.state().phase, "hidden");
  f.live.configure("task", true, true, true);
  await f.tick(1000);
  assert.equal(f.calls(), 1);
  f.live.toggle();
  await f.tick(30000);
  assert.equal(f.calls(), 1);
  assert.equal(f.state().phase, "paused");
  await f.live.refresh();
  assert.equal(f.calls(), 2);
  assert.equal(f.state().phase, "paused");
  f.live.toggle();
  await f.tick(0);
  assert.equal(f.calls(), 3);
  f.live.configure("task", false, true, true);
  await f.tick(30000);
  assert.equal(f.calls(), 3);
  assert.equal(f.state().phase, "stopped");
  f.live.configure("task", true, false, true);
  await f.tick(30000);
  assert.equal(f.calls(), 3);
  await assert.rejects(f.live.refresh());
  assert.equal(f.state().phase, "unavailable");
  f.live.dispose();
});

test("one in-flight request, hide abort and late results cannot confirm stale state", async () => {
  let finish, signal;
  const f = fixture((s) => {
    signal = s;
    return new Promise((resolve) => {
      finish = resolve;
    });
  });
  f.live.configure("task", true, true, true);
  await f.tick(1000);
  await f.live.refresh();
  await f.tick(30000);
  assert.equal(f.calls(), 1);
  f.live.configure("task", true, true, false);
  assert.equal(signal.aborted, true);
  finish(true);
  await f.flush();
  assert.equal(f.state().lastConfirmed, undefined);
  assert.equal(f.timers.size, 0);
  f.live.configure("task", true, true, true);
  await f.tick(1000);
  assert.equal(f.calls(), 2);
  f.live.invalidate();
  assert.equal(signal.aborted, true);
  finish(true);
  await f.flush();
  assert.equal(f.state().lastConfirmed, undefined);
  f.live.dispose();
  assert.equal(f.timers.size, 0);
});

test("denied or invalid reads stop retries until explicit recovery", async () => {
  let reject = true;
  const f = fixture(async () => {
    if (reject) throw new Error("host denied");
    return true;
  });
  f.live.configure("task", true, true, true);
  await f.tick(1000);
  assert.equal(f.state().phase, "error");
  assert.equal(f.timers.size, 0);
  await f.tick(60000);
  assert.equal(f.calls(), 1);
  f.live.configure("task", true, true, false);
  f.live.configure("task", true, true, true);
  await f.tick(10000);
  assert.equal(f.calls(), 1);
  reject = false;
  f.live.toggle();
  await f.tick(0);
  assert.equal(f.calls(), 2);
  assert.equal(f.state().phase, "live");
  f.live.dispose();
});

test("identity changes and disposal cancel pending reads without publishing late state", async () => {
  let finish, signal;
  const f = fixture((s) => {
    signal = s;
    return new Promise((resolve) => {
      finish = resolve;
    });
  });
  f.live.configure("a", true, true, true);
  await f.tick(1000);
  f.live.configure("b", true, true, true);
  assert.equal(signal.aborted, true);
  finish(true);
  await f.flush();
  assert.equal(f.state().lastConfirmed, undefined);
  await f.tick(5000);
  assert.equal(f.calls(), 2);
  f.live.dispose();
  const count = f.states.length;
  assert.equal(signal.aborted, true);
  finish(true);
  await f.flush();
  await f.tick(60000);
  assert.equal(f.states.length, count);
  assert.equal(f.timers.size, 0);
});

test("task projections bind valid identities, compute full-task progress, and keep blocker visible", () => {
  const id = "tsk_0123456789abcdef",
    revision = "tsk1:" + "a".repeat(64);
  const task = {
    id,
    title: "Task",
    status: "active",
    revision,
    steps: [
      { id: "1", title: "Read", status: "completed" },
      { id: "2", title: "Build", status: "in_progress" },
    ],
  };
  const model = normalize({ task }, "en");
  assert.deepEqual(model.progress, { done: 1, total: 2 });
  assert.equal(model.live.revision, revision);
  assert.deepEqual(model.refresh, { action: "snapshot", task_id: id });
  assert.equal(
    normalize(
      { task: { ...task, status: "blocked", blocker: "Need a decision" } },
      "en",
    ).summary,
    "Need a decision",
  );
  assert.equal(
    normalize({ task: { ...task, status: "blocked" } }, "en").live.active,
    false,
  );
  assert.equal(
    normalize({ task: { ...task, archived_at: "now" } }, "en").live.active,
    false,
  );
  assert.equal(
    normalize({ task: { ...task, id: "wrong-id" } }, "en").live,
    undefined,
  );
  const store = new Store(normalize, "en");
  store.result({ structuredContent: { task } });
  store.setLive({ phase: "paused" });
  store.locale("zh-CN");
  assert.equal(store.get().live.phase, "paused");
  store.dispose();
  store.setLive({ phase: "live" });
  assert.equal(store.get().live, undefined);
});
