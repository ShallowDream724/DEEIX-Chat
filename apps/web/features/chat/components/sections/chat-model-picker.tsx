"use client";

import * as React from "react";
import { AudioLines, Check, ChevronDown, ChevronLeft, ChevronRight, CircleDollarSign, Files, FileText, Image as ImageIcon, KeyRound, Paperclip, TicketSlash, Type, Video } from "lucide-react";
import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { InputGroupButton } from "@/components/ui/input-group";
import type { ChatModelOption, ModelModality } from "@/features/chat/types/chat-runtime";
import { useChatPopoverAlignOffset } from "@/features/chat/hooks/use-chat-popover-align-offset";
import {
  resolveDesktopMenuListMaxHeight,
  resolveDesktopModelMenuListMaxHeight,
  createPopoverLayoutRectReader,
} from "./chat-model-picker-layout";
import { MORPH_DURATION_S, MorphingCard, useSwitchDirection } from "./chat-model-picker-motion";
import { useIsMobile } from "@/shared/hooks/use-mobile";
import { SlideSwitch } from "@/shared/components/slide-switch";
import {
  ModelIcon,
  resolveModelIconURL,
  resolveModelOptionIconUrl,
  resolveModelOptionLabel,
  resolveModelPresentationGroup,
} from "@/entities/model";
import {
  type BillingDisplayCurrency,
  type BillingDisplayLabels,
  type BillingDisplayOptions,
  cacheWritePricingLabel,
  cacheWritePricingNote,
  formatBillingDisplayUnitPriceFromUSD,
  formatRateMultiplier,
  resolveCacheWritePricingUSD,
  resolveCurrentSchedulePeriod,
  parseImagePricing,
  IMAGE_PRICING_DISPLAY_FIELDS,
  type ImageBillingLabels,
} from "@/entities/billing";
import { cn } from "@/lib/utils";

type ChatModelPickerProps = {
  modelOptions: ChatModelOption[];
  billingDisplayCurrency: BillingDisplayCurrency;
  billingDisplayUsdToCnyRate: number | null;
  /** False in self-hosted mode, where no model shows a price. */
  billingEnabled: boolean;
  selectedPlatformModelName: string;
  loading: boolean;
  disabled: boolean;
  onModelCatalogRefresh?: () => void | Promise<void>;
  onModelChange: (platformModelName: string) => void;
  /** Side the phone menu opens on: below the composer on the landing page, above it in a conversation. */
  placementPreference: "top" | "bottom";
};

const MODEL_MENU_COLLISION_PADDING = 24;
const DESKTOP_MODEL_MENU_WIDTH = 224;
const DESKTOP_MODEL_SUBMENU_GAP = 8;
const DESKTOP_MODEL_MENU_SIDE_OFFSET = 8;
const DESKTOP_MODEL_MENU_MIN_SCROLL_HEIGHT = 96;
/** Group panel non-list chrome: p-1.5 × 2 + header h-7. */
const DESKTOP_GROUP_MENU_VERTICAL_CHROME = 40;
/** Model submenu non-list chrome: p-1.5 × 2. */
const DESKTOP_SUBMENU_VERTICAL_CHROME = 12;

/** Models on the user's own key are grouped by provider, after the platform groups. */
function resolveModelGroupPresentation(item: ChatModelOption) {
  if (item.personalProviderName !== null) {
    return { key: `personal:${item.personalProviderName}`, label: item.personalProviderName, icon: item.personalProviderIcon };
  }
  return resolveModelPresentationGroup(item);
}

function resolveModelGroups(modelOptions: ChatModelOption[]) {
  const groupMap = new Map<string, { label: string; icon: string; personal: boolean; items: ChatModelOption[] }>();
  for (const item of modelOptions) {
    const presentation = resolveModelGroupPresentation(item);
    const group = groupMap.get(presentation.key);
    if (group) {
      group.items.push(item);
      continue;
    }
    groupMap.set(presentation.key, {
      label: presentation.label,
      icon: presentation.icon,
      personal: item.personalProviderName !== null,
      items: [item],
    });
  }

  return Array.from(groupMap.entries()).map(([key, group]) => ({ key, ...group }));
}

/** Fallback mark for a group served by the user's own key when it has no icon. */
function PersonalGroupIcon() {
  return (
    <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground" aria-hidden="true">
      <KeyRound className="size-3.5" strokeWidth={1.7} />
    </span>
  );
}

/** Tells a user-key group apart from a platform group of the same vendor. */
function PersonalGroupBadge({ label }: { label: string }) {
  return (
    <Badge variant="secondary" className="h-4 shrink-0 px-1 font-normal">
      {label}
    </Badge>
  );
}

function ChatModelIdentity({ model }: { model: ChatModelOption }) {
  const platformModelName = resolveModelOptionLabel(model.platformModelName);
  const iconURL = React.useMemo(() => resolveModelOptionIconUrl(model), [model]);

  return (
    <div className="flex min-w-0 items-center gap-2">
      <ModelIcon iconUrl={iconURL} label={platformModelName} />
      <p className="min-w-0 flex-1 truncate text-[12.5px] font-medium leading-4 text-foreground">{platformModelName}</p>
    </div>
  );
}

function ChatModelTriggerSkeleton() {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <Skeleton className="size-4 shrink-0 rounded-full bg-muted/55" />
      <Skeleton className="h-3.5 w-20 rounded-full bg-muted/50" />
    </div>
  );
}

function ModelMenuScrollContainer({
  children,
  maxHeight,
}: {
  children: React.ReactNode;
  maxHeight?: number;
}) {
  const viewportRef = React.useRef<HTMLDivElement | null>(null);
  const [hasMoreAbove, setHasMoreAbove] = React.useState(false);
  const [hasMoreBelow, setHasMoreBelow] = React.useState(false);
  const resolvedMaxHeight = Number.isFinite(maxHeight) ? Math.max(0, maxHeight ?? 0) : undefined;

  const updateScrollHints = React.useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) {
      setHasMoreAbove(false);
      setHasMoreBelow(false);
      return;
    }
    const remaining = viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop;
    setHasMoreAbove(viewport.scrollTop > 1);
    setHasMoreBelow(remaining > 1);
  }, []);

  React.useLayoutEffect(() => {
    updateScrollHints();
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === "undefined") {
      return;
    }

    const observer = new ResizeObserver(updateScrollHints);
    observer.observe(viewport);
    if (viewport.firstElementChild) {
      observer.observe(viewport.firstElementChild);
    }
    return () => observer.disconnect();
  }, [children, resolvedMaxHeight, updateScrollHints]);

  return (
    <div className="relative">
      <div
        ref={viewportRef}
        data-model-menu-viewport=""
        style={resolvedMaxHeight === undefined ? undefined : { maxHeight: resolvedMaxHeight }}
        className={cn(
          "overflow-y-auto overscroll-contain pr-0 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
          resolvedMaxHeight === undefined
            ? "max-h-[min(20rem,var(--model-menu-scroll-max-height,var(--radix-popover-content-available-height)))]"
            : null,
        )}
        onScroll={updateScrollHints}
      >
        {children}
      </div>
      {hasMoreAbove ? (
        <div className="pointer-events-none absolute inset-x-0 top-0 flex h-4 items-start justify-center rounded-t-lg bg-gradient-to-b from-popover via-popover/80 to-transparent pt-px">
          <ChevronDown className="size-3 rotate-180 text-muted-foreground/75" strokeWidth={1.8} />
        </div>
      ) : null}
      {hasMoreBelow ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-0 flex h-4 items-end justify-center rounded-b-lg bg-gradient-to-t from-popover via-popover/80 to-transparent pb-px">
          <ChevronDown className="size-3 text-muted-foreground/75" strokeWidth={1.8} />
        </div>
      ) : null}
    </div>
  );
}

