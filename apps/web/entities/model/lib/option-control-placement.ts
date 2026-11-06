import type { ModelControlPlacement } from "./model-controls";

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function optionPath(value: unknown): string {
  return typeof value === "string" ? value.split(".").map((segment) => segment.trim()).filter(Boolean).join(".") : "";
}

function matchesOptionControl(control: unknown, path: string): control is Record<string, unknown> {
  return isRecord(control) && control.kind !== "reasoning" && (typeof control.id === "string" && control.id.trim() === path || optionPath(control.path) === path);
}

/** Explicit controls take precedence over legacy controls with the same ID or number path. */
export function resolveOptionControlPlacement(path: string, placement: unknown, explicitControls: unknown): ModelControlPlacement {
  const explicit = Array.isArray(explicitControls) ? explicitControls.find((control) => matchesOptionControl(control, path)) : undefined;
  const effective = explicit ? explicit.placement : placement;
  return effective === "toolbar" ? "toolbar" : "menu";
}

/** Keep explicit patches and presentation intact while updating the parameter's default position. */
export function updateOptionControlPlacements(
  explicitControls: unknown,
  placements: ReadonlyMap<string, ModelControlPlacement>,
): unknown {
  if (!Array.isArray(explicitControls)) return explicitControls;
  return explicitControls.map((control) => {
    if (!isRecord(control) || control.kind === "reasoning") return control;
    const placement = (typeof control.id === "string" ? placements.get(control.id.trim()) : undefined)
      ?? placements.get(optionPath(control.path));
    if (placement === undefined || placement === (control.placement === "toolbar" ? "toolbar" : "menu")) return control;
    return { ...control, placement };
  });
}
