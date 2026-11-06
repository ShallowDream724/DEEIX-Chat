import { Files, type LucideIcon } from "lucide-react";
import { isCodeFilename } from "./file-code-language";
import { FILE_TYPE_ICONS } from "./file-type-icons";

export type FileFilterKey =
  | "all"
  | "image"
  | "document"
  | "spreadsheet"
  | "presentation"
  | "code"
  | "pdf"
  | "audio"
  | "video";

export type FilePreviewKind =
  | "image"
  | "pdf"
  | "audio"
  | "video"
  | "docx"
  | "spreadsheet"
  | "native"
  | "markdown"
  | "code"
  | "text"
  | "unsupported";

type FileDisplaySource = {
  fileName: string;
  mimeType: string;
};

const MARKDOWN_EXTENSIONS = new Set(["md", "mdx", "markdown"]);
const PLAIN_TEXT_EXTENSIONS = new Set(["txt", "log", "csv"]);
const IMAGE_EXTENSIONS = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "heic", "heif"]);
const DOCUMENT_EXTENSIONS = new Set(["doc", "docx", "rtf", "odt", "pages"]);
const SPREADSHEET_EXTENSIONS = new Set(["xls", "xlsx", "csv", "ods", "numbers"]);
const PRESENTATION_EXTENSIONS = new Set(["ppt", "pptx", "odp", "key"]);
const ARCHIVE_EXTENSIONS = new Set(["zip", "7z", "rar", "tar", "gz", "tgz", "bz2", "tbz", "tbz2", "xz", "txz", "zst", "zstd", "tzst", "lz4", "cab"]);
const AUDIO_EXTENSIONS = new Set(["mp3", "wav", "flac", "ogg", "opus", "m4a", "aac", "aiff", "aif", "wma", "mid", "midi"]);
const VIDEO_EXTENSIONS = new Set(["mp4", "mov", "webm", "avi", "mkv", "mpeg", "mpg", "m4v", "wmv", "flv", "3gp"]);
const FILE_FILTER_ICONS: Record<FileFilterKey, LucideIcon> = {
  all: Files,
  image: FILE_TYPE_ICONS.image,
  document: FILE_TYPE_ICONS.text,
  spreadsheet: FILE_TYPE_ICONS.spreadsheet,
  presentation: FILE_TYPE_ICONS.presentation,
  code: FILE_TYPE_ICONS.code,
  pdf: FILE_TYPE_ICONS.pdf,
  audio: FILE_TYPE_ICONS.audio,
  video: FILE_TYPE_ICONS.video,
};

function isDocxMimeType(mimeType: string): boolean {
  return mimeType.includes("officedocument.wordprocessingml.document");
}

function isSpreadsheetMimeType(mimeType: string): boolean {
  return mimeType.includes("officedocument.spreadsheetml") || mimeType.includes("vnd.ms-excel");
}

function isOfficeDocumentMimeType(mimeType: string): boolean {
  return (
    mimeType.includes("msword") ||
    isDocxMimeType(mimeType) ||
    isSpreadsheetMimeType(mimeType) ||
    mimeType.includes("officedocument.presentationml") ||
    mimeType.includes("vnd.ms-powerpoint") ||
    mimeType.includes("rtf") ||
    mimeType.includes("opendocument.text") ||
    mimeType.includes("opendocument.spreadsheet") ||
    mimeType.includes("opendocument.presentation")
  );
}

export function resolveFileExtension(fileName: string): string {
  const normalized = fileName.trim().toLowerCase();
  const segments = normalized.split(".");
  return segments.length > 1 ? segments[segments.length - 1] || "" : "";
}

export function formatBytes(sizeBytes: number): string {
  if (!Number.isFinite(sizeBytes) || sizeBytes <= 0) {
    return "0 B";
  }

  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(Math.floor(Math.log(sizeBytes) / Math.log(1024)), units.length - 1);
  const value = sizeBytes / 1024 ** exponent;
  return `${value >= 100 || exponent === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[exponent]}`;
}

