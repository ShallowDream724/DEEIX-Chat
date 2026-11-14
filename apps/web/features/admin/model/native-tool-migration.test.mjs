import assert from "node:assert/strict";
import test from "node:test";
import { canonicalNativeToolKey, mergeLegacyDeepSeekNativeTools } from "./native-tool-migration.ts";

test("legacy search and read selections keep independent enabled/default state", () => {
  const input = [
    { key: "deepseek.tool_search", enabled: false, defaultEnabled: false, payload: { type: "web_search" } },
    { toolKey: "deepseek.tool_fetch", enabled: true, defaultEnabled: true, payload: { type: "web_fetch" } },
    { key: "openai.web_search", payload: { type: "web_search" } },
  ];
  const result = mergeLegacyDeepSeekNativeTools(input);
  assert.equal(result.length, 3);
  assert.equal(result[0].key, "deepseek.tool_search");
  assert.equal(result[0].enabled, false);
  assert.equal(result[0].defaultEnabled, false);
  assert.equal(result[0].payload.type, "deepseek_web_search");
  assert.equal(result[1].key, "deepseek.tool_fetch");
  assert.equal(result[1].enabled, true);
  assert.equal(result[1].defaultEnabled, true);
  assert.equal(result[1].payload.type, "deepseek_read_url");
  assert.deepEqual(result[1].protocols, ["openai_responses", "openai_chat_completions", "anthropic_messages"]);
  assert.deepEqual(result[2], input[2]);
  assert.equal(canonicalNativeToolKey("deepseek.tool_fetch"), "deepseek.tool_fetch");
});

test("a read-only legacy capability never enables search", () => {
  const [read] = mergeLegacyDeepSeekNativeTools([{ key: "deepseek.tool_fetch", enabled: true, defaultEnabled: false }]);
  assert.equal(read.key, "deepseek.tool_fetch");
  assert.equal(read.type, "deepseek_read_url");
  assert.equal(read.defaultEnabled, false);
});
