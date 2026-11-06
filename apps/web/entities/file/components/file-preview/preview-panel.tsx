"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { ChevronDown, Download, Maximize2, Minimize2, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { FilePreviewContent } from "@/entities/file/components/file-preview/preview-content";
import { useFilePreviewDialog, type FileContentLoader, type PreviewDialogFile } from "@/entities/file/hooks/use-file-preview-dialog";
import { resolveFileIcon } from "@/entities/file/lib/file-display";
import { cn } from "@/lib/utils";

export type FilePreviewPanelProps = {
  file: PreviewDialogFile | null;
  loadContent?: FileContentLoader;
  allowDownload?: boolean;
  onClose: () => void;
  expanded?: boolean;
  onToggleExpanded?: () => void;
  headerContainer?: HTMLElement | null;
};

export function FilePreviewPanel({ file, loadContent, allowDownload = true, onClose, expanded = false, onToggleExpanded, headerContainer }: FilePreviewPanelProps) {
  const t = useTranslations("files.previewDialog");
  const commonT = useTranslations("common.actions");
  const { state, download } = useFilePreviewDialog(file, loadContent);
  const [toolbarContainer, setToolbarContainer] = React.useState<HTMLDivElement | null>(null);
  const [showFullName, setShowFullName] = React.useState(false);
  React.useEffect(() => { setShowFullName(false); }, [file?.fileID]);
  if (!file) return null;
  const FileIcon = resolveFileIcon(file);
  const header = <header data-file-preview-header={headerContainer ? "embedded" : "default"} className={cn(
    "pointer-events-none flex min-w-0 items-center justify-between",
    headerContainer ? "h-full w-full flex-1 gap-1.5" : "absolute inset-x-0 top-0 z-20 h-[52px] gap-3 px-3",
  )}>
      <h2 className="pointer-events-auto w-11 min-w-0 shrink-0 text-sm font-medium">
        <Popover open={showFullName} onOpenChange={setShowFullName}>
          <PopoverTrigger asChild>
            <Button type="button" variant="outline" size="sm" className="h-8 w-full min-w-0 justify-center gap-1 overflow-hidden rounded-lg border-border/50 bg-background/90 px-1.5 text-sm shadow-none" aria-label={file.fileName}>
              <FileIcon className="size-4 shrink-0 text-muted-foreground" strokeWidth={1.5} />
              <ChevronDown className="size-2.5 shrink-0 text-muted-foreground" />
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-max max-w-[min(360px,calc(100vw-2rem))] break-words px-2.5 py-1.5 text-xs leading-4 [overflow-wrap:anywhere]">{file.fileName}</PopoverContent>
        </Popover>
      </h2>
      <div className={cn("flex shrink-0 items-center", headerContainer ? "gap-1.5" : "gap-2")}>
        <div ref={setToolbarContainer} className="pointer-events-auto flex items-center" />
        <div className="pointer-events-auto box-border flex h-8 rounded-lg border border-border/60 bg-background/90">
          {allowDownload ? <Button type="button" variant="ghost" size="icon" className="h-full w-8 rounded-lg text-muted-foreground" disabled={state.status !== "ready"} aria-label={t("download")} onClick={download}><Download className="size-4" /></Button> : null}
          {onToggleExpanded ? <Button type="button" variant="ghost" size="icon" className="h-full w-8 rounded-lg text-muted-foreground" aria-label={expanded ? t("restore") : t("maximize")} aria-pressed={expanded} onClick={onToggleExpanded}>
            {expanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
          </Button> : null}
          <Button type="button" variant="ghost" size="icon" className="h-full w-8 rounded-lg text-muted-foreground" aria-label={commonT("close")} onClick={onClose}><X className="size-4" /></Button>
        </div>
      </div>
    </header>;
  return <section data-file-preview-panel="" tabIndex={-1} className={cn("relative flex h-full min-h-0 min-w-0 flex-col overflow-hidden bg-muted/20 outline-none", headerContainer ? "[--file-preview-toolbar-inset:48px]" : "[--file-preview-toolbar-inset:52px]")} aria-label={t("description")}>
    <style jsx global>{`
      @supports selector(::-webkit-scrollbar) {
        [data-file-preview-panel] [data-file-preview-scroll] {
          scrollbar-width: auto;
          scrollbar-color: auto;
        }
        [data-file-preview-panel] [data-file-preview-scroll]::-webkit-scrollbar {
          width: 8px;
          height: 8px;
        }
        [data-file-preview-panel] [data-file-preview-scroll]::-webkit-scrollbar-button {
          width: 8px;
          height: 8px;
        }
      }
    `}</style>
    {headerContainer ? createPortal(header, headerContainer) : header}
    <div className="min-h-0 flex-1">
      <FilePreviewContent key={file.fileID} file={file} state={state} download={download} allowDownload={allowDownload} toolbarContainer={toolbarContainer} allowFullscreen={false} />
    </div>
  </section>;
}
