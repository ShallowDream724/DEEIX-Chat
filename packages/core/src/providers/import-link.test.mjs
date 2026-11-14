import assert from "node:assert/strict";
import test from "node:test";
import { formatProviderImportLink, parseProviderImportLink } from "./import-link.ts";

test("provider links preserve the explicit DeepSeek Responses protocol", () => {
  const url = formatProviderImportLink("https://deeix.example", {
    baseURL: "https://api.deepseek.com/v1", apiKey: "test-key", protocol: "deepseek_responses", name: "DeepSeek",
  });
  const result = parseProviderImportLink(new URL(url).hash);
  assert.equal(result.ok, true);
  assert.equal(result.links[0].protocol, "deepseek_responses");
  for (const protocol of ["deepseek_chat_completions", "deepseek_messages"]) {
    assert.deepEqual(parseProviderImportLink(new URL(url.replace("deepseek_responses", protocol)).hash), { ok: false, error: "invalid_protocol" });
  }
});
