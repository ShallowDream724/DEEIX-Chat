// Generate a standalone local review from Go-produced deltas; never contacts a provider.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../..");
const output = path.resolve(process.argv[2] || path.join(root, ".cache/chat01-production-review.html"));
const result = spawnSync(process.env.GO_BINARY || "go", ["run", "./cmd/chat01-replay", "-root", root], {
  cwd: path.join(root, "backend"), encoding: "utf8", maxBuffer: 8 * 1024 * 1024,
});
if (result.error || result.status !== 0) throw result.error || new Error(result.stderr);
const payload = JSON.parse(result.stdout);
for (const sample of payload.cases) {
  if (!sample.conserved || !sample.frames.length) throw new Error(`Invalid production replay: ${sample.source}`);
  const blocks = new Map();
  for (const delta of [...sample.frames.flatMap((frame) => frame.deltas || []), ...(sample.finish || [])]) {
    const block = blocks.get(delta.blockId) || { text: "", kind: delta.kind };
    if (block.kind !== delta.kind) throw new Error("Block kind changed unexpectedly");
    block.text += delta.text;
    blocks.set(delta.blockId, block);
  }
  for (const block of sample.blocks) {
    if (blocks.get(block.id)?.text !== block.text) throw new Error(`Delta/block mismatch: ${sample.source}/${block.id}`);
  }
}
const data = JSON.stringify(payload).replaceAll("<", "\\u003c").replaceAll("\u2028", "\\u2028").replaceAll("\u2029", "\\u2029");
const template = fs.readFileSync(path.join(here, "production-preview.html"), "utf8");
const html = template.replace("__PRODUCTION_DATA__", data);
if (html.includes("__PRODUCTION_DATA__") || html.length > 2_000_000) throw new Error("Unexpected review size/template");
fs.mkdirSync(path.dirname(output), { recursive: true });
fs.writeFileSync(output, html);
console.log(JSON.stringify({ output, bytes: fs.statSync(output).size, parser: payload.parserVersion, go: payload.goVersion,
  sourceSHA256: payload.sourceSHA256, cases: payload.cases.map((item) => ({ name: item.name, frames: item.frames.length,
    blocks: item.blocks.length, resources: item.resources?.length || 0, conserved: item.conserved })) }, null, 2));
