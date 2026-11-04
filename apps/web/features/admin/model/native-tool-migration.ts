import { isRecord } from "../../../shared/lib/type-guards.ts";

const DEEPSEEK_SEARCH_KEY = "deepseek.tool_search";
const LEGACY_DEEPSEEK_FETCH_KEY = "deepseek.tool_fetch";

export function canonicalNativeToolKey(key: string): string {
  return key === LEGACY_DEEPSEEK_FETCH_KEY ? DEEPSEEK_SEARCH_KEY : key;
}

function configuredToolKey(tool: Record<string, unknown>): string {
  const key = typeof tool.key === "string" ? tool.key.trim() : "";
  return key || (typeof tool.toolKey === "string" ? tool.toolKey.trim() : "");
}

/** Fetch now runs inside DeepSeek's Responses search tool. Its old settings
 * contribute selection state; only the search declaration supplies parameters. */
export function mergeLegacyDeepSeekNativeTools(tools: Record<string, unknown>[]): Record<string, unknown>[] {
  if (!tools.some((tool) => configuredToolKey(tool) === LEGACY_DEEPSEEK_FETCH_KEY)) {
    return tools;
  }

  const isDeepSeekWebTool = (tool: Record<string, unknown>) =>
    canonicalNativeToolKey(configuredToolKey(tool)) === DEEPSEEK_SEARCH_KEY;
  const firstIndex = tools.findIndex(isDeepSeekWebTool);
  const searchTools = tools.filter((tool) => configuredToolKey(tool) === DEEPSEEK_SEARCH_KEY);
  const search = searchTools.find((tool) => tool.enabled !== false) ?? searchTools[0];
  const enabledTools = tools.filter((tool) => isDeepSeekWebTool(tool) && tool.enabled !== false);
  const canonicalSearch = {
    id: search?.id,
    key: DEEPSEEK_SEARCH_KEY,
    provider: search?.provider || "DeepSeek",
    label: search?.label || "DeepSeek Web Search",
    description: search?.description,
    protocols: ["openai_responses"],
    type: "web_search",
    payload: { ...(isRecord(search?.payload) ? search.payload : {}), type: "web_search" },
    enabled: enabledTools.length > 0,
    defaultEnabled: enabledTools.some((tool) => tool.defaultEnabled === true),
  };
  return tools.flatMap((tool, index) => index === firstIndex ? [canonicalSearch] : isDeepSeekWebTool(tool) ? [] : [tool]);
}
