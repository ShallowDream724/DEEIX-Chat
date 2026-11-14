import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import test from "node:test";
import { createEncoder, getPacketInfo } from "libopus-wasm";

async function load(path, names, globals = {}) {
  const text = await readFile(new URL(path, import.meta.url), "utf8");
  const compiled = stripTypeScriptTypes(text).replace(/^import .*;\r?\n/gm, "").replace(/^export /gm, "").replace(/import\.meta\.url/g, '"file:///speech-capture.ts"');
  const scope = { exports: {}, console, Error, Event, setTimeout, clearTimeout, setInterval, clearInterval, performance, crypto, AbortController, DOMException, TextEncoder, Blob, Float32Array, Int16Array, Uint8Array, ArrayBuffer, DataView, ...globals };
  vm.runInNewContext(`${compiled}\nObject.assign(exports,{${names.join(",")}});`, scope);
  return scope.exports;
}
const { SpeechDraftSegment } = await load("../features/chat/lib/speech-draft.ts", ["SpeechDraftSegment"]);
const { createMediaSpectrum } = await load("../shared/lib/media-spectrum.ts", ["createMediaSpectrum"]);
const { isImeComposingKeyEvent } = await load("../shared/lib/platform-shortcuts.ts", ["isImeComposingKeyEvent"]);
const { speechInputKeyAction } = await load("../features/chat/lib/speech-keyboard.ts", ["speechInputKeyAction"], { isImeComposingKeyEvent });

test("speech keyboard keeps modifiers and IME native, stops on Enter, and interrupts real edits/navigation", () => {
  const action = (key, extra = {}, composing = false) => speechInputKeyAction({ key, ctrlKey: false, metaKey: false, altKey: false, ...extra }, composing);
  for (const key of ["Shift", "Control", "Alt", "Meta", "AltGraph"]) assert.equal(action(key), "preserve");
  for (const extra of [{}, { ctrlKey: true }, { metaKey: true }, { shiftKey: true }]) assert.equal(action("Enter", extra), "stop");
  assert.equal(action("Enter", {}, true), "preserve");
  assert.equal(action("Enter", { nativeEvent: { isComposing: true } }), "preserve");
  assert.equal(action("Enter", { keyCode: 229 }), "preserve");
  assert.equal(action("ArrowDown", {}, true), "preserve");
  for (const key of [" ", "a", "ArrowLeft", "Home", "Tab", "Escape", "Backspace", "Delete"]) assert.equal(action(key), "interrupt");
  for (const key of ["a", "x", "v"]) assert.equal(action(key, { ctrlKey: true }), "interrupt");
  assert.equal(action("c", { ctrlKey: true }), "default");
  assert.equal(action(" ", { ctrlKey: true }), "default");
});

test("recording spectrum taps one existing microphone source and releases it with capture", async () => {
  let requests = 0; let sources = 0; let analysers = 0; let stopped = 0; let disconnected = 0; let worklet;
  const track = new EventTarget();
  Object.assign(track, { readyState: "live", muted: false, getSettings: () => ({}), stop() { stopped++; } });
  const mediaDevices = new EventTarget();
  mediaDevices.getUserMedia = async () => { requests++; return { getAudioTracks: () => [track], getTracks: () => [track] }; };
  class Context extends EventTarget {
    constructor() { super(); this.state = "running"; this.sampleRate = 48000; this.audioWorklet = { addModule: async () => {} }; }
    createMediaStreamSource() { sources++; return { connect() {}, disconnect() { disconnected++; } }; }
    createAnalyser() { analysers++; return { fftSize: 256, getFloatFrequencyData(data) { data.fill(-100); data[10] = -24; }, disconnect() { disconnected++; } }; }
    async resume() {}
    async close() { this.state = "closed"; }
  }
  class Worklet extends EventTarget {
    constructor() { super(); worklet = this; this.port = { postMessage() {}, onmessage: null }; }
    connect() {}
    disconnect() { disconnected++; }
  }
  const received = [];
  const { startSpeechCapture } = await load("../features/chat/lib/speech-capture.ts", ["startSpeechCapture"], { createMediaSpectrum, AudioContext: Context, AudioWorkletNode: Worklet, navigator: { mediaDevices } });
  const capture = await startSpeechCapture({ maxSamples: 16000, onFrame: (frame, bands) => received.push({ frame, bands }), onLimit() {}, onError(error) { assert.fail(error); } });
  worklet.port.onmessage({ data: { type: "frame", frame: new Float32Array(320) } });
  assert.equal(requests, 1); assert.equal(sources, 1); assert.equal(analysers, 1);
  assert.equal(received[0].bands.length, 8);
  assert.ok(received[0].bands[3] > 0);
  assert.equal(received[0].bands.filter((value) => value > 0).length, 1);
  capture.cancel();
  assert.equal(stopped, 1); assert.equal(disconnected, 3);
  assert.equal(worklet.port.onmessage, null);
});

test("active speech revisions replace one range; intentional recordings start a new paragraph", () => {
  const segment = new SpeechDraftSegment("Existing draft.");
  assert.equal(segment.update("Hello"), "Existing draft.\nHello");
  assert.equal(segment.update("Hello, 世界！"), "Existing draft.\nHello, 世界！");
  const second = new SpeechDraftSegment(segment.update("Hello, 世界！"));
  assert.equal(second.update("Another sentence."), "Existing draft.\nHello, 世界！\nAnother sentence.");
  assert.equal(second.cancel(), "Existing draft.\nHello, 世界！");
  assert.equal(new SpeechDraftSegment("Draft\n").update("你好。"), "Draft\n你好。");
});

