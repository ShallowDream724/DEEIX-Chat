import type { ChatAreaMessage } from "../types/messages";

export const CHAT_HISTORY_INITIAL_ROWS = 4;
export const CHAT_HISTORY_OVERSCAN_PX = 1000;

// Deliberately uses lengths and counts only. Estimating an unloaded message must
// never tokenize Markdown, walk a table, or parse an interactive document.
export function estimateChatHistoryHeight(message: ChatAreaMessage, width = 800): number {
  const charactersPerLine = Math.max(20, Math.floor(width / 9));
  const lines = Math.ceil(message.content.length / charactersPerLine);
  const attachments = message.attachments?.length ?? 0;
  const trace = message.processTrace;
  const traceCharacters = (trace?.upstreamThink?.contentMarkdown.length ?? 0) +
    (trace?.tools?.contentMarkdown.length ?? 0) + (trace?.process?.contentMarkdown.length ?? 0);
  return Math.min(4000, Math.max(80, 64 + lines * 28 + attachments * 180 + Math.min(1000, traceCharacters / charactersPerLine * 24)));
}

export function chatHistoryShouldStartMounted(index: number, total: number): boolean {
  return index >= total - CHAT_HISTORY_INITIAL_ROWS;
}

type Materializer = (ids: ReadonlySet<string> | null, signal?: AbortSignal) => Promise<() => void>;
const materializers = new WeakMap<HTMLElement, Materializer>();

export function registerChatHistoryMaterializer(root: HTMLElement, materialize: Materializer): () => void {
  materializers.set(root, materialize);
  return () => materializers.delete(root);
}

// null means the latest screenshot-sized tail; an explicit set means exactly
// those selected rows. The release callback must run after screenshot DOM restore.
export async function materializeChatHistory(
  root: HTMLElement,
  ids: ReadonlySet<string> | null,
  signal?: AbortSignal,
): Promise<() => void> {
  signal?.throwIfAborted();
  return materializers.get(root)?.(ids, signal) ?? (() => undefined);
}
