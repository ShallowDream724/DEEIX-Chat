import assert from "node:assert/strict";
import test from "node:test";
import { readImageBillingSnapshot } from "./image-billing-snapshot.ts";

const usage = { textInputTokens: 100, imageInputTokens: 200, cachedTextInputTokens: 25, cachedImageInputTokens: 50, imageOutputTokens: 300, textOutputTokens: 10 };
const prices = { textInputUSDPerMTokens: 5, imageInputUSDPerMTokens: 8, cachedTextInputUSDPerMTokens: 1.25, cachedImageInputUSDPerMTokens: 2, imageOutputUSDPerMTokens: 30, textOutputUSDPerMTokens: 0 };
const cost = { text_input_billed_nanousd: 123, image_input_billed_nanousd: 456, cached_text_input_billed_nanousd: 78, cached_image_input_billed_nanousd: 90, image_output_billed_nanousd: 1011, text_output_billed_nanousd: 0, billed_nanousd: 1758 };

test("modality rows subtract cached subsets and show recorded money without calculating it", () => {
  const { model } = readImageBillingSnapshot({ image_usage: usage, image_token_cost: cost, image_pricing_json: JSON.stringify(prices), rate_multiplier: 1.5 });
  assert.equal(model.rateMultiplier, 1.5);
  assert.equal(model.billedNanousd, cost.billed_nanousd);
  const rows = Object.fromEntries(model.rows.map((row) => [row.label, row]));
  assert.equal(rows.textInput.tokens, 75);
  assert.equal(rows.imageInput.tokens, 150);
  assert.equal(rows.cachedTextInput.tokens, 25);
  assert.equal(rows.cachedImageInput.tokens, 50);
  assert.equal(rows.textInput.billedNanousd, 123);
  assert.equal(rows.textInput.unitUSDPerM, 7.5);
  assert.equal(rows.textOutput.billedNanousd, 0);
  const { textOutputUSDPerMTokens, ...requiredPrices } = prices;
  assert.equal(readImageBillingSnapshot({ image_usage: usage, image_token_cost: cost, image_pricing_json: requiredPrices }).model.rows.find((row) => row.label === "textOutput").unitUSDPerM, 0);
});

test("native image tools use frozen authorization prices and distinct recorded amounts", () => {
  const { tool, model } = readImageBillingSnapshot({ image_tool_usage: usage, image_tool_token_cost: cost, native_tool_authorization: { "openai.image_generation": { ImagePricingJSON: JSON.stringify(prices), ImageModel: "gpt-image-2.6-flare" } } });
  assert.equal(model, undefined);
  assert.equal(tool.rateMultiplier, 1);
  assert.equal(tool.rows.find((row) => row.label === "imageOutput").unitUSDPerM, 30);
  assert.equal(tool.rows.find((row) => row.label === "imageOutput").billedNanousd, 1011);
});

test("missing or malformed usage, prices and amounts stay unknown, never fabricated zero prices", () => {
  assert.deepEqual(readImageBillingSnapshot({ image_usage: usage, image_pricing_json: JSON.stringify(prices) }), { model: undefined, tool: undefined });
  const { tool } = readImageBillingSnapshot({ image_tool_token_cost: { ...cost, image_output_billed_nanousd: "1011" }, image_tool_usage: { ...usage, cachedTextInputTokens: 101 } });
  assert.equal(tool.rows.find((row) => row.label === "textInput").tokens, undefined);
  assert.equal(tool.rows.find((row) => row.label === "imageOutput").unitUSDPerM, undefined);
  assert.equal(tool.rows.find((row) => row.label === "imageOutput").billedNanousd, undefined);
  assert.equal(tool.billedNanousd, 1758);
});
