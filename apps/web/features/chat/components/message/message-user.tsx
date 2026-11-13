"use client";

import * as React from "react";

import { cn } from "@/lib/utils";
import { useTranslations } from "next-intl";

import { ChevronDown } from "@/components/animate-ui/icons/chevron-down";
import { ChevronUp } from "@/components/animate-ui/icons/chevron-up";
import { MessageUserEditor } from "@/features/chat/components/message/message-user-editor";
import { MessageAttachmentRow } from "@/features/chat/components/message/message-attachment";
import { UserMessageMeta } from "@/features/chat/components/message/message-meta";
import type { ChatAreaMessage, EditUserMessage } from "@/features/chat/types/messages";
import type { SendShortcut } from "@/features/settings";
import type { ChatModelOption } from "@/features/chat/types/chat-runtime";
import type { FileContentLoader } from "@/entities/file";
import { StreamdownRender } from "@/shared/components/markdown/streamdown-render";

// Long user messages collapse to this many lines (leading-6 → 24px each).
const USER_MESSAGE_COLLAPSED_LINES = 6;

type ChatMessageUserProps = {
  item: ChatAreaMessage;
  onRetryUserMessage: (message: ChatAreaMessage) => Promise<void> | void;
  onEditUserMessage: EditUserMessage;
  onDeleteUserMessage?: (message: ChatAreaMessage) => Promise<void> | void;
  modelOptions?: ChatModelOption[];
  selectedPlatformModelName?: string;
  onModelChange?: (platformModelName: string) => void;
  onModelCatalogRefresh?: () => void | Promise<void>;
  onCycleMessageBranch: (parentPublicID: string | null, direction: "previous" | "next") => void;
  onCopy: () => void;
  copySucceeded?: boolean;
  readOnly?: boolean;
  attachmentContentLoader?: FileContentLoader;
  showBranchNavigator?: boolean;
  // Enter saves and regenerates, as the composer does, honouring the same user setting.
  sendShortcut?: SendShortcut;
  screenshotMeta?: React.ReactNode;
};

