import assert from "node:assert/strict";
import test from "node:test";
import { readMeteredBillingSnapshot, buildMeteredBillingTableRow, meteredServiceDisplayName, formatMeteredBillingAmountFromUSD } from "./metered-billing-snapshot.ts";

const labels = { service: "Service", voiceInput: "Voice input", readAloud: "Read aloud", actual: "Actual", billed: "Billed", seconds: "s", hour: "hour", characters: "billable characters", pages: "pages", roundedAmount: "Formula" };
const formatUSD = (value) => `$${value}`;
const asr = { pricing_mode: "metered", service_code: "deepseek.asr", service_name: "语音输入", provider_protocol: "DeepSeek", usage_unit: "millisecond", usage_quantity: 1501, billable_quantity: 2000, unit_price_nanousd: 54166667, price_unit_quantity: 3600000, billing_quantum: 1000, metered_billed_nanousd: 30093, billed_nanousd: 30093 };

test("ASR shows real milliseconds and rounded billed seconds at the recorded hourly rate", () => {
  const snapshot = readMeteredBillingSnapshot(asr);
  const row = buildMeteredBillingTableRow(snapshot, labels, formatUSD, formatUSD);
  assert.equal(row.tokens, "Actual 1.501 s\nBilled 2 s");
  assert.equal(row.unitPrice, "$0.054166667 / hour");
  assert.equal(row.amount, "$0.000030093");
  assert.equal(meteredServiceDisplayName(snapshot, labels), "Voice input · DeepSeek");
});

test("metered prices preserve nanoUSD precision and use the existing display exchange rate", () => {
  assert.equal(formatMeteredBillingAmountFromUSD(0.054166667, { currency: "USD" }), "$0.054166667");
  assert.equal(formatMeteredBillingAmountFromUSD(0.054166667, { currency: "CNY", usdToCnyRate: 7.2 }), "¥0.3900000024");
  assert.equal(formatMeteredBillingAmountFromUSD(1e-9, { currency: "USD" }), "$0.000000001");
});

test("TTS uses upstream weighted characters and preserves the recorded charge and zero prices", () => {
  const snapshot = readMeteredBillingSnapshot({ ...asr, service_code: "deepseek.tts", service_name: "朗读回复", usage_unit: "character", usage_quantity: 7, billable_quantity: 7, price_unit_quantity: 10000, billing_quantum: 1, unit_price_nanousd: 208333333, metered_billed_nanousd: 145834 });
  const row = buildMeteredBillingTableRow(snapshot, labels, formatUSD, formatUSD);
  assert.equal(row.item, "Read aloud");
  assert.equal(row.tokens, "Actual 7 billable characters\nBilled 7 billable characters");
  assert.equal(row.unitPrice, "$0.208333333 / 10,000 billable characters");
  assert.equal(row.amount, "$0.000145834");
  const free = readMeteredBillingSnapshot({ ...asr, unit_price_nanousd: 0, metered_billed_nanousd: 0 });
  assert.equal(buildMeteredBillingTableRow(free, labels, formatUSD, formatUSD).amount, "$0");
});

test("legacy pricing stays outside metered parsing and malformed fields stay unknown", () => {
  for (const value of [null, [], {}, { pricing_mode: "duration", duration_seconds: 12 }]) assert.equal(readMeteredBillingSnapshot(value), undefined);
  const snapshot = readMeteredBillingSnapshot({ ...asr, usage_quantity: "1501", billable_quantity: -1, unit_price_nanousd: NaN, price_unit_quantity: 0, metered_billed_nanousd: "30093", billed_nanousd: undefined });
  const row = buildMeteredBillingTableRow(snapshot, labels, formatUSD, formatUSD);
  assert.equal(row.tokens, "Actual —\nBilled —");
  assert.equal(row.unitPrice, "—");
  assert.equal(row.amount, "—");
});
