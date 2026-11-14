import assert from "node:assert/strict";
import test from "node:test";
import { flattenSpeechSettings, buildSpeechSettingsPatch, buildSpeechSettingsGroupSave, mergeSpeechSettingsDraft, isSpeechSettingsFieldVisible, speechSettingsMissingAPIKey, changeSpeechSetting, canReuseSpeechConnection, speechCredentialsSourceOptions, speechSettingsMissingConnectionFields } from "./speech-settings.ts";

test("saving speech settings preserves a configured, masked API key", () => {
  const saved = flattenSpeechSettings([
    { key: "deepseek_asr_api_key", value: "masked-value", sensitive: true, configured: true },
    { key: "cache_max_bytes", value: "1024" },
    { key: "max_recording_seconds", value: "90" },
  ]);
  assert.equal(saved.deepseek_asr_api_key, "");
  assert.equal(saved.deepseek_api_key, undefined);
  assert.equal(saved.cache_max_bytes, undefined);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, input_provider: "deepseek" }, saved), [
    { namespace: "speech", key: "input_provider", value: "deepseek" },
  ]);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_asr_api_key: " " }, saved), []);
  assert.deepEqual(buildSpeechSettingsPatch({ ...saved, deepseek_asr_api_key: "replacement", asr_cny_per_hour: "0" }, saved), [
    { namespace: "speech", key: "deepseek_asr_api_key", value: "replacement" },
    { namespace: "speech", key: "asr_cny_per_hour", value: "0" },
  ]);
});

test("one site source controls conditional credentials and separate free-by-default charging", () => {
  const defaults = flattenSpeechSettings([]);
  assert.equal(defaults.input_provider, "browser");
  assert.equal(defaults.tts_provider, "deepseek");
  assert.equal(defaults.charge_asr, "false");
  assert.equal(defaults.charge_tts, "false");
  assert.equal(defaults.enabled, undefined);
  assert.equal(defaults.doubao_enabled, undefined);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_api_key", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("asr_cny_per_hour", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("charge_asr", defaults), false);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_asr_api_key", { ...defaults, input_provider: "deepseek" }), true);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_tts_api_key", { ...defaults, input_provider: "deepseek" }), false);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_tts_api_key", { ...defaults, deepseek_tts_enabled: "true" }), true);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_tts_api_key", { ...defaults, deepseek_tts_enabled: "true", deepseek_tts_credentials_source: "asr" }), false);
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
  for (const [role, selected] of [["asr", { ...defaults, input_provider: "deepseek" }], ["tts", { ...defaults, deepseek_tts_enabled: "true" }]]) {
    const key = `deepseek_${role}_api_key`;
    assert.equal(speechSettingsMissingAPIKey(selected, []), true);
    assert.equal(speechSettingsMissingAPIKey({ ...selected, [key]: "  " }, []), true);
    assert.equal(speechSettingsMissingAPIKey({ ...selected, [key]: "new-speech-key" }, []), false);
    assert.equal(speechSettingsMissingAPIKey(selected, [{ key, value: "", sensitive: true, configured: true }]), false);
    assert.equal(speechSettingsMissingAPIKey(selected, [{ key: "deepseek_api_key", value: "", sensitive: true, configured: true }]), true);
  }
});

test("new DeepSeek selections reuse the configured peer without copying a masked key or custom URL", () => {
  const defaults = flattenSpeechSettings([]);
  const asr = { ...defaults, input_provider: "deepseek", deepseek_asr_base_url: "https://speech.example.test", deepseek_asr_api_key: "asr-key" };
  const enabled = changeSpeechSetting(asr, "deepseek_tts_enabled", "true", []);
  assert.equal(enabled.deepseek_tts_credentials_source, "asr");
  assert.equal(enabled.deepseek_tts_api_key, "");
  assert.equal(enabled.deepseek_tts_base_url, "https://api.deepseek.com");
  assert.equal(speechSettingsMissingAPIKey(enabled, []), false);
  const items = [{ key: "deepseek_tts_api_key", configured: true, sensitive: true, value: "" }, { key: "deepseek_asr_credentials_source", value: "own", configured: true }];
  const selected = changeSpeechSetting(defaults, "input_provider", "deepseek", items);
  assert.equal(selected.deepseek_asr_credentials_source, "tts");
  assert.equal(selected.deepseek_asr_api_key, "");
  assert.equal(speechSettingsMissingAPIKey(selected, items), false);
});

