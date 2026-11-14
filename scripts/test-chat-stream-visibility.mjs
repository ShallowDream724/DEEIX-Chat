import assert from "node:assert/strict";
import { getEventListeners } from "node:events";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("../apps/web/features/chat/hooks/use-chat-stream-buffer.ts", import.meta.url), "utf8");
const compiled = `${stripTypeScriptTypes(source).replace(/^import .*;\r?\n/gm, "").replace(/^export /gm, "")}\nexports.useChatStreamBuffer = useChatStreamBuffer;`;

function setup({ hidden = false } = {}) {
  const document = Object.assign(new EventTarget(), { hidden });
  const window = new EventTarget();
  const frames = new Map();
  const timers = new Map();
  const effects = [];
  const thinkUpdates = [];
  const thinking = new Map();
  let exchanges = {};
  let textUpdates = 0;
  let now = 100;
  let nextHandle = 0;
  Object.assign(window, {
    requestAnimationFrame(callback) {
      frames.set(++nextHandle, callback);
      return nextHandle;
    },
    cancelAnimationFrame(handle) { frames.delete(handle); },
    setTimeout(callback, delay) {
      timers.set(++nextHandle, { callback, due: now + delay });
      return nextHandle;
    },
    clearTimeout(handle) { timers.delete(handle); },
  });
  const scope = {
    exports: {},
    document,
    window,
    performance: { now: () => now },
    React: {
      useRef: (current) => ({ current }),
      useCallback: (callback) => callback,
      useEffect: (effect) => effects.push(effect),
    },
    clearLiveUpstreamThinkTrace: (runID) => thinking.delete(runID),
    upsertLiveUpstreamThinkTrace(runID, event) {
      thinkUpdates.push({ runID, event });
      thinking.set(runID, typeof event.contentMarkdown === "string"
        ? event.contentMarkdown
        : (thinking.get(runID) ?? "") + (event.delta ?? ""));
    },
  };
  vm.runInNewContext(compiled, scope);
  // biome-ignore lint/correctness/useHookAtTopLevel: The VM mocks React hooks to test browser scheduling without a renderer.
  const api = scope.exports.useChatStreamBuffer({
    setPendingExchanges(update) {
      const next = update(exchanges);
      if (next !== exchanges) textUpdates++;
      exchanges = next;
    },
  });
  const cleanups = effects.map((effect) => effect()).filter(Boolean);
  return {
    api, document, window, frames, timers, thinking, thinkUpdates,
    get exchanges() { return exchanges; },
    get textUpdates() { return textUpdates; },
    start(exchangeKey = "reply", runID = "run") {
      exchanges[exchangeKey] = { assistantText: "", assistantPending: true, assistantStreaming: false };
      api.startStream(exchangeKey, runID);
    },
    setHidden(value) {
      document.hidden = value;
      document.dispatchEvent(new Event("visibilitychange"));
    },
    pageshow() { window.dispatchEvent(new Event("pageshow")); },
    advance(milliseconds) {
      now += milliseconds;
      for (const [handle, timer] of [...timers]) {
        if (timer.due <= now && timers.delete(handle)) timer.callback();
      }
    },
    frame() {
      // Background tabs stop animation frames even when a timer has already queued one.
      if (document.hidden) return;
      for (const [handle, callback] of [...frames]) {
        if (frames.delete(handle)) callback(now);
      }
    },
    dispose() { for (const cleanup of cleanups) cleanup(); },
  };
}

function think(delta, overrides = {}) {
  return { type: "upstream_think_delta", status: "streaming", eventID: "think", roundID: "round", delta, ...overrides };
}

function assertIdle(state) {
  assert.equal(state.frames.size, 0, "no animation frames remain");
  assert.equal(state.timers.size, 0, "no flush timers remain");
}

