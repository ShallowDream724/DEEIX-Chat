"use client";

import { createContext, useContext } from "react";
import type { FileContentLoader, PreviewDialogFile } from "@/entities/file";

export type ChatFilePreviewRequest = {
  file: PreviewDialogFile;
  loadContent?: FileContentLoader;
  allowDownload?: boolean;
};

// The host owns placement; attachment cards retain their standalone dialog
// when rendered outside a chat workspace (for example a shared snapshot).
export const ChatFilePreviewContext = createContext<((request: ChatFilePreviewRequest) => void) | null>(null);

export function useChatFilePreview() {
  return useContext(ChatFilePreviewContext);
}

export function localAttachmentContentLoader(source: File | string): FileContentLoader {
  return async (_attachment, signal) => {
    signal.throwIfAborted();
    const file = typeof source === "string" ? await (await fetch(source, { signal })).blob() : source;
    return {
      blob: file,
      contentType: file.type || "application/octet-stream",
      disposition: null,
      contentLength: file.size,
    };
  };
}
