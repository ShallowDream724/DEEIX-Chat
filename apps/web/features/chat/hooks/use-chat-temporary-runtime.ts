"use client";

import { UI_BLOCK_FORMAT_VERSION } from "@deeix/core";
import { useTranslations } from "next-intl";
import * as React from "react";
import { toast } from "sonner";

import type { RestoreSubmittedDraft } from "@/features/chat/hooks/use-chat-composer-state";
import { getInputLimitReasons, type InputLimitReason } from "@/shared/lib/input-limit";
import { mergeChatOptionsOverride, splitControlSelections } from "@/features/chat/model/chat-model-controls";
import { mapServerMessage } from "@/features/chat/model/chat-thread";
import { mergeProcessTraceSnapshot, toPendingProcessTrace } from "@/features/chat/model/message-submit";
import {
  clearLiveUpstreamThinkTrace,
  upsertLiveUpstreamThinkTrace,
} from "@/features/chat/model/upstream-think-store";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";
import type { ChatAreaMessage, MessageAttachment, UserMessageEditMode } from "@/features/chat/types/messages";
import {
  resolveErrorDetails,
  resolveGenerationErrorId,
  resolveErrorReason,
  resolveErrorSummary,
} from "@/features/chat/utils/chat-runtime";
import { prepareTemporaryChatRequest, streamTemporaryChatMessage } from "@/shared/api/conversation";
import type { TemporaryChatRequestAttachment } from "@/shared/api/conversation";
import type {
  ConversationOptions,
  MessageUIStateInput,
  TemporaryChatHistoryMessage,
  TemporaryChatUIStateInput,
} from "@/shared/api/conversation-types";
import type { FileContentLoader } from "@/entities/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { createSecureUUID } from "@/shared/lib/secure-id";

type TemporaryMessage = TemporaryChatHistoryMessage & {
  id: string;
  historyOffset: number;
  parentID?: string;
  model?: string;
  runID?: string;
  streaming?: boolean;
  failed?: boolean;
  inputTokens?: number;
  outputTokens?: number;
  latencyMS?: number;
  reasoningEffort?: ChatAreaMessage["reasoningEffort"];
  activityLabel?: string;
  processTrace?: ChatAreaMessage["processTrace"];
  knowledgeSources?: ChatAreaMessage["knowledgeSources"];
  inlineAlert?: ChatAreaMessage["inlineAlert"];
  attachments?: MessageAttachment[];
  localAttachments?: PendingAttachment[];
};

type TemporaryHistoryMessage = TemporaryChatHistoryMessage & {
  attachments?: PendingAttachment[];
};

type TemporaryChatRuntimeInput = {
  active: boolean;
  draft: string;
  model: string;
  options: ConversationOptions;
  selectedToolIDs: number[];
  selectedSkillIDs: number[];
  selectedKnowledgeBaseIDs: string[];
  uiComponentIDs: number[];
  htmlVisualPromptEnabled: boolean;
  attachments: PendingAttachment[];
  onDraftChange: (value: string) => void;
  getComposerRevision: () => number;
  restoreSubmittedDraft: RestoreSubmittedDraft;
  restoreDraftOnFailure: boolean;
  onInputLimit: (reasons: readonly InputLimitReason[]) => void;
  maxTemporaryAttachments?: number;
  onAttachmentsConsumed: (items: PendingAttachment[]) => void;
  releaseAttachments: (items: PendingAttachment[]) => void;
  // Interactive component state of earlier replies, attached to the next turn.
  // Temporary chats are never stored, so this is the only place it goes.
  collectUIStates?: (messages: readonly ChatAreaMessage[]) => MessageUIStateInput[];
};

type SubmitTemporaryTurnInput = {
  content: string;
  localAttachments: PendingAttachment[];
  baseHistory: TemporaryHistoryMessage[];
  replaceFromIndex?: number;
  consumeComposer: boolean;
  // Single-request option overrides merged over the composer options (e.g. "Regenerate with…").
  optionsOverride?: ConversationOptions;
};

