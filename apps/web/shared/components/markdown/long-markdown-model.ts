/** Source-backed pagination happens before Streamdown creates a table AST. */
export const LONG_MARKDOWN_MIN_CHARACTERS = 16_000;
export const LONG_MARKDOWN_MIN_LINES = 300;
export const LARGE_MARKDOWN_TABLE_MIN_ROWS = 200;
export const MARKDOWN_TABLE_PAGE_SIZE = 100;

export type MarkdownTableSource = {
  source: string;
  header: string;
  delimiter: string;
  rows: string[];
  headers: string[];
};

/** A short structural key resets selections when a different table replaces a block. */
export function markdownTableViewShape(table: MarkdownTableSource): string {
  const header = `${table.header}\n${table.delimiter}`;
  let hash = 2166136261;
  for (let index = 0; index < header.length; index += 1) {
    hash = Math.imul(hash ^ header.charCodeAt(index), 16777619);
  }
  return `${table.headers.length}:${header.length}:${hash >>> 0}`;
}

export function isLongMarkdown(source: string): boolean {
  if (source.length >= LONG_MARKDOWN_MIN_CHARACTERS) return true;
  let lines = 1;
  for (let index = 0; index < source.length; index += 1) {
    if (source.charCodeAt(index) === 10 && ++lines >= LONG_MARKDOWN_MIN_LINES) return true;
  }
  return false;
}

/** Outline navigation reads real heading nodes, so heading-bearing blocks must stay mounted. */
export function isHeadingMarkdownBlock(source: string): boolean {
  return /(?:^|\n)[^\n]*?(?:^|\s)#{1,6}(?:\s|$)/m.test(source)
    || /(?:^|\n)[^\n]+\n {0,3}(?:=+|-+)[ \t]*(?:\n|$)/.test(source)
    || /<h[1-6](?:\s|>)/i.test(source);
}

export function splitMarkdownTableRow(line: string): string[] {
  const cells: string[] = [];
  let cell = "";
  const trimmed = line.trim();
  for (let index = 0; index < trimmed.length; index += 1) {
    const character = trimmed[index];
    if (character === "\\" && index + 1 < trimmed.length) {
      cell += character + trimmed[++index];
    } else if (character === "|") {
      cells.push(cell.trim());
      cell = "";
    } else {
      cell += character;
    }
  }
  cells.push(cell.trim());
  if (trimmed.startsWith("|")) cells.shift();
  // Count backslashes to distinguish a final escaped pipe from an outer pipe.
  let slashCount = 0;
  for (let index = trimmed.length - 2; index >= 0 && trimmed[index] === "\\"; index -= 1) slashCount += 1;
  if (trimmed.endsWith("|") && slashCount % 2 === 0) cells.pop();
  return cells;
}

/** Accept only one standalone GFM table. Definitions, HTML and other blocks stay intact. */
export function parseLargeMarkdownTable(source: string): MarkdownTableSource | null {
  const lines = source.trim().split(/\r?\n/);
  if (lines.length < LARGE_MARKDOWN_TABLE_MIN_ROWS + 2) return null;
  const header = lines[0];
  const delimiter = lines[1];
  const headers = splitMarkdownTableRow(header);
  const separators = splitMarkdownTableRow(delimiter);
  if (!header.includes("|") || headers.length < 1 || separators.length !== headers.length
    || !separators.every((cell) => /^:?-+:?$/.test(cell))) return null;
  const rows = lines.slice(2);
  if (rows.some((line) => !line.trim() || !line.includes("|") || /^ {4}|^\t/.test(line))) return null;
  return { source, header, delimiter, rows, headers };
}

export function markdownTablePageSource(table: MarkdownTableSource, page: number): string {
  const start = page * MARKDOWN_TABLE_PAGE_SIZE;
  return [table.header, table.delimiter, ...table.rows.slice(start, start + MARKDOWN_TABLE_PAGE_SIZE)].join("\n");
}

/** CSV preserves formatted cell source rather than losing information to a mounted page's DOM. */
export function markdownTableSourceData(table: MarkdownTableSource): { headers: string[]; rows: string[][] } {
  const unescapeCell = (cell: string) => cell.replace(/\\([\\|])/g, "$1");
  return {
    headers: table.headers.map(unescapeCell),
    // GFM ignores excess cells and pads missing cells, including a streamed final row.
    rows: table.rows.map((line) => {
      const cells = splitMarkdownTableRow(line);
      return table.headers.map((_, index) => unescapeCell(cells[index] ?? ""));
    }),
  };
}
