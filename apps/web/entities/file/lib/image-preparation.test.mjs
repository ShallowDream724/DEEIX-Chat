import assert from "node:assert/strict";
import test from "node:test";
import { ImagePreparationScheduler } from "./image-preparation-scheduler.ts";
import { inspectImageSource, prepareImageSource } from "./image-preparation.ts";
import { processImageWorkerRequest } from "./image-preparation-worker.ts";
import { inspectImageMetadata } from "./image-metadata.ts";
import { bytes, chunk, png, jpeg, u32 } from "./image-metadata-fixtures.mjs";

const policy = { maxDimension: 1536, version: "image-normalize-v1:1536" };
const tick = () => new Promise(resolve => setImmediate(resolve));
const inspection = data => ({ sourceSize: data.length, sourceSha256: "a".repeat(64), metadata: inspectImageMetadata(data) });

test("scheduler limits concurrency and byte budget, and removes cancelled queued work", async () => {
  const scheduler = new ImagePreparationScheduler(2, 100);
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  let active = 0;
  let peak = 0;
  const run = async () => { active++; peak = Math.max(active, peak); await gate; active--; };
  const signal = new AbortController().signal;
  const first = scheduler.schedule(60, signal, run);
  let nextRan = false;
  const removed = new AbortController();
  const second = scheduler.schedule(60, removed.signal, async () => { nextRan = true; });
  const rejected = assert.rejects(second, { name: "AbortError" });
  const third = scheduler.schedule(40, signal, run);
  await tick();
  assert.equal(active, 1);
  removed.abort();
  await rejected;
  await tick();
  assert.equal(active, 2);
  release();
  await Promise.all([first, third]);
  assert.equal(peak, 2);
  assert.equal(nextRan, false);
});

test("inspection hashes original bytes without decode, caches retries, and fast path reuses bytes", async t => {
  const originalWorker = globalThis.Worker;
  let created = 0;
  let terminated = 0;
  globalThis.Worker = class {
    constructor() { created++; }
    postMessage(request) { processImageWorkerRequest(request).then(result => this.onmessage?.({ data: { ok: true, result } })); }
    terminate() { terminated++; }
  };
  t.after(() => { globalThis.Worker = originalWorker; });
  const source = new File([png()], "x.png", { type: "image/png" });
  const signal = new AbortController().signal;
  const [a, b] = await Promise.all([inspectImageSource(source, signal), inspectImageSource(source, signal)]);
  assert.equal(a, b);
  assert.match(a.sourceSha256, /^[a-f0-9]{64}$/);
  assert.equal(a.sourceSize, source.size);
  assert.equal(await inspectImageSource(source, signal), a);
  const prepared = await prepareImageSource(source, a, policy, signal);
  assert.equal(prepared.file, source);
  assert.equal(prepared.reusedOriginal, true);
  assert.equal(created, 1);
  assert.equal(terminated, 0);
  await inspectImageSource(new File([png()], "next.png"), signal);
  assert.equal(created, 1);
  const cancelled = new AbortController();
  cancelled.abort();
  await assert.rejects(prepareImageSource(source, a, policy, cancelled.signal), { name: "AbortError" });
});

test("active cancellation terminates a stalled worker and releases a slot", async t => {
  const originalWorker = globalThis.Worker;
  let terminated = 0;
  globalThis.Worker = class { postMessage() {} terminate() { terminated++; } };
  t.after(() => { globalThis.Worker = originalWorker; });
  const controller = new AbortController();
  const promise = inspectImageSource(new File([png()], "cancel.png"), controller.signal);
  await tick();
  const rejected = assert.rejects(promise, { name: "AbortError" });
  controller.abort();
  await rejected;
  assert.equal(terminated, 1);
});

