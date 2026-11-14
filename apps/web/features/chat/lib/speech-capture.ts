import { createMediaSpectrum } from "@/shared/lib/media-spectrum";

export type SpeechCapture = { track: MediaStreamTrack; stop: () => Promise<void>; cancel: () => void };
/** The optional start(track) overload has no safe probe; restrict it to the documented desktop Chrome implementation. */
export function supportsBrowserRecognitionTrack() {
  if (typeof navigator === "undefined") return false;
  const ua = navigator.userAgent || "";
  if (/Android|iPhone|iPad|iPod|Mobile|\bwv\b|Edg\/|OPR\/|Electron|QtWebEngine|SamsungBrowser|UCBrowser|YaBrowser|HeadlessChrome/.test(ua)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1)) return false;
  // Type assertion: UA-CH is available in Chromium but is absent from the standard Navigator typing.
  const hints = (navigator as Navigator & { userAgentData?: { brands: { brand: string; version: string }[]; mobile: boolean; platform: string } }).userAgentData;
  if (hints) return !hints.mobile && ["Windows", "macOS", "Linux", "Chrome OS"].includes(hints.platform)
    && hints.brands.some((brand) => brand.brand === "Google Chrome" && Number.parseInt(brand.version, 10) >= 135);
  const version = /\bChrome\/(\d+)/.exec(ua);
  return navigator.vendor === "Google Inc." && /Windows NT|Macintosh|X11|CrOS/.test(ua) && Boolean(version && Number(version[1]) >= 135);
}
export type SpeechCaptureError = "audioUnavailable" | "interrupted" | "permissionDenied";
export async function watchMicrophonePermission(signal: AbortSignal, onDenied: () => void) {
  if (!navigator.permissions || signal.aborted) return;
  try {
    // Type assertion: Chromium supports microphone permissions although lib.dom's PermissionName omits it.
    const permission = await navigator.permissions.query({ name: "microphone" as PermissionName });
    if (signal.aborted) return;
    const changed = () => { if (permission.state === "denied" && !signal.aborted) onDenied(); };
    permission.addEventListener("change", changed);
    signal.addEventListener("abort", () => permission.removeEventListener("change", changed), { once: true });
    changed();
  } catch { /* Permission observation is optional on browsers without this query. */ }
}
export function prepareSpeechCaptureContext() {
  const context = new AudioContext();
  void context.resume().catch(() => {});
  return context;
}

