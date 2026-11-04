import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) return nextResolve(new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, context);
  return nextResolve(specifier, context);
} });
const { flattenToolSettings, buildToolSettingsPatch } = await import("./tool-settings.ts");

test("MCP settings load both namespaces and preserve exact image bytes when saving changed fields", () => {
  const settings = flattenToolSettings({ mcp: [{ key: "mcp_tool_retry_count", value: "2" }], file: [
    { key: "image_attachment_processor_max_bytes", value: "20971520" },
    { key: "image_attachment_analysis_max_chars", value: "0" },
    { key: "image_attachment_analysis_total_max_chars", value: "16000" },
    { key: "image_max_dimension", value: "3072" },
  ] });
  assert.equal(settings["file.image_attachment_processor_max_bytes"], "20.97152");
  assert.equal(settings["file.image_attachment_analysis_max_chars"], "0");
  assert.equal(settings["file.image_max_dimension"], undefined);
  assert.deepEqual(buildToolSettingsPatch(settings, new Set()), []);
  assert.deepEqual(buildToolSettingsPatch(settings, new Set(["mcp.mcp_tool_retry_count", "file.image_attachment_processor_max_bytes", "file.image_attachment_analysis_max_chars"])), [
    { namespace: "mcp", key: "mcp_tool_retry_count", value: "2" },
    { namespace: "file", key: "image_attachment_processor_max_bytes", value: "20971520" },
    { namespace: "file", key: "image_attachment_analysis_max_chars", value: "0" },
  ]);
});
