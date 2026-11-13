"use client";

import * as React from "react";
import { flushSync } from "react-dom";
import { useMessageScrollerNavigation } from "@/components/ui/message-scroller";
import { MAX_SCREENSHOT_MESSAGES } from "@/features/chat/model/conversation-screenshot";
import {
  CHAT_HISTORY_OVERSCAN_PX,
  estimateChatHistoryHeight,
  registerChatHistoryMaterializer,
} from "@/features/chat/model/chat-history-window";
import type { ChatAreaMessage } from "@/features/chat/types/messages";
import { createMarkdownViewStateStore, MarkdownViewStateProvider } from "@/shared/components/markdown/markdown-view-state";
import { createChatHistoryScrollAnchor } from "@/features/chat/model/chat-history-scroll-anchor";

type Entry = {
  node: HTMLDivElement;
  publicID: string;
  show: (visible: boolean) => void;
  visible: boolean;
  pins: number;
  resize: () => void;
};

type HistoryWindow = {
  register: (entry: Entry) => () => void;
  viewport: () => HTMLDivElement | null;
  capturing: () => boolean;
  atBottom: () => boolean;
};
const HistoryWindowContext = React.createContext<HistoryWindow | null>(null);

function hasInteraction(node: HTMLElement): boolean {
  if (node.contains(document.activeElement) || node.querySelector("textarea")) {
    return true;
  }
  // Menus and dialogs live in a portal, outside the owning message. Keep the
  // small mounted window while a portal is being operated so its owner survives.
  if (document.querySelector('[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]')) {
    return true;
  }
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed) {
    return false;
  }
  for (let index = 0; index < selection.rangeCount; index += 1) {
    if (selection.getRangeAt(index).intersectsNode(node)) {
      return true;
    }
  }
  return false;
}

function afterPaint(signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      cancelAnimationFrame(frame);
      reject(signal?.reason ?? new DOMException("Aborted", "AbortError"));
    };
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => {
        signal?.removeEventListener("abort", onAbort);
        resolve();
      });
    });
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/** Keeps the scroller's registered shells and scroll algorithm intact. A shared
 * observer only chooses which shell contents exist; it never positions rows. */
