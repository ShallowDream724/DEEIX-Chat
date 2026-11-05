import assert from "node:assert/strict";
import test from "node:test";
import { parseToolPayload, prepareToolArguments, resolveToolResultCategory } from "./tool-result-presentation.ts";

test("server code, shell commands and activity records remain readable without tool output", () => {
  for (const [key, text, type, category] of [
    ["code", "import math\nprint(math.pi)", "code_interpreter", "code_execution"],
    ["command", "python report.py", "shell", "shell"],
    ["record", "Searching for the source document", "web_search", "web_search"],
  ]) {
    const input = parseToolPayload(JSON.stringify({ [key]: text, provider: "chat01" }));
    const presentation = prepareToolArguments(input);
    assert.equal(presentation.raw, "");
    assert.equal(presentation.arguments.find((argument) => argument.key === key)?.text, text);
    assert.equal(resolveToolResultCategory({ type, input }), category);
  }
});

test("truncated input previews remain readable instead of disappearing when no JSON object is available", () => {
  const preview = '{"code":"import math\\nprint(math.pi)...';
  assert.deepEqual(prepareToolArguments(parseToolPayload(preview)), { raw: preview, arguments: [] });
  assert.deepEqual(prepareToolArguments(parseToolPayload("")), { raw: "", arguments: [] });
});
