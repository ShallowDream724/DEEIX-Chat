import assert from "node:assert/strict";
import test from "node:test";
import { flattenSpeechSettings, buildSpeechSettingsPatch, isSpeechSettingsFieldVisible, speechSettingsMissingAPIKey } from "./speech-settings.ts";

test("saving speech settings preserves a configured, masked API key", () => {
  const saved = flattenSpeechSettings([
    { key: "deepseek_api_key", value: "", sensitive: true, configured: true },
    { key: "cache_max_bytes", value: "1024" },
    { key: "max_recording_seconds", value: "90" },
  ]);
  assert.equal(saved.deepseek_api_key, "");
  assert.equal(saved.cache_max_bytes, undefined);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, input_provider: "deepseek" }, saved), [
    { namespace: "speech", key: "input_provider", value: "deepseek" },
  ]);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_api_key: " " }, saved), []);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_api_key: "replacement", asr_cny_per_hour: "0" }, saved), [
    { namespace: "speech", key: "deepseek_api_key", value: "replacement" },
    { namespace: "speech", key: "asr_cny_per_hour", value: "0" },
  ]);
});

test("one site source controls conditional credentials and separate free-by-default charging", () => {
  const defaults = flattenSpeechSettings([]);
  assert.equal(defaults.input_provider, "browser");
  assert.equal(defaults.charge_asr, "false");
  assert.equal(defaults.charge_tts, "false");
  assert.equal(defaults.enabled, undefined);
  assert.equal(defaults.doubao_enabled, undefined);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_api_key", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("asr_cny_per_hour", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("charge_asr", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_api_key", { ...defaults, input_provider: "deepseek" }), true);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_api_key", { ...defaults, deepseek_tts_enabled: "true" }), true);
  assert.equal(isSpeechSettingsFieldVisible("asr_cny_per_hour", { ...defaults, input_provider: "wetype", charge_asr: "true" }), true);
  assert.equal(isSpeechSettingsFieldVisible("tts_cny_per_10000_characters", { ...defaults, charge_tts: "true" }), true);
});

test("missing input source always defaults to browser and explicit choices remain unchanged", () => {
  const legacy = [{ key: "enabled", value: "true" }, { key: "doubao_enabled", value: "true" }, { key: "deepseek_asr_enabled", value: "true" }];
  assert.equal(flattenSpeechSettings(legacy).input_provider, "browser");
  assert.equal(flattenSpeechSettings([...legacy, { key: "input_provider", value: "" }]).input_provider, "browser");
  assert.equal(flattenSpeechSettings([...legacy, { key: "input_provider", value: "wetype" }]).input_provider, "wetype");
});

test("DeepSeek input and read aloud require a new or already-configured speech key", () => {
  const defaults = flattenSpeechSettings([]);
  assert.equal(speechSettingsMissingAPIKey(defaults, []), false);
  for (const selected of [{ ...defaults, input_provider: "deepseek" }, { ...defaults, deepseek_tts_enabled: "true" }]) {
    assert.equal(speechSettingsMissingAPIKey(selected, []), true);
    assert.equal(speechSettingsMissingAPIKey({ ...selected, deepseek_api_key: "  " }, []), true);
    assert.equal(speechSettingsMissingAPIKey({ ...selected, deepseek_api_key: "new-speech-key" }, []), false);
    assert.equal(speechSettingsMissingAPIKey(selected, [{ key: "deepseek_api_key", value: "", sensitive: true, configured: true }]), false);
  }
});