test("sample limit marks stopping before shared track release and keeps it alive for recognition final", async () => {
  let stopped = 0; let worklet; let limits = 0;
  const track = Object.assign(new EventTarget(), { readyState: "live", muted: false, getSettings: () => ({}), stop() { stopped++; } });
  const devices = Object.assign(new EventTarget(), { getUserMedia: async () => ({ getAudioTracks: () => [track], getTracks: () => [track] }) });
  class Context extends EventTarget {
    constructor() { super(); this.state = "running"; this.sampleRate = 48000; this.audioWorklet = { addModule: async () => {} }; }
    createMediaStreamSource() { return { connect() {}, disconnect() {} }; }
    createAnalyser() { return { fftSize: 256, disconnect() {} }; }
    async resume() {}
    async close() { this.state = "closed"; }
  }
  class Worklet extends EventTarget { constructor() { super(); worklet = this; this.port = { onmessage: null, postMessage() {} }; } connect() {} disconnect() {} }
  const { startSpeechCapture } = await load("../features/chat/lib/speech-capture.ts", ["startSpeechCapture"], { createMediaSpectrum, AudioContext: Context, AudioWorkletNode: Worklet, navigator: { mediaDevices: devices } });
  const capture = await startSpeechCapture({ maxSamples: 16000, keepTrackUntilCancel: true, onFrame() {}, onLimit() { limits++; assert.equal(stopped, 0); }, onError(error) { assert.fail(error); } });
  worklet.port.onmessage({ data: { type: "stopped" } });
  await capture.stop();
  assert.equal(limits, 1); assert.equal(stopped, 0);
  capture.cancel(); capture.cancel();
  assert.equal(stopped, 1);
});
test("edits outside speech survive partial revisions and cancellation; edits inside stay authoritative", () => {
  const segment = new SpeechDraftSegment("Draft"); segment.update("hello");
  segment.reconcile("Edited draft\nhello");
  assert.equal(segment.update("hello world"), "Edited draft\nhello world");
  segment.reconcile("Edited draft\nhello world\nUser tail");
  assert.equal(segment.update("hello world!"), "Edited draft\nhello world!\nUser tail");
  assert.equal(segment.cancel(), "Edited draft\nUser tail");
  const edited = new SpeechDraftSegment("Draft"); edited.update("partial"); edited.reconcile("Draft\nmy correction");
  assert.equal(edited.update("late final"), "Draft\nmy correction");
  assert.equal(edited.cancel(), "Draft\nmy correction");
});

test("new speech inserts at the caret without deleting selected or trailing text", () => {
  const segment = new SpeechDraftSegment("前😀后 English", 3);
  assert.equal(segment.update("新句"), "前😀新句后 English");
  assert.equal(segment.selectionEnd, 5);
  assert.equal(segment.update("新句修订。"), "前😀新句修订。后 English");
  assert.equal(segment.cancel(), "前😀后 English");
  const beginning = new SpeechDraftSegment("suffix", 0);
  assert.equal(beginning.update("hello "), "hellosuffix");
});

test("capture resamples 48k to exact 16k sample limit and flushes the last partial frame", async () => {
  const source = await readFile(new URL("../public/speech-capture-worklet.js", import.meta.url), "utf8");
  let Processor;
  const frames = [];
  class Base { constructor() { this.port = { onmessage: null, postMessage: (event) => { if (event.frame) frames.push(event.frame); this.port.onmessage?.({ data: "ack" }); } }; } }
  vm.runInNewContext(source, { AudioWorkletProcessor: Base, sampleRate: 48000, Float32Array, registerProcessor: (_name, value) => { Processor = value; } });
  const processor = new Processor({ processorOptions: { maxSamples: 1001 } });
  for (let i = 0; i < 50; i++) processor.process([[new Float32Array(128).fill(0.25)]]);
  assert.equal(frames.reduce((sum, frame) => sum + frame.length, 0), 1001);
  assert.equal(frames.at(-1).length, 41);
  assert.ok(frames.every((frame) => frame.every((sample) => sample === 0.25)));
  const second = new Processor({ processorOptions: { maxSamples: 16000 } });
  const before = frames.length;
  second.process([[new Float32Array(128).fill(0.1)]]);
  second.port.onmessage({ data: "stop" });
  assert.equal(frames.slice(before).reduce((sum, frame) => sum + frame.length, 0), 42);
});

test("portable WASM encoder produces a mono 20ms raw Opus packet", async () => {
  const encoder = await createEncoder({ sampleRate: 16000, channels: 1, frameSize: 320, bitrate: 24000 });
  try {
    const packet = encoder.encodeFloat(new Float32Array(320));
    const info = await getPacketInfo(packet, { sampleRate: 16000 });
    assert.equal(info.channels, 1);
    assert.equal(info.samples, 320);
    assert.ok(packet.byteLength < 1275);
  } finally { encoder.free(); }
});

