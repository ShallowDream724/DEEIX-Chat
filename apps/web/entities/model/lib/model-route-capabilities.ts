/** Catalog protocols describe eligible routes; a mixed model needs server-side resolution. */
export function usesChat01Protocol(protocols: readonly string[]): boolean {
  return protocols.length > 0 && protocols.every((protocol) => protocol.trim() === "chat01_chat_completions");
}

/** A document capability can be configured whenever an explicit DeepSeek route is present. */
export function hasDeepSeekResponsesProtocol(protocols: readonly string[]): boolean {
  return protocols.some((protocol) => protocol.trim() === "deepseek_responses");
}

/** Keep saved composer preferences intact while omitting unsupported local tool requests. */
export function withoutLocalToolOptions(options: Record<string, unknown>): Record<string, unknown> {
  const result = { ...options };
  for (const key of ["tools", "tool_choice", "parallel_tool_calls", "functions", "function_call"]) {
    delete result[key];
  }
  return result;
}