export function formatDateTime(value: string | null, locale = "en-US"): string {
  if (!value) {
    return "-";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }

  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatCompactDateTime(value: string | null, locale = "en-US"): string {
  if (!value) {
    return "-";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }

  return new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatFileStatus(status: string): string {
  const normalized = status.trim().toLowerCase();
  if (!normalized) {
    return "Unknown";
  }
  if (["active", "ready", "available", "success"].includes(normalized)) {
    return "Available";
  }
  if (["pending", "processing"].includes(normalized)) {
    return "Processing";
  }
  if (["failed", "error"].includes(normalized)) {
    return "Failed";
  }
  return status;
}

export function isReadableTextContent(content: string): boolean {
  const sample = content.slice(0, 4000);
  if (!sample) {
    return true;
  }

  let replacementCount = 0;
  let controlCount = 0;

  for (const char of sample) {
    const code = char.charCodeAt(0);
    if (char === "\uFFFD") {
      replacementCount += 1;
      continue;
    }

    const isAllowedWhitespace = code === 9 || code === 10 || code === 13;
    if ((code < 32 && !isAllowedWhitespace) || code === 127) {
      controlCount += 1;
    }
  }

  return replacementCount / sample.length < 0.08 && controlCount / sample.length < 0.02;
}

export function isFileReady(status: string): boolean {
  return ["active", "ready", "available", "success"].includes(status.trim().toLowerCase());
}

export function resolveFileFilter(file: FileDisplaySource): FileFilterKey {
  const mimeType = file.mimeType.trim().toLowerCase();
  const extension = resolveFileExtension(file.fileName);

  if (mimeType.startsWith("image/") || IMAGE_EXTENSIONS.has(extension)) {
    return "image";
  }
  if (mimeType === "application/pdf" || extension === "pdf") {
    return "pdf";
  }
  if (mimeType.startsWith("audio/")) {
    return "audio";
  }
  if (mimeType.startsWith("video/")) {
    return "video";
  }
  if (SPREADSHEET_EXTENSIONS.has(extension) || mimeType.includes("spreadsheet") || mimeType.includes("excel") || mimeType.includes("csv")) {
    return "spreadsheet";
  }
  if (PRESENTATION_EXTENSIONS.has(extension) || mimeType.includes("presentation") || mimeType.includes("powerpoint")) {
    return "presentation";
  }
  if (DOCUMENT_EXTENSIONS.has(extension) || mimeType.includes("word") || mimeType.includes("rtf") || mimeType.includes("opendocument.text")) {
    return "document";
  }
  if (
    MARKDOWN_EXTENSIONS.has(extension) ||
    isCodeFilename(file.fileName) ||
    mimeType.startsWith("text/") ||
    mimeType.includes("json") ||
    mimeType.includes("javascript") ||
    mimeType.includes("typescript") ||
    mimeType.includes("xml") ||
    mimeType.includes("html") ||
    mimeType.includes("css") ||
    mimeType.includes("yaml") ||
    mimeType.includes("toml") ||
    mimeType.includes("sql") ||
    mimeType.includes("markdown")
  ) {
    return "code";
  }
  return "document";
}

export function resolveFileFilterIcon(filter: FileFilterKey): LucideIcon {
  return FILE_FILTER_ICONS[filter];
}

export function resolveFileIcon(file: FileDisplaySource): LucideIcon {
  const extension = resolveFileExtension(file.fileName);
  const mimeType = file.mimeType.split(";")[0]?.trim().toLowerCase() ?? "";
  if (extension === "gif" || mimeType === "image/gif") return FILE_TYPE_ICONS.gif;
  if (MARKDOWN_EXTENSIONS.has(extension) || mimeType.includes("markdown")) return FILE_TYPE_ICONS.markdown;
  if (ARCHIVE_EXTENSIONS.has(extension) || /(?:zip|gzip|x-rar|vnd\.rar|x-tar|x-7z|x-bzip|x-xz|zstd)/.test(mimeType)) return FILE_TYPE_ICONS.archive;
  if (AUDIO_EXTENSIONS.has(extension)) return FILE_TYPE_ICONS.audio;
  if (VIDEO_EXTENSIONS.has(extension)) return FILE_TYPE_ICONS.video;
  if (extension === "doc" || extension === "docx" || mimeType.includes("msword") || isDocxMimeType(mimeType)) return FILE_TYPE_ICONS.word;
  if (extension === "csv" || extension === "tsv" || mimeType.includes("csv") || mimeType === "text/tab-separated-values") return FILE_TYPE_ICONS.data;
  const filter = resolveFileFilter(file);
  if (filter !== "code" && filter !== "document") return resolveFileFilterIcon(filter);
  if (isCodeFilename(file.fileName)) return FILE_TYPE_ICONS.code;
  if (PLAIN_TEXT_EXTENSIONS.has(extension) || mimeType === "text/plain") return FILE_TYPE_ICONS.text;
  if (filter === "document") return DOCUMENT_EXTENSIONS.has(extension) || mimeType.startsWith("text/") ? FILE_TYPE_ICONS.text : FILE_TYPE_ICONS.file;
  return resolveFileFilterIcon(filter);
}

export function resolveFilePreviewKind(file: FileDisplaySource, contentType: string): FilePreviewKind {
  const mimeType = contentType.split(";")[0]?.trim().toLowerCase() || file.mimeType.trim().toLowerCase();
  const extension = resolveFileExtension(file.fileName);
  const filter = resolveFileFilter(file);

  if (extension === "docx" || isDocxMimeType(mimeType)) {
    return "docx";
  }
  if (extension === "csv" || mimeType === "text/csv") {
    return "spreadsheet";
  }
  if (
    (DOCUMENT_EXTENSIONS.has(extension) && extension !== "docx") ||
    PRESENTATION_EXTENSIONS.has(extension) ||
    (SPREADSHEET_EXTENSIONS.has(extension) && extension !== "csv") ||
    isOfficeDocumentMimeType(mimeType)
  ) {
    return "native";
  }

  if (filter === "image") {
    return "image";
  }
  if (filter === "pdf") {
    return "pdf";
  }
  if (filter === "audio") {
    return "audio";
  }
  if (filter === "video") {
    return "video";
  }
  if (MARKDOWN_EXTENSIONS.has(extension) || mimeType.includes("markdown")) {
    return "markdown";
  }
  if (
    isCodeFilename(file.fileName) ||
    mimeType.includes("json") ||
    mimeType.includes("javascript") ||
    mimeType.includes("typescript") ||
    mimeType.includes("xml") ||
    mimeType.includes("html") ||
    mimeType.includes("css") ||
    mimeType.includes("yaml") ||
    mimeType.includes("toml") ||
    mimeType.includes("sql")
  ) {
    return "code";
  }
  if (mimeType.startsWith("text/") || PLAIN_TEXT_EXTENSIONS.has(extension)) {
    return "text";
  }
  return "unsupported";
}

export function isImageFile(file: FileDisplaySource): boolean {
  return resolveFileFilter(file) === "image";
}
