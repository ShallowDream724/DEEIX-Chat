import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { transformSync } from "next/dist/build/swc/index.js";
import * as history from "./chat-history-window.ts";
import * as scrollAnchoring from "./chat-history-scroll-anchor.ts";

const nodeRequire = createRequire(import.meta.url);
const markdownViewStateStub = { createMarkdownViewStateStore: () => ({}), MarkdownViewStateProvider: ({ children }) => children };
const source = readFileSync(new URL("../components/sections/chat-history-window.tsx", import.meta.url), "utf8");
const compiled = transformSync(source, {
  jsc: { parser: { syntax: "typescript", tsx: true }, transform: { react: { runtime: "automatic" } }, target: "es2022" },
  module: { type: "commonjs" },
}).code;
const adapter = { exports: {} };
new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
  (name) => {
    if (name === "@/features/chat/model/chat-history-window") return history;
    if (name === "@/features/chat/model/chat-history-scroll-anchor") return scrollAnchoring;
    if (name === "@/features/chat/model/conversation-screenshot") return { MAX_SCREENSHOT_MESSAGES: 30 };
    if (name === "@/components/ui/message-scroller") return { useMessageScrollerNavigation: () => () => () => {} };
    if (name === "@/shared/components/markdown/markdown-view-state") return markdownViewStateStub;
    if (["react", "react/jsx-runtime", "react-dom"].includes(name)) return nodeRequire(name);
    throw new Error(`Unexpected dependency: ${name}`);
  }, adapter, adapter.exports,
);
const { ChatHistoryWindowItem } = adapter.exports;
const RenderCounterContext = React.createContext(null);
function HeavyContent() {
  React.useContext(RenderCounterContext).count += 1;
  return React.createElement("div", null, "Long prose, thinking, tables and interactive content");
}
const message = (index, role = index % 2 ? "assistant" : "user") => ({
  key: `m${index}`, publicID: `p${index}`, role, content: "answer ".repeat(30),
});

test("mixed long history initially mounts only the expensive tail and preserves every shell", () => {
  const rendered = { count: 0 };
  const rows = Array.from({ length: 300 }, (_, index) => React.createElement("section", { key: index, "data-message-id": `m${index}` },
    React.createElement(ChatHistoryWindowItem, {
      message: message(index), initiallyMounted: history.chatHistoryShouldStartMounted(index, 300), forceMounted: false,
    }, React.createElement(HeavyContent)),
  ));
  const markup = renderToStaticMarkup(React.createElement(RenderCounterContext.Provider, { value: rendered }, React.createElement("main", null, rows)));
  assert.equal(rendered.count, history.CHAT_HISTORY_INITIAL_ROWS);
  assert.equal((markup.match(/data-message-id=/g) ?? []).length, 300);
  assert.equal((markup.match(/data-chat-history-mounted="false"/g) ?? []).length, 296);
});

test("active and screenshot-selected rows mount even outside the initial tail", () => {
  const rendered = { count: 0 };
  renderToStaticMarkup(React.createElement(RenderCounterContext.Provider, { value: rendered }, React.createElement(ChatHistoryWindowItem, {
    message: message(1), initiallyMounted: false, forceMounted: true,
  }, React.createElement(HeavyContent))));
  assert.equal(rendered.count, 1);
});

test("unloaded geometry estimation stays bounded without parsing message content", () => {
  const content = { length: 10_000_000, toString: () => { throw new Error("content was parsed"); } };
  const large = { ...message(1), content, attachments: Array(10).fill({}), processTrace: { upstreamThink: { contentMarkdown: content } } };
  assert.equal(history.estimateChatHistoryHeight(large, 400), 4000);
  assert.ok(history.estimateChatHistoryHeight(message(1), 350) > history.estimateChatHistoryHeight(message(1), 1000));
});

test("screenshot materialization targets only the requested messages and always exposes release", async () => {
  const root = {};
  const selected = new Set(["p10", "p25"]);
  let released = false;
  const unregister = history.registerChatHistoryMaterializer(root, async (ids, signal) => {
    assert.equal(ids, selected);
    assert.equal(signal.aborted, false);
    return () => { released = true; };
  });
  const release = await history.materializeChatHistory(root, selected, new AbortController().signal);
  assert.equal(released, false);
  release();
  assert.equal(released, true);
  unregister();
  assert.equal(typeof await history.materializeChatHistory(root, null), "function");
});

test("aborted capture never materializes a history target", async () => {
  const root = {};
  let called = false;
  const unregister = history.registerChatHistoryMaterializer(root, async () => { called = true; return () => {}; });
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(history.materializeChatHistory(root, null, controller.signal), { name: "AbortError" });
  assert.equal(called, false);
  unregister();
});

