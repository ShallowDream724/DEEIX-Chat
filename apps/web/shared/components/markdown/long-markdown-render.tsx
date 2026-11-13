"use client";

import * as React from "react";
import { Block, type BlockProps, parseMarkdownIntoBlocks, Streamdown, type StreamdownProps } from "streamdown";
import { useTranslations } from "next-intl";

import { MarkdownTableSourceContext, MarkdownTableStreamingContext } from "./adaptive-markdown-table";
import { isHeadingMarkdownBlock, isLongMarkdown, MARKDOWN_TABLE_PAGE_SIZE, markdownTablePageSource, markdownTableViewShape, parseLargeMarkdownTable } from "./long-markdown-model";
import { captureMarkdownMountAnchor, settleMarkdownMountAnchor } from "./long-markdown-anchor";
import { MarkdownViewStateContext } from "./markdown-view-state";

const CAPTURE_EVENT = "deeix:materialize-markdown";
const LongMarkdownContext = React.createContext({ capture: false, activeTailIndex: -1, viewKey: "markdown" });
const INITIAL_BLOCKS = 8;

// One observer per document, rather than one observer per paragraph in a long answer.
let blockObserver: IntersectionObserver | undefined;
const waitingBlocks = new Map<Element, () => void>();
function observeBlock(element: Element, onVisible: () => void) {
  if (typeof IntersectionObserver === "undefined") {
    onVisible();
    return () => {};
  }
  blockObserver ??= new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      waitingBlocks.get(entry.target)?.();
      waitingBlocks.delete(entry.target);
      blockObserver?.unobserve(entry.target);
    }
  }, { rootMargin: "800px 0px" });
  waitingBlocks.set(element, onVisible);
  blockObserver.observe(element);
  return () => {
    waitingBlocks.delete(element);
    blockObserver?.unobserve(element);
    if (waitingBlocks.size === 0) {
      blockObserver?.disconnect();
      blockObserver = undefined;
    }
  };
}

function SourcePaginatedTable({ table, capture, ...props }: BlockProps & {
  table: NonNullable<ReturnType<typeof parseLargeMarkdownTable>>;
  capture: boolean;
}) {
  const t = useTranslations("common.table");
  const streaming = React.useContext(MarkdownTableStreamingContext);
  const viewState = React.useContext(MarkdownViewStateContext);
  const { viewKey } = React.useContext(LongMarkdownContext);
  const stateKey = `${viewKey}:${props.index}`;
  const shape = markdownTableViewShape(table);
  const hasStreamed = React.useRef(streaming);
  if (streaming) hasStreamed.current = true;
  // null follows incoming rows until the reader explicitly chooses another page.
  const [page, setPage] = React.useState<number | null>(() => viewState?.getPage(stateKey, shape) ?? null);
  const pageCount = Math.ceil(table.rows.length / MARKDOWN_TABLE_PAGE_SIZE);
  const safePage = Math.min(page ?? (hasStreamed.current ? pageCount - 1 : 0), pageCount - 1);
  const selectPage = (next: number) => {
    viewState?.setPage(stateKey, shape, next);
    setPage(next);
  };
  React.useEffect(() => {
    if (page !== null && page !== safePage) {
      viewState?.setPage(stateKey, shape, safePage);
      setPage(safePage);
    }
  }, [page, safePage, shape, stateKey, viewState]);
  const content = capture ? table.source : markdownTablePageSource(table, safePage);
  return (
    <MarkdownTableSourceContext.Provider value={table}>
      <Block {...props} content={content} />
      {!capture && (
        <nav aria-label={t("pagination", { total: table.rows.length, page: safePage + 1, pageCount })}
          className="mt-2 flex flex-wrap items-center justify-end gap-3 text-xs text-muted-foreground"
          data-markdown-table-pagination="">
          <span aria-live="polite">{t("pagination", { total: table.rows.length, page: safePage + 1, pageCount })}</span>
          <button type="button" disabled={safePage === 0} onClick={() => selectPage(safePage - 1)}
            className="rounded px-2 py-1 hover:bg-muted disabled:opacity-40">{t("previousPage")}</button>
          <button type="button" disabled={safePage + 1 === pageCount} onClick={() => selectPage(safePage + 1)}
            className="rounded px-2 py-1 hover:bg-muted disabled:opacity-40">{t("nextPage")}</button>
        </nav>
      )}
    </MarkdownTableSourceContext.Provider>
  );
}

