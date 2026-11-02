"use client";

import * as React from "react";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";
import { getFileNativeReadiness } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";

const EMPTY_FILE_IDS: ReadonlySet<string> = new Set();

export function useChatNativeReadiness({
  attachments,
  model,
  conversationKey,
  conversationID,
  temporary,
}: {
  attachments: PendingAttachment[];
  model: string;
  conversationKey: string;
  conversationID?: string | null;
  temporary: boolean;
}) {
  // Readiness belongs to the current model and complete attachment budget, never the file.
  // Recheck extraction readiness too: completed extraction can reveal native page limits.
  const requestKey = !temporary && model && attachments.some((item) =>
    item.fileCategory !== "image" && !(item.detectedMime || item.mimeType).startsWith("image/"))
    ? JSON.stringify({
      model,
      conversationKey,
      conversationID: conversationID || undefined,
      files: attachments.map((item) => [
        item.fileID, item.fileName, item.detectedMime || item.mimeType, item.fileCategory, item.sizeBytes,
        item.processingReady, item.extractStatus,
      ]),
    })
    : "";
  const [result, setResult] = React.useState<{ key: string; eligibleFileIDs: ReadonlySet<string> } | null>(null);
  const sequenceRef = React.useRef(0);

  React.useEffect(() => {
    const sequence = ++sequenceRef.current;
    if (!requestKey) {
      return;
    }
    const controller = new AbortController();
    let requestTimeout: ReturnType<typeof setTimeout> | undefined;
    // Combine uploads that finish close together into one budget preflight.
    const timeout = setTimeout(() => {
      void (async () => {
        requestTimeout = setTimeout(() => {
          if (sequence === sequenceRef.current) {
            setResult({ key: requestKey, eligibleFileIDs: EMPTY_FILE_IDS });
          }
          controller.abort();
        }, 10_000);
        let eligibleFileIDs: ReadonlySet<string> = EMPTY_FILE_IDS;
        try {
          const token = await resolveAccessToken();
          if (!token || controller.signal.aborted) {
            return;
          }
          // Type assertion: this is the effect's locally serialized snapshot from
          // JSON.stringify above, not data read from an API, storage, or an event.
          const request = JSON.parse(requestKey) as {
            model: string; conversationID?: string; files: [string, ...unknown[]][];
          };
          const response = await getFileNativeReadiness(token, {
            model: request.model,
            fileIDs: request.files.map(([fileID]) => fileID),
            conversationID: request.conversationID,
          }, controller.signal);
          eligibleFileIDs = new Set(response.files.filter((item) => item.eligible).map((item) => item.fileID));
        } catch {
          // An unavailable preflight leaves the existing processing UI in place.
        } finally {
          clearTimeout(requestTimeout);
          if (!controller.signal.aborted && sequence === sequenceRef.current) {
            setResult({ key: requestKey, eligibleFileIDs });
          }
        }
      })();
    }, 150);
    return () => {
      clearTimeout(timeout);
      clearTimeout(requestTimeout);
      controller.abort();
    };
  }, [requestKey]);

  // Invalidate during render so a model or conversation change cannot flash old readiness.
  const current = result?.key === requestKey;
  return {
    eligibleFileIDs: current && result ? result.eligibleFileIDs : EMPTY_FILE_IDS,
    pending: Boolean(requestKey) && !current,
  };
}
