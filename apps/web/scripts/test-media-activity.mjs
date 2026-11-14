import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";

const source = await readFile(new URL("../shared/lib/media-activity.ts", import.meta.url), "utf8");
const compiled = `${stripTypeScriptTypes(source).replace(/^export /gm, "")}\nObject.assign(exports, { beginRecordingActivity, beginPlaybackActivity, pauseAllPlayback, isRecordingActive, registerMediaActivity, subscribeMediaActivity });`;

function setup({ capture = true, audioTrack = true } = {}) {
  const frames = new Map();
  const document = new EventTarget();
  document.hidden = false;
  const stats = { contexts: 0, closed: 0, stopped: 0, disconnected: 0 };
  let frameId = 0;
  class Context {
    constructor() { stats.contexts++; }
    async resume() {}
    async suspend() {}
    async close() { stats.closed++; }
    createMediaStreamSource() { return { connect() {}, disconnect() { stats.disconnected++; } }; }
    createAnalyser() { return { fftSize: 256, getFloatTimeDomainData(buffer) { buffer.fill(0.125); }, disconnect() { stats.disconnected++; } }; }
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
  state.media.muted = true;
  state.media.dispatchEvent(new Event("volumechange"));
  assert.equal(state.frames.size, 0);
  assert.equal(state.observed.at(-1).level, 0);
  state.media.muted = false;
  state.media.dispatchEvent(new Event("volumechange"));
  state.document.hidden = true;
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.frames.size, 0);
  state.document.hidden = false;
  state.document.dispatchEvent(new Event("visibilitychange"));
  assert.equal(state.frames.size, 1);
  state.media.pause();
  assert.equal(state.frames.size, 0);
  assert.equal(state.stats.stopped, 1);
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