export async function startSpeechCapture(options: {
  maxSamples: number;
  onFrame: (frame: Float32Array, bands?: Float32Array<ArrayBuffer>) => void;
  onLimit: () => void;
  onError: (error: SpeechCaptureError) => void;
  context?: AudioContext;
  signal?: AbortSignal;
  keepTrackUntilCancel?: boolean;
}): Promise<SpeechCapture> {
  if (options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
  const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
  let context: AudioContext | undefined = options.context;
  let node: AudioWorkletNode | undefined;
  let source: MediaStreamAudioSourceNode | undefined;
  let analyser: AnalyserNode | undefined;
  let stopping = false;
  let released = false;
  let tracksReleased = false;
  let armed = false;
  const lifetime = new AbortController();
  const disposers: (() => void)[] = [];
  let resolveStopped: (() => void) | undefined;
  const stopped = new Promise<void>((resolve) => { resolveStopped = resolve; });
  const release = (keepTrack = false) => {
    if (!keepTrack && !tracksReleased) {
      tracksReleased = true;
      for (const track of stream.getTracks()) track.stop();
    }
    if (released) return;
    released = true;
    lifetime.abort();
    for (const dispose of disposers) dispose();
    if (node) { node.port.onmessage = null; node.onprocessorerror = null; }
    try { source?.disconnect(); } catch {}
    try { node?.disconnect(); } catch {}
    try { analyser?.disconnect(); } catch {}
    if (context?.state !== "closed") void context?.close().catch(() => {});
  };
  const interrupted = (error: SpeechCaptureError) => {
    if (released || stopping) return;
    stopping = true;
    release();
    resolveStopped?.();
    options.onError(error);
  };
  try {
    if (options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const track = stream.getAudioTracks()[0];
    if (!track) throw new DOMException("No microphone track", "NotFoundError");
    const abort = () => { stopping = true; release(); resolveStopped?.(); };
    options.signal?.addEventListener("abort", abort, { once: true });
    disposers.push(() => options.signal?.removeEventListener("abort", abort));
    context ??= new AudioContext();
    await context.audioWorklet.addModule("/speech-capture-worklet.js");
    if (released || options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    source = context.createMediaStreamSource(stream);
    analyser = context.createAnalyser();
    const spectrum = createMediaSpectrum(analyser, context.sampleRate);
    source.connect(analyser);
    node = new AudioWorkletNode(context, "speech-capture", { processorOptions: { maxSamples: options.maxSamples } });
    node.port.onmessage = ({ data }: MessageEvent<{ type: string; frame?: Float32Array }>) => {
      if (released) return;
      if (data.type === "frame" && data.frame) {
        try { options.onFrame(data.frame, spectrum.sample()); if (!released) node?.port.postMessage("ack"); }
        catch { interrupted("audioUnavailable"); }
      }
      if (data.type === "error") interrupted("audioUnavailable");
      if (data.type === "stopped") {
        resolveStopped?.();
        // Mark the owning session as stopping before ending any shared recognition track.
        try { if (!stopping) options.onLimit(); }
        finally { release(options.keepTrackUntilCancel); }
      }
    };
    const processorError = () => interrupted("audioUnavailable");
    node.addEventListener("processorerror", processorError);
    disposers.push(() => node?.removeEventListener("processorerror", processorError));
    source.connect(node);
    // The processor has no audio output; connecting keeps its render clock alive.
    node.connect(context.destination);
    await context.resume();
    if (released || options.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    armed = true;
    const stateChanged = () => { if (armed && context?.state !== "running") interrupted("interrupted"); };
    context.addEventListener("statechange", stateChanged);
    disposers.push(() => context?.removeEventListener("statechange", stateChanged));
    for (const track of stream.getAudioTracks()) {
      const ended = () => interrupted("interrupted");
      const muted = () => interrupted("interrupted");
      track.addEventListener("ended", ended); track.addEventListener("mute", muted);
      disposers.push(() => { track.removeEventListener("ended", ended); track.removeEventListener("mute", muted); });
      if (track.readyState === "ended" || track.muted) interrupted("interrupted");
    }
    const deviceChanged = async () => {
      const deviceID = stream.getAudioTracks()[0]?.getSettings().deviceId;
      if (!deviceID || released || stopping) return;
      try {
        const devices = await navigator.mediaDevices.enumerateDevices();
        if (!released && !stopping && !devices.some((device) => device.kind === "audioinput" && device.deviceId === deviceID)) interrupted("interrupted");
      } catch { if (!released && !stopping) interrupted("audioUnavailable"); }
    };
    navigator.mediaDevices.addEventListener("devicechange", deviceChanged);
    disposers.push(() => navigator.mediaDevices.removeEventListener("devicechange", deviceChanged));
    void watchMicrophonePermission(lifetime.signal, () => interrupted("permissionDenied"));
    stateChanged();
    return {
      track,
      stop: async () => {
        stopping = true;
        if (!released) node?.port.postMessage("stop");
        let timer: ReturnType<typeof setTimeout> | undefined;
        try {
          await Promise.race([stopped, new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error("capture stop timeout")), 3000); })]);
        } finally {
          clearTimeout(timer);
          release(options.keepTrackUntilCancel);
        }
      },
      cancel: () => {
        stopping = true;
        if (node) node.port.onmessage = null;
        resolveStopped?.();
        release();
      },
    };
  } catch (error) {
    release();
    throw error;
  }
}

export function pcm16Frame(frame: Float32Array) {
  const pcm = new Int16Array(frame.length);
  for (let i = 0; i < frame.length; i++) pcm[i] = Math.round(Math.max(-1, Math.min(1, frame[i])) * (frame[i] < 0 ? 32768 : 32767));
  return pcm;
}

export function speechWav(frames: Int16Array<ArrayBuffer>[], samples: number) {
  const header = new ArrayBuffer(44);
  const view = new DataView(header);
  const write = (offset: number, text: string) => { for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i)); };
  write(0, "RIFF"); view.setUint32(4, 36 + samples * 2, true); write(8, "WAVEfmt ");
  view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true);
  view.setUint32(24, 16000, true); view.setUint32(28, 32000, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true);
  write(36, "data"); view.setUint32(40, samples * 2, true);
  return new Blob([header, ...frames.map((frame) => frame.buffer)], { type: "audio/wav" });
}

