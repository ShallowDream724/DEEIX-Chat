"use client";

import { useId } from "react";
import { useTranslations } from "next-intl";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { emptyImagePricingForm, IMAGE_PRICING_FIELDS, imagePricingFromForm, type ImagePricingField, type ImagePricingForm } from "@/features/admin/model/image-pricing";

const FIELD_LABELS: Record<ImagePricingField | "textOutputUSDPerMTokens", "textInput" | "textOutput" | "imageInput" | "cachedTextInput" | "cachedImageInput" | "imageOutput"> = {
  textInputUSDPerMTokens: "textInput",
  imageInputUSDPerMTokens: "imageInput",
  cachedTextInputUSDPerMTokens: "cachedTextInput",
  cachedImageInputUSDPerMTokens: "cachedImageInput",
  imageOutputUSDPerMTokens: "imageOutput",
  textOutputUSDPerMTokens: "textOutput",
};

const EDITOR_FIELDS = [
  "textInputUSDPerMTokens", "textOutputUSDPerMTokens",
  "imageInputUSDPerMTokens", "imageOutputUSDPerMTokens",
  "cachedTextInputUSDPerMTokens", "cachedImageInputUSDPerMTokens",
] as const;

export function BillingImagePricingEditor({ value, onChange, disabled = false, alignToggle = "start" }: {
  value: ImagePricingForm | null;
  onChange: (value: ImagePricingForm | null) => void;
  disabled?: boolean;
  alignToggle?: "start" | "end";
}) {
  const id = useId();
  const t = useTranslations("adminBilling.imagePricing");
  const invalid = value !== null && IMAGE_PRICING_FIELDS.some((field) => value[field].trim() !== "") && imagePricingFromForm(value) === undefined;
  return (
    <div className="min-w-0 space-y-4 text-left font-sans whitespace-normal">
      <label className={`flex w-fit cursor-pointer items-center gap-2 text-xs text-foreground${alignToggle === "end" ? " ml-auto" : ""}`} htmlFor={id}>
        <Checkbox id={id} checked={value !== null} disabled={disabled} onCheckedChange={(checked) => onChange(checked === true ? emptyImagePricingForm() : null)} />
        {t("enabled")}
      </label>
      {value ? (
        <div className="space-y-2">
          <div className="grid min-w-0 grid-cols-2 gap-5">
            {EDITOR_FIELDS.map((field) => (
              <label key={field} className="flex min-w-0 flex-col gap-1 text-xs text-muted-foreground" htmlFor={`${id}-${field}`}>
                <span>{t(FIELD_LABELS[field])} / 1M</span>
                <Input id={`${id}-${field}`} value={value[field] ?? ""} placeholder={field === "textOutputUSDPerMTokens" ? "0" : undefined} type="number" min="0" step="0.000001" required={field !== "textOutputUSDPerMTokens"} disabled={disabled} onChange={(event) => onChange({ ...value, [field]: event.target.value })} />
              </label>
            ))}
          </div>
          {invalid ? <p className="text-[11px] text-destructive">{t("invalid")}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