test("worker uses one SDR encoder and releases native frame and canvas", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let framesClosed = 0;
  let decodersClosed = 0;
  let encodes = 0;
  let decoderOptions;
  let canvas;
  globalThis.ImageDecoder = class {
    static async isTypeSupported() { return true; }
    constructor(options) { decoderOptions = options; this.tracks = { ready: Promise.resolve(), selectedTrack: { animated: false, frameCount: 1 } }; }
    async decode() { return { image: { displayWidth: 2000, displayHeight: 1500, colorSpace: { transfer: "iec61966-2-1" }, format: "I420", close() { framesClosed++; } } }; }
    close() { decodersClosed++; }
  };
  globalThis.createImageBitmap = async () => { assert.fail("Native scaled decode should succeed"); };
  globalThis.OffscreenCanvas = class {
    constructor(width, height) { this.width = width; this.height = height; canvas = this; }
    getContext(_type, options) { assert.equal(options.colorSpace, "srgb"); assert.equal(options.colorType, "unorm8"); return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) { encodes++; assert.deepEqual(options, { type: "image/jpeg", quality: 0.9 }); return new Blob([jpeg(1536, 1152)], { type: "image/jpeg" }); }
  };
  const data = jpeg();
  const result = await processImageWorkerRequest({ kind: "prepare", source: new File([data], "photo.jpeg"), inspection: inspection(data), policy });
  assert.equal(result.file.name, "photo.jpg");
  assert.equal(result.metadata.width, 1536);
  assert.equal(result.metadata.height, 1152);
  assert.deepEqual([decoderOptions.desiredWidth, decoderOptions.desiredHeight], [2000, 1500]);
  assert.equal(encodes, 1);
  assert.equal(framesClosed, 1);
  assert.equal(decodersClosed, 1);
  assert.deepEqual([canvas.width, canvas.height], [0, 0]);
});

test("undersized native output falls back without upscaling, and transparency remains PNG", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let closed = 0;
  let fallback = 0;
  globalThis.ImageDecoder = class {
    static async isTypeSupported() { return true; }
    constructor() { this.tracks = { ready: Promise.resolve() }; }
    async decode() { return { image: { displayWidth: 1, displayHeight: 1, colorSpace: {}, format: "RGBA", close() { closed++; } } }; }
    close() {}
  };
  globalThis.createImageBitmap = async (_source, options) => { fallback++; assert.equal(options.imageOrientation, "from-image"); return { width: 4000, height: 3000, close() { closed++; } }; };
  globalThis.OffscreenCanvas = class {
    getContext(_type, options) { assert.equal(options.alpha, true); return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) { assert.deepEqual(options, { type: "image/png" }); return new Blob([png(1536, 1152)], { type: "image/png" }); }
  };
  const data = png(4000, 3000);
  const result = await processImageWorkerRequest({ kind: "prepare", source: new File([data], "transparent.png"), inspection: inspection(data), policy });
  assert.equal(result.file.type, "image/png");
  assert.equal(fallback, 1);
  assert.equal(closed, 2);
});

test("missing Worker refuses preparation without a main-thread decode", async t => {
  const old = globalThis.Worker;
  globalThis.Worker = undefined;
  t.after(() => { globalThis.Worker = old; });
  await assert.rejects(inspectImageSource(new File([png()], "missing.png"), new AbortController().signal), { code: "IMAGE_CAPABILITY_UNAVAILABLE" });
});

test("orientation is normalized by the worker bitmap path, including cleanup on encode failure", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let closed = 0;
  let canvas;
  globalThis.ImageDecoder = { async isTypeSupported() { assert.fail("EXIF source must use bitmap orientation handling"); } };
  globalThis.createImageBitmap = async (_source, options) => {
    assert.equal(options.imageOrientation, "from-image");
    return { width: 3000, height: 4000, close() { closed++; } };
  };
  globalThis.OffscreenCanvas = class {
    constructor(width, height) { assert.deepEqual([width, height], [1152, 1536]); this.width = width; this.height = height; canvas = this; }
    getContext() { return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob() { throw new Error("bad encode"); }
  };
  const data = jpeg();
  const inspected = inspection(data);
  inspected.metadata.orientation = 6;
  await assert.rejects(processImageWorkerRequest({ kind: "prepare", source: new File([data], "rotate.jpg"), inspection: inspected, policy }), { code: "IMAGE_INVALID" });
  assert.equal(closed, 1);
  assert.deepEqual([canvas.width, canvas.height], [0, 0]);
});

