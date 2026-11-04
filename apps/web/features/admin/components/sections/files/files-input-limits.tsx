"use client";

import { Loader2, Save } from "lucide-react";
import { useTranslations } from "next-intl";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAdminFilesInputLimitsUpstreams } from "@/features/admin/hooks/use-admin-files-input-limits-upstreams";
import {
  formatInputLimit,
  INPUT_LIMIT_FIELDS,
  INPUT_LIMIT_PROFILES,
  INPUT_LIMIT_PROTOCOLS,
  type InputLimitProfile,
  parseInputLimits,
  parseInputLimitValue,
  resolveInputLimitProfile,
  resolveInheritedInputLimit,
  setInputLimit,
} from "@/features/admin/model/file-input-limits";
import { resolveProtocolLabel } from "@/features/admin/utils/llm-display";
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
  const [profile, setProfile] = React.useState<InputLimitProfile>("openai");
  const [upstream, setUpstream] = React.useState("");
  const [protocol, setProtocol] = React.useState("");
  const { upstreams, loading: upstreamsLoading, error: upstreamsError, retry: retryUpstreams } = useAdminFilesInputLimitsUpstreams();
  const [drafts, setDrafts] = React.useState<Record<string, string>>({});
  const config = React.useMemo(() => parseInputLimits(value), [value]);
  const defaults = React.useMemo(() => parseInputLimits(defaultValue), [defaultValue]);
  const disabled = loading || saving;
  const selectedUpstream = upstreams.find((item) => String(item.id) === upstream);
  const selectedProfile = upstream && protocol ? resolveInputLimitProfile(protocol, selectedUpstream?.baseURL ?? "") : profile;
  const scope = upstream ? `upstream:${upstream}:${protocol}` : `profile:${profile}`;
  const hasInvalidDraft = Object.entries(drafts).some(([key, raw]) => {
    const field = INPUT_LIMIT_FIELDS.find((item) => key.endsWith(`:${item.key}`));
    return field && parseInputLimitValue(raw, field.unit === "mb") === undefined;
  });

  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const requestedUpstream = params.get("inputLimitsUpstream");
    if (requestedUpstream && /^[1-9]\d*$/.test(requestedUpstream)) setUpstream(requestedUpstream);
    const requestedProtocol = params.get("inputLimitsProtocol");
    if (INPUT_LIMIT_PROTOCOLS.some((item) => item === requestedProtocol)) setProtocol(requestedProtocol ?? "");
  }, []);

  React.useEffect(() => {
    // A successful save or reload establishes a new baseline for numeric drafts.
    void savedValue;
    setDrafts({});
  }, [savedValue]);

  return (
    <SettingsSection
      id="model-input-budgets"
      title={t("title")}
      actions={value !== savedValue ? (
        <Button size="sm" type="button" disabled={disabled || !config || hasInvalidDraft} onClick={onSave}>
          {saving ? <Loader2 className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
          {tActions("save")}
        </Button>
      ) : null}
    >
      <p className="text-[11px] leading-5 text-muted-foreground">{t("inheritanceDescription")}</p>
      <div className="grid min-w-0 gap-3 sm:grid-cols-2">
        <div className="min-w-0 space-y-1.5">
          <label htmlFor="input-limits-upstream" className="text-xs font-medium">{t("scope")}</label>
          <Select value={upstream || "profiles"} disabled={disabled || upstreamsLoading} onValueChange={(next) => {
            setUpstream(next === "profiles" ? "" : next);
            setProtocol("");
          }}>
            <SelectTrigger id="input-limits-upstream"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="profiles">{t("profileDefaults")}</SelectItem>
              {upstreams.map((item) => <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="min-w-0 space-y-1.5">
          <label htmlFor="input-limits-profile" className="text-xs font-medium">{t(upstream ? "inheritanceProfile" : "profile")}</label>
          <Select value={selectedProfile} disabled={disabled || Boolean(upstream && protocol)} onValueChange={(next) => {
            const selected = INPUT_LIMIT_PROFILES.find((item) => item === next);
            if (selected) setProfile(selected);
          }}>
            <SelectTrigger id="input-limits-profile"><SelectValue /></SelectTrigger>
            <SelectContent>{INPUT_LIMIT_PROFILES.map((item) => <SelectItem key={item} value={item}>{t(`profiles.${item}`)}</SelectItem>)}</SelectContent>
          </Select>
        </div>
      </div>
      {upstream ? (
        <SettingsFieldRow title={t("protocolScope")} description={t("protocolDescription")}>
          <Select value={protocol || "all"} disabled={disabled} onValueChange={(next) => setProtocol(next === "all" ? "" : next)}>
            <SelectTrigger aria-label={t("protocolScope")}><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("allProtocols")}</SelectItem>
              {INPUT_LIMIT_PROTOCOLS.map((item) => <SelectItem key={item} value={item}>{resolveProtocolLabel(item)}</SelectItem>)}
            </SelectContent>
          </Select>
        </SettingsFieldRow>
      ) : null}
      {upstreamsLoading ? <p className="text-[11px] text-muted-foreground" role="status">{t("loadingUpstreams")}</p> : null}
      {upstreamsError ? <div className="flex items-center gap-2 text-[11px] text-destructive" role="alert">{t("upstreamsFailed")}<Button size="sm" variant="ghost" onClick={retryUpstreams}>{t("retry")}</Button></div> : null}
      {upstream ? <p className="text-[11px] leading-5 text-muted-foreground">{t("overrideDescription")}</p> : null}
      {loading ? <p className="text-xs text-muted-foreground" role="status">{t("loading")}</p> : !config ? (
        <p className="text-xs text-destructive" role="alert">{t("invalidConfig")}</p>
      ) : (
        <SettingsFieldList>
          {INPUT_LIMIT_FIELDS.map((field, index) => {
            const bytes = field.unit === "mb";
            const base = config.profiles[selectedProfile]?.[field.key];
            const stored = upstream ? protocol ? config.upstreams[upstream]?.protocols?.[protocol]?.[field.key] : config.upstreams[upstream]?.[field.key] : base;
            const inherited = resolveInheritedInputLimit(config, defaults, selectedProfile, upstream, field.key, protocol);
            const draftKey = `${scope}:${field.key}`;
            const raw = drafts[draftKey] ?? formatInputLimit(stored, bytes);
            const invalid = parseInputLimitValue(raw, bytes) === undefined;
            return (
              <SettingsFieldItem key={field.key} index={index} className={index > 0 && INPUT_LIMIT_FIELDS[index - 1].group !== field.group ? "mt-3 border-t border-border/60 pt-4 md:pt-5" : undefined}>
                <SettingsFieldRow title={t(`fields.${field.key}.label`)} description={t(`fields.${field.key}.description`)}>
                  <div className="w-full space-y-1">
                    <div className="flex items-center gap-2">
                      <Input
                        id={`input-limit-${field.key}`}
                        aria-label={t(`fields.${field.key}.label`)}
                        value={raw}
                        inputMode={bytes ? "decimal" : "numeric"}
                        aria-invalid={invalid}
                        aria-describedby={invalid ? `input-limit-${field.key}-status` : undefined}
                        disabled={disabled}
                        placeholder={formatInputLimit(inherited, bytes)}
                        className="tabular-nums"
                        onChange={(event) => {
                          const next = event.target.value;
                          setDrafts((current) => ({ ...current, [draftKey]: next }));
                          const parsed = parseInputLimitValue(next, bytes);
                          if (parsed !== undefined) onChange(JSON.stringify(setInputLimit(config, selectedProfile, upstream, field.key, parsed, protocol)));
                        }}
                      />
                      <span className="w-7 shrink-0 text-[11px] text-muted-foreground">{t(`units.${field.unit}`)}</span>
                    </div>
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
