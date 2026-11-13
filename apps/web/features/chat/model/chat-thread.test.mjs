import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const source = readFileSync(new URL("./chat-thread.ts", import.meta.url), "utf8");
const compiled = transformSync(source, {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" }, module: { type: "commonjs" },
}).code;
const adapter = { exports: {} };
new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
  () => ({}), adapter, adapter.exports,
);
const { buildVisibleMessages, reconcileBranchSelections } = adapter.exports;
const message = (publicID, parentPublicID, role = "assistant") => ({
  publicID, parentPublicID, role, key: publicID, content: "Long message paragraph. ".repeat(10_000),
});
const ids = (messages) => messages.map((item) => item.publicID);

test("tail pages honor sibling selection while their shared parent and root are missing", () => {
  const messages = [message("a", "missing"), message("a-user", "a", "user"), message("a-leaf", "a-user"), message("b", "missing")];
  assert.deepEqual(ids(buildVisibleMessages(messages, {})), ["b"]);
  const selected = buildVisibleMessages(messages, { missing: "a" });
  assert.deepEqual(ids(selected), ["a", "a-user", "a-leaf"]);
  assert.deepEqual(selected[0].branchNavigator, { parentPublicID: "missing", index: 1, total: 2, canPrevious: false, canNext: true });
  assert.deepEqual(ids(buildVisibleMessages(messages, { missing: "b" })), ["b"]);
});

test("nested branch selections in an incomplete page survive rapid changes and streaming updates", () => {
  const messages = [message("user", "missing", "user"), message("a", "user"), message("a-child", "a", "user"), message("b", "user")];
  let selected = reconcileBranchSelections(messages, {});
  for (let index = 0; index < 20; index += 1) {
    selected = { ...selected, user: index % 2 ? "b" : "a" };
    const updated = messages.map((item) => item.publicID === "b" ? { ...item, isStreaming: true, content: `${item.content}delta` } : item);
    assert.deepEqual(ids(buildVisibleMessages(updated, selected)), index % 2 ? ["user", "b"] : ["user", "a", "a-child"]);
  }
});

test("loading missing ancestors preserves the chosen branch and yields its complete path", () => {
  const tail = [message("a", "parent"), message("b", "parent")];
  const selected = reconcileBranchSelections(tail, { parent: "a" });
  assert.deepEqual(ids(buildVisibleMessages(tail, selected)), ["a"]);
  const full = [message("root", null, "user"), message("parent", "root", "user"), ...tail];
  assert.deepEqual(ids(buildVisibleMessages(full, reconcileBranchSelections(full, selected))), ["root", "parent", "a"]);
});

test("complete trees still follow the latest leaf's ancestry and retain explicit choices", () => {
  const messages = [message("a", null, "user"), message("b", null, "user"), message("b-leaf", "b"), message("a-leaf", "a")];
  assert.deepEqual(ids(buildVisibleMessages(messages, {})), ["a", "a-leaf"]);
  assert.deepEqual(ids(buildVisibleMessages(messages, { __root__: "b" })), ["b", "b-leaf"]);
});

test("malformed cyclic ancestry stays bounded", () => {
  const messages = [message("a", "b"), message("b", "a")];
  assert.equal(buildVisibleMessages(messages, {}).length, 2);
});
