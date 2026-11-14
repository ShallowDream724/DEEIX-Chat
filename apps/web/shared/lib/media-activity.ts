"use client";
import { createMediaSpectrum } from "./media-spectrum";

export type MediaActivityKind = "recording" | "audio" | "video" | "tts";
export type MediaActivityFrame = { kind: MediaActivityKind | null; level: number; active: boolean; bands?: Float32Array<ArrayBuffer> };
type CapturableMedia = HTMLMediaElement & { captureStream?: () => MediaStream; mozCaptureStream?: () => MediaStream };
type Playback = { element: HTMLMediaElement; kind: Exclude<MediaActivityKind, "recording">; analyser?: AnalyserNode; source?: MediaStreamAudioSourceNode; stream?: MediaStream; samples?: Float32Array<ArrayBuffer>; spectrum?: ReturnType<typeof createMediaSpectrum>; preparing?: boolean; disposed?: boolean };
type PublishedActivity = { level: number; updatedAt: number; bands?: Float32Array<ArrayBuffer> };

const playbacks = new Set<Playback>();
const externalPlaybacks = new Map<string, PublishedActivity & { kind: "tts"; pause: () => void }>();
const listeners = new Set<(frame: MediaActivityFrame) => void>();
let recording: (PublishedActivity & { id: string }) | null = null;
let context: AudioContext | null = null;
let animationFrame = 0;
let lastFrameAt = 0;
let visibilityAttached = false;

export function isRecordingActive() { return recording !== null; }

function publish(activity: PublishedActivity, level: number, bands?: ArrayLike<number>) {
  activity.level = Number.isFinite(level) ? Math.max(0, Math.min(1, level)) : 0;
  if (bands) {
    activity.bands ??= new Float32Array(8);
    for (let i = 0; i < 8; i++) activity.bands[i] = Number.isFinite(bands[i]) ? Math.max(0, Math.min(1, bands[i])) : 0;
  } else activity.bands?.fill(0);
  activity.updatedAt = performance.now();
}

export function pauseAllPlayback() {
  for (const playback of playbacks) playback.element.pause();
  for (const playback of [...externalPlaybacks.values()]) playback.pause();
  externalPlaybacks.clear();
  refresh();
}

export function beginPlaybackActivity(id: string, kind: "tts", pause: () => void) {
  const playback: PublishedActivity & { kind: "tts"; pause: () => void } = { kind, pause, level: 0, updatedAt: 0 };
  if (recording) { pause(); return { publishLevel(_level: number, _bands?: ArrayLike<number>) {}, stop() {} }; }
  externalPlaybacks.get(id)?.pause();
  externalPlaybacks.set(id, playback);
  refresh();
  return {
    publishLevel(level: number, bands?: ArrayLike<number>) {
      if (externalPlaybacks.get(id) !== playback) return;
      publish(playback, level, bands);
    },
    stop() {
      if (externalPlaybacks.get(id) !== playback) return;
      externalPlaybacks.delete(id);
      refresh();
    },
  };
}

export function beginRecordingActivity(id: string) {
  pauseAllPlayback();
  const session: PublishedActivity & { id: string } = { id, level: 0, updatedAt: 0 };
  recording = session;
  refresh();
  return {
    publishLevel(level: number, bands?: ArrayLike<number>) {
      if (recording !== session) return;
      publish(session, level, bands);
    },
    stop() {
      if (recording !== session) return;
      recording = null;
      refresh();
    },
  };
}

function canPlay(playback: Playback) {
  const media = playback.element;
  return !media.paused && !media.ended && !media.error;
}

function measure(playback: Playback) {
  const { element, analyser, samples } = playback;
  if (element.muted || element.volume === 0 || !analyser || !samples) return 0;
  analyser.getFloatTimeDomainData(samples);
  let power = 0;
  for (const value of samples) power += value * value;
  return Math.min(1, Math.sqrt(power / samples.length) * 4 * element.volume);
}

function snapshot(): MediaActivityFrame {
  if (typeof document === "undefined" || document.hidden) return { kind: null, level: 0, active: false };
  if (recording) { const fresh = performance.now() - recording.updatedAt < 200; return { kind: "recording", level: fresh ? recording.level : 0, bands: fresh ? recording.bands : undefined, active: true }; }
  let selected: Playback | undefined;
  let level = 0;
  let bands: Float32Array<ArrayBuffer> | undefined;
  for (const playback of playbacks) {
    if (!canPlay(playback) || !playback.analyser || playback.element.muted || playback.element.volume === 0) continue;
    const next = measure(playback);
    const spectrum = playback.spectrum?.sample(playback.element.volume);
    if (!selected || next > level) { selected = playback; level = next; bands = spectrum; }
  }
  let selectedKind: MediaActivityKind | null = selected?.kind ?? null;
  for (const playback of externalPlaybacks.values()) {
    const fresh = performance.now() - playback.updatedAt < 200;
    const next = fresh ? playback.level : 0;
    if (!selectedKind || next > level) { selectedKind = playback.kind; level = next; bands = fresh ? playback.bands : undefined; }
  }
  return { kind: selectedKind, level, bands, active: Boolean(selectedKind) };
}

