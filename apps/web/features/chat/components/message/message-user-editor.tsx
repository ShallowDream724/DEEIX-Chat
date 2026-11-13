"use client";

import * as React from "react";
import { CircleAlert, LoaderCircle, Paperclip, Replace } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { LazyFilePreviewDialog, type FileContentLoader } from "@/entities/file";
import { ChatMentionMenuPortal } from "@/features/chat/components/shared/chat-mention-menu";
import { ChatComposerAttachment } from "@/features/chat/components/sections/chat-composer-attachment";
import { useChatMentionMenu } from "@/features/chat/hooks/use-chat-mention-menu";
import { useChatMessageEditDraft } from "@/features/chat/hooks/use-chat-message-edit-draft";
import type { ChatAreaMessage, EditUserMessage } from "@/features/chat/types/messages";
import type { ChatModelOption, PendingAttachment } from "@/features/chat/types/chat-runtime";
import type { SendShortcut } from "@/features/settings";
import { isImeComposingKeyEvent, isSendShortcutEvent } from "@/shared/lib/platform-shortcuts";

const EMPTY_TOOLS: [] = [];
const MENTION_KINDS = ["model", "prompt"] as const;

export function MessageUserEditor({ item, onSave, onClose, modelOptions, selectedPlatformModelName, onModelChange, onModelCatalogRefresh, sendShortcut, attachmentContentLoader }: {
  item: ChatAreaMessage;
  onSave: EditUserMessage;
  onClose: () => void;
  modelOptions: ChatModelOption[];
  selectedPlatformModelName: string;
  onModelChange: (model: string) => void;
  onModelCatalogRefresh?: () => void | Promise<void>;
  sendShortcut: SendShortcut;
  attachmentContentLoader?: FileContentLoader;
}) {
  const t = useTranslations("chat.messages");
  const tComposer = useTranslations("chat.composer");
  const tCommon = useTranslations("common.actions");
  const draft = useChatMessageEditDraft(item, selectedPlatformModelName, onSave, onClose);
  const [preview, setPreview] = React.useState<PendingAttachment | null>(null);
  const localPreviewFile = preview?.localFile;
  const fileInput = React.useRef<HTMLInputElement>(null);
  const replaceID = React.useRef<string | null>(null);
  const inputGroup = React.useRef<HTMLDivElement>(null);
  const textarea = React.useRef<HTMLTextAreaElement>(null);
  const composing = React.useRef(false);
  const disabled = draft.saving || draft.uploads.uploading;
  const canSave = draft.changed && draft.valid && !disabled;
  const canRegenerate = canSave && !draft.uploads.nativeReadinessPending && draft.uploads.nativeInputIssues.length === 0;
  const upload = async (files: File[]) => {
    if (disabled || files.length === 0) return;
    const target = replaceID.current;
    replaceID.current = null;
    const uploaded = await draft.uploads.onUploadFiles(target ? files.slice(0, 1) : files);
    if (target && uploaded.length > 0 && !uploaded.some((file) => file.fileID === target)) draft.uploads.onRemoveAttachment(target);
  };
  const mention = useChatMentionMenu({
    attachments: draft.attachments, availableTools: EMPTY_TOOLS, defaultFileLabel: "", disabled,
    draft: draft.content, enabledKinds: MENTION_KINDS, maxSelectedSkills: 0, maxSelectedTools: 0,
    modelOptions, selectedSkills: EMPTY_TOOLS, selectedPlatformModelName, selectedToolIDs: EMPTY_TOOLS,
    anchorRef: inputGroup, textareaRef: textarea, toolsDisabled: true, onDraftChange: draft.setContent,
    onFileSelect: () => undefined, onModelCatalogRefresh, onModelChange, placementPreference: "bottom", onSelectedToolsChange: () => undefined,
  });
  return (
    <div className="flex justify-end">
      <div className="w-full min-w-0 max-w-[640px] rounded-lg bg-muted/60 p-3 text-foreground" aria-busy={draft.saving}
        onDragOver={(event) => { if (event.dataTransfer.types.includes("Files")) event.preventDefault(); }}
        onDrop={(event) => { if (event.dataTransfer.files.length) { event.preventDefault(); replaceID.current = null; void upload(Array.from(event.dataTransfer.files)); } }}>
        <div ref={inputGroup}>
          <ChatMentionMenuPortal activeRowKey={mention.activeRowKey} activeTab={mention.activeTab} menuID={mention.menuID} menuLayout={mention.menuLayout} menuRef={mention.menuRef} menuReady={mention.menuReady} open={mention.open} rows={mention.rows} showTabBar={mention.showTabBar} tabs={mention.tabs} t={tComposer} onListScroll={mention.handleListScroll} onSelect={mention.select} onSelectTab={mention.selectTab} />
          {draft.attachments.length > 0 || draft.uploads.uploading ? (
            <div className="mb-2 flex min-w-0 flex-wrap items-start gap-2">
              {draft.attachments.map((file) => (
                <div key={file.fileID} inert={disabled} className="flex max-w-full flex-col gap-0.5">
                  <ChatComposerAttachment file={file} nativeReady={draft.uploads.nativeBadgeFileIDs.has(file.fileID)} issue={draft.uploads.nativeInputIssues.find((issue) => issue.fileID === file.fileID)} onPreview={setPreview} onRemove={(id) => { if (!disabled) draft.uploads.onRemoveAttachment(id); }} />
                  <Button size="sm" variant="ghost" className="h-7 self-start px-1.5 text-xs text-muted-foreground" disabled={disabled} onClick={() => { replaceID.current = file.fileID; if (fileInput.current) { fileInput.current.multiple = false; fileInput.current.click(); } }}><Replace className="size-3" />{t("editReplaceAttachment")}</Button>
                </div>
              ))}
              {draft.uploads.uploadingAttachments.map((file) => <div key={file.tempID} className="flex h-12 max-w-full items-center gap-2 rounded-md bg-muted/35 px-3 text-xs text-muted-foreground"><LoaderCircle className="size-3 animate-spin" /><span className="max-w-40 truncate">{file.fileName}</span></div>)}
            </div>
          ) : null}
          <Textarea ref={textarea} autoFocus disabled={draft.saving} value={draft.content} className="chat-font-content min-h-[120px] resize-none rounded-lg border-border border-[0.5px] bg-background px-3 py-2 text-sm leading-7 shadow-none focus-visible:border-primary focus-visible:ring-0" style={{ fontFamily: "var(--font-chat)", fontWeight: "var(--font-chat-weight)" }}
            aria-controls={mention.open ? mention.menuID : undefined} aria-expanded={mention.open}
            onBlur={mention.handleBlur} onFocus={mention.handleFocus} onChange={(event) => mention.handleChange(event.target.value)} onClick={mention.handleSelectionChange} onSelect={mention.handleSelectionChange} onKeyUp={mention.handleSelectionChange}
            onCompositionStart={() => { composing.current = true; }} onCompositionEnd={() => { composing.current = false; }}
            onPaste={(event) => { const files = Array.from(event.clipboardData.files); if (files.length) { event.preventDefault(); replaceID.current = null; void upload(files); } }}
            onKeyDown={(event) => { if (mention.handleKeyDown(event) || isImeComposingKeyEvent(event, composing.current) || !isSendShortcutEvent(sendShortcut, event)) return; event.preventDefault(); if (canRegenerate) void draft.save("regenerate"); }} />
        </div>
        <input ref={fileInput} type="file" className="hidden" multiple onChange={(event) => { const files = Array.from(event.target.files ?? []); event.target.value = ""; void upload(files); }} />
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
          <Button size="sm" variant="ghost" className="h-8 px-2 text-xs text-muted-foreground" disabled={disabled} onClick={() => { replaceID.current = null; if (fileInput.current) { fileInput.current.multiple = true; fileInput.current.click(); } }}><Paperclip className="size-3.5" />{tComposer("uploadFile")}</Button>
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1.5">
            <Button size="sm" variant="ghost" disabled={draft.saving} className="text-xs" onClick={onClose}>{tCommon("cancel")}</Button>
            <Button size="sm" variant="outline" disabled={!canSave} className="text-xs shadow-none" onClick={() => void draft.save("save")} title={t(draft.attachmentsChanged ? "editSaveRevisionHint" : "editSaveOnlyHint")}>{t("editSaveOnly")}</Button>
            <Button size="sm" disabled={!canRegenerate} className="text-xs shadow-none" onClick={() => void draft.save("regenerate")} title={t("editRegenerateHint")}>{draft.saving ? <LoaderCircle className="size-3 animate-spin" /> : null}{t("editRegenerate")}</Button>
          </div>
        </div>
        <div className="mt-2 flex items-start gap-1.5 text-xs leading-5 text-muted-foreground"><CircleAlert className="mt-0.5 size-3 shrink-0" /><span>{t(draft.attachmentsChanged ? "editAttachmentModes" : "editModes")}</span></div>
        <LazyFilePreviewDialog file={preview} open={preview !== null} onOpenChange={(open) => { if (!open) setPreview(null); }} loadContent={localPreviewFile ? async () => ({ blob: localPreviewFile, contentType: localPreviewFile.type || "application/octet-stream", disposition: null, contentLength: localPreviewFile.size }) : attachmentContentLoader} />
      </div>
    </div>
  );
}
