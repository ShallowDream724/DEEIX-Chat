import { getChatFilePolicy, lookupImageReuse, uploadFile } from "@/shared/api/file";
import type { ChatFilePolicyDTO, ImageReuseLookupResult, ImageSourceReference, UploadFileResult } from "@/shared/api/file-types";
import { readSessionID, readSessionRevision } from "@/shared/auth/session";
import { ImagePreparationError, inspectImageSource, prepareImageSource, type ImageSourceInspection, type PreparedImageSource } from "./image-preparation";
import { seedThumbnail } from "./thumbnail-resource";

type ImageOptions = { signal?: AbortSignal; scope?: string; allowAnimated?: boolean };
export type PreparedLocalImage = PreparedImageSource & { preparedFile: File; assetID: string; contentVersion: string };
export type PreparedUploadResult = UploadFileResult & { preparedFile?: File; assetID?: string; contentVersion?: string };
type Normalization = { maxDimension: number; version: string; alias: boolean };
type OperationSession = { sessionID: string; revision: number };
type LookupTask = { reference: ImageSourceReference; signal: AbortSignal; resolve: (result: ImageReuseLookupResult) => void; reject: (error: unknown) => void };
type LookupBatch = { accessToken: string; session: OperationSession; tasks: LookupTask[] };

const POLICY_TTL_MS = 30_000;
let cachedPolicy: { token: string; revision: number; expires: number; refreshFromVersion?: string; promise: Promise<ChatFilePolicyDTO> } | undefined;
const batches = new Map<string, LookupBatch>();
const abortError = () => new DOMException("The image operation was canceled", "AbortError");
const mimeAliases: Record<string, string> = { "image/jpg": "image/jpeg", "image/pjpeg": "image/jpeg", "image/x-png": "image/png" };
const normalizeMime = (mime: string) => { const value = mime.split(";", 1)[0].trim().toLowerCase(); return mimeAliases[value] ?? value; };

function isCurrentSession(session: OperationSession) {
  // Access-token rotation increments revision without changing the login. Only
  // legacy sessions lacking an ID must use that stricter fallback identity.
  return session.sessionID ? readSessionID() === session.sessionID : readSessionRevision() === session.revision;
}

function captureSession(): OperationSession {
  return { sessionID: readSessionID(), revision: readSessionRevision() };
}

function sessionScope(session: OperationSession) {
  return `session:${session.sessionID || session.revision}`;
}

function assertCurrent(signal: AbortSignal, session: OperationSession) {
  signal.throwIfAborted();
  if (!isCurrentSession(session)) throw abortError();
}

function withSignal<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const onAbort = () => reject(signal.reason ?? abortError());
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", onAbort));
  });
}

function normalization(policy: ChatFilePolicyDTO): Normalization {
  const version = policy.imagePolicyVersion;
  const match = version?.match(/^image-normalize-v1:(0|[1-9]\d*)$/);
  const maxDimension = policy.imageMaxDimension ?? (match ? Number(match[1]) : 0);
  if (!Number.isSafeInteger(maxDimension) || maxDimension < 0 || maxDimension > 32768 || (version && (!match || Number(match[1]) !== maxDimension))) {
    throw new ImagePreparationError("IMAGE_UNSUPPORTED", "The image preparation policy is invalid.");
  }
  return { maxDimension, version: version || `image-normalize-v1:${maxDimension}`, alias: Boolean(version && match) };
}

export function isImageUploadCandidate(source: File): boolean {
  return normalizeMime(source.type).startsWith("image/") || /\.(?:jpe?g|png|gif|webp|svg|heic|heif|avif|bmp|tiff?|ico|jxl)$/i.test(source.name);
}

function assertSupportedImage(source: File) {
  const mime = normalizeMime(source.type);
  const extension = source.name.split(".").at(-1)?.toLowerCase() || "";
  const unsupported = ["svg", "heic", "heif", "avif", "bmp", "tif", "tiff", "ico", "jxl"];
  if (unsupported.includes(extension) || (mime.startsWith("image/") && !["image/jpeg", "image/png", "image/gif", "image/webp"].includes(mime))) {
    throw new ImagePreparationError("IMAGE_UNSUPPORTED", "This image format cannot be prepared by this browser.");
  }
}

