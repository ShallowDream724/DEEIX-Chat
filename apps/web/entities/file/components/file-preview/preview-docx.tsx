"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { Maximize2, Minimize2 } from "lucide-react";
import { useTranslations } from "next-intl";

import { useLocalizedErrorMessage } from "@/i18n/use-localized-error";
import { Button } from "@/components/ui/button";
import { FileScaleSelect, useFileScale, useFileScaleGestures } from "@/entities/file/components/file-preview/file-scale";
import { PreviewLoading } from "@/entities/file/components/file-preview/preview-loading";

type PreviewDocxProps = {
  source: string;
  toolbarContainer?: HTMLElement | null;
  showLoading?: boolean;
  onLoadingChange?: (loading: boolean) => void;
  allowFullscreen?: boolean;
};

const DOCX_ZOOM = {
  min: 0.25,
  max: 4,
  step: 0.1,
  initial: 1,
};

function measureDocxContent(node: HTMLDivElement): { width: number; height: number } {
  const pages = Array.from(node.querySelectorAll<HTMLElement>(".docx-wrapper > section.docx, section.docx"));
  if (pages.length === 0) {
    return {
      width: node.scrollWidth || node.offsetWidth || 0,
      height: node.scrollHeight || node.offsetHeight || 0,
    };
  }

  let maxWidth = 0;
  let totalHeight = 0;

  for (const page of pages) {
    const style = window.getComputedStyle(page);
    const marginTop = Number.parseFloat(style.marginTop) || 0;
    const marginBottom = Number.parseFloat(style.marginBottom) || 0;
    maxWidth = Math.max(maxWidth, page.offsetWidth);
    totalHeight += page.offsetHeight + marginTop + marginBottom;
  }

  return {
    width: maxWidth,
    height: totalHeight,
  };
}

