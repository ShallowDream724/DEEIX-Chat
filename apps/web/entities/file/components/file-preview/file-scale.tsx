"use client";

import * as React from "react";
import { useTranslations } from "next-intl";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { anchoredScaleScroll, elementScaleAnchor, gestureStartScale, pinchPreviewScale, touchScaleGeometry, wheelPreviewScale, type PinchScaleSession, type ScalePoint } from "./file-scale-gesture";

export type FileScaleConfig = {
  min: number;
  max: number;
  step: number;
  initial: number;
};

type ScrollCenterAnchor = {
  x: number;
  y: number;
};

type UseFileScaleOptions = {
  scrollRef?: React.RefObject<HTMLElement | null>;
  layoutKey?: unknown;
};

export function clampFileScale(config: FileScaleConfig, value: number): number {
  return Math.min(config.max, Math.max(config.min, value));
}

function readScrollCenter(node: HTMLElement): ScrollCenterAnchor {
  return {
    x: (node.scrollLeft + node.clientWidth / 2) / Math.max(node.scrollWidth, node.clientWidth, 1),
    y: (node.scrollTop + node.clientHeight / 2) / Math.max(node.scrollHeight, node.clientHeight, 1),
  };
}

function restoreScrollCenter(node: HTMLElement, anchor: ScrollCenterAnchor) {
  node.scrollLeft = anchor.x * Math.max(node.scrollWidth, node.clientWidth, 1) - node.clientWidth / 2;
  node.scrollTop = anchor.y * Math.max(node.scrollHeight, node.clientHeight, 1) - node.clientHeight / 2;
}

export function useFileScale(config: FileScaleConfig, options: UseFileScaleOptions = {}) {
  const { min, max, step, initial } = config;
  const scrollRefStoreRef = React.useRef(options.scrollRef);
  const pendingAnchorRef = React.useRef<ScrollCenterAnchor | null>(null);
  const [scale, setScaleState] = React.useState(initial);
  const rememberPosition = React.useCallback(() => {
    const node = scrollRefStoreRef.current?.current;
    if (node) pendingAnchorRef.current = readScrollCenter(node);
  }, []);

  React.useLayoutEffect(() => {
    scrollRefStoreRef.current = options.scrollRef;
  }, [options.scrollRef]);

  const setScale = React.useCallback(
    (nextScale: number | ((current: number) => number)) => {
      const scrollNode = scrollRefStoreRef.current?.current ?? null;
      if (scrollNode) {
        pendingAnchorRef.current = readScrollCenter(scrollNode);
      }

      setScaleState((current) => {
        const resolvedScale = typeof nextScale === "function" ? nextScale(current) : nextScale;
        return Math.min(max, Math.max(min, resolvedScale));
      });
    },
    [max, min],
  );

  React.useLayoutEffect(() => {
    const anchor = pendingAnchorRef.current;
    const scrollNode = scrollRefStoreRef.current?.current ?? null;
    if (!anchor || !scrollNode) {
      return;
    }

    pendingAnchorRef.current = null;
    restoreScrollCenter(scrollNode, anchor);
  }, [scale, options.layoutKey]);

  const zoomOut = React.useCallback(() => {
    setScale((current) => current - step);
  }, [setScale, step]);

  const zoomIn = React.useCallback(() => {
    setScale((current) => current + step);
  }, [setScale, step]);

  return {
    scale,
    setScale,
    zoomOut,
    zoomIn,
    canZoomOut: scale > min,
    canZoomIn: scale < max,
    rememberPosition,
  };
}

function scaleAnchorElement(node: HTMLElement, point: ScalePoint): HTMLElement | null {
  const rect = node.getBoundingClientRect();
  for (const x of [point.x, rect.left + node.clientWidth / 2]) {
    const element = document.elementFromPoint(x, point.y)?.closest<HTMLElement>("[data-file-scale-anchor]");
    if (element && node.contains(element)) return element;
  }
  let nearest: HTMLElement | null = null;
  let distance = Number.POSITIVE_INFINITY;
  for (const canvas of node.querySelectorAll("canvas")) {
    const element = canvas.closest<HTMLElement>("[data-file-scale-anchor]");
    if (!element) continue;
    const bounds = element.getBoundingClientRect();
    const next = Math.max(bounds.top - point.y, point.y - bounds.bottom, 0);
    if (next < distance) { nearest = element; distance = next; }
  }
  return nearest ?? node.querySelector<HTMLElement>("[data-file-scale-anchor]");
}

