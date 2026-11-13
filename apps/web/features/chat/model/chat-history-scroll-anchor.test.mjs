import assert from "node:assert/strict";
import test from "node:test";
import { createChatHistoryScrollAnchor } from "./chat-history-scroll-anchor.ts";

function fixture() {
  class Element {
    constructor(id, height) { this.dataset = id ? { chatMessageId: id } : {}; this.height = height; }
    getBoundingClientRect() {
      const top = content.children.slice(0, content.children.indexOf(this)).reduce((sum, row) => sum + row.height, 0) - viewport.scrollTop;
      return { top, bottom: top + this.height, height: this.height };
    }
  }
  const viewport = { scrollTop: 0, clientHeight: 600, getBoundingClientRect: () => ({ top: 0, bottom: 600 }),
    get scrollHeight() { return content.children.reduce((sum, row) => sum + row.height, 0); } };
  const rows = Array.from({ length: 30 }, (_, index) => new Element(`history-${150 + index}`, 800));
  // These stable first/last children are the real ChatArea structure that hid
  // prepends from the primitive's first-direct-child detection.
  const content = { children: [new Element(null, 0), ...rows, new Element(null, 32)] };
  return { Element, viewport, content, rows, prepend: () => content.children.splice(1, 0, ...Array.from({ length: 150 }, (_, index) => new Element(`history-${index}`, 900))) };
}

test("initial tail materialization retains an exact bottom despite later height corrections", () => {
  const previousHTMLElement = globalThis.HTMLElement;
  const { Element, viewport, content, rows } = fixture();
  globalThis.HTMLElement = Element;
  try {
    const anchor = createChatHistoryScrollAnchor(viewport, content);
    anchor.bottom();
    anchor.remember();
    rows.at(-1).height += 180;
    assert.equal(viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight, 180);
    assert.equal(anchor.isAtBottom(), true);
    anchor.bottom();
    anchor.remember();
    assert.equal(viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight, 0);
  } finally { globalThis.HTMLElement = previousHTMLElement; }
});

test("prepending 150 rows preserves the visible old message beneath a stable branding sibling", () => {
  const previousHTMLElement = globalThis.HTMLElement;
  const { Element, viewport, content, rows, prepend } = fixture();
  globalThis.HTMLElement = Element;
  try {
    const anchor = createChatHistoryScrollAnchor(viewport, content);
    viewport.scrollTop = viewport.scrollHeight - viewport.clientHeight - 300;
    anchor.remember();
    assert.equal(anchor.isAtBottom(), false);
    const readingMessage = rows.at(-2);
    const previousTop = readingMessage.getBoundingClientRect().top;
    const previousScroll = viewport.scrollTop;
    prepend();
    assert.notEqual(readingMessage.getBoundingClientRect().top, previousTop);
    assert.equal(anchor.restoreAfterChildrenChange(), true);
    assert.equal(readingMessage.getBoundingClientRect().top, previousTop);
    assert.equal(viewport.scrollTop, previousScroll + 150 * 900);
  } finally { globalThis.HTMLElement = previousHTMLElement; }
});

test("prepend from the bottom remains at the bottom; append while reading leaves scrollTop alone", () => {
  const previousHTMLElement = globalThis.HTMLElement;
  const { Element, viewport, content, prepend } = fixture();
  globalThis.HTMLElement = Element;
  try {
    const anchor = createChatHistoryScrollAnchor(viewport, content);
    anchor.bottom();
    anchor.remember();
    prepend();
    anchor.restoreAfterChildrenChange();
    assert.equal(viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight, 0);
    viewport.scrollTop -= 1000;
    anchor.remember();
    const previousScroll = viewport.scrollTop;
    content.children.splice(-1, 0, new Element("history-180", 700));
    assert.equal(anchor.restoreAfterChildrenChange(), false);
    assert.equal(viewport.scrollTop, previousScroll);
  } finally { globalThis.HTMLElement = previousHTMLElement; }
});
