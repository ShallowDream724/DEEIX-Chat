import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";

const source = await readFile(new URL("../shared/lib/media-activity.ts", import.meta.url), "utf8");
const spectrumSource = await readFile(new URL("../shared/lib/media-spectrum.ts", import.meta.url), "utf8");
const compiled = `${stripTypeScriptTypes(spectrumSource).replace(/^export /gm, "")}\n${stripTypeScriptTypes(source).replace(/^import .*;\r?\n/gm, "").replace(/^export /gm, "")}\nObject.assign(exports, { createMediaSpectrum, beginRecordingActivity, beginPlaybackActivity, pauseAllPlayback, isRecordingActive, registerMediaActivity, subscribeMediaActivity });`;

function setup({ capture = true, audioTrack = true } = {}) {
  const frames = new Map();
  const document = new EventTarget();
  document.hidden = false;
  const stats = { contexts: 0, closed: 0, stopped: 0, disconnected: 0 };
  let frameId = 0;
  class Context {
    constructor() { stats.contexts++; this.sampleRate = 48000; }
    async resume() {}
    async suspend() {}
    async close() { stats.closed++; }
    createMediaStreamSource() { return { connect() {}, disconnect() { stats.disconnected++; } }; }
    createAnalyser() { return { fftSize: 256, getFloatTimeDomainData(buffer) { buffer.fill(0.125); }, getFloatFrequencyData(buffer) { buffer.fill(-100); buffer[10] = -24; }, disconnect() { stats.disconnected++; } }; }
  }
  const scope = { exports: {}, AudioContext: Context, performance: { now: () => 100 }, document, Float32Array, requestAnimationFrame: (callback) => { frames.set(++frameId, callback); return frameId; }, cancelAnimationFrame: (id) => frames.delete(id) };
  vm.runInNewContext(compiled, scope);
  const media = new EventTarget();
  Object.assign(media, { paused: true, ended: false, error: null, muted: false, volume: 1, pause() { this.paused = true; this.dispatchEvent(new Event("pause")); }, play() { this.paused = false; this.dispatchEvent(new Event("play")); } });
  if (capture) media.captureStream = () => {
    const tracks = audioTrack ? [{ stop() { stats.stopped++; } }] : [];
    return { getAudioTracks: () => tracks, getTracks: () => tracks };
  };
  const observed = [];
  const unsubscribe = scope.exports.subscribeMediaActivity((frame) => observed.push(frame));
  const settle = async () => { await Promise.resolve(); await Promise.resolve(); };
  return { api: scope.exports, media, document, frames, observed, stats, unsubscribe, settle };
}

test("recording pauses registered playback and stale recording sessions cannot overwrite the current one", async () => {
  const state = setup();
  const dispose = state.api.registerMediaActivity(state.media, "tts");
  state.media.play();
  await state.settle();
  const first = state.api.beginRecordingActivity("one");
  assert.equal(state.media.paused, true);
  const second = state.api.beginRecordingActivity("two");
  first.stop();
  first.publishLevel(1);
  second.publishLevel(0.4);
  assert.equal(state.api.isRecordingActive(), true);
  state.media.play();
  assert.equal(state.media.paused, true);
  second.stop();
  assert.equal(state.api.isRecordingActive(), false);
  assert.equal(state.media.paused, true);
  dispose(); state.unsubscribe();
  assert.equal(state.frames.size, 0);
});

test("unavailable capture and videos without audio preserve playback without scheduling visual frames", async () => {
  for (const options of [{ capture: false }, { audioTrack: false }]) {
    const state = setup(options);
    const dispose = state.api.registerMediaActivity(state.media, "video");
    state.media.play();
    await state.settle();
    assert.equal(state.media.paused, false);
    assert.equal(state.frames.size, 0);
    assert.equal(state.observed.at(-1).level, 0);
    dispose(); state.unsubscribe();
  }
});

