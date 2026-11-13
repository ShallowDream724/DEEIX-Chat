import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { Script } from "node:vm";
import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { transformSync } from "next/dist/build/swc/index.js";
import * as budget from "./code-block-budget.ts";

test("highlighting budgets keep ordinary snippets and exact limits highlighted", () => {
  assert.equal(budget.exceedsCodeBlockHighlightBudget(""), false);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("const answer = 42;\n"), false);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS)), false);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\n".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES)), false);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\r\n".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES)), false);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\r".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES)), false);
});

test("dense code and tall listings independently bypass highlighting", () => {
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS + 1)), true);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\n".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES + 1)), true);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\r\n".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES + 1)), true);
  assert.equal(budget.exceedsCodeBlockHighlightBudget("a\r".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_LINES + 1)), true);
});

// Compile the real adapter with lightweight UI boundaries. A recording child
// represents Streamdown's expensive code renderer, so these assertions catch
// regressions that merely hide its output after mounting it.
const nodeRequire = createRequire(import.meta.url);
const passThrough = ({ children }) => React.createElement(React.Fragment, null, children);
const inert = () => null;
const icon = () => React.createElement("svg");
const moduleStubs = {
  "lucide-react": { CornerUpLeft: icon, Download: icon, Eye: icon, Maximize2: icon, WandSparkles: icon },
  "next-intl": { useTranslations: () => (key) => key },
  sonner: { toast: { error: inert, success: inert } },
  "@/components/ui/button": { Button: passThrough },
  "@/components/ui/dialog": Object.fromEntries(["Dialog", "DialogContent", "DialogDescription", "DialogFooter", "DialogHeader", "DialogTitle"].map((key) => [key, passThrough])),
  "@/components/ui/input": { Input: inert },
  "@/components/ui/tooltip": { Tooltip: passThrough, TooltipContent: inert, TooltipTrigger: passThrough },
  "@/lib/utils": { cn: (...values) => values.filter(Boolean).join(" ") },
  "@/shared/components/copy-action": { CopyActionButton: ({ value, "aria-label": label }) => React.createElement("button", { type: "button", "aria-label": label, "data-copy-value": value }) },
  "@/shared/components/media-action-bar": {},
  "@/shared/lib/artifact-preview": { resolveArtifactPreviewKind: (language) => language === "html" ? "html" : null },
  "@/shared/hooks/use-image-display-source": {},
  "@/shared/lib/markdown-image-source": {},
  "./adaptive-markdown-table": { MarkdownTableStreamingContext: React.createContext(false) },
  "./code-block-budget": budget,
  "./streamdown-html": { MarkdownFootnotesContext: React.createContext(false) },
  "./streamdown-icons": { StreamdownCheckIcon: icon, StreamdownCopyIcon: icon },
  "./streamdown-mermaid": { MarkdownMermaidLoading: inert, MarkdownMermaidPendingContext: React.createContext(false) },
  "./streamdown-style": {},
  "@deeix/core": { UI_WIDGET_FENCE_LANGUAGE: "ui-widget" },
  "./ui-blocks": {
    UI_BLOCK_FENCE_LANGUAGE: "ui-block",
    UIBlockHost: () => React.createElement("div", { "data-ui-host": "block" }),
    MarkdownWidgetHost: () => React.createElement("div", { "data-ui-host": "widget" }),
  },
};
const source = readFileSync(new URL("./streamdown-components.tsx", import.meta.url), "utf8");
const compiled = transformSync(source, { jsc: { parser: { syntax: "typescript", tsx: true }, transform: { react: { runtime: "automatic" } }, target: "es2022" }, module: { type: "commonjs" } }).code;
const adapterModule = { exports: {} };
new Script(`(function(require, module, exports) { ${compiled}\n})`).runInThisContext()(
  (name) => {
    if (name in moduleStubs) return moduleStubs[name];
    if (name === "react" || name === "react/jsx-runtime") return nodeRequire(name);
    throw new Error(`Unexpected adapter dependency: ${name}`);
  },
  adapterModule,
  adapterModule.exports,
);
const { MarkdownCodePre, MarkdownArtifactActionsContext } = adapterModule.exports;
const HighlightCounterContext = React.createContext(null);

function StreamdownCode({ children }) {
  const counter = React.useContext(HighlightCounterContext);
  counter.mounts += 1;
  return React.createElement("div", { "data-highlighted": "true" }, children);
}

function renderCode(code, language = "javascript", { preview = false } = {}) {
  const counter = { mounts: 0 };
  const child = React.createElement(StreamdownCode, { className: language ? `language-${language}` : undefined }, code);
  const block = React.createElement(MarkdownCodePre, { "data-markdown-source-line": "9" }, child);
  const content = preview
    ? React.createElement(MarkdownArtifactActionsContext.Provider, { value: { onOpenCodeArtifact: inert } }, block)
    : block;
  const markup = renderToStaticMarkup(React.createElement(HighlightCounterContext.Provider, { value: counter }, content));
  return { markup, highlightedChildren: counter.mounts };
}

test("large source renders completely as one escaped code child without mounting the highlighter", () => {
  const code = '<section title="x">&</section>\n'.repeat(605);
  const { markup, highlightedChildren } = renderCode(code, "html", { preview: true });
  assert.equal(highlightedChildren, 0);
  assert.match(markup, /data-markdown-source-line="9"/);
  assert.match(markup, /data-code-block-plain="true"/);
  assert.match(markup, /max-height:22rem/);
  assert.match(markup, /class="overflow-auto"/);
  assert.match(markup, /data-streamdown="code-block-header"[^>]*><span[^>]*>html<\/span>/);
  const escaped = code.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
  assert.ok(markup.includes(`<pre><code>${escaped}</code></pre>`));
  assert.match(markup, /aria-label="copy"/);
  assert.match(markup, /aria-label="openPreview"/);
});

test("short snippets keep Streamdown rendering and unlabeled large fences show text", () => {
  const short = renderCode("const answer = 42;\n");
  assert.equal(short.highlightedChildren, 1);
  assert.doesNotMatch(short.markup, /data-code-block-plain/);
  const unlabeled = renderCode("a".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS + 1), "");
  assert.equal(unlabeled.highlightedChildren, 0);
  assert.match(unlabeled.markup, /data-language="text"/);
});

test("diagram and interactive fences retain their existing render paths at any size", () => {
  const code = "a".repeat(budget.CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS + 1);
  for (const language of ["mermaid", "mmd"]) {
    const result = renderCode(code, language);
    assert.equal(result.highlightedChildren, 1);
    assert.doesNotMatch(result.markup, /data-code-block-plain/);
  }
  for (const [language, host] of [["ui-block", "block"], ["ui-widget", "widget"]]) {
    const result = renderCode(code, language);
    assert.equal(result.highlightedChildren, 0);
    assert.match(result.markup, new RegExp(`data-ui-host="${host}"`));
    assert.doesNotMatch(result.markup, /data-code-block-plain/);
  }
});
