import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const compiled = transformSync(readFileSync(new URL("./use-chat-data.ts", import.meta.url), "utf8"), {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;
const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const page = (start, count = 30, total = 100) => ({ total, results: Array.from({ length: count }, (_, index) => ({ id: start + index, publicID: `p${start + index}`, role: "user", content: "question" })) });

function harness() {
  const slots = [];
  let cursor = 0;
  let effects = [];
  let queued = [];
  let conversation = "a";
  let result;
  const requests = [];
  const translate = (key) => key;
  const memo = (factory, deps) => {
    const index = cursor++;
    const previous = slots[index];
    if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) {
      slots[index] = { value: factory(), deps };
    }
    return slots[index].value;
  };
  const hooks = {
    useRef(initial) { const index = cursor++; slots[index] ??= { current: initial }; return slots[index]; },
    useState(initial) {
      const index = cursor++;
      slots[index] ??= { value: typeof initial === "function" ? initial() : initial };
      return [slots[index].value, (update) => queued.push(() => { slots[index].value = typeof update === "function" ? update(slots[index].value) : update; })];
    },
    useMemo: memo,
    useCallback: (callback, deps) => memo(() => callback, deps),
    useEffect(effect, deps) {
      const index = cursor++;
      const previous = slots[index];
      if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) {
        effects.push(() => {
          previous?.cleanup?.();
          slots[index] = { deps, cleanup: effect() };
        });
      }
    },
  };
  const module = { exports: {} };
  new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()((name) => {
    if (name === "react") return hooks;
    if (name === "next-intl") return { useTranslations: () => translate };
    if (name === "@/shared/auth/resolve-access-token") return { resolveAccessToken: async () => "token" };
    if (name === "@/shared/api/conversation") return { listMessagesPage: (_token, id, options) => { const request = { ...deferred(), id, options }; requests.push(request); return request.promise; } };
    if (name === "@/features/chat/model/message-submit-recovery") return { RecoveredGenerationTracker: class { collect() { return []; } } };
    return {};
  }, module, module.exports);
  const render = (id = conversation) => {
    conversation = id;
    const updates = queued;
    queued = [];
    for (const update of updates) update();
    cursor = 0;
    effects = [];
    // biome-ignore lint/correctness/useHookAtTopLevel: This harness supplies the hook dispatcher and renders a complete hook tree.
    result = module.exports.useChatData(conversation);
    for (const effect of effects) effect();
    return result;
  };
  const settle = async () => {
    for (let index = 0; index < 8; index += 1) { await Promise.resolve(); render(); }
    return result;
  };
  return { render, settle, requests, current: () => result };
}

async function withBrowser(callback) {
  const previous = { window: globalThis.window, document: globalThis.document };
  globalThis.window = { addEventListener() {}, removeEventListener() {} };
  globalThis.document = { addEventListener() {}, removeEventListener() {} };
  try { await callback(); } finally { Object.assign(globalThis, previous); }
}

test("simultaneous older-history requests share a synchronous lock and report loaded rows before React commits", () => withBrowser(async () => {
  const h = harness();
  h.render();
  await h.settle();
  h.requests[0].resolve(page(71));
  await h.settle();
  const first = h.current().loadOlderMessages();
  const duplicate = h.current().loadOlderMessages();
  await h.settle();
  assert.equal(h.requests.length, 2);
  assert.equal(await duplicate, false);
  h.requests[1].resolve(page(41));
  assert.equal(await first, true);
  await h.settle();
  assert.equal(h.current().messages.length, 60);
  assert.equal(h.current().loadingOlder, false);
}));

test("an old conversation's late failure cannot unlock or overwrite a new conversation's history request", () => withBrowser(async () => {
  const h = harness();
  h.render(); await h.settle();
  h.requests[0].resolve(page(71)); await h.settle();
  const oldRequest = h.current().loadOlderMessages(); await h.settle();
  h.render("b"); await h.settle();
  h.requests[2].resolve(page(71)); await h.settle();
  const newRequest = h.current().loadOlderMessages(); await h.settle();
  h.requests[1].reject(Error("old offline read"));
  assert.equal(await oldRequest, false); await h.settle();
  assert.equal(h.current().conversationPublicID, "b");
  assert.equal(h.current().loadingOlder, true);
  h.requests[3].resolve(page(41));
  assert.equal(await newRequest, true); await h.settle();
  assert.equal(h.current().messages.length, 60);
}));

test("same-conversation reload discards an older page from the previous snapshot and permits retry after failure", () => withBrowser(async () => {
  const h = harness();
  h.render(); await h.settle();
  h.requests[0].resolve(page(71)); await h.settle();
  const oldRequest = h.current().loadOlderMessages(); await h.settle();
  h.current().reload(); await h.settle();
  h.requests[2].resolve(page(71)); await h.settle();
  h.requests[1].resolve(page(1));
  assert.equal(await oldRequest, false); await h.settle();
  assert.equal(h.current().messages[0].id, 71);
  const failed = h.current().loadOlderMessages(); await h.settle();
  h.requests[3].reject(Error("temporary offline read"));
  assert.equal(await failed, false); await h.settle();
  assert.equal(h.current().hasOlder, true);
  const retried = h.current().loadOlderMessages(); await h.settle();
  h.requests[4].resolve(page(41));
  assert.equal(await retried, true); await h.settle();
  assert.equal(h.current().messages[0].id, 41);
}));
