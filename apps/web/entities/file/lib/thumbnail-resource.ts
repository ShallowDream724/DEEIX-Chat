import { createImageThumbnail, DEFAULT_THUMBNAIL_SPEC, type ThumbnailSpec } from "./image-thumbnail.ts";

export type ThumbnailIdentity = {
  fileID?: string;
  assetID?: string;
  localFile?: Blob;
  contentVersion?: string | number;
};

export type ThumbnailLease = { promise: Promise<string | null>; release: () => void };
type Entry = {
  key: string;
  scope: string;
  identity: string;
  refs: number;
  touched: number;
  releasedAt: number;
  bytes: number;
  blob?: Blob;
  url?: string;
  controller: AbortController;
  promise: Promise<Blob>;
  timer?: ReturnType<typeof setTimeout>;
};

type ResourceOptions = {
  maxBytes?: number;
  maxEntries?: number;
  maxBlobBytes?: number;
  idleTTLms?: number;
  now?: () => number;
  createURL?: (blob: Blob) => string | null;
  revokeURL?: (url: string) => void;
  generate?: typeof createImageThumbnail;
};

const abortError = () => new DOMException("Thumbnail subscription released", "AbortError");

/** Only encoded thumbnails are retained. Full source blobs belong to the caller. */
export function createThumbnailResourceStore(options: ResourceOptions = {}) {
  const entries = new Map<string, Entry>();
  const localIdentities = new WeakMap<Blob, string>();
  const assetAliases = new Map<string, string>();
  const now = options.now ?? Date.now;
  const maxBytes = options.maxBytes ?? 16 * 1024 * 1024;
  const maxEntries = options.maxEntries ?? 512;
  const maxBlobBytes = options.maxBlobBytes ?? 512 * 1024;
  const idleTTLms = options.idleTTLms ?? 5 * 60 * 1000;
  const generate = options.generate ?? createImageThumbnail;
  const createURL = options.createURL ?? ((blob) => typeof URL !== "undefined" && typeof URL.createObjectURL === "function" ? URL.createObjectURL(blob) : null);
  const revokeURL = options.revokeURL ?? ((url) => { if (typeof URL !== "undefined" && typeof URL.revokeObjectURL === "function") URL.revokeObjectURL(url); });
  let nextLocalIdentity = 0;
  let totalBytes = 0;

  function identityKey(identity: ThumbnailIdentity, scope: string, rememberAlias = false): string {
    // Server files are immutable by ID today; contentVersion supports future replacements.
    if (identity.fileID && !identity.fileID.startsWith("temporary_")) return `file:${identity.fileID}`;
    const aliasKey = identity.assetID ? JSON.stringify([scope, identity.assetID]) : undefined;
    let key = aliasKey ? assetAliases.get(aliasKey) : undefined;
    if (!key && identity.localFile) key = localIdentities.get(identity.localFile);
    key ??= identity.assetID ? `asset:${identity.assetID}` : identity.localFile ? `local:${++nextLocalIdentity}` : undefined;
    if (!key) throw new Error("A stable file or asset identity is required for a thumbnail");
    if (identity.localFile) localIdentities.set(identity.localFile, key);
    if (aliasKey && rememberAlias && key !== `asset:${identity.assetID}`) assetAliases.set(aliasKey, key);
    return key;
  }

  function keyFor(identity: ThumbnailIdentity, scope: string, spec: Readonly<ThumbnailSpec> = DEFAULT_THUMBNAIL_SPEC) {
    return JSON.stringify([scope, identityKey(identity, scope), identity.contentVersion ?? "", spec.size, spec.type, spec.quality]);
  }

  function remove(entry: Entry) {
    if (entries.get(entry.key) !== entry) return;
    entries.delete(entry.key);
    clearTimeout(entry.timer);
    totalBytes -= entry.bytes;
    if (entry.url) revokeURL(entry.url);
    entry.controller.abort();
    if (![...entries.values()].some((other) => other.scope === entry.scope && other.identity === entry.identity)) {
      for (const [alias, identity] of assetAliases) {
        if (identity === entry.identity && JSON.parse(alias)[0] === entry.scope) assetAliases.delete(alias);
      }
    }
  }

  function sweep() {
    const unused = [...entries.values()].filter((entry) => entry.refs === 0).sort((a, b) => a.touched - b.touched);
    for (const entry of unused) {
      if (now() - entry.releasedAt >= idleTTLms || totalBytes > maxBytes || entries.size > maxEntries) remove(entry);
    }
  }

  function schedule(entry: Entry) {
    clearTimeout(entry.timer);
    if (entry.refs || entries.get(entry.key) !== entry) return;
    entry.timer = setTimeout(() => { sweep(); }, Math.max(1, idleTTLms - (now() - entry.releasedAt)));
    // Node test/SSR processes need not stay alive for an idle browser cache.
    if (typeof entry.timer === "object" && "unref" in entry.timer) entry.timer.unref();
  }

  function makeEntry(identity: ThumbnailIdentity, scope: string, spec: Readonly<ThumbnailSpec>): Entry {
    const key = keyFor(identity, scope, spec);
    const entry: Entry = {
      key, scope, identity: identityKey(identity, scope), refs: 0, bytes: 0,
      touched: now(), releasedAt: now(), controller: new AbortController(), promise: Promise.resolve(new Blob()),
    };
    entries.set(key, entry);
    return entry;
  }

  function save(entry: Entry, blob: Blob) {
    entry.controller.signal.throwIfAborted();
    if (blob.size > maxBlobBytes) throw new Error("Thumbnail exceeds cache size limit");
    entry.blob = blob;
    entry.bytes = blob.size;
    totalBytes += blob.size;
    schedule(entry);
    sweep();
    return blob;
  }

  function acquire(identity: ThumbnailIdentity, scope: string, load: (signal: AbortSignal) => Promise<Blob>, request: { spec?: Readonly<ThumbnailSpec>; signal?: AbortSignal } = {}): ThumbnailLease {
    const spec = request.spec ?? DEFAULT_THUMBNAIL_SPEC;
    sweep();
    identityKey(identity, scope, true);
    const key = keyFor(identity, scope, spec);
    let entry = entries.get(key);
    if (!entry) {
      entry = makeEntry(identity, scope, spec);
      const target = entry;
      target.promise = generate(() => load(target.controller.signal), target.controller.signal, spec)
        .then((blob) => save(target, blob))
        .catch((error: unknown) => { remove(target); throw error; });
    }
    const target = entry;
    target.refs++;
    target.touched = now();
    clearTimeout(target.timer);
    let released = false;
    let rejectReleased: (reason: unknown) => void = () => undefined;
    const release = () => {
      if (released) return;
      released = true;
      request.signal?.removeEventListener("abort", release);
      target.controller.signal.removeEventListener("abort", release);
      rejectReleased(abortError());
      target.refs--;
      target.touched = target.releasedAt = now();
      schedule(target);
      sweep();
    };
    const cancelled = new Promise<never>((_, reject) => { rejectReleased = reject; });
    const promise = Promise.race([cancelled, target.promise.then((blob) => {
      if (released || entries.get(key) !== target || target.controller.signal.aborted) throw abortError();
      target.url ??= createURL(blob) ?? undefined;
      return target.url ?? null;
    })]);
    request.signal?.addEventListener("abort", release, { once: true });
    target.controller.signal.addEventListener("abort", release, { once: true });
    if (request.signal?.aborted) release();
    return { promise, release };
  }

  function seed(identity: ThumbnailIdentity, blob: Blob, scope: string, spec: Readonly<ThumbnailSpec> = DEFAULT_THUMBNAIL_SPEC): boolean {
    sweep();
    if (blob.size > maxBlobBytes) return false;
    identityKey(identity, scope, true);
    const key = keyFor(identity, scope, spec);
    if (entries.has(key)) return true;
    const entry = makeEntry(identity, scope, spec);
    save(entry, blob);
    entry.promise = Promise.resolve(blob);
    return entries.get(key) === entry;
  }

  function clearScope(scope: string) {
    for (const entry of entries.values()) if (entry.scope === scope) remove(entry);
    for (const alias of assetAliases.keys()) if (JSON.parse(alias)[0] === scope) assetAliases.delete(alias);
  }

  return {
    keyFor, acquire, seed, clearScope,
    invalidate(identity: ThumbnailIdentity, scope: string) {
      const key = identityKey(identity, scope);
      for (const entry of entries.values()) if (entry.scope === scope && entry.identity === key) remove(entry);
    },
    clearUnusedScopes(activeScope: string | null) {
      for (const scope of new Set([...entries.values()].map((entry) => entry.scope))) if (scope !== activeScope) clearScope(scope);
    },
    clear() { for (const entry of entries.values()) remove(entry); assetAliases.clear(); },
  };
}

const thumbnails = createThumbnailResourceStore();
export const thumbnailScopeForUser = (userID: string | number) => `user:${userID}`;
export const thumbnailResourceKey = thumbnails.keyFor;
export const acquireThumbnail = thumbnails.acquire;
export const seedThumbnail = thumbnails.seed;
export const invalidateThumbnail = thumbnails.invalidate;
export const clearThumbnailScope = thumbnails.clearScope;
export const clearUnusedThumbnailScopes = thumbnails.clearUnusedScopes;
export const clearThumbnailResources = thumbnails.clear;
