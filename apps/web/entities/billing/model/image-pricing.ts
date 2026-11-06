import { isRecord } from "../../../shared/lib/type-guards.ts";

export const IMAGE_PRICING_FIELDS = [
  "textInputUSDPerMTokens",
  "imageInputUSDPerMTokens",
  "cachedTextInputUSDPerMTokens",
  "cachedImageInputUSDPerMTokens",
  "imageOutputUSDPerMTokens",
] as const;

export type ImagePricingField = (typeof IMAGE_PRICING_FIELDS)[number];
export type ImagePricing = Record<ImagePricingField, number> & { textOutputUSDPerMTokens?: number };

export const IMAGE_PRICING_DISPLAY_FIELDS = [
  ["textInputUSDPerMTokens", "textInput"],
  ["textOutputUSDPerMTokens", "textOutput"],
  ["imageInputUSDPerMTokens", "imageInput"],
  ["imageOutputUSDPerMTokens", "imageOutput"],
  ["cachedTextInputUSDPerMTokens", "cachedTextInput"],
  ["cachedImageInputUSDPerMTokens", "cachedImageInput"],
] as const;

/** Empty configuration keeps legacy pricing; malformed configuration is not a zero price. */
export function parseImagePricing(raw: unknown): ImagePricing | null | undefined {
  if (raw === undefined || raw === null || raw === "") return null;
  let value: unknown = raw;
  if (typeof raw === "string") {
    try { value = JSON.parse(raw); } catch { return undefined; }
  }
  if (!isRecord(value)) return undefined;
  if (Object.keys(value).length === 0) return null;
  const pricing: ImagePricing = {
    textInputUSDPerMTokens: 0,
    imageInputUSDPerMTokens: 0,
    cachedTextInputUSDPerMTokens: 0,
    cachedImageInputUSDPerMTokens: 0,
    imageOutputUSDPerMTokens: 0,
  };
  for (const field of IMAGE_PRICING_FIELDS) {
    const price = value[field];
    if (typeof price !== "number" || !Number.isFinite(price) || price < 0) return undefined;
    pricing[field] = price;
  }
  const textOutput = value.textOutputUSDPerMTokens;
  if (textOutput !== undefined) {
    if (typeof textOutput !== "number" || !Number.isFinite(textOutput) || textOutput < 0) return undefined;
    pricing.textOutputUSDPerMTokens = textOutput;
  }
  return pricing;
}

export type ImagePricingForm = Record<ImagePricingField, string> & { textOutputUSDPerMTokens?: string };

export function imagePricingToForm(pricing: ImagePricing | null | undefined): ImagePricingForm | null {
  if (!pricing) return null;
  const form = emptyImagePricingForm();
  for (const field of IMAGE_PRICING_FIELDS) form[field] = String(pricing[field]);
  if (pricing.textOutputUSDPerMTokens !== undefined) form.textOutputUSDPerMTokens = String(pricing.textOutputUSDPerMTokens);
  return form;
}

export function emptyImagePricingForm(): ImagePricingForm {
  return {
    textInputUSDPerMTokens: "",
    imageInputUSDPerMTokens: "",
    cachedTextInputUSDPerMTokens: "",
    cachedImageInputUSDPerMTokens: "",
    imageOutputUSDPerMTokens: "",
  };
}

export function imagePricingFromForm(form: ImagePricingForm | null): ImagePricing | null | undefined {
  if (!form) return null;
  const pricing: Record<string, number> = {};
  for (const field of IMAGE_PRICING_FIELDS) {
    const raw = form[field].trim();
    const price = Number(raw);
    if (!raw || !Number.isFinite(price) || price < 0) return undefined;
    pricing[field] = price;
  }
  const textOutput = form.textOutputUSDPerMTokens?.trim();
  if (textOutput) {
    const price = Number(textOutput);
    if (!Number.isFinite(price) || price < 0) return undefined;
    pricing.textOutputUSDPerMTokens = price;
  }
  return parseImagePricing(pricing);
}

export function stringifyImagePricing(form: ImagePricingForm | null): string {
  const pricing = imagePricingFromForm(form);
  return pricing ? JSON.stringify(pricing) : "";
}
