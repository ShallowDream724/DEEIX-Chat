import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const compiled = transformSync(readFileSync(new URL("./use-chat-media-rail-feedback.ts", import.meta.url), "utf8"), {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;

function setup() {
  const listeners = new Set();
  const reducedMotion = { matches: false };
  let cleanup;
  class Element {
    style = { removeProperty(name) { delete this[name]; } };
    hovered = false;
    lines = [];
    matches() { return this.hovered; }
    querySelectorAll() { return this.lines; }
  }
  class Observer {
    constructor(callback) { this.callback = callback; }
    observe(target) { this.callback([{ target, isIntersecting: true }]); }
    disconnect() {}
  }
  const module = { exports: {} };
  const run = new Script(`(function(require, module, exports) { ${compiled}\n})`).runInNewContext({ HTMLElement: Element, IntersectionObserver: Observer, matchMedia: () => reducedMotion });
  run((name) => {
    if (name === "react") return { useEffect(callback) { cleanup = callback(); } };
    if (name === "@/shared/lib/media-spectrum") return { MEDIA_SPECTRUM_BANDS: 8 };
    if (name === "@/shared/lib/media-activity") return { subscribeMediaActivity(callback) { listeners.add(callback); return () => listeners.delete(callback); } };
    throw Error(`Unexpected dependency ${name}`);
  }, module, module.exports);
  const mount = (count = 8) => {
    const root = new Element(); root.lines = Array.from({ length: count }, () => new Element());
    module.exports.useChatMediaRailFeedback({ current: root }, "test");
    return { root, lines: root.lines, dispose: cleanup };
  };
  const emit = (bands = [1, 0.8, 0.4, 0.2, 0.1, 0, 0, 0], active = true, level = 0.6) => {
    for (const listener of listeners) listener({ active, level, bands: bands && Float32Array.from(bands) });
  };
  return { mount, emit, listeners, reducedMotion };
}

test("bass starts centrally and the measured beat reaches both edges without adding bars", () => {
  const state = setup();
  const left = state.mount();
  state.emit();
  assert.match(left.lines[3].style.transform, /scaleX/);
  assert.equal(left.lines[0].style.transform, undefined);
  for (let frame = 0; frame < 5; frame++) state.emit();
  assert.match(left.lines[0].style.transform, /scaleX/);
  assert.equal(left.lines[0].style.transform, left.lines[7].style.transform);
  assert.notEqual(left.lines[3].style.transform, left.lines[0].style.transform);
  assert.equal(left.lines.length, 8);
  left.dispose(); assert.equal(state.listeners.size, 0);
});

test("silent frequency bands decay back to the unchanged navigation baseline", () => {
  const state = setup();
  const left = state.mount();
  state.emit(); assert.match(left.lines[3].style.transform, /scaleX/);
  for (let frame = 0; frame < 35; frame++) state.emit(new Array(8).fill(0), true, 0);
  for (const line of left.lines) assert.equal(line.style.transform, undefined);
  left.dispose();
});

test("one or two existing lines still represent middle frequencies instead of sampling only the spectrum edges", () => {
  for (const count of [1, 2]) {
    const state = setup(); const rail = state.mount(count);
    state.emit([0, 0, 0.8, 0, 0, 0.6, 0, 0], true, 0);
    for (const line of rail.lines) assert.match(line.style.transform, /scaleX/);
    assert.equal(rail.lines.length, count);
    rail.dispose();
  }
});

test("pause, missing spectrum, reduced motion and navigation restore the original lines immediately", () => {
  const state = setup();
  const right = state.mount(2);
  const expectRestored = () => { for (const line of right.lines) { assert.equal(line.style.transform, undefined); assert.equal(line.style.opacity, undefined); } };
  state.emit(); state.emit(undefined, false); expectRestored();
  state.emit(); state.emit(null); expectRestored();
  state.emit(); state.reducedMotion.matches = true; state.emit(); expectRestored();
  state.reducedMotion.matches = false; state.emit(); right.root.hovered = true; state.emit(); expectRestored();
  right.dispose(); assert.equal(state.listeners.size, 0);
});
