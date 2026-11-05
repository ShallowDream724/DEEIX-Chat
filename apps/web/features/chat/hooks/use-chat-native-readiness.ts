"use client";

import * as React from "react";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";
import { nativeBadgeFileIDs, nativeFileIdentity } from "@/features/chat/model/native-readiness";
import { getFileNativeReadiness } from "@/shared/api/file";
import { ApiError } from "@/shared/api/http-client";
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
  const scope = {
    model,
    conversationKey,
    temporary,
    conversationID: !temporary ? conversationID || undefined : undefined,
    parentMessagePublicID: !temporary ? parentMessagePublicID || undefined : undefined,
    selectedToolIDs: !temporary ? selectedToolIDs : undefined,
  };
  const scopeKey = JSON.stringify(scope);
  const requestKey = model ? JSON.stringify({
    ...scope,
    scopeKey,
    files: temporary ? [] : attachments.map((item) => [
      item.fileID, item.processingReady, item.extractStatus, item.ragReady, item.ragOptOut,
      nativeFileIdentity(item), item.processingErrorCode === "file.not_found",
    ]),
  }) : "";
  const [result, setResult] = React.useState<{
    key: string;
    model: string;
    scopeKey: string;
    fileIdentities: ReadonlyMap<string, string>;
    eligibleFileIDs: ReadonlySet<string>;
    issues: readonly InputLimitReason[];
    maxFilesPerMessage?: number;
  } | null>(null);
  const [unavailableKey, setUnavailableKey] = React.useState("");

  React.useEffect(() => {
    if (!requestKey) return;
    const controller = new AbortController();
    let requestTimeout: ReturnType<typeof setTimeout> | undefined;
    const timeout = setTimeout(() => {
      void (async () => {
        // Type assertion: requestKey was serialized locally above, never read from the server.
        const request = JSON.parse(requestKey) as {
          model: string;
          scopeKey: string;
          conversationID?: string;
          parentMessagePublicID?: string;
          selectedToolIDs?: number[];
          files: [string, unknown, unknown, unknown, unknown, string, boolean][];
        };
        // A failed request is not a new "unsupported" verdict. Keep the last
        // confirmed presentation, while the send path still checks everything.
        const settleUnavailable = () => setUnavailableKey(requestKey);
        requestTimeout = setTimeout(() => {
          settleUnavailable();
          controller.abort();
        }, 10_000);
        try {
          const token = await resolveAccessToken();
          if (controller.signal.aborted) return;
          if (!token) {
            setResult(null);
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
            scopeKey: request.scopeKey,
            fileIdentities: new Map(request.files.map(([fileID, , , , , identity]) => [fileID, identity])),
            eligibleFileIDs: new Set(response.files.filter((item) => item.eligible).map((item) => item.fileID)),
            issues: (response.issues ?? []).flatMap((issue) => {
              const reason = readInputLimitReason(issue);
              return reason ? [reason] : [];
            }),
            maxFilesPerMessage: response.messageFileCount,
          });
        } catch (error) {
          // If preflight is unavailable, sending still uses the authoritative backend check.
          if (!controller.signal.aborted) {
            // Ownership/authentication or invalid-file errors invalidate old badges.
            if (error instanceof ApiError && error.status >= 400 && error.status < 500) setResult(null);
            settleUnavailable();
          }
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

  // Validation belongs to the complete request. Badges retain confirmed native
  // files during revalidation, but never cross model/branch/tool scopes.
  const current = result?.key === requestKey ? result : null;
  return {
    eligibleFileIDs: current?.eligibleFileIDs ?? EMPTY_FILE_IDS,
    badgeFileIDs: nativeBadgeFileIDs(result, scopeKey, attachments),
    issues: current?.issues ?? EMPTY_ISSUES,
    maxFilesPerMessage: result?.scopeKey === scopeKey ? result.maxFilesPerMessage : undefined,
    pending: Boolean(requestKey) && !current && unavailableKey !== requestKey,
  };
}
