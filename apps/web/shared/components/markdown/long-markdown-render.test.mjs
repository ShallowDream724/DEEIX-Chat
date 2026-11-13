import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { Script } from "node:vm";
import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { transformSync } from "next/dist/build/swc/index.js";
import * as streamdown from "streamdown";
import * as model from "./long-markdown-model.ts";
import * as anchor from "./long-markdown-anchor.ts";

const nodeRequire = createRequire(import.meta.url);
function compile(name, dependencies) {
  const source = readFileSync(new URL(name, import.meta.url), "utf8");
  const compiled = transformSync(source, {
    jsc: { parser: { syntax: "typescript", tsx: true }, transform: { react: { runtime: "automatic" } }, target: "es2022" },
    module: { type: "commonjs" },
  }).code;
  const adapter = { exports: {} };
  new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
    (dependency) => {
      if (dependency in dependencies) return dependencies[dependency];
      if (["react", "react/jsx-runtime"].includes(dependency)) return nodeRequire(dependency);
      throw new Error(`Unexpected dependency: ${dependency}`);
    }, adapter, adapter.exports,
  );
  return adapter.exports;
}
const state = compile("./markdown-view-state.tsx", {});
const renderer = compile("./long-markdown-render.tsx", {
  streamdown,
  "next-intl": { useTranslations: () => (key) => key },
  "./adaptive-markdown-table": {
    MarkdownTableSourceContext: React.createContext(null),
    MarkdownTableStreamingContext: React.createContext(false),
  },
  "./long-markdown-model": model,
  "./long-markdown-anchor": anchor,
  "./markdown-view-state": state,
});
function render(source, store = state.createMarkdownViewStateStore(), extra = {}) {
  return renderToStaticMarkup(React.createElement(state.MarkdownViewStateProvider, { store },
    React.createElement(renderer.LongMarkdownStreamdown, {
      mode: "static", isAnimating: false, parseIncompleteMarkdown: false,
      linkSafety: { enabled: false }, viewKey: "markdown-0", ...extra,
    }, source),
  ));
}
const tableSource = (count, header = "Value") => `| ${header} |\n| --- |\n${Array.from({ length: count }, (_, index) => `| VALUE_${index + 1} |`).join("\n")}`;

test("real Streamdown renders bounded rows and the selected page survives a message subtree remount", () => {
  const source = tableSource(2400);
  const table = model.parseLargeMarkdownTable(source);
  const store = state.createMarkdownViewStateStore();
  store.setPage("markdown-0:0", model.markdownTableViewShape(table), 7);
  for (let remount = 0; remount < 2; remount += 1) {
    const html = render(source, store);
    assert.equal((html.match(/<tr/g) ?? []).length, 101);
    assert.ok(html.includes("VALUE_701<"));
    assert.ok(html.includes("VALUE_800<"));
    assert.ok(!html.includes("VALUE_1<"));
    // Ending a render destroys every React hook instance; only the shell's store survives.
    renderToStaticMarkup(React.createElement("div"));
  }
  const shortened = render(tableSource(350), store);
  assert.ok(shortened.includes("VALUE_301<"));
  const replaced = render(tableSource(2400, "Different table"), store);
  assert.ok(replaced.includes("VALUE_1<"));
});

test("thinking and separate Markdown segments never share table page selections", () => {
  const source = tableSource(2400);
  const table = model.parseLargeMarkdownTable(source);
  const store = state.createMarkdownViewStateStore();
  store.setPage("thinking:0", model.markdownTableViewShape(table), 5);
  assert.ok(render(source, store, { viewKey: "thinking" }).includes("VALUE_501<"));
  assert.ok(render(source, store).includes("VALUE_1<"));
  assert.ok(render(source, store, { viewKey: "markdown-1" }).includes("VALUE_1<"));
});

test("real parser keeps distant headings and active prose while leaving offscreen body unparsed", () => {
  const source = Array.from({ length: 600 }, (_, index) => `Paragraph ${index} with **formatted content**.`).join("\n\n");
  const html = render(`${source}\n\n## Distant heading\n\nSetext heading\n===`);
  assert.equal((html.match(/<p[ >]/g) ?? []).length, 8);
  assert.ok(html.includes("Distant heading"));
  assert.ok(html.includes("Setext heading"));
  const active = render(`${source}\n\nACTIVE TAIL`, undefined, {
    mode: "streaming", isAnimating: true, parseIncompleteMarkdown: true,
  });
  assert.ok(active.includes("ACTIVE TAIL"));
});

test("view-state keys retain only bounded structural selections", () => {
  const store = state.createMarkdownViewStateStore();
  for (let index = 0; index < 70; index += 1) store.setPage(String(index), "shape", index);
  assert.equal(store.getPage("0", "shape"), null);
  assert.equal(store.getPage("69", "shape"), 69);
  assert.equal(store.getPage("69", "changed shape"), null);
});
