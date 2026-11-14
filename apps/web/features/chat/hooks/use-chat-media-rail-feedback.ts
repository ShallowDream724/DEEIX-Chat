"use client";

import * as React from "react";
import { subscribeMediaActivity } from "@/shared/lib/media-activity";

export function useChatMediaRailFeedback(ref: React.RefObject<HTMLElement | null>, revision: string) {
  React.useEffect(() => {
    const root = ref.current;
    if (!root || typeof IntersectionObserver === "undefined") return;
    const visible = new Set<HTMLElement>();
    const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
    const reset = (line: HTMLElement) => { line.style.removeProperty("transform"); line.style.removeProperty("filter"); };
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const line = entry.target;
        if (!(line instanceof HTMLElement)) continue;
        if (entry.isIntersecting) visible.add(line);
        else { visible.delete(line); reset(line); }
      }
    }, { root });
    const lines = Array.from(root.querySelectorAll<HTMLElement>("[data-media-rail-line]"));
    for (const line of lines) observer.observe(line);
    const unsubscribe = subscribeMediaActivity((frame) => {
      const interacting = root.matches(":hover, :focus-within");
      const level = reducedMotion.matches || interacting || !frame.active ? 0 : frame.level;
      for (const line of visible) {
        if (level <= 0) { reset(line); continue; }
        line.style.transform = `scaleX(${1 + level * 0.15})`;
        line.style.filter = `opacity(${0.88 + level * 0.12})`;
      }
    });
    return () => { unsubscribe(); observer.disconnect(); for (const line of lines) reset(line); };
  }, [ref, revision]);
}
