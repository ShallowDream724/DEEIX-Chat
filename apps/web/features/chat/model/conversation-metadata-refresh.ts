import { getConversation } from "@/shared/api/conversation";
import type { ConversationDTO, SendMessageResult } from "@/shared/api/conversation-types";
import { ApiError } from "@/shared/api/http-client";

// Title and labels have independent bounded backend attempts. Do not stop
// observing after 45 seconds while their first 90-second attempt is still live.
const CONVERSATION_METADATA_REFRESH_MAX_WAIT_MS = 10 * 60_000;
const CONVERSATION_METADATA_REFRESH_MAX_READS = 48;
const CONVERSATION_METADATA_REFRESH_INITIAL_DELAY_MS = 800;
const CONVERSATION_METADATA_REFRESH_MAX_DELAY_MS = 15_000;
const CONVERSATION_METADATA_REFRESH_BACKOFF = 1.5;
const metadataRefreshes = new Map<string, Promise<void>>();

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function normalizeLabelsJSON(value: string | null | undefined): string {
  const normalized = value?.trim();
  return normalized && normalized !== "null" ? normalized : "[]";
}

export function isPlaceholderConversationTitle(title: string): boolean {
  const value = title.trim().toLowerCase();
  return ["new chat", "新对话"].includes(value);
}

function isFallbackConversationTitle(title: string, fallbackTitle: string): boolean {
  const normalizedFallback = fallbackTitle.trim();
  return normalizedFallback !== "" && title.trim() === normalizedFallback;
}

export function conversationTitleFromFirstUserMessage(content: string): string {
  const value = content.trim().replace(/\s+/g, " ").replace(/^[\s"'`“”‘’]+|[\s"'`“”‘’]+$/g, "");
  if (!value) {
    return "";
  }
  return Array.from(value).slice(0, 16).join("").trim();
}

function hasPendingGeneratedConversationMetadata(
  item: ConversationDTO | null,
  autoGenerateLabels: boolean,
  fallbackTitle = "",
  autoGenerateTitle = true,
): boolean {
  if (!autoGenerateTitle && !autoGenerateLabels) return false;
  return (
    !item ||
    (autoGenerateTitle && (isPlaceholderConversationTitle(item.title) ||
    isFallbackConversationTitle(item.title, fallbackTitle))) ||
    (autoGenerateLabels && normalizeLabelsJSON(item.labelsJSON) === "[]")
  );
}

function hasGeneratedConversationMetadataChanged(
  previous: ConversationDTO | null,
  next: ConversationDTO,
): boolean {
  const previousTitle = previous?.title?.trim() ?? "";
  const nextTitle = next.title.trim();
  if (nextTitle && nextTitle !== previousTitle && !isPlaceholderConversationTitle(nextTitle)) {
    return true;
  }
  return normalizeLabelsJSON(next.labelsJSON) !== normalizeLabelsJSON(previous?.labelsJSON);
}

export function shouldPollGeneratedConversationMetadata(
  item: ConversationDTO | null,
  result: SendMessageResult | null | undefined,
  autoGenerateLabels: boolean,
  fallbackTitle = "",
  autoGenerateTitle = true,
): boolean {
  if (!hasPendingGeneratedConversationMetadata(item, autoGenerateLabels, fallbackTitle, autoGenerateTitle)) {
    return false;
  }
  const hint = result?.metadataRefreshHint?.trim();
  if (!hint) {
    return true;
  }
  return hint === "pending";
}

export function refreshGeneratedConversationMetadata(
  accessToken: string,
  conversationPublicID: string,
  previous: ConversationDTO | null,
  autoGenerateLabels: boolean,
  fallbackTitle: string,
  touchByPublicID: (publicID: string, patch: Partial<ConversationDTO>) => void,
  autoGenerateTitle = true,
): Promise<void> {
  const key = `${accessToken}\0${conversationPublicID}`;
  const current = metadataRefreshes.get(key);
  if (current) return current;
  const refresh = pollGeneratedConversationMetadata(accessToken, conversationPublicID, previous, autoGenerateLabels, fallbackTitle, touchByPublicID, autoGenerateTitle)
    .finally(() => { if (metadataRefreshes.get(key) === refresh) metadataRefreshes.delete(key); });
  metadataRefreshes.set(key, refresh);
  return refresh;
}

async function pollGeneratedConversationMetadata(
  accessToken: string,
  conversationPublicID: string,
  previous: ConversationDTO | null,
  autoGenerateLabels: boolean,
  fallbackTitle: string,
  touchByPublicID: (publicID: string, patch: Partial<ConversationDTO>) => void,
  autoGenerateTitle: boolean,
): Promise<void> {
  const deadline = Date.now() + CONVERSATION_METADATA_REFRESH_MAX_WAIT_MS;
  let delayMS = CONVERSATION_METADATA_REFRESH_INITIAL_DELAY_MS;
  let current = previous;

  for (let reads = 0; reads < CONVERSATION_METADATA_REFRESH_MAX_READS && Date.now() < deadline; reads += 1) {
    const nextDelayMS = Math.min(delayMS, deadline - Date.now());
    await sleep(nextDelayMS);
    if (Date.now() >= deadline) return;
    delayMS = Math.min(Math.round(delayMS * CONVERSATION_METADATA_REFRESH_BACKOFF), CONVERSATION_METADATA_REFRESH_MAX_DELAY_MS);

    let latest: ConversationDTO;
    try {
      latest = await getConversation(accessToken, conversationPublicID, AbortSignal.timeout(Math.max(1, Math.min(5000, deadline - Date.now()))));
    } catch (error) {
      if (error instanceof ApiError && [401, 403, 404].includes(error.status)) return;
      continue;
    }
    if (hasGeneratedConversationMetadataChanged(current, latest)) {
      touchByPublicID(conversationPublicID, latest);
      current = latest;
    }
    if (!hasPendingGeneratedConversationMetadata(latest, autoGenerateLabels, fallbackTitle, autoGenerateTitle)) return;
  }
}
