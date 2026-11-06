import type { FilePreviewKind } from "@/entities/file/lib/file-display";

// The panel can load the selected renderer while its file is being fetched.
// These same imports also back next/dynamic; other formats stay unloaded.
export async function loadPdfPreview() {
  const [viewer] = await Promise.all([import("./preview-pdf"), import("pdfjs-dist/build/pdf.mjs")]);
  return viewer.PreviewPdf;
}

export async function loadDocxPreview() {
  const [viewer] = await Promise.all([import("./preview-docx"), import("docx-preview")]);
  return viewer.PreviewDocx;
}

export const loadSheetPreview = () => import("./preview-sheet").then((mod) => mod.PreviewSheet);
export const loadMediaPreview = () => import("./preview-media").then((mod) => mod.PreviewMedia);
export const loadTextPreview = () => import("./preview-text").then((mod) => mod.PreviewText);

export function preloadFileViewer(kind: FilePreviewKind): Promise<unknown> | undefined {
  switch (kind) {
    case "pdf": return loadPdfPreview();
    case "docx": return loadDocxPreview();
    case "spreadsheet": return loadSheetPreview();
    case "image": case "audio": case "video": return loadMediaPreview();
    case "text": case "code": case "markdown": return loadTextPreview();
    default: return undefined;
  }
}
