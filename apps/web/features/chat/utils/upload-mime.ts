// Browser metadata is advisory. The upload service verifies file bytes and
// applies the authoritative administrator allowlist for every upload path.
export const TEXT_FILE_EXTENSIONS = [
  "txt", "js", "ts", "tsx", "jsx", "mjs", "py", "go", "rs", "java", "c", "cpp", "h", "hpp",
  "cs", "php", "rb", "swift", "kt", "bash", "zsh", "sql", "yaml", "yml", "toml", "sh",
  "html", "htm", "css", "xml", "xhtml", "svg", "ini", "conf", "tex", "bib", "cls", "sty",
  "ipynb", "jsonl", "ndjson", "tsv", "srt", "vtt", "log",
] as const;

export const ACTIVE_FILE_EXTENSIONS = new Set(["html", "htm", "css", "js", "jsx", "mjs", "ts", "tsx", "xml", "xhtml", "svg"]);
export const ACTIVE_UPLOAD_MIMES = new Set([
  "text/html", "text/css", "text/javascript", "text/xml", "application/javascript", "application/ecmascript",
  "application/x-javascript", "application/typescript", "application/xml", "application/xhtml+xml", "image/svg+xml",
]);

const EXTENSION_MIMES: Record<string, string> = {
  mp4: "video/mp4", webm: "video/webm", mov: "video/quicktime", mpeg: "video/mpeg", mpg: "video/mpeg",
  mp3: "audio/mpeg", wav: "audio/wav", m4a: "audio/mp4", aac: "audio/aac", ogg: "audio/ogg", oga: "audio/ogg",
  flac: "audio/flac", aif: "audio/aiff", aiff: "audio/aiff",
  pdf: "application/pdf", docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  doc: "application/msword", pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  ppt: "application/vnd.ms-powerpoint", xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  xls: "application/vnd.ms-excel", csv: "text/csv", md: "text/markdown", markdown: "text/markdown",
  json: "application/json", yaml: "text/yaml", yml: "text/yaml", toml: "application/toml",
  zip: "application/zip", "7z": "application/x-7z-compressed", rar: "application/vnd.rar", tar: "application/x-tar",
  gz: "application/gzip", tgz: "application/gzip", bz2: "application/x-bzip2", tbz: "application/x-bzip2",
  tbz2: "application/x-bzip2", xz: "application/x-xz", txz: "application/x-xz", zst: "application/zstd",
};

export function resolveFileExtension(fileName: string): string {
  const normalizedName = fileName.trim().toLowerCase();
  return normalizedName.includes(".") ? normalizedName.split(".").pop() || "" : "";
}

export function normalizeUploadMimeValue(raw: string): string {
  const value = raw.split(";", 1)[0].trim().toLowerCase();
  switch (value) {
    case "application/x-zip":
    case "application/x-zip-compressed": return "application/zip";
    case "application/x-rar":
    case "application/x-rar-compressed": return "application/vnd.rar";
    case "application/x-gzip":
    case "application/gzipped":
    case "application/x-gunzip": return "application/gzip";
    case "application/bzip2":
    case "application/x-bzip": return "application/x-bzip2";
    case "application/x-zstd": return "application/zstd";
    default: return value;
  }
}

export function normalizeUploadMimeForPolicy(file: Pick<File, "name" | "type">): string {
  const browserMime = normalizeUploadMimeValue(file.type);
  const ext = resolveFileExtension(file.name);
  if (ACTIVE_FILE_EXTENSIONS.has(ext) || ACTIVE_UPLOAD_MIMES.has(browserMime)) return "text/plain";
  if (browserMime.startsWith("image/") || browserMime.startsWith("audio/") || browserMime.startsWith("video/") || browserMime === "application/pdf") {
    return browserMime;
  }
  if (EXTENSION_MIMES[ext]) return EXTENSION_MIMES[ext];
  if ((TEXT_FILE_EXTENSIONS as readonly string[]).includes(ext)) return "text/plain";
  return browserMime;
}
