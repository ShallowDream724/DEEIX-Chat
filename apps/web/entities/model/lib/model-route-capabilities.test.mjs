import assert from "node:assert/strict";
import test from "node:test";
import { hasDeepSeekResponsesProtocol, usesChat01Protocol, withoutLocalToolOptions } from "./model-route-capabilities.ts";

test("only a dedicated Chat01 route disables local tools before server routing", () => {
  assert.equal(usesChat01Protocol([]), false);
  assert.equal(usesChat01Protocol(["chat01_chat_completions"]), true);
  assert.equal(usesChat01Protocol(["chat01_chat_completions", "openai_responses"]), false);
  assert.equal(usesChat01Protocol(["openai_chat_completions"]), false);
  assert.equal(usesChat01Protocol(["deepseek_responses"]), false);
});

test("document configuration follows explicit DeepSeek routes without assuming a vendor", () => {
  assert.equal(hasDeepSeekResponsesProtocol(["deepseek_responses"]), true);
  assert.equal(hasDeepSeekResponsesProtocol(["openai_responses", "deepseek_responses"]), true);
  assert.equal(hasDeepSeekResponsesProtocol(["openai_responses"]), false);
  assert.equal(hasDeepSeekResponsesProtocol(["chat01_chat_completions"]), false);
  assert.equal(hasDeepSeekResponsesProtocol([]), false);
});

test("effective tool options are omitted without changing saved preferences", () => {
  const saved = { tools: [{ type: "function" }], tool_choice: "auto", parallel_tool_calls: true,
    functions: [{ name: "lookup" }], function_call: "auto", temperature: 0.5 };
  assert.deepEqual(withoutLocalToolOptions(saved), { temperature: 0.5 });
  assert.equal(saved.tools.length, 1);
  assert.equal(saved.tool_choice, "auto");
});
