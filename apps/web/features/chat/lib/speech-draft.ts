/** One mutable range belongs to this recording. Edits elsewhere stay outside it. */
export class SpeechDraftSegment {
  private prefix: string;
  private suffix: string;
  private text = "";
  private rendered: string;
  private insertedSeparator = false;
  private detached = false;
  private appendParagraph: boolean;
  constructor(draft: string, insertionIndex = draft.length) {
    const index = Math.max(0, Math.min(draft.length, insertionIndex));
    this.prefix = draft.slice(0, index);
    this.suffix = draft.slice(index);
    this.appendParagraph = index === draft.length;
    this.rendered = draft;
  }
  reconcile(draft: string) {
    if (draft === this.rendered) return false;
    const old = this.rendered;
    let start = 0;
    while (start < old.length && start < draft.length && old[start] === draft[start]) start++;
    let end = 0;
    while (end < old.length - start && end < draft.length - start && old[old.length - end - 1] === draft[draft.length - end - 1]) end++;
    const rangeStart = this.prefix.length;
    const rangeEnd = rangeStart + this.text.length;
    if (start >= rangeEnd) this.suffix = draft.slice(rangeEnd);
    else if (old.length - end <= rangeStart) this.prefix = draft.slice(0, draft.length - end) + old.slice(old.length - end, rangeStart);
    else {
      // A user edit inside the live range becomes authoritative.
      this.prefix = draft;
      this.suffix = "";
      this.text = "";
      this.detached = true;
    }
    this.rendered = draft;
    return true;
  }
  update(text: string) {
    if (this.detached) return this.rendered;
    const trimmed = text.trim();
    if (this.appendParagraph && this.text === "" && trimmed && this.prefix && !this.prefix.endsWith("\n")) {
      this.prefix += "\n";
      this.insertedSeparator = true;
    }
    this.text = trimmed;
    this.rendered = this.prefix + this.text + this.suffix;
    return this.rendered;
  }
  cancel() {
    // Only remove the separator inserted for this range.
    const prefix = !this.detached && this.insertedSeparator && this.prefix.endsWith("\n") ? this.prefix.slice(0, -1) : this.prefix;
    return prefix + this.suffix;
  }
  get selectionEnd(): number | null { return this.detached || !this.text ? null : this.prefix.length + this.text.length; }
}
