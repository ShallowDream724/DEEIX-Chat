"use client";

import { PanelLeft, Plus } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { useSidebarActions } from "@/components/ui/sidebar";
import { MobileHeaderActionSlot } from "@/features/shell/context/mobile-header-action-context";
import { useMobilePreviewHeaderHost } from "@/features/shell/context/mobile-header-action-context";
import { AppLogo } from "@/shared/components/app-logo";
import { cn } from "@/lib/utils";

export function MobileHeader({
  onCreateConversation,
}: {
  onCreateConversation: () => void;
}) {
  const t = useTranslations("common.navigation");
  const { toggleSidebar } = useSidebarActions();
  const preview = useMobilePreviewHeaderHost();

  return (
    <header className={cn("h-12 shrink-0 items-center md:hidden", preview.active ? "pointer-events-none absolute inset-x-0 top-0 z-40 flex gap-1 px-2" : "grid grid-cols-[1fr_auto_1fr] px-3")}>
      <div className={cn("flex shrink-0 justify-start", preview.active && "pointer-events-auto gap-1")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn("transition-colors", preview.active ? "size-8 rounded-lg border border-border/60 bg-background/90 backdrop-blur-sm" : "size-6")}
          aria-label={t("openSidebar")}
          onClick={toggleSidebar}
        >
          <PanelLeft aria-hidden className="size-[18px]" strokeWidth={1.4} />
        </Button>
        {preview.active ? <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 rounded-lg border border-border/60 bg-background/90 backdrop-blur-sm"
          aria-label={t("newChat")}
          onClick={onCreateConversation}
        ><Plus aria-hidden className="size-[18px]" strokeWidth={1.6} /></Button> : null}
      </div>

      <div className={cn("min-w-0 justify-center", preview.active ? "hidden" : "flex")}>
        <AppLogo
          width={64}
          height={48}
          priority
          className="h-5 w-auto object-contain"
        />
      </div>

      <div className={cn("items-center justify-end gap-1", preview.active ? "hidden" : "flex")}>
        <MobileHeaderActionSlot />
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-6"
          aria-label={t("newChat")}
          onClick={onCreateConversation}
        >
          <Plus aria-hidden className="size-4" strokeWidth={1.6} />
        </Button>
      </div>
      <div ref={preview.ref} className={cn("@container min-w-0 flex-1", !preview.active && "hidden")} />
    </header>
  );
}
