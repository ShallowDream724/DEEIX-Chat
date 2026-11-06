import assert from "node:assert/strict";
import test from "node:test";
import { code } from "@streamdown/code";
import { canHighlightFileCode, isCodeFilename, preserveFileCodeTokens, resolveCodeLanguage } from "./file-code-language.ts";

test("common source filenames map to supported grammars, including Windows paths and special configuration names", () => {
  const cases = {
    "script.py": "python", "typing.pyi": "python", "main.c": "c", "C:\\src\\HELLO.CPP": "cpp", "include/widget.hpp": "cpp", "file.cxx": "cpp",
    "index.d.ts": "typescript", "page.tsx": "tsx", "index.cjs": "javascript", "App.jsx": "jsx", "main.go": "go", "lib.rs": "rust", "Main.java": "java", "Program.cs": "csharp", "query.sql": "sql",
    "Dockerfile": "docker", "Dockerfile.dev": "docker", "Makefile": "make", "CMakeLists.txt": "cmake", ".env.production": "dotenv", ".bashrc": "shellscript", "config.yaml": "yaml", "settings.toml": "toml", "template.html": "html",
  };
  for (const [name, language] of Object.entries(cases)) {
    assert.equal(resolveCodeLanguage(name), language, name);
    assert.equal(isCodeFilename(name), true, name);
    assert.equal(code.supportsLanguage(language), true, language);
  }
  for (const name of ["README.md", "notes.txt", "report.csv", "archive.zip", "script.py.txt", "constructor", "__proto__", "file.constructor", "path.py/file", "path.py/", ""]) {
    assert.equal(resolveCodeLanguage(name), null, name);
    assert.equal(isCodeFilename(name), false, name);
  }
});

test("highlighting has character/line/token budgets without truncating source", () => {
  assert.equal(canHighlightFileCode("x".repeat(200_000)), true);
  assert.equal(canHighlightFileCode("x".repeat(200_001)), false);
  assert.equal(canHighlightFileCode("\n".repeat(4999)), true);
  assert.equal(canHighlightFileCode("\n".repeat(5000)), false);
  assert.equal(preserveFileCodeTokens("a".repeat(30_001), [Array.from({ length: 30_001 }, () => ({ content: "a" }))]), null);
});

function highlight(source, language) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Highlighter did not complete")), 15_000);
    const done = (result) => { clearTimeout(timer); resolve(result); };
    const cached = code.highlight({ code: source, language, themes: ["github-light", "github-dark"] }, done);
    if (cached) done(cached);
  });
}

test("real Python/C++/HTML tokens preserve complete source, CRLF, tabs, Unicode, and markup literals", async () => {
  const fixtures = [
    ["python", "# <think>literal text</think>\r\n\r\ndef greet(name):\r\n\tprint('你好 ```html', name)\r\n"],
    ["cpp", "#include <iostream>\n\nint main() {\n\tstd::cout << \"你好 <div>&\";\n}\n"],
    ["html", "<!doctype html>\n<script>window.example = '<think>literal</think>';</script>\n<!-- 原始源码 -->\n"],
  ];
  for (const [language, source] of fixtures) {
    const result = await highlight(source, language);
    const before = JSON.stringify(result.tokens);
    const tokens = preserveFileCodeTokens(source, result.tokens);
    assert.ok(tokens, language);
    assert.equal(tokens.map((line) => line.map((token) => token.content).join("")).join("\n"), source);
    assert.equal(JSON.stringify(result.tokens), before);
    assert.ok(tokens.flat().some((token) => token.lightColor && token.darkColor && token.lightColor !== token.darkColor));
    const cached = code.highlight({ code: source, language, themes: ["github-light", "github-dark"] });
    assert.ok(cached, "the synchronous cached-result path must also be usable");
    assert.deepEqual(preserveFileCodeTokens(source, cached.tokens), tokens);
  }
});

test("unexpected token normalization is rejected rather than displaying altered source", () => {
  assert.equal(preserveFileCodeTokens("safe", [[{ content: "different" }]]), null);
  assert.equal(preserveFileCodeTokens("<div>", [[{ content: "&lt;div&gt;" }]]), null);
  assert.equal(preserveFileCodeTokens("first\nsecond", [[{ content: "firstsecond" }]]), null);
  assert.deepEqual(preserveFileCodeTokens("\r\n", [[], []]), [[{ content: "\r" }], []]);
});