test("returning to the foreground flushes received text and a large thinking backlog exactly once", () => {
  const state = setup();
  state.start();
  state.api.enqueueStreamText("reply", "visible ");
  state.api.enqueueUpstreamThinkDelta("reply", think("initial"));
  state.frame();
  assert.equal(state.exchanges.reply.assistantText, "visible ");
  assert.equal(state.thinking.get("run"), "initial");

  state.api.enqueueStreamText("reply", "queued ");
  state.api.enqueueUpstreamThinkDelta("reply", think("queued"));
  assert.equal(state.timers.size, 2);
  state.setHidden(true);
  assertIdle(state);
  const backlog = "思考".repeat(3000);
  state.api.enqueueStreamText("reply", "hidden one ");
  state.api.enqueueStreamText("reply", "hidden two");
  state.api.enqueueUpstreamThinkDelta("reply", think(backlog));
  state.advance(120_000);
  state.frame();
  assertIdle(state);
  assert.equal(state.exchanges.reply.assistantText, "visible ");
  assert.equal(state.thinking.get("run"), "initial");

  state.setHidden(false);
  assert.equal(state.exchanges.reply.assistantText, "visible queued hidden one hidden two");
  assert.equal(state.thinking.get("run"), `initialqueued${backlog}`);
  assert.equal(state.textUpdates, 2);
  assert.equal(state.thinkUpdates.length, 2);
  assertIdle(state);
  state.pageshow();
  state.setHidden(false);
  assert.equal(state.textUpdates, 2);
  assert.equal(state.thinkUpdates.length, 2);
  assertIdle(state);

  state.api.enqueueStreamText("reply", " fresh");
  state.api.enqueueUpstreamThinkDelta("reply", think("n".repeat(144)));
  assert.equal(state.timers.size, 2);
  assert.equal(state.textUpdates, 2);
  state.advance(50);
  state.frame();
  assert.equal(state.exchanges.reply.assistantText, "visible queued hidden one hidden two fresh");
  assert.equal(state.thinkUpdates.at(-1).event.delta, "n".repeat(48));
  for (let index = 0; index < 2; index++) {
    state.advance(40);
    state.frame();
  }
  assert.equal(state.thinking.get("run"), `initialqueued${backlog}${"n".repeat(144)}`);
  assertIdle(state);
  state.dispose();
});

test("repeated background transitions cancel both queued frames and delayed flushes without losing later chunks", () => {
  const state = setup();
  state.start();
  let expected = "";
  for (let index = 0; index < 3; index++) {
    state.advance(50);
    state.api.enqueueStreamText("reply", "frame");
    state.api.enqueueUpstreamThinkDelta("reply", think("frame"));
    assert.equal(state.frames.size, 2);
    state.setHidden(true);
    assertIdle(state);
    state.api.enqueueStreamText("reply", "hidden");
    state.api.enqueueUpstreamThinkDelta("reply", think("hidden"));
    state.setHidden(false);
    expected += "framehidden";
    assert.equal(state.exchanges.reply.assistantText, expected);
    assert.equal(state.thinking.get("run"), expected);
    assertIdle(state);

    state.api.enqueueStreamText("reply", "timer");
    state.api.enqueueUpstreamThinkDelta("reply", think("timer"));
    assert.equal(state.timers.size, 2);
    state.setHidden(true);
    assertIdle(state);
    state.setHidden(false);
    expected += "timer";
    assert.equal(state.exchanges.reply.assistantText, expected);
    assert.equal(state.thinking.get("run"), expected);
    assertIdle(state);
  }
  assert.equal(getEventListeners(state.document, "visibilitychange").length, 1);
  assert.equal(getEventListeners(state.window, "pageshow").length, 1);
  state.dispose();
});

