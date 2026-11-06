"use client";

import * as React from "react";
import { fetchFileContent } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { createImageThumbnail } from "@/entities/file/lib/image-thumbnail";
import type { FileContentLoader } from "@/entities/file/hooks/use-file-preview-dialog";

export type ThumbnailFile = {
  fileID: string;
  fileName: string;
  mimeType: string;
  previewURL?: string;
  localFile?: File;
  sizeBytes?: number;
};

export function useFileThumbnail(file: ThumbnailFile, loadContent?: FileContentLoader) {
  const { fileID, previewURL, localFile } = file;
  const latest = React.useRef({ file, loadContent });
  latest.current = { file, loadContent };
  const hostRef = React.useRef<HTMLDivElement>(null);
  const [visible, setVisible] = React.useState(false);
  const [source, setSource] = React.useState<{ key: string; url: string } | null>(null);
  const key = `${fileID}:${previewURL ?? ""}`;

  React.useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        setVisible(true);
        observer.disconnect();
      }
    });
    observer.observe(host);
    return () => observer.disconnect();
  }, []);

  React.useEffect(() => {
    if (!visible) return;
    const { file: requestedFile, loadContent: loader } = latest.current;
    const controller = new AbortController();
    const { signal } = controller;
    let ownedURL: string | undefined;
    void (async () => {
      try {
        const thumbnail = await createImageThumbnail(async () => {
          if (loader) return (await loader({ ...requestedFile, sizeBytes: requestedFile.sizeBytes ?? 0 }, signal)).blob;
          if (localFile) return localFile;
          if (previewURL) {
            const response = await fetch(previewURL, { signal });
            return response.blob();
          }
          const token = await resolveAccessToken();
          signal.throwIfAborted();
          if (!token) throw new Error("Authentication required");
          return (await fetchFileContent(token, fileID, signal)).blob;
        }, signal);
        signal.throwIfAborted();
        ownedURL = URL.createObjectURL(thumbnail);
        setSource({ key, url: ownedURL });
      } catch {
        // A preview failure does not change upload or model-input eligibility.
      }
    })();
    return () => {
      controller.abort();
      if (ownedURL) URL.revokeObjectURL(ownedURL);
    };
  }, [fileID, key, localFile, previewURL, visible]);

  return { hostRef, source: source?.key === key ? source.url : null };
}
