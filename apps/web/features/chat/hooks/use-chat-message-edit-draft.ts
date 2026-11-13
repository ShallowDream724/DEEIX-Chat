"use client";

import * as React from "react";
import { useChatAttachments } from "@/features/chat/hooks/use-chat-attachments";
import { useMessageEditAttachmentContext } from "@/features/chat/context/message-edit-attachment-context";
import { toPendingAttachments } from "@/features/chat/model/message-submit";
import { canSaveMessageDraft, createMessageEditAttachments, messageAttachmentIDsEqual } from "@/features/chat/model/message-edit-draft";
import type { ChatAreaMessage, EditUserMessage, UserMessageEditMode } from "@/features/chat/types/messages";
import { createSecureUUID } from "@/shared/lib/secure-id";

const EMPTY_TOOLS: number[] = [];

export function useChatMessageEditDraft(message: ChatAreaMessage, model: string, onSave: EditUserMessage, onClose: () => void) {
  const context = useMessageEditAttachmentContext();
  const [content, setContent] = React.useState(message.content);
  const [attachments, setAttachments] = React.useState(() => createMessageEditAttachments(context.resolveAttachments?.(message) ?? toPendingAttachments(message)));
  const [saving, setSaving] = React.useState(false);
  const savingRef = React.useRef(false);
  const revisionRef = React.useRef<{ fingerprint: string; id: string } | null>(null);
  const append = React.useCallback((_key: string, items: typeof attachments) => setAttachments((current) => [...current, ...items]), []);
  const uploads = useChatAttachments({
    conversationKey: `edit:${message.publicID || message.key}`,
    conversationID: context.conversationID,
    model,
    parentMessagePublicID: message.parentPublicID,
    selectedToolIDs: context.selectedToolIDs ?? EMPTY_TOOLS,
    attachments,
    setAttachments,
    updateAttachmentStatuses: setAttachments,
    appendAttachmentsForKey: append,
    temporary: context.temporary,
  });
  const attachmentsChanged = !messageAttachmentIDsEqual(attachments, message.attachments ?? []);
  const changed = content.trim() !== message.content.trim() || attachmentsChanged;
  const valid = canSaveMessageDraft(content, attachments);
  const save = React.useCallback(async (mode: UserMessageEditMode) => {
    if (savingRef.current || uploads.uploading || !changed || !valid) return;
    if (mode === "regenerate" && (uploads.nativeReadinessPending || uploads.nativeInputIssues.length > 0)) return;
    savingRef.current = true;
    setSaving(true);
    const fingerprint = JSON.stringify([content.trim(), attachments.map((item) => item.fileID)]);
    if (revisionRef.current?.fingerprint !== fingerprint) revisionRef.current = { fingerprint, id: createSecureUUID() };
    try {
      const ok = await onSave(message, content.trim(), mode, attachments, revisionRef.current.id);
      if (ok !== false) onClose();
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }, [attachments, changed, content, message, onClose, onSave, uploads.nativeInputIssues.length, uploads.nativeReadinessPending, uploads.uploading, valid]);
  return { content, setContent, attachments, attachmentsChanged, changed, valid, saving, save, uploads };
}