function ModelPricingTooltipContent(props: ModelPricingTooltipContentProps) {
  const { pricing, labels } = props;
  const current = React.useMemo(
    () => resolveCurrentSchedulePeriod(pricing.schedulePeriods ?? [], pricing.scheduleUTCOffsetMinutes ?? 0),
    [pricing.schedulePeriods, pricing.scheduleUTCOffsetMinutes],
  );
  const periods = pricing.schedulePeriods ?? [];
  return (
    <>
      <ModelPricingBase {...props} />
      {!pricing.isFree && periods.length > 0 ? (
        <div className="mt-2 flex flex-col gap-1 border-t border-background/20 pt-2">
          <span className="font-sans text-xs font-medium leading-4 text-background">{labels.schedule.title}</span>
          {periods.map((period) => {
            const active = current === period;
            return (
              <div key={`${period.name}-${period.start}-${period.end}`} className={cn("grid grid-cols-[minmax(5.5rem,max-content)_1fr_auto] items-baseline gap-x-4 font-sans text-[11px] leading-4", active ? "text-background" : "text-background/70")}>
                <span className="whitespace-nowrap">
                  {period.name}
                  {active ? <span className="ml-1 rounded-sm bg-background/20 px-1 text-[10px]">{labels.schedule.now}</span> : null}
                </span>
                <span className="whitespace-nowrap text-right tabular-nums">
                  {formatScheduleWeekdays(period.weekdays, labels.schedule)}{" "}
                  {labels.schedule.timeRange(period.start, period.end)}
                </span>
                <span className="w-12 whitespace-nowrap text-right tabular-nums">{formatRateMultiplier(period.ratePercent)}</span>
              </div>
            );
          })}
        </div>
      ) : null}
    </>
  );
}

type ModelPricingTooltipContentProps = {
  platformModelName: string;
  protocols: readonly string[];
  pricing: NonNullable<ChatModelOption["pricing"]>;
  billingDisplay: BillingDisplayOptions;
  labels: {
    image: ImageBillingLabels;
    freeModel: string;
    freeModelDescription: string;
    tieredPricing: string;
    callPricing: string;
    durationPricing: string;
    tokenPricing: string;
    input: string;
    output: string;
    cacheRead: string;
    perCall: string;
    perSecond: string;
    callUnit: string;
    secondUnit: string;
    billingDisplay: BillingDisplayLabels;
    schedule: {
      title: string;
      now: string;
      weekdays: readonly string[];
      /** Joins the ends of a weekday range: "周一至周五" (Chinese text avoids the hyphen). */
      dayRange: (from: string, to: string) => string;
      /** Separates weekdays and ranges in a list: "周一至周三、周六", "Mon–Wed, Sat". */
      daySeparator: string;
      /** Joins the ends of a time range: "13:00-18:00". */
      timeRange: (from: string, to: string) => string;
    };
  };
};

type ScheduleLabels = ModelPricingTooltipContentProps["labels"]["schedule"];

// Week-day list → "周一至周五", "周六、周日", "每天"; ranges only for runs of 3+ days.
function formatScheduleWeekdays(
  weekdays: readonly number[],
  { weekdays: names, dayRange: range, daySeparator }: Pick<ScheduleLabels, "weekdays" | "dayRange" | "daySeparator">,
): string {
  const ordered = [1, 2, 3, 4, 5, 6, 0].filter((day) => weekdays.includes(day));
  if (ordered.length === 7) {
    return names[7] ?? "";
  }
  const parts: string[] = [];
  let index = 0;
  while (index < ordered.length) {
    let end = index;
    while (end + 1 < ordered.length && ((ordered[end] ?? 0) + 1) % 7 === ordered[end + 1]) {
      end += 1;
    }
    if (end - index >= 2) {
      parts.push(range(names[ordered[index] ?? 0] ?? "", names[ordered[end] ?? 0] ?? ""));
    } else {
      for (let cursor = index; cursor <= end; cursor += 1) {
        parts.push(names[ordered[cursor] ?? 0] ?? "");
      }
    }
    index = end + 1;
  }
  return parts.join(daySeparator);
}

