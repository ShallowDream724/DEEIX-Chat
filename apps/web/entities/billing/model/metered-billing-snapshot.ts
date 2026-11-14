import { isRecord } from "../../../shared/lib/type-guards.ts";
import { formatBillingDisplayAmountFromUSD, type BillingDisplayOptions } from "../lib/billing-display.ts";

export type MeteredBillingUnit = "millisecond" | "character" | "page";
export type MeteredBillingSnapshot = {
  serviceCode: string;
  serviceName: string;
  provider: string;
  unit?: MeteredBillingUnit;
  quantity?: number;
  billableQuantity?: number;
  unitPriceNanousd?: number;
  priceUnitQuantity?: number;
  quantum?: number;
  billedNanousd?: number;
};

export type MeteredBillingLabels = {
  service: string;
  voiceInput: string;
  readAloud: string;
  actual: string;
  billed: string;
  seconds: string;
  hour: string;
  characters: string;
  pages: string;
  roundedAmount: string;
};

export function formatMeteredBillingAmountFromUSD(value: number, display: BillingDisplayOptions): string {
  return formatBillingDisplayAmountFromUSD(value, display, { minimumFractionDigits: 2, maximumFractionDigits: 12 });
}

function nonnegativeInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

/** Reads the recorded charge and quantities without recalculating a historical bill. */
export function readMeteredBillingSnapshot(value: unknown): MeteredBillingSnapshot | undefined {
  if (!isRecord(value) || value.pricing_mode !== "metered") return undefined;
  const unit = value.usage_unit;
  return {
    serviceCode: typeof value.service_code === "string" ? value.service_code.trim() : "",
    serviceName: typeof value.service_name === "string" ? value.service_name.trim() : "",
    provider: typeof value.provider_protocol === "string" ? value.provider_protocol.trim() : "",
    unit: unit === "millisecond" || unit === "character" || unit === "page" ? unit : undefined,
    quantity: nonnegativeInteger(value.usage_quantity),
    billableQuantity: nonnegativeInteger(value.billable_quantity),
    unitPriceNanousd: nonnegativeInteger(value.unit_price_nanousd),
    priceUnitQuantity: nonnegativeInteger(value.price_unit_quantity) || undefined,
    quantum: nonnegativeInteger(value.billing_quantum) || undefined,
    billedNanousd: nonnegativeInteger(value.metered_billed_nanousd) ?? nonnegativeInteger(value.billed_nanousd),
  };
}

export function meteredServiceName(snapshot: MeteredBillingSnapshot, labels: Pick<MeteredBillingLabels, "service" | "voiceInput" | "readAloud">): string {
  if (["deepseek.asr", "doubao-ime.asr", "wetype.asr"].includes(snapshot.serviceCode)) return labels.voiceInput;
  if (snapshot.serviceCode === "deepseek.tts") return labels.readAloud;
  return snapshot.serviceName || snapshot.serviceCode || labels.service;
}

export function meteredServiceDisplayName(snapshot: MeteredBillingSnapshot, labels: MeteredBillingLabels): string {
  const name = meteredServiceName(snapshot, labels);
  return snapshot.provider ? `${name} · ${snapshot.provider}` : name;
}

export function formatMeteredQuantity(quantity: number | undefined, unit: MeteredBillingUnit | undefined, labels: MeteredBillingLabels, locale = "en-US"): string {
  if (quantity === undefined || unit === undefined) return "—";
  const count = (unit === "millisecond" ? quantity / 1000 : quantity).toLocaleString(locale, { maximumFractionDigits: 3 });
  return `${count} ${unit === "millisecond" ? labels.seconds : unit === "character" ? labels.characters : labels.pages}`;
}

export function formatMeteredPriceUnit(snapshot: MeteredBillingSnapshot, labels: MeteredBillingLabels, locale = "en-US"): string {
  if (snapshot.unit === "millisecond" && snapshot.priceUnitQuantity === 3_600_000) return labels.hour;
  return formatMeteredQuantity(snapshot.priceUnitQuantity, snapshot.unit, labels, locale);
}

export function buildMeteredBillingTableRow(snapshot: MeteredBillingSnapshot, labels: MeteredBillingLabels, formatPrice: (usd: number) => string, formatAmount: (usd: number) => string, locale = "en-US") {
  const actual = formatMeteredQuantity(snapshot.quantity, snapshot.unit, labels, locale);
  const billed = formatMeteredQuantity(snapshot.billableQuantity, snapshot.unit, labels, locale);
  return {
    item: meteredServiceName(snapshot, labels),
    tokens: `${labels.actual} ${actual}\n${labels.billed} ${billed}`,
    unitPrice: snapshot.unitPriceNanousd === undefined || snapshot.priceUnitQuantity === undefined || snapshot.unit === undefined
      ? "—" : `${formatPrice(snapshot.unitPriceNanousd / 1_000_000_000)} / ${formatMeteredPriceUnit(snapshot, labels, locale)}`,
    amount: snapshot.billedNanousd === undefined ? "—" : formatAmount(snapshot.billedNanousd / 1_000_000_000),
  };
}
