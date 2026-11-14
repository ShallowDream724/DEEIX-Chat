"use client";

import * as React from "react";
import { Save } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { loadUserSettingsSnapshot, updateUserSettings, useUserSettings } from "@/entities/user-settings";
import { getSelectedSpeechTTSCapability, parseSpeechPreferences, resolveSpeechPreferences } from "@/features/chat";
import { buildSpeechPreferencesUpdate } from "@/features/settings/utils/speech-preferences";
import { useLocalizedErrorMessage } from "@/i18n/use-localized-error";
import { useAuthSession } from "@/shared/auth/auth-session-context";
import { SettingsFieldList, SettingsFieldRow, SettingsSection } from "@/shared/components/settings-layout";
import { useSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";

export function ChatSpeechSettings({
  autoReadReplies,
  loading,
  onAutoReadChange,
}: {
  autoReadReplies: boolean;
  loading: boolean;
  onAutoReadChange: (enabled: boolean) => void;
}) {
  const t = useTranslations("settings.chatPage.speech");
  const tActions = useTranslations("common.actions");
  const tToasts = useTranslations("settings.chatPage.toasts");
  const translateError = useLocalizedErrorMessage();
  const { accessToken } = useAuthSession();
  const userSettings = useUserSettings();
  const capability = getSelectedSpeechTTSCapability(useSpeechCapabilities());
  const [saving, setSaving] = React.useState(false);
  const [styleDraft, setStyleDraft] = React.useState<{ provider: string; value: string } | null>(null);
  const styleID = React.useId();
  const preference = capability ? resolveSpeechPreferences(capability, userSettings.settings) : undefined;
  const overrides = parseSpeechPreferences(userSettings.settings["chat.speech_preferences"]);
  const savedStyle = preference?.style ?? "";
  const style = styleDraft && styleDraft.provider === capability?.id ? styleDraft.value : savedStyle;
  const styleDirty = style !== savedStyle;
  const disabled = loading || !userSettings.loaded || saving;

  const savePreference = async (changes: { voice?: string; speed?: string; style?: string }) => {
    if (!capability || disabled) return false;
    setSaving(true);
    try {
      const latest = await loadUserSettingsSnapshot(accessToken);
      await updateUserSettings(accessToken, {
        "chat.speech_preferences": buildSpeechPreferencesUpdate(parseSpeechPreferences(latest["chat.speech_preferences"]), capability.id, changes),
      });
      return true;
    } catch (error) {
      toast.error(tToasts("saveFailed"), { description: translateError(error, tToasts("retryLater")) });
      return false;
    } finally {
      setSaving(false);
    }
  };

  const saveStyle = async (value: string | undefined) => {
    if (await savePreference({ style: value })) setStyleDraft(null);
  };

  return (
    <SettingsSection
      title={t("sectionTitle")}
      actions={capability?.supports_style && styleDirty ? (
        <Button type="button" size="sm" disabled={disabled} onClick={() => void saveStyle(style)}>
          <Save className="size-3.5" />
          {saving ? tActions("saving") : tActions("save")}
        </Button>
      ) : undefined}
    >
      <SettingsFieldList>
        <SettingsFieldRow title={t("autoReadTitle")} description={capability ? t("autoReadDescription") : t("unavailable")}>
          <Switch checked={autoReadReplies} onCheckedChange={onAutoReadChange} disabled={loading || !capability} aria-label={t("autoReadTitle")} />
        </SettingsFieldRow>
        {capability?.voices.length ? <div className="pt-4">
          <SettingsFieldRow title={t("voiceTitle")} description={t("voiceDescription")}>
            <Select value={preference?.voice} onValueChange={(voice) => void savePreference({ voice })} disabled={disabled}>
              <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
              <SelectContent>{capability.voices.map((voice) => <SelectItem key={voice.id} value={voice.id}>{voice.name}</SelectItem>)}</SelectContent>
            </Select>
          </SettingsFieldRow>
        </div> : null}
        {capability?.speeds?.length ? <div className="pt-4">
          <SettingsFieldRow title={t("speedTitle")}>
            <Select value={preference?.speed} onValueChange={(speed) => void savePreference({ speed })} disabled={disabled}>
              <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
              <SelectContent>{capability.speeds.map((speed) => <SelectItem key={speed.id} value={speed.id}>{["slow", "normal", "fast"].includes(speed.id) ? t(`speeds.${speed.id}`) : speed.name}</SelectItem>)}</SelectContent>
            </Select>
          </SettingsFieldRow>
        </div> : null}
        {capability?.supports_style ? <Field className="gap-2 pt-4">
          <div className="flex items-center justify-between gap-3">
            <FieldLabel htmlFor={styleID} className="mb-0">{t("styleTitle")}</FieldLabel>
            <Button type="button" variant="ghost" size="sm" className="h-7 shrink-0 px-2 text-xs" disabled={disabled || !styleDirty && overrides[capability.id]?.style === undefined} onClick={() => void saveStyle(undefined)}>
              {t("restoreDefault")}
            </Button>
          </div>
          <Textarea
            id={styleID}
            aria-label={t("styleTitle")}
            value={style}
            disabled={disabled}
            rows={3}
            className="min-h-20 resize-y text-xs"
            onChange={(event) => setStyleDraft({ provider: capability.id, value: Array.from(event.target.value).slice(0, capability.max_style_characters ?? 500).join("") })}
          />
        </Field> : null}
      </SettingsFieldList>
    </SettingsSection>
  );
}
