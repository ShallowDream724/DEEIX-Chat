"use client";

import dynamic from "next/dynamic";

export const LazyFilePreviewPanel = dynamic(
  () => import("@/entities/file/components/file-preview/preview-panel").then((mod) => mod.FilePreviewPanel),
  { ssr: false },
);
