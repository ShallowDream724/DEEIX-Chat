import { ApiError } from "@/shared/api/http-client";
import { isRecord } from "@/shared/lib/type-guards";

export const INPUT_LIMIT_DIMENSIONS = [
  "messageFileCount", "imageCount", "imageSingleBytes", "imageBytes",
  "attachmentCount", "attachmentSingleBytes", "attachmentBytes",
  "nativeCount", "nativeBytes", "pdfSingleBytes", "pdfBytes", "pdfPages", "nonPDFBytes",
  "imageEncodedBytes", "imageWidth", "imageHeight", "requestBytes", "uploadFileBytes",
  "parsedTextBytes", "parsedTextTokens", "parsedPDFPages",
  "contextTokens", "currentTurnTokens", "temporaryMessageCount", "temporaryTextLength", "temporarySingleTextLength",
  "temporaryFileCount", "temporaryRequestBytes", "fileUnsupported", "fileUnavailable",
  "fileParseFailed", "fileReadFailed", "modelTaskUnsupported",
] as const;

export type InputLimitDimension = typeof INPUT_LIMIT_DIMENSIONS[number];
export type InputLimitReason = {
  dimension: InputLimitDimension;
  actual?: number;
  limit?: number;
  fileID?: string;
  fileName?: string;
  // Local task restrictions use the existing operation-specific explanation.
  description?: string;
  fileCategory?: "image" | "audio" | "video" | "document" | "pdf";
  scope?: "current" | "history" | "mixed";
};

const dimensions = new Set<string>(INPUT_LIMIT_DIMENSIONS);
function isInputLimitDimension(value: string): value is InputLimitDimension {
  return dimensions.has(value);
}

export function readInputLimitReason(value: unknown): InputLimitReason | undefined {
  if (!isRecord(value) || typeof value.dimension !== "string") return;
  const rawDimension = value.dimension.replace(/^provider\./, "");
  const dimension = rawDimension === "unsupported" ? "fileUnsupported"
    : rawDimension === "unavailable" ? "fileUnavailable" : rawDimension;
  if (!isInputLimitDimension(dimension)) return;
  const numeric = (amount: unknown) =>
    typeof amount === "number" && Number.isSafeInteger(amount) && amount >= 0 ? amount : undefined;
  const scope = value.scope === "current" || value.scope === "history" || value.scope === "mixed"
    ? value.scope
    : value.current === true ? "current" : value.current === false ? "history" : undefined;
  const category = value.fileCategory ?? value.category ?? value.kind;
  const fileCategory = category === "image" || category === "audio" || category === "video"
    || category === "document" || category === "pdf" ? category : undefined;
  return {
    dimension,
    actual: value.actual === -1 ? -1 : numeric(value.actual),
    limit: numeric(value.limit),
    fileID: typeof value.fileID === "string" ? value.fileID.trim() || undefined : undefined,
    fileName: typeof value.fileName === "string" ? value.fileName.trim() || undefined : undefined,
    fileCategory,
    scope: value.scope === "context" ? "mixed" : scope,
  };
}

export function getInputLimitReasons(error: unknown): InputLimitReason[] {
  if (!(error instanceof ApiError)) return [];
  const details = isRecord(error.details) ? error.details : undefined;
  const fallbackDimension = error.errorCode === "file.model_unsupported" ? "fileUnsupported"
    : (error.errorCode === "file.invalid_reference" || error.errorCode === "file.not_found") && details?.scope
      ? "fileUnavailable"
      : error.errorCode === "file.too_large_for_context" ? "parsedTextBytes"
      : undefined;
  const attachmentFailure = (error.errorCode === "file.not_ready" || error.errorCode === "file.not_found")
    && (details?.dimension === "fileParseFailed" || details?.dimension === "fileReadFailed");
  if (error.errorCode !== "file.too_large" && !fallbackDimension && !attachmentFailure) return [];
  const candidates = isRecord(error.details) && Array.isArray(error.details.reasons)
    ? error.details.reasons
    : [{ ...details, dimension: details?.dimension ?? fallbackDimension }];
  const reasons = candidates.flatMap((value) => {
    const reason = readInputLimitReason(value);
    return reason ? [reason] : [];
  });
  return reasons;
}
