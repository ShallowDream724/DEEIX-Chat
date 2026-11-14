import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) return nextResolve(new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, context);
  return nextResolve(specifier, context);
} });
const { configurableInputModalities, modelInputModalitiesOverride, setModelInputModalitiesInCapabilities } = await import("./model-input-modalities.ts");

test("DeepSeek adds document after the existing input choices and keeps Chat01 attachments separate", () => {
  assert.deepEqual(configurableInputModalities({}), ["image", "pdf", "audio", "video"]);
  assert.deepEqual(configurableInputModalities({ documentInput: true }), ["image", "pdf", "audio", "video", "document"]);
  assert.deepEqual(configurableInputModalities({ attachmentInput: true }), ["image", "attachment"]);
});

test("document and PDF overrides are independent and preserve existing native file policy", () => {
  const original = JSON.stringify({ inputModalities: ["text", "pdf"], nativeFiles: { provider: "deepseek", spreadsheet: "local" } });
  const documentOnly = setModelInputModalitiesInCapabilities(original, ["document"]);
  assert.deepEqual(modelInputModalitiesOverride(documentOnly), ["text", "document"]);
  assert.deepEqual(JSON.parse(documentOnly).nativeFiles, JSON.parse(original).nativeFiles);
  assert.deepEqual(modelInputModalitiesOverride(setModelInputModalitiesInCapabilities(documentOnly, ["pdf"])), ["text", "pdf"]);
  assert.deepEqual(modelInputModalitiesOverride('{"inputModalities":["text","pdf","attachment"]}'), ["text", "pdf", "attachment"]);
});
