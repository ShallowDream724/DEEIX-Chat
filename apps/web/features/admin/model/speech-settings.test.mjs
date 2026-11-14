import assert from "node:assert/strict";
import test from "node:test";
import { flattenSpeechSettings, buildSpeechSettingsPatch } from "./speech-settings.ts";

test("saving speech settings preserves a configured, masked API key", () => {
  const saved = flattenSpeechSettings([
    { key: "deepseek_api_key", value: "", sensitive: true, configured: true },
    { key: "cache_max_bytes", value: "1024" },
    { key: "max_recording_seconds", value: "90" },
  ]);
  assert.equal(saved.deepseek_api_key, "");
  assert.equal(saved.cache_max_bytes, undefined);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, enabled: "true" }, saved), [
    { namespace: "speech", key: "enabled", value: "true" },
  ]);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_api_key: " " }, saved), []);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_api_key: "replacement", asr_cny_per_hour: "0" }, saved), [
    { namespace: "speech", key: "deepseek_api_key", value: "replacement" },
    { namespace: "speech", key: "asr_cny_per_hour", value: "0" },
  ]);
});
