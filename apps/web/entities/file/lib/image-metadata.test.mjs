import assert from "node:assert/strict";
import test from "node:test";
import { inspectImageMetadata, sdrJpegDecodeSource } from "./image-metadata.ts";
import { planImagePreparation } from "./image-preparation-plan.ts";
import { bytes, u32, chunk, png, jpeg } from "./image-metadata-fixtures.mjs";

test("container signatures override MIME guesses and PNG alpha/depth are explicit", () => {
  const metadata = inspectImageMetadata(png());
  assert.equal(metadata.format, "png");
  assert.equal(metadata.hasAlpha, true);
  assert.equal(metadata.width, 800);
  assert.equal(inspectImageMetadata(png(800, 600, [], 16)).colorSpace, "srgb");
  assert.equal(inspectImageMetadata(png(800, 600, [chunk("tRNS", [0])], 8, 2)).hasAlpha, true);
  assert.throws(() => inspectImageMetadata(new Uint8Array(100)), { code: "IMAGE_UNSUPPORTED" });
  assert.throws(() => inspectImageMetadata(png().subarray(0, 35)), { code: "IMAGE_INVALID" });
});

test("APNG, GIF and WebP animation are identified without a pixel decode", () => {
  assert.equal(inspectImageMetadata(png(10, 10, [chunk("acTL", bytes(u32(2), u32(0)))])).animated, true);
  const header = bytes(Buffer.from("GIF89a"), [1, 0, 1, 0, 0, 0, 0]);
  const frame = [44, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 68, 1, 0];
  assert.equal(inspectImageMetadata(bytes(header, frame, [59])).animated, false);
  assert.equal(inspectImageMetadata(bytes(header, frame, frame, [59])).animated, true);
  const webpChunk = (type, content) => bytes(Buffer.from(type), [content.length, 0, 0, 0], content);
  const body = bytes(Buffer.from("WEBP"), webpChunk("VP8X", [2, 0, 0, 0, 0, 0, 0, 0, 0, 0]), webpChunk("ANMF", []));
  const webp = bytes(Buffer.from("RIFF"), [body.length, 0, 0, 0], body);
  assert.equal(inspectImageMetadata(webp).animated, true);
  assert.throws(() => planImagePreparation(inspectImageMetadata(webp), webp.length, { maxDimension: 1536, version: "image-normalize-v1:1536" }), { code: "IMAGE_ANIMATED" });
});

test("EXIF orientation, HDR/gain-map exclusions, and JPEG dimensions are inspected", () => {
  const exif = bytes(Buffer.from("Exif\0\0II"), [42, 0, 8, 0, 0, 0, 1, 0, 18, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0]);
  const segment = bytes([255, 225, (exif.length + 2) >>> 8, (exif.length + 2) & 255], exif);
  const metadata = inspectImageMetadata(jpeg(4000, 3000, [segment]));
  assert.equal(metadata.orientation, 6);
  assert.deepEqual([metadata.width, metadata.height], [4000, 3000]);
  assert.equal(inspectImageMetadata(jpeg(100, 100, [bytes([255, 226, 0, 6], Buffer.from("MPF\0"))])).jpegAuxiliary.kind, "mpf");
  assert.equal(inspectImageMetadata(png(100, 100, [chunk("cICP", [9, 16, 0, 1])])).colorSpace, "unsupported");
});

test("plans resize once, preserve aspect and alpha, round JPEG decode upward, and honor 0", () => {
  const metadata = inspectImageMetadata(jpeg());
  const plan = planImagePreparation(metadata, 1024, { maxDimension: 1536, version: "image-normalize-v1:1536" });
  assert.deepEqual([plan.width, plan.height, plan.decodeWidth, plan.decodeHeight, plan.mime], [1536, 1152, 2000, 1500, "image/jpeg"]);
  const disabled = planImagePreparation(metadata, 1024, { maxDimension: 0, version: "image-normalize-v1:0" });
  assert.equal(disabled.reuse, true);
  const alpha = planImagePreparation(inspectImageMetadata(png(4000, 3000)), 1024, { maxDimension: 1536, version: "image-normalize-v1:1536" });
  assert.equal(alpha.mime, "image/png");
  const rotated = planImagePreparation({ ...metadata, orientation: 6 }, 1024, { maxDimension: 1536, version: "image-normalize-v1:1536" });
  assert.deepEqual([rotated.width, rotated.height], [1152, 1536]);
  assert.throws(() => planImagePreparation({ ...metadata, width: 30000, height: 30000 }, 1024, { maxDimension: 1536, version: "image-normalize-v1:1536" }), { code: "IMAGE_MEMORY_LIMIT" });
  assert.throws(() => planImagePreparation(metadata, 1024, { maxDimension: 100, version: "image-normalize-v1:1536" }), { code: "IMAGE_UNSUPPORTED" });
});

test("8K 29 MiB input fits its own bounded decode reservation", () => {
  const plan = planImagePreparation(inspectImageMetadata(png(7680, 4320)), 29 * 1024 * 1024, { maxDimension: 1536, version: "image-normalize-v1:1536" });
  assert.deepEqual([plan.width, plan.height], [1536, 864]);
  assert.ok(plan.workingBytes < 256 * 1024 * 1024);
  assert.ok(plan.workingBytes > 128 * 1024 * 1024);
});

