"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { Maximize2, Minimize2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { resolveLocalizedErrorMessage } from "@/i18n/resolve-error-message";
import { Button } from "@/components/ui/button";
import { FileScaleSelect, useFileScale, useFileScaleGestures, useSettledFileScale } from "@/entities/file/components/file-preview/file-scale";
import { PreviewLoading } from "@/entities/file/components/file-preview/preview-loading";

type PreviewPdfProps = {
  source: string;
  toolbarContainer?: HTMLElement | null;
  showLoading?: boolean;
  onLoadingChange?: (loading: boolean) => void;
  allowFullscreen?: boolean;
};
type PdfModule = typeof import("pdfjs-dist/build/pdf.mjs");
type PdfDocument = Awaited<ReturnType<PdfModule["getDocument"]>["promise"]>;
type PdfPage = Awaited<ReturnType<PdfDocument["getPage"]>>;
type PageSize = { width: number; height: number };
const PDF_ZOOM = { min: 0.25, max: 4, step: 0.1, initial: 1 };

function resolvePdfErrorMessage(error: unknown, t: (key: string) => string): string {
  if (!(error instanceof Error)) return t("pdfLoadFailed");
  if (error.name === "PasswordException") return t("pdfPassword");
  if (error.name === "InvalidPDFException") return t("pdfInvalid");
  if (error.name === "ResponseException") return t("pdfReadFailed");
  return error.message?.trim() ? resolveLocalizedErrorMessage(error, error.message.trim()) : t("pdfLoadFailed");
}

function PdfPageCanvas({ documentProxy, pageNumber, visible, availableWidth, zoom, fitWidth, initialSize, raster, renderEnabled }: {
  documentProxy: PdfDocument; pageNumber: number; visible: boolean; availableWidth: number;
  zoom: number; fitWidth: boolean; initialSize: PageSize;
  raster: { zoom: number; fitWidth: boolean; availableWidth: number };
  renderEnabled: boolean;
}) {
  const canvasRef = React.useRef<HTMLCanvasElement | null>(null);
  const renderTaskRef = React.useRef<ReturnType<PdfPage["render"]> | null>(null);
  const [size, setSize] = React.useState(initialSize);
  const scale = fitWidth ? (availableWidth || size.width) / size.width : zoom;
  const renderWidth = raster.fitWidth ? raster.availableWidth : 1;
  const renderZoom = raster.zoom;
  const renderFitWidth = raster.fitWidth;
  React.useEffect(() => {
    const canvas = canvasRef.current;
    if (!visible || !renderEnabled || !renderWidth || !canvas) return;
    let cancelled = false;
    let page: PdfPage | null = null;
    let task: ReturnType<PdfPage["render"]> | null = null;
    let buffer: HTMLCanvasElement | null = null;
    const previousTask = renderTaskRef.current;
    void (async () => {
      try {
        // Finish cancelling the previous render before reusing this canvas.
        await previousTask?.promise.catch(() => undefined);
        if (cancelled) return;
        page = await documentProxy.getPage(pageNumber);
        if (cancelled) return;
        const natural = page.getViewport({ scale: 1 });
        setSize((current) => current.width === natural.width && current.height === natural.height ? current : { width: natural.width, height: natural.height });
        const viewport = page.getViewport({ scale: renderFitWidth ? renderWidth / natural.width : renderZoom });
        buffer = document.createElement("canvas");
        const context = buffer.getContext("2d");
        if (!context) return;
        const ratio = Math.min(window.devicePixelRatio || 1, Math.sqrt(16_777_216 / (viewport.width * viewport.height)));
        buffer.width = Math.max(1, Math.floor(viewport.width * ratio));
        buffer.height = Math.max(1, Math.floor(viewport.height * ratio));
        context.setTransform(ratio, 0, 0, ratio, 0, 0);
        task = page.render({ canvas: null, canvasContext: context, viewport });
        renderTaskRef.current = task;
        await task.promise;
        if (cancelled) return;
        // Keep the prior bitmap during pinch/resize; swap a complete replacement in one frame.
        canvas.width = buffer.width;
        canvas.height = buffer.height;
        canvas.getContext("2d")?.drawImage(buffer, 0, 0);
      } catch {
        // Leaving the visible range, resizing and closing cancel in-flight page work.
      } finally {
        page?.cleanup();
        if (buffer) { buffer.width = 0; buffer.height = 0; }
        if (renderTaskRef.current === task) renderTaskRef.current = null;
      }
    })();
    return () => { cancelled = true; task?.cancel(); };
  }, [documentProxy, pageNumber, renderEnabled, renderFitWidth, renderWidth, renderZoom, visible]);
  React.useEffect(() => {
    const canvas = canvasRef.current;
    return () => { if (canvas) { canvas.width = 0; canvas.height = 0; } };
  }, [documentProxy, visible]);
  return <div data-file-scale-anchor="" data-file-scale-natural-width={size.width} className="mx-auto shrink-0 border border-border/50 bg-white shadow-sm" style={{ width: Math.max(1, size.width * scale), height: Math.max(1, size.height * scale) }}>
    {visible ? <canvas ref={canvasRef} className="block size-full" /> : null}
  </div>;
}