test("an explicit independent connection, existing key or custom address is not replaced automatically", () => {
  const values = { ...flattenSpeechSettings([]), deepseek_tts_api_key: "peer-key" };
  const explicit = [{ key: "deepseek_asr_api_key", value: "", sensitive: true, configured: true }];
  assert.equal(changeSpeechSetting(values, "input_provider", "deepseek", explicit).deepseek_asr_credentials_source, "own");
  assert.equal(changeSpeechSetting(values, "input_provider", "deepseek", [], new Set(["deepseek_asr_credentials_source"])).deepseek_asr_credentials_source, "own");
  assert.equal(changeSpeechSetting({ ...values, deepseek_asr_api_key: "own-key" }, "input_provider", "deepseek", []).deepseek_asr_credentials_source, "own");
  assert.equal(changeSpeechSetting({ ...values, deepseek_asr_base_url: "https://own.example.test" }, "input_provider", "deepseek", []).deepseek_asr_credentials_source, "own");
});

test("connection reuse cannot create a cycle, and missing referenced configuration is actionable", () => {
  const values = { ...flattenSpeechSettings([]), input_provider: "deepseek", deepseek_tts_enabled: "true", deepseek_asr_credentials_source: "tts", deepseek_tts_api_key: "tts-key" };
  assert.equal(canReuseSpeechConnection("asr", values, []), true);
  assert.equal(canReuseSpeechConnection("tts", values, []), false);
  assert.deepEqual(speechCredentialsSourceOptions("tts", values, []), ["own"]);
  const cycle = { ...values, deepseek_tts_credentials_source: "asr", deepseek_asr_api_key: "asr-key" };
  assert.deepEqual([...speechSettingsMissingConnectionFields(cycle, [])], ["deepseek_asr_credentials_source", "deepseek_tts_credentials_source"]);
  const missing = { ...values, deepseek_tts_api_key: "" };
  assert.ok(speechSettingsMissingConnectionFields(missing, []).has("deepseek_asr_credentials_source"));
  assert.deepEqual(speechCredentialsSourceOptions("asr", missing, []), ["own", "tts"]);
  assert.equal(canReuseSpeechConnection("tts", { ...values, input_provider: "browser", deepseek_asr_credentials_source: "own", deepseek_asr_api_key: "saved-input-key" }, []), true);
  for (const url of ["", "invalid", "https://name:secret@example.test", "https://example.test?key=secret"]) {
    const invalid = { ...values, deepseek_tts_base_url: url };
    assert.equal(canReuseSpeechConnection("asr", invalid, []), false);
    assert.ok(speechSettingsMissingConnectionFields(invalid, []).has("deepseek_tts_base_url"));
  }
});

test("pricing saves independently of incomplete speech drafts and preserves those drafts", () => {
  const saved = flattenSpeechSettings([]);
  const current = { ...saved, input_provider: "deepseek", deepseek_tts_enabled: "true", charge_tts: "true", tts_cny_per_10000_characters: "2" };
  const plan = buildSpeechSettingsGroupSave(current, saved, [], "pricing");
  assert.deepEqual(plan.patches, [
    { namespace: "speech", key: "charge_tts", value: "true" },
    { namespace: "speech", key: "tts_cny_per_10000_characters", value: "2" },
  ]);
  assert.equal(plan.missingConnectionFields.size, 0);
  const nextSaved = { ...saved, charge_tts: "true", tts_cny_per_10000_characters: "2" };
  const merged = mergeSpeechSettingsDraft(current, saved, plan.patches, nextSaved);
  assert.deepEqual(merged, current);
  assert.deepEqual(buildSpeechSettingsPatch(merged, nextSaved).map((patch) => patch.key), ["input_provider", "deepseek_tts_enabled"]);
  assert.equal(buildSpeechSettingsGroupSave(merged, nextSaved, [], "input").missingConnectionFields.has("deepseek_asr_api_key"), true);
  assert.equal(buildSpeechSettingsGroupSave(merged, nextSaved, [], "output").missingConnectionFields.has("deepseek_tts_api_key"), true);
});

