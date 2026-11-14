import type { PatchSettingItem, SettingItem, SettingsGrouped } from "@/shared/api/settings-types";
import type { SettingsFieldType } from "@/features/admin/components/shared/settings-runtime-panel";

export type SpeechRole = "asr" | "tts";
export type SpeechSettingsGroup = "input" | "output" | "pricing";
export type SpeechSettingsField = {
  key: string;
  type: SettingsFieldType;
  defaultValue: string;
  group: SpeechSettingsGroup;
  subgroupKey?: SpeechRole;
};

const DEFAULT_DEEPSEEK_URL = "https://api.deepseek.com";
export const SPEECH_INPUT_PROVIDERS = ["browser", "doubao-ime", "wetype", "deepseek"] as const;
export const SPEECH_OUTPUT_PROVIDERS = ["deepseek"] as const;

export const SPEECH_SETTINGS_FIELDS: readonly SpeechSettingsField[] = [
  { key: "input_provider", type: "select", defaultValue: "browser", group: "input" },
  { key: "deepseek_asr_credentials_source", type: "select", defaultValue: "own", group: "input", subgroupKey: "asr" },
  { key: "deepseek_asr_base_url", type: "string", defaultValue: DEFAULT_DEEPSEEK_URL, group: "input", subgroupKey: "asr" },
  { key: "deepseek_asr_api_key", type: "password", defaultValue: "", group: "input", subgroupKey: "asr" },
  { key: "max_recording_seconds", type: "int", defaultValue: "120", group: "input" },
  { key: "max_concurrent_per_user", type: "int", defaultValue: "2", group: "input" },
  { key: "tts_provider", type: "select", defaultValue: "deepseek", group: "output" },
  { key: "deepseek_tts_enabled", type: "bool", defaultValue: "false", group: "output" },
  { key: "deepseek_tts_credentials_source", type: "select", defaultValue: "own", group: "output", subgroupKey: "tts" },
  { key: "deepseek_tts_base_url", type: "string", defaultValue: DEFAULT_DEEPSEEK_URL, group: "output", subgroupKey: "tts" },
  { key: "deepseek_tts_api_key", type: "password", defaultValue: "", group: "output", subgroupKey: "tts" },
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
  if (!SPEECH_OUTPUT_PROVIDERS.some((provider) => provider === values.tts_provider)) values.tts_provider = "deepseek";
  return values;
}

function peerRole(role: SpeechRole): SpeechRole { return role === "asr" ? "tts" : "asr"; }
export function speechCredentialsSourceKey(role: SpeechRole) { return `deepseek_${role}_credentials_source`; }

function hasOwnSpeechKey(role: SpeechRole, values: Record<string, string>, items: readonly SettingItem[]) {
  const key = `deepseek_${role}_api_key`;
  return Boolean(values[key]?.trim()) || items.some((item) => item.key === key && item.configured);
}

function hasOwnSpeechConnection(role: SpeechRole, values: Record<string, string>, items: readonly SettingItem[]) {
  return validSpeechBaseURL(values[`deepseek_${role}_base_url`] ?? "") && hasOwnSpeechKey(role, values, items);
}

function validSpeechBaseURL(value: string) {
  try {
    const url = new URL(value.trim());
    return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash;
  } catch { return false; }
}

export function canReuseSpeechConnection(role: SpeechRole, values: Record<string, string>, items: readonly SettingItem[]): boolean {
  const peer = peerRole(role);
  return values[speechCredentialsSourceKey(peer)] === "own" && hasOwnSpeechConnection(peer, values, items);
}

export function speechCredentialsSourceOptions(role: SpeechRole, values: Record<string, string>, items: readonly SettingItem[]): string[] {
  const peer = peerRole(role);
  return canReuseSpeechConnection(role, values, items) || values[speechCredentialsSourceKey(role)] === peer ? ["own", peer] : ["own"];
}

export function changeSpeechSetting(
  values: Record<string, string>, key: string, value: string, items: readonly SettingItem[], explicitSources: ReadonlySet<string> = new Set(),
): Record<string, string> {
  const next = { ...values, [key]: value };
  const role: SpeechRole | undefined = key === "input_provider" && value === "deepseek" && values.input_provider !== value ? "asr"
    : key === "deepseek_tts_enabled" && value === "true" && values.deepseek_tts_enabled !== value ? "tts" : undefined;
  if (!role) return next;
  const sourceKey = speechCredentialsSourceKey(role);
  const explicitlyConfigured = explicitSources.has(sourceKey);
  if (!explicitlyConfigured && next[sourceKey] === "own" && !hasOwnSpeechKey(role, next, items)
    && next[`deepseek_${role}_base_url`] === DEFAULT_DEEPSEEK_URL && canReuseSpeechConnection(role, next, items)) {
    next[sourceKey] = peerRole(role);
  }
  return next;
}

