import type { SpeechCapabilities, SpeechSynthesisOptions, SpeechTTSCapability } from "@/shared/api/speech";

export type SpeechProviderPreferences = { voice?: string; speed?: string; style?: string };
export type SpeechPreferences = Record<string, SpeechProviderPreferences>;

export function parseSpeechPreferences(value?: string): SpeechPreferences {
  if (!value) return {};
  try {
    const parsed: unknown = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).flatMap(([provider, entry]) => {
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) return [];
      const preferences: SpeechProviderPreferences = {};
      for (const key of ["voice", "speed", "style"] as const) {
        if (key in entry && typeof entry[key] === "string") preferences[key] = entry[key];
      }
      return [[provider, preferences]];
    }));
  } catch { return {}; }
}

export function getSelectedSpeechTTSCapability(capabilities: SpeechCapabilities): SpeechTTSCapability | undefined {
  // Older servers expose their only enabled provider without a selection field.
  return capabilities.selected_tts_provider
    ? capabilities.tts.find((item) => item.id === capabilities.selected_tts_provider)
    : capabilities.tts[0];
}

export function resolveSpeechPreferences(capability: SpeechTTSCapability, settings: Readonly<Record<string, string | undefined>>): SpeechSynthesisOptions {
  const preferences = parseSpeechPreferences(settings["chat.speech_preferences"])[capability.id];
  const selectedVoice = preferences?.voice ?? (capability.id === "deepseek" ? settings["chat.speech_voice"] : undefined);
  const defaultVoice = capability.voices.find((voice) => voice.id === capability.default_voice)?.id ?? capability.voices[0]?.id ?? "";
  const voice = capability.voices.find((item) => item.id === selectedVoice)?.id ?? defaultVoice;
  const options: SpeechSynthesisOptions = { provider: capability.id, voice };
  if (capability.speeds?.length) {
    const defaultSpeed = capability.speeds.find((speed) => speed.id === capability.default_speed)?.id ?? capability.speeds[0].id;
    options.speed = capability.speeds.find((speed) => speed.id === preferences?.speed)?.id ?? defaultSpeed;
  }
  if (capability.supports_style) {
    const style = preferences?.style;
    options.style = style !== undefined && (!capability.max_style_characters || [...style].length <= capability.max_style_characters)
      ? style : capability.default_style ?? "";
  }
  return options;
}