test("pageshow catches up an initially hidden page and hidden timer callbacks do not queue frames", () => {
  const state = setup({ hidden: true });
  state.start();
  state.api.enqueueStreamText("reply", "hidden");
  state.api.enqueueUpstreamThinkDelta("reply", think("hidden"));
  state.pageshow();
  assertIdle(state);
  assert.equal(state.textUpdates, 0);
  assert.equal(state.thinkUpdates.length, 0);
  state.document.hidden = false;
  state.pageshow();
  assert.equal(state.exchanges.reply.assistantText, "hidden");
  assert.equal(state.thinking.get("run"), "hidden");

  state.api.enqueueStreamText("reply", "timer");
  state.api.enqueueUpstreamThinkDelta("reply", think("timer"));
  assert.equal(state.timers.size, 2);
  // A throttled visibility event may arrive after an already scheduled timer.
  state.document.hidden = true;
  state.advance(50);
  assertIdle(state);
  state.document.hidden = false;
  state.pageshow();
  assert.equal(state.exchanges.reply.assistantText, "hiddentimer");
  assert.equal(state.thinking.get("run"), "hiddentimer");
  assertIdle(state);

  state.advance(50);
  state.api.enqueueStreamText("reply", "frame");
  state.api.enqueueUpstreamThinkDelta("reply", think("frame"));
  assert.equal(state.frames.size, 2);
  state.document.hidden = true;
  state.frame();
  state.document.hidden = false;
  state.pageshow();
  state.frame();
  assert.equal(state.exchanges.reply.assistantText, "hiddentimerframe");
  assert.equal(state.thinking.get("run"), "hiddentimerframe");
  assertIdle(state);
  state.dispose();
});

test("completed and cleared runs are not resurrected by foreground recovery", () => {
  const state = setup();
  state.start("completed", "completed-run");
  state.start("cleared", "cleared-run");
  state.setHidden(true);
  state.api.enqueueStreamText("completed", "final text");
  state.api.enqueueUpstreamThinkDelta("completed", think("final thought", { status: "completed" }));
  state.api.flushStreamTextNow("completed");
  state.api.flushUpstreamThinkNow("completed");
  state.exchanges.completed.assistantStreaming = false;
  state.api.resetStreamBuffer("completed");
  state.api.enqueueStreamText("cleared", "discarded text");
  state.api.enqueueUpstreamThinkDelta("cleared", think("discarded thought"));
  state.api.resetStreamBuffer("cleared");
  const textUpdates = state.textUpdates;
  const thinkUpdates = state.thinkUpdates.length;
  state.setHidden(false);
  state.pageshow();
  state.advance(1000);
  state.frame();
  assert.equal(state.textUpdates, textUpdates);
  assert.equal(state.thinkUpdates.length, thinkUpdates);
  assert.equal(state.exchanges.completed.assistantText, "final text");
  assert.equal(state.exchanges.completed.assistantStreaming, false);
  assert.equal(state.exchanges.cleared.assistantText, "");
  assert.equal(state.thinking.get("completed-run"), "final thought");
  assert.equal(state.thinking.has("cleared-run"), false);
  assertIdle(state);
  state.dispose();
});

test("replacing a stream keeps only the new run's buffered content", () => {
  const state = setup();
  state.start("reply", "old");
  state.api.enqueueStreamText("reply", "old text");
  state.api.enqueueUpstreamThinkDelta("reply", think("old thought"));
  state.setHidden(true);
  state.start("reply", "new");
  state.api.enqueueStreamText("reply", "new text");
  state.api.enqueueUpstreamThinkDelta("reply", think("new thought"));
  state.setHidden(false);
  assert.equal(state.exchanges.reply.assistantText, "new text");
  assert.equal(state.thinking.has("old"), false);
  assert.equal(state.thinking.get("new"), "new thought");
  assertIdle(state);
  state.dispose();
});

test("disposal removes lifecycle listeners, cancels scheduled work, and ignores later events", () => {
  for (const hidden of [false, true]) {
    const state = setup({ hidden });
    state.start();
    state.api.enqueueStreamText("reply", "discarded");
    state.api.enqueueUpstreamThinkDelta("reply", think("discarded"));
    state.dispose();
    assertIdle(state);
    assert.equal(getEventListeners(state.document, "visibilitychange").length, 0);
    assert.equal(getEventListeners(state.window, "pageshow").length, 0);
    state.setHidden(false);
    state.pageshow();
    state.api.enqueueStreamText("reply", "late");
    state.api.enqueueUpstreamThinkDelta("reply", think("late"));
    state.advance(1000);
    state.frame();
    assert.equal(state.textUpdates, 0);
    assert.equal(state.thinkUpdates.length, 0);
    assertIdle(state);
  }
});
