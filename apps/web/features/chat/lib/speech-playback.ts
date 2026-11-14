import { authedFetch } from "@/shared/api/authed-client";
import { getSpeechSynthesis, synthesizeSpeech, type SpeechSynthesis, type SpeechSynthesisOptions } from "@/shared/api/speech";
import { beginPlaybackActivity, isRecordingActive } from "@/shared/lib/media-activity";
import { createMediaSpectrum } from "@/shared/lib/media-spectrum";
import { normalizeSpeechContent } from "@/features/chat/lib/speech-text";

export type SpeechPlaybackState = { messageID: string; status: "idle" | "loading" | "playing" | "paused" | "error" };
export type SpeechPlaybackConfig = SpeechSynthesisOptions & { scope: string; user: string; token: string; enabled: boolean; maxWeightedCharacters?: number; maxTextBytes?: number };
const idle: SpeechPlaybackState = { messageID: "", status: "idle" };
let state = idle;
const listeners = new Set<() => void>();
type SynthesisEntry = { requestID: string; retry: boolean; pending?: Promise<SpeechSynthesis>; result?: SpeechSynthesis; error?: unknown };
const cache = new Map<string, SynthesisEntry>();
let config: SpeechPlaybackConfig = { scope: "", user: "", token: "", provider: "", voice: "", enabled: false };
let active: { controller: AbortController; context: AudioContext; identity: string; text: string; resume: () => void; activity?: ReturnType<typeof beginPlaybackActivity> } | null = null;
export function subscribeSpeechPlayback(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
export function getSpeechPlaybackState() { return state; }
export function getServerSpeechPlaybackState() { return idle; }
export function isSpeechPlaybackAvailable() { return config.enabled; }
function update(next: SpeechPlaybackState) { state = next; for (const listener of listeners) listener(); }
export function configureSpeechPlayback(next: typeof config) {
  if ((config.enabled && !next.enabled) || !next.token || config.scope !== next.scope || config.user !== next.user || controlsKey(config) !== controlsKey(next) || config.maxWeightedCharacters !== next.maxWeightedCharacters || config.maxTextBytes !== next.maxTextBytes) stopSpeechPlayback();
  if (config.user !== next.user) cache.clear();
  config = next;
  update({ ...state });
}
export function stopSpeechPlayback() {
  const previous = active; active = null;
  if (previous) { previous.controller.abort(); previous.resume(); previous.activity?.stop(); void previous.context.close(); }
  update(idle);
}
export function normalizeSpeechText(text: string) {
  return normalizeSpeechContent(text);
}
export function stopSpeechPlaybackForMessage(messageID: string) { if (state.messageID === messageID) stopSpeechPlayback(); }
export function reconcileSpeechPlaybackMessage(messageID: string, text: string, disabled = false) {
  if (state.messageID === messageID && active && (disabled || active.text !== normalizeSpeechText(text))) stopSpeechPlayback();
}
export function splitSpeechText(text: string, limits: { maxWeightedCharacters?: number; maxTextBytes?: number } = {}) {
  const normalized = normalizeSpeechText(text);
  if (!normalized) return [];
  const budget = (value: number | undefined, fallback: number) => typeof value === "number" && Number.isFinite(value) && value >= 1 ? Math.min(Math.floor(value), fallback) : fallback;
  const maxWeight = budget(limits.maxWeightedCharacters, 1800);
  const maxBytes = budget(limits.maxTextBytes, 65536);
  const sentenceWeight = Math.max(1, Math.floor(maxWeight * 2 / 3));
  const sentenceBytes = Math.max(1, Math.floor(maxBytes * 2 / 3));
  const parts: string[] = [];
  let totalWeight = 0;
  let totalBytes = 0;
  let start = 0;
  let offset = 0;
  let currentWeight = 0;
  let currentBytes = 0;
  const flush = () => {
    if (offset > start) parts.push(normalized.slice(start, offset));
    start = offset; currentWeight = 0; currentBytes = 0;
  };
  for (const character of normalized) {
    const characterWeight = /\p{Script=Han}/u.test(character) ? 2 : 1;
    const codepoint = character.codePointAt(0) ?? 0;
    const characterBytes = codepoint <= 0x7f ? 1 : codepoint <= 0x7ff ? 2 : codepoint <= 0xffff ? 3 : 4;
    totalWeight += characterWeight; totalBytes += characterBytes;
    if (totalWeight > 100000 || totalBytes > 262144 || characterWeight > maxWeight || characterBytes > maxBytes) throw new Error("tooLong");
    if (currentWeight + characterWeight > maxWeight || currentBytes + characterBytes > maxBytes) flush();
    currentWeight += characterWeight; currentBytes += characterBytes; offset += character.length;
    if (/[。！？.!?]/u.test(character) && (currentWeight >= sentenceWeight || currentBytes >= sentenceBytes)) {
      flush();
    }
  }
  flush();
  return parts;
}
function controlsKey(snapshot: SpeechSynthesisOptions) { return JSON.stringify(["tts-controls-v2", snapshot.provider, "wav", snapshot.voice, snapshot.speed, snapshot.style]); }
function synthesisKey(text: string, snapshot: typeof config) { return JSON.stringify([snapshot.scope, snapshot.user, controlsKey(snapshot), text]); }
function rememberSynthesis(text: string, snapshot: typeof config, result: SpeechSynthesis) {
  const entry = cache.get(synthesisKey(text, snapshot));
  if (entry?.result?.id === result.id) entry.result = result;
}
async function synthesis(text: string, snapshot: typeof config, manual: boolean) {
  const key = synthesisKey(text, snapshot);
  let entry = cache.get(key);
  const terminalStatus = entry?.result?.status;
  if (entry && manual && ["expired", "cancelled", "failed", "uncertain"].includes(terminalStatus ?? "")) {
    // An explicit retry creates a new paid attempt; expiry is a fresh synthesis, not a retry of uncertainty.
    entry = undefined;
  }
  if (!entry) {
    entry = { requestID: crypto.randomUUID(), retry: terminalStatus === "uncertain" || terminalStatus === "cancelled" || terminalStatus === "failed" };
    cache.set(key, entry);
    if (cache.size > 64) cache.delete(cache.keys().next().value ?? "");
  }
  if (entry.error && !manual) throw entry.error;
  if (!entry.result && !entry.pending) {
    const attempt = entry;
    // A transport failure keeps its request identity. A later manual click safely reconciles that same attempt.
    const { provider, voice, speed, style } = snapshot;
    attempt.pending = synthesizeSpeech(snapshot.token, text, { provider, voice, speed, style }, attempt.requestID, attempt.retry).then((result) => {
      attempt.pending = undefined; attempt.error = undefined; attempt.result = result; return result;
    }, (error: unknown) => { attempt.pending = undefined; attempt.error = error; throw error; });
  }
  let result = entry.result ?? await entry.pending;
  if (!result || ["uncertain", "failed", "cancelled", "expired"].includes(result.status)) throw new Error("synthesisFailed");
  if (!result.media_url) result = await getSpeechSynthesis(snapshot.token, result.id);
  rememberSynthesis(text, snapshot, result);
  if (!result.media_url || ["uncertain", "failed", "cancelled", "expired"].includes(result.status)) throw new Error("synthesisFailed");
  return result;
}
function abortError() { return new DOMException("Aborted", "AbortError"); }
async function readWav(response: Response, playback: NonNullable<typeof active>, scope: string) {
  const reader = response.body?.getReader();
  if (!reader) throw new Error("audioUnavailable");
  let pending = new Uint8Array(0);
  let headerRead = false;
  let sampleRate = 24000;
  let cursor = playback.context.currentTime;
  let samples = 0;
  let formatValid = false;
  let incoming: Uint8Array | undefined;
  let incomingOffset = 0;
  let done = false;
  const ensureActive = () => { if (active !== playback || config.scope !== scope || playback.controller.signal.aborted) throw abortError(); };
  const wait = async () => {
    ensureActive();
    if (playback.context.state === "suspended") await new Promise<void>((resolve) => { playback.resume = resolve; });
    ensureActive();
    if (cursor - playback.context.currentTime > 0.3) await new Promise<void>((resolve) => setTimeout(resolve, 50));
  };
  try {
    while (true) {
      await wait();
      // Fetch chunks are transport-dependent: a proxy or buffered response can
      // deliver megabytes at once. Bound our parsing/copying, not its chunk size.
      if (!incoming || incomingOffset >= incoming.length) {
        const next = await reader.read();
        incoming = next.value;
        incomingOffset = 0;
        done = next.done;
      }
      ensureActive();
      const value = incoming?.subarray(incomingOffset, incomingOffset + 32 * 1024);
      incomingOffset += value?.length ?? 0;
      if (value?.length) {
        const joined = new Uint8Array(pending.length + value.length); joined.set(pending); joined.set(value, pending.length); pending = joined;
      }
      if (!headerRead) {
        if (pending.length < 12) { if (done) throw new Error("invalidAudio"); continue; }
        const tag = (offset: number) => String.fromCharCode(...pending.subarray(offset, offset + 4));
        if (tag(0) !== "RIFF" || tag(8) !== "WAVE") throw new Error("invalidAudio");
        let offset = 12;
        while (offset + 8 <= pending.length) {
          const view = new DataView(pending.buffer, pending.byteOffset, pending.byteLength);
          const size = view.getUint32(offset + 4, true);
          const kind = tag(offset);
          if (kind === "data") { if (!formatValid) throw new Error("invalidAudio"); pending = pending.slice(offset + 8); headerRead = true; break; }
          if (offset + 8 + size > pending.length) break;
          if (kind === "fmt " && size >= 16) {
            formatValid = view.getUint16(offset + 8, true) === 1 && view.getUint16(offset + 10, true) === 1 && view.getUint16(offset + 22, true) === 16;
            sampleRate = view.getUint32(offset + 12, true);
            if (sampleRate < 8000 || sampleRate > 48000) throw new Error("invalidAudio");
          }
          offset += 8 + size + (size % 2);
        }
        if (!headerRead) { if (pending.length > 65536 || done) throw new Error("invalidAudio"); continue; }
      }
      let offset = 0;
      while (offset + 1 < pending.length) {
        while (cursor - playback.context.currentTime > 0.3 || playback.context.state === "suspended") await wait();
        ensureActive();
        const count = Math.min(4800, Math.floor(sampleRate * 0.2), Math.floor((pending.length - offset) / 2));
        samples += count;
        if (samples > sampleRate * 1800) throw new Error("audioTooLong");
        const buffer = playback.context.createBuffer(1, count, sampleRate);
        const channel = buffer.getChannelData(0);
        const view = new DataView(pending.buffer, pending.byteOffset + offset, count * 2);
        for (let i = 0; i < count; i++) channel[i] = view.getInt16(i * 2, true) / 32768;
        const source = playback.context.createBufferSource(); source.buffer = buffer;
        source.connect(playback.context.destination);
        // Analysis taps the exact scheduled playback rather than the download rate.
        const analyzer = analyzers.get(playback.context);
        if (analyzer) source.connect(analyzer);
        cursor = Math.max(cursor, playback.context.currentTime + 0.015); source.start(cursor); cursor += count / sampleRate;
        source.onended = () => source.disconnect();
        offset += count * 2;
      }
      pending = pending.slice(offset);
      if (done) break;
    }
    while (cursor > playback.context.currentTime) { await wait(); await new Promise<void>((resolve) => setTimeout(resolve, 50)); }
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}
const analyzers = new WeakMap<AudioContext, AnalyserNode>();
export async function playSpeech(messageID: string, text: string, onError: (reason: string) => void, options: { manual?: boolean } = {}) {
  if (!config.enabled || !config.token || isRecordingActive()) return;
  const normalized = normalizeSpeechText(text);
  const identity = JSON.stringify([config.scope, config.user, messageID, controlsKey(config), normalized]);
  if (active && active.identity === identity) {
    if (state.status === "loading") return;
    if (state.status === "playing") { active.activity?.publishLevel(0); await active.context.suspend(); update({ messageID, status: "paused" }); return; }
    if (state.status === "paused") { await active.context.resume(); active.resume(); update({ messageID, status: "playing" }); return; }
  }
  stopSpeechPlayback();
  const snapshot = { ...config };
  let parts: string[];
  try { parts = splitSpeechText(text, snapshot); } catch { onError("tooLong"); return; }
  if (!parts.length) return;
  const context = new AudioContext();
  const playback: NonNullable<typeof active> = { controller: new AbortController(), context, identity, text: normalized, resume: () => {} };
  active = playback;
  const analyzer = context.createAnalyser(); analyzers.set(context, analyzer);
  const spectrum = createMediaSpectrum(analyzer, context.sampleRate);
  const waveform = new Float32Array(analyzer.fftSize);
  playback.activity = beginPlaybackActivity(messageID, "tts", () => stopSpeechPlayback());
  const timer = setInterval(() => {
    if (context.state !== "running" || document.hidden) { playback.activity?.publishLevel(0); return; }
    analyzer.getFloatTimeDomainData(waveform);
    let squared = 0; for (const value of waveform) squared += value * value;
    playback.activity?.publishLevel(Math.min(1, Math.sqrt(squared / waveform.length) * 4), spectrum.sample());
  }, 34);
  update({ messageID, status: "loading" });
  try {
    let resumeTimer: ReturnType<typeof setTimeout> | undefined;
    try { await Promise.race([context.resume(), new Promise<never>((_, reject) => { resumeTimer = setTimeout(() => reject(new Error("autoplayUnavailable")), 3000); })]); }
    finally { clearTimeout(resumeTimer); }
    for (const textPart of parts) {
      if (active !== playback || config.scope !== snapshot.scope || isRecordingActive()) throw abortError();
      let result = await synthesis(textPart, snapshot, Boolean(options.manual));
      if (active !== playback || config.scope !== snapshot.scope || isRecordingActive()) throw abortError();
      const readAudio = (result: SpeechSynthesis) => authedFetch(new URL(result.media_url, window.location.origin).pathname, { accessToken: snapshot.token, signal: playback.controller.signal });
      let response: Response;
      try { response = await readAudio(result); }
      catch (error) {
        if (!(error instanceof Error && "status" in error && error.status === 410)) throw error;
        rememberSynthesis(textPart, snapshot, { ...result, status: "expired" });
        if (!options.manual || active !== playback || config.scope !== snapshot.scope) throw error;
        // One replacement is allowed for this explicit play. Never loop if the replacement also fails.
        result = await synthesis(textPart, snapshot, true);
        if (active !== playback || config.scope !== snapshot.scope || isRecordingActive()) throw abortError();
        response = await readAudio(result);
      }
      if (active !== playback) throw abortError();
      update({ messageID, status: "playing" });
      await readWav(response, playback, snapshot.scope);
      const settled = await getSpeechSynthesis(snapshot.token, result.id);
      rememberSynthesis(textPart, snapshot, { ...result, ...settled });
      // A completed cache entry can expire after its pinned reader finishes.
      // Its consumed audio was valid; retain expiry so the next click renews it.
      if (settled.status !== "complete" && !(result.status === "complete" && settled.status === "expired")) throw new Error("incompleteAudio");
    }
    if (active === playback) stopSpeechPlayback();
  } catch (error) {
    if (active === playback) {
      stopSpeechPlayback(); update({ messageID, status: "error" });
      if (!(error instanceof DOMException && error.name === "AbortError")) onError("failed");
    }
  } finally { clearInterval(timer); playback.activity?.stop(); analyzer.disconnect(); analyzers.delete(context); if (context.state !== "closed") void context.close(); }
}
