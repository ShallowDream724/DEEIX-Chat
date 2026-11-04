"use client";

import * as React from "react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

type Props = Omit<React.ComponentProps<typeof Input>, "value" | "defaultValue"> & {
  value: string;
  suffix?: string;
  zeroDisplayLabel?: string;
  dirty?: boolean;
};

export function SettingsValueInput({ value, suffix, zeroDisplayLabel, dirty = false, placeholder, className, onFocus, onBlur, ...props }: Props) {
  const [focused, setFocused] = React.useState(false);
  const showZeroLabel = value === "0" && Boolean(zeroDisplayLabel) && !dirty && !focused;

  return (
    <div className="relative min-w-0">
      <Input
        {...props}
        value={showZeroLabel ? "" : value}
        placeholder={showZeroLabel ? zeroDisplayLabel : placeholder}
        onFocus={(event) => {
          setFocused(true);
          onFocus?.(event);
        }}
        onBlur={(event) => {
          setFocused(false);
          onBlur?.(event);
        }}
        className={cn("min-w-0 text-left md:text-right", suffix ? "pr-12" : "", className)}
      />
      {suffix ? <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-muted-foreground">{suffix}</span> : null}
    </div>
  );
}
