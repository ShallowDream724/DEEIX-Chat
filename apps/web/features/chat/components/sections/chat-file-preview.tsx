"use client";

import * as React from "react";
import { FilePreviewPanel } from "@/entities/file";
import type { ChatFilePreviewRequest } from "@/features/chat/context/chat-file-preview-context";
import { useMobilePreviewHeaderSlot } from "@/features/shell";

type ChatFilePreviewProps = ChatFilePreviewRequest & {
  onClose: () => void;
  expanded: boolean;
  onToggleExpanded?: () => void;
};

export function ChatFilePreview(props: ChatFilePreviewProps) {
  const hostRef = React.useRef<HTMLDivElement>(null);
  const headerContainer = useMobilePreviewHeaderSlot(!props.onToggleExpanded);
  React.useEffect(() => {
    const origin = document.activeElement;
    const host = hostRef.current;
    host?.focus({ preventScroll: true });
    return () => {
      // Do not pull focus away from navigation or a newly opened dialog.
      const restore = document.activeElement === document.body || host?.contains(document.activeElement);
      if (restore && origin instanceof HTMLElement && origin.isConnected) {
        queueMicrotask(() => origin.focus({ preventScroll: true }));
      }
    };
  }, []);

  return (
    <div
      ref={hostRef}
      tabIndex={-1}
      className="h-full min-h-0 min-w-0 outline-none"
      onKeyDown={(event) => {
        // Portalled zoom/filename menus handle Escape themselves.
        if (event.key !== "Escape" || event.defaultPrevented || !(event.target instanceof Node)) return;
        if (!event.currentTarget.contains(event.target) && !headerContainer?.contains(event.target)) return;
        event.stopPropagation();
        props.onClose();
      }}
    >
      <FilePreviewPanel {...props} headerContainer={headerContainer} />
    </div>
  );
}
