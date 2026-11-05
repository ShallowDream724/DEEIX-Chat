import assert from "node:assert/strict";
import test from "node:test";
import { normalizeUploadMimeForPolicy, normalizeUploadMimeValue } from "./upload-mime.ts";

test("common archives use explicit MIME values when browsers omit type", () => {
  const samples = {
    "archive.zip": "application/zip", "archive.7z": "application/x-7z-compressed",
    "archive.rar": "application/vnd.rar", "archive.tar": "application/x-tar",
    "archive.tar.gz": "application/gzip", "archive.tgz": "application/gzip",
    "archive.bz2": "application/x-bzip2", "archive.xz": "application/x-xz", "archive.zst": "application/zstd",
  };
  for (const [name, want] of Object.entries(samples)) {
    for (const type of ["", "application/octet-stream"]) {
      assert.equal(normalizeUploadMimeForPolicy({ name: name.toUpperCase(), type }), want, `${name} (${type})`);
    }
  }
});

test("common text and code extensions share the existing text preflight path", () => {
  for (const extension of ["tex", "bib", "cls", "sty", "ipynb", "jsonl", "ndjson", "tsv", "srt", "vtt", "log", "py", "go"]) {
    assert.equal(normalizeUploadMimeForPolicy({ name: `notes.${extension}`, type: "" }), "text/plain");
  }
});

test("MIME aliases match equivalent administrator entries", () => {
  for (const [raw, want] of Object.entries({
    " APPLICATION/X-ZIP-COMPRESSED; charset=binary ": "application/zip",
    "application/x-rar-compressed": "application/vnd.rar", "application/x-gzip": "application/gzip",
    "application/bzip2": "application/x-bzip2", "application/x-zstd": "application/zstd",
  })) assert.equal(normalizeUploadMimeValue(raw), want);
});

test("unknown files and executables gain no new preflight MIME", () => {
  for (const name of ["program.exe", "program.dll", "unknown.bin"]) {
    assert.equal(normalizeUploadMimeForPolicy({ name, type: "" }), "");
    assert.equal(normalizeUploadMimeForPolicy({ name, type: "application/octet-stream" }), "application/octet-stream");
  }
  assert.equal(normalizeUploadMimeForPolicy({ name: "wrong.zip", type: "image/png" }), "image/png");
  assert.equal(normalizeUploadMimeForPolicy({ name: "slides.pptx", type: "application/zip" }), "application/vnd.openxmlformats-officedocument.presentationml.presentation");
});
