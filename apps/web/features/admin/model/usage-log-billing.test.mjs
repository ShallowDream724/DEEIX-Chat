import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) {
    const path = specifier === "@/entities/billing" ? "entities/billing/index" : specifier.slice(2);
    return nextResolve(new URL(`../../../${path}.ts`, import.meta.url).href, context);
  }
  return nextResolve(specifier, context);
} });
const { buildUsageBillingTooltipLines, parseUsagePricingSnapshot } = await import("./usage-log-billing.ts");
const { parseBillingSnapshot } = await import("../../chat/model/billing-snapshot.ts");

const labels = {
  input: "Input", output: "Output", cacheRead: "Cache read", total: "Total", freeModelNoBilling: "Free model",
  perCall: "Per call", perSecond: "Per second", callUnit: "calls", secondUnit: "s", rateNote: "Rate", cacheNote: "Cache",
  billingDisplay: { cacheWrite: "Cache write" },
  image: { model: "Image model", tool: "Image tool" },
  tieredRangeBounded: (from, upTo) => `${from}–${upTo}`, tieredRangeOpen: (from) => `${from}+`,
};
const display = { currency: "USD" };
const service = (service_code, service_name, call_count, call_nanousd_per_call, billed_nanousd) => ({ service_code, service_name, pricing_mode: "call", call_count, call_nanousd_per_call, billed_nanousd });
const deepseekItems = [
  service("native_tool.deepseek.search", "DeepSeek search", 2, 10_000_000, 20_000_000),
  service("native_tool.deepseek.fetch", "DeepSeek fetch", 3, 5_000_000, 15_000_000),
  service("native_tool.deepseek.image_search", "DeepSeek image search", 1, 4_000_000, 4_000_000),
];
function ledger(snapshot = {}, overrides = {}) {
  return { inputTokens: 0, outputTokens: 0, reasoningTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, callCount: 2, durationSeconds: 3, billedNanousd: 77_700_000, isFreeModel: false, pricingSnapshotJSON: JSON.stringify(snapshot), ...overrides };
}
const lines = (snapshot, overrides) => buildUsageBillingTooltipLines(ledger(snapshot, overrides), labels, display);

test("admin token breakdown includes all DeepSeek tool charges and preserves the ledger total", () => {
  const result = lines({ service_items: deepseekItems });
  assert.deepEqual(result.slice(4, 7), [
    { type: "row", left: "DeepSeek search", right: "2 calls * $0.01 / calls = $0.020000" },
    { type: "row", left: "DeepSeek fetch", right: "3 calls * $0.01 / calls = $0.015000" },
    { type: "row", left: "DeepSeek image search", right: "1 calls * $0.00 / calls = $0.004000" },
  ]);
  assert.deepEqual(result.at(-1), { type: "row", left: "Total", right: "$0.077700" });
});

test("service unit prices use the existing two-decimal display without changing ledger precision", () => {
  const snapshot = { service_items: deepseekItems.map((item) => ({ ...item, call_nanousd_per_call: 750_000, billed_nanousd: item.call_count * 750_000 })) };
  const result = lines(snapshot, { billedNanousd: 4_500_000 });
  assert.deepEqual(result.slice(4, 7).map((line) => line.right), [
    "2 calls * $0.00 / calls = $0.001500",
    "3 calls * $0.00 / calls = $0.002250",
    "1 calls * $0.00 / calls = $0.000750",
  ]);
  assert.equal(result.at(-1).right, "$0.004500");
  const cny = buildUsageBillingTooltipLines(ledger({ ...snapshot, pricing_mode: "tiered" }, { billedNanousd: 4_500_000 }), labels, { currency: "CNY", usdToCnyRate: 7.2 });
  assert.deepEqual(cny[0].rows.slice(4).map((row) => row.unitPrice), ["¥0.01 / calls", "¥0.01 / calls", "¥0.01 / calls"]);
  assert.equal(cny[0].totalAmount, "¥0.032400");
});

test("tiered billing appends generic service rows without inventing per-call token prices", () => {
  const result = lines({ pricing_mode: "tiered", service_items: [
    service("mcp.custom", " ", 2, 0, 12_000_000),
    { service_code: "native_tool.custom", service_name: "Token service", pricing_mode: "token", billed_nanousd: 10_000_000 },
  ] });
  assert.deepEqual(result[0].rows.slice(4), [
    { item: "mcp.custom", tokens: "2 calls", unitPrice: "$0.01 / calls", amount: "$0.012000" },
    { item: "Token service", tokens: "—", unitPrice: "—", amount: "$0.010000" },
  ]);
  assert.equal(result[0].totalAmount, "$0.077700");
});

test("call, duration and image model billing retain service charge details", () => {
  for (const snapshot of [{ pricing_mode: "call" }, { pricing_mode: "duration" }, { image_token_cost: { billed_nanousd: 1_000_000 } }]) {
    const result = lines({ ...snapshot, service_items: [deepseekItems[0]] });
    assert.deepEqual(result.at(-3), { type: "row", left: "DeepSeek search", right: "2 calls * $0.01 / calls = $0.020000" });
    assert.equal(result.at(-1).right, "$0.077700");
  }
});

test("an image tool charge appears once when its modality breakdown is available", () => {
  const result = lines({ image_tool_token_cost: { billed_nanousd: 10_000_000 }, service_items: [
    service("native_tool.openai.image_generation", "Image generation", 1, 10_000_000, 10_000_000),
    deepseekItems[0],
  ] });
  assert.equal(result.filter((line) => line.type === "tiered-table" && line.rangeLabel === "Image tool").length, 1);
  assert.equal(result.some((line) => line.left === "Image generation"), false);
  assert.equal(result.some((line) => line.left === "DeepSeek search"), true);
  assert.equal(result.at(-1).right, "$0.077700");
});

test("free models with a paid tool retain their charge; empty legacy snapshots stay unchanged", () => {
  assert.equal(lines({ service_items: [deepseekItems[0]] }, { isFreeModel: true }).at(-1).right, "$0.077700");
  const result = lines({}, { isFreeModel: true, billedNanousd: 0 });
  assert.deepEqual(result, [
    ...["Input", "Output", "Cache read", "Cache write"].map((left) => ({ type: "row", left, right: "0 tokens * $0.00 / 1M = $0.000000" })),
    { type: "divider" }, { type: "row", left: "Total", right: "$0.000000 (Free model)" },
  ]);
  assert.deepEqual(lines({ upstream_usage: { tools: [{ name: "search", count: 4 }] } }), lines({}));
});

test("admin and chat share safe service parsing and legacy count/rate fallback", () => {
  const raw = '{"service_items":[null,[],"search",{"service_code":"generic","service_name":4,"call_count":-2,"call_nanousd_per_call":1e309,"billed_nanousd":6000000},{"service_code":"invalid","billed_nanousd":"9"},{"service_code":"free","billed_nanousd":0}]}';
  const parsed = parseUsagePricingSnapshot(raw);
  assert.deepEqual(parsed.service_items, parseBillingSnapshot(raw).service_items);
  assert.deepEqual(parsed.service_items[0], { service_code: "generic", call_count: -2, billed_nanousd: 6_000_000 });
  const result = buildUsageBillingTooltipLines(ledger({}, { pricingSnapshotJSON: raw }), labels, display);
  assert.deepEqual(result.slice(4, -2), [{ type: "row", left: "generic", right: "1 calls * $0.01 / calls = $0.006000" }]);
  assert.equal(parseUsagePricingSnapshot('{"service_items":{}}').service_items, undefined);
  assert.deepEqual(parseUsagePricingSnapshot("invalid JSON"), {});
});
