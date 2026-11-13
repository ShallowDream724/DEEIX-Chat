type MarkdownMountAnchor = {
  height: number;
  viewport: HTMLElement;
  row: HTMLElement;
  scrollTop: number;
  corrections: number;
  cancelled: () => boolean;
  dispose: () => void;
};

// Allow several blocks to settle in the same commit without mistaking our own
// earlier corrections for a reader's scroll between observation and commit.
const viewportCorrections = new WeakMap<HTMLElement, number>();
let adjustmentID = 0;

export function captureMarkdownMountAnchor(block: HTMLElement): MarkdownMountAnchor | null {
  const viewport = block.closest<HTMLElement>('[data-slot="message-scroller-viewport"]');
  const row = block.closest<HTMLElement>("[data-chat-history-mounted]");
  if (!viewport || !row) return null;
  const bounds = block.getBoundingClientRect();
  const viewportTop = viewport.getBoundingClientRect().top;
  const message = row.getBoundingClientRect();
  // History owns rows wholly above the viewport. We own only an above-viewport
  // block inside the message the reader is currently crossing.
  if (bounds.bottom > viewportTop || message.top >= viewportTop || message.bottom <= viewportTop) return null;
  let cancelled = false;
  const cancel = () => { cancelled = true; };
  const events = ["wheel", "touchstart", "pointerdown", "keydown"];
  for (const event of events) viewport.addEventListener(event, cancel, { passive: true });
  return {
    height: bounds.height,
    viewport,
    row,
    scrollTop: viewport.scrollTop,
    corrections: viewportCorrections.get(viewport) ?? 0,
    cancelled: () => cancelled,
    dispose: () => {
      for (const event of events) viewport.removeEventListener(event, cancel);
    },
  };
}

export function settleMarkdownMountAnchor(anchor: MarkdownMountAnchor, block: HTMLElement, capturing: boolean): void {
  anchor.dispose();
  const { viewport, row } = anchor;
  const corrections = viewportCorrections.get(viewport) ?? 0;
  const expectedScroll = anchor.scrollTop + corrections - anchor.corrections;
  if (capturing || anchor.cancelled() || block.isConnected === false || row.isConnected === false
    || Math.abs(viewport.scrollTop - expectedScroll) > 0.5) return;
  const delta = block.getBoundingClientRect().height - anchor.height;
  if (Math.abs(delta) <= 0.5) return;
  // The history ResizeObserver consumes this marker while refreshing its row
  // height. It must not apply the same already-accounted height change twice.
  const marker = String(++adjustmentID);
  row.dataset.markdownAnchorAdjustment = marker;
  const previousScroll = viewport.scrollTop;
  viewport.scrollTop += delta;
  viewportCorrections.set(viewport, corrections + viewport.scrollTop - previousScroll);
  // ResizeObserver runs after rAF during a rendering update. Keep the marker
  // through that delivery; a second frame removes it if no row observer ran.
  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (row.dataset.markdownAnchorAdjustment === marker) delete row.dataset.markdownAnchorAdjustment;
  }));
}