function hookRuntime() {
  let cursor = 0;
  const slots = [];
  const effects = [];
  const React = {
    useRef(value) { const index = cursor++; slots[index] ??= { current: value }; return slots[index]; },
    useState(value) { const index = cursor++; if (!(index in slots)) slots[index] = value; return [slots[index], (next) => { slots[index] = typeof next === "function" ? next(slots[index]) : next; }]; },
    useCallback(callback, deps) { const index = cursor++; if (!slots[index] || deps.some((value, at) => value !== slots[index].deps[at])) slots[index] = { deps, callback }; return slots[index].callback; },
    useMemo(factory, deps) { const index = cursor++; if (!slots[index] || deps.some((value, at) => value !== slots[index].deps[at])) slots[index] = { deps, value: factory() }; return slots[index].value; },
    useSyncExternalStore(subscribe, snapshot) { const index = cursor++; if (!slots[index] || slots[index].subscribe !== subscribe) { slots[index]?.cleanup?.(); slots[index] = { subscribe, cleanup: subscribe(() => {}) }; } return snapshot(); },
    useEffect(callback, deps) { const index = cursor++; if (!slots[index] || deps.some((value, at) => value !== slots[index].deps[at])) effects.push(() => { slots[index]?.cleanup?.(); slots[index] = { deps, cleanup: callback() }; }); },
  };
  return { React, render(callback) { cursor = 0; const result = callback(); while (effects.length) effects.shift()(); return result; }, cleanup() { for (const slot of slots) slot?.cleanup?.(); } };
}
async function setup(provider = "browser", duration = 120000, sharedTrack = false) {
  const runtime = hookRuntime();
  const recognitions = [];
  let draft = "Prior draft";
  let scope = "chat:one";
  const errors = [];
  const persisted = []; const committed = [];
  let stopped = 0;
  class Recognition {
    constructor() { recognitions.push(this); }
    start(track) { this.track = track; this.onstart?.(); }
    stop() { stopped++; }
    abort() {}
    result(text) { this.onresult?.({ results: { length: 1, item: () => ({ isFinal: true, length: 1, item: () => ({ transcript: text }) }) } }); }
  }
  let selectedProvider = provider;
  const capabilities = () => ({ selected_input_provider: selectedProvider, max_recording_duration_ms: duration, asr: [{ id: selectedProvider, streaming: ["wetype", "doubao-ime"].includes(selectedProvider), max_duration_ms: 120000, max_bytes: 8 * 1024 * 1024 }] });
  const remote = { capture: null, uploads: [], wavFrames: null, resolve: null, reject: null, sockets: [], events: [], flushTail: false, finalDuringFlush: false };
  class Socket {
    constructor() { this.bufferedAmount = 0; remote.sockets.push(this); queueMicrotask(() => this.event("ready", 0)); }
    event(type, sequence, text = "", sessionID = "remote", code) { this.onmessage?.({ data: JSON.stringify({ type, sequence, text, session_id: sessionID, code }) }); }
    send(data) { if (typeof data === "string") { this.sent = JSON.parse(data).type; remote.events.push(this.sent); } else remote.events.push("packet"); }
    close() {}
  }
  const api = await load("../features/chat/hooks/use-chat-speech-input.ts", ["useChatSpeechInput"], {
    React: runtime.React, SpeechDraftSegment,
    useOptionalAuthSession: () => ({ accessToken: "token" }),
    useSpeechCapabilities: capabilities, refreshSpeechCapabilities: async () => capabilities(), beginRecordingActivity: () => ({ stop() {}, publishLevel() {} }),
    watchMicrophonePermission: async () => {}, cancelSpeechSession: async () => {},
    startSpeechCapture: async (options) => { remote.capture = options; options.onFrame(new Float32Array(320)); return { track: { id: "single-track", kind: "audio", readyState: "live" }, stop: async () => options.onFrame(new Float32Array(80)), cancel() {} }; },
    supportsBrowserRecognitionTrack: () => sharedTrack,
    prepareSpeechCaptureContext: () => undefined,
    pcm16Frame: (frame) => new Int16Array(frame.length), speechWav: (frames, samples) => { remote.wavFrames = frames; remote.uploads.push(samples); return new Blob(); },
    transcribeSpeech: () => new Promise((resolve, reject) => { remote.resolve = resolve; remote.reject = reject; }),
    createSpeechSession: async () => ({ id: "remote", ws_url: "/speech/ws", max_duration_ms: 120000, max_bytes: 8 * 1024 * 1024 }),
    speechResourceURL: () => "https://example.test/speech/ws", URL, WebSocket: Socket,
    createSpeechOpusEncoder: async (packet) => ({ encode() { packet(new Uint8Array(10)); }, flush: async () => { remote.events.push("flush"); if (remote.flushTail) packet(new Uint8Array(10)); if (remote.finalDuringFlush) { remote.sockets[0].event("final", 3, "Final while flushing."); throw new Error("encoder closed after final"); } }, close() {} }),
    window: Object.assign(new EventTarget(), { isSecureContext: true, SpeechRecognition: Recognition, AudioWorkletNode: class {} }), navigator: { mediaDevices: {} }, supportsSpeechOpus: () => true,
    requestAnimationFrame: (callback) => { queueMicrotask(callback); },
  });
  const render = () => { const capturedScope = scope; return runtime.render(() => api.useChatSpeechInput({ draft, scopeKey: scope, language: "en", onDraftChange: (value) => { draft = value; }, onDraftPersist: (value) => persisted.push({ scope: capturedScope, draft: value }), onResultCommitted: (result) => committed.push(result), onError: (error) => errors.push(error), placeholder: "" })); };
  render();
  return { render, recognitions, errors, persisted, committed, remote, set provider(value) { selectedProvider = value; }, get draft() { return draft; }, set draft(value) { draft = value; }, set scope(value) { scope = value; }, get stopped() { return stopped; }, cleanup: runtime.cleanup };
}
test("manual editing ends the current segment immediately, persists partial and ignores late final/error", async () => {
  const state = await setup();
  try {
    await state.render().start(5);
    const recognition = state.recognitions[0]; recognition.result("Hello"); state.render();
    assert.equal(state.draft, "PriorHello draft");
    const result = recognition.onresult; const error = recognition.onerror;
    state.render().interruptForEditing(); state.render();
    result({ results: { length: 1, item: () => ({ length: 1, item: () => ({ transcript: "late replacement" }) }) } });
    error({ error: "aborted" });
    assert.equal(state.draft, "PriorHello draft");
    assert.equal(state.render().active, false);
    assert.equal(state.persisted.at(-1).draft, state.draft);
    assert.deepEqual(state.errors, []);
    await state.render().start(5); state.recognitions.at(-1).result("新😀"); state.render();
    assert.equal(state.draft, "Prior新😀Hello draft");
  } finally { state.cleanup(); }
});

