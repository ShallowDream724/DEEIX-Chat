import { canonicalizeSrgbCanvasOutput, ImagePreparationError, inspectImageMetadata, sdrJpegDecodeSource } from "./image-metadata.ts";
import { IMAGE_SOURCE_BYTE_LIMIT, planImagePreparation } from "./image-preparation-plan.ts";
import type { ImageSourceInspection, ImageWorkerRequest, PreparedImageSource } from "./image-preparation.ts";

type NativeImageDecoder = {
  tracks: { ready: Promise<void>; selectedTrack?: { animated: boolean; frameCount: number } };
  decode(options: { frameIndex: number; completeFramesOnly: boolean }): Promise<{ image: VideoFrame }>;
  close(): void;
};
type NativeImageDecoderConstructor = {
  new(options: { type: string; data: ReadableStream<Uint8Array>; desiredWidth: number; desiredHeight: number; colorSpaceConversion: "default"; preferAnimation: boolean }): NativeImageDecoder;
  isTypeSupported(type: string): Promise<boolean>;
};

const unavailable = (message: string) => new ImagePreparationError("IMAGE_CAPABILITY_UNAVAILABLE", message);

export async function processImageWorkerRequest(request: ImageWorkerRequest): Promise<ImageSourceInspection | PreparedImageSource> {
  const { source } = request;
  if (!source.size || source.size > IMAGE_SOURCE_BYTE_LIMIT) throw new ImagePreparationError("IMAGE_MEMORY_LIMIT", "The image exceeds the bounded source byte budget.");
  if (request.kind === "inspect") {
    if (!globalThis.crypto?.subtle) throw unavailable("Native SHA-256 is unavailable.");
    // Native digest requires a whole buffer. A hard 64 MiB cap and the shared
    // scheduler reserve three source copies, including browser digest scratch.
    const buffer = await source.arrayBuffer();
    const metadata = inspectImageMetadata(new Uint8Array(buffer));
    const digest = await crypto.subtle.digest("SHA-256", buffer);
    const sourceSha256 = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
    return { sourceSha256, sourceSize: source.size, metadata };
  }
  const { inspection, policy } = request;
  const metadata = inspection.metadata;
  const plan = planImagePreparation(metadata, source.size, policy);
  if (plan.reuse) return { file: source, metadata, policyVersion: policy.version, reusedOriginal: true };
  if (typeof OffscreenCanvas === "undefined") throw unavailable("Background sRGB canvas encoding is unavailable.");
  const decodeSource = sdrJpegDecodeSource(source, metadata);
  let image: ImageBitmap | VideoFrame | undefined;
  let decoder: NativeImageDecoder | undefined;
  let canvas: OffscreenCanvas | undefined;
  try {
    // Type assertion: the browser implements ImageDecoder, but our DOM library
    // does not yet declare it; this optional structural type fills that gap.
    const Decoder = (globalThis as unknown as { ImageDecoder?: NativeImageDecoderConstructor }).ImageDecoder;
    // createImageBitmap has standardized EXIF handling; use it for transformed
    // sources until VideoFrame rotation/flip support is consistent in browsers.
    if (Decoder && metadata.orientation === 1 && await Decoder.isTypeSupported(metadata.mime)) {
      try {
        decoder = new Decoder({ type: metadata.mime, data: decodeSource.stream(), desiredWidth: plan.decodeWidth, desiredHeight: plan.decodeHeight, colorSpaceConversion: "default", preferAnimation: true });
        await decoder.tracks.ready;
        // `animated` describes the track's animation capability, including a
        // single-frame GIF. Container inspection already counted its frames;
        // only an actual additional decoder frame contradicts that result.
        if ((decoder.tracks.selectedTrack?.frameCount ?? 1) > 1) throw new ImagePreparationError("IMAGE_ANIMATED", "Animated images require an animation-preserving upload route.");
        image = (await decoder.decode({ frameIndex: 0, completeFramesOnly: true })).image;
        const transfer = String(image.colorSpace.transfer);
        if (["pq", "hlg", "smpte2084", "arib-std-b67"].includes(transfer)) throw new ImagePreparationError("IMAGE_COLOR_UNSUPPORTED", "The decoded image requires an unsupported HDR tone-mapping path.");
        // 10/16-bit storage precision is not HDR by itself. WebCodecs requires
        // VideoFrame rendering to convert to the target's color space; native
        // drawImage + unorm8 performs the SDR precision conversion below.
        // Desired dimensions are best effort. Do not upscale an undersized
        // decoder result; fall back to a full-size bitmap within our budget.
        if (image.displayWidth < plan.width || image.displayHeight < plan.height) { image.close(); image = undefined; }
      } catch (error) {
        image?.close();
        image = undefined;
        if (error instanceof ImagePreparationError) throw error;
        // A supported codec can still lack this source's native decode path.
      } finally {
        decoder?.close();
        decoder = undefined;
      }
    }
    if (!image) {
      if (typeof createImageBitmap === "undefined") throw unavailable("Background image decoding is unavailable.");
      image = await createImageBitmap(decodeSource, { imageOrientation: "from-image", colorSpaceConversion: "default", premultiplyAlpha: "default" });
      const rotated = metadata.orientation >= 5;
      const expectedWidth = rotated ? metadata.height : metadata.width;
      const expectedHeight = rotated ? metadata.width : metadata.height;
      if (image.width !== expectedWidth || image.height !== expectedHeight) throw unavailable("This browser cannot reliably apply the image's orientation.");
    }
    canvas = new OffscreenCanvas(plan.width, plan.height);
    const context = canvas.getContext("2d", { alpha: metadata.hasAlpha, colorSpace: "srgb", colorType: "unorm8" });
    if (!context) throw unavailable("An SDR sRGB canvas is unavailable.");
    const sample = context.getImageData(0, 0, 1, 1);
    if (sample.colorSpace !== "srgb" || !(sample.data instanceof Uint8ClampedArray)) throw unavailable("An SDR sRGB canvas is unavailable.");
    // Canvas is newly cleared; transparent PNG keeps RGBA instead of flattening.
    context.imageSmoothingEnabled = true;
    context.imageSmoothingQuality = "high";
    context.drawImage(image, 0, 0, plan.width, plan.height);
    const blob = await canvas.convertToBlob({ type: plan.mime, ...(plan.mime === "image/jpeg" ? { quality: 0.9 } : {}) });
    if (blob.type !== plan.mime || !blob.size) throw unavailable("The browser cannot encode the required image format.");
    if (plan.mime === "image/png") {
      const header = new Uint8Array(await blob.slice(0, 33).arrayBuffer());
      if (header.length !== 33 || header[0] !== 137 || header[1] !== 80 || header[2] !== 78 || header[3] !== 71 || header[24] !== 8) throw unavailable("The browser did not produce the required PNG8 output.");
    }
    const extension = plan.mime === "image/jpeg" ? ".jpg" : ".png";
    const name = source.name.replace(/\.[^.]*$/, "") + extension;
    const canonical = await canonicalizeSrgbCanvasOutput(blob);
    const file = new File([canonical], name, { type: plan.mime, lastModified: source.lastModified });
    return { file, metadata: { ...metadata, format: plan.mime === "image/jpeg" ? "jpeg" : "png", mime: plan.mime, width: plan.width, height: plan.height, orientation: 1, animated: false, bitDepth: 8, colorSpace: "srgb", sdrColorConfirmed: true, jpegAuxiliary: undefined }, policyVersion: policy.version, reusedOriginal: false };
  } catch (error) {
    if (error instanceof ImagePreparationError) throw error;
    if (error instanceof DOMException && error.name === "NotSupportedError") throw unavailable("This image is not supported by the browser decoder.");
    throw new ImagePreparationError("IMAGE_INVALID", "The image could not be decoded or encoded.");
  } finally {
    image?.close();
    decoder?.close();
    if (canvas) { canvas.width = 0; canvas.height = 0; }
  }
}

// Type assertion: this file runs in a dedicated worker while the app's TS config
// loads DOM globals; use the worker's messaging shape without changing that lib.
const scope = globalThis as unknown as { postMessage?: (message: unknown) => void; onmessage: ((event: MessageEvent<ImageWorkerRequest>) => void) | null };
if (typeof scope.postMessage === "function" && typeof document === "undefined") {
  scope.onmessage = async event => {
    try { scope.postMessage?.({ ok: true, result: await processImageWorkerRequest(event.data) }); }
    catch (error) {
      const typed = error instanceof ImagePreparationError ? error : new ImagePreparationError("IMAGE_INVALID", "The image could not be inspected.");
      scope.postMessage?.({ ok: false, code: typed.code, message: typed.message });
    }
  };
}
