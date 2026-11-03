import type { MessageAttachment } from "@/features/chat/types/messages";

// Only stored attachments of this message may become authenticated downloads.
// URL extensions and user-facing link labels are never file identity evidence.
export function resolveMessageFileLink(
  href: string,
  attachments: MessageAttachment[],
  allowedOrigins: string[],
): MessageAttachment | null {
  try {
    const relative = href.startsWith("/") && !href.startsWith("//");
    const url = new URL(href, "https://message-file.invalid");
    if (!(relative && url.origin === "https://message-file.invalid") && !allowedOrigins.includes(url.origin)) {
      return null;
    }
    const match = url.pathname.match(/^\/api\/v1\/(?:shared-conversations\/[^/]+\/)?files\/([^/]+)\/content$/);
    if (!match) {
      return null;
    }
    const fileID = decodeURIComponent(match[1]);
    return attachments.find((attachment) => attachment.fileID === fileID) ?? null;
  } catch {
    return null;
  }
}
