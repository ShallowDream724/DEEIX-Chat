const CODE_NOTICE = "这里是一段代码。";
const DIAGRAM_NOTICE = "这里有一张图示。";
const FORMULA_NOTICE = "这里有一条公式。";

function speechTableRow(line: string) {
  return `${line.trim().replace(/^\||\|$/g, "").split(/(?<!\\)\|/u).map((cell) => cell.trim()).filter(Boolean).join("，").replace(/[。.!?！？]+$/, "")}。`;
}

function speechMarkdownLines(text: string) {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  const result: string[] = [];
  let fence: { character: string; length: number } | undefined;
  let table = false;
  let referenceContinuation = false;
  let indentedCode = false;
  for (const sourceLine of lines) {
    const line = sourceLine.replace(/^ {0,3}(?:>\s*)+/, "");
    const marker = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fence) {
      if (marker && marker[1][0] === fence.character && marker[1].length >= fence.length && !marker[2].trim()) fence = undefined;
      continue;
    }
    if (marker) {
      const language = marker[2].trim().split(/\s+/, 1)[0].toLowerCase();
      result.push(["mermaid", "graphviz", "dot", "plantuml"].includes(language) ? DIAGRAM_NOTICE : CODE_NOTICE);
      fence = { character: marker[1][0], length: marker[1].length }; table = false; continue;
    }
    if (/^ {0,3}\[[^\]\n]+\]:/.test(line)) { referenceContinuation = true; continue; }
    if (referenceContinuation && /^[ \t]+\S/.test(line)) continue;
    referenceContinuation = false;
    const indented = /^(?: {4}|\t)/.test(line);
    if (indentedCode && (indented || !line.trim())) continue;
    indentedCode = false;
    if (indented && (!result.length || !result[result.length - 1].trim())) {
      result.push(CODE_NOTICE); indentedCode = true; table = false; continue;
    }
    if (line.includes("|")) {
      const cells = line.trim().replace(/^\||\|$/g, "").split("|");
      if (cells.length > 0 && cells.every((cell) => /^\s*:?-{3,}:?\s*$/.test(cell))) {
        if (result.length && result[result.length - 1].includes("|")) result[result.length - 1] = speechTableRow(result[result.length - 1]);
        table = true; continue;
      }
      if (table) { result.push(speechTableRow(line)); continue; }
    }
    table = false;
    result.push(line.replace(/^ {0,3}(?:#{1,6}\s+|>\s*|[-+*]\s+(?:\[[ xX]\]\s*)?|\d+[.)]\s+)/, ""));
  }
  return result.join("\n");
}

function speechMarkdownLinks(text: string) {
  const brackets: number[] = [];
  const parentheses: number[] = [];
  const closing = new Map<number, number>();
  for (let index = 0; index < text.length; index++) {
    if (text[index] === "\\") { index++; continue; }
    const character = text[index];
    if (character === "[") brackets.push(index);
    else if (character === "(") parentheses.push(index);
    else if (character === "]" && brackets.length) closing.set(brackets.pop() ?? 0, index);
    else if (character === ")" && parentheses.length) closing.set(parentheses.pop() ?? 0, index);
  }
  const skipped = new Map<number, number>();
  const output: string[] = [];
  for (let index = 0; index < text.length; index++) {
    const afterLink = skipped.get(index);
    if (afterLink !== undefined) { index = afterLink; continue; }
    const image = text[index] === "!" && text[index + 1] === "[";
    const opening = image ? index + 1 : index;
    const labelEnd = text[opening] === "[" ? closing.get(opening) : undefined;
    if (labelEnd !== undefined) {
      const targetEnd = ["(", "["].includes(text[labelEnd + 1]) ? closing.get(labelEnd + 1) : undefined;
      if (image) { index = targetEnd ?? labelEnd; output.push(" "); continue; }
      skipped.set(labelEnd, targetEnd ?? labelEnd); continue;
    }
    output.push(text[index]);
  }
  return output.join("");
}

function speechHTML(text: string) {
  const lower = text.toLowerCase();
  const output: string[] = [];
  for (let index = 0; index < text.length; index++) {
    if (text[index] !== "<" || !/[A-Za-z/!]/.test(text[index + 1] ?? "")) { output.push(text[index]); continue; }
    if (text.startsWith("<!--", index)) {
      const end = text.indexOf("-->", index + 4);
      output.push(" "); index = end < 0 ? text.length : end + 2; continue;
    }
    const end = text.indexOf(">", index + 1);
    if (end < 0) { output.push(text.slice(index)); break; }
    const tag = text.slice(index + 1, end);
    const name = /^([A-Za-z][\w:-]*)\b/.exec(tag)?.[1].toLowerCase();
    if (name && /^(?:pre|script|style|svg|iframe|object|canvas|artifact|tool_call|think|analysis|ui[_-][\w-]+|deeix[_-][\w-]+)$/.test(name) && !tag.endsWith("/")) {
      const closing = lower.indexOf(`</${name}`, end + 1);
      const blockEnd = closing < 0 ? -1 : text.indexOf(">", closing);
      const paragraph = blockEnd < 0 ? text.indexOf("\n\n", end + 1) : -1;
      output.push(name === "pre" ? CODE_NOTICE : " "); index = blockEnd < 0 ? (paragraph < 0 ? text.length : paragraph) : blockEnd; continue;
    }
    output.push(" "); index = end;
  }
  return output.join("");
}

function speechMath(text: string) {
  const output: string[] = [];
  for (let index = 0; index < text.length; index++) {
    const character = text[index];
    if (character === "\\" && ["(", "["].includes(text[index + 1])) {
      const end = text.indexOf(text[index + 1] === "(" ? "\\)" : "\\]", index + 2);
      const paragraph = end < 0 ? text.indexOf("\n\n", index + 2) : -1;
      output.push(FORMULA_NOTICE); index = end < 0 ? (paragraph < 0 ? text.length : paragraph) : end + 1; continue;
    }
    if (character === "\\") { output.push(character, text[index + 1] ?? ""); index++; continue; }
    if (character === "$" && text[index + 1] === "$") {
      const end = text.indexOf("$$", index + 2);
      const paragraph = end < 0 ? text.indexOf("\n\n", index + 2) : -1;
      output.push(FORMULA_NOTICE); index = end < 0 ? (paragraph < 0 ? text.length : paragraph) : end + 1; continue;
    }
    if (character === "$") {
      const end = text.indexOf("$", index + 1);
      // Dollar amounts have no closing delimiter. Reject the next amount's
      // leading dollar so "$10 and $20" remains ordinary spoken prose.
      if (end > index + 1 && !/\s/.test(text[index + 1]) && !/\s/.test(text[end - 1]) && !/\d/.test(text[end + 1] ?? "") && !text.slice(index + 1, end).includes("\n")) {
        output.push(FORMULA_NOTICE); index = end; continue;
      }
    }
    output.push(character);
  }
  return output.join("");
}

/** A deterministic, local speech projection of Markdown; it never rewrites prose. */
export function normalizeSpeechContent(text: string) {
  const markdown = speechMarkdownLinks(speechMarkdownLines(text));
  const visible = markdown
    .replace(/\b(?:https?:\/\/|www\.)[^\s<>"'`，。！？；：、]+/giu, (url, offset: number, source: string) => source[offset - 1] === "<" && source[offset + url.length] === ">" ? " " : url.match(/[.,!?;:)\]}]+$/)?.[0] ?? " ")
    .replace(/<\s*>/g, " ");
  return speechHTML(speechMath(visible))
    .replace(/\\([\\`*{}[\]()#+\-.!_$>|~])/g, "$1")
    .replace(/[*_`~]/g, "")
    .replace(/(?:这里是一段代码。|这里有一张图示。|这里有一条公式。)(?:\s*(?:这里是一段代码。|这里有一张图示。|这里有一条公式。))+/g, "这里有一段未朗读的内容。")
    .replace(/(这里(?:是一段代码|有一张图示|有一条公式|有一段未朗读的内容)。)[。.]+/g, "$1")
    .replace(/\s+/g, " ").trim();
}
