import { ImagePreparationError, type ImageMetadata } from "./image-metadata.ts";
import { IMAGE_SOURCE_BYTE_LIMIT, planImagePreparation, type ImagePreparationPolicy } from "./image-preparation-plan.ts";
import { ImagePreparationScheduler } from "./image-preparation-scheduler.ts";

export { ImagePreparationError, type ImageMetadata, type ImagePreparationErrorCode } from "./image-metadata.ts";
export { IMAGE_PREPARATION_POLICY_VERSION, type ImagePreparationPolicy } from "./image-preparation-plan.ts";
export type ImageSourceInspection = { sourceSha256: string; sourceSize: number; metadata: ImageMetadata };
export type PreparedImageSource = { file: File; thumbnailBlob?: Blob; metadata: ImageMetadata; policyVersion: string; reusedOriginal: boolean };
export type ImageWorkerRequest = { kind: "inspect"; source: File } | { kind: "prepare"; source: File; inspection: ImageSourceInspection; policy: ImagePreparationPolicy };
type WorkerResult = { ok: true; result: ImageSourceInspection | PreparedImageSource } | { ok: false; code: ImagePreparationError["code"]; message: string };

export function isImagePreparationError(error: unknown): error is ImagePreparationError {
  return error instanceof ImagePreparationError;
}

const scheduler = new ImagePreparationScheduler();
const inspectionsInFlight = new WeakMap<File, { signal: AbortSignal; promise: Promise<ImageSourceInspection> }>();
const preparationsInFlight = new WeakMap<File, { signal: AbortSignal; version: string; promise: Promise<PreparedImageSource> }>();
const cache = new WeakMap<File, { inspection: ImageSourceInspection; prepared?: PreparedImageSource; expires: number }>();
// Weak references keep cache bookkeeping from retaining users' original files.
const cacheOrder: { source: WeakRef<File>; outputBytes: number }[] = [];
const CACHE_TTL_MS = 120_000;
const CACHE_OUTPUT_BYTE_LIMIT = 32 * 1024 * 1024;

function store(source: File, inspection: ImageSourceInspection, prepared?: PreparedImageSource) {
  const outputBytes = prepared && !prepared.reusedOriginal ? prepared.file.size : 0;
  cache.set(source, { inspection, prepared, expires: Date.now() + CACHE_TTL_MS });
  const previous = cacheOrder.findIndex(entry => entry.source.deref() === source);
  if (previous >= 0) cacheOrder.splice(previous, 1);
  const reference = new WeakRef(source);
  cacheOrder.push({ source: reference, outputBytes });
  while (cacheOrder.length > 8 || cacheOrder.reduce((sum, entry) => sum + entry.outputBytes, 0) > CACHE_OUTPUT_BYTE_LIMIT) {
    const removed = cacheOrder.shift()?.source.deref();
    if (removed) cache.delete(removed);
  }
  const expires = cache.get(source)?.expires;
  const timeout = setTimeout(() => {
    const file = reference.deref();
    if (file && cache.get(file)?.expires === expires) cache.delete(file);
    const index = cacheOrder.findIndex(entry => entry.source === reference);
    if (index >= 0) cacheOrder.splice(index, 1);
  }, CACHE_TTL_MS);
  // Node tests should not stay alive because of an opportunistic browser cache.
  if (typeof timeout === "object" && "unref" in timeout) timeout.unref();
}

function cached(source: File) {
  const entry = cache.get(source);
  if (entry && entry.expires > Date.now()) return entry;
  cache.delete(source);
  return undefined;
}

type IdleWorker = { worker: Worker; factory: typeof Worker; timer: ReturnType<typeof setTimeout> };
const idleWorkers: IdleWorker[] = [];

function releaseWorker(worker: Worker, workerFactory: typeof Worker) {
  worker.onmessage = null;
  worker.onerror = null;
  if (idleWorkers.length >= 2) { worker.terminate(); return; }
  const timer = setTimeout(() => {
    const index = idleWorkers.findIndex(entry => entry.worker === worker);
    if (index >= 0) idleWorkers.splice(index, 1);
    worker.terminate();
  }, 30_000);
  if (typeof timer === "object" && "unref" in timer) timer.unref();
  idleWorkers.push({ worker, factory: workerFactory, timer });
}

function leaseWorker(): Worker {
  while (idleWorkers.length) {
    const entry = idleWorkers.pop();
    if (!entry) break;
    clearTimeout(entry.timer);
    if (entry.factory === Worker) return entry.worker;
    entry.worker.terminate();
  }
  return new Worker(new URL("./image-preparation-worker.ts", import.meta.url), { type: "module" });
}