test("SDR PNG16 must normalize to PNG8; color chunks use PNG3 precedence", () => {
  const policy = { maxDimension: 1536, version: "image-normalize-v1:1536" };
  const source = png(800, 600, [chunk("sRGB", [0])], 16);
  const metadata = inspectImageMetadata(source);
  assert.equal(metadata.bitDepth, 16);
  assert.equal(metadata.colorSpace, "srgb");
  const plan = planImagePreparation(metadata, source.length, policy);
  assert.equal(plan.reuse, false);
  assert.equal(plan.mime, "image/png");
  assert.ok(plan.workingBytes >= 800 * 600 * 16);
  const displayP3 = inspectImageMetadata(png(800, 600, [chunk("cICP", [12, 13, 0, 1])], 16));
  assert.equal(displayP3.colorSpace, "icc");
  assert.equal(displayP3.sdrColorConfirmed, true);
  assert.equal(planImagePreparation(displayP3, 1024, policy).reuse, false);
  const unknownIcc = inspectImageMetadata(png(800, 600, [chunk("iCCP", [1])], 16));
  assert.equal(unknownIcc.colorSpace, "unsupported");
  const hdr = inspectImageMetadata(png(800, 600, [chunk("cICP", [9, 16, 0, 1]), chunk("sRGB", [0])], 16));
  assert.equal(hdr.colorSpace, "unsupported");
  assert.throws(() => planImagePreparation(hdr, 1024, policy), { code: "IMAGE_COLOR_UNSUPPORTED" });
});

test("ordinary MPF and known SDR gain-map bases are isolated; unknown or HDR bases reject", async () => {
  const segment = (marker, payload) => bytes([255, marker, (payload.length + 2) >>> 8, (payload.length + 2) & 255], payload);
  const mpf = segment(226, Buffer.from("MPF\0"));
  const xmp = properties => segment(225, Buffer.from(`http://ns.adobe.com/xap/1.0/\0<rdf:Description xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/" ${properties}/>`));
  const primary = jpeg(800, 600, [mpf, xmp('hdrgm:Version="1.0"')]);
  const secondary = jpeg(100, 100, [xmp('hdrgm:Version="1.0" hdrgm:BaseRenditionIsHDR="False"')]);
  const data = bytes(primary, secondary);
  const metadata = inspectImageMetadata(data);
  assert.equal(metadata.jpegAuxiliary.kind, "gainmap-sdr");
  assert.equal(metadata.jpegAuxiliary.primaryByteLength, primary.length);
  assert.equal(planImagePreparation(metadata, data.length, { maxDimension: 1536, version: "image-normalize-v1:1536" }).reuse, false);
  const clean = new Uint8Array(await sdrJpegDecodeSource(new Blob([data]), metadata).arrayBuffer());
  assert.equal(inspectImageMetadata(clean).jpegAuxiliary, undefined);
  assert.equal(Buffer.from(clean).includes(Buffer.from("hdrgm")), false);
  assert.equal(Buffer.from(clean).includes(Buffer.from("MPF\0")), false);
  assert.equal(inspectImageMetadata(bytes(jpeg(800, 600, [mpf]), jpeg(100, 100))).jpegAuxiliary.kind, "mpf");
  const hdrBase = inspectImageMetadata(bytes(primary, jpeg(100, 100, [xmp('hdrgm:Version="1.0" hdrgm:BaseRenditionIsHDR="True"')])));
  assert.equal(hdrBase.colorSpace, "unsupported");
  const iso = segment(226, Buffer.from("urn:iso:std:iso:ts:21496:-1\0"));
  assert.equal(inspectImageMetadata(bytes(jpeg(800, 600, [mpf, iso]), jpeg(100, 100))).colorSpace, "unsupported");
  assert.throws(() => inspectImageMetadata(data.subarray(0, data.length - 2)), { code: "IMAGE_INVALID" });
});

test("complete JPEGs with private trailers normalize the primary image without accepting truncation", async () => {
  const primary = jpeg(1080, 2400);
  for (const trailer of [bytes([0, 0]), bytes(Buffer.from("screenshot-metadata")), jpeg(2, 2), bytes([255])]) {
    const source = bytes(primary, trailer);
    const metadata = inspectImageMetadata(source);
    assert.deepEqual([metadata.width, metadata.height], [1080, 2400]);
    assert.equal(metadata.jpegAuxiliary.kind, "trailing-data");
    const clean = new Uint8Array(await sdrJpegDecodeSource(new Blob([source]), metadata).arrayBuffer());
    assert.deepEqual(clean, primary);
    assert.equal(inspectImageMetadata(clean).jpegAuxiliary, undefined);
    assert.equal(planImagePreparation(metadata, source.length, { maxDimension: 1536, version: "image-normalize-v1:1536" }).reuse, false);
  }
  assert.throws(() => inspectImageMetadata(primary.subarray(0, primary.length - 1)), { code: "IMAGE_INVALID" });
  assert.throws(() => inspectImageMetadata(primary.subarray(0, primary.length - 2)), { code: "IMAGE_INVALID" });
  // A trailer cannot repair a truncated segment or substitute for an EOI.
  assert.throws(() => inspectImageMetadata(bytes(primary.subarray(0, 12), Buffer.from("metadata"))), { code: "IMAGE_INVALID" });
});