export function ChatHistoryWindow({
  viewportRef,
  contentRef,
  children,
}: {
  viewportRef: React.RefObject<HTMLDivElement | null>;
  contentRef: React.RefObject<HTMLDivElement | null>;
  children: React.ReactNode;
}) {
  const entriesRef = React.useRef(new Map<Element, Entry>());
  const observerRef = React.useRef<IntersectionObserver | null>(null);
  const resizeObserverRef = React.useRef<ResizeObserver | null>(null);
  const captureCountRef = React.useRef(0);
  const scrollAnchorRef = React.useRef<ReturnType<typeof createChatHistoryScrollAnchor> | null>(null);
  const registerNavigation = useMessageScrollerNavigation();
  const window = React.useMemo<HistoryWindow>(() => ({
    register: (entry) => {
      entriesRef.current.set(entry.node, entry);
      observerRef.current?.observe(entry.node);
      resizeObserverRef.current?.observe(entry.node);
      return () => {
        observerRef.current?.unobserve(entry.node);
        resizeObserverRef.current?.unobserve(entry.node);
        entriesRef.current.delete(entry.node);
      };
    },
    viewport: () => viewportRef.current,
    capturing: () => captureCountRef.current > 0,
    atBottom: () => scrollAnchorRef.current?.isAtBottom() ?? true,
  }), [viewportRef]);

  React.useEffect(() => {
    const viewport = viewportRef.current;
    const content = contentRef.current;
    if (!viewport || !content) {
      return;
    }
    const entries = entriesRef.current;
    const scrollAnchor = createChatHistoryScrollAnchor(viewport, content);
    scrollAnchorRef.current = scrollAnchor;
    scrollAnchor.bottom();
    scrollAnchor.remember();
    const rememberScroll = () => {
      if (captureCountRef.current === 0) scrollAnchor.remember();
    };
    viewport.addEventListener("scroll", rememberScroll, { passive: true });
    const mutations = new MutationObserver(() => {
      if (captureCountRef.current === 0) scrollAnchor.restoreAfterChildrenChange();
    });
    mutations.observe(content, { childList: true });
    const update = (entry: Entry) => entry.show(entry.visible || entry.pins > 0);
    const refreshVisibility = (entry: Entry) => {
      const bounds = entry.node.getBoundingClientRect();
      const viewportBounds = viewport.getBoundingClientRect();
      entry.visible = bounds.bottom >= viewportBounds.top - CHAT_HISTORY_OVERSCAN_PX &&
        bounds.top <= viewportBounds.bottom + CHAT_HISTORY_OVERSCAN_PX;
      update(entry);
    };
    const observer = typeof IntersectionObserver === "undefined" ? null : new IntersectionObserver((changes) => {
      for (const change of changes) {
        const entry = entries.get(change.target);
        if (entry) {
          entry.visible = change.isIntersecting;
          if (captureCountRef.current === 0) update(entry);
        }
      }
    }, { root: viewport, rootMargin: `${CHAT_HISTORY_OVERSCAN_PX}px 0px` });
    observerRef.current = observer;
    const resizeObserver = new ResizeObserver((changes) => {
      for (const change of changes) entries.get(change.target)?.resize();
      if (captureCountRef.current === 0) {
        if (scrollAnchor.isAtBottom()) scrollAnchor.bottom();
        scrollAnchor.remember();
      }
    });
    resizeObserverRef.current = resizeObserver;
    // The primitive establishes its initial bottom position during mount. Avoid
    // briefly mounting the start of the history before that position is set.
    const frame = requestAnimationFrame(() => {
      for (const entry of entries.values()) {
        if (observer) observer.observe(entry.node);
        else entry.show(true);
        resizeObserver.observe(entry.node);
      }
    });
    const recheckInteractions = () => {
      if (captureCountRef.current > 0) return;
      for (const entry of entries.values()) {
        if (!entry.visible && entry.pins === 0) update(entry);
      }
    };
    document.addEventListener("selectionchange", recheckInteractions);
    document.addEventListener("focusin", recheckInteractions);
    content.addEventListener("focusout", recheckInteractions);
    let navigationController: AbortController | null = null;
    const cancelNavigation = () => navigationController?.abort();
    viewport.addEventListener("wheel", cancelNavigation, { passive: true });
    viewport.addEventListener("touchstart", cancelNavigation, { passive: true });
    viewport.addEventListener("pointerdown", cancelNavigation, { passive: true });
    const unregisterNavigation = registerNavigation(({ messageId, options, scrollToMessage }) => {
      const target = Array.from(entries.values()).find((entry) => entry.node.parentElement?.dataset.chatMessageId === messageId);
      if (!target || captureCountRef.current > 0) return false;
      const bounds = target.node.getBoundingClientRect();
      const viewportBounds = viewport.getBoundingClientRect();
      const distant = Math.abs(bounds.top - viewportBounds.top) > viewport.clientHeight * 2;
      if (target.node.dataset.chatHistoryMounted === "true" && !distant) return false;
      navigationController?.abort();
      const controller = new AbortController();
      navigationController = controller;
      const nearby = Array.from(entries.values()).filter((entry) => {
        const rect = entry.node.getBoundingClientRect();
        return rect.bottom >= bounds.top - CHAT_HISTORY_OVERSCAN_PX && rect.top <= bounds.top + viewport.clientHeight + CHAT_HISTORY_OVERSCAN_PX;
      });
      flushSync(() => {
        for (const entry of nearby) {
          entry.pins += 1;
          entry.show(true);
        }
      });
      // Unknown heights make a long smooth animation's absolute destination
      // stale. Land immediately, then let the primitive align once more after
      // materialization. Direct user scrolling cancels that final alignment.
      scrollToMessage(messageId, { ...options, behavior: "auto" });
      void afterPaint(controller.signal).then(() => {
        if (!controller.signal.aborted) scrollToMessage(messageId, { ...options, behavior: "auto" });
      }).catch(() => {}).finally(() => {
        for (const entry of nearby) {
          entry.pins -= 1;
          if (entries.get(entry.node) === entry) refreshVisibility(entry);
        }
        if (navigationController === controller) navigationController = null;
      });
      return true;
    });
    const unregister = registerChatHistoryMaterializer(content, async (ids, signal) => {
      signal?.throwIfAborted();
      const all = Array.from(entries.values()).sort((first, second) => (
        first.node.compareDocumentPosition(second.node) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
      ));
      const targets = ids ? all.filter((entry) => ids.has(entry.publicID)).slice(-MAX_SCREENSHOT_MESSAGES) : all.slice(-MAX_SCREENSHOT_MESSAGES);
      const scrollTop = viewport.scrollTop;
      captureCountRef.current += 1;
      const release = () => {
        captureCountRef.current -= 1;
        const stillCurrent = targets.some((entry) => entries.get(entry.node) === entry);
        if (stillCurrent) viewport.scrollTop = scrollTop;
        scrollAnchor.remember();
        flushSync(() => {
          for (const entry of targets) {
            entry.pins -= 1;
            if (entries.get(entry.node) === entry) refreshVisibility(entry);
          }
        });
        if (stillCurrent) viewport.scrollTop = scrollTop;
      };
      try {
        flushSync(() => {
          for (const entry of targets) {
            entry.pins += 1;
            entry.show(true);
          }
        });
        await afterPaint(signal);
        signal?.throwIfAborted();
        return release;
      } catch (error) {
        release();
        throw error;
      }
    });
    return () => {
      cancelAnimationFrame(frame);
      navigationController?.abort();
      unregisterNavigation();
      observer?.disconnect();
      resizeObserver.disconnect();
      mutations.disconnect();
      viewport.removeEventListener("scroll", rememberScroll);
      scrollAnchorRef.current = null;
      observerRef.current = null;
      resizeObserverRef.current = null;
      unregister();
      document.removeEventListener("selectionchange", recheckInteractions);
      document.removeEventListener("focusin", recheckInteractions);
      content.removeEventListener("focusout", recheckInteractions);
      viewport.removeEventListener("wheel", cancelNavigation);
      viewport.removeEventListener("touchstart", cancelNavigation);
      viewport.removeEventListener("pointerdown", cancelNavigation);
    };
  }, [contentRef, viewportRef, registerNavigation]);

  return <HistoryWindowContext.Provider value={window}>{children}</HistoryWindowContext.Provider>;
}

