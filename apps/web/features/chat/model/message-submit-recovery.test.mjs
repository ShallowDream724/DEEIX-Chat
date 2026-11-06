import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";
import { RecoveredGenerationTracker, recoverSubmittedRun, resumeFallbackPollingDelay, resumeWithRecovery } from "./message-submit-recovery.ts";

// Keep the public API/real stream parser under test while replacing network and
// unrelated browser services. Node's type stripping also runs the production TS.
const mocks = new Map(Object.entries({
  "@/shared/api/authed-client": "export const authedFetch = (...args) => globalThis.__recoveryFetch(...args); export const authedRequest = () => { throw Error('unexpected request'); };",
  "@/shared/api/file": "export const getChatFilePolicy = () => ({});",
  "@/shared/api/http-client": "export class ApiError extends Error { constructor(message, status, details, errorCode, requestId) { super(message); Object.assign(this, {status, details, errorCode, requestId}); } } export const apiRequest = () => {}; export const pathParam = encodeURIComponent;",
  "@/features/chat/model/chat-reasoning-effort": "export const normalizeMessageReasoningEffort = x => x;",
  "@/features/chat/model/chat-thread": "export const parseAttachments = () => [];",
  "@/features/chat/model/message-submit": "export const resolveAssistantInputSideUsageValue = () => 0; export const toPendingProcessTrace = x => x;",
  "@/features/chat/model/message-submit-media": "export const streamEventErrorToApiError = () => {};",
  "@/features/chat/model/upstream-think-store": "export const mergeLiveUpstreamThinkTrace = x => x; export const preserveRicherLiveUpstreamThinkTrace = x => x; export const readLiveUpstreamThinkTrace = () => {};",
  "@/features/chat/utils/chat-runtime": "export const resolveErrorReason = () => ''; export const resolveGenerationErrorId = () => '';",
}));
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (mocks.has(specifier)) return { url: `data:text/javascript,${encodeURIComponent(mocks.get(specifier))}`, shortCircuit: true };
    if (specifier.startsWith("@/")) return { url: new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, shortCircuit: true };
    return nextResolve(specifier, context);
  },
});
const { streamMessage, resumeMessageGenerationStream, ConversationStreamServerError } = await import("../../../shared/api/conversation.ts");
const { ConversationStreamDisconnectedError } = await import("../../../shared/api/conversation-stream-error.ts");
const { ApiError } = await import("@/shared/api/http-client");
const { detachPendingExchange, failPendingExchange, collectSettledExchanges } = await import("./message-submit-exchange.ts");

const makeRecovery = (overrides = {}) => recoverSubmittedRun({
  accepted: false,
  signal: new AbortController().signal,
  queryStatus: async () => undefined,
  readResult: async () => { throw Error("unexpected result read"); },
  isTerminalError: (error) => error instanceof ConversationStreamServerError,
  pause: async () => {},
  ...overrides,
});

test("pending identity detaches without creating another request", async () => {
  assert.deepEqual(await makeRecovery({ queryStatus: async () => "pending" }), { kind: "detached" });
});

test("a request lost before headers can resolve by its existing run identity", async () => {
  let reads = 0;
  const result = { assistantMessage: { status: "success" } };
  assert.deepEqual(await makeRecovery({
    queryStatus: async () => ++reads < 2 ? undefined : "success",
    readResult: async () => result,
  }), { kind: "completed", result });
  assert.equal(reads, 2);
});

test("authoritative failed payload follows the same completed settlement", async () => {
  const result = { assistantMessage: { status: "error", errorMessage: "model failed" } };
  assert.deepEqual(await makeRecovery({ queryStatus: async () => "error", readResult: async () => result }), { kind: "completed", result });
  const failure = new ConversationStreamServerError("server terminal", 502);
  await assert.rejects(makeRecovery({ queryStatus: async () => "error", readResult: async () => { throw failure; } }), (error) => error === failure);
});

