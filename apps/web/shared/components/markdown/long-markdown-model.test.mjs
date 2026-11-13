import assert from "node:assert/strict";
import test from "node:test";
import { tableDataToCSV } from "streamdown";
import {
  isLongMarkdown,
  isHeadingMarkdownBlock,
  markdownTablePageSource,
  markdownTableSourceData,
  parseLargeMarkdownTable,
  splitMarkdownTableRow,
} from "./long-markdown-model.ts";

const tableSource = (count) => `| ID | Value |\n| --: | :-- |\n${Array.from({ length: count }, (_, index) => `| ${index + 1} | Row ${index + 1} |`).join("\n")}`;

test("large source tables paginate before parsing while every source row remains exportable", () => {
  const source = tableSource(2400);
  const table = parseLargeMarkdownTable(source);
  assert.ok(table);
  assert.equal(table.rows.length, 2400);
  assert.equal(table.source, source);
  const firstPage = markdownTablePageSource(table, 0);
  assert.equal(firstPage.split("\n").length, 102);
  assert.ok(firstPage.includes("Row 100 |"));
  assert.ok(!firstPage.includes("Row 101 |"));
  assert.ok(markdownTablePageSource(table, 23).includes("Row 2400 |"));
  const csv = tableDataToCSV(markdownTableSourceData(table));
  assert.equal(csv.split(/\r?\n/).length, 2401);
  assert.ok(csv.includes("2400,Row 2400"));
});

test("table pagination preserves escaped delimiters, alignment and formatted cell source", () => {
  assert.deepEqual(splitMarkdownTableRow("| a\\|b | `c` |"), ["a\\|b", "`c`"]);
  assert.deepEqual(splitMarkdownTableRow("a | b\\|"), ["a", "b\\|"]);
  const source = `| A | B |\n| :-- | --: |\n${"| **bold** | a\\|b |\n".repeat(200)}`;
  const table = parseLargeMarkdownTable(source);
  assert.ok(table);
  assert.equal(table.source, source);
  assert.deepEqual(markdownTableSourceData(table).rows[0], ["**bold**", "a|b"]);
  assert.ok(markdownTablePageSource(table, 1).includes("| :-- | --: |"));
});

test("normal tables and tables grouped with references or other blocks retain ordinary rendering", () => {
  assert.equal(parseLargeMarkdownTable(tableSource(10)), null);
  assert.equal(parseLargeMarkdownTable(`${tableSource(200)}\n\n[x]: https://example.com`), null);
  assert.equal(parseLargeMarkdownTable(`${tableSource(200)}\n\n[^1]: footnote`), null);
  assert.equal(parseLargeMarkdownTable(`${tableSource(200)}\n\nNext paragraph`), null);
  const partial = parseLargeMarkdownTable(`${tableSource(200)}\n| incoming`);
  assert.ok(partial);
  assert.deepEqual(markdownTableSourceData(partial).rows.at(-1), ["incoming", ""]);
});

test("long detection covers line-dense content and one very long paragraph without rewriting either", () => {
  assert.equal(isLongMarkdown("x\n".repeat(300)), true);
  assert.equal(isLongMarkdown("x".repeat(16_000)), true);
  assert.equal(isLongMarkdown("A short message."), false);
});

test("outline headings remain discoverable in distant ATX, setext, HTML and grouped blocks", () => {
  for (const source of ["## Far heading", "Setext heading\n---", "Setext heading\n===", "<h2>Heading</h2>", "Definition group\n\n### Heading\n\nBody"]) {
    assert.equal(isHeadingMarkdownBlock(source), true, source);
  }
  assert.equal(isHeadingMarkdownBlock("Ordinary paragraph."), false);
});

test("single-column GFM tables paginate and retain all rows", () => {
  const source = `| Item |\n| --- |\n${"| value |\n".repeat(201)}`;
  const table = parseLargeMarkdownTable(source);
  assert.ok(table);
  assert.equal(table.rows.length, 201);
  assert.equal(markdownTablePageSource(table, 2).split("\n").length, 3);
});
