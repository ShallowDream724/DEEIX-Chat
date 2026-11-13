import { ImagePreparationError, type ImageMetadata } from "./image-metadata.ts";

export const IMAGE_SOURCE_BYTE_LIMIT = 64 * 1024 * 1024;
export const IMAGE_WORKING_BYTE_LIMIT = 256 * 1024 * 1024;
export const IMAGE_DECODE_PIXEL_LIMIT = 64 * 1024 * 1024;
export const IMAGE_PREPARATION_POLICY_VERSION = "image-normalize-v1";

export type ImagePreparationPolicy = { maxDimension: number; version: string };
export type ImagePreparationPlan = { width: number; height: number; decodeWidth: number; decodeHeight: number; mime: "image/png" | "image/jpeg"; reuse: boolean; workingBytes: number };

export function planImagePreparation(metadata: ImageMetadata, sourceSize: number, policy: ImagePreparationPolicy): ImagePreparationPlan {
  if (!Number.isSafeInteger(metadata.width) || !Number.isSafeInteger(metadata.height) || metadata.width <= 0 || metadata.height <= 0 || !Number.isInteger(metadata.orientation) || metadata.orientation < 1 || metadata.orientation > 8) {
    throw new ImagePreparationError("IMAGE_INVALID", "The image dimensions or orientation are invalid.");
  }
  if (!Number.isSafeInteger(policy.maxDimension) || policy.maxDimension < 0 || policy.maxDimension > 32768 || policy.version !== `${IMAGE_PREPARATION_POLICY_VERSION}:${policy.maxDimension}`) {
    throw new ImagePreparationError("IMAGE_UNSUPPORTED", "The image preparation policy is invalid.");
  }
  if (metadata.animated) throw new ImagePreparationError("IMAGE_ANIMATED", "Animated images require an animation-preserving upload route.");
  const supportedPrecision = metadata.bitDepth <= 8 || metadata.format === "png" && metadata.bitDepth === 16 && (metadata.colorSpace === "srgb" || metadata.sdrColorConfirmed);
  if (metadata.colorSpace === "unsupported" || !supportedPrecision) throw new ImagePreparationError("IMAGE_COLOR_UNSUPPORTED", "This image's HDR or color encoding cannot be normalized reliably.");
  if (!Number.isSafeInteger(sourceSize) || sourceSize <= 0 || sourceSize > IMAGE_SOURCE_BYTE_LIMIT) throw new ImagePreparationError("IMAGE_MEMORY_LIMIT", "The image exceeds the bounded source byte budget.");
  const rotated = metadata.orientation >= 5;
  const orientedWidth = rotated ? metadata.height : metadata.width;
  const orientedHeight = rotated ? metadata.width : metadata.height;
  const ratio = policy.maxDimension === 0 ? 1 : Math.min(1, policy.maxDimension / Math.max(orientedWidth, orientedHeight));
  const width = Math.max(1, Math.round(orientedWidth * ratio));
  const height = Math.max(1, Math.round(orientedHeight * ratio));
  const mime = metadata.format === "jpeg" && !metadata.hasAlpha ? "image/jpeg" : "image/png";
  const reuse = ratio === 1 && metadata.orientation === 1 && metadata.colorSpace === "srgb" && (metadata.format === "jpeg" || metadata.format === "png") && metadata.bitDepth === 8 && !metadata.jpegAuxiliary;
  // ImageDecoder desired dimensions are best effort, so reserve full decoded
  // pixels even when requesting native downsampling. Never assume it succeeded.
  const pixels = metadata.width * metadata.height;
  const decodedBytesPerPixel = metadata.bitDepth > 8 ? 8 : 4;
  // Target reservation includes canvas RGBA8, the encoded blob and a bounded
  // readback used to remove redundant color metadata without another encode.
  const workingBytes = sourceSize * 2 + pixels * decodedBytesPerPixel + width * height * 12;
  if (!reuse && (!Number.isSafeInteger(pixels) || pixels > IMAGE_DECODE_PIXEL_LIMIT || workingBytes > IMAGE_WORKING_BYTE_LIMIT)) {
    throw new ImagePreparationError("IMAGE_MEMORY_LIMIT", "This image exceeds the browser's bounded decode budget.");
  }
  // JPEG native scaled DCT works in eighths. Round the requested ratio up so
  // a decoder cannot choose a resolution smaller than the final canvas.
  const decodeRatio = metadata.format === "jpeg" ? Math.min(1, Math.ceil(ratio * 8) / 8) : ratio;
  return { width, height, decodeWidth: Math.ceil(metadata.width * decodeRatio), decodeHeight: Math.ceil(metadata.height * decodeRatio), mime, reuse, workingBytes: reuse ? 0 : workingBytes };
}
