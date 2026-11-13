import assert from "node:assert/strict";
import test from "node:test";
import { createThumbnailResourceStore } from "./thumbnail-resource.ts";

function fixture(options = {}) {
  let generated = 0;
  let created = 0;
  const revoked = [];
  const store = createThumbnailResourceStore({
    generate: async (load, signal) => { generated++; return load(signal); },
    createURL: () => `blob:thumbnail-${++created}`,
    revokeURL: url => revoked.push(url),
    ...options,
  });
  return { store, revoked, generated: () => generated, created: () => created };
}

test("concurrent consumers share one job; releasing one does not abort the other or a quick remount", async () => {
  const f = fixture();
  let finish;
  let loadSignal;
  const load = signal => { loadSignal = signal; return new Promise(resolve => { finish = resolve; }); };
  const first = f.store.acquire({ fileID: "file-1" }, "user:1", load);
  const second = f.store.acquire({ fileID: "file-1" }, "user:1", load);
  const cancelled = assert.rejects(first.promise, { name: "AbortError" });
  first.release();
  assert.equal(loadSignal.aborted, false);
  finish(new Blob(["small"]));
  const url = await second.promise;
  await cancelled;
  assert.equal(f.generated(), 1);
  second.release();
  assert.deepEqual(f.revoked, []);
  const remounted = f.store.acquire({ fileID: "file-1" }, "user:1", load);
  assert.equal(await remounted.promise, url);
  assert.equal(f.generated(), 1);
  assert.equal(f.created(), 1);
  remounted.release();
  f.store.clear();
  assert.deepEqual(f.revoked, [url]);
});

test("temporary retry and edit IDs and object URLs do not change stable asset or File identity", async () => {
  const f = fixture();
  const localFile = new Blob(["source"]);
  f.store.seed({ localFile }, new Blob(["seed"]), "user:1");
  const first = f.store.acquire({ fileID: "temporary_first", assetID: "asset-1", localFile, previewURL: "blob:first" }, "user:1", async () => { throw new Error("must use seed"); });
  const url = await first.promise;
  first.release();
  const retried = f.store.acquire({ fileID: "temporary_retry", assetID: "asset-1", previewURL: "blob:retry" }, "user:1", async () => { throw new Error("must use seed"); });
  assert.equal(await retried.promise, url);
  retried.release();
  const fileOnly = f.store.acquire({ fileID: "temporary_edit", localFile }, "user:1", async () => { throw new Error("must use seed"); });
  assert.equal(await fileOnly.promise, url);
  fileOnly.release();
  assert.equal(f.generated(), 0);
  f.store.clear();
});

test("persistent identity takes precedence over local File, while content version and thumbnail spec isolate results", async () => {
  const f = fixture();
  const load = async () => new Blob(["small"]);
  const first = f.store.acquire({ fileID: "server-file", localFile: new Blob(), contentVersion: "v1" }, "user:1", load);
  const url = await first.promise;
  first.release();
  const same = f.store.acquire({ fileID: "server-file", localFile: new Blob(), contentVersion: "v1" }, "user:1", load);
  assert.equal(await same.promise, url);
  same.release();
  const version = f.store.acquire({ fileID: "server-file", contentVersion: "v2" }, "user:1", load);
  assert.notEqual(await version.promise, url);
  const spec = f.store.acquire({ fileID: "server-file", contentVersion: "v1" }, "user:1", load, { spec: { size: 80, type: "image/webp", quality: 0.8 } });
  assert.notEqual(await spec.promise, url);
  assert.equal(f.generated(), 3);
  version.release(); spec.release(); f.store.clear();
});

test("byte pressure evicts least recently used idle blobs and never revokes a referenced URL", async () => {
  const f = fixture({ maxBytes: 8 });
  const load = async () => new Blob(["1234"]);
  const active = f.store.acquire({ fileID: "active" }, "user:1", load);
  const activeURL = await active.promise;
  const idle = f.store.acquire({ fileID: "idle" }, "user:1", load);
  const idleURL = await idle.promise;
  idle.release();
  const extra = f.store.acquire({ fileID: "extra" }, "user:1", load);
  await extra.promise;
  assert.deepEqual(f.revoked, [idleURL]);
  assert.equal(f.revoked.includes(activeURL), false);
  active.release(); extra.release(); f.store.clear();
});

test("idle TTL evicts URLs after the remount grace period, and failures can be retried", async () => {
  let now = 0;
  const f = fixture({ idleTTLms: 50, now: () => now });
  const load = async () => new Blob(["thumbnail"]);
  const first = f.store.acquire({ fileID: "file" }, "user:1", load);
  const url = await first.promise;
  first.release();
  now = 51;
  const next = f.store.acquire({ fileID: "file" }, "user:1", load);
  assert.notEqual(await next.promise, url);
  assert.deepEqual(f.revoked, [url]);
  next.release();
  const failed = f.store.acquire({ fileID: "broken" }, "user:1", async () => { throw new Error("transient"); });
  await assert.rejects(failed.promise);
  failed.release();
  const retry = f.store.acquire({ fileID: "broken" }, "user:1", load);
  assert.ok(await retry.promise);
  retry.release(); f.store.clear();
});

test("account scope switch clears old URLs and pending permissions; file deletion invalidates all versions", async () => {
  const f = fixture();
  const load = async () => new Blob(["small"]);
  const old = f.store.acquire({ fileID: "shared-id" }, "user:1", load);
  const oldURL = await old.promise;
  const current = f.store.acquire({ fileID: "shared-id" }, "user:2", load);
  const currentURL = await current.promise;
  assert.notEqual(oldURL, currentURL);
  let pendingSignal;
  const pending = f.store.acquire({ fileID: "pending" }, "user:1", signal => { pendingSignal = signal; return new Promise(() => {}); });
  const rejected = assert.rejects(pending.promise, { name: "AbortError" });
  f.store.clearUnusedScopes("user:2");
  await rejected;
  assert.equal(pendingSignal.aborted, true);
  assert.deepEqual(f.revoked, [oldURL]);
  f.store.invalidate({ fileID: "shared-id" }, "user:2");
  assert.deepEqual(f.revoked, [oldURL, currentURL]);
  old.release(); current.release(); pending.release(); f.store.clear();
});

test("subscriber AbortSignal only cancels its lease; seed and read work without browser URL support", async () => {
  const f = fixture({ createURL: () => null });
  f.store.seed({ fileID: "seeded" }, new Blob(["small"]), "user:1");
  const controller = new AbortController();
  const first = f.store.acquire({ fileID: "seeded" }, "user:1", async () => new Blob(), { signal: controller.signal });
  const second = f.store.acquire({ fileID: "seeded" }, "user:1", async () => new Blob());
  const rejected = assert.rejects(first.promise, { name: "AbortError" });
  controller.abort();
  await rejected;
  assert.equal(await second.promise, null);
  assert.equal(f.generated(), 0);
  first.release(); second.release(); f.store.clear();
});
