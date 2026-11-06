import { isRecord } from "../../../shared/lib/type-guards.ts";

const IMAGE_FIELDS = [
  ["textInput", "textInputTokens", "textInputUSDPerMTokens", "text_input_billed_nanousd"],
  ["textOutput", "textOutputTokens", "textOutputUSDPerMTokens", "text_output_billed_nanousd"],
  ["imageInput", "imageInputTokens", "imageInputUSDPerMTokens", "image_input_billed_nanousd"],
  ["imageOutput", "imageOutputTokens", "imageOutputUSDPerMTokens", "image_output_billed_nanousd"],
  ["cachedTextInput", "cachedTextInputTokens", "cachedTextInputUSDPerMTokens", "cached_text_input_billed_nanousd"],
  ["cachedImageInput", "cachedImageInputTokens", "cachedImageInputUSDPerMTokens", "cached_image_input_billed_nanousd"],
] as const;

export type ImageBillingLabel = (typeof IMAGE_FIELDS)[number][0];
export type ImageBillingLabels = Record<ImageBillingLabel | "model" | "tool", string>;
export type ImageBillingRow = { label: ImageBillingLabel; tokens?: number; unitUSDPerM?: number; billedNanousd?: number };
export type ImageBillingBreakdown = { rows: ImageBillingRow[]; billedNanousd?: number; rateMultiplier: number };
export type ImageBillingSnapshot = { model?: ImageBillingBreakdown; tool?: ImageBillingBreakdown };

function record(value: unknown): Record<string, unknown> | undefined {
  return isRecord(value) ? value : undefined;
}

function nonnegative(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

function pricingRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== "string") return record(value);
  try { return record(JSON.parse(value)); } catch { return undefined; }
}

function breakdown(usageValue: unknown, costValue: unknown, pricingValue: unknown, rateMultiplier: number): ImageBillingBreakdown | undefined {
  const cost = record(costValue);
  if (!cost) return undefined;
  const usage = record(usageValue);
  const pricing = pricingRecord(pricingValue);
  const completePricing = IMAGE_FIELDS.every(([label, , priceKey]) => label === "textOutput" || nonnegative(pricing?.[priceKey]) !== undefined);
  return {
    rateMultiplier,
    billedNanousd: nonnegative(cost.billed_nanousd),
    rows: IMAGE_FIELDS.map(([label, usageKey, priceKey, billedKey]) => {
      let tokens = nonnegative(usage?.[usageKey]);
      // Input counters include the cached subset; display each token once.
      if (label === "textInput" || label === "imageInput") {
        const cached = nonnegative(usage?.[label === "textInput" ? "cachedTextInputTokens" : "cachedImageInputTokens"]);
        tokens = tokens !== undefined && cached !== undefined && cached <= tokens ? tokens - cached : undefined;
      }
      const basePrice = nonnegative(pricing?.[priceKey]) ?? (label === "textOutput" && completePricing ? 0 : undefined);
      // Match the ledger's integer nanodollar rate rounding; never recalculate recorded charges.
      const unitUSDPerM = basePrice === undefined ? undefined : Math.round(Math.round(basePrice * 1_000_000_000) * rateMultiplier) / 1_000_000_000;
      return { label, tokens, unitUSDPerM, billedNanousd: nonnegative(cost[billedKey]) };
    }),
  };
}

/** Reads recorded amounts only. Missing costs/rates stay missing rather than becoming a fabricated price. */
export function readImageBillingSnapshot(value: unknown): ImageBillingSnapshot {
  const snapshot = record(value);
  if (!snapshot) return {};
  const auth = record(snapshot.native_tool_authorization);
  const imageToolPrice = record(auth?.["openai.image_generation"]);
  return {
    model: breakdown(snapshot.image_usage, snapshot.image_token_cost, snapshot.image_pricing_json, nonnegative(snapshot.rate_multiplier) || 1),
    tool: breakdown(snapshot.image_tool_usage, snapshot.image_tool_token_cost, imageToolPrice?.ImagePricingJSON, 1),
  };
}
