"use client";

import * as React from "react";
import dynamic from "next/dynamic";
import { Download, FileX } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { PreviewLoading } from "@/entities/file/components/file-preview/preview-loading";
import type { FilePreviewState, PreviewDialogFile } from "@/entities/file/hooks/use-file-preview-dialog";

type SourceProps = {
  source: string;
  toolbarContainer?: HTMLElement | null;
  showLoading?: boolean;
  onLoadingChange?: (loading: boolean) => void;
  allowFullscreen?: boolean;
};
type MediaProps = SourceProps & { kind: "image" | "audio" | "video"; alt: string; contentType: string };
type TextProps = { kind: "markdown" | "code" | "text"; content: string; fileName?: string };
const fallback = () => <PreviewLoading className="h-full min-h-40" />;
const PreviewPdf = dynamic<SourceProps>(() => import("./preview-pdf").then((mod) => mod.PreviewPdf), { ssr: false, loading: fallback });
const PreviewDocx = dynamic<SourceProps>(() => import("./preview-docx").then((mod) => mod.PreviewDocx), { ssr: false, loading: fallback });
const PreviewSheet = dynamic<SourceProps>(() => import("./preview-sheet").then((mod) => mod.PreviewSheet), { ssr: false, loading: fallback });
const PreviewMedia = dynamic<MediaProps>(() => import("./preview-media").then((mod) => mod.PreviewMedia), { ssr: false, loading: fallback });
const PreviewText = dynamic<TextProps>(() => import("./preview-text").then((mod) => mod.PreviewText), { ssr: false, loading: fallback });

export function FilePreviewContent({ file, state, download, allowDownload, toolbarContainer, allowFullscreen = true }: {
  file: PreviewDialogFile | null;
  state: FilePreviewState;
  download: () => void;
  allowDownload: boolean;
  toolbarContainer?: HTMLElement | null;
  allowFullscreen?: boolean;
}) {
  const t = useTranslations("files.previewDialog");
  const [viewerLoading, setViewerLoading] = React.useState(true);
  if (!file) return null;
  const isDocument = state.status === "ready" && ["pdf", "docx", "spreadsheet"].includes(state.kind);
  let content: React.ReactNode = null;
  if (state.status === "error") {
    content = <div className="flex h-full min-h-40 flex-col items-center justify-center gap-3 px-6 text-center">
      <FileX className="size-8 text-muted-foreground/50" />
      <p className="text-sm font-medium">{t("cannotPreview")}</p>
      <p className="max-w-[340px] text-xs text-muted-foreground">{state.message}</p>
    </div>;
  } else if (state.status === "ready") {
    const props: SourceProps = { source: state.objectURL, toolbarContainer, showLoading: false, onLoadingChange: setViewerLoading, allowFullscreen };
    if (state.kind === "pdf") content = <PreviewPdf {...props} />;
    else if (state.kind === "docx") content = <PreviewDocx {...props} />;
    else if (state.kind === "spreadsheet") content = <PreviewSheet {...props} />;
    else if (state.kind === "image" || state.kind === "audio" || state.kind === "video") {
      content = <PreviewMedia {...props} kind={state.kind} alt={file.fileName} contentType={state.contentType} />;
    } else if (state.kind === "markdown" || state.kind === "code" || state.kind === "text") {
      content = <div data-file-preview-scroll="" className="h-full overflow-auto"><div className="p-3 pt-[calc(var(--file-preview-toolbar-inset,0px)+0.75rem)]"><PreviewText kind={state.kind} content={state.textContent ?? ""} fileName={file.fileName} /></div></div>;
    } else {
      content = <div className="flex h-full min-h-40 flex-col items-center justify-center gap-3 px-6 text-center">
        <FileX className="size-8 text-muted-foreground/50" />
        <p className="text-sm font-medium">{t("unsupported")}</p>
        <p className="text-xs text-muted-foreground">{allowDownload ? t("downloadHint") : t("downloadUnavailableForShare")}</p>
        {allowDownload ? <Button size="sm" variant="outline" onClick={download} className="gap-1.5"><Download className="size-3.5" />{t("downloadFile")}</Button> : null}
      </div>;
    }
  }
  return <div className="relative h-full min-h-0 overflow-hidden">
    {content}
    {state.status === "loading" || isDocument && viewerLoading ? <PreviewLoading className="pointer-events-none absolute inset-0 z-10" /> : null}
  </div>;
}