export function ChatMessageUser({
  item,
  onRetryUserMessage,
  onEditUserMessage,
  onDeleteUserMessage,
  modelOptions = [],
  selectedPlatformModelName = "",
  onModelChange = () => undefined,
  onModelCatalogRefresh,
  onCycleMessageBranch,
  onCopy,
  copySucceeded = false,
  readOnly = false,
  attachmentContentLoader,
  showBranchNavigator = true,
  sendShortcut = "enter",
  screenshotMeta,
}: ChatMessageUserProps) {
  const tMessages = useTranslations("chat.messages");
  const [isEditing, setIsEditing] = React.useState(false);
  const [expandedContentKey, setExpandedContentKey] = React.useState("");
  const [canCollapse, setCanCollapse] = React.useState(false);
  const [isToggleHovered, setIsToggleHovered] = React.useState(false);
  const [contentHeight, setContentHeight] = React.useState(0);
  const [collapsedHeight, setCollapsedHeight] = React.useState(USER_MESSAGE_COLLAPSED_LINES * 24);
  const [measuredContentKey, setMeasuredContentKey] = React.useState("");
  const contentRef = React.useRef<HTMLDivElement>(null);
  const measurementKey = React.useMemo(
    () => `${item.publicID || item.key}:${item.content}`,
    [item.content, item.key, item.publicID],
  );
  const measured = measuredContentKey === measurementKey;
  const expanded = measured && expandedContentKey === measurementKey;
  const contentMaxHeight = expanded
    ? contentHeight
    : !measured || canCollapse
      ? collapsedHeight
      : undefined;

  React.useEffect(() => {
    setIsEditing(false);
  }, [item.publicID]);

  React.useLayoutEffect(() => {
    const element = contentRef.current;
    if (!element) {
      setCanCollapse(false);
      return;
    }

    const measure = () => {
      const lineHeight = Number.parseFloat(window.getComputedStyle(element).lineHeight);
      const collapsedHeight =
        Number.isFinite(lineHeight) && lineHeight > 0
          ? lineHeight * USER_MESSAGE_COLLAPSED_LINES
          : USER_MESSAGE_COLLAPSED_LINES * 24;
      setContentHeight(element.scrollHeight);
      setCollapsedHeight(collapsedHeight);
      setCanCollapse(element.scrollHeight > collapsedHeight + 1);
      setMeasuredContentKey(measurementKey);
    };

    measure();
    if (typeof ResizeObserver === "undefined") {
      return;
    }
    const resizeObserver = new ResizeObserver(measure);
    resizeObserver.observe(element);
    return () => resizeObserver.disconnect();
  }, [isEditing, item.content, measurementKey]);

  const onRetry = React.useCallback(() => {
    void onRetryUserMessage(item);
  }, [item, onRetryUserMessage]);

  const onDelete = React.useCallback(
    () => onDeleteUserMessage?.(item),
    [item, onDeleteUserMessage],
  );

  if (!readOnly && isEditing) {
    return (
      <MessageUserEditor item={item} onSave={onEditUserMessage} onClose={() => setIsEditing(false)}
        modelOptions={modelOptions} selectedPlatformModelName={selectedPlatformModelName} onModelChange={onModelChange}
        onModelCatalogRefresh={onModelCatalogRefresh} sendShortcut={sendShortcut} attachmentContentLoader={attachmentContentLoader} />
    );
  }

  return (
    <div className="group/user-message flex min-w-0 max-w-full flex-col items-end gap-2">
      {item.attachments && item.attachments.length > 0 ? (
        <MessageAttachmentRow
          attachments={item.attachments}
          loadContent={attachmentContentLoader}
          allowDownload={!readOnly}
        />
      ) : null}
      <div
        className="chat-font-content min-w-0 max-w-[70%] overflow-hidden rounded-xl bg-muted/60 p-3 text-[15px] leading-6 text-foreground [overflow-wrap:anywhere] max-sm:max-w-[88%]"
        style={{ fontFamily: "var(--font-chat)", fontWeight: "var(--font-chat-weight)" }}
      >
        {item.content.trim() ? (
          <>
            <div className="relative">
              <div
                ref={contentRef}
                className={cn(
                  "chat-user-message-collapsible overflow-hidden",
                  measured && canCollapse && "transition-[height] duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none",
                )}
                style={contentMaxHeight == null ? undefined : measured && canCollapse ? { height: contentMaxHeight } : { maxHeight: contentMaxHeight }}
              >
                <StreamdownRender content={item.content} variant="user" />
              </div>
              {measured && canCollapse ? (
                <div
                  aria-hidden="true"
                  data-screenshot-exclude="true"
                  className={cn(
                    "pointer-events-none absolute inset-x-0 bottom-0 h-14 bg-[linear-gradient(to_bottom,transparent,color-mix(in_oklab,var(--muted)_60%,var(--background)))] transition-opacity duration-300 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none",
                    expanded ? "opacity-0" : "opacity-100",
                  )}
                />
              ) : null}
            </div>
            {measured && canCollapse ? (
              <button
                type="button"
                data-screenshot-exclude="true"
                className="mt-0.5 inline-flex items-center gap-1 p-0 text-sm font-medium leading-7 text-foreground/70 outline-none transition-colors hover:text-foreground focus-visible:text-foreground"
                aria-expanded={expanded}
                onClick={() =>
                  setExpandedContentKey((current) => (current === measurementKey ? "" : measurementKey))
                }
                onMouseEnter={() => setIsToggleHovered(true)}
                onMouseLeave={() => setIsToggleHovered(false)}
              >
                {expanded ? (
                  <ChevronUp className="size-4 shrink-0" animate={isToggleHovered ? "default" : undefined} />
                ) : (
                  <ChevronDown className="size-4 shrink-0" animate={isToggleHovered ? "default" : undefined} />
                )}
                <span>{expanded ? tMessages("collapseUserMessage") : tMessages("expandUserMessage")}</span>
              </button>
            ) : null}
          </>
        ) : null}
      </div>
      {screenshotMeta}
      <UserMessageMeta
        item={item}
        showRetry={!item.isPending && item.status?.trim().toLowerCase() !== "pending"}
        onCycleBranch={onCycleMessageBranch}
        onRetry={onRetry}
        onEdit={() => setIsEditing(true)}
        onCopy={onCopy}
        onDelete={onDeleteUserMessage ? onDelete : undefined}
        copySucceeded={copySucceeded}
        readOnly={readOnly}
        alwaysVisible={readOnly}
        showBranchNavigator={showBranchNavigator}
      />
    </div>
  );
}
