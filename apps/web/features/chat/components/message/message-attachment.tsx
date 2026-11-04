"use client";

import * as React from "react";
import { Download, Loader2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";

import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentMedia,
  AttachmentTitle,
  AttachmentTrigger,
} from "@/components/ui/attachment";
import type { MessageAttachment } from "@/features/chat/types/messages";
import {
  type FileContentLoader,
  LazyFilePreviewDialog,
  formatBytes,
  resolveFileExtension,
  resolveFileIcon,
} from "@/entities/file";

// ─── helpers ──────────────────────────────────────────────────────────────────

function resolveFileExt(name: string): string {
  const ext = resolveFileExtension(name);
  return ext ? ext.toUpperCase().slice(0, 6) : "FILE";
}

function resolveCardMeta(att: MessageAttachment, includeSize = true): string {
  const values = [resolveFileExt(att.fileName)];
  if (includeSize && att.sizeBytes > 0) values.push(formatBytes(att.sizeBytes));
  if (att.durationSeconds && att.durationSeconds > 0) {
    values.push(`${att.durationSeconds}s`);
  }
  return values.join(" · ");
}

// ─── single card ─────────────────────────────────────────────────────────────

function AttachmentCard({
  att,
  onClick,
  grouped = false,
  onDownload,
  downloading = false,
}: {
  att: MessageAttachment;
  onClick: () => void;
  grouped?: boolean;
  onDownload?: () => void;
  downloading?: boolean;
}) {
  const tActions = useTranslations("files.previewDialog");
  const meta = resolveCardMeta(att, !grouped);
  const fileIcon = resolveFileIcon(att);

  return (
    <Attachment
      size="sm"
      role={grouped ? "listitem" : undefined}
      className={cn(
        "border-0 text-left",
        grouped
          ? "h-16 w-full flex-nowrap gap-3 rounded-none bg-transparent px-3 hover:bg-muted/40 sm:px-4"
          : "h-12 w-56 bg-muted/35 hover:bg-muted/50 dark:bg-white/[0.06] dark:hover:bg-white/[0.09]",
      )}
    >
      <AttachmentMedia className="size-8 bg-transparent text-muted-foreground">
        {React.createElement(fileIcon, { className: "size-5", strokeWidth: 1.6 })}
      </AttachmentMedia>
      <AttachmentContent className="flex min-w-0 flex-1 flex-col justify-center px-0 py-0">
        <AttachmentTitle className={cn("leading-4 text-foreground/90", grouped ? "text-[13px] font-medium" : "text-[12px]")} title={att.fileName}>
          {att.fileName}
        </AttachmentTitle>
        <AttachmentDescription className="mt-1 text-[11px] leading-none">
          {meta}
        </AttachmentDescription>
      </AttachmentContent>
      {grouped ? (
        <AttachmentActions className="gap-2 sm:gap-3">
          {att.sizeBytes > 0 ? <span className="w-16 whitespace-nowrap text-right text-xs tabular-nums text-muted-foreground">{formatBytes(att.sizeBytes)}</span> : null}
          {onDownload ? (
            <AttachmentAction
              size="icon-sm"
              aria-label={`${tActions("download")} ${att.fileName}`}
              title={tActions("download")}
              aria-busy={downloading}
              disabled={downloading}
              onClick={(event) => {
                event.stopPropagation();
                onDownload();
              }}
            >
              {downloading ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" strokeWidth={1.5} />}
            </AttachmentAction>
          ) : null}
        </AttachmentActions>
      ) : null}
      <AttachmentTrigger
        onClick={onClick}
        aria-label={att.fileName}
        className={cn("focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring", grouped ? "rounded-none" : "rounded-lg")}
      />
    </Attachment>
  );
}

// ─── public export ────────────────────────────────────────────────────────────

export function MessageAttachmentRow({
  attachments,
  loadContent,
  allowDownload = true,
  align = "end",
  onOpenAttachment,
  variant = "cards",
  onDownloadAttachment,
  downloadingFileIDs,
}: {
  attachments: MessageAttachment[];
  loadContent?: FileContentLoader;
  allowDownload?: boolean;
  align?: "start" | "end";
  onOpenAttachment?: (attachment: MessageAttachment) => void;
  variant?: "cards" | "list";
  onDownloadAttachment?: (attachment: MessageAttachment) => void;
  downloadingFileIDs?: ReadonlySet<string>;
}) {
  const [activeAtt, setActiveAtt] = React.useState<MessageAttachment | null>(null);
  const [dialogOpen, setDialogOpen] = React.useState(false);

  const handleClick = React.useCallback((att: MessageAttachment) => {
    if (onOpenAttachment) {
      onOpenAttachment(att);
      return;
    }
    setActiveAtt(att);
    setDialogOpen(true);
  }, [onOpenAttachment]);

  const handleOpenChange = React.useCallback((v: boolean) => {
    setDialogOpen(v);
    if (!v) setActiveAtt(null);
  }, []);

  return (
    <>
      <div
        role={variant === "list" ? "list" : undefined}
        className={variant === "list"
          ? "w-full max-w-[640px] min-w-0 divide-y divide-border/60 overflow-hidden rounded-xl border border-border/70 bg-card"
          : `flex max-w-full flex-wrap gap-2 sm:max-w-[70%] ${align === "start" ? "justify-start" : "justify-end"}`}
      >
        {attachments.map((att) => (
          <AttachmentCard
            key={att.fileID}
            att={att}
            grouped={variant === "list"}
            onClick={() => handleClick(att)}
            onDownload={allowDownload && onDownloadAttachment ? () => onDownloadAttachment(att) : undefined}
            downloading={downloadingFileIDs?.has(att.fileID)}
          />
        ))}
      </div>
      {activeAtt ? (
        <LazyFilePreviewDialog
          file={activeAtt}
          open={dialogOpen}
          onOpenChange={handleOpenChange}
          loadContent={loadContent}
          allowDownload={allowDownload}
        />
      ) : null}
    </>
  );
}
