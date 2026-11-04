"use client";

import * as React from "react";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";
import { getFileNativeReadiness } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { readInputLimitReason, type InputLimitReason } from "@/shared/lib/input-limit";

const EMPTY_FILE_IDS: ReadonlySet<string> = new Set();
const EMPTY_ISSUES: readonly InputLimitReason[] = [];

export function useChatNativeReadiness({
  attachments,
  model,
  conversationKey,
  conversationID,
  parentMessagePublicID,
  selectedToolIDs,
  temporary,
}: {
  attachments: PendingAttachment[];
  model: string;
  conversationKey: string;
  conversationID?: string | null;
  parentMessagePublicID?: string | null;
  selectedToolIDs: number[];
  temporary: boolean;
}) {
  // Request the effective channel limits even when the composer has no files.
  const requestKey = model ? JSON.stringify({
    model,
    conversationKey,
    conversationID: !temporary ? conversationID || undefined : undefined,
    parentMessagePublicID: !temporary ? parentMessagePublicID || undefined : undefined,
    selectedToolIDs: !temporary ? selectedToolIDs : undefined,
    files: temporary ? [] : attachments.map((item) => [
      item.fileID, item.processingReady, item.extractStatus, item.ragReady, item.ragOptOut,
    ]),
  }) : "";
  const [result, setResult] = React.useState<{
    key: string;
    model: string;
    eligibleFileIDs: ReadonlySet<string>;
    issues: readonly InputLimitReason[];
    maxFilesPerMessage?: number;
  } | null>(null);

  React.useEffect(() => {
    if (!requestKey) return;
    const controller = new AbortController();
    let requestTimeout: ReturnType<typeof setTimeout> | undefined;
    const timeout = setTimeout(() => {
      void (async () => {
        // Type assertion: requestKey was serialized locally above, never read from the server.
        const request = JSON.parse(requestKey) as {
          model: string;
          conversationID?: string;
          parentMessagePublicID?: string;
          selectedToolIDs?: number[];
          files: [string, ...unknown[]][];
        };
        const fallback = {
          key: requestKey, model: request.model, eligibleFileIDs: EMPTY_FILE_IDS, issues: EMPTY_ISSUES,
        };
        const settleUnavailable = () => setResult((previous) => previous?.model === request.model
          ? { ...fallback, maxFilesPerMessage: previous.maxFilesPerMessage }
          : fallback);
        requestTimeout = setTimeout(() => {
          settleUnavailable();
          controller.abort();
        }, 10_000);
        try {
          const token = await resolveAccessToken();
          if (controller.signal.aborted) return;
          if (!token) {
            settleUnavailable();
            return;
          }
          const params = {
            model: request.model,
            fileIDs: request.files.map(([fileID]) => fileID),
            conversationID: request.conversationID,
            parentMessagePublicID: request.parentMessagePublicID,
            selectedToolIDs: request.selectedToolIDs,
          };
          const response = await getFileNativeReadiness(token, params, controller.signal);
          if (controller.signal.aborted) return;
          setResult({
            key: requestKey,
            model: request.model,
            eligibleFileIDs: new Set(response.files.filter((item) => item.eligible).map((item) => item.fileID)),
            issues: (response.issues ?? []).flatMap((issue) => {
              const reason = readInputLimitReason(issue);
              return reason ? [reason] : [];
            }),
            maxFilesPerMessage: response.messageFileCount,
          });
        } catch {
          // If preflight is unavailable, sending still uses the authoritative backend check.
          if (!controller.signal.aborted) settleUnavailable();
        } finally {
          clearTimeout(requestTimeout);
        }
      })();
    }, 150);
    return () => {
      clearTimeout(timeout);
      clearTimeout(requestTimeout);
      controller.abort();
    };
  }, [requestKey]);

  // Only channel limits can be reused as files change; eligibility and issues
  // belong to the complete request (model, current files and selected branch).
  const current = result?.key === requestKey ? result : null;
  return {
    eligibleFileIDs: current?.eligibleFileIDs ?? EMPTY_FILE_IDS,
    issues: current?.issues ?? EMPTY_ISSUES,
    maxFilesPerMessage: result?.model === model ? result.maxFilesPerMessage : undefined,
    pending: Boolean(requestKey) && !current,
  };
}
