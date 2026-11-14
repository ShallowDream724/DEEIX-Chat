/** Container inspection only: no image decoder or pixel allocation. */
export type ImageFormat = "jpeg" | "png" | "webp" | "gif";
export type ImageMetadata = {
  format: ImageFormat;
  mime: string;
  /** Encoded dimensions, before EXIF orientation. */
  width: number;
  height: number;
  orientation: number;
  animated: boolean;
  hasAlpha: boolean;
  bitDepth: number;
  colorSpace: "srgb" | "icc" | "unsupported";
  /** Explicit SDR signaling for higher-depth wide-gamut PNG inputs. */
  sdrColorConfirmed?: boolean;
  /** Decode only this standalone SDR primary, removing auxiliary declarations. */
  jpegAuxiliary?: { kind: "mpf" | "gainmap-sdr" | "trailing-data"; primaryByteLength: number; stripRanges: [number, number][] };
};

export type ImagePreparationErrorCode = "IMAGE_INVALID" | "IMAGE_UNSUPPORTED" | "IMAGE_ANIMATED" | "IMAGE_COLOR_UNSUPPORTED" | "IMAGE_MEMORY_LIMIT" | "IMAGE_CAPABILITY_UNAVAILABLE" | "IMAGE_TIMEOUT";
export class ImagePreparationError extends Error {
  code: ImagePreparationErrorCode;
  constructor(code: ImagePreparationErrorCode, message: string) {
    super(message);
    this.code = code;
    this.name = "ImagePreparationError";
  }
}

const invalid = (): never => { throw new ImagePreparationError("IMAGE_INVALID", "The image container is invalid or incomplete."); };
const ascii = (data: Uint8Array, start: number, length: number) => String.fromCharCode(...data.subarray(start, start + length));
const be16 = (data: Uint8Array, start: number) => (data[start] << 8) | data[start + 1];
const be32 = (data: Uint8Array, start: number) => new DataView(data.buffer, data.byteOffset, data.byteLength).getUint32(start);
const le32 = (data: Uint8Array, start: number) => new DataView(data.buffer, data.byteOffset, data.byteLength).getUint32(start, true);

type JpegSegment = { marker: number; start: number; payload: number; end: number };
/** Walk JPEG markers and byte-stuffed entropy; this does not decode samples. */
function scanJpeg(data: Uint8Array, start: number): { end: number; segments: JpegSegment[] } {
  if (data[start] !== 0xff || data[start + 1] !== 0xd8) return invalid();
  const segments: JpegSegment[] = [];
  let offset = start + 2;
  let entropy = false;
  let sawScan = false;
  while (offset < data.length) {
    if (entropy) while (offset < data.length && data[offset] !== 0xff) offset++;
    const markerStart = offset;
    if (data[offset++] !== 0xff) return invalid();
    while (data[offset] === 0xff) offset++;
    const marker = data[offset++];
    if (entropy && (marker === 0 || marker >= 0xd0 && marker <= 0xd7)) continue;
    if (marker === 0xd9) {
      if (!sawScan) return invalid();
      return { end: offset, segments };
    }
    if (marker === 0xd8 || marker === 0 || offset + 2 > data.length) return invalid();
    const length = be16(data, offset);
    if (length < 2 || offset + length > data.length || segments.length >= 65536) return invalid();
    segments.push({ marker, start: markerStart, payload: offset + 2, end: offset + length });
    offset += length;
    entropy = marker === 0xda;
    sawScan ||= entropy;
  }
  return invalid();
}

