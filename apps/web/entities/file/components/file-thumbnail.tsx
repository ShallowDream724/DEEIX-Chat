"use client";

import { resolveFileIcon } from "@/entities/file/lib/file-display";
import { useFileThumbnail, type ThumbnailFile } from "@/entities/file/hooks/use-file-thumbnail";
import type { FileContentLoader } from "@/entities/file/hooks/use-file-preview-dialog";

export function FileThumbnail({ file, loadContent }: { file: ThumbnailFile; loadContent?: FileContentLoader }) {
  const { hostRef, source } = useFileThumbnail(file, loadContent);
  const Icon = resolveFileIcon(file);
  return (
    <div ref={hostRef} className="flex size-full items-center justify-center bg-muted/40">
      {source ? (
        <img src={source} alt="" draggable={false} decoding="async" className="size-full object-cover" />
      ) : <Icon aria-hidden="true" className="size-6 text-muted-foreground" />}
    </div>
  );
}
