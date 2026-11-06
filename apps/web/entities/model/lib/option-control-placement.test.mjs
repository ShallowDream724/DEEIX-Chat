import assert from "node:assert/strict";
import test from "node:test";
import { resolveOptionControlPlacement, updateOptionControlPlacements } from "./option-control-placement.ts";

test("administrator placement follows explicit control precedence across parameter types", () => {
  assert.equal(resolveOptionControlPlacement("quality", "toolbar", undefined), "toolbar");
  assert.equal(resolveOptionControlPlacement("enabled", undefined, undefined), "menu");
  assert.equal(resolveOptionControlPlacement("quality", "toolbar", [{ id: "quality" }]), "menu");
  assert.equal(resolveOptionControlPlacement("quality", "menu", [{ id: "quality", placement: "toolbar" }]), "toolbar");
  assert.equal(resolveOptionControlPlacement("temperature", "menu", [{ id: "sampling", type: "number", path: "temperature", placement: "toolbar" }]), "toolbar");
  assert.equal(resolveOptionControlPlacement("reasoning", "menu", [{ kind: "reasoning", id: "reasoning", placement: "toolbar" }]), "menu");
});

test("visual pin changes survive explicit controls without changing patches or reasoning", () => {
  const controls = [
    { id: "quality", type: "select", label: "Quality", placement: "menu", protocols: ["openai_responses"], options: [{ value: "high", patches: { openai_responses: { quality: "high" } } }] },
    { id: "sampling", type: "number", path: "temperature", min: 0, max: 2, default: 1 },
    { id: "reasoning", kind: "reasoning", placement: "toolbar", icon: "brain" },
    { id: "unrelated", type: "toggle", placement: "toolbar", on: { patch: { custom: true } } },
  ];
  const snapshot = JSON.stringify(controls);
  const pinned = updateOptionControlPlacements(controls, new Map([["quality", "toolbar"], ["temperature", "toolbar"], ["reasoning", "menu"]]));
  assert.equal(resolveOptionControlPlacement("quality", "menu", pinned), "toolbar");
  assert.equal(resolveOptionControlPlacement("temperature", "menu", pinned), "toolbar");
  assert.deepEqual(pinned[0], { ...controls[0], placement: "toolbar" });
  assert.deepEqual(pinned[1], { ...controls[1], placement: "toolbar" });
  assert.equal(pinned[2], controls[2]);
  assert.equal(pinned[3], controls[3]);
  assert.equal(JSON.stringify(controls), snapshot);
  assert.equal(updateOptionControlPlacements(controls, new Map([["temperature", "menu"]]))[1], controls[1]);
  const saved = JSON.parse(JSON.stringify(updateOptionControlPlacements(pinned, new Map([["quality", "menu"]]))));
  assert.equal(resolveOptionControlPlacement("quality", "toolbar", saved), "menu");
  assert.deepEqual(saved[0].options, controls[0].options);
});
