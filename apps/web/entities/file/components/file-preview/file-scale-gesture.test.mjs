import assert from "node:assert/strict";
import test from "node:test";
import { anchoredScaleScroll, clampPreviewScale, elementScaleAnchor, gestureStartScale, pinchPreviewScale, touchScaleGeometry, wheelPreviewScale } from "./file-scale-gesture.ts";

const near = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9, `${actual} != ${expected}`);

test("starting from fit width uses the document's actual 52%, then wheel deltas accumulate without a jump to stored zoom", () => {
  const actual = gestureStartScale({ left: 0, top: 0, width: 520, height: 780 }, 1000, 3.5);
  near(actual, 0.52);
  const doubleDelta = -Math.log(2) / 0.002;
  near(wheelPreviewScale(actual, doubleDelta, 0, 600), 1.04);
  const halfDelta = doubleDelta / 2;
  near(wheelPreviewScale(wheelPreviewScale(actual, halfDelta, 0, 600), halfDelta, 0, 600), 1.04);
  near(gestureStartScale(null, 0, 1.25), 1.25);
});

test("a two-finger pinch scales from its starting distance rather than compounding every move", () => {
  const start = touchScaleGeometry({ x: 100, y: 100 }, { x: 200, y: 100 });
  const session = { scale: 0.52, distance: start.distance };
  assert.deepEqual(start.point, { x: 150, y: 100 });
  const middle = touchScaleGeometry({ x: 100, y: 120 }, { x: 250, y: 120 });
  near(pinchPreviewScale(session, middle.distance), 0.78);
  const final = touchScaleGeometry({ x: 110, y: 140 }, { x: 310, y: 140 });
  near(pinchPreviewScale(session, final.distance), 1.04);
  assert.deepEqual(final.point, { x: 210, y: 140 });
  near(pinchPreviewScale({ scale: 1.04, distance: 200 }, 100), 0.52);
});

test("both gesture paths respect 25%-400%, with equivalent pixel/line/page wheel units", () => {
  near(wheelPreviewScale(1, 16, 0, 640), wheelPreviewScale(1, 1, 1, 640));
  near(wheelPreviewScale(1, 640, 0, 640), wheelPreviewScale(1, 1, 2, 640));
  assert.equal(wheelPreviewScale(0.26, 10000, 0, 600), 0.25);
  assert.equal(wheelPreviewScale(3.8, -10000, 0, 600), 4);
  assert.equal(pinchPreviewScale({ scale: 0.52, distance: 100 }, 1), 0.25);
  assert.equal(pinchPreviewScale({ scale: 0.52, distance: 100 }, 2000), 4);
  assert.equal(clampPreviewScale(1), 1);
});

test("the same document point stays under the pointer after zoom despite page margins and scroll offsets", () => {
  const pointer = { x: 210, y: 180 };
  const anchor = elementScaleAnchor({ left: 80, top: -600, width: 520, height: 1040 }, pointer);
  const scroll = { left: 40, top: 900 };
  const after = { left: 60, top: -1000, width: 780, height: 1560 };
  const next = anchoredScaleScroll(scroll, after, anchor, pointer);
  near(next.left, 85);
  near(next.top, 890);
  near(after.left - (next.left - scroll.left) + anchor.x * after.width, pointer.x);
  near(after.top - (next.top - scroll.top) + anchor.y * after.height, pointer.y);
});

test("moving the two-finger centre pans the anchored content while zooming", () => {
  const anchor = elementScaleAnchor({ left: 80, top: -600, width: 520, height: 1040 }, { x: 210, y: 180 });
  const scroll = { left: 40, top: 900 };
  const after = { left: 60, top: -1000, width: 780, height: 1560 };
  const centre = { x: 230, y: 210 };
  const next = anchoredScaleScroll(scroll, after, anchor, centre);
  near(next.left, 65);
  near(next.top, 860);
  near(after.left - (next.left - scroll.left) + anchor.x * after.width, centre.x);
  near(after.top - (next.top - scroll.top) + anchor.y * after.height, centre.y);
});