// Drive the actual row adapter with a small hook host. The fake geometry is
// intentional: it exercises scroll correction deterministically without a
// browser's scroll anchoring hiding an incorrect placeholder implementation.
function createRowHarness({ trackScroll = false } = {}) {
  const values = [];
  const dependencies = [];
  let cursor = 0;
  let effects = [];
  let entry;
  let focused = false;
  let selected = false;
  const viewport = { scrollTop: 1000, getBoundingClientRect: () => ({ top: 0 }) };
  const bounds = { top: -1000, height: 120, width: 800 };
  const node = { style: {}, dataset: {}, contains: () => focused, querySelector: () => null,
    getBoundingClientRect: () => trackScroll ? { ...bounds, top: bounds.top - (viewport.scrollTop - 1000) } : bounds };
  const windowContext = {
    register: (value) => { entry = value; return () => {}; }, viewport: () => viewport, capturing: () => false, atBottom: () => false,
  };
  const hooks = {
    ...React,
    useContext: () => windowContext,
    useRef: (initial) => { const index = cursor++; values[index] ??= { current: initial }; return values[index]; },
    useState: (initial) => { const index = cursor++; if (!(index in values)) values[index] = initial; return [values[index], (value) => { values[index] = value; }]; },
    useLayoutEffect: (effect, deps) => {
      const index = cursor++;
      if (!dependencies[index] || deps.some((dep, depIndex) => dep !== dependencies[index][depIndex])) effects.push(effect);
      dependencies[index] = deps;
    },
  };
  const rowModule = { exports: {} };
  new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
    (name) => {
      if (name === "react") return hooks;
      if (name === "@/features/chat/model/chat-history-window") return history;
      if (name === "@/features/chat/model/chat-history-scroll-anchor") return scrollAnchoring;
      if (name === "@/features/chat/model/conversation-screenshot") return { MAX_SCREENSHOT_MESSAGES: 30 };
      if (name === "@/components/ui/message-scroller") return { useMessageScrollerNavigation: () => () => () => {} };
      if (name === "@/shared/components/markdown/markdown-view-state") return markdownViewStateStub;
      return nodeRequire(name);
    }, rowModule, rowModule.exports,
  );
  const render = (forceMounted = false) => {
    cursor = 0;
    effects = [];
    const view = rowModule.exports.ChatHistoryWindowItem({ message: message(1), initiallyMounted: false, forceMounted, children: "expensive row" });
    view.props.ref.current = node;
    for (const effect of effects) effect();
    return { ...view, viewState: view.props.children.props.store, props: { ...view.props, children: view.props.children.props.children } };
  };
  return { render, bounds, viewport, entry: () => entry, focus: (value) => { focused = value; }, select: (value) => { selected = value; },
    globals: { document: { activeElement: {}, querySelector: () => null }, window: { getSelection: () => ({ isCollapsed: !selected, rangeCount: 1, getRangeAt: () => ({ intersectsNode: () => selected }) }) } } };
}

test("observer entry mounts remote content, caches its measured height and keeps reading position", () => {
  const harness = createRowHarness();
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  Object.assign(globalThis, harness.globals);
  try {
    const initial = harness.render();
    assert.equal(initial.props.children, null);
    initial.viewState.tablePage = 7;
    const previousHeight = Number.parseFloat(harness.entry().node.style.height);
    const previousScroll = harness.viewport.scrollTop;
    harness.entry().show(true);
    harness.bounds.height = 650;
    const loaded = harness.render();
    assert.equal(loaded.props.children, "expensive row");
    assert.equal(loaded.viewState, initial.viewState);
    assert.equal(harness.viewport.scrollTop, previousScroll + 650 - previousHeight);
    harness.entry().show(false);
    const unloaded = harness.render();
    assert.equal(unloaded.props.children, null);
    assert.equal(unloaded.props.style.height, 650);
    assert.equal(unloaded.viewState.tablePage, 7);
    assert.equal(harness.viewport.scrollTop, previousScroll + 650 - previousHeight);
  } finally {
    globalThis.window = previousWindow;
    globalThis.document = previousDocument;
  }
});

test("focus, text selection and live rows cannot be unloaded by the visibility observer", () => {
  const harness = createRowHarness();
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  Object.assign(globalThis, harness.globals);
  try {
    harness.render();
    harness.entry().show(true);
    harness.render();
    harness.focus(true);
    harness.entry().show(false);
    assert.equal(harness.render().props.children, "expensive row");
    harness.focus(false);
    harness.select(true);
    harness.entry().show(false);
    assert.equal(harness.render().props.children, "expensive row");
    harness.select(false);
    harness.entry().show(false);
    assert.equal(harness.render(true).props.children, "expensive row");
  } finally {
    globalThis.window = previousWindow;
    globalThis.document = previousDocument;
  }
});

test("paragraph anchor compensation is consumed once while the row still caches its new height", () => {
  const harness = createRowHarness();
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  Object.assign(globalThis, harness.globals);
  try {
    harness.render();
    const previousScroll = harness.viewport.scrollTop;
    harness.entry().node.dataset.markdownAnchorAdjustment = "17";
    harness.entry().show(true);
    harness.bounds.height = 650;
    harness.render();
    assert.equal(harness.viewport.scrollTop, previousScroll);
    assert.equal(harness.entry().node.dataset.markdownAnchorAdjustment, undefined);
    harness.entry().show(false);
    assert.equal(harness.render().props.style.height, 650);
  } finally {
    globalThis.window = previousWindow;
    globalThis.document = previousDocument;
  }
});

