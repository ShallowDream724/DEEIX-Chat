"use client";

import { RefreshCw, Save } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { SettingsFieldEditor } from "@/features/admin/components/shared/settings-runtime-panel";
import { useAdminSpeechSettings } from "@/features/admin/hooks/use-admin-speech-settings";
import { SPEECH_INPUT_PROVIDERS, SPEECH_OUTPUT_PROVIDERS, SPEECH_SETTINGS_FIELDS, isSpeechSettingsFieldVisible, speechCredentialsSourceOptions, type SpeechSettingsField } from "@/features/admin/model/speech-settings";
import { SettingsFieldInset, SettingsFieldItem, SettingsFieldList, SettingsFieldRow, SettingsPage, SettingsSection, SettingsSectionSeparator } from "@/shared/components/settings-layout";

const GROUPS = ["input", "output", "pricing"] as const;

function fieldBlocks(fields: readonly SpeechSettingsField[]) {
  const blocks: { key: string; inset: boolean; fields: SpeechSettingsField[] }[] = [];
  for (const field of fields) {
    const key = field.subgroupKey ?? field.key;
    const previous = blocks.at(-1);
    if (field.subgroupKey && previous?.key === key) previous.fields.push(field);
    else blocks.push({ key, inset: Boolean(field.subgroupKey), fields: [field] });
  }
  return blocks;
}

export function AdminSpeechPage() {
  const t = useTranslations("adminSpeech");
  const tActions = useTranslations("common.actions");
  const settings = useAdminSpeechSettings();
  const disabled = settings.loading || settings.saving || Boolean(settings.error);
  const groups = GROUPS.map((group) => ({ group, blocks: fieldBlocks(SPEECH_SETTINGS_FIELDS.filter((field) => {
    if (field.group !== group || !isSpeechSettingsFieldVisible(field.key, settings.values)) return false;
    return !field.key.endsWith("_credentials_source") || !field.subgroupKey || speechCredentialsSourceOptions(field.subgroupKey, settings.values, settings.items).length > 1;
  })) })).filter(({ blocks }) => blocks.length > 0);

  const renderEditor = (field: SpeechSettingsField) => {
    const credentialSource = field.key.endsWith("_credentials_source") && field.subgroupKey;
    const providerOptions = field.key === "input_provider" ? SPEECH_INPUT_PROVIDERS : field.key === "tts_provider" ? SPEECH_OUTPUT_PROVIDERS : undefined;
    const options = credentialSource
      ? speechCredentialsSourceOptions(credentialSource, settings.values, settings.items).map((value) => ({ value, label: t(`connectionSources.${value}`) }))
      : providerOptions?.map((value) => ({ value, label: t(`providers.${value}`) }));
    const missing = settings.missingConnectionFields.has(field.key) && !settings.loading;
    return <SettingsFieldEditor
      key={field.key}
      field={{ id: `speech.${field.key}`, type: field.type, label: t(`fields.${field.key}.label`), description: t(`fields.${field.key}.description`), options, valueSuffix: field.key === "max_recording_seconds" ? t("seconds") : undefined }}
      value={settings.values[field.key] ?? ""}
      configured={settings.items.some((item) => item.key === field.key && item.configured)}
      dirty={settings.dirtyKeys.has(field.key)}
      disabled={disabled}
      afterControl={missing ? <p role="alert" className="text-xs text-destructive">{t(credentialSource ? "connectionRequired" : field.type === "password" ? "apiKeyRequired" : "baseURLRequired")}</p> : undefined}
      onChange={(value) => settings.setValue(field.key, value)}
    />;
  };

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
      {groups.map(({ group, blocks }, groupIndex) => (
        <div key={group}>
          {groupIndex > 0 ? <SettingsSectionSeparator /> : null}
          <SettingsSection
            title={t(`sections.${group}`)}
            actions={settings.groupSaves[group].patches.length > 0 ? (
              <Button type="button" size="sm" disabled={disabled || settings.groupSaves[group].missingConnectionFields.size > 0} onClick={() => void settings.save(group)}>
                <Save className="size-3.5" />
                {settings.savingGroup === group ? tActions("saving") : tActions("save")}
              </Button>
            ) : undefined}
          >
            <SettingsFieldList>
              {group === "pricing" && settings.values.input_provider === "browser" ? <SettingsFieldRow title={t("fields.charge_asr.label")} description={t("browserFree")}><span className="text-xs text-muted-foreground">{t("free")}</span></SettingsFieldRow> : null}
              <AnimatePresence initial={false}>
                {blocks.map((block, index) => {
                  const itemIndex = index + (group === "pricing" && settings.values.input_provider === "browser" ? 1 : 0);
                  if (!block.inset) return <SettingsFieldItem key={block.key} index={itemIndex}>{block.fields.map(renderEditor)}</SettingsFieldItem>;
                  return <motion.div
                    key={block.key}
                    className="min-w-0"
                    initial={{ opacity: 0, gridTemplateRows: "0fr" }}
                    animate={{ opacity: 1, gridTemplateRows: "1fr" }}
                    exit={{ opacity: 0, gridTemplateRows: "0fr" }}
                    transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
                    style={{ display: "grid" }}
                  >
                    <div className="-mx-px overflow-hidden px-px pb-px">
                      <SettingsFieldItem index={itemIndex}>
                        <SettingsFieldInset>
                          <SettingsFieldList className="gap-3 md:gap-4">{block.fields.map(renderEditor)}</SettingsFieldList>
                        </SettingsFieldInset>
                      </SettingsFieldItem>
                    </div>
                  </motion.div>;
                })}
              </AnimatePresence>
            </SettingsFieldList>
          </SettingsSection>
        </div>
      ))}
    </SettingsPage>
  );
}
