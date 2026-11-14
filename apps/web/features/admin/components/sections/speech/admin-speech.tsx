"use client";

import { RefreshCw, Save } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { SettingsFieldEditor } from "@/features/admin/components/shared/settings-runtime-panel";
import { useAdminSpeechSettings } from "@/features/admin/hooks/use-admin-speech-settings";
import { SPEECH_INPUT_PROVIDERS, SPEECH_SETTINGS_FIELDS, isSpeechSettingsFieldVisible } from "@/features/admin/model/speech-settings";
import { SettingsFieldItem, SettingsFieldList, SettingsFieldRow, SettingsPage, SettingsSection, SettingsSectionSeparator } from "@/shared/components/settings-layout";

const GROUPS = ["input", "output", "connection", "pricing"] as const;

export function AdminSpeechPage() {
  const t = useTranslations("adminSpeech");
  const tActions = useTranslations("common.actions");
  const settings = useAdminSpeechSettings();
  const disabled = settings.loading || settings.saving || Boolean(settings.error);
  const groups = GROUPS.map((group) => ({ group, fields: SPEECH_SETTINGS_FIELDS.filter((field) => field.group === group && isSpeechSettingsFieldVisible(field.key, settings.values)) })).filter(({ fields }) => fields.length > 0);

  return (
    <SettingsPage>
      {settings.error ? (
        <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border px-3 py-2">
          <p className="text-xs text-destructive">{settings.error}</p>
          <Button size="sm" variant="outline" className="h-7 gap-1 px-2 text-xs" onClick={() => void settings.load()}>
            <RefreshCw className="size-3.5 stroke-1" />
            {tActions("retry")}
          </Button>
        </div>
      ) : null}
      {groups.map(({ group, fields }, groupIndex) => (
        <div key={group}>
          {groupIndex > 0 ? <SettingsSectionSeparator /> : null}
          <SettingsSection
            title={t(`sections.${group}`)}
            actions={groupIndex === 0 ? (
              <Button size="sm" className="h-7 gap-1 px-2 text-xs" disabled={disabled || settings.dirtyKeys.size === 0 || settings.missingAPIKey} onClick={() => void settings.save()}>
                <Save className="size-3.5 stroke-1" />
                {settings.saving ? tActions("saving") : tActions("save")}
              </Button>
            ) : undefined}
          >
            <SettingsFieldList>
              {group === "pricing" && settings.values.input_provider === "browser" ? <SettingsFieldRow title={t("fields.charge_asr.label")} description={t("browserFree")}><span className="text-xs text-muted-foreground">{t("free")}</span></SettingsFieldRow> : null}
              {fields.map((field, index) => (
                <SettingsFieldItem key={field.key} index={index + (group === "pricing" && settings.values.input_provider === "browser" ? 1 : 0)}>
                  <SettingsFieldEditor
                    field={{ id: `speech.${field.key}`, type: field.type, label: t(`fields.${field.key}.label`), description: t(`fields.${field.key}.description`), options: field.key === "input_provider" ? SPEECH_INPUT_PROVIDERS.map((value) => ({ value, label: t(`providers.${value}`) })) : undefined }}
                    value={settings.values[field.key] ?? ""}
                    configured={settings.items.some((item) => item.key === field.key && item.configured)}
                    dirty={settings.dirtyKeys.has(field.key)}
                    disabled={disabled}
                    afterControl={field.key === "deepseek_api_key" && settings.missingAPIKey && !settings.loading ? <p role="alert" className="text-xs text-destructive">{t("apiKeyRequired")}</p> : undefined}
                    onChange={(value) => settings.setValues((current) => ({ ...current, [field.key]: value }))}
                  />
                </SettingsFieldItem>
              ))}
            </SettingsFieldList>
          </SettingsSection>
        </div>
      ))}
    </SettingsPage>
  );
}
