// Decode one source at a time; retain only a small, static preview per attachment.
let pending: Promise<unknown> = Promise.resolve();

export type ThumbnailSpec = { size: number; type: string; quality: number };
export const DEFAULT_THUMBNAIL_SPEC: Readonly<ThumbnailSpec> = { size: 160, type: "image/webp", quality: 0.8 };

export function createImageThumbnail(load: () => Promise<Blob>, signal: AbortSignal, spec: Readonly<ThumbnailSpec> = DEFAULT_THUMBNAIL_SPEC): Promise<Blob> {
  const task = pending.catch(() => undefined).then(async () => {
    signal.throwIfAborted();
    const blob = await load();
    signal.throwIfAborted();
    const bitmap = await createImageBitmap(blob);
    let canvas: HTMLCanvasElement | undefined;
    try {
      canvas = document.createElement("canvas");
      signal.throwIfAborted();
      canvas.width = canvas.height = spec.size;
      const context = canvas.getContext("2d");
      if (!context) throw new Error("Canvas unavailable");
      const edge = Math.min(bitmap.width, bitmap.height);
      context.drawImage(bitmap, (bitmap.width - edge) / 2, (bitmap.height - edge) / 2, edge, edge, 0, 0, spec.size, spec.size);
      const outputCanvas = canvas;
      const thumbnail = await new Promise<Blob>((resolve, reject) => {
        outputCanvas.toBlob((result) => result ? resolve(result) : reject(new Error("Thumbnail unavailable")), spec.type, spec.quality);
      });
      signal.throwIfAborted();
      return thumbnail;
    } finally {
      bitmap.close();
      if (canvas) canvas.width = canvas.height = 0;
    }
  });
  pending = task.catch(() => undefined);
  return task;
}