function validateOutput(file: { type: string; size: number }, policy: ChatFilePolicyDTO) {
  const mime = normalizeMime(file.type);
  if (!mime.startsWith("image/") || (policy.allowedMIMETypes.length && !policy.allowedMIMETypes.some((allowed) => normalizeMime(allowed) === mime))) {
    throw new ImagePreparationError("IMAGE_UNSUPPORTED", "The prepared image type is not allowed by the file policy.");
  }
  const limit = policy.effectiveImageMaxBytes || policy.imageMaxBytes || policy.maxUploadFileBytes;
  if (file.size <= 0 || (limit > 0 && file.size > limit)) {
    throw new ImagePreparationError("IMAGE_MEMORY_LIMIT", "The prepared image exceeds the allowed image size.");
  }
}

async function policyFor(accessToken: string, signal: AbortSignal, session: OperationSession, refreshFromVersion?: string) {
  assertCurrent(signal, session);
  const revision = readSessionRevision();
  if (!cachedPolicy || cachedPolicy.token !== accessToken || cachedPolicy.revision !== revision || cachedPolicy.expires <= Date.now() || (refreshFromVersion && cachedPolicy.refreshFromVersion !== refreshFromVersion)) {
    const entry = { token: accessToken, revision, expires: Date.now() + POLICY_TTL_MS, refreshFromVersion, promise: getChatFilePolicy(accessToken) };
    cachedPolicy = entry;
    void entry.promise.catch(() => { if (cachedPolicy === entry) cachedPolicy = undefined; });
  }
  const policy = await withSignal(cachedPolicy.promise, signal);
  assertCurrent(signal, session);
  return policy;
}

async function flushBatch(key: string, batch: LookupBatch) {
  if (batches.get(key) === batch) batches.delete(key);
  const active = batch.tasks.filter((task) => !task.signal.aborted);
  if (!active.length) return;
  for (let offset = 0; offset < active.length; offset += 100) {
    const tasks = active.slice(offset, offset + 100);
    try {
      if (!isCurrentSession(batch.session)) throw abortError();
      // Each subscriber owns cancellation. A shared request must never use one
      // subscriber's signal, since it also carries other selected images.
      const result = await lookupImageReuse(batch.accessToken, tasks.map((task) => task.reference));
      if (!isCurrentSession(batch.session)) throw abortError();
      for (const task of tasks) task.resolve(result);
    } catch (error) { for (const task of tasks) task.reject(error); }
  }
}

function lookup(accessToken: string, reference: ImageSourceReference, signal: AbortSignal, session: OperationSession, scope: string) {
  const key = JSON.stringify([session.sessionID, session.revision, accessToken, scope]);
  let batch = batches.get(key);
  if (!batch) {
    batch = { accessToken, session, tasks: [] };
    batches.set(key, batch);
    const pending = batch;
    queueMicrotask(() => { void flushBatch(key, pending); });
  }
  const pending = batch;
  return withSignal(new Promise<ImageReuseLookupResult>((resolve, reject) => {
    pending.tasks.push({ reference, signal, resolve, reject });
  }), signal);
}

function referenceFor(inspection: ImageSourceInspection, policy: Normalization): ImageSourceReference {
  return { sourceSha256: inspection.sourceSha256, sourceSizeBytes: inspection.sourceSize, imagePolicyVersion: policy.version };
}

async function prepare(source: File, inspection: ImageSourceInspection, policy: ChatFilePolicyDTO, options: ImageOptions, signal: AbortSignal, session: OperationSession): Promise<PreparedLocalImage> {
  const normalized = normalization(policy);
  let result: PreparedImageSource;
  if (inspection.metadata.animated) {
    if (!options.allowAnimated) throw new ImagePreparationError("IMAGE_ANIMATED", "Animated images cannot be normalized without losing animation.");
    const file = source.type === inspection.metadata.mime ? source : new File([source], source.name, { type: inspection.metadata.mime, lastModified: source.lastModified });
    result = { file, metadata: inspection.metadata, policyVersion: "image-original", reusedOriginal: true };
  } else {
    result = await prepareImageSource(source, inspection, normalized, signal);
  }
  assertCurrent(signal, session);
  validateOutput(result.file, policy);
  const contentVersion = `${inspection.sourceSha256}:${result.policyVersion}`;
  const assetID = `image:${contentVersion}`;
  return { ...result, preparedFile: result.file, assetID, contentVersion };
}

