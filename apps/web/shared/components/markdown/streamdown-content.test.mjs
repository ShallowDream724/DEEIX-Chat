import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Script } from "node:vm";
import { transformSync } from "next/dist/build/swc/index.js";

const source = readFileSync(new URL("./streamdown-content.ts", import.meta.url), "utf8");
// Count actual candidate/character visits in the real implementation. This
// checks complexity without depending on the machine's wall clock speed.
const work = { fence: 0, html: 0, latex: 0, visual: 0 };
const instrumented = source
  .replace("if (remainingInspections-- <= 0)", "if ((work.fence++, remainingInspections-- <= 0))")
  .replace("const character = source[end];", "work.html++; const character = source[end];")
  .replace("const end = source.indexOf(closing,", "work.latex++; const end = source.indexOf(closing,")
  .replace("const matching = closers.get(match[3])", "work.visual++; const matching = closers.get(match[3])");
assert.notEqual(instrumented, source);
const compiled = transformSync(instrumented, {
  jsc: { parser: { syntax: "typescript" }, target: "es2022" },
  module: { type: "commonjs" },
}).code;
const contentModule = { exports: {} };
new Script(`(function(require, module, exports, work) { ${compiled}\n})`).runInThisContext()(
  (name) => {
    assert.equal(name, "@/shared/lib/type-guards");
    return { isRecord: (value) => value != null && typeof value === "object" && !Array.isArray(value) };
  },
  contentModule,
  contentModule.exports,
  work,
);
const { normalizeHTMLBlockBlankLines, normalizeHTMLVisualMarkdownFences, normalizeNestedCodeFences, normalizeMathDelimiters, escapeNonMathDollars } = contentModule.exports;

test("HTML blank lines are repaired across complete, nested, sibling and streaming blocks", () => {
  const fixtures = [
    ["before\n\n<div>first\n\nsecond</div>\n\nafter", "before\n\n<div>first\n<!-- -->\nsecond</div>\n\nafter"],
    ["<div>outer\n\n<div>inner\n\ntext</div>\n\nend</div>", "<div>outer\n<!-- -->\n<div>inner\n<!-- -->\ntext</div>\n<!-- -->\nend</div>"],
    ["<section>a\n\nb</section>\n\n<article>c\n\nd</article>", "<section>a\n<!-- -->\nb</section>\n\n<article>c\n<!-- -->\nd</article>"],
    ['<DIV title="a > b">a\r\n \t\r\nb</DIV>', '<DIV title="a > b">a\r\n<!-- -->\r\nb</DIV>'],
    ["<div><!-- </div> -->a\n\nb</div>", "<div><!-- </div> -->a\n<!-- -->\nb</div>"],
    ["<div/>\n\nordinary text", "<div/>\n\nordinary text"],
    ["<div-custom>a\n\nb</div-custom>", "<div-custom>a\n\nb</div-custom>"],
  ];
  for (const [input, expected] of fixtures) assert.equal(normalizeHTMLBlockBlankLines(input), expected);
  const unfinished = "<div>open\n\nstill open\n\n<section>child";
  assert.equal(normalizeHTMLBlockBlankLines(unfinished), unfinished);
  assert.equal(normalizeHTMLBlockBlankLines(unfinished, true), unfinished.replaceAll("\n\n", "\n<!-- -->\n"));
  const unclosedOuter = "<div>open\n\n<div>closed\n\nchild</div>";
  assert.equal(normalizeHTMLBlockBlankLines(unclosedOuter), "<div>open\n\n<div>closed\n<!-- -->\nchild</div>");
});

test("HTML-looking code stays literal with complete, longer and unfinished fences", () => {
  for (const marker of ["```", "~~~~", "````` "]) {
    const code = `${marker.trim()}html\n<div>a\n\nb</div>\n${marker.trim()}`;
    assert.equal(normalizeHTMLBlockBlankLines(code), code);
    const unfinished = `${marker.trim()}html\n<div>a\n\nb</div>`;
    assert.equal(normalizeHTMLBlockBlankLines(unfinished, true), unfinished);
  }
  const quoted = '`<div>a\n\nb</div>`';
  assert.equal(normalizeHTMLBlockBlankLines(quoted), quoted);
  const malformed = '<div title="unfinished\n\n<section>a\n\nb</section>';
  assert.equal(normalizeHTMLBlockBlankLines(malformed, true), malformed);
});

