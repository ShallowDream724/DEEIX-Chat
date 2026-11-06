import assert from "node:assert/strict";
import test from "node:test";
import { createImageThumbnail } from "./image-thumbnail.ts";

test("thumbnails serialize decoding, skip removed queued files and release decoded pixels", async (t) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  let closed = 0;
  let decodes = 0;
  const canvases = [];
  const oldBitmap = globalThis.createImageBitmap;
  const oldDocument = globalThis.document;
  t.after(() => { globalThis.createImageBitmap = oldBitmap; globalThis.document = oldDocument; });
  globalThis.createImageBitmap = async () => {
    decodes++;
    await gate;
    return { width: 800, height: 600, close: () => { closed++; } };
  };
  globalThis.document = { createElement: () => {
    const canvas = { width: 0, height: 0, getContext: () => ({ drawImage() {} }), toBlob: callback => callback(new Blob(["preview"])) };
    canvases.push(canvas);
    return canvas;
  } };
  const first = createImageThumbnail(async () => new Blob(["image"]), new AbortController().signal);
  const removed = new AbortController();
  let loadedRemoved = false;
  const second = createImageThumbnail(async () => { loadedRemoved = true; return new Blob(); }, removed.signal);
  const rejected = assert.rejects(second, { name: "AbortError" });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(decodes, 1);
  removed.abort();
  release();
  await first;
  await rejected;
  assert.equal(loadedRemoved, false);
  assert.equal(closed, 1);
  assert.equal(canvases[0].width, 0);
  assert.equal(canvases[0].height, 0);

  globalThis.createImageBitmap = async () => { throw new Error("Invalid image"); };
  await assert.rejects(createImageThumbnail(async () => new Blob(), new AbortController().signal));
  globalThis.createImageBitmap = async () => ({ width: 1, height: 1, close: () => { closed++; } });
  const result = await createImageThumbnail(async () => new Blob(), new AbortController().signal);
  assert.equal(await result.text(), "preview");
  assert.equal(closed, 2);
});
