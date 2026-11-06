"use client";

import { LoaderCircle, TextQuote } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { X as XIcon } from "@/components/animate-ui/icons/x";
import {
  Attachment, AttachmentAction, AttachmentActions, AttachmentContent,
  AttachmentDescription, AttachmentMedia, AttachmentTitle, AttachmentTrigger,
} from "@/components/ui/attachment";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { FileThumbnail, formatBytes, isFileProcessing, isImageFile, resolveFileExtension, resolveFileIcon, resolveFileProcessingBadge } from "@/entities/file";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";
import { normalizeAppLocale } from "@/i18n/config";
import { resolveInputLimitPresentation } from "@/i18n/resolve-error-message";
import type { InputLimitReason } from "@/shared/lib/input-limit";

export function ChatComposerAttachment({
  file, nativeReady, issue, onPreview, onRemove, onRestoreText,
}: {
  file: PendingAttachment;
  nativeReady: boolean;
  issue?: InputLimitReason;
  onPreview: (file: PendingAttachment) => void;
  onRemove: (fileID: string) => void;
  onRestoreText?: (fileID: string) => void;
}) {
  const t = useTranslations("chat.composer");
  const tStatus = useTranslations("files.status");
  const locale = normalizeAppLocale(useLocale());
  const badge = resolveFileProcessingBadge(file, (key, values) => tStatus(key, values));
  const image = isImageFile(file);
  const Icon = resolveFileIcon(file);
  const failed = Boolean(issue) || (!nativeReady && (badge.tone === "danger" || badge.tone === "warning"));
  const processing = !nativeReady && !failed && isFileProcessing(file);
  const meta = `${resolveFileExtension(file.fileName).toUpperCase() || "FILE"} · ${formatBytes(file.sizeBytes)}`;
  const issueLabel = issue?.dimension === "fileUnsupported" ? t("attachmentUnsupported")
    : issue?.dimension === "fileUnavailable" ? t("attachmentUnavailable")
    : issue?.limit && ["imageSingleBytes", "pdfSingleBytes", "attachmentSingleBytes", "uploadFileBytes"].includes(issue.dimension)
      ? t("attachmentSizeExceeded", { limit: formatBytes(issue.limit) })
      : issue ? resolveInputLimitPresentation(issue, locale).title : undefined;
  const statusLabel = issueLabel || (failed || processing ? badge.label : undefined);
  const removeAction = (
    <Tooltip>
      <TooltipTrigger asChild>
        <AttachmentAction
          type="button"
          className={image
            ? "relative size-8 rounded-full text-white before:absolute before:inset-1 before:rounded-full before:bg-black/60 hover:bg-transparent hover:text-white hover:before:bg-black/75"
            : "size-8 rounded-md text-muted-foreground hover:bg-accent hover:text-foreground sm:size-7"}
          onClick={() => onRemove(file.fileID)}
          aria-label={t("removeAttachment", { name: file.fileName })}
        ><XIcon className="relative" size={image ? 13 : 15} strokeWidth={1.8} animateOnHover="default" /></AttachmentAction>
      </TooltipTrigger>
      <TooltipContent side="top" className="text-xs">{t("removeAttachmentTooltip")}</TooltipContent>
    </Tooltip>
  );

  if (image) return (
    <Attachment state={failed ? "error" : "done"} size="sm" className="size-20 min-w-0 overflow-hidden border-0 bg-muted/35 p-0">
      <FileThumbnail file={file} />
      <AttachmentTrigger onClick={() => onPreview(file)} aria-label={t("previewAttachment", { name: file.fileName })} />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/85 via-black/45 to-transparent px-1.5 pb-1 pt-4 text-[10px] font-medium leading-3 text-white">
        <span className="block truncate">{statusLabel || formatBytes(file.sizeBytes)}</span>
      </div>
      <AttachmentActions className="absolute right-0 top-0">{removeAction}</AttachmentActions>
    </Attachment>
  );

  const trigger = <AttachmentTrigger onClick={() => onPreview(file)} aria-label={t("previewAttachment", { name: file.fileName })} />;
  return (
    <Attachment state={failed ? "error" : "done"} aria-busy={processing} size="sm" className="h-12 w-[168px] flex-nowrap border-0 bg-muted/35 px-2 text-left hover:bg-muted/50 dark:bg-white/[0.06] dark:hover:bg-white/[0.09] sm:w-[228px] sm:px-2.5">
      <AttachmentMedia className="size-7 bg-transparent text-muted-foreground"><Icon className="size-5" /></AttachmentMedia>
      <AttachmentContent className="flex min-w-0 flex-1 flex-col justify-center px-0 py-0">
        <AttachmentTitle className="text-[12px] leading-4 text-foreground/90">{file.fileName}</AttachmentTitle>
        <AttachmentDescription className="mt-1 flex min-w-0 items-center gap-1.5 text-[11px] font-normal leading-none">
          {nativeReady && !failed ? <span className="inline-flex h-3 shrink-0 items-center justify-center rounded bg-emerald-500/10 px-1 text-[10px] font-normal leading-none text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-400">{t("nativeAttachment")}</span>
            : processing ? <LoaderCircle className="size-3 shrink-0 animate-spin" strokeWidth={1.8} /> : null}
          <span className="min-w-0 shrink truncate">{issueLabel || (nativeReady ? `· ${meta}` : statusLabel ? `${statusLabel} · ${meta}` : meta)}</span>
          {file.ragOptOut ? <span className="shrink-0 rounded-md bg-muted/60 px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground/65" title={t("ragDisabledTitle")}>{t("ragOff")}</span> : null}
        </AttachmentDescription>
      </AttachmentContent>
      {!issue && (failed || processing) && badge.detail ? <Tooltip><TooltipTrigger asChild>{trigger}</TooltipTrigger><TooltipContent side="top" className="max-w-72 leading-5 text-pretty">{badge.detail}</TooltipContent></Tooltip> : trigger}
      <AttachmentActions>
        {onRestoreText ? <Tooltip><TooltipTrigger asChild><AttachmentAction type="button" className="size-8 rounded-md text-muted-foreground hover:bg-accent hover:text-foreground sm:size-7" onClick={() => onRestoreText(file.fileID)} aria-label={t("insertPastedText", { name: file.fileName })}><TextQuote size={15} strokeWidth={1.8} /></AttachmentAction></TooltipTrigger><TooltipContent side="top" className="text-xs">{t("insertPastedTextTooltip")}</TooltipContent></Tooltip> : null}
        {removeAction}
      </AttachmentActions>
    </Attachment>
  );
}