function toMessageAttachment(item: PendingAttachment): MessageAttachment {
  return {
    fileID: item.fileID,
    fileName: item.fileName,
    mimeType: item.mimeType,
    detectedMime: item.detectedMime,
    fileCategory: item.fileCategory,
    sizeBytes: item.sizeBytes,
    kind: item.fileCategory === "image" || item.mimeType.startsWith("image/") ? "image" : "file",
    previewURL: item.previewURL,
    processingStatus: item.processingStatus,
    processingReady: item.processingReady,
    extractStatus: item.extractStatus,
    embedStatus: item.embedStatus,
    ragReady: item.ragReady,
    ragReason: item.ragReason,
    ocrUsed: item.ocrUsed,
  };
}

function toAreaMessage(message: TemporaryMessage): ChatAreaMessage {
  return {
    key: message.id,
    publicID: message.id,
    parentPublicID: message.parentID ?? null,
    sourcePublicID: null,
    role: message.role,
    content: message.content,
    branchReason: "default",
    status: message.failed ? "failed" : message.streaming ? "processing" : "completed",
    isStreaming: message.streaming,
    platformModelName: message.model,
    runID: message.runID,
    inputTokens: message.inputTokens,
    outputTokens: message.outputTokens,
    latencyMS: message.latencyMS,
    reasoningEffort: message.reasoningEffort,
    activityLabel: message.activityLabel,
    processTrace: message.processTrace,
    knowledgeSources: message.knowledgeSources,
    inlineAlert: message.inlineAlert,
    attachments: message.attachments,
  };
}

// Temporary chats have no message ids on the server: a block is located by the
// index of its reply in the request's `messages`. A reply sits right after its
// user turn in the full history.
function toTemporaryUIStates(
  entries: readonly MessageUIStateInput[],
  messages: readonly TemporaryMessage[],
  fullHistory: readonly TemporaryHistoryMessage[],
): TemporaryChatUIStateInput[] {
  const result: TemporaryChatUIStateInput[] = [];
  for (const entry of entries) {
    const message = messages.find((item) => item.id === entry.messagePublicID && item.role === "assistant");
    if (!message) {
      continue;
    }
    const messageIndex = message.historyOffset + 1;
    if (fullHistory[messageIndex]?.role !== "assistant") {
      continue;
    }
    result.push({ messageIndex, blockID: entry.blockID, title: entry.title, state: entry.state });
  }
  return result;
}

function isImageAttachment(item: PendingAttachment): boolean {
  return item.fileCategory === "image" || item.mimeType.startsWith("image/");
}

function prepareTemporaryRequestHistory(history: TemporaryHistoryMessage[]) {
  return {
    messages: history.map(({ role, content }): TemporaryChatHistoryMessage => ({ role, content: content.trim() })),
    attachments: history.flatMap((message, messageIndex) =>
      (message.attachments ?? []).map((attachment): TemporaryChatRequestAttachment => {
        if (!attachment.localFile) throw new Error("Temporary attachment is unavailable");
        return {
          file: attachment.localFile,
          kind: isImageAttachment(attachment) ? "image" : "file",
          messageIndex,
        };
      }),
    ),
  };
}

function getTemporaryHistoryIssues(
  history: TemporaryHistoryMessage[],
  current: TemporaryHistoryMessage,
  maxAttachments?: number,
): InputLimitReason[] {
  const allMessages = [...history, current];
  const issues: InputLimitReason[] = [];
  const maxMessages = 100;
  const maxChars = 1_000_000;
  const maxMessageChars = 200_000;
  if (allMessages.length > maxMessages) {
    issues.push({ dimension: "temporaryMessageCount", actual: allMessages.length, limit: maxMessages });
  }
  // Only count Unicode code points when the cheap UTF-16 upper bound can exceed a limit.
  const mayExceedTotal = allMessages.reduce((total, message) => total + message.content.length, 0) > maxChars;
  let totalChars = 0;
  for (const message of allMessages) {
    const content = message.content.trim();
    if (!mayExceedTotal && content.length <= maxMessageChars) continue;
    let chars = 0;
    for (let index = 0; index < content.length; index++, chars++) {
      if ((content.codePointAt(index) ?? 0) > 0xffff) index++;
    }
    totalChars += chars;
    if (chars > maxMessageChars) {
      issues.push({
        dimension: "temporarySingleTextLength",
        actual: chars,
        limit: maxMessageChars,
        scope: message === current ? "current" : "history",
      });
    }
  }
  if (totalChars > maxChars) issues.push({ dimension: "temporaryTextLength", actual: totalChars, limit: maxChars });
  const attachments = allMessages.flatMap((message) => message.attachments ?? []);
  if (maxAttachments !== undefined && maxAttachments > 0 && attachments.length > maxAttachments) {
    issues.push({ dimension: "temporaryFileCount", actual: attachments.length, limit: maxAttachments });
  }
  for (const message of allMessages) {
    for (const attachment of message.attachments ?? []) {
      if (!(attachment.localFile instanceof File)) {
        issues.push({
          dimension: "fileUnavailable",
          fileID: attachment.fileID,
          fileName: attachment.fileName,
          scope: message === current ? "current" : "history",
        });
      }
    }
  }
  return issues;
}

