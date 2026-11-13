import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const compile = (path) => transformSync(readFileSync(new URL(path, import.meta.url), "utf8"), {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;
const hookSource = compile("./use-chat-attachments.ts");
const bulkSource = compile("../../../shared/lib/bulk-action.ts");
const load = (source, dependencies) => {
  const module = { exports: {} };
  new Script(`(function(require, module, exports) { ${source}\n})`).runInThisContext()((name) => {
    if (name in dependencies) return dependencies[name];
    throw Error(`Unexpected dependency ${name}`);
  }, module, module.exports);
  return module.exports;
};
const deferred = () => { let resolve; const promise = new Promise((yes) => { resolve = yes; }); return { promise, resolve }; };
const sourceFile = () => new File(["image"], "image.png", { type: "image/png" });
const attachment = (fileID, previewURL) => ({ fileID, fileName: `${fileID}.png`, mimeType: "image/png", sizeBytes: 5, previewURL });
const uploaded = (fileID = "B") => ({ file: { ...attachment(fileID), processingReady: true }, reused: false, quota: {}, preparedFile: sourceFile() });

function harness({ existing = [], upload = async () => uploaded() } = {}) {
  const slots = [];
  let cursor = 0;
  let effects = [];
  let queued = [];
  let props;
  let result;
  let session = "session-a";
  const calls = { upload: [], append: [], revoked: [], created: [] };
  const stable = (key) => key;
  const memo = (factory, deps) => {
    const index = cursor++;
    const previous = slots[index];
    if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) slots[index] = { value: factory(), deps };
    return slots[index].value;
  };
  const hooks = {
    useRef(initial) { const index = cursor++; slots[index] ??= { current: initial }; return slots[index]; },
    useState(initial) { const index = cursor++; slots[index] ??= { value: typeof initial === "function" ? initial() : initial }; return [slots[index].value, (update) => queued.push(() => { slots[index].value = typeof update === "function" ? update(slots[index].value) : update; })]; },
    useMemo: memo, useCallback: (callback, deps) => memo(() => callback, deps),
    useEffect(effect, deps) {
      const index = cursor++;
      const previous = slots[index];
      if (!previous || deps.some((value, offset) => !Object.is(value, previous.deps[offset]))) effects.push(() => { previous?.cleanup?.(); slots[index] = { deps, cleanup: effect() }; });
    },
  };
  hooks.useLayoutEffect = hooks.useEffect;
  const toast = { error() {}, success() {} };
  const bulk = load(bulkSource, { sonner: { toast } });
  const dependencies = {
    react: hooks, "next-intl": { useTranslations: () => stable }, sonner: { toast },
    "@/features/chat/hooks/use-chat-native-readiness": { useChatNativeReadiness: () => ({}) },
    "@/features/chat/utils/attachments": { inferUploadCategory: () => "image", resolveUploadPolicyRejection: () => null },
    "@/features/chat/utils/browser-media": {}, "@/features/chat/utils/chat-runtime": { resolveMaxFilesPerMessage: () => 10 },
    "@/i18n/use-localized-error": { useLocalizedErrorMessage: () => stable },
    "@/shared/api/file": { getChatFilePolicy: async () => ({ maxMessageFiles: 10 }) },
    "@/shared/auth/resolve-access-token": { resolveAccessToken: async () => "token" },
    "@/shared/auth/auth-session-context": { useOptionalAuthSession: () => ({ user: { id: 1 } }) },
    "@/shared/auth/session": { readSessionID: () => session },
    "@/entities/file": { isFileProcessing: () => false, useFileProcessingStatusPolling() {}, thumbnailScopeForUser: (id) => `user:${id}`, isImagePreparationError: () => false,
      uploadPreparedFile: async (...args) => { calls.upload.push(args); return upload(...args); } },
    "@/shared/lib/bulk-action": bulk, "@/shared/lib/secure-id": { createSecureUUID: () => "uuid" },
  };
  const module = load(hookSource, dependencies);
  props = { conversationKey: "a", conversationID: "a", model: "model", selectedToolIDs: [], attachments: existing, setAttachments() {}, updateAttachmentStatuses() {}, appendAttachmentsForKey: (...args) => calls.append.push(args) };
  const render = (updates = {}) => {
    props = { ...props, ...updates };
    const pending = queued; queued = []; for (const update of pending) update();
    cursor = 0; effects = [];
    // biome-ignore lint/correctness/useHookAtTopLevel: This harness supplies the complete hook dispatcher.
    result = module.useChatAttachments(props);
    for (const effect of effects) effect();
    return result;
  };
  const settle = async () => { for (let index = 0; index < 10; index += 1) { await Promise.resolve(); render(); } return result; };
  return { render, settle, calls, current: () => result, setSession: (value) => { session = value; }, unmount: () => { for (const slot of slots) slot?.cleanup?.(); } };
}

async function withURLs(callback) {
  const previous = { create: URL.createObjectURL, revoke: URL.revokeObjectURL };
  const urls = { created: [], revoked: [] };
  URL.createObjectURL = () => { const url = `blob:test-${urls.created.length}`; urls.created.push(url); return url; };
  URL.revokeObjectURL = (url) => urls.revoked.push(url);
  try { await callback(urls); } finally { URL.createObjectURL = previous.create; URL.revokeObjectURL = previous.revoke; }
}

test("two upload calls from the same committed render start only one batch", () => withURLs(async () => {
  const pending = deferred();
  const h = harness({ upload: () => pending.promise });
  h.render(); await h.settle();
  const callback = h.current().onUploadFiles;
  const first = callback([sourceFile()]);
  const second = callback([sourceFile()]);
  await h.settle();
  assert.deepEqual(await second, []);
  assert.equal(h.calls.upload.length, 1);
  pending.resolve(uploaded());
  const result = await first;
  assert.equal(result[0].fileID, "B");
  assert.equal(h.calls.append.length, 1);
}));

test("reselecting attached B returns the existing usable B object for replacing A and never returns a revoked preview", () => withURLs(async (urls) => {
  const existingB = attachment("B", "blob:existing-B");
  const h = harness({ existing: [attachment("A", "blob:existing-A"), existingB] });
  h.render(); await h.settle();
  const result = await h.current().onUploadFiles([sourceFile()]);
  assert.equal(result.length, 1);
  assert.equal(result[0], existingB);
  assert.equal(result[0].contentVersion, undefined);
  assert.ok(!urls.revoked.includes(result[0].previewURL));
  assert.ok(urls.revoked.includes(urls.created[0]));
  assert.ok(h.calls.append.every(([, items]) => items.length === 0));
}));

test("unmount or account change during upload rejects late results and releases their preview URLs", () => withURLs(async (urls) => {
  for (const reason of ["unmount", "session"]) {
    const pending = deferred();
    const h = harness({ upload: () => pending.promise });
    h.render(); await h.settle();
    const operation = h.current().onUploadFiles([sourceFile()]);
    await h.settle();
    if (reason === "unmount") h.unmount(); else h.setSession("session-b");
    pending.resolve(uploaded());
    assert.deepEqual(await operation, []);
    assert.equal(h.calls.append.length, 0);
  }
  assert.deepEqual(urls.revoked, urls.created);
}));

test("an upload completed after switching conversations appends only to its original key and returns no revoked URL", () => withURLs(async (urls) => {
  const pending = deferred();
  const h = harness({ upload: () => pending.promise });
  h.render(); await h.settle();
  const operation = h.current().onUploadFiles([sourceFile()]);
  await h.settle();
  h.render({ conversationKey: "b", conversationID: "b", attachments: [attachment("current-b")] });
  await h.settle();
  pending.resolve(uploaded("old-a"));
  const result = await operation;
  assert.equal(h.calls.append.length, 1);
  assert.equal(h.calls.append[0][0], "a");
  assert.equal(h.calls.append[0][1][0].fileID, "old-a");
  assert.equal(result[0].previewURL, undefined);
  assert.deepEqual(urls.revoked, urls.created);
}));
