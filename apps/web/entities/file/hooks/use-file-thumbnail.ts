"use client";

import * as React from "react";
import { fetchFileContent } from "@/shared/api/file";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";
import { acquireThumbnail, thumbnailResourceKey, thumbnailScopeForUser } from "@/entities/file/lib/thumbnail-resource";
import type { FileContentLoader } from "@/entities/file/hooks/use-file-preview-dialog";

export type ThumbnailFile = {
  fileID: string;
  fileName: string;
  mimeType: string;
  previewURL?: string;
  localFile?: File;
  sizeBytes?: number;
  assetID?: string;
  contentVersion?: string | number;
};

export function useFileThumbnail(file: ThumbnailFile, loadContent?: FileContentLoader) {
  const session = useOptionalAuthSession();
  const scope = session?.user && session.userStatus === "ready" ? thumbnailScopeForUser(session.user.id) : null;
  const latest = React.useRef({ file, loadContent });
  latest.current = { file, loadContent };
  const hostRef = React.useRef<HTMLDivElement>(null);
  const [visible, setVisible] = React.useState(false);
  const [source, setSource] = React.useState<{ key: string; url: string } | null>(null);
  const key = scope ? thumbnailResourceKey(file, scope) : null;

  React.useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }
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
    if (!visible || !scope || !key) return;
    const { file: requestedFile, loadContent: loader } = latest.current;
    const lease = acquireThumbnail(requestedFile, scope, async (signal) => {
      if (requestedFile.localFile) return requestedFile.localFile;
      if (requestedFile.previewURL) {
        try {
          const response = await fetch(requestedFile.previewURL, { signal });
          if (response.ok) return await response.blob();
        } catch {
          signal.throwIfAborted();
          // Local object URLs can expire while the attachment identity remains valid.
        }
      }
      if (loader) return (await loader({ ...requestedFile, sizeBytes: requestedFile.sizeBytes ?? 0 }, signal)).blob;
      const token = await resolveAccessToken();
      signal.throwIfAborted();
      if (!token) throw new Error("Authentication required");
      return (await fetchFileContent(token, requestedFile.fileID, signal)).blob;
    });
    let active = true;
    void lease.promise.then((url) => {
      if (active && url) setSource({ key, url });
    }).catch(() => {
      // A preview failure does not change upload or model-input eligibility.
    });
    return () => { active = false; lease.release(); };
  }, [file.fileID, file.localFile, file.previewURL, key, scope, visible]);

  return { hostRef, source: source?.key === key ? source.url : null };
}
