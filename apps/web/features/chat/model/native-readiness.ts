import type { PendingAttachment } from "@/features/chat/types/chat-runtime";

const EMPTY_FILE_IDS: ReadonlySet<string> = new Set();

export type NativeBadgeSnapshot = {
  scopeKey: string;
  eligibleFileIDs: ReadonlySet<string>;
  fileIdentities: ReadonlyMap<string, string>;
};

// Extraction progress does not change the original file's native capability.
export function nativeFileIdentity(file: PendingAttachment): string {
  return JSON.stringify([file.fileID, file.sizeBytes, file.mimeType, file.detectedMime, file.fileCategory]);
}

// Presentation only. Sending must use the latest complete readiness response.
export function nativeBadgeFileIDs(
  snapshot: NativeBadgeSnapshot | null,
  scopeKey: string,
  files: readonly PendingAttachment[],
): ReadonlySet<string> {
  if (!snapshot || snapshot.scopeKey !== scopeKey) return EMPTY_FILE_IDS;
  return new Set(files.filter((file) => file.processingErrorCode !== "file.not_found"
    && snapshot.eligibleFileIDs.has(file.fileID)
    && snapshot.fileIdentities.get(file.fileID) === nativeFileIdentity(file)).map((file) => file.fileID));
}
