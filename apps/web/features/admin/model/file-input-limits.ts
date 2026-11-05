export const INPUT_LIMIT_FIELDS = [
	{ key: "messageFileCount", unit: "count", group: "request" },
  { key: "attachmentCount", unit: "count", group: "request" },
  { key: "attachmentSingleBytes", unit: "mb", group: "request" },
  { key: "attachmentBytes", unit: "mb", group: "request" },
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
export type InputLimitsConfig = {
  defaults: InputLimitValues;
  upstreams: Record<string, InputLimitValues>;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseInputLimits(raw: string): InputLimitsConfig | null {
  try {
    const config: unknown = JSON.parse(raw);
    if (!isRecord(config) || !isRecord(config.defaults) || !isRecord(config.upstreams)) return null;
    if (Object.keys(config).some((key) => key !== "defaults" && key !== "upstreams")) return null;
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
    const defaults = parseValues(config.defaults);
    if (!defaults) return null;
    const upstreams: InputLimitsConfig["upstreams"] = {};
    for (const [id, upstream] of Object.entries(config.upstreams)) {
      if (!/^[1-9]\d*$/.test(id)) return null;
      const values = parseValues(upstream);
      if (!values) return null;
      upstreams[id] = values;
    }
    return { defaults, upstreams };
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

export function resolveInputLimitValues(config: InputLimitsConfig, upstream: string): InputLimitValues {
  const values: InputLimitValues = {};
  for (const { key } of INPUT_LIMIT_FIELDS) {
    values[key] = config.upstreams[upstream]?.[key] ?? config.defaults[key];
  }
  return values;
}

export function resolveInheritedInputLimit(config: InputLimitsConfig, systemDefaults: InputLimitsConfig | null, upstream: string, key: InputLimitKey): number | null | undefined {
  const system = systemDefaults?.defaults[key];
  return upstream ? config.defaults[key] ?? system : system;
}

export function setInputLimit(config: InputLimitsConfig, upstream: string, key: InputLimitKey, value: number | null): InputLimitsConfig {
  const values = { ...(upstream ? config.upstreams[upstream] : config.defaults), [key]: value };
  if (value === null) delete values[key];
  if (!upstream) return { ...config, defaults: values };
  const upstreams = { ...config.upstreams, [upstream]: values };
  if (Object.keys(values).length === 0) delete upstreams[upstream];
  return { ...config, upstreams };
}