test("native decoder HDR output is rejected and closed without a fallback", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let closed = 0;
  globalThis.OffscreenCanvas = class {};
  globalThis.createImageBitmap = async () => { assert.fail("HDR must not fall back to a silent SDR decode"); };
  globalThis.ImageDecoder = class {
    static async isTypeSupported() { return true; }
    constructor() { this.tracks = { ready: Promise.resolve() }; }
    async decode() { return { image: { displayWidth: 4000, displayHeight: 3000, colorSpace: { transfer: "smpte2084" }, format: "I420P10", close() { closed++; } } }; }
    close() {}
  };
  const data = jpeg();
  await assert.rejects(processImageWorkerRequest({ kind: "prepare", source: new File([data], "hdr.jpg"), inspection: inspection(data), policy }), { code: "IMAGE_COLOR_UNSUPPORTED" });
  assert.equal(closed, 1);
});

test("native SDR RGB10 / PNG16 precision is normalized by one unorm8 encode", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let encodes = 0;
  let closed = 0;
  globalThis.ImageDecoder = class {
    static async isTypeSupported() { return true; }
    constructor() { this.tracks = { ready: Promise.resolve() }; }
    async decode() { return { image: { displayWidth: 800, displayHeight: 600, colorSpace: { transfer: "iec61966-2-1" }, format: "I444P10", close() { closed++; } } }; }
    close() {}
  };
  globalThis.createImageBitmap = async () => { assert.fail("Native SDR precision conversion should succeed"); };
  globalThis.OffscreenCanvas = class {
    getContext(_type, options) { assert.equal(options.colorType, "unorm8"); return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) { encodes++; assert.deepEqual(options, { type: "image/png" }); return new Blob([png()], { type: "image/png" }); }
  };
  const data = png(800, 600, [], 16);
  const result = await processImageWorkerRequest({ kind: "prepare", source: new File([data], "precision.png"), inspection: inspection(data), policy });
  assert.equal(result.reusedOriginal, false);
  assert.equal(result.metadata.bitDepth, 8);
  assert.equal(result.metadata.colorSpace, "srgb");
  assert.equal(encodes, 1);
  assert.equal(closed, 1);
});

test("MPF preparation decodes only the stripped SDR primary and re-encodes it", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  globalThis.ImageDecoder = undefined;
  let decodes = 0;
  let encodes = 0;
  globalThis.createImageBitmap = async source => {
    decodes++;
    const decoded = inspectImageMetadata(new Uint8Array(await source.arrayBuffer()));
    assert.equal(decoded.jpegAuxiliary, undefined);
    assert.deepEqual([decoded.width, decoded.height], [800, 600]);
    return { width: 800, height: 600, close() {} };
  };
  globalThis.OffscreenCanvas = class {
    getContext() { return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) { encodes++; assert.deepEqual(options, { type: "image/jpeg", quality: 0.9 }); return new Blob([jpeg(800, 600)], { type: "image/jpeg" }); }
  };
  const mpf = bytes([255, 226, 0, 6], Buffer.from("MPF\0"));
  const data = bytes(jpeg(800, 600, [mpf]), jpeg(100, 100));
  const result = await processImageWorkerRequest({ kind: "prepare", source: new File([data], "multi.jpg"), inspection: inspection(data), policy });
  assert.equal(result.reusedOriginal, false);
  assert.equal(result.metadata.jpegAuxiliary, undefined);
  assert.equal(encodes, 1);
  assert.equal(decodes, 1);
});

