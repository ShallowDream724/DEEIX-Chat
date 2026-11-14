import assert from "node:assert/strict";
import test from "node:test";
import { buildSpeechPreferencesUpdate } from "./speech-preferences.ts";

test("changing one speech preference preserves provider choices and other providers", () => {
  const preferences = { deepseek: { voice: "tide" }, mimo: { voice: "冰糖", style: "自然清晰" } };
  assert.deepEqual(JSON.parse(buildSpeechPreferencesUpdate(preferences, "mimo", { speed: "fast" })), {
    deepseek: { voice: "tide" }, mimo: { voice: "冰糖", style: "自然清晰", speed: "fast" },
  });
  assert.equal(preferences.mimo.speed, undefined);
});

test("empty custom style stays explicit while restore default removes only the style override", () => {
  const preferences = { deepseek: { voice: "tide" }, mimo: { voice: "冰糖", style: "自然清晰" } };
  assert.equal(JSON.parse(buildSpeechPreferencesUpdate(preferences, "mimo", { style: "" })).mimo.style, "");
  assert.deepEqual(JSON.parse(buildSpeechPreferencesUpdate(preferences, "mimo", { style: undefined })), {
    deepseek: { voice: "tide" }, mimo: { voice: "冰糖" },
  });
  assert.deepEqual(JSON.parse(buildSpeechPreferencesUpdate({ mimo: { style: "" } }, "mimo", { style: undefined })), {});
});
