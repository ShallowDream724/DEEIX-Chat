export const INPUT_LIMIT_PROFILES = ["openai", "google_generate_content", "gemini_interactions", "anthropic", "openrouter", "compatible", "xai_image_edits", "xai_video", "openrouter_images"] as const;
export type InputLimitProfile = (typeof INPUT_LIMIT_PROFILES)[number];
export const INPUT_LIMIT_PROTOCOLS = [
  "openai_responses", "openai_chat_completions", "openai_image_generations", "openai_image_edits",
  "openrouter_chat_completions", "openrouter_responses", "openrouter_images", "anthropic_messages",
  "google_generate_content", "google_image_generation", "gemini_interactions", "xai_responses",
  "xai_image", "xai_image_edits", "xai_video", "xai_video_extensions",
] as const;
export const INPUT_LIMIT_FIELDS = [
  { key: "requestBytes", unit: "mb", group: "request" },
  { key: "imageCount", unit: "count", group: "images" },
  { key: "imageSingleBytes", unit: "mb", group: "images" },
  { key: "imageBytes", unit: "mb", group: "images" },
  { key: "nativeCount", unit: "count", group: "native" },
  { key: "nativeBytes", unit: "mb", group: "native" },
  { key: "pdfSingleBytes", unit: "mb", group: "native" },
  { key: "pdfBytes", unit: "mb", group: "native" },
  { key: "pdfPages", unit: "pages", group: "native" },
  { key: "nonPDFBytes", unit: "mb", group: "native" },
] as const;
export type InputLimitKey = (typeof INPUT_LIMIT_FIELDS)[number]["key"];
export type InputLimitValues = Partial<Record<InputLimitKey, number | null>>;
export type InputLimitUpstream = InputLimitValues & { protocols?: Record<string, InputLimitValues> };
export type InputLimitsConfig = {
  profiles: Partial<Record<InputLimitProfile, InputLimitValues>>;
  upstreams: Record<string, InputLimitUpstream>;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseInputLimits(raw: string): InputLimitsConfig | null {
  try {
    const config: unknown = JSON.parse(raw);
    if (!isRecord(config) || !isRecord(config.profiles) || !isRecord(config.upstreams)) return null;
    if (Object.keys(config).some((key) => key !== "profiles" && key !== "upstreams")) return null;
    const parseValues = (values: unknown): InputLimitValues | null => {
      if (!isRecord(values)) return null;
      const result: InputLimitValues = {};
      for (const [key, value] of Object.entries(values)) {
        const field = INPUT_LIMIT_FIELDS.find((item) => item.key === key);
        if (!field) return null;
        if (value !== null && (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || (field.unit !== "mb" && value > 2_147_483_647))) return null;
        result[field.key] = value;
      }
      return result;
    };
    const profiles: InputLimitsConfig["profiles"] = {};
    for (const [key, value] of Object.entries(config.profiles)) {
      const profile = INPUT_LIMIT_PROFILES.find((item) => item === key);
      const values = parseValues(value);
      if (!profile || !values) return null;
      profiles[profile] = values;
    }
    const upstreams: InputLimitsConfig["upstreams"] = {};
    for (const [id, upstream] of Object.entries(config.upstreams)) {
      if (!/^[1-9]\d*$/.test(id)) return null;
      if (!isRecord(upstream)) return null;
      const { protocols, ...values } = upstream;
      const parsed = parseValues(values);
      if (!parsed) return null;
      const result: InputLimitUpstream = { ...parsed };
      if (protocols !== undefined) {
        if (!isRecord(protocols)) return null;
        result.protocols = {};
        for (const [key, limits] of Object.entries(protocols)) {
          const protocol = INPUT_LIMIT_PROTOCOLS.find((item) => item === key);
          const protocolValues = parseValues(limits);
          if (!protocol || !protocolValues) return null;
          result.protocols[protocol] = protocolValues;
        }
      }
      upstreams[id] = result;
    }
    return { profiles, upstreams };
  } catch {
    return null;
  }
}

export function formatInputLimit(value: number | null | undefined, bytes: boolean): string {
  if (value == null) return "";
  if (!bytes) return String(value);
  const fraction = String(value % 1_000_000).padStart(6, "0").replace(/0+$/, "");
  return `${Math.floor(value / 1_000_000)}${fraction ? `.${fraction}` : ""}`;
}

export function parseInputLimitValue(raw: string, bytes: boolean): number | null | undefined {
  if (!raw.trim()) return null;
  if (!/^\d+(?:\.\d{1,6})?$/.test(raw.trim()) || (!bytes && raw.includes("."))) return undefined;
  const [whole, fraction = ""] = raw.trim().split(".");
  const value = bytes ? Number(`${whole}${fraction.padEnd(6, "0")}`) : Number(whole);
  return Number.isSafeInteger(value) && value >= 0 && (bytes || value <= 2_147_483_647) ? value : undefined;
}

export function resolveInputLimitProfile(protocol: string, baseURL: string): InputLimitProfile {
  protocol = protocol.trim().toLowerCase();
  if (protocol === "xai_image_edits" || protocol === "xai_video" || protocol === "openrouter_images") return protocol;
  if (protocol === "google_generate_content" || protocol === "google_image_generation") return "google_generate_content";
  if (protocol === "gemini_interactions") return "gemini_interactions";
  if (protocol.startsWith("openrouter_")) return "openrouter";
  try {
    const host = new URL(baseURL).hostname.toLowerCase().replace(/\.$/, "");
    if (host === "api.anthropic.com" && protocol === "anthropic_messages") return "anthropic";
    if (host === "openrouter.ai") return "openrouter";
    if (host === "api.openai.com" && ["", "openai_responses", "openai_chat_completions"].includes(protocol)) return "openai";
  } catch { /* Invalid URLs use the compatible channel budget. */ }
  return "compatible";
}

export function resolveUpstreamInputLimitProtocol(upstream: { compatible: string; protocolDefaultsJSON: string }): string {
  try {
    const defaults: unknown = JSON.parse(upstream.protocolDefaultsJSON);
    if (isRecord(defaults) && typeof defaults.chat === "string" && INPUT_LIMIT_PROTOCOLS.some((protocol) => protocol === defaults.chat)) return defaults.chat;
  } catch { /* An unset chat protocol uses the upstream compatibility default. */ }
  switch (upstream.compatible) {
    case "google": return "google_generate_content";
    case "anthropic": return "anthropic_messages";
    case "openrouter": return "openrouter_chat_completions";
    case "xai": return "xai_responses";
    default: return "openai_responses";
  }
}

export function resolveInputLimitValues(config: InputLimitsConfig, profile: InputLimitProfile, upstream: string, protocol = ""): InputLimitValues {
  const values: InputLimitValues = {};
  for (const { key } of INPUT_LIMIT_FIELDS) {
    values[key] = config.upstreams[upstream]?.protocols?.[protocol]?.[key] ?? config.upstreams[upstream]?.[key] ?? config.profiles[profile]?.[key];
  }
  return values;
}

export function resolveInheritedInputLimit(config: InputLimitsConfig, defaults: InputLimitsConfig | null, profile: InputLimitProfile, upstream: string, key: InputLimitKey, protocol = ""): number | null | undefined {
  const system = defaults?.profiles[profile]?.[key];
  if (!upstream) return system;
  const inheritedProfile = config.profiles[profile]?.[key] ?? system;
  return protocol ? config.upstreams[upstream]?.[key] ?? inheritedProfile : inheritedProfile;
}

export function setInputLimit(config: InputLimitsConfig, profile: InputLimitProfile, upstream: string, key: InputLimitKey, value: number | null, protocol = ""): InputLimitsConfig {
  if (upstream) {
    if (protocol) {
      const channel = config.upstreams[upstream] ?? {};
      const values = { ...channel.protocols?.[protocol], [key]: value };
      if (value === null) delete values[key];
      const protocols = { ...channel.protocols, [protocol]: values };
      if (Object.keys(values).length === 0) delete protocols[protocol];
      const nextChannel: InputLimitUpstream = { ...channel, protocols };
      if (Object.keys(protocols).length === 0) delete nextChannel.protocols;
      const upstreams = { ...config.upstreams, [upstream]: nextChannel };
      if (Object.keys(nextChannel).length === 0) delete upstreams[upstream];
      return { ...config, upstreams };
    }
    const values = { ...config.upstreams[upstream], [key]: value };
    if (value === null) delete values[key];
    const upstreams = { ...config.upstreams, [upstream]: values };
    if (Object.keys(values).length === 0) delete upstreams[upstream];
    return { ...config, upstreams };
  }
  return { ...config, profiles: { ...config.profiles, [profile]: { ...config.profiles[profile], [key]: value } } };
}