export function ChatHistoryWindowItem({
  message,
  initiallyMounted,
  forceMounted,
  children,
}: {
  message: ChatAreaMessage;
  initiallyMounted: boolean;
  forceMounted: boolean;
  children: React.ReactNode;
}) {
  const window = React.useContext(HistoryWindowContext);
  const nodeRef = React.useRef<HTMLDivElement | null>(null);
  const initialMountedRef = React.useRef(initiallyMounted);
  const markdownViewStateRef = React.useRef<ReturnType<typeof createMarkdownViewStateStore> | null>(null);
  if (!markdownViewStateRef.current) markdownViewStateRef.current = createMarkdownViewStateStore();
  const [mounted, setMounted] = React.useState(initiallyMounted || forceMounted);
  const heightRef = React.useRef(estimateChatHistoryHeight(message));
  const widthRef = React.useRef(0);
  const visible = mounted || forceMounted;
  const previousVisibleRef = React.useRef(visible);
  const materializingRef = React.useRef(false);
  if (!visible) materializingRef.current = false;
  else if (!previousVisibleRef.current) materializingRef.current = true;
  previousVisibleRef.current = visible;
  const messageRef = React.useRef(message);
  messageRef.current = message;
  const resizeRef = React.useRef(() => {});
  resizeRef.current = () => {
    const node = nodeRef.current;
    const viewport = window?.viewport();
    if (!node || !viewport || window?.capturing()) return;
    const bounds = node.getBoundingClientRect();
    const markdownAdjusted = node.dataset.markdownAnchorAdjustment !== undefined;
    delete node.dataset.markdownAnchorAdjustment;
    const previous = heightRef.current;
    const viewportTop = viewport.getBoundingClientRect().top;
    const above = bounds.top + previous <= viewportTop;
    const materializing = materializingRef.current;
    materializingRef.current = false;
    if (visible) {
      heightRef.current = bounds.height;
      widthRef.current = bounds.width;
    } else if (Math.abs(bounds.width - widthRef.current) >= 1) {
      heightRef.current = estimateChatHistoryHeight(messageRef.current, bounds.width);
      widthRef.current = bounds.width;
      node.style.height = `${heightRef.current}px`;
    }
    // Preserve the visible reading position when an overscan row above it
    // acquires its actual height, or cached geometry is invalidated by width.
    if (window?.atBottom()) {
      viewport.scrollTop = viewport.scrollHeight;
    } else if (!markdownAdjusted && above && Math.abs(heightRef.current - previous) > 0.5) {
      viewport.scrollTop += heightRef.current - previous;
    } else if (!markdownAdjusted && materializing && previous > 0 && bounds.top < viewportTop && bounds.top + previous > viewportTop) {
      // A direct scrollbar jump can land inside an unmeasured placeholder. Keep
      // that row in view when its estimate is replaced, rather than shrinking
      // past it and repeatedly materializing the next unknown row. Real content
      // resizes retain exact pixel offsets; only this first estimate uses a ratio.
      viewport.scrollTop += (heightRef.current - previous) * (viewportTop - bounds.top) / previous;
    }
  };

  React.useLayoutEffect(() => {
    const node = nodeRef.current;
    if (!node || !window) return;
    return window.register({
      node,
      publicID: message.publicID.trim(),
      visible: initialMountedRef.current,
      pins: 0,
      resize: () => resizeRef.current(),
      show: (next) => {
        if (next || !hasInteraction(node)) setMounted(next);
      },
    });
  }, [window, message.publicID]);

  React.useLayoutEffect(() => {
    resizeRef.current();
  }, [visible, window]);

  return (
    <div
      ref={nodeRef}
      className="flow-root min-w-0"
      data-chat-history-mounted={visible ? "true" : "false"}
      style={visible ? undefined : { height: heightRef.current }}
      aria-hidden={visible ? undefined : true}
    >
      <MarkdownViewStateProvider store={markdownViewStateRef.current}>
        {visible ? children : null}
      </MarkdownViewStateProvider>
    </div>
  );
}
