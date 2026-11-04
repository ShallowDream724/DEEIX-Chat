import assert from "node:assert/strict";
import test from "node:test";
import { formatInputLimit, parseInputLimits, parseInputLimitValue, resolveInheritedInputLimit, resolveInputLimitProfile, resolveInputLimitValues, setInputLimit } from "./file-input-limits.ts";

test("blank budget placeholders exclude the edited scope and inherit true system defaults", () => {
  const defaults = { profiles: { openai: { imageCount: 1500, requestBytes: 512000000 } }, upstreams: {} };
  const config = { profiles: { openai: { imageCount: 123, requestBytes: null } }, upstreams: { "7": { imageCount: 10, protocols: { openai_responses: { imageCount: 2 } } } } };
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "", "imageCount"), 1500);
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "7", "imageCount"), 123);
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "7", "imageCount", "openai_responses"), 10);
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "7", "requestBytes", "openai_responses"), 512000000);
  config.upstreams["7"].imageCount = 0;
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "7", "imageCount", "openai_responses"), 0);
  config.profiles.openai.imageCount = 0;
  assert.equal(resolveInheritedInputLimit(config, defaults, "openai", "7", "imageCount"), 0);
});

test("decimal MB preserves provider byte budgets exactly through an edit round trip", () => {
  for (const bytes of [0, 1, 8_388_608, 49_999_999, 50_000_000, 74_213_568, 100_000_000, Number.MAX_SAFE_INTEGER]) {
    assert.equal(parseInputLimitValue(formatInputLimit(bytes, true), true), bytes);
  }
  assert.equal(formatInputLimit(50_000_000, true), "50");
  assert.equal(formatInputLimit(49_999_999, true), "49.999999");
});

test("invalid numeric edits cannot be serialized as a changed budget", () => {
  for (const raw of ["-1", "NaN", "Infinity", "1e6", "0.0000001", "9007199255"]) {
    assert.equal(parseInputLimitValue(raw, true), undefined);
  }
  assert.equal(parseInputLimitValue("1.5", false), undefined);
  assert.equal(parseInputLimitValue("", true), null);
  assert.equal(parseInputLimitValue("0", true), 0);
});

test("clearing an upstream override restores inheritance while an explicit zero remains", () => {
  const original = { profiles: { openai: { imageCount: 1500 } }, upstreams: { "123": { imageCount: 10, nativeBytes: 0 } } };
  const cleared = setInputLimit(original, "openai", "123", "imageCount", null);
  assert.deepEqual(cleared.upstreams["123"], { nativeBytes: 0 });
  assert.equal(cleared.profiles.openai.imageCount, 1500);
  assert.equal(original.upstreams["123"].imageCount, 10);
  const inherited = setInputLimit(cleared, "openai", "123", "nativeBytes", null);
  assert.deepEqual(inherited.upstreams, {});
});

test("malformed saved configurations are rejected instead of replaced with an empty draft", () => {
  for (const raw of ["{", "null", "[]", '{"profiles":{},"upstreams":{"abc":{}}}', '{"profiles":{"openai":{"imageCount":-1}},"upstreams":{}}', '{"profiles":{"openai":{"imageCount":"10"}},"upstreams":{}}']) {
    assert.equal(parseInputLimits(raw), null);
  }
  assert.deepEqual(parseInputLimits('{"profiles":{"openai":{"imageCount":null}},"upstreams":{"123":{"imageCount":0}}}'), {
    profiles: { openai: { imageCount: null } }, upstreams: { "123": { imageCount: 0 } },
  });
});

test("protocol-specific edits inherit profile then shared upstream values and remain isolated", () => {
  const config = {
    profiles: { openai: { imageCount: 1500, requestBytes: 512_000_000 } },
    upstreams: { "123": { imageCount: 10, protocols: { openai_responses: { imageCount: 0 } } } },
  };
  assert.equal(resolveInputLimitValues(config, "openai", "123", "openai_responses").imageCount, 0);
  assert.equal(resolveInputLimitValues(config, "openai", "123", "openai_chat_completions").imageCount, 10);
  assert.equal(resolveInputLimitValues(config, "openai", "456", "openai_responses").imageCount, 1500);
  const updated = setInputLimit(config, "openai", "123", "requestBytes", 50_000_000, "openai_chat_completions");
  assert.equal(resolveInputLimitValues(updated, "openai", "123", "openai_chat_completions").requestBytes, 50_000_000);
  assert.equal(resolveInputLimitValues(updated, "openai", "123", "openai_responses").requestBytes, 512_000_000);
  const cleared = setInputLimit(updated, "openai", "123", "imageCount", null, "openai_responses");
  assert.equal(resolveInputLimitValues(cleared, "openai", "123", "openai_responses").imageCount, 10);
  assert.deepEqual(parseInputLimits(JSON.stringify(cleared)), cleared);
});

test("official budgets require the actual endpoint and supported protocol", () => {
  assert.equal(resolveInputLimitProfile("openai_responses", "https://api.openai.com/v1"), "openai");
  assert.equal(resolveInputLimitProfile("openai_responses", "https://chat01.example/v1"), "compatible");
  assert.equal(resolveInputLimitProfile("anthropic_messages", "https://api.anthropic.com"), "anthropic");
  assert.equal(resolveInputLimitProfile("anthropic_messages", "https://host.example/anthropic"), "compatible");
  assert.equal(resolveInputLimitProfile("google_generate_content", "https://proxy.example"), "google_generate_content");
  assert.equal(resolveInputLimitProfile("openrouter_responses", "https://proxy.example"), "openrouter");
});
