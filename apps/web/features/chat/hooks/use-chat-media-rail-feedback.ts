"use client";

import * as React from "react";
import { subscribeMediaActivity } from "@/shared/lib/media-activity";
import { MEDIA_SPECTRUM_BANDS } from "@/shared/lib/media-spectrum";

type RailLine = { element: HTMLElement; index: number; envelope: number };

export function useChatMediaRailFeedback(ref: React.RefObject<HTMLElement | null>, revision: string) {
  React.useEffect(() => {
    const root = ref.current;
    if (!root || typeof IntersectionObserver === "undefined") return;
    const visible = new Map<HTMLElement, RailLine>();
    let ordered: RailLine[] = [];
    const historySize = 5;
    const recentBands = new Float32Array(historySize * MEDIA_SPECTRUM_BANDS);
    const recentLevels = new Float32Array(historySize);
    let cursor = 0;
    const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
    const reset = (line: HTMLElement) => { line.style.removeProperty("transform"); line.style.removeProperty("opacity"); };
    const lines = Array.from(root.querySelectorAll<HTMLElement>("[data-media-rail-line]"));
    const indices = new Map(lines.map((line, index) => [line, index]));
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const line = entry.target;
        if (!(line instanceof HTMLElement)) continue;
        if (entry.isIntersecting) {
          if (!visible.has(line)) visible.set(line, { element: line, index: indices.get(line) ?? 0, envelope: 0 });
        } else { visible.delete(line); reset(line); }
      }
      ordered = Array.from(visible.values()).sort((a, b) => a.index - b.index);
    }, { root });
    for (const line of lines) observer.observe(line);
    const unsubscribe = subscribeMediaActivity((frame) => {
      const interacting = root.matches(":hover, :focus-within");
      if (reducedMotion.matches || interacting || !frame.active || frame.bands?.length !== MEDIA_SPECTRUM_BANDS) {
        recentBands.fill(0); recentLevels.fill(0); cursor = 0;
        for (const line of ordered) { line.envelope = 0; reset(line.element); }
        return;
      }
      recentBands.set(frame.bands, cursor * MEDIA_SPECTRUM_BANDS);
      recentLevels[cursor] = frame.level;
      const strength = (0.75 + frame.level * 0.5).toFixed(3);
      for (let index = 0; index < ordered.length; index++) {
        const line = ordered[index];
        const distance = ordered.length <= 2 ? 0 : Math.abs(index * 2 / (ordered.length - 1) - 1);
        const historySlot = (cursor - Math.floor(distance * (historySize - 1)) + historySize) % historySize;
        const offset = historySlot * MEDIA_SPECTRUM_BANDS;
        let energy = 0;
        if (ordered.length <= 2) {
          const first = Math.floor(index * frame.bands.length / ordered.length);
          const end = Math.ceil((index + 1) * frame.bands.length / ordered.length);
          for (let band = first; band < end; band++) energy = Math.max(energy, recentBands[offset + band]);
        } else {
          // Bass starts in the middle; higher bands and the same beat reach both edges.
          const band = distance * (frame.bands.length - 1);
          const lower = Math.floor(band);
          const fraction = band - lower;
          energy = recentBands[offset + lower] * (1 - fraction) + recentBands[offset + Math.min(lower + 1, frame.bands.length - 1)] * fraction;
        }
        const combined = energy * 0.7 + recentLevels[historySlot] * 0.3;
        const target = combined <= 0.015 ? 0 : combined;
        line.envelope += (target - line.envelope) * (target > line.envelope ? 0.85 : 0.2);
        if (line.envelope < 0.003) { reset(line.element); continue; }
        line.element.style.transform = `scaleX(${(1 + line.envelope * 4.3).toFixed(3)})`;
        line.element.style.opacity = `calc(var(--media-rail-base-opacity, 0.35) * ${strength})`;
      }
      cursor = (cursor + 1) % historySize;
    });
    return () => { unsubscribe(); observer.disconnect(); for (const line of lines) reset(line); };
  }, [ref, revision]);
}