test("saving either speech role atomically includes a new reused connection but leaves the other group's controls unsaved", () => {
  const saved = flattenSpeechSettings([]);
  for (const [group, role, peer] of [["input", "asr", "tts"], ["output", "tts", "asr"]]) {
    const current = {
      ...saved,
      input_provider: "deepseek",
      deepseek_tts_enabled: "true",
      max_recording_seconds: "180",
      [`deepseek_${role}_credentials_source`]: peer,
      [`deepseek_${peer}_base_url`]: "https://speech.example.test",
      [`deepseek_${peer}_api_key`]: "new-peer-key",
      charge_tts: "true",
    };
    const plan = buildSpeechSettingsGroupSave(current, saved, [], group);
    const submitted = Object.fromEntries(plan.patches.map((patch) => [patch.key, patch.value]));
    assert.equal(plan.missingConnectionFields.size, 0);
    assert.equal(submitted[`deepseek_${role}_credentials_source`], peer);
    assert.equal(submitted[`deepseek_${peer}_base_url`], "https://speech.example.test");
    assert.equal(submitted[`deepseek_${peer}_api_key`], "new-peer-key");
    assert.equal(submitted.charge_tts, undefined);
    assert.equal(submitted[group === "input" ? "deepseek_tts_enabled" : "input_provider"], undefined);
    if (group === "output") assert.equal(submitted.max_recording_seconds, undefined);

    const nextSaved = { ...saved, ...submitted, [`deepseek_${peer}_api_key`]: "" };
    const merged = mergeSpeechSettingsDraft(current, saved, plan.patches, nextSaved);
    assert.equal(merged[`deepseek_${peer}_api_key`], "");
    assert.equal(merged.charge_tts, "true");
    assert.equal(merged[group === "input" ? "deepseek_tts_enabled" : "input_provider"], group === "input" ? "true" : "deepseek");
  }
});

test("switching connection ownership saves the peer source and key together without a transient cycle", () => {
  const items = [{ key: "deepseek_asr_api_key", configured: true }];
  const saved = { ...flattenSpeechSettings([]), input_provider: "deepseek", deepseek_tts_enabled: "true", deepseek_tts_credentials_source: "asr" };
  const current = { ...saved, deepseek_asr_credentials_source: "tts", deepseek_tts_credentials_source: "own", deepseek_tts_api_key: "new-output-key", deepseek_tts_enabled: "false" };
  const plan = buildSpeechSettingsGroupSave(current, saved, items, "input");
  assert.equal(plan.missingConnectionFields.size, 0);
  assert.deepEqual(plan.patches.map((patch) => patch.key), ["deepseek_asr_credentials_source", "deepseek_tts_credentials_source", "deepseek_tts_api_key"]);
  const cycle = buildSpeechSettingsGroupSave({ ...current, deepseek_tts_credentials_source: "asr" }, saved, items, "input");
  assert.ok(cycle.missingConnectionFields.has("deepseek_asr_credentials_source"));
  assert.ok(cycle.missingConnectionFields.has("deepseek_tts_credentials_source"));
});

test("a saved peer connection is reused without consuming its replacement-key draft", () => {
  const items = [{ key: "deepseek_tts_api_key", configured: true }];
  const saved = flattenSpeechSettings([]);
  const current = { ...saved, input_provider: "deepseek", deepseek_asr_credentials_source: "tts", deepseek_tts_api_key: "replacement-key" };
  const plan = buildSpeechSettingsGroupSave(current, saved, items, "input");
  assert.equal(plan.missingConnectionFields.size, 0);
  assert.deepEqual(plan.patches.map((patch) => patch.key), ["input_provider", "deepseek_asr_credentials_source"]);
  const nextSaved = { ...saved, input_provider: "deepseek", deepseek_asr_credentials_source: "tts" };
  const merged = mergeSpeechSettingsDraft(current, saved, plan.patches, nextSaved);
  assert.equal(merged.deepseek_tts_api_key, "replacement-key");
  assert.deepEqual(buildSpeechSettingsPatch(merged, nextSaved).map((patch) => patch.key), ["deepseek_tts_api_key"]);
});

