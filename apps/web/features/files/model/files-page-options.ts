import { resolveFileFilterIcon, type FileFilterKey } from "@/entities/file";
import type { FileFilterOption, FileSortOption } from "@/features/files/types/files";

const FILE_FILTER_KEYS: FileFilterKey[] = ["all", "image", "audio", "video", "document", "spreadsheet", "presentation", "pdf", "code"];
export const FILE_FILTER_OPTIONS: FileFilterOption[] = FILE_FILTER_KEYS.map((value) => ({ value, icon: resolveFileFilterIcon(value) }));

export const FILE_SORT_OPTIONS: FileSortOption[] = [
  { value: "size" },
  { value: "name" },
  { value: "created" },
  { value: "last_used" },
];
