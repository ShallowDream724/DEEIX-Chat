import { beginPlaybackActivity, isRecordingActive } from "@/shared/lib/media-activity";
import { createMediaSpectrum } from "@/shared/lib/media-spectrum";

type PlaybackStatus = "loading" | "playing" | "paused";

/** Owns one playback's browser resources, including cleanup while requests stall. */
export function createSpeechPlaybackLifecycle(context: AudioContext, messageID: string, onState: (status: PlaybackStatus) => void, onStop: () => void) {
  const controller = new AbortController();
  const analyzer = context.createAnalyser();
  const spectrum = createMediaSpectrum(analyzer, context.sampleRate);
  const waveform = new Float32Array(analyzer.fftSize);
  const waiters = new Set<() => void>();
  const operations = new Set<() => void>();
  const sources = new Set<AudioBufferSourceNode>();
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let activity: ReturnType<typeof beginPlaybackActivity> | undefined;
  let analysisTimer: ReturnType<typeof setInterval> | undefined;
  let disposed = false;
  let userPaused = false;
  let intentGeneration = 0;
  let hasAudio = false;
  let autoResumeAttempted = false;
  let resuming: Promise<boolean> | undefined;
  let suspending: Promise<boolean> | undefined;
  const ensureAlive = () => { if (disposed || controller.signal.aborted) throw new DOMException("Aborted", "AbortError"); };
  const wake = () => { for (const resolve of [...waiters]) resolve(); };
  const stopAnalysis = () => {
    clearInterval(analysisTimer); analysisTimer = undefined;
    activity?.stop(); activity = undefined;
  };
  const sync = () => {
    if (disposed) return;
    if (context.state === "closed") { onStop(); return; }
    const running = context.state === "running" && !userPaused;
    onState(running ? hasAudio ? "playing" : "loading" : "paused");
    if (!running || document.hidden || !hasAudio) { stopAnalysis(); return; }
    if (!activity) activity = beginPlaybackActivity(messageID, "tts", onStop);
    if (disposed || analysisTimer !== undefined) return;
    analysisTimer = setInterval(() => {
      if (disposed || document.hidden || context.state !== "running" || userPaused) { stopAnalysis(); return; }
      analyzer.getFloatTimeDomainData(waveform);
      let squared = 0; for (const value of waveform) squared += value * value;
      activity?.publishLevel(Math.min(1, Math.sqrt(squared / waveform.length) * 4), spectrum.sample());
    }, 34);
  };
  const boundedOperation = (action: () => Promise<void>) => new Promise<boolean>((resolve) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (success: boolean) => {
      if (settled) return;
      settled = true; clearTimeout(timer); operations.delete(cancel); resolve(success && !disposed);
    };
    const cancel = () => finish(false);
    operations.add(cancel);
    timer = setTimeout(cancel, 3000);
    try { void action().then(() => finish(true), () => finish(false)); } catch { finish(false); }
  });
  const resume = (manual = false): Promise<boolean> => {
    if (disposed || isRecordingActive()) return Promise.resolve(false);
    if (manual) { userPaused = false; intentGeneration++; }
    if (userPaused) return Promise.resolve(false);
    if (!suspending && context.state === "running") { sync(); wake(); return Promise.resolve(true); }
    if (resuming) return resuming;
    const attempt = () => {
      if (disposed || userPaused || isRecordingActive()) return Promise.resolve(false);
      return context.state === "running" ? Promise.resolve(true) : boundedOperation(() => context.resume());
    };
    resuming = (suspending ? suspending.then(attempt) : attempt()).then((success) => {
      resuming = undefined;
      if (!disposed) { sync(); wake(); }
      return success && !userPaused && context.state === "running";
    });
    return resuming;
  };
  const suspend = (): Promise<boolean> => {
    if (suspending) return suspending;
    const generation = intentGeneration;
    suspending = boundedOperation(() => context.suspend().then(() => {
      // A native suspend may finish even after its deadline. Honor a newer
      // Continue click once, without reviving disposed or explicitly paused audio.
      if (!disposed && !userPaused && generation !== intentGeneration) void resume();
    })).then((success) => {
      suspending = undefined;
      if (!disposed) { sync(); wake(); }
      return success;
    });
    return suspending;
  };
  const stateChanged = () => {
    if (disposed) return;
    if (context.state === "running") {
      autoResumeAttempted = false;
      if (userPaused) void suspend();
    }
    sync(); wake();
  };
  const visible = () => {
    if (disposed) return;
    if (document.hidden) autoResumeAttempted = false;
    sync(); wake();
    if (!document.hidden && !userPaused && context.state !== "running" && context.state !== "closed" && !autoResumeAttempted) {
      autoResumeAttempted = true;
      void resume();
    }
  };
  const pageHidden = () => onStop();
  context.addEventListener("statechange", stateChanged);
  document.addEventListener("visibilitychange", visible);
  document.addEventListener("resume", visible);
  window.addEventListener("pageshow", visible);
  window.addEventListener("pagehide", pageHidden);
  return {
    context, controller, analyzer,
    resume,
    async pause() {
      if (disposed) return;
      userPaused = true; intentGeneration++; sync(); wake();
      await suspend();
    },
    markPlaying() { if (!disposed && !hasAudio) { hasAudio = true; sync(); } },
    async waitUntilRunning() {
      ensureAlive();
      while (userPaused || context.state !== "running") {
        await new Promise<void>((resolve) => { const finish = () => { waiters.delete(finish); resolve(); }; waiters.add(finish); });
        ensureAlive();
      }
    },
    async waitForClock(milliseconds: number) {
      ensureAlive();
      await new Promise<void>((resolve) => {
        const finish = () => { clearTimeout(timer); waiters.delete(finish); resolve(); };
        const timer = setTimeout(finish, milliseconds);
        waiters.add(finish);
      });
      ensureAlive();
    },
    ownReader(next: ReadableStreamDefaultReader<Uint8Array>) { ensureAlive(); reader = next; },
    releaseReader(previous: ReadableStreamDefaultReader<Uint8Array>) { if (reader === previous) reader = undefined; },
    ownSource(source: AudioBufferSourceNode) {
      ensureAlive(); sources.add(source);
      source.onended = () => { sources.delete(source); source.disconnect(); };
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      context.removeEventListener("statechange", stateChanged);
      document.removeEventListener("visibilitychange", visible);
      document.removeEventListener("resume", visible);
      window.removeEventListener("pageshow", visible);
      window.removeEventListener("pagehide", pageHidden);
      stopAnalysis();
      controller.abort();
      for (const cancel of [...operations]) cancel();
      wake();
      void reader?.cancel().catch(() => {}); reader = undefined;
      for (const source of sources) { source.onended = null; try { source.stop(); } catch {} source.disconnect(); }
      sources.clear(); analyzer.disconnect();
      if (context.state !== "closed") void context.close().catch(() => {});
    },
  };
}
