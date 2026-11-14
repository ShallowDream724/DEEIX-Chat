import type { PatchSettingItem, SettingItem, SettingsGrouped } from "@/shared/api/settings-types";
import type { SettingsFieldType } from "@/features/admin/components/shared/settings-runtime-panel";

type SpeechSettingsField = {
  key: string;
  type: SettingsFieldType;
  defaultValue: string;
  group: "general" | "input" | "deepseek" | "pricing";
};

export const SPEECH_SETTINGS_FIELDS: readonly SpeechSettingsField[] = [
  { key: "enabled", type: "bool", defaultValue: "false", group: "general" },
  { key: "max_recording_seconds", type: "int", defaultValue: "120", group: "general" },
  { key: "max_concurrent_per_user", type: "int", defaultValue: "2", group: "general" },
  { key: "doubao_enabled", type: "bool", defaultValue: "false", group: "input" },
  { key: "wetype_enabled", type: "bool", defaultValue: "false", group: "input" },
  { key: "deepseek_asr_enabled", type: "bool", defaultValue: "false", group: "deepseek" },
  { key: "deepseek_tts_enabled", type: "bool", defaultValue: "false", group: "deepseek" },
  { key: "deepseek_base_url", type: "string", defaultValue: "https://api.deepseek.com", group: "deepseek" },
  { key: "deepseek_api_key", type: "password", defaultValue: "", group: "deepseek" },
  { key: "asr_cny_per_hour", type: "string", defaultValue: "0.39", group: "pricing" },
  { key: "tts_cny_per_10000_characters", type: "string", defaultValue: "1.5", group: "pricing" },
];

export function flattenSpeechSettings(items: readonly SettingItem[]): Record<string, string> {
  return Object.fromEntries(SPEECH_SETTINGS_FIELDS.map((field) => [
    field.key,
    field.type === "password" ? "" : items.find((item) => item.key === field.key)?.value ?? field.defaultValue,
  ]));
}

export function buildSpeechSettingsPatch(current: Record<string, string>, saved: Record<string, string>): PatchSettingItem[] {
  return SPEECH_SETTINGS_FIELDS
    .filter((field) => current[field.key] !== saved[field.key] && (field.type !== "password" || Boolean(current[field.key]?.trim())))
    .map((field) => ({ namespace: "speech", key: field.key, value: current[field.key] ?? "" }));
}

export function speechSettingsFromResponse(grouped: SettingsGrouped): SettingItem[] {
  return grouped.speech ?? [];
}
