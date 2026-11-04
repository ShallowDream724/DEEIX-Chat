"use client";

import { useLocale } from "next-intl";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { normalizeAppLocale } from "@/i18n/config";
import { resolveInputLimitPresentation } from "@/i18n/resolve-error-message";
import { InputLimitDialog } from "@/shared/components/input-limit-dialog";
import { useTheme } from "@/shared/components/theme-provider";
import type { InputLimitReason } from "@/shared/lib/input-limit";

const cases = {
  count: [{ dimension: "imageCount", actual: 17, limit: 10, scope: "mixed" }],
  size: [{ dimension: "pdfSingleBytes", actual: 62_000_000, limit: 50_000_000, fileName: "报告.pdf" }],
  unsupported: [{ dimension: "fileUnsupported", fileName: "会议录音.mp3", fileCategory: "audio", scope: "current" }],
  context: [{ dimension: "contextTokens", actual: 122_000, limit: 114_688, scope: "history" }],
  multiple: [
    { dimension: "messageFileCount", actual: 12, limit: 10, scope: "current" },
    { dimension: "fileUnsupported", fileName: "演示视频.mp4", fileCategory: "video", scope: "history" },
  ],
} satisfies Record<string, InputLimitReason[]>;

function isScenario(value: string | null): value is keyof typeof cases {
  return value !== null && Object.hasOwn(cases, value);
}

export function InputLimitReview() {
  const locale = normalizeAppLocale(useLocale());
  const { setTheme } = useTheme();
  const [scenario, setScenario] = React.useState<keyof typeof cases | null>(null);
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const initialScenario = params.get("scenario");
    const theme = params.get("theme");
    if (theme === "dark" || theme === "light") setTheme(theme);
    if (initialScenario !== "closed") {
      setScenario(isScenario(initialScenario) ? initialScenario : "count");
    }
  }, [setTheme]);

  return (
    <main className="mx-auto flex h-full max-w-2xl flex-col justify-center gap-4 px-6">
      <h1 className="text-base font-semibold">{locale === "zh-CN" ? "发送限制预览" : "Send limit preview"}</h1>
      <div className="flex flex-wrap gap-2">
        {Object.entries(cases).map(([key, reasons]) => (
          <Button key={key} variant="outline" size="sm" onClick={() => { if (isScenario(key)) setScenario(key); }}>
            {key === "multiple"
              ? (locale === "zh-CN" ? "多个原因" : "Multiple reasons")
              : resolveInputLimitPresentation(reasons[0], locale).title}
          </Button>
        ))}
      </div>
      <InputLimitDialog
        reasons={scenario ? cases[scenario] : []}
        onClose={() => setScenario(null)}
        onNewConversation={() => setScenario(null)}
      />
    </main>
  );
}