test("browser sample and wall clock limits finish normally, retain final tail and allow another segment", async () => {
  for (const sharedTrack of [true, false]) {
    const state = await setup("browser", 10, sharedTrack);
    try {
      await state.render().start(); const recognition = state.recognitions[0]; recognition.result("Partial"); state.render();
      if (sharedTrack) { assert.equal(state.remote.capture.keepTrackUntilCancel, true); state.remote.capture.onLimit(); }
      else await new Promise((resolve) => setTimeout(resolve, 250));
      assert.equal(state.stopped, 1); assert.equal(state.render().status, "transcribing");
      recognition.onerror({ error: "aborted" }); recognition.result("Final tail."); recognition.onend(); await Promise.resolve(); state.render();
      assert.deepEqual(state.errors, []); assert.equal(state.draft, "Prior draft\nFinal tail."); assert.equal(state.render().active, false);
      await state.render().start(); assert.equal(state.render().active, true);
    } finally { state.cleanup(); }
  }
});

test("both streaming sources flush the extra native Opus tail before finish at the sample limit", async () => {
  for (const provider of ["doubao-ime", "wetype"]) {
    const state = await setup(provider, 1000);
    try {
      await state.render().start(); state.remote.flushTail = true;
      const socket = state.remote.sockets[0]; socket.event("transcript", 1, "partial"); state.render();
      state.remote.capture.onLimit(); await new Promise((resolve) => setTimeout(resolve, 0));
      assert.deepEqual(state.remote.events.slice(-3), ["flush", "packet", "finish"]);
      socket.event("final", 2, "Final tail."); await Promise.resolve(); state.render();
      assert.equal(state.draft, "Prior draft\nFinal tail."); assert.equal(state.render().active, false); assert.deepEqual(state.errors, []);
    } finally { state.cleanup(); }
  }
});

test("real stream service failures keep partial and are not classified as unsupported browser", async () => {
  const state = await setup("doubao-ime");
  try {
    await state.render().start(); const socket = state.remote.sockets[0]; socket.event("transcript", 1, "kept text"); state.render();
    socket.event("error", 2, "", "remote", "speech.failed"); state.render();
    assert.deepEqual(state.errors, ["processingFailed"]); assert.equal(state.draft, "Prior draft\nkept text"); assert.equal(state.render().active, false);
  } finally { state.cleanup(); }
});

test("a server final racing with encoder flush remains successful and explicit send still happens once", async () => {
  const state = await setup("doubao-ime");
  try {
    await state.render().start(); state.remote.finalDuringFlush = true; let sends = 0;
    await state.render().finishAndSend((text) => { sends++; assert.equal(text, "Prior draft\nFinal while flushing."); });
    state.render(); assert.equal(sends, 1); assert.deepEqual(state.errors, []); assert.equal(state.render().active, false);
  } finally { state.cleanup(); }
});

test("nonstreaming sample limit uploads the captured tail and completes without capability errors", async () => {
  const state = await setup("deepseek", 1000);
  try {
    await state.render().start(); state.remote.capture.onLimit(); await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(state.remote.uploads, [400]);
    assert.equal(state.remote.wavFrames.length, 0, "release PCM chunks while transcription is still pending");
    state.remote.resolve({ text: "Complete WAV tail.", sequence: 1 }); await new Promise((resolve) => setTimeout(resolve, 0)); state.render();
    assert.equal(state.render().active, false); assert.equal(state.draft, "Prior draft\nComplete WAV tail."); assert.deepEqual(state.errors, []);
  } finally { state.cleanup(); }
});

test("old browser recognition errors cannot terminate the replacement recognition", async () => {
  const state = await setup();
  try {
    await state.render().start();
    const first = state.recognitions[0]; first.result("First"); const lateError = first.onerror;
    first.onend(); state.render();
    lateError({ error: "aborted" });
    state.recognitions.at(-1).result("Second"); state.render();
    assert.equal(state.render().active, true);
    assert.equal(state.draft, "Prior draft\nFirst Second");
    assert.deepEqual(state.errors, []);
  } finally { state.cleanup(); }
});

