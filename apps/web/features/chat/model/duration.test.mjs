import assert from "node:assert/strict";
import test from "node:test";
import { timelineDurationMS } from "./duration.ts";

const time = (seconds) => new Date(seconds * 1000).toISOString();
const step = (start, end) => ({ startedAt: time(start), endedAt: time(end) });

test("execution duration uses elapsed time rather than summing overlapping steps", () => {
  assert.equal(timelineDurationMS([step(10, 20), step(15, 25)]), 15_000);
  assert.equal(timelineDurationMS([step(10, 20), step(10, 20)]), 10_000);
  assert.equal(timelineDurationMS([step(30, 35), step(10, 20)]), 25_000);
});

test("unknown or running timelines do not present a fabricated completed duration", () => {
  assert.equal(timelineDurationMS([]), undefined);
  assert.equal(timelineDurationMS([step(10, 20), { startedAt: time(15) }]), undefined);
  assert.equal(timelineDurationMS([step(20, 10)]), undefined);
  assert.equal(timelineDurationMS([{ startedAt: "invalid", endedAt: time(10) }]), undefined);
});