test("nested fence repair preserves LIFO, marker length, code characters and trailing text", () => {
  const nested = "```markdown\n# Example\n```js\nconst x = 1;\n```\nend\n```\nafter";
  assert.equal(normalizeNestedCodeFences(nested), nested.replace("```markdown", "````markdown").replace("end\n```", "end\n````"));
  const siblings = "~~~md\n~~~js\na\n~~~\n~~~py\nb\n~~~\n~~~";
  assert.equal(normalizeNestedCodeFences(siblings), siblings.replace("~~~md", "~~~~md").replace(/~~~$/, "~~~~"));
  for (const unchanged of [
    "```js\nconst x = 1;\n```",
    "```\n```js\na\n```\n```",
    "```md\n````js\na\n````\n```",
    "```md\n```js\na\n```",
    "```md\n~~~js\na\n~~~\n```",
  ]) assert.equal(normalizeNestedCodeFences(unchanged), unchanged);
});

test("ambiguous fence repair has a linear total work budget and retains all source", () => {
  for (const count of [300, 600, 1200, 2400]) {
    const input = "```markdown\nunchanged content\n".repeat(count);
    work.fence = 0;
    assert.equal(normalizeNestedCodeFences(input), input);
    assert.ok(work.fence <= 16 * count + 1, `${count} fences inspected ${work.fence} candidates`);
    assert.ok(work.fence >= count);
    const repairedPrefix = "~~~md\n~~~js\ncode\n~~~\n~~~\n";
    assert.equal(normalizeNestedCodeFences(repairedPrefix + input), repairedPrefix + input);
  }
});

test("HTML matching scans long complete, unclosed and malformed source once", () => {
  for (const count of [300, 600, 1200, 2400]) {
    for (const input of [
      "<div>open\n\n".repeat(count),
      "<div>open\n\n".repeat(count) + "</div>\n".repeat(count),
      `<div title="unfinished\n\n${"<section>\n\n".repeat(count)}`,
    ]) {
      work.html = 0;
      normalizeHTMLBlockBlankLines(input);
      assert.ok(work.html > 0);
      assert.ok(work.html <= input.length, `${input.length} characters visited ${work.html} times`);
    }
  }
});

test("long mixed prose, prices and math retain their normal rendering semantics", () => {
  const paragraph = "Price $1/月 and $2. Formula \\(x + 1\\). Code `\\(literal\\)`\n\n";
  const expected = "Price &#36;1/月 and &#36;2. Formula $x + 1$. Code `\\(literal\\)`\n\n";
  assert.equal(escapeNonMathDollars(normalizeMathDelimiters(paragraph.repeat(2400))), expected.repeat(2400));
});

test("LaTeX delimiter pairs retain whitespace, display, escaping and code semantics", () => {
  assert.equal(normalizeMathDelimiters("before \\[ \n x + 1 \n \\] after \\( y \\)"), "before $$\nx + 1\n$$ after $y$");
  assert.equal(normalizeMathDelimiters("\\(\\(x\\) tail"), "$\\(x$ tail");
  assert.equal(normalizeMathDelimiters("`\\(code\\)` and \\(x\\)"), "`\\(code\\)` and $x$");
  for (const opening of ["\\[", "\\("]) {
    const unfinished = `${opening} unfinished\n`.repeat(2400);
    work.latex = 0;
    assert.equal(normalizeMathDelimiters(unfinished), unfinished);
    assert.equal(work.latex, 1);
  }
});

test("visual HTML unwraps only complete styled blocks and keeps unfinished fence work linear", () => {
  const visual = '<div style="color: red; padding: 10px">\nhello\n</div>';
  for (const marker of ["```", "~~~"]) {
    assert.equal(normalizeHTMLVisualMarkdownFences(`before\n${marker}markdown\n${visual}\n    ${marker}\nafter`), `before\n${visual}\nafter`);
    const plain = `${marker}markdown\n<div>plain HTML</div>\n${marker}`;
    assert.equal(normalizeHTMLVisualMarkdownFences(plain), plain);
  }
  const mixed = `\`\`\`markdown\n~~~md\n${visual}\n~~~`;
  assert.equal(normalizeHTMLVisualMarkdownFences(mixed), `\`\`\`markdown\n${visual}`);
  for (const count of [300, 600, 1200, 2400]) {
    const unfinished = "```markdown\n<div>open\n".repeat(count);
    work.visual = 0;
    assert.equal(normalizeHTMLVisualMarkdownFences(unfinished), unfinished);
    assert.equal(work.visual, count);
  }
});