test("missing and unreadable terminal identities stop at a bounded unconfirmed state", async () => {
  let queries = 0;
  assert.deepEqual(await makeRecovery({ queryStatus: async () => { queries++; return undefined; } }), { kind: "unconfirmed" });
  assert.equal(queries, 3);
  assert.deepEqual(await makeRecovery({ accepted: true, queryStatus: async () => "success", readResult: async () => null }), { kind: "unconfirmed" });
  assert.deepEqual(await makeRecovery({ queryStatus: async () => { throw Error("offline"); } }), { kind: "unconfirmed" });
  assert.deepEqual(await makeRecovery({ accepted: true, queryStatus: async () => { throw Error("offline"); } }), { kind: "detached" });
});

test("explicit cancel interrupts recovery and prevents further lookups", async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(makeRecovery({ signal: controller.signal }), { name: "AbortError" });
});

test("unknown run statuses never masquerade as terminal state", async () => {
  assert.deepEqual(await makeRecovery({ queryStatus: async () => "cancelling" }), { kind: "unconfirmed" });
});

test("polling-only completion refreshes metadata once using persisted parent and assistant", () => {
  const tracker = new RecoveredGenerationTracker();
  const user = { role: "user", publicID: "user", content: "first turn" };
  const assistant = { role: "assistant", publicID: "assistant", parentPublicID: "user", runID: "run_1", status: "pending" };
  assert.deepEqual(tracker.collect([user, assistant]), []);
  const finished = { ...assistant, status: "success" };
  assert.deepEqual(tracker.collect([user, finished], new Set(["run_1"])), []);
  assert.deepEqual(tracker.collect([user, finished]), [{ userMessage: user, assistantMessage: finished }]);
  assert.deepEqual(tracker.collect([user, finished]), []);
  assert.deepEqual(tracker.collect([{ ...finished, runID: "run_2", parentPublicID: "missing" }]), []);
});

test("historical successful turns do not start recovery; recent remounted completion does", () => {
  const now = Date.parse("2026-11-06T00:00:00Z");
  const user = { role: "user", publicID: "u" };
  const assistant = { role: "assistant", publicID: "a", parentPublicID: "u", runID: "old", status: "success", updatedAt: new Date(now - 24 * 60 * 60_000).toISOString() };
  assert.deepEqual(new RecoveredGenerationTracker().collect([user, assistant], undefined, now), []);
  const recent = { ...assistant, runID: "recent", updatedAt: new Date(now - 60_000).toISOString() };
  assert.equal(new RecoveredGenerationTracker().collect([user, recent], undefined, now).length, 1);
  const expiredRecent = { ...recent, updatedAt: new Date(now - 3 * 60_000).toISOString() };
  assert.deepEqual(new RecoveredGenerationTracker().collect([user, expiredRecent], undefined, now), []);
});

test("pending/completion tracking expires and remains bounded", () => {
  const tracker = new RecoveredGenerationTracker();
  const now = Date.now();
  const user = { role: "user", publicID: "u" };
  const assistant = { role: "assistant", publicID: "a", parentPublicID: "u", runID: "old", status: "pending" };
  tracker.collect([user, assistant], undefined, now);
  assert.deepEqual(tracker.collect([user, { ...assistant, status: "success" }], undefined, now + 31 * 60_000), []);
  tracker.collect(Array.from({ length: 129 }, (_, i) => ({ ...assistant, runID: `run_${i}` })), undefined, now);
  assert.deepEqual(tracker.collect([user, { ...assistant, runID: "run_0", status: "success" }], undefined, now), []);
  for (let i = 0; i < 129; i++) tracker.markCompleted(`done_${i}`, now);
  assert.equal(tracker.markCompleted("done_0", now), true);
  assert.equal(tracker.markCompleted("done_128", now), false);
});

test("transient resume failures reconnect the same GET with bounded backoff", async () => {
  let attempts = 0;
  const delays = [];
  const state = await resumeWithRecovery({
    signal: new AbortController().signal,
    attempt: async () => { if (++attempts < 3) throw new TypeError("reset"); },
    isPermanentFailure: () => false,
    onFailure: () => {},
    pause: async (_signal, delay) => { delays.push(delay); },
  });
  assert.equal(state, "completed");
  assert.equal(attempts, 3);
  assert.deepEqual(delays, [1000, 2000]);
});

