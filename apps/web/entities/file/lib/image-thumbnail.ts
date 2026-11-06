// Decode one source at a time; retain only a small, static preview per attachment.
let pending: Promise<unknown> = Promise.resolve();

export function createImageThumbnail(load: () => Promise<Blob>, signal: AbortSignal): Promise<Blob> {
  const task = pending.catch(() => undefined).then(async () => {
    signal.throwIfAborted();
    const blob = await load();
    signal.throwIfAborted();
    const bitmap = await createImageBitmap(blob);
    const canvas = document.createElement("canvas");
    try {
      signal.throwIfAborted();
      canvas.width = canvas.height = 160;
      const context = canvas.getContext("2d");
      if (!context) throw new Error("Canvas unavailable");
      const edge = Math.min(bitmap.width, bitmap.height);
      context.drawImage(bitmap, (bitmap.width - edge) / 2, (bitmap.height - edge) / 2, edge, edge, 0, 0, 160, 160);
      const thumbnail = await new Promise<Blob>((resolve, reject) => {
        canvas.toBlob((result) => result ? resolve(result) : reject(new Error("Thumbnail unavailable")), "image/webp", 0.8);
      });
      signal.throwIfAborted();
      return thumbnail;
    } finally {
      bitmap.close();
      canvas.width = canvas.height = 0;
    }
  });
  pending = task.catch(() => undefined);
  return task;
}
