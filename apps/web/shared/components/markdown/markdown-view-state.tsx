"use client";

import * as React from "react";

type PageSelection = { shape: string; page: number };
export type MarkdownViewStateStore = {
  getPage: (key: string, shape: string) => number | null;
  setPage: (key: string, shape: string, page: number) => void;
};

/** The stable message shell owns this small view-only store, never Markdown source. */
export function createMarkdownViewStateStore(): MarkdownViewStateStore {
  const pages = new Map<string, PageSelection>();
  return {
    getPage: (key, shape) => {
      const selection = pages.get(key);
      if (selection?.shape === shape) return selection.page;
      pages.delete(key);
      return null;
    },
    setPage: (key, shape, page) => {
      pages.delete(key);
      pages.set(key, { shape, page: Math.max(0, Math.floor(page)) });
      if (pages.size > 64) {
        const oldest = pages.keys().next().value;
        if (oldest !== undefined) pages.delete(oldest);
      }
    },
  };
}

export const MarkdownViewStateContext = React.createContext<MarkdownViewStateStore | null>(null);

export function MarkdownViewStateProvider({ store, children }: {
  store: MarkdownViewStateStore;
  children: React.ReactNode;
}) {
  return <MarkdownViewStateContext.Provider value={store}>{children}</MarkdownViewStateContext.Provider>;
}