test("resume retries exhaust, foreground restart gets a new budget, auth and cancel stop", async () => {
  let attempts = 0;
  const options = {
    signal: new AbortController().signal,
    attempt: async () => { attempts++; throw Error("offline"); },
    isPermanentFailure: () => false,
    onFailure: () => {},
    pause: async () => {},
  };
  assert.equal(await resumeWithRecovery(options), "exhausted");
  assert.equal(attempts, 4);
  assert.equal(await resumeWithRecovery({ ...options, attempt: async () => { attempts++; } }), "completed");
  assert.equal(attempts, 5);
  let authAttempts = 0;
  assert.equal(await resumeWithRecovery({ ...options, attempt: async () => { authAttempts++; throw new ApiError("forbidden", 403); }, isPermanentFailure: error => error.status === 403 }), "stopped");
  assert.equal(authAttempts, 1);
  const controller = new AbortController();
  let failures = 0;
  assert.equal(await resumeWithRecovery({ ...options, signal: controller.signal, attempt: async () => { controller.abort(); throw new DOMException("Aborted", "AbortError"); }, onFailure: () => { failures++; } }), "stopped");
  assert.equal(failures, 0);
});

test("exhausted resume polling backs off to a bounded slower cadence", () => {
  assert.deepEqual([0, 1, 2, 3, 100].map(resumeFallbackPollingDelay), [15000, 30000, 60000, 60000, 60000]);
});

test("detached exchange hands both pending and terminal persisted state to resume", () => {
  const exchange = { key: "x", runID: "run_1", tempUserPublicID: "temp_u", tempAssistantPublicID: "temp_a", assistantText: "partial", assistantPending: true, assistantStreaming: true };
  const detached = detachPendingExchange(exchange, "reconnecting");
  assert.equal(detached.assistantPending, true);
  assert.equal(detached.assistantStreaming, false);
  assert.equal(detached.assistantStatus, "pending");
  assert.equal(detached.assistantInlineAlert, undefined);
  for (const status of ["pending", "success", "error"]) {
    const message = { publicID: "assistant", parentPublicID: "user", role: "assistant", runID: "run_1", status };
    assert.deepEqual(collectSettledExchanges({ x: detached }, new Set(["assistant"]), [message]).completedKeys, ["x"]);
    assert.deepEqual(collectSettledExchanges({ x: exchange }, new Set(["assistant"]), [message]).completedKeys, []);
  }
  const unresolved = detachPendingExchange(exchange, "", { title: "unknown", message: "check later" });
  assert.equal(unresolved.assistantPending, false);
  assert.notEqual(unresolved.assistantStatus, "error");
  assert.equal(failPendingExchange(exchange, { clientRunID: "run_1", title: "failed", errorMessage: "server error" }).assistantStatus, "error");
});

const submit = (options) => streamMessage("token", "conversation", { content: "test", clientRunID: "run_1" }, options);
const response = (events, failAfter = false) => new Response(new ReadableStream({
  start(controller) {
    for (const event of events) controller.enqueue(new TextEncoder().encode(JSON.stringify(event)));
  },
  pull(controller) {
    if (failAfter) controller.error(new TypeError("connection reset"));
    else controller.close();
  },
}));

test("HTTP rejection and explicit terminal error retain authoritative failure", async () => {
  const rejection = new ApiError("too large", 413, undefined, "input_limit");
  globalThis.__recoveryFetch = async () => { throw rejection; };
  await assert.rejects(submit(), (error) => error === rejection);
  globalThis.__recoveryFetch = async () => response([{ type: "error", status: 502, message: "model rejected", requestId: "request_1" }]);
  await assert.rejects(submit(), (error) => error instanceof ConversationStreamServerError && error.requestId === "request_1");
});

