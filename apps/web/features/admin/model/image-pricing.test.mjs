import assert from "node:assert/strict";
import test from "node:test";
import { emptyImagePricingForm, imagePricingFromForm, imagePricingToForm, parseImagePricing, stringifyImagePricing } from "../../../entities/billing/model/image-pricing.ts";

const pricing = {
  textInputUSDPerMTokens: 5,
  imageInputUSDPerMTokens: 8,
  cachedTextInputUSDPerMTokens: 1.25,
  cachedImageInputUSDPerMTokens: 2,
  imageOutputUSDPerMTokens: 30,
};

test("unconfigured image prices preserve legacy billing and explicit zero stays configured", () => {
  for (const raw of [undefined, null, "", "{}", {}]) assert.equal(parseImagePricing(raw), null);
  const zero = Object.fromEntries(Object.keys(pricing).map((field) => [field, 0]));
  assert.deepEqual(parseImagePricing(zero), zero);
  assert.equal(stringifyImagePricing(null), "");
});

test("all five image rates are required and invalid pricing cannot become a zero-price policy", () => {
  for (const invalid of ["{", [], "null", { ...pricing, imageInputUSDPerMTokens: -1 }, { ...pricing, imageOutputUSDPerMTokens: Infinity }, { ...pricing, textInputUSDPerMTokens: "5" }]) {
    assert.equal(parseImagePricing(invalid), undefined);
  }
  const { cachedImageInputUSDPerMTokens, ...incomplete } = pricing;
  assert.equal(parseImagePricing(incomplete), undefined);
  assert.equal(imagePricingFromForm(emptyImagePricingForm()), undefined);
  const draft = imagePricingToForm(pricing);
  assert.equal(imagePricingFromForm({ ...draft, imageInputUSDPerMTokens: "" }), undefined);
});

test("editing and JSON serialization preserve modality rates and optional text output", () => {
  for (const original of [pricing, { ...pricing, textOutputUSDPerMTokens: 10 }]) {
    const draft = imagePricingToForm(parseImagePricing(JSON.stringify(original)));
    assert.deepEqual(imagePricingFromForm(draft), original);
    assert.deepEqual(JSON.parse(stringifyImagePricing(draft)), original);
  }
  assert.deepEqual(imagePricingFromForm({ ...imagePricingToForm(pricing), textOutputUSDPerMTokens: "" }), pricing);
  assert.equal(imagePricingFromForm({ ...imagePricingToForm(pricing), textOutputUSDPerMTokens: "-1" }), undefined);
});
