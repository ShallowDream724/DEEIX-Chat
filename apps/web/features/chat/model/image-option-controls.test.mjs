import assert from "node:assert/strict";
import test from "node:test";
import { imageOptionControls, imageOptionDefaults, isImageOptionVisible, IMAGE_OPTION_NUMBER_RANGES } from "./image-option-controls.ts";

const protocols = ["openai_image_generations", "openai_image_edits"];

test("Image 2.6 controls include GIF and planning without extending other image models", () => {
  for (const name of ["gpt-image-2.6-sunburst", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst-2026-11-01", "gpt-image-2.6-flare-2026-11-01"]) {
    const controls = imageOptionControls([], protocols, name);
    assert.deepEqual(controls.find((item) => item.path === "output_format").options, ["png", "jpeg", "webp", "gif"]);
    assert.deepEqual(controls.find((item) => item.path === "complexity").options, ["low", "medium", "high"]);
    assert.equal(controls.filter((item) => item.path === "output_format").length, 1);
    assert.deepEqual(imageOptionDefaults({}, controls), { output_format: "png", quality: "auto" });
    assert.equal(imageOptionDefaults({ output_format: "gif" }, controls).output_format, "gif");
  }
  assert.deepEqual(imageOptionControls([], protocols, "gpt-image-1"), []);
  assert.deepEqual(imageOptionControls([], ["openrouter_images"], "gpt-image-2.6-flare"), []);
  assert.deepEqual(imageOptionControls([], protocols, "gpt-image-2.6-flare-custom"), []);
});

test("declared controls and limited choices remain authoritative", () => {
  const declared = [
    { path: "output_format", options: ["gif"], locked: true },
    { path: "quality", options: ["high", "max"], label: "Rendering" },
    { path: "complexity", options: [], type: "text" },
  ];
  const controls = imageOptionControls(declared, protocols, "gpt-image-2.6-sunburst");
  for (const control of declared) assert.equal(controls.find((item) => item.path === control.path), control);
  assert.deepEqual(imageOptionDefaults({}, controls), {});
  assert.deepEqual(imageOptionDefaults({ output_format: "gif", quality: "high" }, controls), { output_format: "gif", quality: "high" });
  assert.equal(controls.filter((item) => item.path === "output_format").length, 1);
  assert.ok(controls.some((item) => item.path === "frame_count"));
});

test("default GIF remains visible when cached options omit format, while explicit choices override it", () => {
  for (const protocol of protocols) {
    const defaults = { output_format: "gif" };
    assert.equal(isImageOptionVisible("frame_count", {}, [protocol], defaults), true);
    assert.equal(isImageOptionVisible("frame_duration_ms", { quality: "high" }, [protocol], defaults), true);
    assert.equal(isImageOptionVisible("output_compression", {}, [protocol], defaults), false);
    assert.equal(isImageOptionVisible("partial_images", {}, [protocol], defaults), false);
    assert.equal(isImageOptionVisible("frame_count", { output_format: "webp" }, [protocol], defaults), false);
    assert.equal(isImageOptionVisible("output_compression", { output_format: "webp" }, [protocol], defaults), true);
  }
});

test("animation and compression controls follow the output format in generation and edit paths", () => {
  for (const protocol of protocols) {
    for (const format of ["png", "jpeg", "webp", "gif"]) {
      const options = { output_format: format };
      assert.equal(isImageOptionVisible("frame_count", options, [protocol]), format === "gif");
      assert.equal(isImageOptionVisible("frame_duration_ms", options, [protocol]), format === "gif");
      assert.equal(isImageOptionVisible("output_compression", options, [protocol]), format === "jpeg" || format === "webp");
      assert.equal(isImageOptionVisible("partial_images", options, [protocol]), format !== "gif");
      assert.equal(isImageOptionVisible("mask", options, [protocol]), format !== "gif");
      assert.equal(isImageOptionVisible("quality", options, [protocol]), true);
    }
  }
  assert.deepEqual(IMAGE_OPTION_NUMBER_RANGES.frame_count, { min: 2, max: 16, step: 1, default: 9 });
  assert.deepEqual(IMAGE_OPTION_NUMBER_RANGES.frame_duration_ms, { min: 20, max: 2000, step: 10, default: 200 });
});
