"use client";

import * as React from "react";

type MobileHeaderActionContextValue = {
  slot: HTMLElement | null;
  setSlot: React.Dispatch<React.SetStateAction<HTMLElement | null>>;
  previewActive: boolean;
  setPreviewActive: React.Dispatch<React.SetStateAction<boolean>>;
  previewSlot: HTMLElement | null;
  setPreviewSlot: React.Dispatch<React.SetStateAction<HTMLElement | null>>;
};

const MobileHeaderActionContext = React.createContext<MobileHeaderActionContextValue | null>(null);

export function MobileHeaderActionProvider({ children }: { children: React.ReactNode }) {
  const [slot, setSlot] = React.useState<HTMLElement | null>(null);
  const [previewActive, setPreviewActive] = React.useState(false);
  const [previewSlot, setPreviewSlot] = React.useState<HTMLElement | null>(null);
  const value = React.useMemo(() => ({ slot, setSlot, previewActive, setPreviewActive, previewSlot, setPreviewSlot }), [slot, previewActive, previewSlot]);

  return (
    <MobileHeaderActionContext.Provider value={value}>
      {children}
    </MobileHeaderActionContext.Provider>
  );
}

export function MobileHeaderActionSlot() {
  const context = React.useContext(MobileHeaderActionContext);
  if (!context) {
    throw new Error("MobileHeaderActionSlot must be used within MobileHeaderActionProvider");
  }
  return <div ref={context.setSlot} className="flex items-center" />;
}

export function useMobileHeaderActionSlot() {
  const context = React.useContext(MobileHeaderActionContext);
  if (!context) {
    throw new Error("useMobileHeaderActionSlot must be used within MobileHeaderActionProvider");
  }
  return context.slot;
}

// A preview contributes file controls while the shell retains navigation.
export function useMobilePreviewHeaderSlot(active: boolean) {
  const context = React.useContext(MobileHeaderActionContext);
  const setActive = context?.setPreviewActive;
  React.useLayoutEffect(() => {
    setActive?.(active);
    return () => setActive?.(false);
  }, [active, setActive]);
  return active ? context?.previewSlot ?? null : null;
}

export function useMobilePreviewHeaderHost() {
  const context = React.useContext(MobileHeaderActionContext);
  if (!context) throw new Error("Mobile header must be used within MobileHeaderActionProvider");
  return { active: context.previewActive, ref: context.setPreviewSlot };
}
