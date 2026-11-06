import { isRecord } from "@/shared/lib/type-guards";
import { MAX_PINNED_MODEL_CONTROLS, type ModelControlPlacement } from "@/entities/model";

// User overrides of where a control is shown, keyed by control id (shared by all models).
export const MODEL_CONTROL_PLACEMENTS_SETTING_KEY = "chat.model_control_placements";

export type ModelControlPlacements = Record<string, ModelControlPlacement>;

export function parseModelControlPlacements(raw: string | null | undefined): ModelControlPlacements {
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (!isRecord(parsed)) return {};
    const result: ModelControlPlacements = {};
    for (const [id, placement] of Object.entries(parsed)) {
      if (placement === "toolbar" || placement === "menu") result[id] = placement;
    }
    return result;
  } catch {
    return {};
  }
}

// A composer item the user can place: a model control or a provider-native tool toggle.
export type ComposerControlItem = {
  id: string;
  defaultPlacement: ModelControlPlacement;
  pinnable: boolean;
};

/**
 * Splits items into toolbar and menu. User overrides win over the administrator default; at most
 * MAX_PINNED_MODEL_CONTROLS items reach the toolbar, in declaration order.
 */
export function splitComposerControlItems<T extends ComposerControlItem>(
  items: readonly T[],
  placements: ModelControlPlacements,
): { toolbar: T[]; menu: T[] } {
  const toolbar: T[] = [];
  const menu: T[] = [];
  const pinnedIDs = new Set(items
    .filter((item) => item.pinnable && placements[item.id] === "toolbar")
    .slice(0, MAX_PINNED_MODEL_CONTROLS)
    .map((item) => item.id));
  for (const item of items) {
    if (pinnedIDs.size >= MAX_PINNED_MODEL_CONTROLS) break;
    if (item.pinnable && placements[item.id] === undefined && item.defaultPlacement === "toolbar") pinnedIDs.add(item.id);
  }
  for (const item of items) {
    if (pinnedIDs.has(item.id)) {
      toolbar.push(item);
    } else {
      menu.push(item);
    }
  }
  return { toolbar, menu };
}

/**
 * Next placements after pinning or unpinning an item. Only differences from the item default are
 * stored; pinning beyond the limit returns null.
 */
export function nextModelControlPlacements<T extends ComposerControlItem>(
  items: readonly T[],
  placements: ModelControlPlacements,
  item: T,
  pinned: boolean,
): ModelControlPlacements | null {
  const next: ModelControlPlacements = { ...placements };
  const target: ModelControlPlacement = pinned ? "toolbar" : "menu";
  if (target === item.defaultPlacement) {
    delete next[item.id];
  } else {
    next[item.id] = target;
  }
  const explicitPinnedCount = items.filter((candidate) => candidate.pinnable && next[candidate.id] === "toolbar").length;
  const toolbar = splitComposerControlItems(items, next).toolbar;
  if (pinned && (explicitPinnedCount > MAX_PINNED_MODEL_CONTROLS || !toolbar.some((candidate) => candidate.id === item.id))) {
    return null;
  }
  // Placements of controls from other models are kept, but only the pinned ones count toward the
  // server-side limit, so drop stale pins that would push the total over it.
  const visibleIDs = new Set(items.map((candidate) => candidate.id));
  const stalePins = Object.entries(next).filter(([id, placement]) => placement === "toolbar" && !visibleIDs.has(id));
  let overflow = Object.values(next).filter((placement) => placement === "toolbar").length - MAX_PINNED_MODEL_CONTROLS;
  for (const [id] of stalePins) {
    if (overflow <= 0) break;
    delete next[id];
    overflow -= 1;
  }
  return next;
}
