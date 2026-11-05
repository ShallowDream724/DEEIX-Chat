// Review prototype: text already decoded from Chat01's SSE envelope.
// It never executes tools, fetches resources, or infers tool success.
export class Chat01ReplayParser {
  constructor() {
    this.pending = "";
    this.offset = 0;
    this.blocks = [];
    this.mode = "answer";
    this.tool = null;
    this.fence = false;
    this.heredoc = null;
    this.quote = null;
    this.escaped = false;
    this.comment = false;
    this.lineStart = true;
    this.section = 0;
    this.maxPending = 0;
  }
  emit(kind, value, reason = "") {
    if (!value) return;
    const previous = this.blocks.at(-1);
    if (previous && previous.kind === kind && previous.reason === reason && previous.section === this.section) {
      previous.text += value;
      previous.end += value.length;
    } else {
      this.blocks.push({kind, text: value, reason, section: this.section, start: this.offset, end: this.offset + value.length});
    }
    this.offset += value.length;
    this.lineStart = value.endsWith("\n");
    this.pending = this.pending.slice(value.length);
  }
  route() { return this.tool || this.mode; }
  feed(fragment, final = false) {
    this.pending += fragment;
    while (this.pending) {
      const s = this.pending;
      const process = this.mode === "reasoning";
      if (this.tool === "python" && this.quote) {
        if (this.escaped) { this.escaped = false; this.emit("python", s[0]); continue; }
        if (s[0] === "\\") { this.escaped = true; this.emit("python", s[0]); continue; }
        if (!final && this.quote.startsWith(s) && s.length < this.quote.length) break;
        if (s.startsWith(this.quote)) { const quote = this.quote; this.quote = null; this.emit("python", quote); continue; }
        this.emit("python", s[0]); continue;
      }
      if (this.tool === "python" && this.comment) {
        if (s[0] === "\n") this.comment = false;
        this.emit("python", s[0]); continue;
      }
      const markers = ["<think>", "</think>"];
      if (this.lineStart || (process && !this.fence && !this.heredoc)) {
        markers.push("## Answer:", "## Thought:", "## Python:", "## Shell:", "## Browser:");
      }
      if (!this.fence && !this.heredoc || this.tool === "python" && this.lineStart && (s.startsWith("</think>") || "</think>".startsWith(s))) {
        const marker = markers.find(value => s.startsWith(value));
        if (marker) {
          // Plain Answer sections may contain tutorial headings; Python/Shell
          // become process records only inside an explicit process region.
          if (!process && ["## Python:", "## Shell:", "## Browser:"].includes(marker)) {
            this.emit(this.route(), s[0]);
            continue;
          }
          this.emit("control", marker, "上游分段标记");
          if (marker === "<think>" || marker === "## Thought:") { this.mode = "reasoning"; this.tool = null; }
          if (marker === "</think>" || marker === "## Answer:") { this.mode = "answer"; this.tool = null; this.fence = false; }
          if (marker === "## Answer:") this.section++;
          if (marker === "## Python:") this.tool = "python";
          if (marker === "## Shell:") this.tool = "shell";
          if (marker === "## Browser:") this.tool = "search";
          continue;
        }
        if (!final && markers.some(marker => marker.startsWith(s))) break;
      }
      if (this.lineStart && s.startsWith("```")) {
        const end = s.indexOf("\n");
        if (end < 0 && !final && s.length < 96) break;
        const value = s.slice(0, end < 0 ? s.length : end + 1);
        this.fence = !this.fence;
        this.emit(this.tool ? "control" : this.route(), value, this.tool ? "代码围栏" : "");
        continue;
      }
      if (this.lineStart && !final && "```".startsWith(s)) break;
      if (this.heredoc && this.lineStart) {
        if (s.startsWith(this.heredoc)) {
          const rest = s.slice(this.heredoc.length);
          if (!rest && !final) break;
          if (!final && ["\r\n", "## Thought:", "## Answer:"].some(marker => marker.startsWith(rest))) break;
          if (/^(?:\r?\n|## Thought:|## Answer:)/.test(rest) || final && !rest) {
            this.emit("shell", this.heredoc);
            this.heredoc = null;
            this.tool = null;
            continue;
          }
        } else if (!final && this.heredoc.startsWith(s)) break;
      }
      if (process && !this.fence && !this.heredoc) {
        const starts = ["**Searched ", "**Used ", "Worked for ", '{"uri":"skills://', "data && python - <<", "bash -lc python - <<"];
        if (!final && starts.some(prefix => prefix.startsWith(s))) break;
        if (s.startsWith("**Searched ") || s.startsWith("**Used ")) {
          const end = s.indexOf("**", 2);
          if (end < 0 && !final && s.length < 512) break;
          if (end >= 0) {
            const search = s.startsWith("**Searched ");
            this.emit(search ? "search" : "tool-note", s.slice(0, end + 2), search ? "仅收到搜索摘要，查询与结果未返回" : "上游工具活动摘要");
            continue;
          }
        }
        if (s.startsWith("Worked for ")) {
          const end = s.indexOf("\n");
          if (end < 0 && !final && s.length < 128) break;
          this.emit("meta", s.slice(0, end < 0 ? s.length : end + 1), "上游耗时文字");
          continue;
        }
        if (s.startsWith('{"uri":"skills://')) {
          const end = s.indexOf("}");
          if (end < 0 && !final && s.length < 4096) break;
          if (end >= 0) { this.emit("tool-note", s.slice(0, end + 1), "上游资源读取记录"); continue; }
        }
        if (s.startsWith("data && python - <<") || s.startsWith("bash -lc python - <<")) {
          const end = s.indexOf("\n");
          if (end < 0 && !final && s.length < 512) break;
          const value = s.slice(0, end < 0 ? s.length : end + 1);
          const match = value.match(/<<['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?/);
          if (match) { this.heredoc = match[1]; this.tool = "shell"; this.emit("shell", value); continue; }
        }
      }
      if (!this.fence && !this.heredoc && s.startsWith("genui")) {
        const end = s.indexOf("");
        if (end < 0 && !final && s.length < 65536) break;
        this.emit("component", s.slice(0, end < 0 ? s.length : end + 1), "上游组件数据，完整性单独判断");
        continue;
      }
      if (!this.fence && !this.heredoc && !final && "genui".startsWith(s)) break;
      if (this.tool === "python") {
        if (s[0] === '"' || s[0] === "'") {
          const triple = s[0].repeat(3);
          if (!final && triple.startsWith(s) && s.length < 3) break;
          this.quote = s.startsWith(triple) ? triple : s[0];
          this.emit("python", this.quote); continue;
        }
        if (s[0] === "#") this.comment = true;
      }
      this.emit(this.route(), s[0]);
    }
    this.maxPending = Math.max(this.maxPending, this.pending.length);
    return this;
  }
  finish() { return this.feed("", true); }
}

export function resourcesFor(blocks) {
  const resources = new Map();
  for (const block of blocks) {
    if (block.kind !== "answer") continue;
    const pattern = /(!?)\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)/g;
    for (const match of block.text.matchAll(pattern)) {
      const url = new URL(match[3]);
      const kind = match[1] ? "image" : url.hostname === "files.chat01.ai" ? "file" : "source";
      if (!resources.has(match[3])) resources.set(match[3], {kind, url: match[3], title: match[2], name: decodeURIComponent(url.pathname.split("/").at(-1)), start: block.start + match.index});
    }
  }
  return [...resources.values()];
}
