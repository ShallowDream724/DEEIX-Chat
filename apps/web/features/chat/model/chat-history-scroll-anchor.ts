// The scroller primitive detects prepends from the first direct child. Chat
// content also contains branding nodes, so history anchoring must identify the
// stable message shells explicitly instead of treating that first child as a row.
export function createChatHistoryScrollAnchor(viewport: HTMLElement, content: HTMLElement) {
  let atBottom = true;
  let firstMessage: HTMLElement | null = null;
  let anchor: { element: HTMLElement; top: number } | null = null;
  const messages = () => Array.from(content.children).filter((child): child is HTMLElement => (
    child instanceof HTMLElement && Boolean(child.dataset.chatMessageId)
  ));
  let rows = messages();
  const bottom = () => { viewport.scrollTop = Math.max(0, viewport.scrollHeight - viewport.clientHeight); };
  const remember = () => {
    const bounds = viewport.getBoundingClientRect();
    let start = 0;
    let end = rows.length;
    while (start < end) {
      const middle = Math.floor((start + end) / 2);
      if (rows[middle].getBoundingClientRect().bottom > bounds.top) end = middle;
      else start = middle + 1;
    }
    const candidate = rows[start];
    const visible = candidate && candidate.getBoundingClientRect().top < bounds.bottom ? candidate : rows[0];
    firstMessage = rows[0] ?? null;
    anchor = visible ? { element: visible, top: visible.getBoundingClientRect().top - bounds.top } : null;
    atBottom = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 48;
  };
  const restoreAfterChildrenChange = () => {
    rows = messages();
    const prepended = firstMessage !== null && rows.indexOf(firstMessage) > 0;
    if (atBottom) {
      bottom();
    } else if (prepended && anchor && rows.includes(anchor.element)) {
      const top = anchor.element.getBoundingClientRect().top - viewport.getBoundingClientRect().top;
      viewport.scrollTop += top - anchor.top;
    }
    remember();
    return prepended;
  };
  return { remember, restoreAfterChildrenChange, isAtBottom: () => atBottom, bottom };
}