export async function prepareLocalImageFile(source: File, options: ImageOptions & { policy: ChatFilePolicyDTO }): Promise<PreparedLocalImage> {
  const signal = options.signal ?? new AbortController().signal;
  const session = captureSession();
  assertCurrent(signal, session);
  if (!isImageUploadCandidate(source)) throw new ImagePreparationError("IMAGE_UNSUPPORTED", "The selected file is not a supported image.");
  assertSupportedImage(source);
  const inspection = await inspectImageSource(source, signal);
  assertCurrent(signal, session);
  const result = await prepare(source, inspection, options.policy, options, signal, session);
  if (result.thumbnailBlob) seedThumbnail({ assetID: result.assetID, localFile: result.file, contentVersion: result.contentVersion }, result.thumbnailBlob, options.scope ?? sessionScope(session));
  return result;
}

export async function uploadPreparedFile(accessToken: string, source: File, options: ImageOptions & { purpose?: string; policy?: ChatFilePolicyDTO } = {}): Promise<PreparedUploadResult> {
  const signal = options.signal ?? new AbortController().signal;
  const session = captureSession();
  assertCurrent(signal, session);
  if (!isImageUploadCandidate(source)) {
    const result = await uploadFile(accessToken, source, { purpose: options.purpose, signal });
    assertCurrent(signal, session);
    return result;
  }
  assertSupportedImage(source);
  const inspection = await inspectImageSource(source, signal);
  assertCurrent(signal, session);
  if (inspection.metadata.animated && !options.allowAnimated) throw new ImagePreparationError("IMAGE_ANIMATED", "Animated images cannot be normalized without losing animation.");
  let policy = options.policy ?? await policyFor(accessToken, signal, session);
  let normalized = normalization(policy);
  const scope = options.scope ?? sessionScope(session);
  let alias = normalized.alias && !inspection.metadata.animated;
  if (alias) {
    let result: ImageReuseLookupResult | undefined;
    const reference = referenceFor(inspection, normalized);
    try { result = await lookup(accessToken, reference, signal, session, scope); }
    catch (error) {
      assertCurrent(signal, session);
      const status = typeof error === "object" && error !== null && "status" in error ? error.status : undefined;
      if (status === 401 || status === 403) throw error;
      // Lookup is an optimization. Network/service failures still use the full
      // normalized upload path, without claiming that an image was reused.
    }
    assertCurrent(signal, session);
    if (result && result.imagePolicyVersion !== normalized.version) {
      policy = await policyFor(accessToken, signal, session, normalized.version);
      normalized = normalization(policy);
      alias = normalized.alias && normalized.version === result.imagePolicyVersion;
    } else if (result) {
      const hit = result.items.find((item) => item.sourceSha256 === reference.sourceSha256 && item.sourceSizeBytes === reference.sourceSizeBytes && item.imagePolicyVersion === reference.imagePolicyVersion)?.file;
      if (hit) {
        validateOutput({ type: hit.mimeType, size: hit.sizeBytes }, policy);
        return { file: hit, reused: true, quota: result.quota };
      }
    }
  }
  const prepared = await prepare(source, inspection, policy, options, signal, session);
  assertCurrent(signal, session);
  const result = await uploadFile(accessToken, prepared.file, { purpose: options.purpose, signal, imageSource: alias ? referenceFor(inspection, normalized) : undefined });
  assertCurrent(signal, session);
  validateOutput({ type: result.file.mimeType, size: result.file.sizeBytes }, policy);
  // A source-alias race can reuse a canonical image encoded by another browser.
  // Only the server file ID identifies those stored bytes; our local output is
  // safe to expose and seed when it became the newly stored file.
  if (result.reused) return result;
  if (prepared.thumbnailBlob) seedThumbnail({ fileID: result.file.fileID }, prepared.thumbnailBlob, scope);
  return { ...result, preparedFile: prepared.file, assetID: prepared.assetID };
}
