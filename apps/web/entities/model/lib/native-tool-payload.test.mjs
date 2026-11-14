import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) return nextResolve(new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, context);
  return nextResolve(specifier, context);
} });
const { nativeToolDefinitionVariantsFromConfig } = await import("./native-tool-payload.ts");
const { resolveModelOptionPolicyProtocol } = await import("./model-option-policy.ts");

test("DeepSeek tool variants preserve their transport while sharing Responses parameter rules", () => {
  const catalog = ["openai_responses", "deepseek_responses"].map((protocol) => ({
    protocol, provider: "DeepSeek", toolKey: "deepseek.tool_search", type: "deepseek_web_search",
    label: "DeepSeek Search", description: "", payload: { type: "deepseek_web_search" },
  }));
  const config = { key: "deepseek.tool_search", protocol: "", protocols: ["deepseek_responses"],
    type: "deepseek_web_search", label: "DeepSeek Search", enabled: true, defaultEnabled: true,
    payload: { type: "deepseek_web_search" } };
  assert.equal(resolveModelOptionPolicyProtocol("deepseek_responses"), "openai_responses");
  assert.deepEqual(nativeToolDefinitionVariantsFromConfig(config, catalog, ["deepseek_responses"]).map((tool) => tool.protocol), ["deepseek_responses"]);
  assert.deepEqual(nativeToolDefinitionVariantsFromConfig({ ...config, protocols: ["openai_responses"] }, catalog, ["openai_responses"]).map((tool) => tool.protocol), ["openai_responses"]);
  assert.deepEqual(nativeToolDefinitionVariantsFromConfig({ ...config, protocols: [] }, catalog, ["deepseek_responses"]).map((tool) => tool.protocol), ["deepseek_responses"]);
});