export function useChatTemporaryRuntime({
  active,
  draft,
  model,
  options,
  selectedToolIDs,
  selectedSkillIDs,
  selectedKnowledgeBaseIDs,
  uiComponentIDs,
  htmlVisualPromptEnabled,
  attachments,
  onDraftChange,
  getComposerRevision,
  restoreSubmittedDraft,
  restoreDraftOnFailure,
  onInputLimit,
  maxTemporaryAttachments,
  onAttachmentsConsumed,
  releaseAttachments,
  collectUIStates,
}: TemporaryChatRuntimeInput) {
  const t = useTranslations("chat.temporary");
  const tSubmit = useTranslations("chat.submit");
  const [messages, setMessages] = React.useState<TemporaryMessage[]>([]);
  const [sending, setSending] = React.useState(false);
  const messagesRef = React.useRef<TemporaryMessage[]>([]);
  const historyRef = React.useRef<TemporaryHistoryMessage[]>([]);
  const [history, setHistory] = React.useState<TemporaryHistoryMessage[]>([]);
  const replaceHistory = React.useCallback((next: TemporaryHistoryMessage[]) => {
    historyRef.current = next;
    setHistory(next);
  }, []);
  const retainedAttachmentsRef = React.useRef(new Map<string, PendingAttachment>());
  const sessionIDRef = React.useRef("");
  const abortControllerRef = React.useRef<AbortController | null>(null);
  const sendingRef = React.useRef(false);
  const liveRunIDsRef = React.useRef(new Set<string>());

  const clearLiveTraces = React.useCallback(() => {
    liveRunIDsRef.current.forEach(clearLiveUpstreamThinkTrace);
    liveRunIDsRef.current.clear();
  }, []);

  const clearRetainedAttachments = React.useCallback(() => {
    const retained = Array.from(retainedAttachmentsRef.current.values());
    retainedAttachmentsRef.current.clear();
    if (retained.length > 0) {
      releaseAttachments(retained);
    }
  }, [releaseAttachments]);

  const replaceMessages = React.useCallback((next: TemporaryMessage[]) => {
    messagesRef.current = next;
    setMessages(next);
  }, []);

  const updateMessage = React.useCallback(
    (messageID: string, update: (message: TemporaryMessage) => TemporaryMessage) => {
      replaceMessages(messagesRef.current.map((message) =>
        message.id === messageID ? update(message) : message
      ));
    },
    [replaceMessages],
  );

  const replaceMessageTail = React.useCallback((
    fromIndex: number,
    replacements: TemporaryMessage[],
    preservedAttachmentIDs: ReadonlySet<string> = new Set(),
  ) => {
    const current = messagesRef.current;
    const released: PendingAttachment[] = [];
    for (const message of current.slice(fromIndex)) {
      if (message.runID) {
        clearLiveUpstreamThinkTrace(message.runID);
        liveRunIDsRef.current.delete(message.runID);
      }
      for (const attachment of message.localAttachments ?? []) {
        if (preservedAttachmentIDs.has(attachment.fileID)) {
          continue;
        }
        const retained = retainedAttachmentsRef.current.get(attachment.fileID);
        if (retained) {
          retainedAttachmentsRef.current.delete(attachment.fileID);
          released.push(retained);
        }
      }
    }
    if (released.length > 0) {
      releaseAttachments(released);
    }
    replaceMessages([...current.slice(0, fromIndex), ...replacements]);
  }, [releaseAttachments, replaceMessages]);

  const finishSending = React.useCallback((controller: AbortController) => {
    if (abortControllerRef.current !== controller) {
      return;
    }
    abortControllerRef.current = null;
    setSending(false);
    sendingRef.current = false;
  }, []);

  const startNew = React.useCallback(() => {
    abortControllerRef.current?.abort();
    abortControllerRef.current = null;
    sendingRef.current = false;
    sessionIDRef.current = "";
    replaceHistory([]);
    clearRetainedAttachments();
    clearLiveTraces();
    setSending(false);
    replaceMessages([]);
  }, [clearLiveTraces, clearRetainedAttachments, replaceHistory, replaceMessages]);

  React.useEffect(() => {
    if (!active) startNew();
  }, [active, startNew]);

  React.useEffect(() => {
    const abort = () => abortControllerRef.current?.abort();
    window.addEventListener("pagehide", abort);
    return () => {
      window.removeEventListener("pagehide", abort);
      abort();
      sessionIDRef.current = "";
      historyRef.current = [];
      clearRetainedAttachments();
      clearLiveTraces();
    };
  }, [clearLiveTraces, clearRetainedAttachments]);

  const stop = React.useCallback(() => {
    abortControllerRef.current?.abort();
  }, []);

  const loadAttachmentContent = React.useCallback<FileContentLoader>(async (file, signal) => {
    if (signal.aborted) {
      throw new DOMException("The operation was aborted", "AbortError");
    }
    const source = retainedAttachmentsRef.current.get(file.fileID)?.localFile;
    if (!source) {
      throw new Error("Temporary attachment is no longer available");
    }
    return {
      blob: source,
      contentType: source.type || "application/octet-stream",
      disposition: null,
      contentLength: source.size,
    };
  }, []);

  const submitTurn = React.useCallback(async ({
    content,
    localAttachments,
    baseHistory,
    replaceFromIndex,
    consumeComposer,
    optionsOverride,
  }: SubmitTemporaryTurnInput): Promise<boolean> => {
    const normalizedContent = content.trim();
    const selectedModel = model.trim();
    if (
      !active ||
      (!normalizedContent && localAttachments.length === 0) ||
      !selectedModel ||
      sendingRef.current
    ) {
      return false;
    }
    const userHistory: TemporaryHistoryMessage = {
      role: "user",
      content: normalizedContent,
      attachments: localAttachments,
    };
    const issues = getTemporaryHistoryIssues(baseHistory, userHistory, maxTemporaryAttachments);
    if (issues.length > 0) {
      onInputLimit(issues);
      return false;
    }

    const initialRevision = getComposerRevision();
    sendingRef.current = true;
    setSending(true);
    const controller = new AbortController();
    abortControllerRef.current = controller;
    const token = await resolveAccessToken().catch(() => "");
    if (controller.signal.aborted || (consumeComposer && initialRevision !== getComposerRevision())) {
      finishSending(controller);
      return false;
    }
    if (!token) {
      finishSending(controller);
      toast.error(t("sessionExpired"));
      return false;
    }
    if (!sessionIDRef.current) {
      sessionIDRef.current = createSecureUUID();
    }

    const historyOffset = baseHistory.length;
    const currentMessages = messagesRef.current;
    const insertionIndex = replaceFromIndex ?? currentMessages.length;
    const userMessage: TemporaryMessage = {
      id: createSecureUUID(),
      role: "user",
      content: normalizedContent,
      historyOffset,
      parentID: currentMessages[insertionIndex - 1]?.id,
      attachments: localAttachments.map(toMessageAttachment),
      localAttachments,
    };
    const assistantID = createSecureUUID();
    const clientRunID = createSecureUUID();
    const fullHistory = [...baseHistory, userHistory];
    let preparedBody: FormData | string;
    try {
      const preparedRequest = prepareTemporaryRequestHistory(fullHistory);
      const turnUIStates = collectUIStates
        ? toTemporaryUIStates(
            collectUIStates(currentMessages.slice(0, insertionIndex).map(toAreaMessage)),
            currentMessages,
            fullHistory,
          )
        : [];
      const requestOptions = splitControlSelections(mergeChatOptionsOverride(options, optionsOverride));
      preparedBody = await prepareTemporaryChatRequest(token, {
        sessionID: sessionIDRef.current,
        clientRunID,
        model: selectedModel,
        options: requestOptions.options,
        controls: requestOptions.controls,
        selectedToolIDs: selectedToolIDs.length > 0 ? selectedToolIDs : undefined,
        skillIDs: selectedSkillIDs.length > 0 ? selectedSkillIDs : undefined,
        knowledgeBaseIDs: selectedKnowledgeBaseIDs.length > 0 ? selectedKnowledgeBaseIDs : undefined,
        htmlVisualPrompt: htmlVisualPromptEnabled || undefined,
        uiComponentIDs: uiComponentIDs.length > 0 ? uiComponentIDs : undefined,
        uiBlockVersion: uiComponentIDs.length > 0 ? UI_BLOCK_FORMAT_VERSION : undefined,
        uiStates: turnUIStates.length > 0 ? turnUIStates : undefined,
        messages: preparedRequest.messages,
      }, preparedRequest.attachments, controller.signal);
    } catch (error) {
      if (!controller.signal.aborted) {
        const inputLimits = getInputLimitReasons(error);
        if (inputLimits.length > 0) onInputLimit(inputLimits);
        else toast.error(tSubmit("sendFailed"), { description: resolveErrorSummary(error, tSubmit("retryLater")) });
      }
      finishSending(controller);
      return false;
    }
    if (controller.signal.aborted || (consumeComposer && initialRevision !== getComposerRevision())) {
      finishSending(controller);
      return false;
    }

    for (const attachment of localAttachments) {
      retainedAttachmentsRef.current.set(attachment.fileID, attachment);
    }
    if (consumeComposer) {
      onAttachmentsConsumed(localAttachments);
      onDraftChange("");
    }
    const composerRevision = consumeComposer ? getComposerRevision() : undefined;
    replaceHistory(baseHistory);
    const replacements: TemporaryMessage[] = [
      userMessage,
      {
        id: assistantID,
        role: "assistant",
        content: "",
        historyOffset,
        parentID: userMessage.id,
        model: selectedModel,
        runID: clientRunID,
        streaming: true,
      },
    ];
    if (replaceFromIndex === undefined) {
      replaceMessages([...currentMessages, ...replacements]);
    } else {
      replaceMessageTail(
        replaceFromIndex,
        replacements,
        new Set(localAttachments.map((item) => item.fileID)),
      );
    }

    let streamedAssistantText = "";
    let moderationBlocked = false;

    try {
      const completed = await streamTemporaryChatMessage(
        token,
        preparedBody,
        {
          signal: controller.signal,
          onDelta: (delta) => {
            streamedAssistantText += delta;
            updateMessage(assistantID, (message) => ({
              ...message,
              content: `${message.content}${delta}`,
              activityLabel: undefined,
            }));
          },
          onRagSearch: (message) => {
            updateMessage(assistantID, (item) => ({ ...item, activityLabel: message }));
          },
          onProcessUpdate: (event) => {
            updateMessage(assistantID, (message) => ({
              ...message,
              activityLabel: undefined,
              processTrace: event.trace
                ? mergeProcessTraceSnapshot(message.processTrace, toPendingProcessTrace(event.trace))
                : message.processTrace,
            }));
          },
          onUpstreamThinkDelta: (event) => {
            liveRunIDsRef.current.add(clientRunID);
            upsertLiveUpstreamThinkTrace(clientRunID, event);
          },
          onUsage: (event) => {
            updateMessage(assistantID, (message) => ({
              ...message,
              inputTokens: event.input_tokens > 0 ? event.input_tokens : message.inputTokens,
              outputTokens: event.output_tokens > 0 ? event.output_tokens : message.outputTokens,
            }));
          },
          onModerationBlocked: (event) => {
            moderationBlocked = true;
            // Temporary chats have no billing to review later, so the still-billed-after-block notice can only be shown with the live event.
            updateMessage(assistantID, (message) => ({
              ...message,
              content: [t("blocked"), event.billedReason ? tSubmit("moderationBlockedBilled") : ""]
                .filter(Boolean)
                .join("\n"),
              streaming: false,
              failed: true,
            }));
          },
        },
      );
      if (controller.signal.aborted || abortControllerRef.current !== controller) {
        throw new DOMException("The operation was aborted", "AbortError");
      }
      const mappedAssistant = mapServerMessage(completed.assistantMessage);
      replaceHistory([
        ...baseHistory,
        userHistory,
        { role: "assistant", content: completed.assistantMessage.content },
      ]);
      updateMessage(assistantID, (message) => ({
        ...message,
        content: completed.assistantMessage.content,
        streaming: false,
        failed: false,
        inputTokens: completed.userMessage.inputTokens,
        outputTokens: completed.assistantMessage.outputTokens,
        latencyMS: completed.assistantMessage.latencyMS,
        reasoningEffort: mappedAssistant.reasoningEffort,
        activityLabel: undefined,
        processTrace: mappedAssistant.processTrace,
        knowledgeSources: mappedAssistant.knowledgeSources,
        inlineAlert: undefined,
      }));
    } catch (error) {
      if (abortControllerRef.current !== controller) {
        return false;
      }
      const aborted = controller.signal.aborted;
      if (!moderationBlocked && streamedAssistantText.trim()) {
        replaceHistory([
          ...baseHistory,
          userHistory,
          { role: "assistant", content: streamedAssistantText },
        ]);
      }
      if (moderationBlocked) {
        updateMessage(assistantID, (message) => ({
          ...message,
          streaming: false,
          failed: true,
          activityLabel: undefined,
          inlineAlert: undefined,
        }));
        return false;
      }
      const errorMessage = aborted ? "" : resolveErrorReason(error, tSubmit("retryLater"));
      const errorDetails = aborted ? undefined : resolveErrorDetails(error);
      updateMessage(assistantID, (message) => ({
        ...message,
        content: aborted ? message.content || t("stopped") : message.content,
        streaming: false,
        failed: true,
        activityLabel: undefined,
        inlineAlert: aborted
          ? undefined
          : {
              title: tSubmit("generationInterrupted"),
              message: errorMessage,
              errorId: resolveGenerationErrorId(error),
              details: errorDetails,
            },
      }));
      if (!aborted) {
        if (composerRevision !== undefined && restoreDraftOnFailure) {
          // The failed turn retains its local files for retry. Restoring the
          // composer shares the files but owns separate preview URLs.
          const restoredAttachments = localAttachments.map((item) => ({
            ...item,
            fileID: `temporary_${createSecureUUID()}`,
            previewURL: item.previewURL && item.localFile ? URL.createObjectURL(item.localFile) : undefined,
          }));
          if (!restoreSubmittedDraft(composerRevision, content, restoredAttachments)) {
            releaseAttachments(restoredAttachments);
          }
        }
        const inputLimits = getInputLimitReasons(error);
        if (inputLimits.length > 0) onInputLimit(inputLimits);
        else toast.error(tSubmit("sendFailed"), { description: resolveErrorSummary(error, tSubmit("retryLater")) });
      }
    } finally {
      finishSending(controller);
    }
    return true;
  }, [
    active,
    collectUIStates,
    finishSending,
    getComposerRevision,
    htmlVisualPromptEnabled,
    maxTemporaryAttachments,
    model,
    onAttachmentsConsumed,
    onDraftChange,
    onInputLimit,
    options,
    releaseAttachments,
    replaceHistory,
    replaceMessageTail,
    replaceMessages,
    restoreDraftOnFailure,
    restoreSubmittedDraft,
    selectedKnowledgeBaseIDs,
    uiComponentIDs,
    selectedSkillIDs,
    selectedToolIDs,
    t,
    tSubmit,
    updateMessage,
  ]);

  const send = React.useCallback(async () => {
    await submitTurn({
      content: draft,
      localAttachments: attachments,
      baseHistory: [...historyRef.current],
      consumeComposer: true,
    });
  }, [attachments, draft, submitTurn]);

  // An interactive block's `ask`: the text becomes the next turn; the draft and
  // its attachments are left alone.
  const sendText = React.useCallback(
    async (text: string) => {
      const content = text.trim();
      if (!content || sendingRef.current) {
        return false;
      }
      return submitTurn({
        content,
        localAttachments: [],
        baseHistory: [...historyRef.current],
        consumeComposer: false,
      });
    },
    [submitTurn],
  );

  const resolveUserTurn = React.useCallback((message: ChatAreaMessage) => {
    const messageIndex = messagesRef.current.findIndex((item) => item.id === message.publicID);
    if (messageIndex < 0) {
      return null;
    }
    const target = messagesRef.current[messageIndex];
    if (target.role === "user") {
      return { message: target, index: messageIndex };
    }
    for (let index = messageIndex - 1; index >= 0; index -= 1) {
      const candidate = messagesRef.current[index];
      if (candidate.role === "user" && candidate.historyOffset === target.historyOffset) {
        return { message: candidate, index };
      }
    }
    return null;
  }, []);

  // Retries the turn with the composer's current settings, including its reasoning effort.
  const retryMessage = React.useCallback(async (message: ChatAreaMessage) => {
    const turn = resolveUserTurn(message);
    if (!turn) {
      toast.error(t("failed"));
      return;
    }
    await submitTurn({
      content: turn.message.content,
      localAttachments: turn.message.localAttachments ?? [],
      baseHistory: historyRef.current.slice(0, turn.message.historyOffset),
      replaceFromIndex: turn.index,
      consumeComposer: false,
    });
  }, [resolveUserTurn, submitTurn, t]);

  const editUserMessage = React.useCallback(async (message: ChatAreaMessage, content: string, mode: UserMessageEditMode) => {
    const turn = resolveUserTurn(message);
    if (!turn) {
      toast.error(t("failed"));
      return false;
    }
    if (mode === "save") {
      const nextContent = content.trim();
      if (!nextContent || sendingRef.current) {
        return false;
      }
      updateMessage(turn.message.id, (current) => ({ ...current, content: nextContent }));
      const historyEntry = historyRef.current[turn.message.historyOffset];
      if (historyEntry?.role === "user") {
        const nextHistory = historyRef.current.slice();
        nextHistory[turn.message.historyOffset] = { ...historyEntry, content: nextContent };
        replaceHistory(nextHistory);
      }
      return true;
    }
    return submitTurn({
      content,
      localAttachments: turn.message.localAttachments ?? [],
      baseHistory: historyRef.current.slice(0, turn.message.historyOffset),
      replaceFromIndex: turn.index,
      consumeComposer: false,
    });
  }, [replaceHistory, resolveUserTurn, submitTurn, t, updateMessage]);

  const editAssistantMessage = React.useCallback(async (message: ChatAreaMessage, content: string) => {
    const nextContent = content.trim();
    const assistantIndex = messagesRef.current.findIndex(
      (item) => item.id === message.publicID && item.role === "assistant",
    );
    if (!nextContent || assistantIndex < 0 || sendingRef.current) {
      return false;
    }
    const assistant = messagesRef.current[assistantIndex];
    const turn = resolveUserTurn(message);
    if (!turn) {
      toast.error(t("failed"));
      return false;
    }
    replaceMessageTail(assistantIndex + 1, []);
    updateMessage(assistant.id, (current) => ({
      ...current,
      content: nextContent,
      streaming: false,
      failed: false,
      activityLabel: undefined,
      inlineAlert: undefined,
    }));
    replaceHistory([
      ...historyRef.current.slice(0, turn.message.historyOffset),
      {
        role: "user",
        content: turn.message.content,
        attachments: turn.message.localAttachments ?? [],
      },
      { role: "assistant", content: nextContent },
    ]);
    return true;
  }, [replaceHistory, replaceMessageTail, resolveUserTurn, t, updateMessage]);

  const areaMessages = React.useMemo<ChatAreaMessage[]>(() => messages.map(toAreaMessage), [messages]);

  const inputIssues = React.useMemo(() => active
    ? getTemporaryHistoryIssues(history, { role: "user", content: draft.trim(), attachments }, maxTemporaryAttachments)
    : [], [active, history, attachments, draft, maxTemporaryAttachments]);

  const historyAttachments = React.useMemo(
    () => history.flatMap((item) => item.attachments ?? []),
    [history],
  );

  return {
    messages: areaMessages,
    inputIssues,
    historyAttachments,
    sending,
    send,
    sendText,
    startNew,
    stop,
    loadAttachmentContent,
    onRetryUserMessage: retryMessage,
    onRetryAssistantMessage: retryMessage,
    onEditUserMessage: editUserMessage,
    onEditAssistantMessage: editAssistantMessage,
  };
}