test("manual interruption before nonstreaming transcription keeps only existing text and never uploads", async () => {
  const state = await setup("deepseek");
  try {
    await state.render().start();
    state.render().interruptForEditing(); state.render();
    assert.equal(state.render().active, false);
    assert.equal(state.draft, "Prior draft");
    assert.equal(state.persisted.at(-1).draft, "Prior draft");
    assert.deepEqual(state.remote.uploads, []);
  } finally { state.cleanup(); }
});

test("stop keeps browser tail and sending is exactly once; late callbacks cannot revive sent text", async () => {
  const state = await setup();
  try {
    await state.render().start(); const recognition = state.recognitions[0]; recognition.result("Partial"); state.render();
    let sends = 0;
    const input = state.render();
    const first = input.finishAndSend((finalDraft) => { sends++; assert.equal(finalDraft, "Prior draft\nFinal tail."); assert.equal(state.draft, "Prior draft\nFinal tail."); });
    const second = input.finishAndSend(() => { sends++; });
    assert.equal(sends, 0); assert.equal(state.stopped, 1);
    recognition.result("Final tail."); recognition.onend();
    await Promise.all([first, second]); assert.equal(sends, 1);
    state.draft = ""; recognition.result("Late old text"); assert.equal(state.draft, "");
  } finally { state.cleanup(); }
});
test("cancel only removes the current segment and changing conversations ignores late results", async () => {
  const state = await setup();
  try {
    await state.render().start(); state.recognitions[0].result("Current segment"); state.render().cancel(); assert.equal(state.draft, "Prior draft");
    await state.render().start(); const recognition = state.recognitions.at(-1); const late = recognition.onresult;
    state.scope = "chat:two"; state.draft = "Other draft"; state.render();
    late({ results: { length: 1, item: () => ({ length: 1, item: () => ({ transcript: "late" }) }) } });
    assert.equal(state.draft, "Other draft");
  } finally { state.cleanup(); }
});

test("completion events deduplicate message identity without replaying history to new subscribers", async () => {
  const api = await load("../features/chat/lib/response-completion.ts", ["publishResponseCompletion", "subscribeResponseCompletion"]);
  const seen = []; const stop = api.subscribeResponseCompletion((event) => seen.push(event.messageID));
  api.publishResponseCompletion({ conversationID: "one", messageID: "reply", content: "first" });
  api.publishResponseCompletion({ conversationID: "one", messageID: "reply", content: "duplicate" }); stop();
  api.subscribeResponseCompletion((event) => seen.push(event.messageID));
  assert.deepEqual(seen, ["reply"]);
});

test("DeepSeek waits for capture tail, preserves concurrent draft edits and never sends after recognition failure", async () => {
  const state = await setup("deepseek");
  try {
    await state.render().start();
    assert.deepEqual(state.errors, []);
    let sends = 0;
    const result = state.render().finishAndSend(() => { sends++; });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(state.remote.uploads, [400]);
    assert.equal(sends, 0);
    state.draft = "User edit while waiting"; state.render();
    state.remote.resolve({ text: "Final result" });
    await result;
    assert.equal(state.draft, "User edit while waiting\nFinal result"); assert.equal(sends, 1);
    await state.render().start();
    const failure = state.render().finishAndSend(() => { sends++; });
    await new Promise((resolve) => setTimeout(resolve, 0)); state.remote.reject(new Error("failed"));
    await failure;
    assert.equal(sends, 1); assert.equal(state.draft, "User edit while waiting\nFinal result");
  } finally { state.cleanup(); }
});

test("streaming partials ignore stale sequence and identity; stop sends finish after audio flush then consumes final tail", async () => {
  const state = await setup("wetype");
  try {
    await state.render().start(); const socket = state.remote.sockets[0];
    assert.deepEqual(state.errors, []);
    socket.event("transcript", 2, "最新 partial"); socket.event("transcript", 1, "old"); socket.event("transcript", 5, "wrong", "another-session");
    assert.equal(state.draft, "Prior draft\n最新 partial"); state.render();
    let sends = 0; const result = state.render().finishAndSend(() => { sends++; });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(socket.sent, "finish"); assert.equal(sends, 0);
    socket.event("final", 3, "最后一句。"); await result;
    assert.equal(state.draft, "Prior draft\n最后一句。"); assert.equal(sends, 1);
    socket.event("transcript", 10, "late"); assert.equal(state.draft, "Prior draft\n最后一句。");
  } finally { state.cleanup(); }
});