test("a jump inside an unknown placeholder stays inside the same row when its estimate changes", () => {
  const harness = createRowHarness();
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  Object.assign(globalThis, harness.globals);
  try {
    harness.render();
    const estimate = Number.parseFloat(harness.entry().node.style.height);
    harness.bounds.top = -estimate / 2;
    const previousScroll = harness.viewport.scrollTop;
    harness.entry().show(true);
    harness.bounds.height = 650;
    harness.render();
    assert.equal(harness.viewport.scrollTop, previousScroll + (650 - estimate) / 2);
    const afterMountScroll = harness.viewport.scrollTop;
    harness.bounds.height = 700;
    harness.entry().resize();
    assert.equal(harness.viewport.scrollTop, afterMountScroll);
  } finally {
    globalThis.window = previousWindow;
    globalThis.document = previousDocument;
  }
});

test("repeated viewport exit and reentry converge on cached geometry without scroll feedback", () => {
  const harness = createRowHarness({ trackScroll: true });
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  Object.assign(globalThis, harness.globals);
  try {
    harness.render();
    const estimate = Number.parseFloat(harness.entry().node.style.height);
    harness.bounds.top = -estimate / 2;
    harness.bounds.height = 650;
    harness.entry().show(true);
    harness.render();
    const settledScroll = harness.viewport.scrollTop;
    assert.equal(harness.entry().node.getBoundingClientRect().top, -325);
    for (let index = 0; index < 20; index += 1) {
      harness.entry().show(false);
      assert.equal(harness.render().props.style.height, 650);
      harness.entry().show(true);
      harness.render();
      harness.entry().resize();
      assert.equal(harness.viewport.scrollTop, settledScroll);
      assert.equal(harness.entry().node.getBoundingClientRect().top, -325);
    }
  } finally {
    globalThis.window = previousWindow;
    globalThis.document = previousDocument;
  }
});

test("capture pins materialize exactly their targets and abort restores the viewport and releases pins", async () => {
  const pendingEffects = [];
  const frames = new Map();
  let frameID = 0;
  const events = new Map();
  const viewport = { scrollTop: 0, scrollHeight: 500, clientHeight: 500, getBoundingClientRect: () => ({ top: 0, bottom: 500 }),
    addEventListener: (name, callback) => events.set(name, callback), removeEventListener: () => {} };
  const content = { children: [], addEventListener: () => {}, removeEventListener: () => {} };
  const globals = {
    document: { addEventListener: () => {}, removeEventListener: () => {} },
    Node: { DOCUMENT_POSITION_FOLLOWING: 4 },
    IntersectionObserver: class { observe() {} unobserve() {} disconnect() {} },
    ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: (callback) => { const id = ++frameID; frames.set(id, callback); return id; },
    cancelAnimationFrame: (id) => frames.delete(id),
  };
  const previous = Object.fromEntries(Object.keys(globals).map((name) => [name, globalThis[name]]));
  Object.assign(globalThis, globals);
  let cleanup;
  try {
    const hooks = { ...React, useRef: (current) => ({ current }), useMemo: (factory) => factory(), useEffect: (effect) => pendingEffects.push(effect) };
    const providerModule = { exports: {} };
    new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
      (name) => {
        if (name === "react") return hooks;
        if (name === "react-dom") return { flushSync: (callback) => callback() };
        if (name === "@/features/chat/model/chat-history-window") return history;
        if (name === "@/features/chat/model/chat-history-scroll-anchor") return scrollAnchoring;
        if (name === "@/features/chat/model/conversation-screenshot") return { MAX_SCREENSHOT_MESSAGES: 30 };
        if (name === "@/components/ui/message-scroller") return { useMessageScrollerNavigation: () => () => () => {} };
        if (name === "@/shared/components/markdown/markdown-view-state") return markdownViewStateStub;
        return nodeRequire(name);
      }, providerModule, providerModule.exports,
    );
    const view = providerModule.exports.ChatHistoryWindow({ viewportRef: { current: viewport }, contentRef: { current: content }, children: null });
    const visibility = Array(60).fill(false);
    for (let index = 0; index < 60; index += 1) {
      view.props.value.register({ publicID: `p${index}`, visible: false, pins: 0, resize: () => {},
        node: { compareDocumentPosition: (other) => other.index > index ? 4 : 2, index,
          getBoundingClientRect: () => ({ top: index * 500, bottom: index * 500 + 300 }) },
        show: (visible) => { visibility[index] = visible; },
      });
    }
    cleanup = pendingEffects[0]();
    const controller = new AbortController();
    const capture = history.materializeChatHistory(content, new Set(["p20", "p45"]), controller.signal);
    assert.deepEqual(visibility.flatMap((visible, index) => visible ? [index] : []), [20, 45]);
    assert.equal(view.props.value.capturing(), true);
    viewport.scrollTop = 9000;
    controller.abort();
    await assert.rejects(capture, { name: "AbortError" });
    assert.equal(viewport.scrollTop, 0);
    assert.equal(view.props.value.capturing(), false);
    assert.ok(visibility.every((visible) => !visible));
  } finally {
    cleanup?.();
    Object.assign(globalThis, previous);
  }
});