export function PreviewDocx({ source, toolbarContainer, showLoading = true, onLoadingChange, allowFullscreen = true }: PreviewDocxProps) {
  const t = useTranslations("files.previewErrors");
  const tPreview = useTranslations("files.preview");
  const resolveErrorMessage = useLocalizedErrorMessage();
  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const previewRef = React.useRef<HTMLDivElement | null>(null);
  const scrollRegionRef = React.useRef<HTMLDivElement | null>(null);
  const [status, setStatus] = React.useState<"loading" | "ready" | "error">("loading");
  const [errorMessage, setErrorMessage] = React.useState(t("wordLoadFailed"));
  const [fitWidth, setFitWidth] = React.useState(true);
  const [availableWidth, setAvailableWidth] = React.useState(0);
  const { scale: zoom, setScale, rememberPosition } = useFileScale(DOCX_ZOOM, { scrollRef: scrollRegionRef, layoutKey: `${fitWidth}:${availableWidth}` });
  const [isFullscreen, setIsFullscreen] = React.useState(false);
  const [contentWidth, setContentWidth] = React.useState(0);
  const [contentHeight, setContentHeight] = React.useState(0);

  React.useEffect(() => {
    onLoadingChange?.(status === "loading");
  }, [onLoadingChange, status]);

  React.useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(document.fullscreenElement === previewRef.current);
    };

    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => {
      document.removeEventListener("fullscreenchange", handleFullscreenChange);
    };
  }, []);

  React.useEffect(() => {
    if (status !== "ready") {
      setAvailableWidth(0);
      return undefined;
    }

    const node = scrollRegionRef.current;
    if (!node) {
      return undefined;
    }

    const updateWidth = () => {
      rememberPosition();
      setAvailableWidth(Math.max(node.clientWidth - 32, 0));
    };

    updateWidth();

    const observer = new ResizeObserver(updateWidth);
    observer.observe(node);
    return () => observer.disconnect();
  }, [rememberPosition, status]);

  React.useEffect(() => {
    let cancelled = false;
    const abortController = new AbortController();
    const node = containerRef.current;
    let renderRoot: HTMLDivElement | null = null;

    void (async () => {
      try {
        setStatus("loading");
        const [docxModule, response] = await Promise.all([
          import("docx-preview"),
          fetch(source, { signal: abortController.signal }),
        ]);
        const buffer = await response.arrayBuffer();
        if (cancelled) return;
        const renderAsync = docxModule.renderAsync ?? docxModule.default?.renderAsync;
        if (!renderAsync) {
          throw new Error(t("wordViewerLoadFailed"));
        }
        if (node) {
          node.innerHTML = "";
        }

        if (!node) {
          throw new Error(t("wordContainerNotReady"));
        }

        renderRoot = document.createElement("div");
        renderRoot.className = "docx-preview-document";
        node.appendChild(renderRoot);

        await renderAsync(buffer, renderRoot, undefined, {
          inWrapper: false,
          breakPages: true,
          useBase64URL: true,
          ignoreWidth: false,
          ignoreHeight: false,
          renderFootnotes: true,
          renderEndnotes: true,
        });

        if (!cancelled) {
          setStatus("ready");
          return;
        }

        if (renderRoot.parentNode === node) {
          node.removeChild(renderRoot);
        }
      } catch (error) {
        if (cancelled) {
          return;
        }
        setStatus("error");
        setErrorMessage(resolveErrorMessage(error, t("wordLoadFailed")));
      }
    })();

    return () => {
      cancelled = true;
      abortController.abort();
      if (!node) {
        return;
      }
      if (renderRoot?.parentNode === node) {
        node.removeChild(renderRoot);
        return;
      }
      node.innerHTML = "";
    };
  }, [resolveErrorMessage, source, t]);

  React.useEffect(() => {
    if (status !== "ready") {
      return undefined;
    }

    const node = containerRef.current;
    if (!node) {
      return undefined;
    }

    const updateSize = () => {
      const metrics = measureDocxContent(node);
      setContentWidth(metrics.width);
      setContentHeight(metrics.height);
    };

    updateSize();

    const observer = new ResizeObserver(updateSize);
    observer.observe(node);
    return () => observer.disconnect();
  }, [status]);

  const fitScale = React.useMemo(() => {
    if (!contentWidth || !availableWidth) {
      return 1;
    }
    return availableWidth / contentWidth;
  }, [availableWidth, contentWidth]);

  const effectiveScale = fitWidth ? fitScale : zoom;
  useFileScaleGestures({ scrollRef: scrollRegionRef, scale: effectiveScale, onScale: (value) => { setFitWidth(false); setScale(value); }, enabled: status === "ready", resetKey: source });

  const toggleFullscreen = React.useCallback(async () => {
    const element = previewRef.current;
    if (!element) {
      return;
    }

    if (document.fullscreenElement === element) {
      await document.exitFullscreen();
      return;
    }

    await element.requestFullscreen();
  }, []);

  const toolbar = (
    <div className="flex items-center gap-1.5">
      <FileScaleSelect value={fitWidth ? null : zoom} fitScale={fitScale} disabled={status !== "ready"} onChange={(value) => {
        rememberPosition();
        setFitWidth(value === null);
        if (value !== null) setScale(value);
      }} />
      {allowFullscreen ? <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-7 rounded-full"
        aria-label={isFullscreen ? tPreview("exitFullscreen") : tPreview("enterFullscreen")}
        onClick={() => void toggleFullscreen()}
        disabled={status !== "ready"}
      >
        {isFullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
      </Button> : null}
    </div>
  );

  return (
    <div ref={previewRef} className="flex h-full min-h-0 flex-col bg-transparent">
      {toolbarContainer ? createPortal(toolbar, toolbarContainer) : (
        <div className="flex shrink-0 items-center justify-end gap-1.5 px-1 py-2">{toolbar}</div>
      )}

      <div className="relative min-h-0 flex-1">
        {status === "error" ? (
          <div className="flex min-h-0 h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            {errorMessage}
          </div>
        ) : null}

        {status !== "error" ? (
          <div ref={scrollRegionRef} data-file-preview-scroll="" className={`min-h-0 h-full flex-1 overflow-auto ${status === "loading" ? "invisible" : ""}`}>
            <div className="mx-auto flex min-w-full w-max justify-center px-4 pb-4 pt-[var(--file-preview-toolbar-inset,0px)]">
              <div
                data-file-scale-anchor=""
                className="relative shrink-0"
                style={{
                  width: contentWidth > 0 ? `${contentWidth * effectiveScale}px` : undefined,
                  height: contentHeight > 0 ? `${contentHeight * effectiveScale}px` : undefined,
                }}
              >
                <div
                  style={{
                    transform: `scale(${effectiveScale})`,
                    transformOrigin: "top left",
                    width: contentWidth > 0 ? `${contentWidth}px` : undefined,
                  }}
                >
                  <div ref={containerRef} className="docx-preview-root" />
                </div>
              </div>
            </div>
          </div>
        ) : null}

        {showLoading && status === "loading" ? (
          <PreviewLoading className="pointer-events-none absolute inset-0 z-10" />
        ) : null}
      </div>

      <style jsx global>{`
        .docx-preview-root {
          color: #000;
          width: auto;
          margin: 0;
        }

        .docx-preview-root .docx-wrapper {
          background: transparent;
          padding: 0;
          width: auto;
          margin: 0;
        }

        .docx-preview-root section.docx {
          margin: 0 auto 24px;
          box-shadow: 0 1px 3px rgb(0 0 0 / 8%);
          border: 1px solid var(--border);
          background-color: #fff;
        }

        .docx-preview-root section.docx:last-child {
          margin-bottom: 0;
        }
      `}</style>
    </div>
  );
}
