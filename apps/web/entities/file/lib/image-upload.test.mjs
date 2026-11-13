import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const compiled = transformSync(readFileSync(new URL("./image-upload.ts", import.meta.url), "utf8"), {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;
const version = "image-normalize-v1:1024";
const policy = { imageMaxDimension: 1024, imagePolicyVersion: version, allowedMIMETypes: ["image/jpeg", "image/png", "image/gif"], effectiveImageMaxBytes: 100, imageMaxBytes: 100, maxUploadFileBytes: 10 };
const sourceFile = (name = "source.png", size = 300, type = "image/png") => new File([new Uint8Array(size)], name, { type });
const serverFile = (id = "stored", mimeType = "image/jpeg", sizeBytes = 5) => ({ fileID: id, sha256: `sha-${id}`, mimeType, sizeBytes });
const deferred = () => { let resolve; let reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
const tick = async () => { for (let index = 0; index < 12; index += 1) await Promise.resolve(); };

function harness(options = {}) {
  const calls = { inspect: [], prepare: [], lookup: [], upload: [], policy: [], seed: [] };
  let revision = 1;
  let sessionID = options.sessionID ?? "";
  let nextHash = 0;
  const fingerprints = new WeakMap();
  class ImagePreparationError extends Error { constructor(code, message) { super(message); this.code = code; } }
  const api = {
    getChatFilePolicy: async (...args) => { calls.policy.push(args); return options.policy?.(...args) ?? policy; },
    lookupImageReuse: async (...args) => {
      calls.lookup.push(args);
      return options.lookup?.(...args) ?? { items: args[1].map((item) => ({ ...item, file: null })), imagePolicyVersion: version, quota: { usedBytes: 1 } };
    },
    uploadFile: async (...args) => { calls.upload.push(args); return options.upload?.(...args) ?? { file: serverFile(), quota: { usedBytes: 1 }, reused: false }; },
  };
  const preparation = {
    ImagePreparationError,
    inspectImageSource: async (source, signal) => {
      calls.inspect.push([source, signal]);
      if (options.inspect) return options.inspect(source, signal);
      if (!fingerprints.has(source)) fingerprints.set(source, (++nextHash).toString(16).padStart(64, "0"));
      return { sourceSha256: fingerprints.get(source), sourceSize: source.size, metadata: { mime: "image/png", animated: false } };
    },
    prepareImageSource: async (...args) => {
      calls.prepare.push(args);
      return options.prepare?.(...args) ?? { file: sourceFile("prepared.jpg", 5, "image/jpeg"), thumbnailBlob: new Blob(["tiny"]), metadata: args[1].metadata, policyVersion: args[2].version, reusedOriginal: false };
    },
  };
  const module = { exports: {} };
  new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()((name) => {
    if (name === "@/shared/api/file") return api;
    if (name === "@/shared/auth/session") return { readSessionID: () => sessionID, readSessionRevision: () => revision };
    if (name === "./image-preparation") return preparation;
    if (name === "./thumbnail-resource") return { seedThumbnail: (...args) => calls.seed.push(args) };
    throw Error(`Unexpected dependency ${name}`);
  }, module, module.exports);
  return { ...module.exports, calls, setRevision: (value) => { revision = value; }, setSessionID: (value) => { sessionID = value; } };
}

test("reuse hits never decode, prepare, upload or expose the original File as a preview", async () => {
  const stored = serverFile();
  const h = harness({ lookup: async (_token, items) => ({ items: items.map((item) => ({ ...item, file: stored })), imagePolicyVersion: version, quota: { usedBytes: 9 } }) });
  const result = await h.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(result.file, stored);
  assert.equal(result.reused, true);
  assert.equal(result.quota.usedBytes, 9);
  assert.equal(result.preparedFile, undefined);
  assert.equal(h.calls.prepare.length, 0);
  assert.equal(h.calls.upload.length, 0);
  assert.equal(h.calls.seed.length, 0);
});

test("misses upload only prepared bytes with source metadata and seed a small persistent thumbnail", async () => {
  const h = harness();
  const source = sourceFile();
  const result = await h.uploadPreparedFile("token", source, { policy, scope: "user:1", purpose: "chat" });
  assert.equal(h.calls.upload.length, 1);
  assert.equal(h.calls.upload[0][1], result.preparedFile);
  assert.notEqual(result.preparedFile, source);
  assert.equal(result.preparedFile.size, 5);
  assert.deepEqual(h.calls.upload[0][2].imageSource, { sourceSha256: "1".padStart(64, "0"), sourceSizeBytes: 300, imagePolicyVersion: version });
  assert.equal(h.calls.upload[0][2].purpose, "chat");
  assert.equal(h.calls.seed[0][0].fileID, "stored");
  assert.equal(h.calls.seed[0][0].contentVersion, undefined);
  assert.equal(result.contentVersion, undefined);
  assert.equal(h.calls.seed[0][1].size, 4);
  assert.equal(h.calls.seed[0][2], "user:1");
});

test("first upload and later reuse share the immutable persistent file identity", async () => {
  let lookups = 0;
  const h = harness({ lookup: async (_token, items) => ({ items: items.map((item) => ({ ...item, file: ++lookups > 1 ? serverFile() : null })), imagePolicyVersion: version, quota: {} }) });
  const first = await h.uploadPreparedFile("token", sourceFile(), { policy, scope: "user:1" });
  const reused = await h.uploadPreparedFile("token", sourceFile(), { policy, scope: "user:1" });
  assert.equal(first.file.fileID, reused.file.fileID);
  assert.equal(first.contentVersion, reused.contentVersion);
  assert.equal(reused.contentVersion, undefined);
  assert.deepEqual(h.calls.seed[0][0], { fileID: reused.file.fileID });
  assert.equal(h.calls.upload.length, 1);
});

test("a competing canonical upload returned as reused never exposes or seeds different local encoding", async () => {
  const canonical = serverFile("canonical", "image/png", 9);
  const h = harness({ upload: async () => ({ file: canonical, reused: true, quota: {} }) });
  const result = await h.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(result.file, canonical);
  assert.equal(result.reused, true);
  assert.equal(result.preparedFile, undefined);
  assert.equal(result.contentVersion, undefined);
  assert.equal(h.calls.prepare.length, 1);
  assert.equal(h.calls.seed.length, 0);
  const rejectedCanonical = harness({ upload: async () => ({ file: canonical, reused: true, quota: {} }) });
  await assert.rejects(rejectedCanonical.uploadPreparedFile("token", sourceFile(), { policy: { ...policy, allowedMIMETypes: ["image/jpeg"] } }), { code: "IMAGE_UNSUPPORTED" });
  assert.equal(rejectedCanonical.calls.seed.length, 0);
});

test("temporary preparation exposes stable local identity and seeds its thumbnail without network", async () => {
  const h = harness();
  const result = await h.prepareLocalImageFile(sourceFile(), { policy, scope: "temporary:1" });
  assert.equal(result.file, result.preparedFile);
  assert.ok(result.assetID.includes(result.contentVersion));
  assert.equal(h.calls.seed[0][0].assetID, result.assetID);
  assert.equal(h.calls.seed[0][0].localFile, result.file);
  assert.equal(h.calls.seed[0][2], "temporary:1");
  assert.equal(h.calls.lookup.length + h.calls.upload.length + h.calls.policy.length, 0);
});

test("fingerprints ready in one turn use one lookup; cancellation of one image leaves the other running", async () => {
  const pending = deferred();
  const h = harness({ lookup: () => pending.promise });
  const controller = new AbortController();
  const canceled = h.uploadPreparedFile("token", sourceFile("a.png"), { policy, signal: controller.signal });
  const remaining = h.uploadPreparedFile("token", sourceFile("b.png"), { policy });
  await tick();
  assert.equal(h.calls.lookup.length, 1);
  assert.equal(h.calls.lookup[0][1].length, 2);
  assert.equal(h.calls.lookup[0][2], undefined);
  controller.abort();
  await assert.rejects(canceled, { name: "AbortError" });
  pending.resolve({ items: h.calls.lookup[0][1].map((item) => ({ ...item, file: serverFile() })), imagePolicyVersion: version, quota: {} });
  assert.equal((await remaining).reused, true);
  assert.equal(h.calls.prepare.length + h.calls.upload.length, 0);
});

test("batch boundaries separate scopes and obey the server's 100-item limit", async () => {
  const h = harness();
  await Promise.all(Array.from({ length: 205 }, (_, index) => h.uploadPreparedFile("token", sourceFile(`${index}.png`), { policy })));
  assert.deepEqual(h.calls.lookup.map((args) => args[1].length), [100, 100, 5]);
  const other = harness();
  await Promise.all(["a", "b"].map((scope) => other.uploadPreparedFile("token", sourceFile(), { policy, scope })));
  assert.equal(other.calls.lookup.length, 2);
});

test("session changes invalidate in-flight results and shared policy cache", async () => {
  const pending = deferred();
  const h = harness({ lookup: () => pending.promise });
  const old = h.uploadPreparedFile("token", sourceFile());
  await tick();
  h.setRevision(2);
  pending.resolve({ items: [], imagePolicyVersion: version, quota: {} });
  await assert.rejects(old, { name: "AbortError" });
  await h.uploadPreparedFile("token", sourceFile());
  assert.equal(h.calls.policy.length, 2);
  assert.equal(h.calls.upload.length, 1);
  assert.equal(h.calls.seed.length, 1);
  assert.equal(h.calls.seed[0][2], "session:2");
});

test("access-token refresh in the same login survives policy, lookup, preparation and upload boundaries", async () => {
  for (const stage of ["policy", "lookup", "prepare", "upload"]) {
    let h;
    const refresh = async (...args) => {
      h.setRevision(2);
      if (stage === "policy") return policy;
      if (stage === "lookup") return { items: args[1].map((item) => ({ ...item, file: null })), imagePolicyVersion: version, quota: {} };
      if (stage === "prepare") return { file: sourceFile("prepared.jpg", 5, "image/jpeg"), thumbnailBlob: new Blob(["tiny"]), metadata: args[1].metadata, policyVersion: args[2].version, reusedOriginal: false };
      return { file: serverFile(), quota: {}, reused: false };
    };
    h = harness({ sessionID: "login-a", [stage]: refresh });
    const result = await h.uploadPreparedFile("old-token", sourceFile());
    assert.equal(result.file.fileID, "stored", stage);
    assert.equal(h.calls.seed[0][2], "session:login-a", stage);
    await h.uploadPreparedFile("refreshed-token", sourceFile());
    assert.equal(h.calls.policy.length, 2, `${stage}: policy cache follows new token/revision`);
  }
});

test("changing the login still rejects late results at every asynchronous upload boundary", async () => {
  for (const stage of ["policy", "lookup", "prepare", "upload"]) {
    let h;
    const changeLogin = async (...args) => {
      h.setSessionID("login-b");
      h.setRevision(2);
      if (stage === "policy") return policy;
      if (stage === "lookup") return { items: [], imagePolicyVersion: version, quota: {} };
      if (stage === "prepare") return { file: sourceFile("prepared.jpg", 5, "image/jpeg"), thumbnailBlob: new Blob(["tiny"]), metadata: args[1].metadata, policyVersion: args[2].version, reusedOriginal: false };
      return { file: serverFile(), quota: {}, reused: false };
    };
    h = harness({ sessionID: "login-a", [stage]: changeLogin });
    await assert.rejects(h.uploadPreparedFile("token", sourceFile()), { name: "AbortError" }, stage);
    assert.equal(h.calls.seed.length, 0, stage);
    assert.equal(h.calls.upload.length, stage === "upload" ? 1 : 0, stage);
  }
});

test("temporary preparation allows token rotation but discards results from a different login", async () => {
  for (const changeLogin of [false, true]) {
    let h;
    h = harness({ sessionID: "login-a", prepare: async (_source, inspection, normalized) => {
      h.setRevision(2);
      if (changeLogin) h.setSessionID("login-b");
      return { file: sourceFile("prepared.jpg", 5, "image/jpeg"), thumbnailBlob: new Blob(["tiny"]), metadata: inspection.metadata, policyVersion: normalized.version, reusedOriginal: false };
    } });
    const result = h.prepareLocalImageFile(sourceFile(), { policy });
    if (changeLogin) {
      await assert.rejects(result, { name: "AbortError" });
      assert.equal(h.calls.seed.length, 0);
    } else {
      assert.equal((await result).file.size, 5);
      assert.equal(h.calls.seed[0][2], "session:login-a");
    }
  }
});

test("policy reads are shared and a changed lookup version refreshes policy once before preparing", async () => {
  const h = harness({ lookup: async () => ({ items: [], imagePolicyVersion: "image-normalize-v1:512", quota: {} }), policy: async () => ({ ...policy, imageMaxDimension: 512, imagePolicyVersion: "image-normalize-v1:512" }) });
  await h.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(h.calls.lookup.length, 1);
  assert.equal(h.calls.policy.length, 1);
  assert.equal(h.calls.prepare[0][2].maxDimension, 512);
  assert.equal(h.calls.upload[0][2].imageSource.imagePolicyVersion, "image-normalize-v1:512");
  const shared = harness();
  await Promise.all([shared.uploadPreparedFile("token", sourceFile()), shared.uploadPreparedFile("token", sourceFile())]);
  assert.equal(shared.calls.policy.length, 1);
});

test("a concurrent batch shares one policy refresh and stale refreshed policy cannot write aliases", async () => {
  const changed = harness({ lookup: async () => ({ items: [], imagePolicyVersion: "image-normalize-v1:512", quota: {} }), policy: async () => ({ ...policy, imageMaxDimension: 512, imagePolicyVersion: "image-normalize-v1:512" }) });
  await Promise.all(Array.from({ length: 5 }, () => changed.uploadPreparedFile("token", sourceFile(), { policy })));
  assert.equal(changed.calls.lookup.length, 1);
  assert.equal(changed.calls.policy.length, 1);
  assert.ok(changed.calls.prepare.every((args) => args[2].maxDimension === 512));
  const stillStale = harness({ lookup: async () => ({ items: [], imagePolicyVersion: "image-normalize-v1:512", quota: {} }) });
  await stillStale.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(stillStale.calls.policy.length, 1);
  assert.equal(stillStale.calls.lookup.length, 1);
  assert.equal(stillStale.calls.upload[0][2].imageSource, undefined);
});

test("lookup service failure falls back to full prepared upload while 401/403 remain failures", async () => {
  const offline = harness({ lookup: async () => { throw Error("offline"); } });
  const result = await offline.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(result.reused, false);
  assert.equal(offline.calls.upload.length, 1);
  for (const status of [401, 403]) {
    const error = Object.assign(Error("denied"), { status });
    const denied = harness({ lookup: async () => { throw error; } });
    await assert.rejects(denied.uploadPreparedFile("token", sourceFile(), { policy }), (value) => value === error);
    assert.equal(denied.calls.prepare.length + denied.calls.upload.length, 0);
  }
});

test("output validation uses actual MIME and effective image category bytes after preparation", async () => {
  const allowed = harness({ prepare: async (_source, inspection, normalized) => ({ file: sourceFile("output.jpg", 50, "image/jpeg"), metadata: inspection.metadata, policyVersion: normalized.version, reusedOriginal: false }) });
  await allowed.uploadPreparedFile("token", sourceFile(), { policy });
  assert.equal(allowed.calls.upload[0][1].size, 50); // Image override may exceed the general default of 10.
  const wrongMime = harness();
  await assert.rejects(wrongMime.uploadPreparedFile("token", sourceFile(), { policy: { ...policy, allowedMIMETypes: ["image/png"] } }), { code: "IMAGE_UNSUPPORTED" });
  assert.equal(wrongMime.calls.upload.length, 0);
  const tooLarge = harness();
  await assert.rejects(tooLarge.uploadPreparedFile("token", sourceFile(), { policy: { ...policy, effectiveImageMaxBytes: 4 } }), { code: "IMAGE_MEMORY_LIMIT" });
  assert.equal(tooLarge.calls.upload.length, 0);
});

test("animated images default to an explicit failure; storage opt-in validates original bytes without aliases", async () => {
  const inspect = async (source) => ({ sourceSha256: "a".repeat(64), sourceSize: source.size, metadata: { mime: "image/gif", animated: true } });
  const h = harness({ inspect });
  const source = sourceFile("animated.gif", 20, "image/gif");
  await assert.rejects(h.uploadPreparedFile("token", source, { policy }), { code: "IMAGE_ANIMATED" });
  await h.uploadPreparedFile("token", source, { policy, allowAnimated: true });
  assert.equal(h.calls.lookup.length + h.calls.prepare.length, 0);
  assert.equal(h.calls.upload[0][1], source);
  assert.equal(h.calls.upload[0][2].imageSource, undefined);
  await assert.rejects(h.uploadPreparedFile("token", source, { policy: { ...policy, allowedMIMETypes: ["image/png"] }, allowAnimated: true }), { code: "IMAGE_UNSUPPORTED" });
});

test("legacy policy preserves dimensions and disables aliases; unsupported formats and invalid policy fail clearly", async () => {
  const h = harness();
  await h.uploadPreparedFile("token", sourceFile(), { policy: { ...policy, imageMaxDimension: undefined, imagePolicyVersion: undefined } });
  assert.equal(h.calls.lookup.length, 0);
  assert.equal(h.calls.prepare[0][2].maxDimension, 0);
  assert.equal(h.calls.upload[0][2].imageSource, undefined);
  for (const [name, type] of [["image.svg", "image/svg+xml"], ["image.heic", "image/heic"], ["image.unknown", "image/unknown"]]) {
    await assert.rejects(h.uploadPreparedFile("token", sourceFile(name, 30, type), { policy }), { code: "IMAGE_UNSUPPORTED" });
  }
  await assert.rejects(h.uploadPreparedFile("token", sourceFile(), { policy: { ...policy, imagePolicyVersion: "other:1024" } }), { code: "IMAGE_UNSUPPORTED" });
});

test("ordinary files keep the existing upload path without image inspection or policy reads", async () => {
  const h = harness();
  const source = sourceFile("notes.txt", 20, "text/plain");
  await h.uploadPreparedFile("token", source, { purpose: "chat" });
  assert.equal(h.calls.upload[0][1], source);
  assert.equal(h.calls.upload[0][2].purpose, "chat");
  assert.equal(h.calls.inspect.length + h.calls.prepare.length + h.calls.lookup.length + h.calls.policy.length, 0);
});

test("shared image classifier routes unsupported images to explicit preparation errors without throwing itself", () => {
  const h = harness();
  for (const [name, type] of [["photo.PNG", ""], ["photo.jpg", "application/octet-stream"], ["vector.svg", ""], ["photo.heic", "image/heic"], ["unknown.bin", "image/unknown"]]) {
    assert.equal(h.isImageUploadCandidate(sourceFile(name, 20, type)), true);
  }
  assert.equal(h.isImageUploadCandidate(sourceFile("notes.txt", 20, "text/plain")), false);
  assert.equal(h.isImageUploadCandidate(sourceFile("unknown.bin", 20, "application/octet-stream")), false);
});