function runWorker(request: Extract<ImageWorkerRequest, { kind: "inspect" }>, signal: AbortSignal): Promise<ImageSourceInspection>;
function runWorker(request: Extract<ImageWorkerRequest, { kind: "prepare" }>, signal: AbortSignal): Promise<PreparedImageSource>;
function runWorker(request: ImageWorkerRequest, signal: AbortSignal): Promise<ImageSourceInspection | PreparedImageSource> {
  signal.throwIfAborted();
  if (typeof Worker === "undefined") return Promise.reject(new ImagePreparationError("IMAGE_CAPABILITY_UNAVAILABLE", "Background image preparation is unavailable in this browser."));
  return new Promise((resolve, reject) => {
    let worker: Worker;
    const workerFactory = Worker;
    try { worker = leaseWorker(); }
    catch { reject(new ImagePreparationError("IMAGE_CAPABILITY_UNAVAILABLE", "The image worker could not be started.")); return; }
    let settled = false;
    const finish = (callback: () => void, reusable = false) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      signal.removeEventListener("abort", onAbort);
      // Recycle ordinary tasks. Large encoded buffers get a fresh worker next
      // time so native digest/codec scratch does not remain in an idle heap.
      if (reusable && request.source.size < 16 * 1024 * 1024) releaseWorker(worker, workerFactory);
      else worker.terminate();
      callback();
    };
    const onAbort = () => finish(() => reject(signal.reason ?? new DOMException("Aborted", "AbortError")));
    const timeout = setTimeout(() => finish(() => reject(new ImagePreparationError("IMAGE_TIMEOUT", "The image operation exceeded its time budget."))), 60_000);
    signal.addEventListener("abort", onAbort, { once: true });
    worker.onmessage = (event: MessageEvent<WorkerResult>) => finish(() => {
      if (event.data.ok) resolve(event.data.result);
      else reject(new ImagePreparationError(event.data.code, event.data.message));
    }, true);
    worker.onerror = () => finish(() => reject(new ImagePreparationError("IMAGE_CAPABILITY_UNAVAILABLE", "The image worker failed.")));
    try { worker.postMessage(request); }
    catch (error) { finish(() => reject(error)); }
  });
}

/** Hash original bytes and inspect the container. Deliberately never decodes. */
export async function inspectImageSource(source: File, signal: AbortSignal): Promise<ImageSourceInspection> {
  signal.throwIfAborted();
  const existing = cached(source);
  if (existing) return existing.inspection;
  if (!source.size || source.size > IMAGE_SOURCE_BYTE_LIMIT) throw new ImagePreparationError("IMAGE_MEMORY_LIMIT", "The image exceeds the bounded source byte budget.");
  const inFlight = inspectionsInFlight.get(source);
  if (inFlight?.signal === signal) return inFlight.promise;
  const promise = scheduler.schedule(source.size * 3, signal, () => runWorker({ kind: "inspect", source }, signal)).then(result => {
    signal.throwIfAborted();
    store(source, result);
    return result;
  });
  inspectionsInFlight.set(source, { signal, promise });
  try { return await promise; }
  finally { if (inspectionsInFlight.get(source)?.promise === promise) inspectionsInFlight.delete(source); }
}

/** Call only after source-fingerprint lookup misses. Upload only result.file. */
export async function prepareImageSource(source: File, inspection: ImageSourceInspection, policy: ImagePreparationPolicy, signal: AbortSignal): Promise<PreparedImageSource> {
  signal.throwIfAborted();
  if (inspection.sourceSize !== source.size || !/^[a-f0-9]{64}$/.test(inspection.sourceSha256)) throw new ImagePreparationError("IMAGE_INVALID", "The source fingerprint does not match this image.");
  const plan = planImagePreparation(inspection.metadata, source.size, policy);
  const existing = cached(source);
  if (existing?.prepared?.policyVersion === policy.version) return existing.prepared;
  if (plan.reuse) {
    const file = source.type === inspection.metadata.mime ? source : new File([source], source.name, { type: inspection.metadata.mime, lastModified: source.lastModified });
    const result: PreparedImageSource = { file, metadata: inspection.metadata, policyVersion: policy.version, reusedOriginal: true };
    store(source, inspection, result);
    return result;
  }
  const inFlight = preparationsInFlight.get(source);
  if (inFlight?.signal === signal && inFlight.version === policy.version) return inFlight.promise;
  const promise = scheduler.schedule(plan.workingBytes, signal, () => runWorker({ kind: "prepare", source, inspection, policy }, signal)).then(result => {
    signal.throwIfAborted();
    store(source, inspection, result);
    return result;
  });
  preparationsInFlight.set(source, { signal, version: policy.version, promise });
  try { return await promise; }
  finally { if (preparationsInFlight.get(source)?.promise === promise) preparationsInFlight.delete(source); }
}
