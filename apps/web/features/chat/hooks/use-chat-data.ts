"use client";

import { useTranslations } from "next-intl";
import * as React from "react";
import { buildMediaImagePreviewMarkdown } from "@/features/chat/model/media-image-preview";
import { mergeProcessTraceSnapshot } from "@/features/chat/model/message-submit";
import { RecoveredGenerationTracker, resumeFallbackPollingDelay, resumeWithRecovery } from "@/features/chat/model/message-submit-recovery";
import { upsertLiveUpstreamThinkTrace } from "@/features/chat/model/upstream-think-store";
import { cancelMessageGeneration, ConversationStreamServerError, listMessagesPage, resumeMessageGenerationStream } from "@/shared/api/conversation";
import { ConversationStreamDisconnectedError } from "@/shared/api/conversation-stream-error";
import { ApiError } from "@/shared/api/http-client";
import type { MessageDTO, SendMessageResult } from "@/shared/api/conversation-types";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";

// The first page covers recent turns; older ones load as the user scrolls up.
const MESSAGE_PAGE_SIZE = 30;

type ChatDataState = {
  conversationPublicID: string;
  loading: boolean;
  loadingOlder: boolean;
  errorMsg: string;
  messages: MessageDTO[];
  total: number;
  hasOlder: boolean;
};

type ActiveResumeStream = {
  controller: AbortController;
  runID: string;
  accessToken: string | null;
};

