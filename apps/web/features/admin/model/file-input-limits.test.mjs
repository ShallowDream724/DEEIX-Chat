import assert from "node:assert/strict";
import test from "node:test";
import { formatInputLimit, parseInputLimits, parseInputLimitValue, resolveInheritedInputLimit, resolveInputLimitValues, setInputLimit } from "./file-input-limits.ts";

test("channel placeholders use system settings while system settings inherit built-in defaults", () => {
  const builtIn = { defaults: { imageCount: 0, requestBytes: 100_000_000 }, upstreams: {} };
  const config = { defaults: { imageCount: 30, requestBytes: null }, upstreams: { "7": { imageCount: 10 } } };
  assert.equal(resolveInheritedInputLimit(config, builtIn, "", "imageCount"), 0);
  assert.equal(resolveInheritedInputLimit(config, builtIn, "7", "imageCount"), 30);
  assert.equal(resolveInheritedInputLimit(config, builtIn, "7", "requestBytes"), 100_000_000);
  assert.equal(resolveInputLimitValues(config, "7").imageCount, 10);
  assert.equal(resolveInputLimitValues(config, "8").imageCount, 30);
});

test("clearing a channel value restores the shared default and explicit zero stays unlimited", () => {
  const config = { defaults: { requestBytes: 100_000_000, imageCount: 30 }, upstreams: { "7": { imageCount: 10 } } };
  const unlimited = setInputLimit(config, "7", "imageCount", 0);
  assert.equal(resolveInputLimitValues(unlimited, "7").imageCount, 0);
  const cleared = setInputLimit(unlimited, "7", "imageCount", null);
  assert.deepEqual(cleared.upstreams, {});
  assert.equal(resolveInputLimitValues(cleared, "7").imageCount, 30);
  assert.equal(config.upstreams["7"].imageCount, 10);
  const shared = setInputLimit(cleared, "", "imageCount", 20);
  assert.equal(resolveInputLimitValues(shared, "7").imageCount, 20);
  assert.equal(resolveInputLimitValues(shared, "8").imageCount, 20);
});

test("no hidden provider or protocol settings can be saved", () => {
  for (const raw of [
    `{"profiles":{},"upstreams":{}}`,
    `{"defaults":{},"upstreams":{"7":{"protocols":{"openai_responses":{"imageCount":2}}}}}`,
    `{"defaults":{"unknown":1},"upstreams":{}}`,
    `{"defaults":{"imageCount":-1},"upstreams":{}}`,
    `{"defaults":{},"upstreams":{"abc":{}}}`,
  ]) assert.equal(parseInputLimits(raw), null);
  const valid = { defaults: { requestBytes: 100_000_000 }, upstreams: { "7": { imageCount: 0 } } };
  assert.deepEqual(parseInputLimits(JSON.stringify(valid)), valid);
});

test("decimal MB remains exact and invalid edits cannot be serialized", () => {
  for (const bytes of [0, 1, 8_388_608, 49_999_999, 50_000_000, 100_000_000, Number.MAX_SAFE_INTEGER]) {
    assert.equal(parseInputLimitValue(formatInputLimit(bytes, true), true), bytes);
  }
  for (const raw of ["-1", "NaN", "Infinity", "1e6", "0.0000001", "9007199255"]) assert.equal(parseInputLimitValue(raw, true), undefined);
  assert.equal(parseInputLimitValue("1.5", false), undefined);
  assert.equal(parseInputLimitValue("", true), null);
  assert.equal(parseInputLimitValue("0", true), 0);
});
