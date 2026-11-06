"use client";

import * as React from "react";
import { fetchFileContent } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { createImageThumbnail } from "@/entities/file/lib/image-thumbnail";
import { resolveFileIcon } from "@/entities/file/lib/file-display";

type ThumbnailFile = {
  fileID: string;
  fileName: string;
  mimeType: string;
  previewURL?: string;
  localFile?: File;
};

export function FileThumbnail({ file }: { file: ThumbnailFile }) {
  const { fileID, previewURL, localFile } = file;
  const hostRef = React.useRef<HTMLDivElement>(null);
  const [visible, setVisible] = React.useState(false);
  const [source, setSource] = React.useState<{ key: string; url: string } | null>(null);
  const key = `${fileID}:${previewURL ?? ""}`;
  const Icon = resolveFileIcon(file);

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
    const controller = new AbortController();
    const { signal } = controller;
    let ownedURL: string | undefined;
    void (async () => {
      try {
        const thumbnail = await createImageThumbnail(async () => {
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

  return (
    <div ref={hostRef} className="flex size-full items-center justify-center bg-muted/40">
      {source?.key === key ? (
        <img src={source.url} alt="" draggable={false} decoding="async" className="size-full object-cover" />
      ) : <Icon aria-hidden="true" className="size-6 text-muted-foreground" />}
    </div>
  );
}
