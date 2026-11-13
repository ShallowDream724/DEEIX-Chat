import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import test from "node:test";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "@/shared/config/branding") return { url: "data:text/javascript,export const getBrandingSnapshot=()=>({});", shortCircuit: true };
    if (specifier.startsWith("@/")) return { url: new URL(`../../../${specifier.slice(2)}.ts`, import.meta.url).href, shortCircuit: true };
    return nextResolve(specifier, context);
  },
});
const { createMessageArtifactSelector } = await import("./chat-artifacts.ts");
const message = (id, content = "```html\n<h1>Complete original source</h1>\n```", more = {}) => ({
  key: id, publicID: id, role: "assistant", content, ...more,
});

test("streaming updates reuse unchanged history even when DTO objects are rebuilt", () => {
  const select = createMessageArtifactSelector();
  const history = Array.from({ length: 300 }, (_, i) => message(`history-${i}`));
  const first = select([...history, message("live", "```html\n<h1>first", { isStreaming: true })]);
  const next = select([...history.map((row) => ({ ...row })), message("live", "```html\n<h1>second", { isStreaming: true })]);
  for (let i = 0; i < 300; i++) assert.equal(next[i], first[i]);
  assert.notEqual(next[300], first[300]);
  assert.match(next[300].code, /second/);
});

test("completion and persisted message identity refresh artifact metadata", () => {
  const select = createMessageArtifactSelector();
  const first = select([message("live", undefined, { publicID: "pending", runID: "run", isStreaming: true })]);
  const next = select([message("live", undefined, { publicID: "saved", runID: "run", isStreaming: false })]);
  assert.notEqual(first[0], next[0]);
  assert.equal(next[0].messageID, "saved");
  assert.equal(next[0].streaming, false);
});

test("removed messages leave the cache instead of retaining past conversations", () => {
  const select = createMessageArtifactSelector();
  const original = message("old");
  const first = select([original])[0];
  assert.deepEqual(select([]), []);
  assert.notEqual(select([original])[0], first);
});