test("new canvas JPEG/PNG color profiles are canonicalized once and re-selection reuses exact bytes", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  globalThis.ImageDecoder = undefined;
  let encodes = 0;
  const icc = Buffer.from("ICC_PROFILE\0\x01\x01unknown-original-profile");
  const jpegWithIcc = jpeg(800, 600, [bytes([255, 226, (icc.length + 2) >>> 8, (icc.length + 2) & 255], icc)]);
  const pngWithIcc = png(800, 600, [chunk("iCCP", [1]), chunk("gAMA", u32(50000)), chunk("cHRM", new Uint8Array(32))]);
  globalThis.createImageBitmap = async () => ({ width: 800, height: 600, close() {} });
  globalThis.OffscreenCanvas = class {
    getContext() { return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) {
      encodes++;
      return new Blob([options.type === "image/jpeg" ? jpegWithIcc : pngWithIcc], { type: options.type });
    }
  };
  for (const [data, type] of [[jpegWithIcc, "image/jpeg"], [pngWithIcc, "image/png"]]) {
    const source = new File([data], "source", { type });
    const originalInspection = inspection(data);
    assert.equal(originalInspection.metadata.colorSpace, "icc");
    const prepared = await processImageWorkerRequest({ kind: "prepare", source, inspection: originalInspection, policy });
    assert.equal(prepared.reusedOriginal, false);
    const canonicalBytes = new Uint8Array(await prepared.file.arrayBuffer());
    const canonicalInspection = inspection(canonicalBytes);
    assert.equal(canonicalInspection.metadata.colorSpace, "srgb");
    const reselected = new File([prepared.file], prepared.file.name, { type: prepared.file.type });
    const second = await prepareImageSource(reselected, canonicalInspection, policy, new AbortController().signal);
    assert.equal(second.reusedOriginal, true);
    assert.equal(second.file, reselected);
    assert.deepEqual(new Uint8Array(await second.file.arrayBuffer()), canonicalBytes);
  }
  assert.equal(encodes, 2);
});

test("a single-frame GIF normalizes when its native track is animation-capable", async t => {
  const original = { OffscreenCanvas: globalThis.OffscreenCanvas, ImageDecoder: globalThis.ImageDecoder, createImageBitmap: globalThis.createImageBitmap };
  t.after(() => Object.assign(globalThis, original));
  let encodes = 0;
  let closed = 0;
  globalThis.ImageDecoder = class {
    static async isTypeSupported() { return true; }
    constructor() { this.tracks = { ready: Promise.resolve(), selectedTrack: { animated: true, frameCount: 1 } }; }
    async decode() { return { image: { displayWidth: 1, displayHeight: 1, colorSpace: { transfer: "iec61966-2-1" }, format: "RGBA", close() { closed++; } } }; }
    close() {}
  };
  globalThis.createImageBitmap = async () => { assert.fail("Single-frame native decode should succeed"); };
  globalThis.OffscreenCanvas = class {
    getContext() { return { getImageData: () => ({ colorSpace: "srgb", data: new Uint8ClampedArray(4) }), drawImage() {} }; }
    async convertToBlob(options) { encodes++; assert.deepEqual(options, { type: "image/png" }); return new Blob([png(1, 1)], { type: "image/png" }); }
  };
  const data = bytes(Buffer.from("GIF89a"), [1, 0, 1, 0, 0, 0, 0], [44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0, 59]);
  const inspected = inspection(data);
  assert.equal(inspected.metadata.animated, false);
  const result = await processImageWorkerRequest({ kind: "prepare", source: new File([data], "static.gif"), inspection: inspected, policy });
  assert.equal(result.file.type, "image/png");
  assert.equal(result.metadata.animated, false);
  assert.equal(result.reusedOriginal, false);
  assert.equal(encodes, 1);
  assert.equal(closed, 1);
});
