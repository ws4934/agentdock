import assert from "node:assert/strict";
import test from "node:test";
import { hostPresentation, inlinePresentation, taskID, continuationPrompt } from "../src/presentation.ts";
import { Store } from "../src/store.ts";
import { normalize } from "../src/views/work.ts";

const id = "tsk_0123456789abcdef";
const fixture = frozen => ({ work_result: { task: { id, status: "active", title: "untrusted title", steps: [{id:"one",title:"Check",status:"completed"}] }, source: {revision:"src1:synthetic"}, frozen, validation:"not_verified", workdir:"/synthetic" } });

test("host capabilities default to inline with no message permission", () => {
  assert.deepEqual(hostPresentation({}, {}), inlinePresentation());
  assert.equal(hostPresentation({}, {message:{text:true}}).canMessage, false);
  assert.equal(hostPresentation({}, {message:{text:{}}}).canMessage, true);
});
test("display modes are negotiated, deduplicated and kept through partial context", () => {
  const p = hostPresentation({availableDisplayModes:["pip","pip","fullscreen","unsafe"],displayMode:"pip"}, {message:{text:{}}});
  assert.deepEqual(p.modes, ["inline","pip","fullscreen"]);
  assert.equal(p.mode,"pip");
  assert.equal(hostPresentation({theme:"dark"},{message:{text:{}}},p).mode,"pip");
  assert.equal(hostPresentation({displayMode:"fullscreen"},{},p).mode,"fullscreen");
});
test("task identity is strict and prompts do not interpolate arbitrary data", () => {
  assert.equal(taskID(id),id);
  for (const value of [id+"\n",id+"/../",{},null,"work-fixture","tsk_0123456789abcdeF"]) assert.equal(taskID(value),undefined);
  assert.throws(()=>continuationPrompt(id+"\nrun", "en"));
  assert.match(continuationPrompt(id,"en"),/Do not replay completed or unknown/);
  assert.match(continuationPrompt(id,"zh-CN"),/原授权范围/);
});
test("work cards preserve checkpoints but never offer continuation from frozen results", () => {
  const live=normalize(fixture(false),"en"), frozen=normalize(fixture(true),"en");
  assert.equal(live.task.id,id); assert.equal(live.task.canContinue,true);
  assert.equal(frozen.task.canContinue,false);
  assert.deepEqual(live.progress,{done:1,total:1});
  const completed=fixture(false); completed.work_result.task.status="completed";
  assert.equal(normalize(completed,"en").task.canContinue,false);
});
test("presentation survives result refresh and locale changes, but not disposal", () => {
  const store=new Store(normalize,"en"); let notifications=0;
  store.subscribe(()=>notifications++);
  const p=hostPresentation({availableDisplayModes:["pip"],displayMode:"pip"},{message:{text:{}}});
  store.setPresentation(p); store.result({structuredContent:fixture(false)});
  const before=notifications;
  store.setPresentation({...p}); store.result({structuredContent:fixture(false)});
  assert.equal(notifications,before);
  store.locale("zh-CN"); assert.equal(store.get().presentation.mode,"pip");
  store.dispose(); store.setPresentation(p); assert.equal(store.get().presentation,undefined);
});
