"use client";

import * as React from "react";
import { subscribeMediaActivity } from "@/shared/lib/media-activity";
import { cn } from "@/lib/utils";

/** Display-only: the recording session owns capture and publishes its measured amplitude. */
export function MediaWaveform({ className, active = true }: { className?: string; active?: boolean }) {
  const ref = React.useRef<SVGSVGElement>(null);
  React.useEffect(() => {
    const svg = ref.current;
    if (!svg) return;
    const bars = Array.from(svg.querySelectorAll("line"));
    const reset = () => { for (const bar of bars) { bar.setAttribute("y1", "11.5"); bar.setAttribute("y2", "12.5"); } };
    if (!active) { reset(); return; }
    const levels = new Float32Array(bars.length);
    const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
    const unsubscribe = subscribeMediaActivity((frame) => {
      if (document.hidden) return;
      if (reducedMotion.matches || !frame.active || frame.kind !== "recording") { reset(); return; }
      levels.copyWithin(0, 1);
      levels[levels.length - 1] = frame.kind === "recording" ? frame.level : 0;
      bars.forEach((bar, index) => {
        const height = 1 + levels[index] * 21;
        bar.setAttribute("y1", String(12 - height / 2));
        bar.setAttribute("y2", String(12 + height / 2));
      });
    });
    return () => { unsubscribe(); reset(); };
  }, [active]);
  return <svg ref={ref} viewBox="0 0 192 24" preserveAspectRatio="none" aria-hidden="true" className={cn("h-6 min-w-0 flex-1 text-muted-foreground", className)}>{Array.from({ length: 48 }, (_, index) => <line key={index} x1={index * 4 + 2} x2={index * 4 + 2} y1="11.5" y2="12.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />)}</svg>;
}
