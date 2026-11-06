/** Probe a bounded prefix before decoding an unknown or mislabeled binary file. */
export async function readPreviewText(blob: Blob, readable: (text: string) => boolean, signal: AbortSignal): Promise<string | null> {
  signal.throwIfAborted();
  const prefix = await blob.slice(0, 8192).text();
  signal.throwIfAborted();
  const zipSignature = prefix.startsWith("PK") && [3, 5, 7].includes(prefix.charCodeAt(2)) && prefix.charCodeAt(3) === prefix.charCodeAt(2) + 1;
  if (zipSignature || !readable(prefix)) return null;
  const text = blob.size <= 8192 ? prefix : await blob.text();
  signal.throwIfAborted();
  return text;
}