function ModelPricingBase({ platformModelName, protocols, pricing, billingDisplay, labels }: ModelPricingTooltipContentProps) {
  const cacheWriteLabel = cacheWritePricingLabel(protocols, labels.billingDisplay);
  const cacheWriteNote = cacheWritePricingNote(protocols, pricing, labels.billingDisplay);
  if (pricing.isFree) {
    return (
      <div className="flex flex-col gap-1">
        <span className="font-sans text-xs font-medium leading-4 text-background">{labels.freeModel}</span>
        <span className="font-sans text-[11px] leading-4 text-background/80">{labels.freeModelDescription}</span>
      </div>
    );
  }

  if (pricing.mode === "tiered") {
    return (
      <PricingTable
        platformModelName={platformModelName}
        title={labels.tieredPricing}
        footerNote={cacheWriteNote}
        headerRow={["", ...pricing.tiers.map((tier) => formatTierRange(tier.fromTokens, tier.upToTokens))]}
        bodyRows={[
          [labels.input, ...pricing.tiers.map((tier) => formatPricingUnitUSD(tier.inputUSDPerMTokens, billingDisplay))],
          [labels.output, ...pricing.tiers.map((tier) => formatPricingUnitUSD(tier.outputUSDPerMTokens, billingDisplay))],
          [labels.cacheRead, ...pricing.tiers.map((tier) => formatPricingUnitUSD(tier.cacheReadUSDPerMTokens, billingDisplay))],
          [cacheWriteLabel, ...pricing.tiers.map((tier) => formatPricingUnitUSD(resolveCacheWritePricingUSD(protocols, tier.cacheWriteUSDPerMTokens, pricing), billingDisplay))],
        ]}
      />
    );
  }

  if (pricing.mode === "call") {
    return (
      <div className="flex flex-col gap-1">
        <span className="font-sans text-xs font-medium leading-4 text-background">{labels.callPricing}</span>
        <PricingTooltipRow label={labels.perCall} value={`${formatPricingUnitUSD(pricing.callUSDPerCall, billingDisplay)} / ${labels.callUnit}`} />
      </div>
    );
  }

  if (pricing.mode === "duration") {
    return (
      <div className="flex flex-col gap-1">
        <span className="font-sans text-xs font-medium leading-4 text-background">{labels.durationPricing}</span>
        <PricingTooltipRow label={labels.perSecond} value={`${formatPricingUnitUSD(pricing.durationUSDPerSecond, billingDisplay)} / ${labels.secondUnit}`} />
      </div>
    );
  }

  const imageRows = resolveImagePriceRows(pricing, billingDisplay, labels);
  if (imageRows) {
    return (
      <div className="flex flex-col gap-1">
        <span className="font-sans text-xs font-medium leading-4 text-background">{labels.tokenPricing}</span>
        {imageRows.map((row) => <PricingTooltipRow key={row.label} label={row.label} value={`${row.values[0]} / 1M tokens`} />)}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1">
      <span className="font-sans text-xs font-medium leading-4 text-background">{labels.tokenPricing}</span>
      <PricingTooltipRow label={labels.input} value={`${formatPricingUnitUSD(pricing.inputUSDPerMTokens, billingDisplay)} / 1M tokens`} />
      <PricingTooltipRow label={labels.output} value={`${formatPricingUnitUSD(pricing.outputUSDPerMTokens, billingDisplay)} / 1M tokens`} />
      <PricingTooltipRow label={labels.cacheRead} value={`${formatPricingUnitUSD(pricing.cacheReadUSDPerMTokens, billingDisplay)} / 1M tokens`} />
      <PricingTooltipRow label={cacheWriteLabel} value={`${formatPricingUnitUSD(resolveCacheWritePricingUSD(protocols, pricing.cacheWriteUSDPerMTokens, pricing), billingDisplay)} / 1M tokens`} />
      {cacheWriteNote ? <span className="block max-w-72 font-sans text-[11px] leading-4 text-background/70">{cacheWriteNote}</span> : null}
    </div>
  );
}

function PricingTooltipRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[minmax(5.5rem,max-content)_auto] items-baseline gap-5 font-sans text-[11px] leading-4 text-background/80">
      <span className="whitespace-nowrap text-left">{label}</span>
      <span className="whitespace-nowrap text-right tabular-nums">{value}</span>
    </div>
  );
}

