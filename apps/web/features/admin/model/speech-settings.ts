import type { PatchSettingItem, SettingItem, SettingsGrouped } from "@/shared/api/settings-types";
import type { SettingsFieldType } from "@/features/admin/components/shared/settings-runtime-panel";

type SpeechSettingsField = {
  key: string;
  type: SettingsFieldType;
  defaultValue: string;
  group: "input" | "output" | "connection" | "pricing";
};

export const SPEECH_SETTINGS_FIELDS: readonly SpeechSettingsField[] = [
  { key: "input_provider", type: "select", defaultValue: "browser", group: "input" },
  { key: "max_recording_seconds", type: "int", defaultValue: "120", group: "input" },
  { key: "max_concurrent_per_user", type: "int", defaultValue: "2", group: "input" },
  { key: "deepseek_tts_enabled", type: "bool", defaultValue: "false", group: "output" },
  { key: "deepseek_base_url", type: "string", defaultValue: "https://api.deepseek.com", group: "connection" },
  { key: "deepseek_api_key", type: "password", defaultValue: "", group: "connection" },
  { key: "charge_asr", type: "bool", defaultValue: "false", group: "pricing" },
  { key: "asr_cny_per_hour", type: "string", defaultValue: "0.39", group: "pricing" },
  { key: "charge_tts", type: "bool", defaultValue: "false", group: "pricing" },
  { key: "tts_cny_per_10000_characters", type: "string", defaultValue: "1.5", group: "pricing" },
];

export function flattenSpeechSettings(items: readonly SettingItem[]): Record<string, string> {
  const values = Object.fromEntries(SPEECH_SETTINGS_FIELDS.map((field) => [
    field.key,
    field.type === "password" ? "" : items.find((item) => item.key === field.key)?.value ?? field.defaultValue,
  ]));
  if (!SPEECH_INPUT_PROVIDERS.some((provider) => provider === values.input_provider)) values.input_provider = "browser";
  return values;
}

export const SPEECH_INPUT_PROVIDERS = ["browser", "doubao-ime", "wetype", "deepseek"] as const;

export function isSpeechSettingsFieldVisible(key: string, values: Record<string, string>): boolean {
  if (key === "deepseek_base_url" || key === "deepseek_api_key") return values.input_provider === "deepseek" || values.deepseek_tts_enabled === "true";
  if (key === "charge_asr") return Boolean(values.input_provider) && values.input_provider !== "browser";
  if (key === "asr_cny_per_hour") return values.input_provider !== "browser" && Boolean(values.input_provider) && values.charge_asr === "true";
  if (key === "tts_cny_per_10000_characters") return values.charge_tts === "true";
  return true;
}

export function speechSettingsMissingAPIKey(values: Record<string, string>, items: readonly SettingItem[]): boolean {
  const requiresKey = values.input_provider === "deepseek" || values.deepseek_tts_enabled === "true";
  const configured = items.some((item) => item.key === "deepseek_api_key" && item.configured);
  return requiresKey && !configured && !values.deepseek_api_key?.trim();
}

export function buildSpeechSettingsPatch(current: Record<string, string>, saved: Record<string, string>): PatchSettingItem[] {
  return SPEECH_SETTINGS_FIELDS
    .filter((field) => current[field.key] !== saved[field.key] && (field.type !== "password" || Boolean(current[field.key]?.trim())))
    .map((field) => ({ namespace: "speech", key: field.key, value: current[field.key] ?? "" }));
}

export function speechSettingsFromResponse(grouped: SettingsGrouped): SettingItem[] {
  return grouped.speech ?? [];
}
