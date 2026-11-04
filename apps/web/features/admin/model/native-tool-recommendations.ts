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
  return nativeToolMatchesProvider(toolProvider, modelProvider)
    && (routeProtocols.size === 0 || protocols.some((protocol) => routeProtocols.has(protocol)));
}
