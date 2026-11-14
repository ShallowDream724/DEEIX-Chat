import { Application, createEncoder } from "libopus-wasm";

// Type assertion: this module runs in a dedicated Worker; the app otherwise compiles with the Window DOM library.
const scope = self as unknown as { onmessage: ((event: MessageEvent<{ frame?: Float32Array; type: string }>) => void) | null; postMessage: (message: unknown, transfer?: Transferable[]) => void };
void createEncoder({ sampleRate: 16000, channels: 1, frameSize: 320, application: Application.Voip, bitrate: 24000, complexity: 5 }).then((encoder) => {
  scope.onmessage = ({ data }) => {
    try {
      if (data.type === "frame" && data.frame) {
        const frame = data.frame.length === 320 ? data.frame : new Float32Array(320);
        if (frame !== data.frame) frame.set(data.frame);
        const packet = encoder.encodeFloat(frame, { maxPacketBytes: 1275 }).slice();
        scope.postMessage({ type: "packet", packet }, [packet.buffer]);
      }
      if (data.type === "flush") scope.postMessage({ type: "flushed" });
      if (data.type === "close") encoder.free();
    } catch { scope.postMessage({ type: "error" }); }
  };
  scope.postMessage({ type: "ready" });
}).catch(() => scope.postMessage({ type: "error" }));
