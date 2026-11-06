import assert from "node:assert/strict";
import test from "node:test";
import { readPreviewText } from "./preview-content-data.ts";

test("large ZIP and binary fallbacks inspect only a bounded prefix", async () => {
  for (const [prefix, readable] of [[`PK${String.fromCharCode(3, 4)}archive`, () => true], ["binary", () => false]]) {
    let probes = 0;
    const blob = {
      size: 100_000_000,
      slice: (start, end) => {
        probes += 1;
        assert.equal(start, 0);
        assert.equal(end, 8192);
        return new Blob([prefix]);
      },
      text: () => { throw new Error("Full binary decoding must not run"); },
    };
    assert.equal(await readPreviewText(blob, readable, new AbortController().signal), null);
    assert.equal(probes, 1);
  }
});

test("readable text is complete, including empty and non-ASCII files", async () => {
  for (const content of ["", "Hello\n世界", "完整内容\n".repeat(5000)]) {
    assert.equal(await readPreviewText(new Blob([content]), () => true, new AbortController().signal), content);
  }
});

test("aborting before or during probing prevents further decoding", async () => {
  const aborted = new AbortController();
  aborted.abort();
  await assert.rejects(readPreviewText(new Blob(["text"]), () => true, aborted.signal), { name: "AbortError" });
  const controller = new AbortController();
  let resolvePrefix;
  const blob = {
    size: 100_000,
    slice: () => ({ text: () => new Promise((resolve) => { resolvePrefix = resolve; }) }),
    text: () => { throw new Error("An aborted request must not decode the full file"); },
  };
  const pending = readPreviewText(blob, () => true, controller.signal);
  controller.abort();
  resolvePrefix("readable");
  await assert.rejects(pending, { name: "AbortError" });
});
