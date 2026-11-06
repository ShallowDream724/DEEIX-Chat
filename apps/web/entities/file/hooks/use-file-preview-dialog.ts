"use client";

import * as React from "react";
import { useTranslations } from "next-intl";

import { useLocalizedErrorMessage } from "@/i18n/use-localized-error";
import { fetchFileContent, type FileContentResult } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { isReadableTextContent, resolveFilePreviewKind } from "@/entities/file/lib/file-display";
import { readPreviewText } from "@/entities/file/components/file-preview/preview-content-data";

export type PreviewDialogFile = {
  fileID: string;
  fileName: string;
  mimeType: string;
  sizeBytes: number;
};

export type FilePreviewState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; message: string }
  | {
      status: "ready";
      fileID: string;
      kind: ReturnType<typeof resolveFilePreviewKind>;
      objectURL: string;
      textContent: string | null;
      contentType: string;
    };

export type FileContentLoader = (
  file: PreviewDialogFile,
  signal: AbortSignal,
) => Promise<FileContentResult>;

export function useFilePreviewDialog(file: PreviewDialogFile | null, loadContent?: FileContentLoader) {
  const t = useTranslations("files.previewDialog");
  const resolveErrorMessage = useLocalizedErrorMessage();
  const [state, setState] = React.useState<FilePreviewState>({ status: "idle" });
  const latest = React.useRef({ file, loadContent, resolveErrorMessage, t });
  latest.current = { file, loadContent, resolveErrorMessage, t };
  const fileID = file?.fileID;

  React.useEffect(() => {
    const { file: requestedFile, loadContent: loader } = latest.current;
    if (!requestedFile) {
      setState({ status: "idle" });
      return undefined;
    }

    let cancelled = false;
    let objectURL: string | null = null;
    const controller = new AbortController();
    setState({ status: "loading" });

    void (async () => {
      try {
        const result = loader
          ? await loader(requestedFile, controller.signal)
          : await (async () => {
              const token = await resolveAccessToken();
              if (!token) {
                  throw new Error(latest.current.t("sessionExpired"));
              }
              return fetchFileContent(token, requestedFile.fileID, controller.signal);
            })();
        if (cancelled || controller.signal.aborted) return;
        let kind = resolveFilePreviewKind(requestedFile, result.contentType);

        let textContent: string | null = null;
        if (kind === "markdown" || kind === "code" || kind === "text" || kind === "unsupported") {
          const text = await readPreviewText(result.blob, isReadableTextContent, controller.signal);
          if (text !== null) {
            textContent = text;
            if (kind === "unsupported") {
              kind = "text";
            }
          } else {
            kind = "unsupported";
          }
        }

        if (cancelled || controller.signal.aborted) {
          return;
        }
        objectURL = URL.createObjectURL(result.blob);
        setState({ status: "ready", fileID: requestedFile.fileID, kind, objectURL, textContent, contentType: result.contentType });
      } catch (error) {
        if (cancelled || controller.signal.aborted) {
          return;
        }
        setState({ status: "error", message: latest.current.resolveErrorMessage(error, latest.current.t("loadFailed")) });
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [fileID]);

  const download = React.useCallback(() => {
    if (state.status !== "ready" || !file || state.fileID !== file.fileID) {
      return;
    }
    const anchor = document.createElement("a");
    anchor.href = state.objectURL;
    anchor.download = file.fileName;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
  }, [file, state]);

  const currentState: FilePreviewState = state.status === "ready" && state.fileID !== fileID ? { status: "loading" } : state;
  return { state: currentState, download };
}