export function PreviewPdf({ source, toolbarContainer, showLoading = true, onLoadingChange, allowFullscreen = true }: PreviewPdfProps) {
  const t = useTranslations("files.previewErrors");
  const tPreview = useTranslations("files.preview");
  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const scrollRegionRef = React.useRef<HTMLDivElement | null>(null);
  const pageRefs = React.useRef<Array<HTMLDivElement | null>>([]);
  const [documentProxy, setDocumentProxy] = React.useState<PdfDocument | null>(null);
  const [pageCount, setPageCount] = React.useState(0);
  const [firstPageSize, setFirstPageSize] = React.useState<PageSize>({ width: 612, height: 792 });
  const [visiblePages, setVisiblePages] = React.useState<Set<number>>(() => new Set([0]));
  const [availableWidth, setAvailableWidth] = React.useState(0);
  const [fitWidth, setFitWidth] = React.useState(true);
  const { scale: zoom, setScale, rememberPosition } = useFileScale(PDF_ZOOM, { scrollRef: scrollRegionRef, layoutKey: `${fitWidth}:${availableWidth}` });
  const [isFullscreen, setIsFullscreen] = React.useState(false);
  const [status, setStatus] = React.useState<"loading" | "ready" | "error">("loading");
  const [errorMessage, setErrorMessage] = React.useState("");
  useFileScaleGestures({ scrollRef: scrollRegionRef, scale: fitWidth && availableWidth ? availableWidth / firstPageSize.width : zoom, onScale: (value) => { setFitWidth(false); setScale(value); }, enabled: status === "ready", resetKey: source });
  const raster = useSettledFileScale(zoom, fitWidth, availableWidth);
  const renderEnabled = raster.zoom === zoom && raster.fitWidth === fitWidth && (!fitWidth || raster.availableWidth === availableWidth);
  React.useEffect(() => { onLoadingChange?.(status === "loading"); }, [onLoadingChange, status]);
  React.useEffect(() => {
    if (!allowFullscreen) return;
    const update = () => setIsFullscreen(document.fullscreenElement === containerRef.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, [allowFullscreen]);
  React.useEffect(() => {
    const node = scrollRegionRef.current;
    if (status !== "ready" || !node) return;
    let frame = 0;
    const update = () => { rememberPosition(); setAvailableWidth(Math.max(node.clientWidth - 64, 0)); };
    update();
    const observer = new ResizeObserver(() => {
      window.cancelAnimationFrame(frame); frame = window.requestAnimationFrame(update);
    });
    observer.observe(node);
    return () => { window.cancelAnimationFrame(frame); observer.disconnect(); };
  }, [rememberPosition, status]);
  React.useEffect(() => {
    const root = scrollRegionRef.current;
    if (status !== "ready" || !root) return;
    const observer = new IntersectionObserver((entries) => {
      setVisiblePages((current) => {
        const next = new Set(current);
        for (const entry of entries) {
          const index = Number(entry.target.getAttribute("data-page-index"));
          if (entry.isIntersecting) next.add(index); else next.delete(index);
        }
        return next;
      });
    }, { root, rootMargin: "600px 0px" });
    pageRefs.current.slice(0, pageCount).forEach((node) => { if (node) observer.observe(node); });
    return () => observer.disconnect();
  }, [pageCount, status]);
  React.useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    let loadingTask: ReturnType<PdfModule["getDocument"]> | null = null;
    setDocumentProxy(null); setPageCount(0); setVisiblePages(new Set([0])); setFitWidth(true); setStatus("loading");
    void (async () => {
      try {
        const [mod, response] = await Promise.all([import("pdfjs-dist/build/pdf.mjs"), fetch(source, { signal: controller.signal })]);
        const buffer = await response.arrayBuffer();
        if (cancelled) return;
        mod.GlobalWorkerOptions.workerSrc = new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url).toString();
        loadingTask = mod.getDocument({ data: new Uint8Array(buffer), cMapUrl: "/pdfjs/cmaps/", cMapPacked: true, standardFontDataUrl: "/pdfjs/standard_fonts/", useSystemFonts: true, enableXfa: true, stopAtErrors: false });
        const pdf = await loadingTask.promise;
        const page = await pdf.getPage(1);
        const size = page.getViewport({ scale: 1 });
        page.cleanup();
        if (cancelled) return;
        setFirstPageSize({ width: size.width, height: size.height }); setDocumentProxy(pdf); setPageCount(pdf.numPages); setStatus("ready");
      } catch (error) {
        if (!cancelled) { setErrorMessage(resolvePdfErrorMessage(error, t)); setStatus("error"); }
      }
    })();
    return () => { cancelled = true; controller.abort(); void loadingTask?.destroy().catch(() => undefined); };
  }, [source, t]);
  const toolbar = <div className="flex items-center gap-1.5">
    <FileScaleSelect value={fitWidth ? null : zoom} fitScale={availableWidth ? availableWidth / firstPageSize.width : 1} disabled={status !== "ready"} onChange={(value) => {
      rememberPosition(); setFitWidth(value === null); if (value !== null) setScale(value);
    }} />
    {allowFullscreen ? <Button type="button" variant="ghost" size="icon" className="size-7" aria-label={isFullscreen ? tPreview("exitFullscreen") : tPreview("enterFullscreen")} onClick={() => {
      if (document.fullscreenElement === containerRef.current) void document.exitFullscreen(); else void containerRef.current?.requestFullscreen();
    }} disabled={status !== "ready"}>{isFullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}</Button> : null}
  </div>;
  return <div ref={containerRef} className="flex h-full min-h-0 flex-col bg-transparent">
    {toolbarContainer ? createPortal(toolbar, toolbarContainer) : <div className="flex shrink-0 justify-end px-1 py-2">{toolbar}</div>}
    <div className="relative min-h-0 flex-1">
      {status === "error" ? <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">{errorMessage}</div> : null}
      {status === "ready" && documentProxy ? <div ref={scrollRegionRef} data-file-preview-scroll="" className="h-full min-h-0 overflow-auto [overflow-anchor:none]">
        <div className="flex min-w-full w-max flex-col gap-6 px-8 pb-8 pt-[calc(var(--file-preview-toolbar-inset,0px)+0.5rem)]">
          {Array.from({ length: pageCount }, (_, index) => <div key={index} data-page-index={index} ref={(node) => { pageRefs.current[index] = node; }}>
            <PdfPageCanvas documentProxy={documentProxy} pageNumber={index + 1} visible={visiblePages.has(index)} availableWidth={availableWidth} zoom={zoom} fitWidth={fitWidth} initialSize={firstPageSize} raster={raster} renderEnabled={renderEnabled} />
          </div>)}
        </div>
      </div> : null}
      {showLoading && status === "loading" ? <PreviewLoading className="pointer-events-none absolute inset-0 z-10" /> : null}
    </div>
  </div>;
}