function emit() {
  const frame = snapshot();
  for (const listener of listeners) listener(frame);
}

function hasActivity() {
  return Boolean(recording) || externalPlaybacks.size > 0 || [...playbacks].some((item) => item.analyser && canPlay(item) && !item.element.muted && item.element.volume > 0);
}

function tick(now: number) {
  animationFrame = 0;
  if (now - lastFrameAt >= 1000 / 30) { lastFrameAt = now; emit(); }
  if (listeners.size && !document.hidden && hasActivity()) animationFrame = requestAnimationFrame(tick);
}

function refresh() {
  if (typeof document === "undefined") return;
  emit();
  if (!listeners.size || document.hidden || !hasActivity()) {
    if (animationFrame) cancelAnimationFrame(animationFrame);
    animationFrame = 0;
  } else if (!animationFrame) animationFrame = requestAnimationFrame(tick);
}

async function prepareAnalysis(playback: Playback) {
  if (playback.analyser || playback.preparing || playback.disposed) return;
  // Type assertion: captureStream is supported by browsers but missing from lib.dom media element types.
  const media = playback.element as CapturableMedia;
  const capture = media.captureStream ?? media.mozCaptureStream;
  if (!capture || typeof AudioContext === "undefined") return;
  playback.preparing = true;
  let stream: MediaStream | undefined;
  try {
    // Capturing decoded playback leaves the element's output untouched, including CORS fallbacks.
    stream = capture.call(media);
    if (!stream.getAudioTracks().length) { for (const track of stream.getTracks()) track.stop(); return; }
    context ??= new AudioContext();
    await context.resume();
    if (playback.disposed || !canPlay(playback) || media.muted || media.volume === 0 || recording) { for (const track of stream.getTracks()) track.stop(); return; }
    const source = context.createMediaStreamSource(stream);
    const analyser = context.createAnalyser();
    playback.spectrum = createMediaSpectrum(analyser, context.sampleRate);
    source.connect(analyser);
    playback.stream = stream;
    playback.source = source;
    playback.analyser = analyser;
    playback.samples = new Float32Array(analyser.fftSize);
  } catch {
    for (const track of stream?.getTracks() ?? []) track.stop();
  } finally { playback.preparing = false; refresh(); }
}

function releaseAnalysis(playback: Playback) {
  playback.source?.disconnect();
  playback.analyser?.disconnect();
  for (const track of playback.stream?.getTracks() ?? []) track.stop();
  playback.source = undefined;
  playback.analyser = undefined;
  playback.stream = undefined;
  playback.samples = undefined;
  playback.spectrum = undefined;
}

export function registerMediaActivity(element: HTMLMediaElement, kind: Exclude<MediaActivityKind, "recording">) {
  const existing = [...playbacks].find((item) => item.element === element);
  if (existing) return () => {};
  const playback: Playback = { element, kind };
  playbacks.add(playback);
  const update = () => {
    if (recording && !element.paused) element.pause();
    if (canPlay(playback) && !element.muted && element.volume > 0) void prepareAnalysis(playback);
    else releaseAnalysis(playback);
    if (context && ![...playbacks].some(canPlay)) void context.suspend().catch(() => {});
    refresh();
  };
  const events = ["play", "playing", "pause", "ended", "volumechange", "error", "emptied", "loadeddata"];
  for (const event of events) element.addEventListener(event, update);
  update();
  return () => {
    playback.disposed = true;
    for (const event of events) element.removeEventListener(event, update);
    releaseAnalysis(playback);
    playbacks.delete(playback);
    if (!playbacks.size && context) { const old = context; context = null; void old.close().catch(() => {}); }
    refresh();
  };
}

export function subscribeMediaActivity(listener: (frame: MediaActivityFrame) => void) {
  listeners.add(listener);
  if (!visibilityAttached) { document.addEventListener("visibilitychange", refresh); visibilityAttached = true; }
  refresh();
  return () => {
    listeners.delete(listener);
    if (!listeners.size && visibilityAttached) { document.removeEventListener("visibilitychange", refresh); visibilityAttached = false; }
    refresh();
  };
}
