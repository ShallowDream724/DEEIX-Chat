"use client";

import { Loader2, Save } from "lucide-react";
import { useTranslations } from "next-intl";
import { useSearchParams } from "next/navigation";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { SettingsValueInput } from "@/features/admin/components/shared/settings-value-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAdminFilesInputLimitsUpstreams } from "@/features/admin/hooks/use-admin-files-input-limits-upstreams";
import {
  formatInputLimit,
  INPUT_LIMIT_FIELDS,
  parseInputLimits,
  parseInputLimitValue,
  resolveInheritedInputLimit,
  setInputLimit,
} from "@/features/admin/model/file-input-limits";
import { SettingsFieldItem, SettingsFieldList, SettingsFieldRow, SettingsSection } from "@/shared/components/settings-layout";

type Props = {
  defaultValue: string;
  value: string;
  savedValue: string;
  loading: boolean;
  saving: boolean;
  onChange: (value: string) => void;
  onSave: () => void;
};

export function FilesInputLimits({ value, defaultValue, savedValue, loading, saving, onChange, onSave }: Props) {
  const t = useTranslations("adminFiles.inputLimits");
  const tActions = useTranslations("common.actions");
  const searchParams = useSearchParams();
  const requestedUpstream = searchParams.get("inputLimitsUpstream") ?? "";
  const scrollTarget = React.useRef<HTMLElement>(null);
  const scrolledLink = React.useRef<string | null>(null);
  const [upstream, setUpstream] = React.useState("");
  const { upstreams, loading: upstreamsLoading, error: upstreamsError, retry: retryUpstreams } = useAdminFilesInputLimitsUpstreams();
  const [drafts, setDrafts] = React.useState<Record<string, string>>({});
  const config = React.useMemo(() => parseInputLimits(value), [value]);
  const savedConfig = React.useMemo(() => parseInputLimits(savedValue), [savedValue]);
  const defaults = React.useMemo(() => parseInputLimits(defaultValue), [defaultValue]);
  const disabled = loading || saving;
  const scope = upstream || "defaults";
  const hasInvalidDraft = Object.entries(drafts).some(([key, raw]) => {
    const field = INPUT_LIMIT_FIELDS.find((item) => key.endsWith(`:${item.key}`));
    return field && parseInputLimitValue(raw, field.unit === "mb") === undefined;
  });

  React.useEffect(() => {
    setUpstream(/^[1-9]\d*$/.test(requestedUpstream) ? requestedUpstream : "");
  }, [requestedUpstream]);

  React.useEffect(() => {
    if (loading || upstreamsLoading || window.location.hash !== "#model-input-budgets") return;
    if (scrolledLink.current === requestedUpstream) return;
    const frame = window.requestAnimationFrame(() => {
      scrollTarget.current?.scrollIntoView({ block: "start" });
      scrolledLink.current = requestedUpstream;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [loading, upstreamsLoading, requestedUpstream]);

  React.useEffect(() => {
    // A successful save or reload establishes a new baseline for numeric drafts.
    void savedValue;
    setDrafts({});
  }, [savedValue]);

  return (
    <SettingsSection
      id="model-input-budgets"
      ref={scrollTarget}
      className="scroll-mt-4"
      title={t("title")}
      actions={value !== savedValue ? (
        <Button size="sm" type="button" disabled={disabled || !config || hasInvalidDraft} onClick={onSave}>
          {saving ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
          {tActions("save")}
        </Button>
      ) : null}
    >
      <SettingsFieldRow title={t("scope")} description={t("inheritanceDescription")}>
        <Select value={upstream || "defaults"} disabled={disabled || upstreamsLoading} onValueChange={(next) => setUpstream(next === "defaults" ? "" : next)}>
          <SelectTrigger id="input-limits-upstream" aria-label={t("scope")}><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="defaults">{t("systemDefault")}</SelectItem>
            {upstreams.map((item) => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}
          </SelectContent>
        </Select>
      </SettingsFieldRow>
      {upstreamsLoading ? <p className="text-[11px] text-muted-foreground" role="status">{t("loadingUpstreams")}</p> : null}
      {upstreamsError ? <div className="flex items-center gap-2 text-[11px] text-destructive" role="alert">{t("upstreamsFailed")}<Button size="sm" variant="ghost" onClick={retryUpstreams}>{t("retry")}</Button></div> : null}
      {loading ? <p className="text-xs text-muted-foreground" role="status">{t("loading")}</p> : !config ? (
        <p className="text-xs text-destructive" role="alert">{t("invalidConfig")}</p>
      ) : (
        <SettingsFieldList>
          {INPUT_LIMIT_FIELDS.map((field, index) => {
            const bytes = field.unit === "mb";
            const stored = upstream ? config.upstreams[upstream]?.[field.key] : config.defaults[field.key];
            const saved = upstream ? savedConfig?.upstreams[upstream]?.[field.key] : savedConfig?.defaults[field.key];
            const inherited = resolveInheritedInputLimit(config, defaults, upstream, field.key);
            const draftKey = `${scope}:${field.key}`;
            const raw = drafts[draftKey] ?? formatInputLimit(stored, bytes);
            const invalid = parseInputLimitValue(raw, bytes) === undefined;
            return (
              <SettingsFieldItem key={field.key} index={index} className={index > 0 && INPUT_LIMIT_FIELDS[index - 1].group !== field.group ? "mt-3 border-t border-border/60 pt-4 md:pt-5" : undefined}>
                <SettingsFieldRow title={t(`fields.${field.key}.label`)} description={t(`fields.${field.key}.description`)}>
                  <div className="w-full space-y-1">
                      <SettingsValueInput
                        id={`input-limit-${field.key}`}
                        aria-label={t(`fields.${field.key}.label`)}
                        value={raw}
                        inputMode={bytes ? "decimal" : "numeric"}
                        aria-invalid={invalid}
                        aria-describedby={invalid ? `input-limit-${field.key}-status` : undefined}
                        disabled={disabled}
                        placeholder={inherited === 0 ? t("unlimited") : formatInputLimit(inherited, bytes)}
                        suffix={t(`units.${field.unit}`)}
                        zeroDisplayLabel={t("unlimited")}
                        dirty={raw !== formatInputLimit(saved, bytes)}
                        className="tabular-nums"
                        onChange={(event) => {
                          const next = event.target.value;
                          setDrafts((current) => ({ ...current, [draftKey]: next }));
                          const parsed = parseInputLimitValue(next, bytes);
                          if (parsed !== undefined) onChange(JSON.stringify(setInputLimit(config, upstream, field.key, parsed)));
                        }}
                      />
                    {invalid ? <p id={`input-limit-${field.key}-status`} className="text-[10px] leading-4 text-destructive">
                      {t("invalidValue")}
                    </p> : null}
                  </div>
                </SettingsFieldRow>
              </SettingsFieldItem>
            );
          })}
        </SettingsFieldList>
      )}
      {hasInvalidDraft ? <p className="text-[11px] text-destructive" role="alert">{t("fixInvalidValues")}</p> : null}
    </SettingsSection>
  );
}