test("mute, hidden pages, pause and unregister stop visual work and release analysis", async () => {
  const state = setup();
  const dispose = state.api.registerMediaActivity(state.media, "audio");
  state.media.play();
  await state.settle();
  assert.equal(state.stats.contexts, 1);
  assert.equal(state.frames.size, 1);
  assert.ok(state.observed.at(-1).level > 0);
  assert.equal(state.observed.at(-1).bands.length, 8);
  assert.ok(state.observed.at(-1).bands.some((level) => level > 0));
  state.media.muted = true;
  state.media.dispatchEvent(new Event("volumechange"));
  assert.equal(state.frames.size, 0);
  assert.equal(state.observed.at(-1).level, 0);
  assert.equal(state.observed.at(-1).bands, undefined);
  state.media.muted = false;
  state.media.dispatchEvent(new Event("volumechange"));
  await state.settle();
  state.document.hidden = true;
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.frames.size, 0);
  state.document.hidden = false;
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.frames.size, 1);
  state.media.pause();
  assert.equal(state.frames.size, 0);
  assert.equal(state.stats.stopped, 2);
  dispose(); state.unsubscribe();
  assert.equal(state.stats.closed, 1);
});

test("dispose while analysis is opening does not revive a detached playback graph", async () => {
  const state = setup();
  const dispose = state.api.registerMediaActivity(state.media, "audio");
  state.media.play();
  dispose();
  await state.settle();
  assert.equal(state.frames.size, 0);
  assert.equal(state.stats.stopped, 1);
  assert.equal(state.stats.disconnected, 0);
  state.unsubscribe();
});

test("logarithmic spectrum follows frequency and fixed energy, reuses buffers, and clears silence", () => {
  let now = 0;
  let bin = 10;
  let db = -24;
  const scope = { exports: {}, performance: { now: () => now }, Float32Array, Uint8Array, Uint16Array };
  vm.runInNewContext(`${stripTypeScriptTypes(spectrumSource).replace(/^export /gm, "")}\nexports.createMediaSpectrum = createMediaSpectrum;`, scope);
  const analyser = { fftSize: 256, getFloatFrequencyData(data) { data.fill(-100); data[bin] = db; } };
  const spectrum = scope.exports.createMediaSpectrum(analyser, 48000);
  const first = spectrum.sample();
  assert.equal(analyser.fftSize, 1024);
  assert.ok(first[3] > 0.5);
  assert.equal(first.filter((value) => value > 0).length, 1);
  const loud = first[3];
  now += 40; db = -54;
  assert.equal(spectrum.sample(), first);
  assert.ok(first[3] < loud / 2);
  now += 40; bin = 64; db = -24;
  spectrum.sample();
  assert.equal(first[3], 0);
  assert.ok(first[6] > 0);
  assert.ok(spectrum.sample(0).every((value) => value === 0));
  now += 40; db = -100;
  assert.ok(spectrum.sample().every((value) => value === 0));
  assert.ok(scope.exports.createMediaSpectrum({ fftSize: 256 }, 48000).sample().every((value) => value === 0));
});

test("recording and TTS preserve independent spectrum bands and clear them on stop", () => {
  const state = setup();
  const producer = new Float32Array([0, 0.2, 0.7, 1, 0.1, 0, 0.3, 0.5]);
  const playback = state.api.beginPlaybackActivity("reply", "tts", () => {});
  playback.publishLevel(0.6, producer);
  state.document.dispatchEvent(new Event("visibilitychange"));
  const buffer = state.observed.at(-1).bands;
  assert.deepEqual([...buffer], [...producer]);
  producer.fill(0);
  assert.ok(buffer[3] === 1);
  playback.publishLevel(0);
  assert.ok(buffer.every((value) => value === 0));
  const recording = state.api.beginRecordingActivity("input");
  recording.publishLevel(0.5, new Float32Array(8).fill(0.25));
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.observed.at(-1).kind, "recording");
  assert.ok(state.observed.at(-1).bands.every((value) => value === 0.25));
  recording.stop();
  assert.equal(state.observed.at(-1).bands, undefined);
  assert.equal(state.frames.size, 0);
  state.unsubscribe();
});

test("streaming TTS registers its own measured level and recording clears playback without resuming it", () => {
  const state = setup();
  let pauses = 0;
  const first = state.api.beginPlaybackActivity("reply", "tts", () => { pauses++; });
  first.publishLevel(0.7);
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.observed.at(-1).kind, "tts");
  assert.equal(state.observed.at(-1).level, 0.7);
  const second = state.api.beginPlaybackActivity("reply", "tts", () => { pauses++; });
  first.stop();
  assert.equal(state.frames.size, 1);
  const recording = state.api.beginRecordingActivity("input");
  assert.equal(pauses, 2);
  second.publishLevel(1);
  recording.stop();
  assert.equal(state.observed.at(-1).active, false);
  assert.equal(state.frames.size, 0);
  state.unsubscribe();
});