export function useChatData(
  conversationID: string | null,
  {
    activeGenerationRunsRef,
    activeGenerationRunsRevision = 0,
    onConversationRunFinished,
    onRecoveredGenerationCompleted,
  }: {
    activeGenerationRunsRef?: React.RefObject<Set<string>>;
    activeGenerationRunsRevision?: number;
    onConversationRunFinished?: (runID: string) => void;
    onRecoveredGenerationCompleted?: (accessToken: string, conversationPublicID: string, result: SendMessageResult) => void;
  } = {},
) {
  const t = useTranslations("chat.data");
  const tSubmit = useTranslations("chat.submit");
  const [state, setState] = React.useState<ChatDataState>({
    conversationPublicID: conversationID ?? "",
    loading: Boolean(conversationID),
    loadingOlder: false,
    errorMsg: "",
    messages: [],
    total: 0,
    hasOlder: false,
  });
  const [reloadToken, setReloadToken] = React.useState(0);
  const [loadCompletionRevision, setLoadCompletionRevision] = React.useState(0);
  const [resumeRetryToken, setResumeRetryToken] = React.useState(0);
  const [resumeAuthBlockedRunID, setResumeAuthBlockedRunID] = React.useState("");
  const [resumeFallbackRunID, setResumeFallbackRunID] = React.useState("");
  const fallbackPollAttemptsRef = React.useRef({ runID: "", attempts: 0 });
  const [resumingRunID, setResumingRunID] = React.useState("");
  const [resumingActivityLabel, setResumingActivityLabel] = React.useState("");
  const stateRef = React.useRef(state);
  stateRef.current = state;
  const previousConversationIDRef = React.useRef<string | null>(conversationID);
  const resumeSeqByRunRef = React.useRef<Record<string, number>>({});
  const pendingAssistantContentRef = React.useRef("");
  const resumedTextByRunRef = React.useRef<Record<string, string>>({});
  const activeResumeStreamRef = React.useRef<ActiveResumeStream | null>(null);
  const recoveredRunsRef = React.useRef(new RecoveredGenerationTracker());
  const recoveredCompletionRef = React.useRef(onRecoveredGenerationCompleted);
  recoveredCompletionRef.current = onRecoveredGenerationCompleted;
  // The resume cursor is only valid while its visible content is retained; both must be cleared together.
  const clearResumeCheckpoint = React.useCallback((runID: string) => {
    const normalizedRunID = runID.trim();
    if (!normalizedRunID) {
      return;
    }
    delete resumeSeqByRunRef.current[normalizedRunID];
    delete resumedTextByRunRef.current[normalizedRunID];
  }, []);

  React.useEffect(() => {
    let cancelled = false;

    async function load() {
      if (!conversationID) {
        setState({
          conversationPublicID: "",
          loading: false,
          loadingOlder: false,
          errorMsg: "",
          messages: [],
          total: 0,
          hasOlder: false,
        });
        return;
      }

      const isConversationSwitch = previousConversationIDRef.current !== conversationID;
      previousConversationIDRef.current = conversationID;
      setState((prev) => ({
        conversationPublicID: conversationID,
        loading: isConversationSwitch || prev.messages.length === 0,
        loadingOlder: false,
        errorMsg: "",
        messages: isConversationSwitch ? [] : prev.messages,
        total: isConversationSwitch ? 0 : prev.total,
        hasOlder: isConversationSwitch ? false : prev.hasOlder,
      }));
      try {
        const token = await resolveAccessToken();
        if (!token) {
          if (!cancelled) {
            setState({
              conversationPublicID: conversationID,
              loading: false,
              loadingOlder: false,
              errorMsg: t("signInRequired"),
              messages: [],
              total: 0,
              hasOlder: false,
            });
          }
          return;
        }

        const data = await listMessagesPage(token, conversationID, {
          page: 1,
          pageSize: MESSAGE_PAGE_SIZE,
          tail: true,
          trace: "summary",
        });
        if (cancelled) {
          return;
        }

        const recovered = recoveredRunsRef.current.collect(data.results, activeGenerationRunsRef?.current);
        for (const completed of recovered) {
          recoveredCompletionRef.current?.(token, conversationID, completed);
        }

        setState((prev) => {
          const firstTailMessageID = data.results[0]?.id ?? 0;
          // Keep the old range only if extra history pages were loaded, so ordinary reloads don't accumulate tail messages indefinitely.
          const loadedOlderMessages =
            isConversationSwitch ||
            firstTailMessageID <= 0 ||
            prev.messages.length <= MESSAGE_PAGE_SIZE
              ? []
              : prev.messages.filter((message) => message.id < firstTailMessageID);
          const messages = [...loadedOlderMessages, ...data.results];
          return {
            conversationPublicID: conversationID,
            loading: false,
            loadingOlder: false,
            errorMsg: "",
            messages,
            total: data.total,
            hasOlder: messages.length < data.total,
          };
        });
      } catch {
        if (!cancelled) {
          setState((prev) => ({
            ...prev,
            loading: false,
            loadingOlder: false,
            errorMsg: t("loadFailed"),
          }));
        }
      } finally {
        if (!cancelled) setLoadCompletionRevision((current) => current + 1);
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [activeGenerationRunsRef, conversationID, reloadToken, t]);

  const reload = React.useCallback(() => {
    setReloadToken((prev) => prev + 1);
  }, []);

  React.useEffect(() => {
    if (!conversationID) return;
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") {
        reload();
        setResumeAuthBlockedRunID("");
        setResumeFallbackRunID("");
        fallbackPollAttemptsRef.current.attempts = 0;
        // Exhausted/permanently stopped attempts can retry after the user
        // returns or reconnects. A healthy resume stream keeps its connection.
        if (!activeResumeStreamRef.current) setResumeRetryToken((current) => current + 1);
      }
    };
    // A failed read while offline or in a suspended tab must reconcile on return,
    // even if no persisted pending message was available during that failed read.
    window.addEventListener("online", refreshWhenVisible);
    window.addEventListener("focus", refreshWhenVisible);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      window.removeEventListener("online", refreshWhenVisible);
      window.removeEventListener("focus", refreshWhenVisible);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, [conversationID, reload]);

  const replaceMessage = React.useCallback((nextMessage: MessageDTO) => {
    setState((prev) => ({
      ...prev,
      messages: prev.messages.map((message) =>
        message.publicID === nextMessage.publicID ? nextMessage : message,
      ),
    }));
  }, []);

  const loadOlderMessages = React.useCallback(async () => {
    const current = stateRef.current;
    if (
      !conversationID ||
      current.conversationPublicID !== conversationID ||
      current.loading ||
      current.loadingOlder ||
      !current.hasOlder ||
      current.messages.length === 0
    ) {
      return false;
    }

    const beforeID = current.messages[0]?.id ?? 0;
    if (beforeID <= 0) {
      setState((prev) => {
        const next = { ...prev, hasOlder: false };
        stateRef.current = next;
        return next;
      });
      return false;
    }

    setState((prev) => {
      const next = { ...prev, loadingOlder: true };
      stateRef.current = next;
      return next;
    });
    try {
      const token = await resolveAccessToken();
      if (!token) {
        setState((prev) => {
          const next = { ...prev, loadingOlder: false, hasOlder: false };
          stateRef.current = next;
          return next;
        });
        return false;
      }

      const data = await listMessagesPage(token, conversationID, {
        pageSize: MESSAGE_PAGE_SIZE,
        beforeID,
        trace: "summary",
      });
      if (previousConversationIDRef.current !== conversationID) {
        return false;
      }
      let loaded = false;
      setState((prev) => {
        const existingPublicIDs = new Set(prev.messages.map((message) => message.publicID));
        const olderMessages = data.results.filter((message) => !existingPublicIDs.has(message.publicID));
        const messages = [...olderMessages, ...prev.messages];
        loaded = olderMessages.length > 0;
        const next = {
          ...prev,
          loadingOlder: false,
          messages,
          total: data.total,
          hasOlder: loaded && messages.length < data.total,
        };
        stateRef.current = next;
        return next;
      });
      return loaded;
    } catch {
      setState((prev) => {
        const next = { ...prev, loadingOlder: false };
        stateRef.current = next;
        return next;
      });
      return false;
    }
  }, [conversationID]);

  const cancelResumedGeneration = React.useCallback(async () => {
    const active = activeResumeStreamRef.current;
    if (!active) {
      return false;
    }

    active.controller.abort();
    clearResumeCheckpoint(active.runID);
    setResumingRunID("");

    const token = active.accessToken ?? (await resolveAccessToken());
    if (!token) {
      return false;
    }

    const result = await cancelMessageGeneration(token, active.runID).catch((): null => null);
    if (result?.canceled) {
      onConversationRunFinished?.(active.runID);
    }
    reload();
    return Boolean(result?.canceled);
  }, [clearResumeCheckpoint, onConversationRunFinished, reload]);

  const pendingAssistant = React.useMemo(() => {
    if (!conversationID || state.conversationPublicID !== conversationID) {
      return null;
    }
    for (let index = state.messages.length - 1; index >= 0; index -= 1) {
      const message = state.messages[index];
      if (message.role === "assistant" && message.status === "pending") {
        return message;
      }
    }
    return null;
  }, [conversationID, state.conversationPublicID, state.messages]);

  const pendingRunID = pendingAssistant?.runID?.trim() || "";
  // revision only forces re-reading the mutable Set; the effect depends only on the current pending run's actual activity.
  const pendingRunIsActive = React.useMemo(
    () => Boolean(pendingRunID && activeGenerationRunsRef?.current.has(pendingRunID)),
    [activeGenerationRunsRef, activeGenerationRunsRevision, pendingRunID],
  );

  React.useEffect(() => {
    pendingAssistantContentRef.current = pendingAssistant?.content ?? "";
  }, [pendingAssistant?.content]);

  React.useEffect(() => {
    if (
      !conversationID ||
      !pendingRunID ||
      pendingRunIsActive ||
      document.hidden || navigator.onLine === false
    ) {
      setResumingRunID("");
      setResumingActivityLabel("");
      return;
    }

    const controller = new AbortController();
    let closed = false;
    const baseContent = pendingAssistantContentRef.current;
    const resumedTextByRun = resumedTextByRunRef.current;
    const clearResumedText = () => {
      delete resumedTextByRun[pendingRunID];
    };
    const isResumeInactive = () => closed || controller.signal.aborted;
    const updateResumeState = (update: (current: ChatDataState) => ChatDataState) => {
      setState((current) =>
        isResumeInactive() || current.conversationPublicID !== conversationID
          ? current
          : update(current),
      );
    };
    resumedTextByRun[pendingRunID] = baseContent;
    activeResumeStreamRef.current = {
      controller,
      runID: pendingRunID,
      accessToken: null,
    };
    setResumingRunID(pendingRunID);
    setResumingActivityLabel("");

    async function resume() {
        const token = await resolveAccessToken();
        if (!token || controller.signal.aborted) {
          return;
        }
        if (activeResumeStreamRef.current?.controller === controller) {
          activeResumeStreamRef.current.accessToken = token;
        }
        const completed = await resumeMessageGenerationStream(token, pendingRunID, {
          signal: controller.signal,
          afterSeq: resumeSeqByRunRef.current[pendingRunID] ?? 0,
          onTerminal: () => {
            onConversationRunFinished?.(pendingRunID);
          },
          onEventSeq: (seq) => {
            if (isResumeInactive()) {
              return;
            }
            resumeSeqByRunRef.current[pendingRunID] = Math.max(resumeSeqByRunRef.current[pendingRunID] ?? 0, seq);
          },
          onMediaStatus: (event) => {
            if (isResumeInactive()) {
              return;
            }
            const status = event.status.trim();
            const contentType = event.content_type === "video" ? "video" : "image";
            const activityLabel =
              status === "queued"
                ? tSubmit(contentType === "video" ? "mediaStatus.videoQueued" : "mediaStatus.queued")
                : status === "running"
                  ? tSubmit(contentType === "video" ? "mediaStatus.videoRunning" : "mediaStatus.running")
                  : status === "saving_artifact"
                    ? tSubmit(contentType === "video" ? "mediaStatus.videoSavingArtifact" : "mediaStatus.savingArtifact")
                    : event.message.trim() || status;
            setResumingActivityLabel(activityLabel);
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? { ...message, contentType }
                  : message,
              ),
            }));
          },
          onMediaImageDelta: (event) => {
            if (isResumeInactive()) {
              return;
            }
            clearResumedText();
            const previewMarkdown = buildMediaImagePreviewMarkdown(event, tSubmit("imagePreviewAlt"));
            if (!previewMarkdown) {
              return;
            }
            setResumingActivityLabel("");
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? { ...message, content: previewMarkdown, contentType: "image" }
                  : message,
              ),
            }));
          },
          onTextSnapshot: (content) => {
            if (isResumeInactive()) {
              return;
            }
            setResumingActivityLabel("");
            resumedTextByRun[pendingRunID] = content;
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? { ...message, content, contentType: "text" }
                  : message,
              ),
            }));
          },
          onDelta: (delta) => {
            if (isResumeInactive()) {
              return;
            }
            setResumingActivityLabel("");
            const nextContent = `${resumedTextByRun[pendingRunID] ?? ""}${delta}`;
            resumedTextByRun[pendingRunID] = nextContent;
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? { ...message, content: nextContent }
                  : message,
              ),
            }));
          },
          onProcessUpdate: (event) => {
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? { ...message, processTrace: event.trace ? mergeProcessTraceSnapshot(message.processTrace, event.trace) : message.processTrace }
                  : message,
              ),
            }));
          },
          onUpstreamThinkDelta: (event) => {
            if (isResumeInactive()) {
              return;
            }
            upsertLiveUpstreamThinkTrace(pendingRunID, event);
          },
          onUsage: (event) => {
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant" && message.status === "pending"
                  ? {
                      ...message,
                      inputTokens: event.input_tokens > 0 ? event.input_tokens : message.inputTokens,
                      outputTokens: event.output_tokens > 0 ? event.output_tokens : message.outputTokens,
                      cacheReadTokens:
                        event.cache_read_tokens > 0 ? event.cache_read_tokens : message.cacheReadTokens,
                      cacheWriteTokens:
                        event.cache_write_tokens > 0 ? event.cache_write_tokens : message.cacheWriteTokens,
                      reasoningTokens:
                        event.reasoning_tokens > 0 ? event.reasoning_tokens : message.reasoningTokens,
                    }
                  : message,
              ),
            }));
          },
          onModerationChecking: () => {
            if (!isResumeInactive()) {
              setResumingActivityLabel(tSubmit("moderationChecking"));
            }
          },
          onModerationBlocked: (event) => {
            if (isResumeInactive()) {
              return;
            }
            clearResumedText();
            setResumingActivityLabel("");
            const categories = Array.isArray(event.categories) ? event.categories : [];
            updateResumeState((prev) => ({
              ...prev,
              messages: prev.messages.map((message) =>
                message.runID === pendingRunID && message.role === "assistant"
                  ? {
                      ...message,
                      status: "blocked",
                      content: "",
                      contentType: "text",
                      attachments: "[]",
                      processTrace: undefined,
                      errorCode: "content_moderation.blocked",
                      errorMessage: tSubmit("moderationBlockedDescription"),
                      moderation: {
                        state: "blocked",
                        direction: event.direction,
                        eventID: event.eventID,
                        categories,
                      },
                    }
                  : message,
              ),
            }));
          },
        });
        if (!completed && !controller.signal.aborted) throw new ConversationStreamDisconnectedError(true);
        if (!controller.signal.aborted) {
          if (completed && conversationID && recoveredRunsRef.current.markCompleted(pendingRunID)) {
            recoveredCompletionRef.current?.(token, conversationID, completed);
          }
          setResumingActivityLabel("");
          clearResumeCheckpoint(pendingRunID);
          reload();
        }
    }

    void resumeWithRecovery({
      signal: controller.signal,
      attempt: resume,
      isPermanentFailure: (error) => document.hidden || navigator.onLine === false || error instanceof ConversationStreamServerError ||
        (error instanceof ApiError && [401, 403, 404, 410].includes(error.status)),
      onFailure: (error) => {
          const authBlocked = error instanceof ApiError && [401, 403].includes(error.status);
          if (authBlocked) setResumeAuthBlockedRunID(pendingRunID);
          clearResumeCheckpoint(pendingRunID);
          setResumingActivityLabel(tSubmit("recoveryPending"));
          if (!authBlocked && !document.hidden && navigator.onLine !== false) reload();
      },
    }).then((outcome) => {
      if (outcome !== "completed" && !closed && !controller.signal.aborted) setResumeFallbackRunID(pendingRunID);
    }).finally(() => {
        if (activeResumeStreamRef.current?.controller === controller) {
          activeResumeStreamRef.current = null;
        }
        if (!controller.signal.aborted && !closed) {
          setResumingRunID("");
          setResumingActivityLabel("");
        }
    });
    return () => {
      closed = true;
      controller.abort();
      setResumingActivityLabel("");
      clearResumeCheckpoint(pendingRunID);
      if (activeResumeStreamRef.current?.controller === controller) {
        activeResumeStreamRef.current = null;
      }
    };
  }, [
    clearResumeCheckpoint,
    conversationID,
    pendingRunID,
    pendingRunIsActive,
    resumeRetryToken,
    onConversationRunFinished,
    reload,
    tSubmit,
  ]);

  React.useEffect(() => {
    if (
      !conversationID ||
      !pendingAssistant ||
      document.hidden || navigator.onLine === false ||
      pendingRunIsActive ||
      (pendingRunID && pendingRunID === resumeAuthBlockedRunID) ||
      (pendingRunID && pendingRunID === resumingRunID)
    ) {
      return;
    }
    if (fallbackPollAttemptsRef.current.runID !== pendingRunID) fallbackPollAttemptsRef.current = { runID: pendingRunID, attempts: 0 };
    const fallback = pendingRunID && pendingRunID === resumeFallbackRunID;
    const delay = fallback ? resumeFallbackPollingDelay(fallbackPollAttemptsRef.current.attempts) : 1500;
    const timer = window.setTimeout(() => {
      if (document.hidden || navigator.onLine === false) return;
      if (fallback) fallbackPollAttemptsRef.current.attempts += 1;
      reload();
    }, delay);
    return () => {
      window.clearTimeout(timer);
    };
  }, [conversationID, loadCompletionRevision, pendingAssistant, pendingRunID, pendingRunIsActive, reload, resumeAuthBlockedRunID, resumeFallbackRunID, resumingRunID]);

  return {
    ...state,
    cancelResumedGeneration,
    loadOlderMessages,
    reload,
    replaceMessage,
    resumingActivityLabel,
    resumingRunID,
  };
}