/** Native listeners are scoped to the reader; ordinary scrolling and browser gestures elsewhere remain native. */
export function useFileScaleGestures({ scrollRef, scale, onScale, enabled = true, resetKey }: {
  scrollRef: React.RefObject<HTMLElement | null>;
  scale: number;
  onScale: (scale: number) => void;
  enabled?: boolean;
  resetKey?: unknown;
}) {
  const latest = React.useRef({ scale, onScale });
  latest.current = { scale, onScale };
  const anchorRef = React.useRef<{ element: HTMLElement; anchor: ScalePoint; point: ScalePoint } | null>(null);
  const restoreAnchorRef = React.useRef(false);
  const [revision, setRevision] = React.useState(0);
  const [gesturing, setGesturing] = React.useState(false);

  React.useLayoutEffect(() => {
    const node = scrollRef.current;
    const current = anchorRef.current;
    if (!node || !current || !restoreAnchorRef.current || !node.contains(current.element)) return;
    restoreAnchorRef.current = false;
    const next = anchoredScaleScroll({ left: node.scrollLeft, top: node.scrollTop }, current.element.getBoundingClientRect(), current.anchor, current.point);
    node.scrollLeft = next.left;
    node.scrollTop = next.top;
  }, [revision, scale, scrollRef]);

  React.useEffect(() => {
    setGesturing(false);
    const node = scrollRef.current;
    if (!enabled || !node) return;
    let frame = 0;
    let timer = 0;
    let active = false;
    let wheelScale: number | null = null;
    let pinch: PinchScaleSession | null = null;
    let pendingScale: number | null = null;
    const priorTouchAction = node.style.touchAction;
    node.style.touchAction = "pan-x pan-y";
    const flush = () => {
      frame = 0;
      if (pendingScale === null) return;
      const next = pendingScale;
      pendingScale = null;
      latest.current.onScale(next);
      setRevision((value) => value + 1);
    };
    const start = (point: ScalePoint) => {
      const element = scaleAnchorElement(node, point);
      const rect = element?.getBoundingClientRect() ?? null;
      anchorRef.current = element && rect ? { element, anchor: elementScaleAnchor(rect, point), point } : null;
      active = true;
      setGesturing(true);
      return gestureStartScale(rect, Number(element?.getAttribute("data-file-scale-natural-width")), latest.current.scale);
    };
    const queue = (next: number, point: ScalePoint) => {
      pendingScale = next;
      if (anchorRef.current) anchorRef.current.point = point;
      restoreAnchorRef.current = true;
      if (!frame) frame = window.requestAnimationFrame(flush);
    };
    const finish = () => {
      window.clearTimeout(timer);
      window.cancelAnimationFrame(frame);
      flush();
      active = false;
      wheelScale = null;
      pinch = null;
      setGesturing(false);
    };
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey) return;
      event.preventDefault();
      const point = { x: event.clientX, y: event.clientY };
      if (!active) wheelScale = start(point);
      wheelScale = wheelPreviewScale(wheelScale ?? latest.current.scale, event.deltaY, event.deltaMode, node.clientHeight);
      queue(wheelScale, point);
      window.clearTimeout(timer);
      timer = window.setTimeout(finish, 120);
    };
    const geometry = (touches: TouchList) => {
      const first = touches.item(0);
      const second = touches.item(1);
      return first && second ? touchScaleGeometry({ x: first.clientX, y: first.clientY }, { x: second.clientX, y: second.clientY }) : null;
    };
    const onTouchStart = (event: TouchEvent) => {
      if (event.touches.length !== 2) return;
      const next = geometry(event.touches);
      if (!next || next.distance < 1) return;
      event.preventDefault();
      if (active) finish();
      const startScale = start(next.point);
      pinch = { scale: startScale, distance: next.distance };
    };
    const onTouchMove = (event: TouchEvent) => {
      if (!pinch || event.touches.length !== 2) return;
      const next = geometry(event.touches);
      if (!next) return;
      event.preventDefault();
      queue(pinchPreviewScale(pinch, next.distance), next.point);
    };
    const onTouchEnd = (event: TouchEvent) => { if (pinch && event.touches.length !== 2) finish(); };
    node.addEventListener("wheel", onWheel, { passive: false });
    node.addEventListener("touchstart", onTouchStart, { passive: false });
    node.addEventListener("touchmove", onTouchMove, { passive: false });
    node.addEventListener("touchend", onTouchEnd);
    node.addEventListener("touchcancel", finish);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(timer);
      node.removeEventListener("wheel", onWheel);
      node.removeEventListener("touchstart", onTouchStart);
      node.removeEventListener("touchmove", onTouchMove);
      node.removeEventListener("touchend", onTouchEnd);
      node.removeEventListener("touchcancel", finish);
      node.style.touchAction = priorTouchAction;
      anchorRef.current = null;
      restoreAnchorRef.current = false;
    };
  }, [enabled, resetKey, scrollRef]);
  return gesturing;
}

/** Keep the displayed bitmap while layout changes; replace it only after the reader settles. */
export function useSettledFileScale(zoom: number, fitWidth: boolean, availableWidth: number) {
  const [settled, setSettled] = React.useState({ zoom, fitWidth, availableWidth });
  React.useEffect(() => {
    const timer = window.setTimeout(() => setSettled({ zoom, fitWidth, availableWidth }), 120);
    return () => window.clearTimeout(timer);
  }, [availableWidth, fitWidth, zoom]);
  return settled.availableWidth > 0 ? settled : { zoom, fitWidth, availableWidth };
}

const PREVIEW_SCALES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4];

/** Percentages are natural document/image size; fit-width remains a separate mode. */
export function FileScaleSelect({ value, fitScale = 1, onChange, disabled = false }: {
  value: number | null;
  fitScale?: number;
  onChange: (value: number | null) => void;
  disabled?: boolean;
}) {
  const t = useTranslations("files.preview");
  const values = value === null || PREVIEW_SCALES.includes(value) ? PREVIEW_SCALES : [...PREVIEW_SCALES, value].sort((a, b) => a - b);
  return (
    <Select value={value === null ? "fit-width" : String(value)} onValueChange={(next) => onChange(next === "fit-width" ? null : Number(next))} disabled={disabled}>
      <SelectTrigger size="sm" className="h-8 w-[72px] gap-1 rounded-lg border-border/50 bg-background/90 px-1.5 text-sm shadow-none [[data-file-preview-header=embedded]_&]:w-[68px]" aria-label={t("zoom")}>
        <SelectValue>{Math.round((value ?? fitScale) * 100)}%</SelectValue>
      </SelectTrigger>
      <SelectContent position="popper" align="end" sideOffset={4}>
        <SelectItem value="fit-width">{t("fitWidth")}</SelectItem>
        {values.map((scale) => <SelectItem key={scale} value={String(scale)}>{Math.round(scale * 100)}%</SelectItem>)}
      </SelectContent>
    </Select>
  );
}
