import assert from "node:assert/strict";
import test from "node:test";
import { denormalizeMBValue, normalizeMBValue } from "./files-size-values.ts";

test("existing binary-sized limits display decimal MB and round trip without changing bytes", () => {
  assert.equal(normalizeMBValue("52428800"), "52.4288");
  assert.equal(normalizeMBValue("104857600"), "104.8576");
  for (const bytes of ["0", "1", "52428800", "104857600", "9007199254740991", "9223372036854775807"]) {
    const displayed = normalizeMBValue(bytes);
    assert.equal(denormalizeMBValue(displayed), bytes);
    assert.equal(normalizeMBValue(bytes), displayed);
  }
});

test("blank inheritance and explicit zero remain distinct", () => {
  assert.equal(normalizeMBValue(""), "");
  assert.equal(denormalizeMBValue(""), "");
  assert.equal(normalizeMBValue("0"), "0");
  assert.equal(denormalizeMBValue("0"), "0");
  assert.equal(denormalizeMBValue("0.000001"), "1");
  assert.equal(denormalizeMBValue("50"), "50000000");
});
