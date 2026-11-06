import type { ModelOptionControl } from "@/features/chat/types/chat-runtime";
import type { ConversationOptions } from "@/shared/api/conversation-types";

export const IMAGE_OPTION_NUMBER_RANGES: Record<string, { min: number; max: number; step: number; default?: number }> = {
  frame_count: { min: 2, max: 16, step: 1, default: 9 },
  frame_duration_ms: { min: 20, max: 2000, step: 10, default: 200 },
  output_compression: { min: 0, max: 100, step: 1 },
};

const IMAGE_26_CONTROLS: ModelOptionControl[] = [
  { path: "output_format", type: "select", options: ["png", "jpeg", "webp", "gif"] },
  { path: "quality", type: "select", options: ["auto", "low", "medium", "high", "xhigh", "max"] },
  { path: "complexity", type: "select", options: ["low", "medium", "high"] },
  { path: "frame_count", type: "number" },
  { path: "frame_duration_ms", type: "number" },
  { path: "output_compression", type: "number" },
  { path: "moderation", type: "select", options: ["auto", "low"] },
];

export function isOpenAIImageProtocol(protocols: readonly string[]): boolean {
  return protocols.some((protocol) => protocol === "openai_image_generations" || protocol === "openai_image_edits");
}

/** Explicit capability choices also cover platform aliases whose name differs from the upstream. */
export function imageOptionControls(
  controls: readonly ModelOptionControl[],
  protocols: readonly string[],
  modelName: string,
): ModelOptionControl[] {
  if (!isOpenAIImageProtocol(protocols) || !/^gpt-image-2\.6(?:-(?:sunburst|flare))?(?:-\d{4}-\d{2}-\d{2})?$/i.test(modelName.trim())) return [...controls];
  const declared = new Set(controls.map((control) => control.path));
  return [
    ...controls,
    ...IMAGE_26_CONTROLS.filter((control) => !declared.has(control.path)),
  ];
}

export function isImageOptionVisible(
  path: string,
  options: ConversationOptions,
  protocols: readonly string[],
  defaultOptions: ConversationOptions = {},
): boolean {
  if (!isOpenAIImageProtocol(protocols)) return true;
  const effectiveOptions = { ...defaultOptions, ...options };
  const format = typeof effectiveOptions.output_format === "string" ? effectiveOptions.output_format.toLowerCase() : "png";
  if (path === "frame_count" || path === "frame_duration_ms") return format === "gif";
  if (path === "output_compression") return format === "jpeg" || format === "webp";
  if (format === "gif" && ["stream", "partial_images", "mask", "input_fidelity"].includes(path)) return false;
  return true;
}

export function imageOptionDefaults(options: ConversationOptions, controls: readonly ModelOptionControl[]): ConversationOptions {
  if (!controls.some((control) => control.path === "output_format" && control.options?.includes("gif"))) return options;
  const fallbackDefaults: ConversationOptions = {};
  if (controls.some((control) => control.path === "output_format" && control.options?.includes("png"))) fallbackDefaults.output_format = "png";
  if (controls.some((control) => control.path === "quality" && control.options?.includes("auto"))) fallbackDefaults.quality = "auto";
  return { ...fallbackDefaults, ...options };
}
