import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {performance} from "node:perf_hooks";
import {Chat01ReplayParser, resourcesFor} from "./normalizer.mjs";

const base = process.argv[2] || ".cache/chat01-probes/20261104";
const cases = ["01_native_image", "02_search_code_files", "03_mixed_image_search_code", "05_search_map_cards", "06_nonstream_code_files"];
const results = [];
for (const name of cases) {
  const raw = readFileSync(`${base}/${name}/content.txt`, "utf8").replace(/\r\n/g, "\n");
  const start = performance.now();
  const baseline = new Chat01ReplayParser().feed(raw).finish();
  assert.equal(baseline.blocks.map(b => b.text).join(""), raw, "no source characters may disappear");
  for (const step of [1, 7, 64, 511]) {
    const parser = new Chat01ReplayParser();
    for (let i = 0; i < raw.length; i += step) parser.feed(raw.slice(i, i + step));
    parser.finish();
    assert.ok(JSON.stringify(parser.blocks) === JSON.stringify(baseline.blocks), `${name}: chunk size ${step}`);
  }
  const sse = readFileSync(`${base}/${name}/events.jsonl`, "utf8").trim();
  if (sse) {
    const parser = new Chat01ReplayParser();
    let joined = "";
    for (const line of sse.split("\n")) {
      const event = JSON.parse(line);
      try {
        const data = JSON.parse(event.data);
        const text = data.choices?.[0]?.delta?.content || "";
        joined += text; parser.feed(text);
      } catch { /* SSE terminal marker; no content. */ }
    }
    if (joined) { assert.ok(joined === raw, `${name}: captured SSE text`); assert.ok(JSON.stringify(parser.finish().blocks) === JSON.stringify(baseline.blocks), `${name}: captured SSE boundaries`); }
  }
  const answers = baseline.blocks.filter(b => b.kind === "answer").map(b => b.text).join("");
  if (name === "02_search_code_files") {
    assert.ok(answers.includes("sum(n**2 for n in range(1, 101))"), "answer's teaching code must remain");
    assert.ok(!answers.includes("import zipfile"), "execution code must leave the answer");
    assert.ok(baseline.blocks.some(b => b.kind === "search"));
  }
  if (name === "03_mixed_image_search_code") {
    assert.equal(new Set(baseline.blocks.filter(b => b.kind === "answer" && b.text.trim()).map(b => b.section)).size, 4);
    assert.ok(!answers.includes("from pathlib import Path"));
    assert.ok(answers.includes("测试结果：3/3 项完成"));
    assert.equal(resourcesFor(baseline.blocks).filter(r => r.kind === "file").length, 2);
  }
  if (name === "05_search_map_cards") assert.equal(baseline.blocks.filter(b => b.kind === "component").length, 1);
  results.push({name, characters: raw.length, blocks: baseline.blocks.length, resources: resourcesFor(baseline.blocks).length, experimentMs: +(performance.now()-start).toFixed(2)});
}
const literal = '## Answer:\n\n```python\nprint("<think>")\n## Thought:\n```\n正文没有丢失。';
const protectedCode = new Chat01ReplayParser();
for (const ch of literal) protectedCode.feed(ch);
protectedCode.finish();
assert.equal(protectedCode.blocks.filter(b => b.kind === "reasoning").length, 0);
assert.ok(protectedCode.blocks.some(b => b.kind === "answer" && b.text.includes('print("<think>")')));
const quoted = '<think>\n## Python:\n```python\ns = """\n</think>\n## Answer:\ntext\n"""\nprint(s)\n</think>\n## Answer:\n真正的答案';
for (const step of [1, quoted.length]) {
  const parser = new Chat01ReplayParser();
  for (let i=0; i<quoted.length; i+=step) parser.feed(quoted.slice(i,i+step));
  parser.finish();
  const answer = parser.blocks.filter(b=>b.kind==="answer").map(b=>b.text).join("").trim();
  assert.equal(answer, "真正的答案", "markers inside a Python multiline string are not protocol boundaries");
}
console.log(JSON.stringify({sourceConservation: true, chunkInvariance: true, tutorialCodePreserved: true, results}, null, 2));
