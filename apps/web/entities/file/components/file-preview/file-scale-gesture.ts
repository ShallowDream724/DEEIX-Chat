export type ScalePoint = { x: number; y: number };
export type ScaleRect = { left: number; top: number; width: number; height: number };
export type PinchScaleSession = { scale: number; distance: number };

export function clampPreviewScale(scale: number): number {
  return Math.min(4, Math.max(0.25, scale));
}

export function gestureStartScale(rect: ScaleRect | null, naturalWidth: number, fallback: number): number {
  return rect && naturalWidth > 0 ? rect.width / naturalWidth : fallback;
}

export function wheelPreviewScale(scale: number, deltaY: number, deltaMode: number, viewportHeight: number): number {
  const pixels = deltaY * (deltaMode === 1 ? 16 : deltaMode === 2 ? viewportHeight : 1);
  return clampPreviewScale(scale * Math.exp(-pixels * 0.002));
}

export function touchScaleGeometry(first: ScalePoint, second: ScalePoint): { point: ScalePoint; distance: number } {
  return { point: { x: (first.x + second.x) / 2, y: (first.y + second.y) / 2 }, distance: Math.hypot(second.x - first.x, second.y - first.y) };
}

export function pinchPreviewScale(session: PinchScaleSession, distance: number): number {
  return clampPreviewScale(session.scale * distance / Math.max(session.distance, 1));
}

export function elementScaleAnchor(rect: ScaleRect, point: ScalePoint): ScalePoint {
  return { x: (point.x - rect.left) / Math.max(rect.width, 1), y: (point.y - rect.top) / Math.max(rect.height, 1) };
}

/** Keep the same document point under the pointer, including translated pinch centres. */
export function anchoredScaleScroll(scroll: { left: number; top: number }, rect: ScaleRect, anchor: ScalePoint, point: ScalePoint) {
  return { left: scroll.left + rect.left + anchor.x * rect.width - point.x, top: scroll.top + rect.top + anchor.y * rect.height - point.y };
}
