import type { HighlightOptions } from "@streamdown/code";

export type FileCodeLanguage = HighlightOptions["language"];
export type FileCodeToken = { content: string; lightColor?: string; darkColor?: string };
type HighlightToken = { content: string; color?: string; htmlStyle?: Record<string, string> };

const EXTENSION_LANGUAGES: Record<string, FileCodeLanguage> = {
  py: "python", pyw: "python", pyi: "python",
  c: "c", h: "cpp", cc: "cpp", cpp: "cpp", cxx: "cpp", hh: "cpp", hpp: "cpp", hxx: "cpp",
  cs: "csharp", go: "go", rs: "rust", java: "java", kt: "kotlin", kts: "kotlin", swift: "swift",
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "jsx",
  ts: "typescript", mts: "typescript", cts: "typescript", tsx: "tsx",
  json: "json", jsonc: "jsonc", json5: "json5", ipynb: "json",
  html: "html", htm: "html", css: "css", scss: "scss", sass: "sass", less: "less",
  vue: "vue", svelte: "svelte", astro: "astro",
  xml: "xml", svg: "xml", xsl: "xml", xsd: "xml", yaml: "yaml", yml: "yaml", toml: "toml", ini: "ini",
  sh: "shellscript", bash: "shellscript", zsh: "shellscript", fish: "fish", ps1: "powershell", psm1: "powershell", psd1: "powershell", bat: "bat", cmd: "bat",
  rb: "ruby", rake: "ruby", gemspec: "ruby", php: "php", lua: "lua", pl: "perl", pm: "perl", r: "r", jl: "julia", scala: "scala", sc: "scala", ex: "elixir", exs: "elixir", erl: "erlang", hrl: "erlang", hs: "haskell", dart: "dart",
  sql: "sql", graphql: "graphql", gql: "graphql", proto: "proto", prisma: "prisma", tf: "hcl", tfvars: "hcl",
  diff: "diff", patch: "diff", cmake: "cmake", dockerfile: "docker", tex: "latex",
};

const FILENAME_LANGUAGES: Record<string, FileCodeLanguage> = {
  dockerfile: "docker", containerfile: "docker", makefile: "make", gnumakefile: "make", "cmakelists.txt": "cmake",
  gemfile: "ruby", rakefile: "ruby", ".bashrc": "shellscript", ".zshrc": "shellscript", ".profile": "shellscript",
};

export function resolveCodeLanguage(fileName?: string): FileCodeLanguage | null {
  const name = fileName?.split(/[\\/]/).pop()?.trim().toLowerCase() ?? "";
  if (!name) return null;
  if (name === ".env" || name.startsWith(".env.")) return "dotenv";
  if (name.startsWith("dockerfile.") || name.startsWith("containerfile.")) return "docker";
  const named = Object.hasOwn(FILENAME_LANGUAGES, name) ? FILENAME_LANGUAGES[name] : undefined;
  if (named) return named;
  const dot = name.lastIndexOf(".");
  const extension = dot >= 0 ? name.slice(dot + 1) : "";
  return Object.hasOwn(EXTENSION_LANGUAGES, extension) ? EXTENSION_LANGUAGES[extension] ?? null : null;
}

export function isCodeFilename(fileName: string): boolean {
  return resolveCodeLanguage(fileName) !== null;
}

export function canHighlightFileCode(content: string): boolean {
  if (content.length > 200_000) return false;
  let lines = 1;
  for (let index = 0; index < content.length; index += 1) {
    if (content.charCodeAt(index) === 10 && ++lines > 5000) return false;
  }
  return true;
}

/** Reject any token result that changes the original text; restore CRLF removed by tokenization. */
export function preserveFileCodeTokens(content: string, highlighted: readonly (readonly HighlightToken[])[]): FileCodeToken[][] | null {
  const lines = content.split("\n");
  if (highlighted.length !== lines.length) return null;
  let tokenCount = 0;
  const result: FileCodeToken[][] = [];
  for (let index = 0; index < lines.length; index += 1) {
    const source = lines[index] ?? "";
    const tokens = highlighted[index] ?? [];
    tokenCount += tokens.length;
    if (tokenCount > 30_000) return null;
    const text = tokens.map((token) => token.content).join("");
    const restoreCR = source.endsWith("\r") && source.slice(0, -1) === text;
    if (source !== text && !restoreCR) return null;
    const normalized: FileCodeToken[] = tokens.map((token) => ({
      content: token.content,
      lightColor: token.htmlStyle?.color ?? token.color,
      darkColor: token.htmlStyle?.["--shiki-dark"] ?? token.htmlStyle?.color ?? token.color,
    }));
    if (restoreCR) normalized.push({ content: "\r" });
    result.push(normalized);
  }
  return result;
}
