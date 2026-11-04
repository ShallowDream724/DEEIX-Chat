import assert from "node:assert/strict";
import test from "node:test";
import { nativeToolIsRecommended } from "./native-tool-recommendations.ts";

test("DeepSeek only recommends its Responses tools, not OpenAI tools sharing the protocol", () => {
  const responses = new Set(["openai_responses"]);
  const chat = new Set(["openai_chat_completions"]);
  assert.equal(nativeToolIsRecommended("DeepSeek", ["openai_responses"], responses, "deepseek"), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], responses, "deepseek"), false);
  assert.equal(nativeToolIsRecommended("DeepSeek", ["openai_responses"], chat, "deepseek"), false);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_chat_completions"], chat, "deepseek"), false);
});

test("provider aliases and unknown providers retain appropriate recommendations", () => {
  assert.equal(nativeToolIsRecommended("Google", ["google_generate_content"], new Set(["google_generate_content"]), "gemini"), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], new Set(["openai_responses"]), ""), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], new Set(["anthropic_messages"]), ""), false);
});