test("a site limit below ten seconds is exposed immediately and enforced by the recorder sample budget", async () => {
  const state = await setup("deepseek", 8000);
  try {
    await state.render().start();
    assert.equal(state.render().remainingMS, 8000);
    assert.equal(state.remote.capture.maxSamples, 128000);
    state.remote.capture.onLimit();
    assert.equal(state.render().remainingMS, 0);
    await new Promise((resolve) => setTimeout(resolve, 0));
    state.remote.resolve({ text: "At the limit" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(state.render().status, "idle");
    assert.equal(state.draft, "Prior draft\nAt the limit");
  } finally { state.cleanup(); }
});

test("session settlement synchronously persists current text and emits the exact result caret only when the user has not edited", async () => {
  const state = await setup();
  try {
    await state.render().start(); const recognition = state.recognitions.at(-1); recognition.result("Final sentence."); state.render();
    const stop = state.render().stop(); recognition.onend(); await stop;
    assert.equal(state.persisted.at(-1).draft, "Prior draft\nFinal sentence.");
    assert.equal(state.committed.at(-1).selectionEnd, "Prior draft\nFinal sentence.".length);
    await state.render().start(); const editedRecognition = state.recognitions.at(-1); editedRecognition.result("Partial"); state.render();
    state.draft = "I edited the draft\nPartial"; state.render();
    const editedStop = state.render().stop(); editedRecognition.result("Final."); editedRecognition.onend(); await editedStop;
    assert.equal(state.committed.length, 1); assert.equal(state.persisted.at(-1).draft, "I edited the draft\nFinal.");
  } finally { state.cleanup(); }
});

test("capture interruption preserves transcript, persists immediately and ignores late start callbacks", async () => {
  const state = await setup("wetype");
  try {
    await state.render().start(); state.remote.sockets[0].event("transcript", 1, "已识别的内容。"); state.render();
    state.remote.capture.onError("interrupted");
    assert.equal(state.render().status, "idle"); assert.equal(state.draft, "Prior draft\n已识别的内容。");
    assert.equal(state.persisted.at(-1).draft, state.draft); assert.deepEqual(state.errors, ["interrupted"]);
    state.remote.sockets[0].event("transcript", 10, "late"); assert.equal(state.draft, "Prior draft\n已识别的内容。");
    state.provider = "browser"; await state.render().start(); const recognition = state.recognitions.at(-1); const lateStart = recognition.onstart;
    state.render().cancel(); lateStart(); assert.equal(state.render().status, "idle");
  } finally { state.cleanup(); }
});

test("capability invalidation cannot publish an older in-flight provider and refreshes across tabs, focus and visible polling", async () => {
  const runtime = hookRuntime(); const requests = []; const timers = []; const broadcasts = [];
  const browser = Object.assign(new EventTarget(), { localStorage: { setItem: (key) => broadcasts.push(key) }, setInterval: (callback) => { timers.push(callback); return 1; }, clearInterval() {} });
  const document = Object.assign(new EventTarget(), { hidden: false });
  const api = await load("../shared/hooks/use-speech-capabilities.ts", ["useSpeechCapabilities", "refreshSpeechCapabilities", "invalidateSpeechCapabilities"], {
    React: runtime.React, window: browser, document, useOptionalAuthSession: () => ({ accessToken: "token", user: { publicID: "user" } }),
    getSpeechCapabilities: () => new Promise((resolve) => requests.push(resolve)),
  });
  const render = () => runtime.render(() => api.useSpeechCapabilities());
  try {
    render(); assert.equal(requests.length, 1);
    api.invalidateSpeechCapabilities(); assert.equal(broadcasts.length, 1);
    requests[0]({ selected_input_provider: "browser", asr: [], tts: [] });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(requests.length, 2); assert.equal(render().selected_input_provider, undefined);
    requests[1]({ selected_input_provider: "deepseek", asr: [], tts: [] }); await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(render().selected_input_provider, "deepseek");
    browser.dispatchEvent(Object.assign(new Event("storage"), { key: broadcasts[0] }));
    requests[2]({ selected_input_provider: "wetype", asr: [], tts: [] }); await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(render().selected_input_provider, "wetype");
    document.hidden = true; timers[0](); assert.equal(requests.length, 3);
    document.hidden = false; browser.dispatchEvent(new Event("focus")); assert.equal(requests.length, 4);
    requests[3]({ selected_input_provider: "doubao-ime", asr: [], tts: [] }); await new Promise((resolve) => setTimeout(resolve, 0));
    const fresh = api.refreshSpeechCapabilities("token", "user"); assert.equal(requests.length, 5);
    requests[4]({ selected_input_provider: "browser", asr: [], tts: [] }); assert.equal((await fresh).selected_input_provider, "browser");
  } finally { runtime.cleanup(); }
});

test("known desktop Chrome can reuse the one capture track for native recognition while keeping its tail", async () => {
  const state = await setup("browser", 120000, true);
  try {
    await state.render().start(); const recognition = state.recognitions.at(-1);
    assert.equal(recognition.track.id, "single-track"); assert.ok(state.remote.capture);
    recognition.result("Native partial"); const pending = state.render().stop(); recognition.result("Native final."); recognition.onend(); await pending;
    assert.equal(state.draft, "Prior draft\nNative final."); assert.equal(state.remote.uploads.length, 0);
  } finally { state.cleanup(); }
});

test("track-overload allowlist excludes mobile, unknown Chromium brands and old Chrome without starting recognition", async () => {
  const run = async (navigator) => (await load("../features/chat/lib/speech-capture.ts", ["supportsBrowserRecognitionTrack"], { navigator })).supportsBrowserRecognitionTrack();
  const browser = { userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36", vendor: "Google Inc.", platform: "Win32", maxTouchPoints: 0, userAgentData: { brands: [{ brand: "Google Chrome", version: "140" }], platform: "Windows", mobile: false } };
  assert.equal(await run(browser), true);
  assert.equal(await run({ ...browser, userAgent: "Chrome/140 Android Mobile" }), false);
  assert.equal(await run({ ...browser, userAgentData: { ...browser.userAgentData, brands: [{ brand: "Chromium", version: "140" }] } }), false);
  assert.equal(await run({ ...browser, userAgentData: { ...browser.userAgentData, brands: [{ brand: "Google Chrome", version: "134" }] } }), false);
  assert.equal(await run({ ...browser, userAgent: "iPhone Chrome/140" }), false);
});

async function playbackSetup({ fail = false, failTimes = fail ? Infinity : 0, deferred = false, incomplete = false, statuses = [], expiredReads = 0, sampleCount = 240, clockScale = 1, chunkSizes = [], streamingHeader = false } = {}) {
  const stats = { syntheses: 0, requests: [], reads: 0, contexts: 0, closed: 0, recording: false, resolve: null, samples: 0, maxBufferSamples: 0, cancelled: 0 };
  class Context {
    constructor() { stats.contexts++; this.state = "running"; this.started = performance.now(); }
    get currentTime() { return (performance.now() - this.started) / 1000 * clockScale; }
    async resume() { this.state = "running"; }
    async suspend() { this.state = "suspended"; }
    async close() { if (this.state !== "closed") stats.closed++; this.state = "closed"; }
    createAnalyser() { return { fftSize: 256, getFloatTimeDomainData(buffer) { buffer.fill(0); }, disconnect() {} }; }
    createBuffer(_channels, count) { stats.samples += count; stats.maxBufferSamples = Math.max(stats.maxBufferSamples, count); return { getChannelData: () => new Float32Array(count) }; }
    createBufferSource() { return { connect() {}, disconnect() {}, start() {} }; }
  }
  const audio = new Uint8Array(44 + sampleCount * 2);
  const view = new DataView(audio.buffer);
  for (const [offset, text] of [[0, "RIFF"], [8, "WAVEfmt "], [36, "data"]]) for (let i = 0; i < text.length; i++) audio[offset + i] = text.charCodeAt(i);
  view.setUint32(4, streamingHeader ? 0xffffffff : audio.length - 8, true); view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true); view.setUint32(24, 24000, true); view.setUint16(34, 16, true); view.setUint32(40, streamingHeader ? 0xffffffff : sampleCount * 2, true);
  const api = await load("../features/chat/lib/speech-playback.ts", ["configureSpeechPlayback", "playSpeech", "stopSpeechPlayback", "getSpeechPlaybackState", "splitSpeechText"], {
    AudioContext: Context, createMediaSpectrum, document: { hidden: false }, window: { location: { origin: "https://example.test" } }, URL,
    isRecordingActive: () => stats.recording, beginPlaybackActivity: () => ({ stop() {}, publishLevel() {} }),
    synthesizeSpeech: (_token, _text, _voice, requestID, retry) => { stats.syntheses++; stats.requests.push({ requestID, retry }); if (stats.syntheses <= failTimes) return Promise.reject(new Error("failed")); const result = { id: `audio-${stats.syntheses}`, media_url: "/api/v1/speech/tts/audio/audio", status: statuses[stats.syntheses - 1] ?? "pending" }; return deferred ? new Promise((resolve) => { stats.resolve = () => resolve(result); }) : Promise.resolve(result); },
    getSpeechSynthesis: async () => ({ status: incomplete ? "uncertain" : "complete" }),
    authedFetch: async () => {
      stats.reads++;
      if (stats.reads <= expiredReads) throw Object.assign(new Error("expired"), { status: 410 });
      let offset = 0; let index = 0;
      return new Response(new ReadableStream({
        pull(controller) {
          if (offset >= audio.length) { controller.close(); return; }
          const end = Math.min(audio.length, offset + (chunkSizes[index++] ?? audio.length));
          controller.enqueue(audio.subarray(offset, end)); offset = end;
        },
        cancel() { stats.cancelled++; },
      }));
    },
  });
  const config = { scope: "conversation", user: "user", token: "token", voice: "stella", enabled: true };
  api.configureSpeechPlayback(config);
  return { api, stats, config };
}

test("TTS synthesis identity is separate from playback: repeated play reuses one synthesis and current voice changes its version", async () => {
  const { api, stats, config } = await playbackSetup(); const errors = [];
  await api.playSpeech("message", "Hello", (error) => errors.push(error));
  await api.playSpeech("message", "Hello", (error) => errors.push(error));
  assert.equal(stats.syntheses, 1); assert.equal(stats.reads, 2); assert.deepEqual(errors, []);
  api.configureSpeechPlayback({ ...config, voice: "tide" }); await api.playSpeech("message", "Hello", (error) => errors.push(error));
  assert.equal(stats.syntheses, 2); assert.equal(stats.closed, stats.contexts);
  assert.throws(() => api.splitSpeechText("汉".repeat(5001)), /tooLong/);
});

test("TTS plays large transport chunks and fragmented streaming WAV headers without dropping audio or allocating large PCM buffers", async () => {
  for (const chunkSizes of [[44 + 3 * 48000], [1, 7, 13, 22, 2, 144001]]) {
    const { api, stats } = await playbackSetup({ sampleCount: 28 * 24000, clockScale: 1000, chunkSizes, streamingHeader: true });
    const errors = [];
    await api.playSpeech("message", "Long audio", error => errors.push(error));
    assert.deepEqual(errors, []);
    assert.equal(stats.samples, 28 * 24000);
    assert.ok(stats.maxBufferSamples <= 4800);
    assert.equal(stats.closed, stats.contexts);
    assert.equal(api.getSpeechPlaybackState().status, "idle");
  }
});

test("TTS failure does not automatically resynthesize and an incomplete stream is not successful", async () => {
  const state = await playbackSetup({ fail: true });
  const errors = [];
  await state.api.playSpeech("message", "Hello", (error) => errors.push(error));
  await state.api.playSpeech("message", "Hello", (error) => errors.push(error));
  assert.equal(state.stats.syntheses, 1); assert.equal(state.stats.reads, 0); assert.equal(errors.length, 2);
  const partial = await playbackSetup({ incomplete: true });
  await partial.api.playSpeech("message", "Hello", (error) => errors.push(error));
  assert.equal(partial.api.getSpeechPlaybackState().status, "error");
});

test("switching conversation during synthesis prevents late audio playback; recording blocks synthesis", async () => {
  const { api, stats, config } = await playbackSetup({ deferred: true });
  const pending = api.playSpeech("message", "Hello", () => {});
  await new Promise((resolve) => setTimeout(resolve, 0));
  api.configureSpeechPlayback({ ...config, scope: "other" }); stats.resolve(); await pending;
  assert.equal(stats.reads, 0); assert.equal(stats.closed, 1);
  stats.recording = true; await api.playSpeech("new", "Another text", () => {});
  assert.equal(stats.syntheses, 1);
});

test("disabling TTS stops active playback and prevents a late synthesis response from reviving hidden controls", async () => {
  const { api, stats, config } = await playbackSetup({ deferred: true });
  const pending = api.playSpeech("message", "Hello", () => {});
  await new Promise((resolve) => setTimeout(resolve, 0));
  api.configureSpeechPlayback({ ...config, enabled: false }); stats.resolve(); await pending;
  assert.equal(stats.reads, 0); assert.equal(stats.closed, 1); assert.equal(api.getSpeechPlaybackState().status, "idle");
});

test("manual pause and resume reuse the active TTS stream without synthesis or playback duplication", async () => {
  const { api, stats } = await playbackSetup();
  const pending = api.playSpeech("message", "Hello", () => {});
  await new Promise((resolve) => setTimeout(resolve, 5));
  await api.playSpeech("message", "Hello", () => {});
  assert.equal(api.getSpeechPlaybackState().status, "paused");
  await api.playSpeech("message", "Hello", () => {});
  assert.equal(api.getSpeechPlaybackState().status, "playing");
  await pending;
  assert.equal(stats.syntheses, 1); assert.equal(stats.reads, 1);
});

test("manual retry after transport failure reconciles the same request identity instead of remaining stuck or creating another paid attempt", async () => {
  const { api, stats } = await playbackSetup({ failTimes: 1 }); const errors = [];
  await api.playSpeech("message", "Hello", (reason) => errors.push(reason));
  await api.playSpeech("message", "Hello", (reason) => errors.push(reason), { manual: true });
  assert.equal(stats.syntheses, 2); assert.equal(stats.requests[0].requestID, stats.requests[1].requestID);
  assert.equal(stats.requests[1].retry, false); assert.equal(stats.reads, 1); assert.equal(errors.length, 1);
});

test("uncertain synthesis never retries automatically; an explicit retry creates one new identity with retry permission", async () => {
  const { api, stats } = await playbackSetup({ statuses: ["uncertain", "pending"] });
  await api.playSpeech("message", "Hello", () => {});
  await api.playSpeech("message", "Hello", () => {});
  assert.equal(stats.syntheses, 1);
  await api.playSpeech("message", "Hello", () => {}, { manual: true });
  assert.equal(stats.syntheses, 2); assert.notEqual(stats.requests[0].requestID, stats.requests[1].requestID);
  assert.equal(stats.requests[1].retry, true); assert.equal(stats.reads, 1);
});

test("expired metadata and a 410 media response allow a bounded fresh synthesis on manual playback", async () => {
  const expired = await playbackSetup({ statuses: ["expired", "pending"] });
  await expired.api.playSpeech("message", "Hello", () => {});
  await expired.api.playSpeech("message", "Hello", () => {}, { manual: true });
  assert.equal(expired.stats.syntheses, 2); assert.notEqual(expired.stats.requests[0].requestID, expired.stats.requests[1].requestID);
  assert.equal(expired.stats.requests[1].retry, false);
  const gone = await playbackSetup({ expiredReads: 1 });
  await gone.api.playSpeech("message", "Hello", () => {}, { manual: true });
  assert.equal(gone.stats.syntheses, 2); assert.equal(gone.stats.reads, 2);
  assert.notEqual(gone.stats.requests[0].requestID, gone.stats.requests[1].requestID);
  const stillGone = await playbackSetup({ expiredReads: Infinity });
  await stillGone.api.playSpeech("message", "Hello", () => {}, { manual: true });
  assert.equal(stillGone.stats.syntheses, 2); assert.equal(stillGone.stats.reads, 2);
});

test("long mixed-language splitting preserves every character and the per-part weighted budget", async () => {
  const { api } = await playbackSetup(); const text = `${"中文 words! ".repeat(400)}Tail.`;
  const parts = api.splitSpeechText(text);
  assert.equal(parts.join(""), text.replace(/\s+/g, " ").trim());
  for (const part of parts) assert.ok([...part].reduce((sum, character) => sum + (/\p{Script=Han}/u.test(character) ? 2 : 1), 0) <= 1800);
});
