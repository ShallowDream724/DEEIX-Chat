import assert from "node:assert/strict";
import test from "node:test";
import { isChat01ToolInput, parseToolPayload, prepareToolArguments, resolveToolResultCategory, toolPresentationInput } from "./tool-result-presentation.ts";

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

test("Chat01 presentation keeps real arguments and unwraps legacy activity records", () => {
  assert.deepEqual(toolPresentationInput({ provider: "chat01", command: "python report.py" }), { command: "python report.py" });
  assert.deepEqual(toolPresentationInput({ provider: "chat01", record: '{"uri":"skills://pdfs","start_line":1}' }), { uri: "skills://pdfs", start_line: 1 });
  assert.equal(toolPresentationInput({ provider: "chat01", record: "**Used pdfs skill**" }), "**Used pdfs skill**");
  assert.deepEqual(toolPresentationInput({ provider: "chat01", code: "print(1)" }), { code: "print(1)" });
  assert.deepEqual(toolPresentationInput({ command: "pwd", provider: "chat01" }, "chat01:call"), { command: "pwd" });
});

test("Chat01 filtering does not change other providers or erase raw previews", () => {
  const ordinaryInput = { provider: "custom", record: '{"uri":"skills://pdfs"}', command: "pwd" };
  assert.equal(toolPresentationInput(ordinaryInput), ordinaryInput);
  assert.equal(isChat01ToolInput(ordinaryInput), false);
  assert.equal(isChat01ToolInput(null, "chat01:call"), true);
  const preview = '{"command":"python report.py...';
  assert.equal(toolPresentationInput(preview, "chat01:call"), preview);
});
