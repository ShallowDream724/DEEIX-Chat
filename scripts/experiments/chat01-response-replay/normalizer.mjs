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
    this.fenceOpeningLine = false;
    this.inlineTicks = 0;
    this.literalLine = false;
    this.markdownEscape = false;
    this.component = null;
    this.processBoundary = false;
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
      if (this.fenceOpeningLine) {
        if (s[0] === "\n") this.fenceOpeningLine = false;
        this.emit("control", s[0], "代码围栏"); continue;
      }
      if (this.component) {
        const count = this.component.count + 1;
        this.component.count = count;
        this.emit("component", s[0], "上游组件数据，完整性单独判断");
        const block = this.blocks.at(-1);
        block.complete = s[0] === "";
        block.oversized = count > 65536;
        if (block.complete) this.component = null;
        continue;
      }
      if (this.literalLine) {
        if (s[0] === "\n") this.literalLine = false;
        this.emit(this.route(), s[0]); continue;
      }
      if (this.markdownEscape) {
        this.markdownEscape = false; this.emit(this.route(), s[0]); continue;
      }
      if (this.inlineTicks) {
        if (s[0] === "`") {
          const run = s.match(/^`+/)[0];
          if (run.length === s.length && !final) break;
          if (run.length === this.inlineTicks) this.inlineTicks = 0;
          this.emit(this.route(), run); continue;
        }
        this.emit(this.route(), s[0]); continue;
      }
      const pythonLike = this.tool === "python" || this.tool === "code-fragment";
      if (pythonLike && this.quote) {
        if (this.escaped) { this.escaped = false; this.emit(this.tool, s[0]); continue; }
        if (s[0] === "\\") { this.escaped = true; this.emit(this.tool, s[0]); continue; }
        if (!final && this.quote.startsWith(s) && s.length < this.quote.length) break;
        if (s.startsWith(this.quote)) { const quote = this.quote; this.quote = null; this.emit(this.tool, quote); continue; }
        if (s[0] === "\n" && this.quote.length === 1) this.quote = null;
        this.emit(this.tool, s[0]); continue;
      }
      if (pythonLike && this.comment) {
        if (s[0] === "\n") this.comment = false;
        this.emit(this.tool, s[0]); continue;
      }
      // Markdown code owns its contents. A fence closes only with the same
      // delimiter and at least the opening run length, on an otherwise empty line.
      if (this.lineStart && !this.heredoc) {
        const indent = s.match(/^[ ]{0,4}/)[0];
        if (indent.length === s.length && !final && indent.length < 4) break;
        if (!this.fence && !this.tool && (indent.length >= 4 || s[0] === "\t")) {
          this.literalLine = true; continue;
        }
        const match = s.match(/^[ ]{0,3}(`+|~+)/);
        if (match) {
          const run = match[1], prefix = match[0], rest = s.slice(prefix.length);
          if (!rest && !final) break;
          if (!this.fence && run.length >= 3) {
            this.fence = {char:run[0], length:run.length};
            this.fenceOpeningLine = Boolean(this.tool);
            this.emit(this.tool ? "control" : this.route(), prefix, this.tool ? "代码围栏" : "");
            continue;
          }
          if (this.fence && run[0] === this.fence.char && run.length >= this.fence.length) {
            const end = rest.match(/^[ \t\r]*(?:\n|$)/);
            if (end) {
              if (end[0].length === rest.length && !rest.endsWith("\n") && !final) break;
              this.fence = false;
              this.emit(this.tool ? "control" : this.route(), prefix + end[0], this.tool ? "代码围栏" : "");
              continue;
            }
          }
        }
      }
      const markers = ["<think>", "</think>"];
      if (this.lineStart || (process && !this.fence && !this.heredoc)) {
        markers.push("## Answer:", "## Thought:", "## Python:", "## Shell:", "## Browser:");
      }
      if (!this.fence && !this.heredoc || pythonLike && this.lineStart && (s.startsWith("</think>") || "</think>".startsWith(s))) {
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
      if (this.heredoc && this.lineStart) {
        if (s.startsWith(this.heredoc)) {
          const rest = s.slice(this.heredoc.length);
          if (!rest && !final) break;
          if (!final && ["\r\n", "## Thought:", "## Answer:"].some(marker => marker.startsWith(rest))) break;
          if (/^(?:\r?\n|## Thought:|## Answer:)/.test(rest) || final && !rest) {
            this.emit("shell", this.heredoc);
            this.heredoc = null;
            continue;
          }
        } else if (!final && this.heredoc.startsWith(s)) break;
      }
      if (process && !this.fence && !this.heredoc) {
        if (!pythonLike && (this.lineStart || this.processBoundary) && s.startsWith("**") && !s.startsWith("**Searched ") && !s.startsWith("**Used ")) {
          const end = s.indexOf("**", 2);
          if (end < 0 && !final && s.length < 512) break;
          if (end >= 0) {
            this.tool = null; this.quote = null;
            this.emit("reasoning", s.slice(0, end+2));
            this.processBoundary = true;
            continue;
          }
        }
        if (!final && ((!pythonLike && s === "*") || "bash -lc ".startsWith(s))) break;
        if (s.startsWith("bash -lc ")) {
          this.tool = "shell";
          this.processBoundary = false;
          this.emit("shell", "bash -lc ");
          continue;
        }
        if (this.tool === "shell" && s.startsWith("<<")) {
          const match = s.match(/^<<-?[ \t]*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?/);
          if (!final && s.length < 96 && (!match || match[0].length === s.length)) break;
          if (match) {
            this.heredoc = match[1];
            this.emit("shell", match[0]);
            continue;
          }
        }
        if (this.tool === "shell" && !final && s === "<") break;
        if (!this.tool && (this.lineStart || this.processBoundary) && s[0] !== "\n" && !s.startsWith("##")) {
          const end = s.indexOf("\n");
          if (end < 0 && !final && s.length < 512) break;
          const first = s.slice(0,end < 0 ? s.length : end);
          const strength = codeLineStrength(first);
          if (strength) {
            const after = end < 0 ? "" : s.slice(end+1);
            const nextEnd = after.indexOf("\n");
            if (nextEnd < 0 && !final && after.length < 512) break;
            const second = after.slice(0,nextEnd < 0 ? after.length : nextEnd);
            if (codeLineStrength(second) === 2) {
              this.tool = "code-fragment";
              this.processBoundary = false;
              continue;
            }
          }
          this.processBoundary = false;
        }
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
        this.component = {count:"genui".length};
        this.emit("component", "genui", "上游组件数据，完整性单独判断");
        this.blocks.at(-1).complete = false;
        continue;
      }
      if (!this.fence && !this.heredoc && !final && "genui".startsWith(s)) break;
      if (!this.fence && !this.tool && !this.heredoc) {
        if (s[0] === "\\") { this.markdownEscape = true; this.emit(this.route(), s[0]); continue; }
        if (s[0] === "`") {
          const run = s.match(/^`+/)[0];
          if (run.length === s.length && !final) break;
          this.inlineTicks = run.length;
          this.emit(this.route(), run); continue;
        }
      }
      if (pythonLike) {
        if (s[0] === '"' || s[0] === "'") {
          const triple = s[0].repeat(3);
          if (!final && triple.startsWith(s) && s.length < 3) break;
          this.quote = s.startsWith(triple) ? triple : s[0];
          this.emit(this.tool, this.quote); continue;
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

function codeLineStrength(line) {
  const s = line.trim();
  if (/^(?:from [\w.]+ import |import [\w.]+|(?:async )?def \w+\(|class \w+[(:]|assert [\w('"[]|(?:with|for|if|elif|while) .+:$|(?:try|else|finally):$|except(?: .+)?:$|print\(|raise \w+|return\b)/.test(s)) return 2;
  if (/^[\w]+(?:\.[\w]+|\[[^\]\n]+\])*\s*=(?!=)/.test(s)) return 2;
  if (/^(?:['"]?PY['"]?|.*\.py\s*<<['"]?\w+['"]?|[^\s]+\.(?:json|csv|txt)['"]\)\.(?:read|write)\w*\(.*)$/.test(s)) return 1;
  return 0;
}

export function resourcesFor(blocks) {
  const resources = new Map();
  for (const block of blocks) {
    if (block.kind !== "answer") continue;
    const pattern = /(!?)\[([^\]]*)\]\((?:https?:\/\/|sandbox:\/)[^\s)]+\)/g;
    for (const match of block.text.matchAll(pattern)) {
      const href = match[0].slice(match[0].indexOf("](")+2,-1);
      let url;
      try { url = new URL(href); } catch { continue; }
      const kind = url.protocol === "sandbox:" ? "file-unavailable" : match[1] ? "image" : url.hostname === "files.chat01.ai" ? "file" : "source";
      let name = url.pathname.split("/").at(-1);
      try { name = decodeURIComponent(name); } catch { /* Keep a malformed name readable; preserve the answer. */ }
      if (!resources.has(href)) resources.set(href, {kind, url: href, title: match[2], name, start: block.start + match.index});
    }
  }
  return [...resources.values()];
}
