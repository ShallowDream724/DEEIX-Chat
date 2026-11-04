import assert from "node:assert/strict";
import test from "node:test";
import { mergeLegacyDeepSeekNativeTools } from "./native-tool-migration.ts";

const search = {
  key: "deepseek.tool_search",
  protocols: ["openai_responses"],
  enabled: true,
  defaultEnabled: false,
  payload: { type: "web_search", allowed_domains: ["example.org"] },
};
const fetch = {
  toolKey: "deepseek.tool_fetch",
  protocol: "openai_chat_completions",
  enabled: true,
  defaultEnabled: true,
  payload: { type: "web_fetch", fetch_only: true },
};

test("enabled legacy fetch survives a disabled search without losing search parameters", () => {
  for (const tools of [[{ ...search, enabled: false }, fetch], [fetch, { ...search, enabled: false }]]) {
    const result = mergeLegacyDeepSeekNativeTools(tools);
    assert.equal(result.length, 1);
    const [merged] = result;
    assert.equal(merged.key, search.key);
    assert.deepEqual(merged.protocols, ["openai_responses"]);
    assert.deepEqual(merged.payload, search.payload);
    assert.equal(merged.enabled, true);
    assert.equal(merged.defaultEnabled, true);
  }
});

test("only enabled legacy declarations contribute to the merged default", () => {
  const cases = [
    [search, fetch, true, true],
    [search, { ...fetch, enabled: false }, true, false],
    [{ ...search, enabled: false, defaultEnabled: true }, { ...fetch, defaultEnabled: false }, true, false],
    [{ ...search, enabled: false }, { ...fetch, enabled: false }, false, false],
  ];
  for (const [searchTool, fetchTool, enabled, defaultEnabled] of cases) {
    const [merged] = mergeLegacyDeepSeekNativeTools([searchTool, fetchTool]);
    assert.equal(merged.enabled, enabled);
    assert.equal(merged.defaultEnabled, defaultEnabled);
  }
});

test("fetch-only settings migrate to one canonical search without importing fetch parameters", () => {
  const [merged, other] = mergeLegacyDeepSeekNativeTools([fetch, { key: "openai.web_search" }]);
  assert.equal(merged.key, "deepseek.tool_search");
  assert.deepEqual(merged.payload, { type: "web_search" });
  assert.equal(merged.enabled, true);
  assert.equal(merged.defaultEnabled, true);
  assert.deepEqual(other, { key: "openai.web_search" });
});
