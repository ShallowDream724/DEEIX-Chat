import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

const mocks = new Map(Object.entries({
  "@/i18n/resolve-error-message": "export const appendErrorReference=x=>x; export const resolveLocalizedErrorMessage=x=>x; export const resolveLocalizedErrorMessageWithoutReference=x=>x;",
  "@/shared/api/http-client": "export class ApiError extends Error {}",
}));
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (mocks.has(specifier)) return { url: `data:text/javascript,${encodeURIComponent(mocks.get(specifier))}`, shortCircuit: true };
    if (specifier.startsWith("@/")) return { url: new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, shortCircuit: true };
    return nextResolve(specifier, context);
  },
});
const { summarizeUpstreamError } = await import("./chat-runtime.ts");

test("successful SSE headers do not replace a classified timeout with a format error", () => {
  const result = summarizeUpstreamError("模型响应超时（HTTP 200）\n错误：读取响应超时", {
    response: { statusCode: 200, body: 'data: {"message":"unrelated upstream metadata"}\n\ndata: [DONE]' },
  });
  assert.deepEqual(result, { statusCode: 200, reason: "读取响应超时" });
});

test("raw SSE is not exposed as an error reason", () => {
  assert.deepEqual(summarizeUpstreamError('data: {"choices":[{"delta":{"content":"private text"}}]}', {
    response: { statusCode: 200, body: 'data: {"choices":[]}' },
  }), { statusCode: 200, reason: "" });
});

test("non-success status retains structured upstream error details", () => {
  assert.deepEqual(summarizeUpstreamError("模型请求失败", {
    response: { statusCode: 429, body: '{"error":{"message":"rate limit"}}' },
  }), { statusCode: 429, reason: "rate limit" });
});
