import assert from "node:assert/strict";
import test from "node:test";
import { capabilityPresetIsRecommended, nativeToolIsRecommended } from "./native-tool-recommendations.ts";

test("DeepSeek recommends its three supported protocols without recommending other providers", () => {
  const supported = ["openai_responses", "openai_chat_completions", "anthropic_messages"];
  for (const protocol of supported) {
    const route = new Set([protocol]);
    assert.equal(nativeToolIsRecommended("DeepSeek", supported, route, "deepseek"), true);
    assert.equal(nativeToolIsRecommended("OpenAI", [protocol], route, "deepseek"), false);
  }
  assert.equal(nativeToolIsRecommended("DeepSeek", supported, new Set(["google_generate_content"]), "deepseek"), false);
  assert.equal(nativeToolIsRecommended("DeepSeek", ["openai_responses"], new Set(["anthropic_messages"]), "deepseek"), false);
});

test("provider aliases and unknown providers retain appropriate recommendations", () => {
  assert.equal(nativeToolIsRecommended("Google", ["google_generate_content"], new Set(["google_generate_content"]), "gemini"), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], new Set(["openai_responses"]), ""), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], new Set(["anthropic_messages"]), ""), false);
});

test("explicit DeepSeek transport recommends its own template and tools regardless of display vendor", () => {
  const routes = new Set(["deepseek_responses"]);
  assert.equal(capabilityPresetIsRecommended(["deepseek_responses"], routes), true);
  assert.equal(capabilityPresetIsRecommended(["openai_responses"], routes), false);
  assert.equal(capabilityPresetIsRecommended(["deepseek_responses"], new Set(["openai_responses"])), false);
  assert.equal(nativeToolIsRecommended("DeepSeek", ["deepseek_responses"], routes, "custom-display-vendor"), true);
  assert.equal(nativeToolIsRecommended("OpenAI", ["openai_responses"], routes, "openai"), false);
});