function inspectJpeg(data: Uint8Array, metadata: ImageMetadata) {
  const primary = scanJpeg(data, 0);
  const stripRanges: [number, number][] = [];
  let hasMpf = false;
  let hasGainmap = false;
  let hasAdobeSdrVersion = false;
  let hasIsoGainmap = false;
  let unknownGainmap = false;
  let hdrPrimary = false;
  const inspectSegments = (segments: JpegSegment[], isPrimary: boolean) => {
    for (const segment of segments) {
      const { marker, payload: start, end } = segment;
      if (isPrimary && [0xc0, 0xc1, 0xc2].includes(marker)) {
        if (end - start < 6 || metadata.width) return invalid();
        metadata.bitDepth = data[start];
        metadata.height = be16(data, start + 1);
        metadata.width = be16(data, start + 3);
        if (data[start + 5] !== 1 && data[start + 5] !== 3) metadata.colorSpace = "unsupported";
      } else if (isPrimary && marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker)) metadata.colorSpace = "unsupported";
      else if (isPrimary && marker === 0xe1 && ascii(data, start, 6) === "Exif\0\0") metadata.orientation = exifOrientation(data.subarray(start, end));
      else if (isPrimary && marker === 0xe2 && ascii(data, start, 12) === "ICC_PROFILE\0" && metadata.colorSpace !== "unsupported") metadata.colorSpace = "icc";
      if (marker === 0xe2 && ascii(data, start, 4) === "MPF\0") {
        hasMpf = true;
        if (isPrimary) stripRanges.push([segment.start, end]);
      }
      if (marker === 0xe2 && ascii(data, start, 27) === "urn:iso:std:iso:ts:21496:-1") {
        hasIsoGainmap = true;
        if (isPrimary) stripRanges.push([segment.start, end]);
      }
      if (marker !== 0xe1 || ascii(data, start, 6) === "Exif\0\0") continue;
      const text = ascii(data, start, end - start);
      if (isPrimary) stripRanges.push([segment.start, end]);
      if (!/hdr-gain-map|hdrgm:|21496/i.test(text)) continue;
      hasGainmap = true;
      const namespace = text.match(/xmlns:([\w.-]+)\s*=\s*["']http:\/\/ns\.adobe\.com\/hdr-gain-map\/1\.0\/["']/);
      if (!namespace || /<!DOCTYPE|<!ENTITY/i.test(text)) { unknownGainmap = true; continue; }
      const prefix = namespace[1].replace(/\./g, "\\.");
      const value = (name: string) => text.match(new RegExp(`${prefix}:${name}\\s*=\\s*["']([^"']*)["']`))?.[1] ?? text.match(new RegExp(`<${prefix}:${name}\\s*>([^<]*)</${prefix}:${name}\\s*>`))?.[1];
      const version = value("Version");
      const baseHdr = value("BaseRenditionIsHDR");
      if (version === "1.0") hasAdobeSdrVersion = true;
      else if (version !== undefined) unknownGainmap = true;
      if (baseHdr !== undefined && baseHdr.toLowerCase() !== "false") hdrPrimary = true;
    }
  };
  inspectSegments(primary.segments, true);
  // Inspect auxiliary JPEG headers too: Adobe's BaseRenditionIsHDR property is
  // normally stored on the secondary gain map, not just the primary image.
  if (hasMpf || hasGainmap || hasIsoGainmap) {
    let offset = primary.end;
    let images = 1;
    while (offset < data.length) {
      while (offset < data.length && data[offset] === 0) offset++;
      if (offset === data.length) break;
      if (++images > 256) return invalid();
      const auxiliary = scanJpeg(data, offset);
      inspectSegments(auxiliary.segments, false);
      offset = auxiliary.end;
    }
    // MPF is a multi-picture index, not an HDR transfer function. A known Adobe
    // v1 SDR base can be separated from its gain map without tone mapping.
    if (hdrPrimary || unknownGainmap || (hasGainmap || hasIsoGainmap) && !hasAdobeSdrVersion) metadata.colorSpace = "unsupported";
    else metadata.jpegAuxiliary = { kind: hasGainmap || hasIsoGainmap ? "gainmap-sdr" : "mpf", primaryByteLength: primary.end, stripRanges };
  } else if (primary.end !== data.length) {
    // Editors and screenshot tools may append private data after a complete
    // JPEG. The first codestream's EOI establishes its boundary; trailing
    // bytes do not make those pixels incomplete. Decode only that codestream,
    // while declared MPF/gain-map images still use the checks above.
    metadata.jpegAuxiliary = { kind: "trailing-data", primaryByteLength: primary.end, stripRanges: [] };
  }
}

/** Blob slices retain only the SDR JPEG codestream and its EXIF/ICC metadata. */
export function sdrJpegDecodeSource(source: Blob, metadata: ImageMetadata): Blob {
  const auxiliary = metadata.jpegAuxiliary;
  if (!auxiliary) return source;
  const parts: Blob[] = [];
  let start = 0;
  for (const [from, to] of auxiliary.stripRanges) {
    if (from < start || to < from || to > auxiliary.primaryByteLength) return invalid();
    parts.push(source.slice(start, from));
    start = to;
  }
  parts.push(source.slice(start, auxiliary.primaryByteLength));
  return new Blob(parts, { type: "image/jpeg" });
}

/**
 * Only call for a NEW serialization of a verified sRGB/unorm8 canvas. Removing
 * its redundant color declarations changes no encoded pixels and makes the
 * canonical SDR result reusable after selection in a later session. Unknown
 * source ICC profiles must still go through native color conversion first.
 */
export async function canonicalizeSrgbCanvasOutput(blob: Blob): Promise<Blob> {
  const data = new Uint8Array(await blob.arrayBuffer());
  const ranges: [number, number][] = [];
  if (blob.type === "image/jpeg") {
    const jpeg = scanJpeg(data, 0);
    if (jpeg.end !== data.length) return invalid();
    for (const segment of jpeg.segments) {
      if (segment.marker === 0xe2 && ascii(data, segment.payload, 12) === "ICC_PROFILE\0") ranges.push([segment.start, segment.end]);
    }
  } else if (blob.type === "image/png") {
    // Preserve every IDAT byte and CRC. Whole ancillary chunks can be removed
    // without recomputing checksums or invoking the image encoder again.
    inspectImageMetadata(data);
    for (let offset = 8; offset < data.length;) {
      const end = offset + 12 + be32(data, offset);
      if (["iCCP", "cICP", "gAMA", "cHRM"].includes(ascii(data, offset + 4, 4))) ranges.push([offset, end]);
      offset = end;
    }
  } else return invalid();
  if (!ranges.length) return blob;
  const parts: Blob[] = [];
  let start = 0;
  for (const [from, to] of ranges) { parts.push(blob.slice(start, from)); start = to; }
  parts.push(blob.slice(start));
  return new Blob(parts, { type: blob.type });
}

function exifOrientation(data: Uint8Array): number {
  // EXIF is optional metadata. Some screenshot exporters write an unspecified
  // orientation (including LONG/0); it must not invalidate the image container.
  // Only a well-formed SHORT orientation can override the native default.
  const start = ascii(data, 0, 6) === "Exif\0\0" ? 6 : 0;
  if (data.length < start + 8) return 1;
  const order = ascii(data, start, 2);
  if (order !== "II" && order !== "MM") return 1;
  const view = new DataView(data.buffer, data.byteOffset + start, data.byteLength - start);
  const little = order === "II";
  if (view.getUint16(2, little) !== 42) return 1;
  const offset = view.getUint32(4, little);
  if (offset < 8 || offset + 2 > view.byteLength) return 1;
  const count = view.getUint16(offset, little);
  if (offset + 2 + count * 12 > view.byteLength) return 1;
  for (let i = 0; i < count; i++) {
    const entry = offset + 2 + i * 12;
    if (view.getUint16(entry, little) !== 0x112) continue;
    if (view.getUint16(entry + 2, little) !== 3 || view.getUint32(entry + 4, little) !== 1) return 1;
    const value = view.getUint16(entry + 8, little);
    if (value < 1 || value > 8) return 1;
    return value;
  }
  return 1;
}

export function inspectImageMetadata(data: Uint8Array): ImageMetadata {
  const metadata: ImageMetadata = { format: "jpeg", mime: "image/jpeg", width: 0, height: 0, orientation: 1, animated: false, hasAlpha: false, bitDepth: 8, colorSpace: "srgb" };
  if (data.length < 12) return invalid();
  if (ascii(data, 0, 8) === "\x89PNG\r\n\x1a\n") {
    metadata.format = "png";
    metadata.mime = "image/png";
    let end = false;
    let pixels = false;
    let colorPriority = 0;
    const setColor = (priority: number, space: ImageMetadata["colorSpace"], confirmed = false) => {
      if (priority < colorPriority) return;
      colorPriority = priority;
      metadata.colorSpace = space;
      metadata.sdrColorConfirmed = confirmed;
    };
    for (let offset = 8; offset < data.length;) {
      if (offset + 12 > data.length) return invalid();
      const length = be32(data, offset);
      const type = ascii(data, offset + 4, 4);
      const start = offset + 8;
      if (start + length + 4 > data.length) return invalid();
      if (offset === 8 && (type !== "IHDR" || length !== 13)) return invalid();
      if (type === "IHDR") {
        if (offset !== 8 || length !== 13) return invalid();
        metadata.width = be32(data, start);
        metadata.height = be32(data, start + 4);
        metadata.bitDepth = data[start + 8];
        const colorType = data[start + 9];
        if (![0, 2, 3, 4, 6].includes(colorType) || data[start + 10] !== 0 || data[start + 11] !== 0 || data[start + 12] > 1) return invalid();
        const validDepths = colorType === 3 ? [1, 2, 4, 8] : colorType === 0 ? [1, 2, 4, 8, 16] : [8, 16];
        if (!validDepths.includes(metadata.bitDepth)) return invalid();
        metadata.hasAlpha = colorType === 4 || colorType === 6;
      } else if (type === "acTL") {
        if (length !== 8 || be32(data, start) === 0) return invalid();
        metadata.animated = true;
      } else if (type === "tRNS") metadata.hasAlpha = true;
      else if (type === "iCCP") setColor(50, "icc");
      else if (type === "sRGB") {
        if (length !== 1 || data[start] > 3) return invalid();
        setColor(80, "srgb", true);
      }
      else if (type === "cICP") {
        if (length !== 4) return invalid();
        // PNG3 gives cICP precedence over all other color chunks. Full-range
        // sRGB and Display-P3 are SDR regardless of 8/16-bit sample precision.
        const sdr = [1, 12].includes(data[start]) && data[start + 1] === 13 && data[start + 2] === 0 && data[start + 3] === 1;
        setColor(100, sdr ? data[start] === 1 ? "srgb" : "icc" : "unsupported", sdr);
      } else if (type === "gAMA") {
        if (length !== 4 || be32(data, start) === 0) return invalid();
        if (be32(data, start) !== 45455) setColor(30, "icc");
      } else if (type === "cHRM") setColor(30, "icc");
      else if (type === "eXIf") metadata.orientation = exifOrientation(data.subarray(start, start + length));
      else if (type === "IDAT") pixels = true;
      else if (type === "IEND") {
        if (length !== 0 || start + 4 !== data.length) return invalid();
        end = true;
        break;
      }
      offset = start + length + 4;
    }
    if (!end || !pixels) return invalid();
  } else if (data[0] === 0xff && data[1] === 0xd8) {
    inspectJpeg(data, metadata);
  } else if (ascii(data, 0, 4) === "RIFF" && ascii(data, 8, 4) === "WEBP") {
    metadata.format = "webp";
    metadata.mime = "image/webp";
    if (le32(data, 4) + 8 !== data.length) return invalid();
    let pixels = false;
    for (let offset = 12; offset < data.length;) {
      if (offset + 8 > data.length) return invalid();
      const type = ascii(data, offset, 4);
      const length = le32(data, offset + 4);
      const start = offset + 8;
      if (start + length > data.length) return invalid();
      if (type === "VP8X") {
        if (length !== 10) return invalid();
        metadata.animated = !!(data[start] & 2);
        metadata.hasAlpha = !!(data[start] & 16);
        metadata.width = 1 + data[start + 4] + (data[start + 5] << 8) + (data[start + 6] << 16);
        metadata.height = 1 + data[start + 7] + (data[start + 8] << 8) + (data[start + 9] << 16);
      } else if (type === "VP8 ") {
        if (length < 10 || ascii(data, start + 3, 3) !== "\x9d\x01\x2a") return invalid();
        if (!metadata.width) {
          metadata.width = (data[start + 6] | data[start + 7] << 8) & 0x3fff;
          metadata.height = (data[start + 8] | data[start + 9] << 8) & 0x3fff;
        }
        pixels = true;
      } else if (type === "VP8L") {
        if (length < 5 || data[start] !== 0x2f) return invalid();
        const bits = le32(data, start + 1);
        if (!metadata.width) { metadata.width = 1 + (bits & 0x3fff); metadata.height = 1 + ((bits >>> 14) & 0x3fff); }
        metadata.hasAlpha ||= !!(bits & 0x10000000);
        pixels = true;
      } else if (type === "ANIM" || type === "ANMF") { metadata.animated = true; pixels = true; }
      else if (type === "ALPH") metadata.hasAlpha = true;
      else if (type === "ICCP") metadata.colorSpace = "icc";
      else if (type === "EXIF") metadata.orientation = exifOrientation(data.subarray(start, start + length));
      offset = start + length + (length & 1);
      if (offset > data.length) return invalid();
    }
    if (!pixels) return invalid();
  } else if (["GIF87a", "GIF89a"].includes(ascii(data, 0, 6))) {
    metadata.format = "gif";
    metadata.mime = "image/gif";
    metadata.width = data[6] | data[7] << 8;
    metadata.height = data[8] | data[9] << 8;
    let offset = 13 + ((data[10] & 0x80) ? 3 * (1 << ((data[10] & 7) + 1)) : 0);
    let frames = 0;
    let end = false;
    while (offset < data.length) {
      const block = data[offset++];
      if (block === 0x3b) { end = true; break; }
      if (block === 0x2c) {
        if (offset + 9 > data.length) return invalid();
        const flags = data[offset + 8];
        offset += 9 + ((flags & 0x80) ? 3 * (1 << ((flags & 7) + 1)) : 0);
        if (offset >= data.length || data[offset] < 2 || data[offset] > 8) return invalid();
        offset++;
        frames++;
      } else if (block === 0x21) {
        const label = data[offset++];
        if (label === 0xf9 && data[offset] === 4) metadata.hasAlpha ||= !!(data[offset + 1] & 1);
      } else return invalid();
      while (true) {
        if (offset >= data.length) return invalid();
        const length = data[offset++];
        if (length === 0) break;
        offset += length;
        if (offset > data.length) return invalid();
      }
    }
    if (!end || frames === 0) return invalid();
    metadata.animated = frames > 1;
  } else throw new ImagePreparationError("IMAGE_UNSUPPORTED", "This image format cannot be prepared reliably in this browser. Convert it to SDR PNG or JPEG first.");
  if (!metadata.width || !metadata.height) return invalid();
  // Precision alone is not HDR. A 16-bit PNG with conventional sRGB/default
  // samples or explicit SDR cICP can be quantized by the native SDR canvas.
  if (metadata.bitDepth > 8 && (metadata.format !== "png" || metadata.colorSpace !== "srgb" && !metadata.sdrColorConfirmed)) metadata.colorSpace = "unsupported";
  return metadata;
}