test("a newer edit survives a response for an earlier submitted value", () => {
  const saved = flattenSpeechSettings([]);
  const submitted = [{ namespace: "speech", key: "max_recording_seconds", value: "180" }];
  const current = { ...saved, max_recording_seconds: "240" };
  const nextSaved = { ...saved, max_recording_seconds: "180" };
  assert.equal(mergeSpeechSettingsDraft(current, saved, submitted, nextSaved).max_recording_seconds, "240");
});

test("MiMo uses an independent connection and preserves the DeepSeek provider default", () => {
  const defaults = flattenSpeechSettings([]);
  assert.equal(defaults.tts_provider, "deepseek");
  assert.equal(defaults.mimo_base_url, "https://api.xiaomimimo.com/v1");
  assert.equal(flattenSpeechSettings([{ key: "tts_provider", value: "mimo" }]).tts_provider, "mimo");
  const values = { ...defaults, tts_provider: "mimo", input_provider: "deepseek", deepseek_asr_api_key: "asr-key", deepseek_tts_api_key: "tts-key" };
  const enabled = changeSpeechSetting(values, "deepseek_tts_enabled", "true", []);
  assert.equal(enabled.deepseek_tts_credentials_source, "own");
  assert.equal(isSpeechSettingsFieldVisible("mimo_api_key", values), false);
  assert.equal(isSpeechSettingsFieldVisible("mimo_api_key", enabled), true);
  assert.equal(isSpeechSettingsFieldVisible("deepseek_tts_api_key", enabled), false);
  assert.deepEqual([...speechSettingsMissingConnectionFields(enabled, [])], ["mimo_api_key"]);
  assert.equal(speechSettingsMissingConnectionFields(enabled, [{ key: "mimo_api_key", configured: true }]).size, 0);
  const invalidURL = { ...enabled, mimo_api_key: "mimo-key", mimo_base_url: "https://name:secret@example.test" };
  assert.deepEqual([...speechSettingsMissingConnectionFields(invalidURL, [])], ["mimo_base_url"]);
});

test("saving MiMo output leaves input credentials and pricing drafts independent", () => {
  const saved = flattenSpeechSettings([]);
  const current = {
    ...saved, tts_provider: "mimo", deepseek_tts_enabled: "true", mimo_api_key: "mimo-key",
    deepseek_tts_credentials_source: "asr", input_provider: "deepseek", deepseek_asr_api_key: "asr-draft", charge_tts: "true",
  };
  const plan = buildSpeechSettingsGroupSave(current, saved, [], "output");
  assert.equal(plan.missingConnectionFields.size, 0);
  assert.deepEqual(plan.patches.map((patch) => patch.key), ["tts_provider", "deepseek_tts_enabled", "deepseek_tts_credentials_source", "mimo_api_key"]);
  const nextSaved = { ...saved, ...Object.fromEntries(plan.patches.map((patch) => [patch.key, patch.value])), mimo_api_key: "" };
  const merged = mergeSpeechSettingsDraft(current, saved, plan.patches, nextSaved);
  assert.equal(merged.mimo_api_key, "");
  assert.equal(merged.deepseek_asr_api_key, "asr-draft");
  assert.equal(merged.charge_tts, "true");
  assert.deepEqual(buildSpeechSettingsPatch(merged, nextSaved).map((patch) => patch.key), ["input_provider", "deepseek_asr_api_key", "charge_tts"]);
});

test("DeepSeek input references remain atomic when MiMo is the selected output", () => {
  const saved = flattenSpeechSettings([{ key: "tts_provider", value: "mimo" }]);
  const current = { ...saved, input_provider: "deepseek", deepseek_asr_credentials_source: "tts", deepseek_tts_api_key: "deepseek-key", mimo_api_key: "mimo-draft" };
  const plan = buildSpeechSettingsGroupSave(current, saved, [], "input");
  assert.equal(plan.missingConnectionFields.size, 0);
  assert.deepEqual(plan.patches.map((patch) => patch.key), ["input_provider", "deepseek_asr_credentials_source", "deepseek_tts_api_key"]);
  assert.equal(plan.patches.some((patch) => patch.key === "mimo_api_key"), false);
});
