"use client";

import * as React from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { type FileContentLoader, formatBytes } from "@/entities/file";
import type { MessageAttachment } from "@/features/chat/types/messages";
import { resolveMessageFileLink } from "@/features/chat/model/message-file-link";
import { useLocalizedErrorMessage } from "@/i18n/use-localized-error";
import { fetchFileContent } from "@/shared/api/file";
import { resolveApiBaseURL } from "@/shared/api/http-client";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import type { MarkdownFileActions } from "@/shared/components/markdown/streamdown-components";
import { downloadBlob } from "@/shared/lib/export-download";

export function useChatMessageFileActions(
  attachments: MessageAttachment[] | undefined,
  loadContent: FileContentLoader | undefined,
  allowDownload: boolean,
): MarkdownFileActions & {
  inlineImageFileIDs: ReadonlySet<string>;
  downloadingFileIDs: ReadonlySet<string>;
  downloadAttachment: (attachment: MessageAttachment) => void;
} {
  const t = useTranslations("files.previewDialog");
  const resolveErrorMessage = useLocalizedErrorMessage();
  const pending = React.useRef(new Set<string>());
  const [downloadingFileIDs, setDownloadingFileIDs] = React.useState<ReadonlySet<string>>(new Set());
  const [inlineImages, setInlineImages] = React.useState<Record<string, number>>({});
  const resolveAttachment = React.useCallback((href: string) => {
    const origins = [new URL(resolveApiBaseURL()).origin];
    if (typeof window !== "undefined") origins.push(window.location.origin);
    return resolveMessageFileLink(href, attachments ?? [], origins);
  }, [attachments]);
  const onInlineImageMount = React.useCallback((src: string) => {
    const attachment = resolveAttachment(src);
    if (!attachment) return undefined;
    const id = attachment.fileID;
    setInlineImages((current) => ({ ...current, [id]: (current[id] ?? 0) + 1 }));
    return () => setInlineImages((current) => ({ ...current, [id]: Math.max(0, (current[id] ?? 0) - 1) }));
  }, [resolveAttachment]);
  const downloadAttachment = React.useCallback((attachment: MessageAttachment) => {
    if (!allowDownload || pending.current.has(attachment.fileID)) return;
    pending.current.add(attachment.fileID);
    setDownloadingFileIDs(new Set(pending.current));
    void (async () => {
      try {
        const signal = new AbortController().signal;
        const result = loadContent
          ? await loadContent(attachment, signal)
          : await (async () => {
              const token = await resolveAccessToken();
              if (!token) throw new Error(t("sessionExpired"));
              return fetchFileContent(token, attachment.fileID, signal);
            })();
        downloadBlob(result.blob, attachment.fileName);
      } catch (error) {
        toast.error(resolveErrorMessage(error, t("loadFailed")));
      } finally {
        pending.current.delete(attachment.fileID);
        setDownloadingFileIDs(new Set(pending.current));
      }
    })();
  }, [allowDownload, loadContent, resolveErrorMessage, t]);
  return React.useMemo(() => ({
    inlineImageFileIDs: new Set(Object.keys(inlineImages).filter((id) => inlineImages[id] > 0)),
    downloadingFileIDs,
    downloadAttachment,
    onInlineImageMount,
    resolveFileLink: (href: string) => {
      const attachment = resolveAttachment(href);
      if (!attachment) return null;
      return {
        fileName: attachment.fileName,
        sizeLabel: attachment.sizeBytes > 0 ? formatBytes(attachment.sizeBytes) : undefined,
        download: () => downloadAttachment(attachment),
      };
    },
  }), [downloadAttachment, downloadingFileIDs, inlineImages, onInlineImageMount, resolveAttachment]);
}
