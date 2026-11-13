import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const compiled = transformSync(readFileSync(new URL("./use-chat-composer-state.ts", import.meta.url), "utf8"), {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;
const attachment = (fileID) => ({ fileID, fileName: `${fileID}.png`, mimeType: "image/png", sizeBytes: 5, previewURL: `blob:${fileID}` });

function harness() {
  const slots = [];
  let cursor = 0;
  let effects = [];
  let layoutEffects = [];
  let pendingEffects = [];
  let queued = [];
  let conversation = "a";
  let scope = "user:1";
  let result;
  const memo = (factory, deps) => {
    const index = cursor++;
    const previous = slots[index];
    if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) slots[index] = { value: factory(), deps };
    return slots[index].value;
  };
  const effect = (callback, deps, layout = false) => {
    const index = cursor++;
    const previous = slots[index];
    if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) (layout ? layoutEffects : effects).push(() => { previous?.cleanup?.(); slots[index] = { deps, cleanup: callback() }; });
  };
  const hooks = {
    useRef(initial) { const index = cursor++; slots[index] ??= { current: initial }; return slots[index]; },
    useState(initial) { const index = cursor++; slots[index] ??= { value: typeof initial === "function" ? initial() : initial }; return [slots[index].value, (update) => queued.push(() => { slots[index].value = typeof update === "function" ? update(slots[index].value) : update; })]; },
    useMemo: memo, useCallback: (callback, deps) => memo(() => callback, deps),
    useEffect: effect, useLayoutEffect: (callback, deps) => effect(callback, deps, true),
  };
  const module = { exports: {} };
  new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()((name) => {
    if (name === "react") return hooks;
    if (name === "@/shared/lib/type-guards") return { isRecord: (value) => typeof value === "object" && value !== null && !Array.isArray(value) };
    throw Error(`Unexpected dependency ${name}`);
  }, module, module.exports);
  const flushEffects = () => { const pending = pendingEffects; pendingEffects = []; for (const callback of pending) callback(); };
  const render = (id = conversation, storageScope = scope, deferEffects = false) => {
    flushEffects();
    conversation = id; scope = storageScope;
    const pending = queued; queued = []; for (const update of pending) update();
    cursor = 0; effects = []; layoutEffects = [];
    // biome-ignore lint/correctness/useHookAtTopLevel: This harness supplies the complete hook dispatcher.
    result = module.exports.useChatComposerState(conversation, { storageScope: scope });
    for (const callback of layoutEffects) callback();
    pendingEffects = effects;
    if (!deferEffects) flushEffects();
    return result;
  };
  const settle = () => { for (let index = 0; index < 8; index += 1) render(); return result; };
  return { render, settle, flushEffects, current: () => result };
}

test("a stale upload callback saves only its original conversation while preserving the active draft and stable file IDs", () => {
  const previousWindow = globalThis.window;
  const storage = new Map();
  const timers = new Map();
  let nextTimer = 0;
  globalThis.window = {
    localStorage: { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key) },
    addEventListener() {}, removeEventListener() {},
    setTimeout: (callback) => { const id = ++nextTimer; timers.set(id, callback); return id; }, clearTimeout: (id) => timers.delete(id),
  };
  try {
    const h = harness();
    h.render(); h.settle();
    h.current().setDraft("Draft for A");
    h.current().setAttachments([attachment("attached-a")]);
    h.settle();
    const oldAppend = h.current().appendAttachmentsForKey;
    const oldStatusUpdate = h.current().updateAttachmentStatuses;

    h.render("b"); h.settle();
    h.current().setDraft("Keep my current B draft");
    h.current().setAttachments([attachment("attached-b")]);
    h.settle();
    oldAppend("a", [attachment("late-upload")]);
    oldAppend("a", [attachment("late-upload")]);
    oldStatusUpdate(() => [attachment("stale-status")]);
    h.settle();
    assert.equal(h.current().draft, "Keep my current B draft");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["attached-b"]);

    h.render("a"); h.settle();
    assert.equal(h.current().draft, "Draft for A");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["attached-a", "late-upload"]);
    assert.ok(h.current().attachments.every((item) => item.previewURL === undefined && item.contentVersion === undefined));
    h.render("b"); h.settle();
    assert.equal(h.current().draft, "Keep my current B draft");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["attached-b"]);

    h.render("b", "user:2"); h.settle();
    oldAppend("a", [attachment("wrong-account")]); h.settle();
    h.render("a"); h.settle();
    assert.deepEqual(h.current().attachments, []);
    assert.equal(h.current().draft, "");
  } finally { globalThis.window = previousWindow; }
});

test("upload completion and navigation in the same render batch retain the new-chat attachment", () => {
  const previousWindow = globalThis.window;
  const storage = new Map();
  const timers = new Map();
  let nextTimer = 0;
  globalThis.window = {
    localStorage: { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key) },
    addEventListener() {}, removeEventListener() {},
    setTimeout: (callback) => { const id = ++nextTimer; timers.set(id, callback); return id; }, clearTimeout: (id) => timers.delete(id),
  };
  try {
    const h = harness();
    h.render(null); h.settle();
    h.current().setDraft("Unsent audio draft"); h.settle();
    h.current().appendAttachmentsForKey("__new__", [attachment("uploaded-audio")]);
    // Route changes can commit before the upload's setState gets its own render.
    h.render("b"); h.settle();
    assert.equal(h.current().attachments.length, 0);
    h.render(null); h.settle();
    assert.equal(h.current().draft, "Unsent audio draft");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["uploaded-audio"]);

    // Removing the reference must remain a replacement, never revive a saved file.
    h.current().setAttachments([]); h.settle();
    h.render("b"); h.settle();
    h.render(null); h.settle();
    assert.deepEqual(h.current().attachments, []);

    const append = h.current().appendAttachmentsForKey;
    h.current().setDraft("Pending snapshot"); h.settle();
    h.render("b", "user:1", true);
    append("__new__", [attachment("background-audio")]);
    h.flushEffects(); h.settle();
    // Return before debounce runs; hydration must read the completed upload.
    h.render(null); h.settle();
    assert.equal(h.current().draft, "Pending snapshot");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["background-audio"]);

    h.current().setDraft("Just edited");
    h.render(null, "user:1", true);
    h.current().appendAttachmentsForKey("__new__", [attachment("before-passive-effect")]);
    h.render("b"); h.settle();
    h.render(null); h.settle();
    assert.equal(h.current().draft, "Just edited");
    assert.deepEqual(h.current().attachments.map((item) => item.fileID), ["background-audio", "before-passive-effect"]);
  } finally { globalThis.window = previousWindow; }
});
