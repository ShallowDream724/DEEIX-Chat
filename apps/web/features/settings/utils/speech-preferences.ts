import type { SpeechPreferences } from "@/features/chat";

export function buildSpeechPreferencesUpdate(
  preferences: SpeechPreferences,
  provider: string,
  changes: { voice?: string; speed?: string; style?: string },
): string {
  const next = { ...preferences, [provider]: { ...preferences[provider], ...changes } };
  for (const field of ["voice", "speed", "style"] as const) {
    if (next[provider][field] === undefined) delete next[provider][field];
  }
  if (Object.keys(next[provider]).length === 0) delete next[provider];
  return JSON.stringify(next);
}
