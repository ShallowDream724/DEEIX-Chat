import { isRecord, readFiniteNumber, readString } from "../../../shared/lib/type-guards.ts";

export type BillingServiceItemSnapshot = {
  service_code?: string;
  service_name?: string;
  pricing_mode?: string;
  call_count?: number;
  call_nanousd_per_call?: number;
  billed_nanousd?: number;
};

export type BillingServiceItemEntry = {
  tokenPriced: boolean;
  label: string;
  callCount: number;
  rateNanousd: number;
  billedNanousd: number;
};

/** Keep the ledger's typed service fields; tool events do not determine charges. */
export function readBillingServiceItems(snapshot: Record<string, unknown>): BillingServiceItemSnapshot[] | undefined {
  if (!Array.isArray(snapshot.service_items)) return undefined;
  return snapshot.service_items.filter(isRecord).map((record) => {
    const item: BillingServiceItemSnapshot = {};
    for (const key of ["service_code", "service_name", "pricing_mode"] as const) {
      const value = readString(record, key);
      if (value !== undefined) item[key] = value;
    }
    for (const key of ["call_count", "call_nanousd_per_call", "billed_nanousd"] as const) {
      const value = readFiniteNumber(record, key);
      if (value !== undefined) item[key] = value;
    }
    return item;
  });
}

/** Match the chat cost breakdown, including legacy per-call price fallbacks. */
export function buildBillingServiceItemEntries(items: BillingServiceItemSnapshot[] | undefined, hasImageToolBreakdown = false): BillingServiceItemEntry[] {
  const entries: BillingServiceItemEntry[] = [];
  for (const item of items ?? []) {
    if (hasImageToolBreakdown && item.service_code === "native_tool.openai.image_generation") continue;
    const billed = Number.isFinite(item.billed_nanousd) ? item.billed_nanousd ?? 0 : 0;
    if (billed <= 0) continue;
    const label = item.service_name?.trim() || item.service_code?.trim();
    if (!label) continue;
    const rawCount = item.call_count ?? 0;
    const callCount = Number.isFinite(rawCount) && rawCount > 0 ? rawCount : 1;
    const tokenPriced = item.pricing_mode === "token";
    const rawRate = item.call_nanousd_per_call ?? 0;
    const rate = tokenPriced ? 0 : Number.isFinite(rawRate) && rawRate > 0 ? rawRate : Math.round(billed / callCount);
    entries.push({ label, callCount, rateNanousd: rate, billedNanousd: billed, tokenPriced });
  }
  return entries;
}
