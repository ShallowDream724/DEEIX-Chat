import type { BillingUsageLedgerDTO } from "@/shared/api/billing-types";
import { isRecord, parseJSON } from "../../../shared/lib/type-guards.ts";

type ServiceItem = Record<string, unknown>;
export type UsageLogDisplayRow = { item: BillingUsageLedgerDTO; baseServiceItems: ServiceItem[] };

function snapshot(item: BillingUsageLedgerDTO): Record<string, unknown> {
  const parsed = parseJSON(item.pricingSnapshotJSON);
  return isRecord(parsed) ? parsed : {};
}

function serviceItems(value: Record<string, unknown>): ServiceItem[] {
  return Array.isArray(value.service_items) ? value.service_items.filter(isRecord) : [];
}

function isBaseServiceLedger(item: BillingUsageLedgerDTO): boolean {
  const value = snapshot(item);
  // Metered services have their own operation identity and must retain their own row.
  if (value.pricing_mode === "metered") return false;
  const modelAmount = ["input_billed_nanousd", "cache_read_billed_nanousd", "cache_write_billed_nanousd", "output_billed_nanousd", "call_billed_nanousd", "duration_billed_nanousd"]
    .reduce((sum, key) => sum + (typeof value[key] === "number" && Number.isFinite(value[key]) ? value[key] : 0), 0);
  return serviceItems(value).length > 0 && modelAmount <= 0;
}

export function buildUsageLogDisplayRows(items: BillingUsageLedgerDTO[]): UsageLogDisplayRow[] {
  const rows = items.filter((item) => !isBaseServiceLedger(item)).map((item): UsageLogDisplayRow => ({ item, baseServiceItems: [] }));
  for (const serviceLedger of items.filter(isBaseServiceLedger)) {
    const services = serviceItems(snapshot(serviceLedger));
    const serviceTime = new Date(serviceLedger.createdAt || serviceLedger.usageDate).getTime();
    let matchedIndex = -1;
    let matchedDistance = Number.POSITIVE_INFINITY;
    for (let index = 0; index < rows.length; index += 1) {
      const row = rows[index];
      if (snapshot(row.item).pricing_mode === "metered" || row.item.conversationID !== serviceLedger.conversationID) continue;
      const rowTime = new Date(row.item.createdAt || row.item.usageDate).getTime();
      const distance = Math.abs((Number.isFinite(serviceTime) ? serviceTime : 0) - (Number.isFinite(rowTime) ? rowTime : 0));
      if (distance < matchedDistance) {
        matchedDistance = distance;
        matchedIndex = index;
      }
    }
    if (matchedIndex >= 0) rows[matchedIndex].baseServiceItems.push(...services);
  }
  return rows;
}
