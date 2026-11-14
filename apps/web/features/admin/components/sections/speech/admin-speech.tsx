"use client";

import { RefreshCw, Save } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { SettingsFieldEditor } from "@/features/admin/components/shared/settings-runtime-panel";
import { useAdminSpeechSettings } from "@/features/admin/hooks/use-admin-speech-settings";
import { SPEECH_SETTINGS_FIELDS } from "@/features/admin/model/speech-settings";
import { SettingsFieldItem, SettingsFieldList, SettingsPage, SettingsSection, SettingsSectionSeparator } from "@/shared/components/settings-layout";

const GROUPS = ["general", "input", "deepseek", "pricing"] as const;

export function AdminSpeechPage() {
  const t = useTranslations("adminSpeech");
  const tActions = useTranslations("common.actions");
  const settings = useAdminSpeechSettings();
  const disabled = settings.loading || settings.saving || Boolean(settings.error);

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
      {GROUPS.map((group, groupIndex) => (
        <div key={group}>
          {groupIndex > 0 ? <SettingsSectionSeparator /> : null}
          <SettingsSection
            title={t(`sections.${group}`)}
            actions={groupIndex === 0 ? (
              <Button size="sm" className="h-7 gap-1 px-2 text-xs" disabled={disabled || settings.dirtyKeys.size === 0} onClick={() => void settings.save()}>
                <Save className="size-3.5 stroke-1" />
                {settings.saving ? tActions("saving") : tActions("save")}
              </Button>
            ) : undefined}
          >
            <SettingsFieldList>
              {SPEECH_SETTINGS_FIELDS.filter((field) => field.group === group).map((field, index) => (
                <SettingsFieldItem key={field.key} index={index}>
                  <SettingsFieldEditor
                    field={{ id: `speech.${field.key}`, type: field.type, label: t(`fields.${field.key}.label`), description: t(`fields.${field.key}.description`) }}
                    value={settings.values[field.key] ?? ""}
                    configured={settings.items.some((item) => item.key === field.key && item.configured)}
                    dirty={settings.dirtyKeys.has(field.key)}
                    disabled={disabled}
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