type EncodedAudio = { byteLength: number; copyTo: (destination: Uint8Array) => void };
type AudioEncoderInstance = { encodeQueueSize: number; configure: (config: object) => void; encode: (data: { close: () => void }) => void; flush: () => Promise<void>; close: () => void };
type AudioCodecs = {
  AudioEncoder?: { new (callbacks: { output: (chunk: EncodedAudio) => void; error: (error: Error) => void }): AudioEncoderInstance; isConfigSupported?: (config: object) => Promise<{ supported?: boolean }> };
  AudioData?: new (options: { format: string; sampleRate: number; numberOfFrames: number; numberOfChannels: number; timestamp: number; data: Float32Array }) => { close: () => void };
};
export function supportsSpeechOpus() {
  // Type assertion: TypeScript's DOM library omits the optional AudioEncoder and AudioData APIs.
  const codecs = globalThis as unknown as AudioCodecs;
  return Boolean((codecs.AudioEncoder && codecs.AudioData) || (typeof Worker !== "undefined" && typeof WebAssembly !== "undefined"));
}
function createNativeSpeechOpusEncoder(onPacket: (data: Uint8Array) => void, onError: (error: Error) => void) {
  // Type assertion: TypeScript's DOM library omits the optional AudioEncoder and AudioData APIs.
  const codecs = globalThis as unknown as AudioCodecs;
  if (!codecs.AudioEncoder || !codecs.AudioData) throw new Error("Opus unavailable");
  const AudioData = codecs.AudioData;
  let samples = 0;
  const encoder = new codecs.AudioEncoder({ output: (chunk) => { const packet = new Uint8Array(chunk.byteLength); chunk.copyTo(packet); onPacket(packet); }, error: onError });
  try { encoder.configure({ codec: "opus", sampleRate: 16000, numberOfChannels: 1, bitrate: 24000, opus: { frameDuration: 20000 } }); }
  catch (error) { encoder.close(); throw error; }
  return {
    encode(frame: Float32Array) {
      if (encoder.encodeQueueSize > 50) throw new Error("Speech encoder backpressure");
      const data = new AudioData({ format: "f32-planar", sampleRate: 16000, numberOfFrames: frame.length, numberOfChannels: 1, timestamp: Math.round(samples * 1000000 / 16000), data: frame });
      samples += frame.length;
      try { encoder.encode(data); } finally { data.close(); }
    },
    flush: () => encoder.flush(),
    close: () => encoder.close(),
  };
}

export async function createSpeechOpusEncoder(onPacket: (data: Uint8Array) => void, onError: (error: Error) => void) {
  // Type assertion: TypeScript's DOM library omits the optional AudioEncoder and AudioData APIs.
  const codecs = globalThis as unknown as AudioCodecs;
  if (codecs.AudioEncoder && codecs.AudioData) {
    try {
      const support = await codecs.AudioEncoder.isConfigSupported?.({ codec: "opus", sampleRate: 16000, numberOfChannels: 1, bitrate: 24000, opus: { frameDuration: 20000 } });
      if (!support || support.supported) return createNativeSpeechOpusEncoder(onPacket, onError);
    } catch { /* Use the same PCM source with the portable encoder. */ }
  }
  const worker = new Worker(new URL("./speech-opus-worker.ts", import.meta.url), { type: "module" });
  let queued = 0;
  let flushResolve: (() => void) | undefined;
  let flushReject: ((error: Error) => void) | undefined;
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => { worker.terminate(); reject(new Error("Opus initialization timeout")); }, 10000);
    worker.onmessage = ({ data }: MessageEvent<{ type: string; packet: Uint8Array }>) => {
      if (data.type === "ready") { clearTimeout(timer); resolve(); }
      if (data.type === "packet") { queued--; onPacket(data.packet); }
      if (data.type === "flushed") { flushResolve?.(); flushResolve = undefined; }
      if (data.type === "error") { clearTimeout(timer); worker.terminate(); const error = new Error("Opus encoder failed"); reject(error); flushReject?.(error); onError(error); }
    };
    worker.onerror = () => { clearTimeout(timer); worker.terminate(); const error = new Error("Opus worker failed"); reject(error); flushReject?.(error); onError(error); };
  });
  return {
    encode(frame: Float32Array) {
      if (queued >= 50) throw new Error("Speech encoder backpressure");
      queued++;
      worker.postMessage({ type: "frame", frame }, [frame.buffer]);
    },
    flush: () => new Promise<void>((resolve, reject) => { flushResolve = resolve; flushReject = reject; worker.postMessage({ type: "flush" }); }),
    close: () => { worker.postMessage({ type: "close" }); worker.terminate(); flushResolve?.(); },
  };
}
