import assert from "node:assert/strict";
import test from "node:test";
import { buildUsageLogDisplayRows } from "./subscription-usage-rows.ts";

function ledger(id, time, snapshot, conversationID = "chat-1") {
  return { id, conversationID, createdAt: time, usageDate: time, pricingSnapshotJSON: JSON.stringify(snapshot), inputTokens: 0, outputTokens: 0, reasoningTokens: 0 };
}

test("ASR/TTS standalone ledgers retain separate rows even next to a chat with zero tokens", () => {
  const chat = ledger(1, "2026-11-14T10:00:00Z", { pricing_mode: "token", input_billed_nanousd: 200 });
  const asr = ledger(2, "2026-11-14T10:00:01Z", { pricing_mode: "metered", service_items: [{ service_code: "deepseek.asr", billed_nanousd: 30 }] });
  const tts = ledger(3, "2026-11-14T10:00:02Z", { pricing_mode: "metered", service_items: [{ service_code: "deepseek.tts", billed_nanousd: 40 }] });
  assert.deepEqual(buildUsageLogDisplayRows([chat, asr, tts]), [chat, asr, tts].map((item) => ({ item, baseServiceItems: [] })));
  assert.equal(buildUsageLogDisplayRows([asr]).length, 1);
});

test("legacy base services still merge into the nearest chat, never into closer metered rows", () => {
  const chat = ledger(1, "2026-11-14T10:00:00Z", { pricing_mode: "token", input_billed_nanousd: 200 });
  const service = { service_code: "title", billed_nanousd: 10 };
  const base = ledger(2, "2026-11-14T10:00:02Z", { service_items: [service] });
  const asr = ledger(3, "2026-11-14T10:00:02Z", { pricing_mode: "metered", service_items: [{ service_code: "deepseek.asr", billed_nanousd: 30 }] });
  assert.deepEqual(buildUsageLogDisplayRows([chat, base, asr]), [
    { item: chat, baseServiceItems: [service] },
    { item: asr, baseServiceItems: [] },
  ]);
});
