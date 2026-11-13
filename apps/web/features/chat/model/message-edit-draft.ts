import type { PendingAttachment } from "@/features/chat/types/chat-runtime";

export function messageAttachmentIDsEqual(left: readonly { fileID: string }[], right: readonly { fileID: string }[]) {
  return left.length === right.length && left.every((item, index) => item.fileID === right[index]?.fileID);
}

export function canSaveMessageDraft(content: string, attachments: readonly PendingAttachment[]) {
  return content.trim().length > 0 || attachments.length > 0;
}

// Editing owns its previews, including when a temporary message retains the same File.
export function createMessageEditAttachments(items: readonly PendingAttachment[]): PendingAttachment[] {
  return items.map((item) => ({
    ...item,
    previewURL: item.localFile && (item.fileCategory === "image" || item.mimeType.startsWith("image/"))
      ? URL.createObjectURL(item.localFile)
      : item.previewURL?.startsWith("blob:") ? undefined : item.previewURL,
  }));
}