export function isSpeechSettingsFieldVisible(key: string, values: Record<string, string>): boolean {
  for (const role of ["asr", "tts"] as const) {
    if (key === speechCredentialsSourceKey(role) || key === `deepseek_${role}_base_url` || key === `deepseek_${role}_api_key`) {
      const enabled = role === "asr" ? values.input_provider === "deepseek" : values.deepseek_tts_enabled === "true" && values.tts_provider === "deepseek";
      return enabled && (key === speechCredentialsSourceKey(role) || values[speechCredentialsSourceKey(role)] === "own");
    }
  }
  if (key === "charge_asr") return Boolean(values.input_provider) && values.input_provider !== "browser";
  if (key === "asr_cny_per_hour") return values.input_provider !== "browser" && Boolean(values.input_provider) && values.charge_asr === "true";
  if (key === "tts_cny_per_10000_characters") return values.charge_tts === "true";
  return SPEECH_SETTINGS_FIELDS.some((field) => field.key === key);
}

export function speechSettingsMissingConnectionFields(values: Record<string, string>, items: readonly SettingItem[]): ReadonlySet<string> {
  const missing = new Set<string>();
  for (const role of ["asr", "tts"] as const) {
    const active = role === "asr" ? values.input_provider === "deepseek" : values.deepseek_tts_enabled === "true";
    if (!active) continue;
    const sourceKey = speechCredentialsSourceKey(role);
    if (values[sourceKey] !== "own") {
      if (values[sourceKey] !== peerRole(role) || !canReuseSpeechConnection(role, values, items)) missing.add(sourceKey);
    } else {
      if (!validSpeechBaseURL(values[`deepseek_${role}_base_url`] ?? "")) missing.add(`deepseek_${role}_base_url`);
      if (!hasOwnSpeechKey(role, values, items)) missing.add(`deepseek_${role}_api_key`);
    }
  }
  return missing;
}

export function speechSettingsMissingAPIKey(values: Record<string, string>, items: readonly SettingItem[]): boolean {
  return speechSettingsMissingConnectionFields(values, items).size > 0;
}

export function buildSpeechSettingsPatch(current: Record<string, string>, saved: Record<string, string>): PatchSettingItem[] {
  return SPEECH_SETTINGS_FIELDS
    .filter((field) => current[field.key] !== saved[field.key] && (field.type !== "password" || Boolean(current[field.key]?.trim())))
    .map((field) => ({ namespace: "speech", key: field.key, value: current[field.key] ?? "" }));
}

export function buildSpeechSettingsGroupSave(
  current: Record<string, string>, saved: Record<string, string>, items: readonly SettingItem[], group: SpeechSettingsGroup,
) {
  const dirty = buildSpeechSettingsPatch(current, saved);
  const groupKeys = new Set(SPEECH_SETTINGS_FIELDS.filter((field) => field.group === group).map((field) => field.key));
  const patches = dirty.filter((item) => groupKeys.has(item.key));
  if (group === "pricing" || patches.length === 0) return { patches, missingConnectionFields: new Set<string>() };

  const effective = { ...saved, ...Object.fromEntries(patches.map((item) => [item.key, item.value])) };
  const role = group === "input" ? "asr" : "tts";
  const peer = peerRole(role);
  // A new reference may need the peer's connection in the same transaction,
  // while its enable switch and other unsaved fields remain local drafts.
  if (effective[speechCredentialsSourceKey(role)] === peer && !canReuseSpeechConnection(role, effective, items)) {
    const dependencyKeys = new Set(SPEECH_SETTINGS_FIELDS.filter((field) => field.subgroupKey === peer).map((field) => field.key));
    for (const patch of dirty.filter((item) => dependencyKeys.has(item.key))) {
      patches.push(patch);
      effective[patch.key] = patch.value;
    }
  }
  const missingConnectionFields = new Set(speechSettingsMissingConnectionFields(effective, items));
  if (effective.deepseek_asr_credentials_source === "tts" && effective.deepseek_tts_credentials_source === "asr") {
    missingConnectionFields.add(speechCredentialsSourceKey("asr"));
    missingConnectionFields.add(speechCredentialsSourceKey("tts"));
  }
  return { patches, missingConnectionFields };
}

export function mergeSpeechSettingsDraft(
  current: Record<string, string>, saved: Record<string, string>, patches: readonly PatchSettingItem[], nextSaved: Record<string, string>,
): Record<string, string> {
  const next = { ...nextSaved };
  const submitted = new Map(patches.map((item) => [item.key, item.value]));
  for (const field of SPEECH_SETTINGS_FIELDS) {
    if (current[field.key] !== saved[field.key] && (!submitted.has(field.key) || current[field.key] !== submitted.get(field.key))) {
      next[field.key] = current[field.key];
    }
  }
  return next;
}

export function speechSettingsFromResponse(grouped: SettingsGrouped): SettingItem[] {
  return grouped.speech ?? [];
}
