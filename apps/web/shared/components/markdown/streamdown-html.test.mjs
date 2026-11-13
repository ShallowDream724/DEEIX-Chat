import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { Script } from "node:vm";
import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { transformSync } from "next/dist/build/swc/index.js";

const require = createRequire(import.meta.url);
let clones = 0;
const stubs = {
  react: { ...React, cloneElement: (...args) => { clones += 1; return React.cloneElement(...args); } },
  "react/jsx-runtime": require("react/jsx-runtime"),
  "@/lib/utils": { cn: (...values) => values.filter(Boolean).join(" ") },
  "@/shared/lib/type-guards": { isOneOf: (values) => (value) => values.includes(value) },
  "./streamdown-content": { containsMarkdownMath: (source) => source.includes("$x$") },
  "./streamdown-style": { sanitizeHTMLStyle: (style) => style, sanitizeKatexHTMLStyle: (style) => style },
};
const source = readFileSync(new URL("./streamdown-html.tsx", import.meta.url), "utf8");
const compiled = transformSync(source, {
  jsc: { parser: { syntax: "typescript", tsx: true }, transform: { react: { runtime: "automatic" } }, target: "es2022" },
  module: { type: "commonjs" },
}).code;
const htmlModule = { exports: {} };
new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
  (name) => {
    assert.ok(name in stubs, `Unexpected HTML dependency: ${name}`);
    return stubs[name];
  },
  htmlModule,
  htmlModule.exports,
);
const html = htmlModule.exports;
const boundaries = [html.MarkdownHTMLDiv, html.MarkdownHTMLSection, html.MarkdownHTMLArticle, html.MarkdownHTMLAside, html.MarkdownHTMLMain, html.MarkdownHTMLDetails];

test("nested HTML adapters preserve strong text through native elements, fragments and inline spans", () => {
  const inner = React.createElement(html.MarkdownHTMLDiv, null,
    React.createElement(React.Fragment, null,
      React.createElement(html.MarkdownHTMLSpan, null, "**inside span**"),
      React.createElement("em", null, "__inside native element__")));
  const markup = renderToStaticMarkup(React.createElement(html.MarkdownHTMLSection, null, "**outside**", inner));
  for (const text of ["outside", "inside span", "inside native element"]) {
    assert.match(markup, new RegExp(`<strong[^>]*>${text}</strong>`));
  }
  assert.doesNotMatch(markup, /\*\*|__/);
});

test("nested HTML adapters retain each child's inline math renderer and KaTeX spans", () => {
  const mathRenderer = (source) => React.createElement("math", { "data-source": source }, source);
  const child = React.createElement(html.MarkdownHTMLArticle, null, "**formula** $x$");
  const katex = React.createElement(html.MarkdownHTMLSpan, { className: "katex", style: { top: "1em" } },
    React.createElement("span", { className: "mord" }, "x"));
  const tree = React.createElement(html.MarkdownHTMLInlineRendererContext.Provider, { value: mathRenderer },
    React.createElement(html.MarkdownHTMLDiv, null, child, katex));
  const markup = renderToStaticMarkup(tree);
  assert.match(markup, /<math data-source="\*\*formula\*\* \$x\$">/);
  assert.match(markup, /class="katex" style="top:1em"/);
  assert.match(markup, /<span class="mord">x<\/span>/);
});

test("deep HTML normalizes each native descendant once across adapter boundaries", () => {
  for (const depth of [30, 60, 120]) {
    let tree = null;
    for (let index = 0; index < depth; index += 1) {
      tree = React.createElement(boundaries[index % boundaries.length], null,
        React.createElement("span", null, `**row ${index}**`), tree);
    }
    clones = 0;
    const markup = renderToStaticMarkup(tree);
    assert.equal((markup.match(/<strong /g) ?? []).length, depth);
    assert.equal(clones, depth);
  }
});