const LongMarkdownBlock = React.memo(function LongMarkdownBlock(props: BlockProps) {
  const { capture, activeTailIndex } = React.useContext(LongMarkdownContext);
  // isIncomplete describes an unfinished fenced block, not the final prose block.
  const activeTail = props.index === activeTailIndex;
  const [visited, setVisited] = React.useState(props.index < INITIAL_BLOCKS);
  const ref = React.useRef<HTMLDivElement>(null);
  const pendingAnchor = React.useRef<ReturnType<typeof captureMarkdownMountAnchor>>(null);
  const table = React.useMemo(() => parseLargeMarkdownTable(props.content), [props.content]);
  const heading = React.useMemo(() => isHeadingMarkdownBlock(props.content), [props.content]);
  React.useEffect(() => {
    if (props.isIncomplete || activeTail) {
      setVisited(true);
      return;
    }
    if (visited || capture || !ref.current) return;
    return observeBlock(ref.current, () => {
      if (ref.current) pendingAnchor.current = captureMarkdownMountAnchor(ref.current);
      setVisited(true);
    });
  }, [activeTail, capture, props.isIncomplete, visited]);
  React.useLayoutEffect(() => {
    const anchor = pendingAnchor.current;
    pendingAnchor.current = null;
    if (!anchor) return;
    if (ref.current) settleMarkdownMountAnchor(anchor, ref.current, capture);
    else anchor.dispose();
  });
  React.useEffect(() => () => pendingAnchor.current?.dispose(), []);

  if (!visited && !capture && !props.isIncomplete && !activeTail && !heading) {
    // Reserve an approximate wrapped height without parsing an offscreen block.
    const lines = Math.max(props.content.split("\n").length, Math.ceil(props.content.length / 80));
    return <div ref={ref} aria-hidden="true" data-markdown-block-placeholder="" className="flow-root min-w-0"
      style={{ minHeight: Math.min(1600, Math.max(32, lines * 28)) }} />;
  }
  const rendered = table
    ? <SourcePaginatedTable key={markdownTableViewShape(table)} {...props} table={table} capture={capture} />
    : <Block {...props} />;
  return props.index >= INITIAL_BLOCKS && !heading
    ? <div ref={ref} className="flow-root min-w-0 space-y-4" data-markdown-mounted-block="">{rendered}</div>
    : rendered;
});

/** Keep reference/footnote grouping in Streamdown's parser; source-position mode keeps global offsets. */
export function LongMarkdownStreamdown({ sourcePositions = false, viewKey = "markdown", ...props }: StreamdownProps & {
  sourcePositions?: boolean;
  viewKey?: string;
}) {
  const source = typeof props.children === "string" ? props.children : "";
  const long = !sourcePositions && isLongMarkdown(source);
  const blocks = React.useMemo(() => long ? (props.parseMarkdownIntoBlocksFn ?? parseMarkdownIntoBlocks)(source) : [],
    [long, props.parseMarkdownIntoBlocksFn, source]);
  const parseBlocks = React.useCallback((markdown: string) => markdown === source
    ? blocks
    : (props.parseMarkdownIntoBlocksFn ?? parseMarkdownIntoBlocks)(markdown),
  [blocks, props.parseMarkdownIntoBlocksFn, source]);
  const [capture, setCapture] = React.useState(false);
  const activeTailIndex = props.mode === "streaming" && props.isAnimating !== false ? blocks.length - 1 : -1;
  const context = React.useMemo(() => ({ capture, activeTailIndex, viewKey }), [activeTailIndex, capture, viewKey]);
  const ref = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    const element = ref.current;
    if (!element || !long) return;
    const materialize = (event: Event) => {
      if (event instanceof CustomEvent && typeof event.detail === "boolean") setCapture(event.detail);
    };
    element.addEventListener(CAPTURE_EVENT, materialize);
    return () => element.removeEventListener(CAPTURE_EVENT, materialize);
  }, [long]);
  if (!long) return <Streamdown {...props} />;
  return (
    <div ref={ref} data-long-markdown="">
      <LongMarkdownContext.Provider value={context}>
        <Streamdown {...props} mode="streaming" parseMarkdownIntoBlocksFn={parseBlocks} BlockComponent={LongMarkdownBlock} />
      </LongMarkdownContext.Provider>
    </div>
  );
}

/** Screenshot callers materialize after message windows, then restore in their finally block. */
export async function materializeMarkdownForCapture(root: HTMLElement, signal?: AbortSignal): Promise<() => void> {
  signal?.throwIfAborted();
  const elements = Array.from(root.querySelectorAll<HTMLElement>("[data-long-markdown]"));
  if (root.matches("[data-long-markdown]")) elements.unshift(root);
  const restore = () => {
    for (const element of elements) element.dispatchEvent(new CustomEvent(CAPTURE_EVENT, { detail: false }));
  };
  for (const element of elements) element.dispatchEvent(new CustomEvent(CAPTURE_EVENT, { detail: true }));
  try {
    await new Promise<void>((resolve, reject) => {
      let firstFrame = 0;
      let secondFrame = 0;
      const abort = () => {
        cancelAnimationFrame(firstFrame);
        cancelAnimationFrame(secondFrame);
        signal?.removeEventListener("abort", abort);
        reject(signal?.reason ?? new DOMException("Aborted", "AbortError"));
      };
      signal?.addEventListener("abort", abort, { once: true });
      firstFrame = requestAnimationFrame(() => {
        secondFrame = requestAnimationFrame(() => {
          signal?.removeEventListener("abort", abort);
          resolve();
        });
      });
    });
    signal?.throwIfAborted();
    return restore;
  } catch (error) {
    restore();
    throw error;
  }
}
