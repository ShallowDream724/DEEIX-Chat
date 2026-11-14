"use client";

import * as React from "react";
import { subscribeMediaActivity } from "@/shared/lib/media-activity";
import { cn } from "@/lib/utils";

/** Display-only: the recording session owns capture and publishes its measured amplitude. */
export function MediaWaveform({ className, active = true }: { className?: string; active?: boolean }) {
  const ref = React.useRef<SVGSVGElement>(null);
  const pathRef = React.useRef<SVGPathElement>(null);
  const [count, setCount] = React.useState(48);
  const baseline = React.useMemo(() => waveformPath(new Float32Array(count)), [count]);
  React.useEffect(() => {
    const svg = ref.current;
    if (!svg) return;
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setCount(Math.max(12, Math.min(160, Math.floor(entry.contentRect.width / 8))));
    });
    observer.observe(svg);
    return () => observer.disconnect();
  }, []);
  React.useEffect(() => {
    const path = pathRef.current;
    if (!path) return;
    const levels = new Float32Array(count);
    const reset = () => { levels.fill(0); path.setAttribute("d", baseline); };
    if (!active) { reset(); return; }
    const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
    const unsubscribe = subscribeMediaActivity((frame) => {
      if (document.hidden || reducedMotion.matches || !frame.active || frame.kind !== "recording") { reset(); return; }
      levels.copyWithin(0, 1);
      levels[levels.length - 1] = frame.level;
      path.setAttribute("d", waveformPath(levels));
    });
    return () => { unsubscribe(); reset(); };
  }, [active, baseline, count]);
  return <svg ref={ref} data-media-waveform viewBox={`0 0 ${count * 8} 40`} preserveAspectRatio="none" aria-hidden="true" className={cn("h-10 min-w-0 flex-1 text-muted-foreground/45", className)}><path ref={pathRef} d={baseline} fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" vectorEffect="non-scaling-stroke" /></svg>;
}

function waveformPath(levels: Float32Array) {
  return Array.from(levels, (level, index) => {
    const halfHeight = 0.01 + Math.min(1, Math.max(0, level)) * 17;
    const x = index * 8 + 4;
    return `M${x} ${(20 - halfHeight).toFixed(2)}V${(20 + halfHeight).toFixed(2)}`;
  }).join("");
}
