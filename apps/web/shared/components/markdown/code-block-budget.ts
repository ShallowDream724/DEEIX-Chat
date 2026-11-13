// Highlighting expands source into per-line and per-token elements. Bound both
// dense/minified code (16K source characters) and tall listings (300 lines) before
// mounting Streamdown's highlighter. These limits cover ordinary answer snippets
// while avoiding token trees for whole files. Larger blocks stay complete and
// selectable through the plain-text renderer.
export const CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS = 16_384;
export const CODE_BLOCK_HIGHLIGHT_MAX_LINES = 300;

export function exceedsCodeBlockHighlightBudget(code: string): boolean {
  if (code.length > CODE_BLOCK_HIGHLIGHT_MAX_CHARACTERS) {
    return true;
  }

  let lines = 1;
  for (let index = 0; index < code.length; index += 1) {
    // Markdown's final newline terminates its last line; it is not an extra
    // displayed line. Count LF and CR line endings, treating CRLF as one break.
    const character = code.charCodeAt(index);
    const lineBreak = character === 10 || (character === 13 && code.charCodeAt(index + 1) !== 10);
    if (lineBreak && index < code.length - 1) {
      lines += 1;
      if (lines > CODE_BLOCK_HIGHLIGHT_MAX_LINES) {
        return true;
      }
    }
  }

  return false;
}
