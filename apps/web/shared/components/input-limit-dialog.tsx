"use client";

import { useLocale, useTranslations } from "next-intl";
import * as React from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { normalizeAppLocale } from "@/i18n/config";
import { resolveInputLimitPresentation } from "@/i18n/resolve-error-message";
import type { InputLimitReason } from "@/shared/lib/input-limit";

export function InputLimitDialog({
  reasons,
  onClose,
  onNewConversation,
  temporary = false,
}: {
  reasons: readonly InputLimitReason[];
  onClose: () => void;
  onNewConversation?: () => void;
  temporary?: boolean;
}) {
  const t = useTranslations("errors.inputLimit");
  const locale = normalizeAppLocale(useLocale());
  const returnFocusRef = React.useRef<HTMLElement | null>(null);
  const entries = reasons.map((reason) => resolveInputLimitPresentation(reason, locale));
  const first = entries[0];
  const canStartNewConversation = Boolean(onNewConversation && reasons.some((reason) =>
    reason.scope === "history" || reason.scope === "mixed" || reason.dimension === "contextTokens"
    || (reason.dimension.startsWith("temporary") && reason.scope !== "current"))
    && !reasons.some((reason) => reason.dimension === "currentTurnTokens"
      || (reason.dimension === "temporarySingleTextLength" && reason.scope !== "history")));

  return (
    <AlertDialog open={Boolean(first)} onOpenChange={(open) => { if (!open) onClose(); }}>
      <AlertDialogContent
        size="compact"
        className="min-w-0 grid-cols-[minmax(0,1fr)] motion-reduce:animate-none"
        style={{ width: "min(440px, calc(100dvw - 2rem))", maxWidth: "none" }}
        onOpenAutoFocus={() => {
          returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        }}
        onCloseAutoFocus={(event) => {
          if (returnFocusRef.current?.isConnected) {
            event.preventDefault();
            returnFocusRef.current.focus({ preventScroll: true });
          }
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{entries.length === 1 ? first?.title : t("title")}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            {entries.length === 1 ? (
              <p className="[overflow-wrap:anywhere]">{first?.description}</p>
            ) : (
              <ul className="space-y-2.5 [overflow-wrap:anywhere]">
                {entries.map((entry, index) => (
                  <li key={index}>
                    <p className="font-medium text-foreground">{entry.title}</p>
                    <p>{entry.description}</p>
                  </li>
                ))}
              </ul>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="flex-wrap">
          <AlertDialogCancel variant={canStartNewConversation ? "outline" : "default"}>{t("close")}</AlertDialogCancel>
          {canStartNewConversation ? (
            <AlertDialogAction onClick={onNewConversation}>
              {t(temporary ? "newTemporaryConversation" : "newConversation")}
            </AlertDialogAction>
          ) : null}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
