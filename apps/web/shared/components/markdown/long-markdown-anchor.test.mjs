import assert from "node:assert/strict";
import test from "node:test";
import { captureMarkdownMountAnchor, settleMarkdownMountAnchor } from "./long-markdown-anchor.ts";

function geometry({ messageBottom = 1500, blockBottom = -20 } = {}) {
  const events = new Map();
  const viewport = {
    scrollTop: 1000,
    getBoundingClientRect: () => ({ top: 0 }),
    addEventListener: (name, listener) => {
      const listeners = events.get(name) ?? new Set();
      listeners.add(listener);
      events.set(name, listeners);
    },
    removeEventListener: (name, listener) => events.get(name)?.delete(listener),
  };
  const row = { dataset: {}, getBoundingClientRect: () => ({ top: -500, bottom: messageBottom }) };
  const bounds = { height: 64, bottom: blockBottom };
  const block = {
    isConnected: true,
    closest: (selector) => selector.includes("viewport") ? viewport : row,
    getBoundingClientRect: () => bounds,
  };
  return { block, bounds, viewport, row, events };
}

test("mounting an above-viewport block preserves the visible line inside a crossing message", () => {
  const oldFrame = globalThis.requestAnimationFrame;
  const frames = [];
  globalThis.requestAnimationFrame = (callback) => frames.push(callback);
  try {
    const { block, bounds, viewport, row } = geometry();
    const anchor = captureMarkdownMountAnchor(block);
    assert.ok(anchor);
    const visibleLineBefore = 400;
    bounds.height = 200;
    settleMarkdownMountAnchor(anchor, block, false);
    const visibleLineAfter = visibleLineBefore + 200 - 64 - (viewport.scrollTop - 1000);
    assert.equal(visibleLineAfter, visibleLineBefore);
    assert.ok(row.dataset.markdownAnchorAdjustment);
    while (frames.length) frames.shift()();
    assert.equal(row.dataset.markdownAnchorAdjustment, undefined);
  } finally {
    globalThis.requestAnimationFrame = oldFrame;
  }
});

test("multiple blocks in one commit accumulate their own corrections without losing a visible anchor", () => {
  const oldFrame = globalThis.requestAnimationFrame;
  globalThis.requestAnimationFrame = () => 1;
  try {
    const { block, bounds, viewport, row } = geometry();
    const secondBounds = { height: 64, bottom: -5 };
    const second = { ...block, getBoundingClientRect: () => secondBounds };
    const firstAnchor = captureMarkdownMountAnchor(block);
    const secondAnchor = captureMarkdownMountAnchor(second);
    bounds.height = 128;
    secondBounds.height = 48;
    settleMarkdownMountAnchor(firstAnchor, block, false);
    settleMarkdownMountAnchor(secondAnchor, second, false);
    assert.equal(viewport.scrollTop, 1048);
    assert.ok(row.dataset.markdownAnchorAdjustment);
  } finally {
    globalThis.requestAnimationFrame = oldFrame;
  }
});

test("history owns whole rows above the viewport and below-anchor blocks never adjust scrolling", () => {
  assert.equal(captureMarkdownMountAnchor(geometry({ messageBottom: -1 }).block), null);
  assert.equal(captureMarkdownMountAnchor(geometry({ blockBottom: 1 }).block), null);
});

test("reader scrolling, input intent and screenshot materialization cancel pending correction", () => {
  for (const reason of ["scroll", "wheel", "touchstart", "pointerdown", "keydown", "capture"]) {
    const { block, bounds, viewport, events, row } = geometry();
    const anchor = captureMarkdownMountAnchor(block);
    assert.ok(anchor);
    bounds.height = 500;
    if (reason === "scroll") viewport.scrollTop += 40;
    events.get(reason)?.forEach((listener) => { listener(); });
    const expectedScroll = viewport.scrollTop;
    settleMarkdownMountAnchor(anchor, block, reason === "capture");
    assert.equal(viewport.scrollTop, expectedScroll, reason);
    assert.equal(row.dataset.markdownAnchorAdjustment, undefined);
    assert.ok(Array.from(events.values()).every((listeners) => listeners.size === 0));
  }
});
