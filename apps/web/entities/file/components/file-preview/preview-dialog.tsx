"use client";

import * as React from "react";
import { useTranslations } from "next-intl";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { FilePreviewPanel } from "@/entities/file/components/file-preview/preview-panel";
import type { FileContentLoader, PreviewDialogFile } from "@/entities/file/hooks/use-file-preview-dialog";
import { cn } from "@/lib/utils";
import { useIsMobile } from "@/shared/hooks/use-mobile";

export type { FileContentLoader, PreviewDialogFile };

/** Compatibility modal host; the shared panel owns loading, rendering and viewer controls. */
export function FilePreviewDialog({ file, open, onOpenChange, loadContent, allowDownload = true }: {
  file: PreviewDialogFile | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  loadContent?: FileContentLoader;
  allowDownload?: boolean;
}) {
  const t = useTranslations("files.previewDialog");
  const isMobile = useIsMobile();
  const [expanded, setExpanded] = React.useState(false);
  React.useEffect(() => { if (!open) setExpanded(false); }, [open]);
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className={cn(
      "h-svh max-h-none w-screen max-w-none gap-0 overflow-hidden rounded-none p-0 md:h-[min(80svh,760px)] md:w-[calc(100vw-2rem)] md:max-w-[900px] md:rounded-lg",
      expanded && "md:h-[calc(100svh-2rem)] md:max-w-[calc(100vw-2rem)]",
    )}>
      <DialogTitle className="sr-only">{file?.fileName ?? t("fallbackTitle")}</DialogTitle>
      <DialogDescription className="sr-only">{t("description")}</DialogDescription>
      <FilePreviewPanel file={open ? file : null} loadContent={loadContent} allowDownload={allowDownload} onClose={() => onOpenChange(false)} expanded={expanded} onToggleExpanded={isMobile ? undefined : () => setExpanded((value) => !value)} />
    </DialogContent>
  </Dialog>;
}
