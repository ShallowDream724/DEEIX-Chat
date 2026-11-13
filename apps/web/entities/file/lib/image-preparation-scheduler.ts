import { ImagePreparationError } from "./image-metadata.ts";
import { IMAGE_WORKING_BYTE_LIMIT } from "./image-preparation-plan.ts";

type QueueEntry = { bytes: number; signal: AbortSignal; run: () => Promise<void>; reject: (error: unknown) => void; onAbort: () => void };

/** Shared byte budget applies to hashing and decoding; at most two workers. */
export class ImagePreparationScheduler {
  private queue: QueueEntry[] = [];
  private active = 0;
  private bytes = 0;
  private readonly maxActive: number;
  private readonly maxBytes: number;
  constructor(maxActive = 2, maxBytes = IMAGE_WORKING_BYTE_LIMIT) {
    this.maxActive = maxActive;
    this.maxBytes = maxBytes;
  }

  schedule<T>(bytes: number, signal: AbortSignal, run: () => Promise<T>): Promise<T> {
    signal.throwIfAborted();
    if (bytes > this.maxBytes || bytes < 0 || !Number.isFinite(bytes)) return Promise.reject(new ImagePreparationError("IMAGE_MEMORY_LIMIT", "The image task exceeds the available byte budget."));
    return new Promise<T>((resolve, reject) => {
      const entry: QueueEntry = { bytes, signal, run: async () => { resolve(await run()); }, reject, onAbort: () => {
        const index = this.queue.indexOf(entry);
        if (index < 0) return;
        this.queue.splice(index, 1);
        signal.removeEventListener("abort", entry.onAbort);
        reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
        this.drain();
      } };
      signal.addEventListener("abort", entry.onAbort, { once: true });
      this.queue.push(entry);
      this.drain();
    });
  }

  private drain() {
    while (this.queue.length && this.active < this.maxActive) {
      // FIFO prevents a large task from starving behind smaller arrivals.
      const entry = this.queue[0];
      if (this.bytes + entry.bytes > this.maxBytes) break;
      this.queue.shift();
      entry.signal.removeEventListener("abort", entry.onAbort);
      this.active++;
      this.bytes += entry.bytes;
      Promise.resolve().then(() => { entry.signal.throwIfAborted(); return entry.run(); }).catch(entry.reject).finally(() => {
        this.active--;
        this.bytes -= entry.bytes;
        this.drain();
      });
    }
  }
}