function PricingTable({
  platformModelName,
  title,
  footerNote,
  headerRow,
  bodyRows,
}: {
  platformModelName: string;
  title: string;
  footerNote?: string | null;
  headerRow: string[];
  bodyRows: string[][];
}) {
  return (
    <div className="flex max-w-[560px] flex-col gap-2 overflow-x-auto">
      <span className="font-sans text-xs font-medium leading-4 text-background">{title}</span>
      <table className="border-collapse text-left font-sans text-[11px] leading-4 text-background/80 tabular-nums">
        <thead>
          <tr className="border-b border-background/20">
            {headerRow.map((cell, index) => (
              <th
                key={`${platformModelName}-pricing-head-${index}`}
                scope="col"
                className={cn(
                  "whitespace-nowrap px-2 pb-1 font-medium text-background/70 first:pl-0 last:pr-0",
                  index > 0 ? "text-right" : null,
                )}
              >
                {cell}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {bodyRows.map((row, rowIndex) => (
            <tr key={`${platformModelName}-pricing-row-${rowIndex}`} className="border-b border-background/10 last:border-0">
              {row.map((cell, cellIndex) => (
                <td
                  key={`${platformModelName}-pricing-cell-${rowIndex}-${cellIndex}`}
                  className={cn(
                    "whitespace-nowrap px-2 py-1 first:pl-0 last:pr-0",
                    cellIndex === 0 ? "font-medium text-background/90" : "text-right",
                  )}
                >
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {footerNote ? <span className="block font-sans text-[11px] leading-4 text-background/70">{footerNote}</span> : null}
    </div>
  );
}

function formatPricingUnitUSD(value: number, billingDisplay: BillingDisplayOptions): string {
  return formatBillingDisplayUnitPriceFromUSD(value, billingDisplay);
}

// "≤ 200K", "200K – 1M", "> 1M": the token range a pricing tier applies to.
function formatTierRange(fromTokens: number, upToTokens: number | null): string {
  if (!upToTokens || upToTokens <= 0) {
    return `> ${formatTokenQuantity(fromTokens)}`;
  }
  if (fromTokens <= 0) {
    return `≤ ${formatTokenQuantity(upToTokens)}`;
  }
  return `${formatTokenQuantity(fromTokens)} – ${formatTokenQuantity(upToTokens)}`;
}

function formatTokenQuantity(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return "0";
  }
  if (value >= 1000000 && value % 1000000 === 0) {
    return `${value / 1000000}M`;
  }
  if (value >= 1000 && value % 1000 === 0) {
    return `${value / 1000}K`;
  }
  return String(value);
}

const MODALITY_ICONS: Record<ModelModality, React.ComponentType<{ className?: string; strokeWidth?: number }>> = {
  text: Type,
  image: ImageIcon,
  pdf: FileText,
  audio: AudioLines,
  video: Video,
  document: Files,
  attachment: Paperclip,
};

// Text is implied for every chat model, so only the other modalities are worth pointing out.
function mediaModalities(modalities: readonly ModelModality[]): ModelModality[] {
  return modalities.filter((modality) => modality !== "text");
}

type InputModalityLabels = {
  title: string;
  separator: string;
  names: Record<ModelModality, string>;
};

// "Input: image, video" style label; null when the model accepts nothing beyond text.
function describeInputModalities(modalities: ModelModality[], labels: InputModalityLabels): string | null {
  const visible = mediaModalities(modalities);
  if (visible.length === 0) {
    return null;
  }
  return `${labels.title}${visible.map((modality) => labels.names[modality]).join(labels.separator)}`;
}

// Non-text input modalities as compact icons; text is implied and omitted.
function ModelInputModalities({
  modalities,
  labels,
}: {
  modalities: ModelModality[];
  labels: InputModalityLabels;
}) {
  const description = describeInputModalities(modalities, labels);
  if (!description) {
    return null;
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="flex shrink-0 items-center gap-1 px-1 text-muted-foreground/60 group-hover:text-current group-data-[selected=true]:text-current"
          role="img"
          aria-label={description}
        >
          {mediaModalities(modalities).map((modality) => {
            const Icon = MODALITY_ICONS[modality];
            return <Icon key={modality} className="size-3" strokeWidth={1.8} />;
          })}
        </span>
      </TooltipTrigger>
      <TooltipContent side="right" sideOffset={8} className="z-[80] text-xs">
        {description}
      </TooltipContent>
    </Tooltip>
  );
}

function ModelPricingButton({ viewPricingLabel, ...props }: ModelPricingTooltipContentProps & { viewPricingLabel: string }) {
  const [pinned, setPinned] = React.useState(false);
  const [hovered, setHovered] = React.useState(false);
  // Preserve mouse hover behavior; touch, pen and keyboard can pin the same pricing content.
  const allowPinRef = React.useRef(true);

  return (
    <Popover open={pinned} onOpenChange={(next) => { if (!next || allowPinRef.current) setPinned(next); }}>
      <Tooltip open={hovered && !pinned} onOpenChange={setHovered}>
        <PopoverTrigger asChild>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground/70 transition-colors hover:text-current focus-visible:text-current focus-visible:outline-none group-hover:text-current group-focus-within:text-current group-data-[selected=true]:text-current"
              aria-label={viewPricingLabel}
              onPointerDown={(event) => { allowPinRef.current = event.pointerType !== "mouse"; }}
              onKeyDown={() => { allowPinRef.current = true; }}
              onClick={(event) => {
                event.stopPropagation();
                setHovered(false);
              }}
            >
              {props.pricing.isFree ? <TicketSlash className="size-3.5" strokeWidth={1.8} /> : <CircleDollarSign className="size-3.5" strokeWidth={1.8} />}
            </button>
          </TooltipTrigger>
        </PopoverTrigger>
        <TooltipContent side="right" align="center" sideOffset={8} className="z-[80] max-w-[min(92vw,35rem)] text-left font-medium tabular-nums">
          <ModelPricingTooltipContent {...props} />
        </TooltipContent>
      </Tooltip>
      <PopoverContent
        data-model-pricing-content=""
        side="right"
        align="center"
        sideOffset={8}
        collisionPadding={12}
        aria-label={`${props.platformModelName} · ${viewPricingLabel}`}
        className="z-[80] max-h-[var(--radix-popover-content-available-height)] w-max max-w-[min(calc(100vw-1.5rem),35rem)] overflow-auto overscroll-contain rounded-md border-0 bg-foreground px-3 py-1.5 text-left text-xs font-medium text-background tabular-nums"
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        onCloseAutoFocus={(event) => {
          // Outside clicks keep their target focus; closing must not reopen the hover tooltip.
          event.preventDefault();
          setHovered(false);
        }}
      >
        <ModelPricingTooltipContent {...props} />
      </PopoverContent>
    </Popover>
  );
}

function ChatModelMenuItem({
  model,
  selected,
  onSelect,
  billingDisplay,
  pricingLabels,
  inputModalityLabels,
  viewPricingLabel,
}: {
  model: ChatModelOption;
  selected: boolean;
  onSelect: () => void;
  billingDisplay: BillingDisplayOptions;
  pricingLabels: React.ComponentProps<typeof ModelPricingTooltipContent>["labels"];
  inputModalityLabels: InputModalityLabels;
  viewPricingLabel: string;
}) {
  const platformModelName = resolveModelOptionLabel(model.platformModelName);
  const iconURL = React.useMemo(() => resolveModelOptionIconUrl(model), [model]);

  return (
    <div
      data-selected={selected}
      className="group flex h-7 items-center rounded-md text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-within:bg-accent focus-within:text-accent-foreground data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground"
    >
      <button
        type="button"
        className="flex h-7 min-w-0 flex-1 items-center gap-2 rounded-md bg-transparent py-0 pl-2 pr-1 text-left text-[11px] font-medium leading-none text-inherit outline-none"
        onClick={onSelect}
      >
        <ModelIcon iconUrl={iconURL} label={platformModelName} />
        <span className="min-w-0 flex-1 truncate leading-4">
          {platformModelName}
        </span>
        <span className="flex size-3 shrink-0 items-center justify-center">
          {selected ? <Check className="size-3 text-current" strokeWidth={1.7} /> : null}
        </span>
      </button>
      <ModelInputModalities modalities={model.inputModalities} labels={inputModalityLabels} />
      {model.pricing ? <ModelPricingButton platformModelName={model.platformModelName} protocols={model.protocols} pricing={model.pricing} billingDisplay={billingDisplay} labels={pricingLabels} viewPricingLabel={viewPricingLabel} /> : null}
    </div>
  );
}

function resolveImagePriceRows(
  pricing: NonNullable<ChatModelOption["pricing"]>,
  display: BillingDisplayOptions,
  labels: ModelPricingTooltipContentProps["labels"],
): { label: string; values: string[] }[] | null {
  const image = pricing.mode === "token" ? parseImagePricing(pricing.imagePricingJSON) : null;
  if (!image) return null;
  return IMAGE_PRICING_DISPLAY_FIELDS.map(([field, label]) => ({
    label: labels.image[label], values: [formatPricingUnitUSD(image[field] ?? 0, display)],
  }));
}

function findSubmenuContent(submenu: HTMLElement, groupKey: string): HTMLElement | null {
  return submenu.querySelector<HTMLElement>(`[data-model-submenu-group="${CSS.escape(groupKey)}"]`);
}

export function ChatModelPicker({
  modelOptions,
  billingDisplayCurrency,
  billingDisplayUsdToCnyRate,
  selectedPlatformModelName,
  loading,
  disabled,
  onModelCatalogRefresh,
  onModelChange,
  placementPreference,
}: ChatModelPickerProps) {
  const t = useTranslations("chat.modelPicker");
  const tImage = useTranslations("adminBilling.imagePricing");
  const isMobile = useIsMobile();
  const mobileShift = useChatPopoverAlignOffset("end");
  const [open, setOpen] = React.useState(false);
  const [activeGroupKey, setActiveGroupKey] = React.useState("");
  const [mobileGroupKey, setMobileGroupKey] = React.useState<string | null>(null);
  const [desktopSubmenuSide, setDesktopSubmenuSide] = React.useState<"right" | "left">("right");
  const [desktopSubmenuTop, setDesktopSubmenuTop] = React.useState(0);
  const [desktopSubmenuWidth, setDesktopSubmenuWidth] = React.useState(DESKTOP_MODEL_MENU_WIDTH);
  // The submenu's content height, set explicitly so switching groups can animate it.
  const [desktopSubmenuBodyHeight, setDesktopSubmenuBodyHeight] = React.useState<number | null>(null);
  const [desktopGroupListMaxHeight, setDesktopGroupListMaxHeight] = React.useState(320);
  const [desktopSubmenuListMaxHeight, setDesktopSubmenuListMaxHeight] = React.useState(320);
  const desktopMenuRootRef = React.useRef<HTMLDivElement | null>(null);
  const desktopGroupMenuRef = React.useRef<HTMLDivElement | null>(null);
  // Radix mounts the popover body in a portal one commit after it opens, so the refs are still
  // empty when the open effect runs. This flips once the node exists and re-runs the metrics.
  const [desktopMenuMounted, setDesktopMenuMounted] = React.useState(false);
  const attachDesktopMenuRoot = React.useCallback((node: HTMLDivElement | null) => {
    desktopMenuRootRef.current = node;
    setDesktopMenuMounted(node !== null);
  }, []);
  const desktopSubmenuRef = React.useRef<HTMLDivElement | null>(null);
  const desktopSubmenuBodyRef = React.useRef<HTMLDivElement | null>(null);
  const pendingDesktopFocusRef = React.useRef(false);
  const desktopGroupItemRefs = React.useRef(new Map<string, HTMLButtonElement>());
  const selectedModel = React.useMemo(
    () => modelOptions.find((item) => item.platformModelName === selectedPlatformModelName) ?? null,
    [modelOptions, selectedPlatformModelName],
  );
  // Same grouping as the list, so a model on the user's own key opens its provider group.
  const selectedPresentation = React.useMemo(
    () => (selectedModel ? resolveModelGroupPresentation(selectedModel) : null),
    [selectedModel],
  );
  const selectedGroupKey = selectedPresentation?.key ?? "";
  const selectedGroupLabel = selectedPresentation?.label ?? "";
  const modelGroups = React.useMemo(() => resolveModelGroups(modelOptions), [modelOptions]);
  // Counts share one width, so the badges in front of them line up whatever the digit count.
  const groupCountStyle = React.useMemo<React.CSSProperties>(
    () => ({ minWidth: `${String(Math.max(0, ...modelGroups.map((group) => group.items.length))).length}ch` }),
    [modelGroups],
  );
  const activeDesktopGroupKey = activeGroupKey || selectedGroupKey || modelGroups[0]?.key || "";
  const activeDesktopGroup = React.useMemo(
    () => modelGroups.find((group) => group.key === activeDesktopGroupKey) ?? modelGroups[0] ?? null,
    [activeDesktopGroupKey, modelGroups],
  );
  const hasDesktopModelSubmenu = Boolean(activeDesktopGroup?.items.length);
  const mobileGroup = React.useMemo(
    () => modelGroups.find((group) => group.key === mobileGroupKey) ?? null,
    [mobileGroupKey, modelGroups],
  );
  // Which way the pointer moved, so the next group's models slide in from that side.
  const desktopGroupDirection = useSwitchDirection(activeDesktopGroup ? modelGroups.indexOf(activeDesktopGroup) : -1);
  const inputModalityLabels = React.useMemo<InputModalityLabels>(
    () => ({
      title: t("inputModalities.title"),
      separator: t("inputModalities.separator"),
      names: {
        text: t("inputModalities.text"),
        image: t("inputModalities.image"),
        pdf: t("inputModalities.pdf"),
        audio: t("inputModalities.audio"),
        video: t("inputModalities.video"),
        document: t("inputModalities.document"),
        attachment: t("inputModalities.attachment"),
      },
    }),
    [t],
  );
  const pricingLabels = React.useMemo(
    () => ({
      image: { model: tImage("modelUsage"), tool: tImage("toolUsage"), textInput: tImage("textInput"), textOutput: tImage("textOutput"), imageInput: tImage("imageInput"), imageOutput: tImage("imageOutput"), cachedTextInput: tImage("cachedTextInput"), cachedImageInput: tImage("cachedImageInput") },
      freeModel: t("freeModel"),
      freeModelDescription: t("freeModelDescription"),
      tieredPricing: t("tieredPricing"),
      callPricing: t("callPricing"),
      durationPricing: t("durationPricing"),
      tokenPricing: t("tokenPricing"),
      input: t("input"),
      output: t("output"),
      cacheRead: t("cacheRead"),
      perCall: t("perCall"),
      perSecond: t("perSecond"),
      callUnit: t("callUnit"),
      secondUnit: t("secondUnit"),
      schedule: {
        title: t("schedule.title"),
        now: t("schedule.now"),
        weekdays: [t("schedule.sun"), t("schedule.mon"), t("schedule.tue"), t("schedule.wed"), t("schedule.thu"), t("schedule.fri"), t("schedule.sat"), t("schedule.everyDay")],
        dayRange: (from: string, to: string) => t("schedule.dayRange", { from, to }),
        daySeparator: t("schedule.daySeparator"),
        timeRange: (from: string, to: string) => t("schedule.timeRange", { from, to }),
      },
      billingDisplay: {
        cacheWrite: t("cacheWrite"),
        cacheWrite5m: t("cacheWrite5m"),
        cacheWrite1h: t("cacheWrite1h"),
        cacheWrite5m1h: t("cacheWrite5m1h"),
        claudeCacheWriteMixedNote: (multiplier: string) => t("claudeCacheWriteMixedNote", { multiplier }),
        claudeCacheWriteNote: (timeout: "5m" | "1h", multiplier: string) => t("claudeCacheWriteNote", { timeout, multiplier }),
        claudeFastModeNote: (multiplier: string) => t("claudeFastModeNote", { multiplier }),
        openaiServiceTierNote: (tier: string, multiplier: string) => t("openaiServiceTierNote", { tier, multiplier }),
        scheduleRateNote: (period: string, multiplier: string) => t("scheduleRateNote", { period, multiplier }),
        cacheWritePricingLabel: t("cacheWritePricingLabel"),
        cacheWritePricingNote: t("cacheWritePricingNote"),
      },
    }),
    [t, tImage],
  );
  const billingDisplay = React.useMemo<BillingDisplayOptions>(
    () => ({
      currency: billingDisplayCurrency,
      usdToCnyRate: billingDisplayUsdToCnyRate,
    }),
    [billingDisplayCurrency, billingDisplayUsdToCnyRate],
  );

  React.useEffect(() => {
    if (!open || !isMobile) {
      setMobileGroupKey(null);
    }
  }, [isMobile, open]);

  const updateDesktopSubmenuMetrics = React.useCallback(() => {
    if (!open || isMobile) {
      // Keep the last placement: Radix keeps the content mounted through the
      // ~150ms close animation, and resetting side/top here would visibly
      // snap a left-side submenu to the right while it fades out. Reopening
      // recomputes every metric in the layout effect before paint.
      return;
    }

    const menuRoot = desktopMenuRootRef.current;
    const groupMenu = desktopGroupMenuRef.current;
    if (!menuRoot || !groupMenu) {
      return;
    }

    const layoutRect = createPopoverLayoutRectReader(menuRoot);
    const menuRootRect = layoutRect(menuRoot);
    const groupMenuRect = layoutRect(groupMenu);
    const submenu = desktopSubmenuRef.current;
    const submenuBody = desktopSubmenuBodyRef.current;
    // While groups switch, the outgoing list is still in the card; measure the incoming one.
    const submenuContent = submenu && activeDesktopGroup ? findSubmenuContent(submenu, activeDesktopGroup.key) : null;
    const submenuScrollViewport = submenuContent?.querySelector<HTMLElement>("[data-model-menu-viewport]");
    const activeGroupButton = activeDesktopGroup
      ? desktopGroupItemRefs.current.get(activeDesktopGroup.key)
      : null;
    const activeGroupRect = activeGroupButton ? layoutRect(activeGroupButton) : undefined;
    const triggerRect = document.getElementById("chat-model-menu-trigger")?.getBoundingClientRect();
    const viewportLeft = MODEL_MENU_COLLISION_PADDING;
    const viewportRight = window.innerWidth - MODEL_MENU_COLLISION_PADDING;
    const viewportTop = MODEL_MENU_COLLISION_PADDING;
    const viewportBottom = window.innerHeight - MODEL_MENU_COLLISION_PADDING;
    const viewportHeight = Math.max(0, viewportBottom - viewportTop);
    const rightAvailableWidth = Math.max(0, viewportRight - groupMenuRect.right - DESKTOP_MODEL_SUBMENU_GAP);
    const leftAvailableWidth = Math.max(0, groupMenuRect.left - viewportLeft - DESKTOP_MODEL_SUBMENU_GAP);
    const nextSubmenuSide =
      rightAvailableWidth >= DESKTOP_MODEL_MENU_WIDTH || rightAvailableWidth >= leftAvailableWidth
        ? "right"
        : "left";
    const nextSubmenuWidth = Math.max(
      DESKTOP_MODEL_MENU_MIN_SCROLL_HEIGHT,
      Math.min(
        DESKTOP_MODEL_MENU_WIDTH,
        nextSubmenuSide === "right" ? rightAvailableWidth : leftAvailableWidth,
      ),
    );
    const nextGroupListMaxHeight = resolveDesktopModelMenuListMaxHeight({
      viewportTop,
      viewportBottom,
      triggerTop: triggerRect?.top,
      triggerBottom: triggerRect?.bottom,
      sideOffset: DESKTOP_MODEL_MENU_SIDE_OFFSET,
      verticalChrome: DESKTOP_GROUP_MENU_VERTICAL_CHROME,
    });

    let nextSubmenuTop = 0;
    let nextSubmenuListMaxHeight = nextGroupListMaxHeight;
    let nextSubmenuBodyHeight: number | null = null;
    if (hasDesktopModelSubmenu && activeGroupRect) {
      // Derive the height from the unclamped scroll content instead of feeding
      // the rendered (clamped) height back in. The assumed chrome misses the
      // real border-included chrome by ~1px, so that feedback loop grows a
      // bottom-anchored submenu by the error once per ResizeObserver pass,
      // crawling it upward for seconds instead of placing it in one frame.
      // Padding and border around the body. Layout sizes ignore the opening scale and stay exact
      // while the body height animates, unlike rects.
      const submenuChrome = submenu && submenuBody
        ? Math.max(DESKTOP_SUBMENU_VERTICAL_CHROME, submenu.offsetHeight - submenuBody.offsetHeight)
        : DESKTOP_SUBMENU_VERTICAL_CHROME;
      const submenuContentHeight = submenuScrollViewport?.scrollHeight ?? nextGroupListMaxHeight;
      const submenuOuterHeight = Math.min(submenuContentHeight + submenuChrome, viewportHeight);
      const maxViewportTop = Math.max(viewportTop, viewportBottom - submenuOuterHeight);
      // Anchor in viewport coordinates, then convert to an offset within
      // menuRoot (which may sit above viewportTop for a frame before re-shift).
      const preferredSubmenuViewportTop = Math.min(
        Math.max(activeGroupRect.top, viewportTop),
        maxViewportTop,
      );
      nextSubmenuTop = preferredSubmenuViewportTop - menuRootRect.top;
      const actualSubmenuViewportTop = menuRootRect.top + nextSubmenuTop;
      nextSubmenuListMaxHeight = resolveDesktopMenuListMaxHeight(
        Math.min(viewportHeight, viewportBottom - actualSubmenuViewportTop),
        submenuChrome,
      );
      nextSubmenuBodyHeight = submenuScrollViewport ? Math.min(submenuContentHeight, nextSubmenuListMaxHeight) : null;
    }

    setDesktopSubmenuSide(nextSubmenuSide);
    setDesktopSubmenuTop(nextSubmenuTop);
    setDesktopSubmenuBodyHeight(nextSubmenuBodyHeight);
    setDesktopSubmenuWidth(nextSubmenuWidth);
    setDesktopGroupListMaxHeight(nextGroupListMaxHeight);
    setDesktopSubmenuListMaxHeight(nextSubmenuListMaxHeight);
  }, [activeDesktopGroup, hasDesktopModelSubmenu, isMobile, open]);

  React.useLayoutEffect(() => {
    updateDesktopSubmenuMetrics();

    if (!open || isMobile) {
      return;
    }

    window.addEventListener("resize", updateDesktopSubmenuMetrics);
    window.addEventListener("scroll", updateDesktopSubmenuMetrics, true);
    // Radix places the popover by rewriting its wrapper's transform: on the first frame and again
    // whenever the trigger moves (the composer growing, for one). No resize observer sees a
    // transform, so watch the style itself; the viewport clamping depends on where the menu is.
    const popperWrapper = desktopMenuRootRef.current?.closest("[data-slot=popover-content]")?.parentElement;
    const moveObserver = new MutationObserver(updateDesktopSubmenuMetrics);
    if (popperWrapper) {
      moveObserver.observe(popperWrapper, { attributes: true, attributeFilter: ["style"] });
    }
    if (typeof ResizeObserver === "undefined") {
      return () => {
        window.removeEventListener("resize", updateDesktopSubmenuMetrics);
        window.removeEventListener("scroll", updateDesktopSubmenuMetrics, true);
        moveObserver.disconnect();
      };
    }

    const observer = new ResizeObserver(updateDesktopSubmenuMetrics);
    if (desktopMenuRootRef.current) {
      observer.observe(desktopMenuRootRef.current);
    }
    if (desktopGroupMenuRef.current) {
      observer.observe(desktopGroupMenuRef.current);
    }
    const submenuContent = desktopSubmenuRef.current && activeDesktopGroup
      ? findSubmenuContent(desktopSubmenuRef.current, activeDesktopGroup.key)
      : null;
    if (submenuContent) {
      observer.observe(submenuContent);
      const list = submenuContent.querySelector("[data-model-menu-viewport]")?.firstElementChild;
      if (list) {
        observer.observe(list);
      }
    }
    const activeGroupButton = activeDesktopGroup
      ? desktopGroupItemRefs.current.get(activeDesktopGroup.key)
      : null;
    if (activeGroupButton) {
      observer.observe(activeGroupButton);
    }

    return () => {
      window.removeEventListener("resize", updateDesktopSubmenuMetrics);
      window.removeEventListener("scroll", updateDesktopSubmenuMetrics, true);
      moveObserver.disconnect();
      observer.disconnect();
    };
  }, [activeDesktopGroup, desktopMenuMounted, hasDesktopModelSubmenu, isMobile, open, updateDesktopSubmenuMetrics]);

  React.useLayoutEffect(() => {
    if (!open || isMobile || !desktopMenuMounted || desktopSubmenuBodyHeight === null || desktopSubmenuListMaxHeight <= 0 || activeDesktopGroupKey !== selectedGroupKey) return;
    const frame = window.requestAnimationFrame(() => {
      const submenu = desktopSubmenuRef.current;
      const content = submenu ? findSubmenuContent(submenu, selectedGroupKey) : null;
      const button = content?.querySelector<HTMLButtonElement>("[data-selected=true] > button");
      const viewport = button?.closest<HTMLElement>("[data-model-menu-viewport]");
      if (!button || !viewport) return;
      if (pendingDesktopFocusRef.current) {
        button.focus({ preventScroll: true });
        pendingDesktopFocusRef.current = false;
      }
      const rowRect = button.getBoundingClientRect();
      const viewportRect = viewport.getBoundingClientRect();
      const scale = viewport.clientHeight > 0 ? viewportRect.height / viewport.clientHeight : 1;
      if (rowRect.bottom > viewportRect.bottom) {
        viewport.scrollTop += (rowRect.bottom - viewportRect.bottom) / (scale || 1);
      } else if (rowRect.top < viewportRect.top) {
        viewport.scrollTop += (rowRect.top - viewportRect.top) / (scale || 1);
      }
    });
    return () => window.cancelAnimationFrame(frame);
  }, [open, isMobile, desktopMenuMounted, activeDesktopGroupKey, selectedGroupKey, desktopSubmenuBodyHeight, desktopSubmenuListMaxHeight]);

  const handleOpenChange = React.useCallback(
    (nextOpen: boolean) => {
      pendingDesktopFocusRef.current = nextOpen;
      if (nextOpen) {
        setActiveGroupKey(selectedGroupKey || modelGroups[0]?.key || "");
        if (onModelCatalogRefresh) {
          void Promise.resolve(onModelCatalogRefresh()).catch((): undefined => undefined);
        }
      }
      setOpen(nextOpen);
    },
    [modelGroups, onModelCatalogRefresh, selectedGroupKey],
  );

  const closeMenu = React.useCallback(() => {
    handleOpenChange(false);
  }, [handleOpenChange]);

  const selectDesktopGroup = React.useCallback((groupKey: string) => {
    if (groupKey === activeDesktopGroupKey) {
      return;
    }
    setActiveGroupKey(groupKey);
  }, [activeDesktopGroupKey]);

  return (
      <div className="min-w-0 max-w-[min(320px,100%)] shrink">
        <Popover open={open} onOpenChange={handleOpenChange}>
          <PopoverTrigger asChild>
            <InputGroupButton
              ref={mobileShift.triggerRef}
              id="chat-model-menu-trigger"
              type="button"
              variant="ghost"
              size="sm"
              className="w-full min-w-0 max-w-[min(320px,100%)] rounded-lg px-1.5 hover:bg-accent focus-visible:bg-accent data-[state=open]:bg-accent sm:px-2"
              disabled={disabled || loading}
              aria-label={t("selectModel")}
            >
              {loading ? (
                <ChatModelTriggerSkeleton />
              ) : selectedModel ? (
                <ChatModelIdentity model={selectedModel} />
              ) : selectedPlatformModelName.trim() ? (
                <span className="truncate text-[12px] font-medium text-foreground">
                  {resolveModelOptionLabel(selectedPlatformModelName)}
                </span>
              ) : (
                <span className="truncate text-[12px] font-medium text-muted-foreground">
                  {t("selectModel")}
                </span>
              )}
            </InputGroupButton>
          </PopoverTrigger>
          {/* On a phone the menu keeps the composer's side, as the other composer menus do, and its
              list shrinks to the room left there. Letting Radix flip it measured the unclamped list,
              which never fits below the landing composer, so it always opened upwards. */}
          <PopoverContent
            ref={isMobile ? mobileShift.contentRef : undefined}
            align="end"
            alignOffset={isMobile ? mobileShift.alignOffset : 0}
            side={isMobile ? placementPreference : "bottom"}
            avoidCollisions={!isMobile}
            sideOffset={DESKTOP_MODEL_MENU_SIDE_OFFSET}
            collisionPadding={24}
            onInteractOutside={(event) => {
              if (event.target instanceof Element && event.target.closest("[data-model-pricing-content]")) event.preventDefault();
            }}
            onOpenAutoFocus={(event) => {
              if (!isMobile) {
                event.preventDefault();
                // Focus the incoming group's row after portal mount and sizing;
                // Radix's default focus also scrolls animated clipping ancestors.
              }
            }}
            className={cn(
              "relative overflow-visible rounded-xl",
              isMobile
                ? "w-[min(20rem,calc(100vw-3rem))] p-1.5 [--model-menu-scroll-max-height:max(7rem,calc(var(--radix-popover-content-available-height)-2.75rem))]"
                : "w-[min(14rem,calc(100vw-3rem))] border-0 bg-transparent p-0 shadow-none",
            )}
          >
            {isMobile ? (
              <>
                <div className="flex h-7 items-center justify-between gap-2 px-2">
                  {mobileGroup ? (
                    <button
                      type="button"
                      className="-ml-1.5 flex h-7 min-w-0 items-center gap-0.5 rounded-md px-0.5 text-[11px] font-medium text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-foreground focus-visible:bg-accent focus-visible:text-foreground"
                      onClick={() => setMobileGroupKey(null)}
                    >
                      <ChevronLeft className="size-3.5" strokeWidth={1.8} />
                      <span>{t("group")}</span>
                    </button>
                  ) : (
                    <span className="text-[11px] font-medium text-foreground">{t("group")}</span>
                  )}
                  <span className="min-w-0 truncate text-right text-[10px] font-medium text-muted-foreground">
                    {mobileGroup ? mobileGroup.label : selectedGroupLabel}
                  </span>
                </div>
                {modelGroups.length === 0 ? (
                  <div className="px-2 py-3 text-[11px] leading-4 text-muted-foreground">
                    {t("empty")}
                  </div>
                ) : (
                  <ModelMenuScrollContainer>
                    {mobileGroup ? (
                      <div className="flex flex-col gap-0.5">
                        {mobileGroup.items.map((item) => (
                          <ChatModelMenuItem
                            key={item.platformModelName}
                            model={item}
                            selected={item.platformModelName === selectedPlatformModelName}
                            onSelect={() => {
                              onModelChange(item.platformModelName);
                              closeMenu();
                            }}
                            billingDisplay={billingDisplay}
                            pricingLabels={pricingLabels}
                            inputModalityLabels={inputModalityLabels}
                            viewPricingLabel={t("viewPricing")}
                          />
                        ))}
                      </div>
                    ) : (
                      <div className="flex flex-col gap-0.5">
                        {modelGroups.map((group) => {
                          const selectedGroup = group.key === selectedGroupKey;
                          const groupIconURL = resolveModelIconURL(group.icon);
                          return (
                            <button
                              type="button"
                              key={group.key}
                              className={cn(
                                "flex h-7 w-full items-center justify-between gap-2 rounded-md px-2 py-0 text-left text-[11px] font-medium outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground",
                                selectedGroup ? "bg-accent text-accent-foreground" : "text-muted-foreground",
                              )}
                              onClick={() => {
                                setMobileGroupKey(group.key);
                              }}
                            >
                              {group.personal && !group.icon ? <PersonalGroupIcon /> : <ModelIcon iconUrl={groupIconURL} label={group.label} />}
                              <span className="min-w-0 flex-1 truncate font-medium">{group.label}</span>
                              {group.personal ? <PersonalGroupBadge label={t("personalBadge")} /> : null}
                              <span className="shrink-0 text-right text-[10px] tabular-nums text-muted-foreground/80" style={groupCountStyle}>
                                {group.items.length}
                              </span>
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </ModelMenuScrollContainer>
                )}
              </>
            ) : (
              <div ref={attachDesktopMenuRoot} className="relative min-w-0">
                {hasDesktopModelSubmenu && activeDesktopGroup ? (
                  <MorphingCard
                    ref={desktopSubmenuRef}
                    bodyRef={desktopSubmenuBodyRef}
                    top={desktopSubmenuTop}
                    bodyHeight={desktopSubmenuBodyHeight}
                    style={{ width: desktopSubmenuWidth }}
                    className={cn(
                      "absolute flex max-h-[calc(100dvh-3rem)] flex-col overflow-hidden rounded-xl border-[0.5px] border-border bg-popover p-1.5 shadow-xs",
                      desktopSubmenuSide === "right" ? "left-[calc(100%+0.5rem)]" : "right-[calc(100%+0.5rem)]",
                    )}
                  >
                    <SlideSwitch
                      itemKey={activeDesktopGroup.key}
                      direction={desktopGroupDirection}
                      axis="y"
                      duration={MORPH_DURATION_S}
                    >
                      <div data-model-submenu-group={activeDesktopGroup.key}>
                        <ModelMenuScrollContainer maxHeight={desktopSubmenuListMaxHeight}>
                          <div className="flex flex-col gap-0.5">
                            {activeDesktopGroup.items.map((item) => (
                              <ChatModelMenuItem
                                key={item.platformModelName}
                                model={item}
                                selected={item.platformModelName === selectedPlatformModelName}
                                onSelect={() => {
                                  onModelChange(item.platformModelName);
                                  closeMenu();
                                }}
                                billingDisplay={billingDisplay}
                                pricingLabels={pricingLabels}
                                inputModalityLabels={inputModalityLabels}
                                viewPricingLabel={t("viewPricing")}
                              />
                            ))}
                          </div>
                        </ModelMenuScrollContainer>
                      </div>
                    </SlideSwitch>
                  </MorphingCard>
                ) : null}


                <div
                  ref={desktopGroupMenuRef}
                  className="flex min-w-0 max-h-[calc(100dvh-3rem)] flex-col overflow-hidden rounded-xl border-[0.5px] border-border bg-popover p-1.5 shadow-xs"
                >
                  <div className="flex h-7 shrink-0 items-center justify-between gap-3 px-2">
                    <span className="text-[11px] font-medium text-foreground">{t("group")}</span>
                    <span className="truncate text-[10px] font-medium text-muted-foreground">
                      {selectedGroupLabel}
                    </span>
                  </div>
                  {modelGroups.length === 0 ? (
                    <div className="px-2 py-3 text-[11px] leading-4 text-muted-foreground">
                      {t("empty")}
                    </div>
                  ) : (
                    <div className="min-h-0 min-w-0">
                      <ModelMenuScrollContainer maxHeight={desktopGroupListMaxHeight}>
                        <div className="flex flex-col gap-0.5">
                          {modelGroups.map((group) => {
                            const selectedGroup = group.key === selectedGroupKey;
                            const activeGroup = group.key === activeDesktopGroup?.key;
                            const groupIconURL = resolveModelIconURL(group.icon);
                            return (
                              <button
                                type="button"
                                key={group.key}
                                ref={(node) => {
                                  if (node) {
                                    desktopGroupItemRefs.current.set(group.key, node);
                                    return;
                                  }
                                  desktopGroupItemRefs.current.delete(group.key);
                                }}
                                className={cn(
                                  "flex h-7 w-full items-center gap-2 rounded-md px-2 py-0 text-left text-[11px] font-medium outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground",
                                  activeGroup ? "bg-accent text-accent-foreground" : "text-muted-foreground",
                                  selectedGroup && !activeGroup ? "text-foreground" : null,
                                )}
                                onMouseEnter={() => selectDesktopGroup(group.key)}
                                onFocus={() => selectDesktopGroup(group.key)}
                                onClick={() => selectDesktopGroup(group.key)}
                              >
                                {group.personal && !group.icon ? <PersonalGroupIcon /> : <ModelIcon iconUrl={groupIconURL} label={group.label} />}
                                <span className="min-w-0 flex-1 truncate font-medium">{group.label}</span>
                                {group.personal ? <PersonalGroupBadge label={t("personalBadge")} /> : null}
                                <span className="shrink-0 text-right text-[10px] tabular-nums text-muted-foreground/80" style={groupCountStyle}>
                                  {group.items.length}
                                </span>
                                <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/65" strokeWidth={1.8} />
                              </button>
                            );
                          })}
                        </div>
                      </ModelMenuScrollContainer>
                    </div>
                  )}
                </div>
              </div>
            )}
        </PopoverContent>
      </Popover>
      </div>
  );
}
