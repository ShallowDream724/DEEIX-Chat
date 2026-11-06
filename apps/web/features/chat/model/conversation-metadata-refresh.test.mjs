import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "@/shared/api/conversation") {
      return { url: `data:text/javascript,${encodeURIComponent("export const getConversation = (...args) => globalThis.__metadataRead(...args);")}`, shortCircuit: true };
    }
    if (specifier === "@/shared/api/http-client") {
      return { url: `data:text/javascript,${encodeURIComponent("export class ApiError extends Error { constructor(message, status) { super(message); this.status = status; } }")}`, shortCircuit: true };
    }
    return nextResolve(specifier, context);
  },
});
const { refreshGeneratedConversationMetadata } = await import("./conversation-metadata-refresh.ts");
const { ApiError } = await import("@/shared/api/http-client");

async function fakeTime(work) {
  const originalNow = Date.now;
  const originalWindow = globalThis.window;
  let now = 0;
  Date.now = () => now;
  globalThis.window = { setTimeout: (callback, ms) => { now += ms; queueMicrotask(callback); } };
  try { await work(() => now, (ms) => { now += ms; }); }
  finally { Date.now = originalNow; globalThis.window = originalWindow; delete globalThis.__metadataRead; }
}

test("a title completed after 90 seconds is still read and duplicate watchers share the same work", async () => {
  await fakeTime(async (now) => {
    const patches = [];
    let reads = 0;
    globalThis.__metadataRead = async (_token, _id, signal) => {
      reads++;
      assert.ok(signal instanceof AbortSignal);
      return { title: now() > 90_000 ? "生成完成的标题" : "新对话", labelsJSON: "[]" };
    };
    const args = ["test-token", "conversation", { title: "新对话" }, false, "", (_id, patch) => patches.push(patch)];
    const first = refreshGeneratedConversationMetadata(...args);
    const second = refreshGeneratedConversationMetadata(...args);
    assert.equal(first, second);
    await first;
    assert.equal(patches.length, 1);
    assert.equal(patches[0].title, "生成完成的标题");
    assert.ok(now() > 90_000 && reads < 20);
  });
});

test("failed reads remain bounded and account for time spent in the request", async () => {
  await fakeTime(async (now, advance) => {
    let reads = 0;
    globalThis.__metadataRead = async () => { reads++; advance(5000); throw Error("offline"); };
    await refreshGeneratedConversationMetadata("test-token", "failed", null, false, "", () => assert.fail("no fabricated metadata"));
    assert.ok(reads > 0 && reads <= 48);
    assert.ok(now() <= 605_000);
  });
});

test("labels-only preference stops after labels arrive without waiting for a disabled title", async () => {
  await fakeTime(async () => {
    let reads = 0;
    globalThis.__metadataRead = async () => { reads++; return { title: "新对话", labelsJSON: '["研究"]' }; };
    await refreshGeneratedConversationMetadata("test-token", "labels-only", { title: "新对话", labelsJSON: "[]" }, true, "", () => {}, false);
    assert.equal(reads, 1);
  });
});

test("deleted conversations and lost access stop metadata polling", async () => {
  await fakeTime(async () => {
    for (const status of [401, 403, 404]) {
      let reads = 0;
      globalThis.__metadataRead = async () => { reads++; throw new ApiError("unavailable", status); };
      await refreshGeneratedConversationMetadata("test-token", `denied-${status}`, null, true, "", () => assert.fail("no metadata"));
      assert.equal(reads, 1);
    }
  });
});
