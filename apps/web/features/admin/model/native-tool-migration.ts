import { isRecord } from "../../../shared/lib/type-guards.ts";

const DEEPSEEK_SEARCH_KEY = "deepseek.tool_search";
const LEGACY_DEEPSEEK_FETCH_KEY = "deepseek.tool_fetch";

export function canonicalNativeToolKey(key: string): string {
  return key;
}

function configuredToolKey(tool: Record<string, unknown>): string {
  const key = typeof tool.key === "string" ? tool.key.trim() : "";
  return key || (typeof tool.toolKey === "string" ? tool.toolKey.trim() : "");
}

/** Preserve saved model-specific wire contracts; the server resolves the actual upstream model. */
export function mergeLegacyDeepSeekNativeTools(tools: Record<string, unknown>[]): Record<string, unknown>[] {
  return tools.map((tool) => {
    const key = configuredToolKey(tool);
    if (key !== DEEPSEEK_SEARCH_KEY && key !== LEGACY_DEEPSEEK_FETCH_KEY) return tool;
    const oldType = typeof tool.type === "string" ? tool.type : isRecord(tool.payload) ? tool.payload.type : "";
    const legacy = oldType === "web_search" || oldType === "web_fetch" || oldType === "tool_fetch";
    const type = key === DEEPSEEK_SEARCH_KEY
      ? (legacy ? "web_search" : "deepseek_web_search")
      : (legacy ? "web_fetch" : "deepseek_read_url");
    const protocols = !tool.protocol && !Array.isArray(tool.protocols)
      ? (legacy ? ["deepseek_responses", "openai_responses"] : ["deepseek_responses", "openai_responses", "openai_chat_completions", "anthropic_messages"])
      : tool.protocols;
    return { ...tool, key, provider: tool.provider || "DeepSeek", ...(protocols ? { protocols } : {}), type, payload: { ...(isRecord(tool.payload) ? tool.payload : {}), type } };
  });
}
