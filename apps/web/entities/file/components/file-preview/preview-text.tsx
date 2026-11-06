"use client";

import * as React from "react";

import { StreamdownRender } from "@/shared/components/markdown/streamdown-render";
import { canHighlightFileCode, preserveFileCodeTokens, resolveCodeLanguage, type FileCodeToken } from "@/entities/file/lib/file-code-language";
import type { HighlightResult } from "@streamdown/code";

type PreviewTextProps = {
  kind: "markdown" | "code" | "text";
  content: string;
  className?: string;
  fileName?: string;
};

function tokenStyle(token: FileCodeToken): React.CSSProperties & { "--file-code-light": string; "--file-code-dark": string } {
  return { "--file-code-light": token.lightColor ?? "inherit", "--file-code-dark": token.darkColor ?? token.lightColor ?? "inherit" };
}

function CodeBlock({ content, fileName }: { content: string; fileName?: string }) {
  const lines = React.useMemo(() => content.split("\n"), [content]);
  const language = resolveCodeLanguage(fileName);
  const [highlighted, setHighlighted] = React.useState<{ content: string; language: string; lines: FileCodeToken[][] } | null>(null);
  React.useEffect(() => {
    if (!language || !canHighlightFileCode(content)) return;
    let cancelled = false;
    const apply = (result: HighlightResult) => {
      if (cancelled) return;
      const tokens = preserveFileCodeTokens(content, result.tokens);
      if (tokens) setHighlighted({ content, language, lines: tokens });
    };
    void import("@streamdown/code").then(({ code }) => {
      if (cancelled || !code.supportsLanguage(language)) return;
      const cached = code.highlight({ code: content, language, themes: ["github-light", "github-dark"] }, apply);
      if (cached) apply(cached);
    }).catch(() => { /* Original text remains available if the highlighter cannot load. */ });
    return () => { cancelled = true; };
  }, [content, language]);
  const tokens = highlighted?.content === content && highlighted.language === language ? highlighted.lines : null;

  return (
    <div className="overflow-hidden rounded-md border border-border/40 bg-background/80">
      <div className="min-w-full px-4 py-4 font-mono text-[12.5px] leading-6">
        {lines.map((line, index) => (
          <div key={index} className="flex min-w-0 items-start">
            <span className="w-10 shrink-0 select-none pr-4 text-right text-[11px] text-muted-foreground/75">
              {index + 1}
            </span>
            <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-foreground">
              {tokens?.[index]?.length ? tokens[index].map((token, tokenIndex) => (
                <span key={tokenIndex} className="text-[color:var(--file-code-light)] dark:text-[color:var(--file-code-dark)]" style={tokenStyle(token)}>{token.content}</span>
              )) : line || " "}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function PreviewText({
  kind,
  content,
  className,
  fileName,
}: PreviewTextProps) {
  return (
    <div className={className ?? "min-h-[320px]"}>
      {kind === "markdown" ? (
        <div className="bg-background/80">
          <div className="px-5 py-5">
            <StreamdownRender content={content} className="text-sm" />
          </div>
        </div>
      ) : null}

      {kind === "code" || kind === "text" ? <CodeBlock content={content} fileName={fileName} /> : null}
    </div>
  );
}