test("fetch loss, stream reset and missing terminal payload are transport uncertainty", async () => {
  globalThis.__recoveryFetch = async () => { throw new TypeError("network"); };
  await assert.rejects(submit(), (error) => error instanceof ConversationStreamDisconnectedError && !error.accepted);
  for (const stream of [response([], true), response([{ type: "delta", delta: "partial" }]), new Response(null)]) {
    globalThis.__recoveryFetch = async () => stream;
    await assert.rejects(submit(), (error) => error instanceof ConversationStreamDisconnectedError && error.accepted);
  }
});

test("a bare CDN timeout reconciles the existing pending run without reposting", async () => {
  let submissions = 0;
  globalThis.__recoveryFetch = async () => { submissions++; throw new ApiError("<html>524 A timeout occurred</html>", 524); };
  let disconnected;
  await assert.rejects(submit(), (error) => {
    disconnected = error;
    return error instanceof ConversationStreamDisconnectedError && !error.accepted;
  });
  assert.deepEqual(await makeRecovery({ accepted: disconnected.accepted, queryStatus: async () => "pending" }), { kind: "detached" });
  assert.equal(submissions, 1);
  const applicationError = new ApiError("upstream unavailable", 502, undefined, "upstream.unavailable", "request_1");
  globalThis.__recoveryFetch = async () => { throw applicationError; };
  await assert.rejects(submit(), (error) => error === applicationError);
  const internalFailure = new ApiError("internal error", 500);
  globalThis.__recoveryFetch = async () => { throw internalFailure; };
  await assert.rejects(submit(), (error) => error === internalFailure);
});

test("a completed payload remains authoritative when the connection then resets", async () => {
  const result = { assistantMessage: { status: "success", content: "done" } };
  globalThis.__recoveryFetch = async () => response([{ type: "completed", data: result }], true);
  assert.deepEqual(await submit(), result);
});

test("terminal run status plus a lost or timed out result GET remains unconfirmed", async () => {
  for (const failure of [new TypeError("reset"), new DOMException("Timed out", "TimeoutError")]) {
    let resultReads = 0;
    globalThis.__recoveryFetch = async () => { resultReads++; throw failure; };
    assert.deepEqual(await makeRecovery({
      accepted: true,
      queryStatus: async () => "success",
      readResult: (signal) => resumeMessageGenerationStream("token", "run_1", { signal }),
    }), { kind: "unconfirmed" });
    assert.equal(resultReads, 3);
  }
  globalThis.__recoveryFetch = async () => response([{ type: "error", status: 502, message: "model failed" }]);
  await assert.rejects(makeRecovery({ queryStatus: async () => "error", readResult: (signal) => resumeMessageGenerationStream("token", "run_1", { signal }) }), ConversationStreamServerError);
});

test("resume reconnect loop retries only GET requests and accepts the final snapshot", async () => {
  const methods = [];
  const result = { assistantMessage: { status: "success", content: "completed after reconnect" } };
  globalThis.__recoveryFetch = async (_endpoint, options) => {
    methods.push(options.method);
    if (methods.length < 3) throw new TypeError("connection reset");
    return response([{ type: "completed", data: result }]);
  };
  let completed;
  assert.equal(await resumeWithRecovery({
    signal: new AbortController().signal,
    attempt: async () => { completed = await resumeMessageGenerationStream("token", "run_1"); },
    onFailure: () => {},
    isPermanentFailure: () => false,
    pause: async () => {},
  }), "completed");
  assert.deepEqual(methods, ["GET", "GET", "GET"]);
  assert.deepEqual(completed, result);
});

test("moderation and user cancellation keep their distinct behavior", async () => {
  globalThis.__recoveryFetch = async () => response([{ type: "moderation_blocked", eventID: "blocked" }], true);
  await assert.rejects(submit(), (error) => error instanceof ApiError && error.errorCode === "content_moderation.blocked");
  const controller = new AbortController();
  controller.abort();
  globalThis.__recoveryFetch = async () => { throw new DOMException("Aborted", "AbortError"); };
  await assert.rejects(submit({ signal: controller.signal }), { name: "AbortError" });
});
