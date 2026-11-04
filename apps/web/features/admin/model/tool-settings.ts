import type { PatchSettingItem, SettingsGrouped } from "@/shared/api/settings-types";
import { denormalizeMBValue, normalizeMBValue } from "@/features/admin/model/files-size-values";

export type ToolSettingsFieldType = "int" | "bool" | "textarea";

export type ToolSettingsField = {
  namespace: "mcp" | "file";
  key:
    | "mcp_enable"
    | "mcp_tool_timeout_seconds"
    | "mcp_tool_retry_count"
    | "mcp_max_concurrent_calls"
    | "mcp_max_selected_tools_per_message"
    | "mcp_max_llm_calls_per_run"
    | "mcp_max_tool_calls_per_run"
    | "mcp_tool_prompt"
    | "image_attachment_processor_max_bytes"
    | "image_attachment_analysis_max_chars"
    | "image_attachment_analysis_total_max_chars";
  labelKey: string;
  descriptionKey: string;
  type: ToolSettingsFieldType;
  placeholder?: string;
  placeholderKey?: string;
  valueUnit?: "mb";
  zeroDisplayLabelKey?: string;
};

export const TOOL_SETTINGS_FIELDS: ToolSettingsField[] = [
  {
    namespace: "mcp",
    key: "mcp_enable",
    labelKey: "mcpEnable.label",
    descriptionKey: "mcpEnable.description",
    type: "bool",
  },
  {
    namespace: "mcp",
    key: "mcp_tool_prompt",
    labelKey: "toolPrompt.label",
    descriptionKey: "toolPrompt.description",
    type: "textarea",
    placeholderKey: "defaultPromptPlaceholder",
  },
  {
    namespace: "mcp",
    key: "mcp_max_selected_tools_per_message",
    labelKey: "maxSelectedTools.label",
    descriptionKey: "maxSelectedTools.description",
    type: "int",
    placeholder: "32",
  },
  {
    namespace: "mcp",
    key: "mcp_max_llm_calls_per_run",
    labelKey: "maxLLMCalls.label",
    descriptionKey: "maxLLMCalls.description",
    type: "int",
    placeholder: "5",
  },
  {
    namespace: "mcp",
    key: "mcp_max_tool_calls_per_run",
    labelKey: "maxToolCalls.label",
    descriptionKey: "maxToolCalls.description",
    type: "int",
    placeholder: "8",
  },
  {
    namespace: "mcp",
    key: "mcp_max_concurrent_calls",
    labelKey: "maxConcurrentCalls.label",
    descriptionKey: "maxConcurrentCalls.description",
    type: "int",
    placeholder: "8",
  },
  {
    namespace: "mcp",
    key: "mcp_tool_timeout_seconds",
    labelKey: "toolTimeout.label",
    descriptionKey: "toolTimeout.description",
    type: "int",
    placeholder: "10",
  },
  {
    namespace: "mcp",
    key: "mcp_tool_retry_count",
    labelKey: "toolRetry.label",
    descriptionKey: "toolRetry.description",
    type: "int",
    placeholder: "0",
  },
  { namespace: "file", key: "image_attachment_processor_max_bytes", labelKey: "imageInputLimit.label", descriptionKey: "imageInputLimit.description", type: "int", valueUnit: "mb", zeroDisplayLabelKey: "unlimited" },
  { namespace: "file", key: "image_attachment_analysis_max_chars", labelKey: "imageAnalysisLimit.label", descriptionKey: "imageAnalysisLimit.description", type: "int", zeroDisplayLabelKey: "unlimited" },
  { namespace: "file", key: "image_attachment_analysis_total_max_chars", labelKey: "imageAnalysisTotalLimit.label", descriptionKey: "imageAnalysisTotalLimit.description", type: "int", zeroDisplayLabelKey: "unlimited" },
];

export function toolFieldID(field: ToolSettingsField): string {
  return `${field.namespace}.${field.key}`;
}

export function flattenToolSettings(grouped: SettingsGrouped): Record<string, string> {
  const result: Record<string, string> = {};
  for (const field of TOOL_SETTINGS_FIELDS) {
    const item = grouped[field.namespace]?.find((entry) => entry.key === field.key);
    const value = item?.value ?? "";
    result[toolFieldID(field)] = field.valueUnit === "mb" ? normalizeMBValue(value) : value;
  }
  return applyToolSettingsDefaults(result);
}

export function buildToolSettingsPatch(settings: Record<string, string>, dirtyFieldIDs: ReadonlySet<string>): PatchSettingItem[] {
  return TOOL_SETTINGS_FIELDS.filter((field) => dirtyFieldIDs.has(toolFieldID(field))).map((field) => ({
    namespace: field.namespace,
    key: field.key,
    value: field.valueUnit === "mb" ? denormalizeMBValue(settings[toolFieldID(field)] ?? "") : settings[toolFieldID(field)] ?? "",
  }));
}

export function applyToolSettingsDefaults(settings: Record<string, string>): Record<string, string> {
  return {
    ...settings,
    "mcp.mcp_enable": settings["mcp.mcp_enable"] || "false",
    "mcp.mcp_tool_prompt": settings["mcp.mcp_tool_prompt"] ?? "",
    "mcp.mcp_max_selected_tools_per_message": settings["mcp.mcp_max_selected_tools_per_message"] || "32",
    "mcp.mcp_max_llm_calls_per_run": settings["mcp.mcp_max_llm_calls_per_run"] || "5",
    "mcp.mcp_max_tool_calls_per_run": settings["mcp.mcp_max_tool_calls_per_run"] || "8",
    "mcp.mcp_max_concurrent_calls": settings["mcp.mcp_max_concurrent_calls"] || "8",
    "mcp.mcp_tool_timeout_seconds": settings["mcp.mcp_tool_timeout_seconds"] || "10",
    "mcp.mcp_tool_retry_count": settings["mcp.mcp_tool_retry_count"] || "0",
  };
}

export function toToolEditorField(
  field: ToolSettingsField,
  translate: (key: string) => string,
) {
  return {
    id: toolFieldID(field),
    label: translate(field.labelKey),
    description: translate(field.descriptionKey),
    type: field.type,
    placeholder: field.placeholderKey ? translate(field.placeholderKey) : field.placeholder,
    valueUnit: field.valueUnit,
    valueSuffix: field.valueUnit === "mb" ? "MB" : undefined,
    zeroDisplayLabel: field.zeroDisplayLabelKey ? translate(field.zeroDisplayLabelKey) : undefined,
  } as const;
}
