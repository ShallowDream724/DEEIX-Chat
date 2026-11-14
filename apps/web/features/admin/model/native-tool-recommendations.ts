function canonicalProvider(value: string): string {
  const provider = value.trim().toLowerCase().replace(/[\s_-]/g, "");
  return provider === "gemini" ? "google" : provider === "claude" ? "anthropic" : provider;
}

export function nativeToolMatchesProvider(toolProvider: string, modelProvider: string): boolean {
  const provider = canonicalProvider(modelProvider);
  return !provider || canonicalProvider(toolProvider) === provider;
}

export function nativeToolIsRecommended(
  toolProvider: string,
  protocols: string[],
  routeProtocols: ReadonlySet<string>,
  modelProvider: string,
): boolean {
  if (routeProtocols.has("deepseek_responses")) {
    return canonicalProvider(toolProvider) === "deepseek" && protocols.includes("deepseek_responses");
  }
  return nativeToolMatchesProvider(toolProvider, modelProvider)
    && (routeProtocols.size === 0 || protocols.some((protocol) => routeProtocols.has(protocol)));
}

/** Templates follow the selected transport identity, independently of display vendor names. */
export function capabilityPresetIsRecommended(protocols: readonly string[], routeProtocols: ReadonlySet<string>): boolean {
  return protocols.some((protocol) => routeProtocols.has(protocol));
}
