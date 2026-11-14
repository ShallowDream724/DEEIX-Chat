import * as React from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { listAdminSettingsByNamespace, patchAdminSettings } from "@/features/admin/api/settings";
import { buildSpeechSettingsGroupSave, buildSpeechSettingsPatch, changeSpeechSetting, flattenSpeechSettings, mergeSpeechSettingsDraft, speechSettingsFromResponse, speechSettingsMissingConnectionFields, type SpeechSettingsGroup } from "@/features/admin/model/speech-settings";
import { resolveAdminErrorMessage } from "@/features/admin/utils/admin-error";
import type { SettingItem } from "@/shared/api/settings-types";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { invalidateSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";

export function useAdminSpeechSettings() {
  const t = useTranslations("adminSpeech");
  const [loading, setLoading] = React.useState(true);
  const [savingGroup, setSavingGroup] = React.useState<SpeechSettingsGroup | null>(null);
  const saving = savingGroup !== null;
  const [error, setError] = React.useState("");
  const [items, setItems] = React.useState<SettingItem[]>([]);
  const [values, setValues] = React.useState(() => flattenSpeechSettings([]));
  const [saved, setSaved] = React.useState(() => flattenSpeechSettings([]));
  const explicitSources = React.useRef(new Set<string>());
  const applyItems = React.useCallback((nextItems: SettingItem[]) => {
    explicitSources.current.clear();
    setItems(nextItems);
    const next = flattenSpeechSettings(nextItems);
    setValues(next);
    setSaved(next);
  }, []);

  const load = React.useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const token = await resolveAccessToken();
      if (!token) throw new Error(t("sessionExpired"));
      applyItems(await listAdminSettingsByNamespace(token, "speech"));
    } catch (cause) {
      setError(resolveAdminErrorMessage(cause, t("loadFailed")));
    } finally {
      setLoading(false);
    }
  }, [applyItems, t]);
  React.useEffect(() => { void load(); }, [load]);

  const patches = React.useMemo(() => buildSpeechSettingsPatch(values, saved), [values, saved]);
  const missingConnectionFields = speechSettingsMissingConnectionFields(values, items);
  const groupSaves = React.useMemo(() => ({
    input: buildSpeechSettingsGroupSave(values, saved, items, "input"),
    output: buildSpeechSettingsGroupSave(values, saved, items, "output"),
    pricing: buildSpeechSettingsGroupSave(values, saved, items, "pricing"),
  }), [values, saved, items]);
  const setValue = React.useCallback((key: string, value: string) => {
    if (key.endsWith("_credentials_source")) explicitSources.current.add(key);
    setValues((current) => changeSpeechSetting(current, key, value, items, explicitSources.current));
  }, [items]);
  const dirtyKeys = React.useMemo(() => new Set(patches.map((item) => item.key)), [patches]);
  const save = React.useCallback(async (group: SpeechSettingsGroup) => {
    const { patches: groupPatches, missingConnectionFields: missing } = groupSaves[group];
    if (!groupPatches.length || saving || loading || error || missing.size > 0) return;
    setSavingGroup(group);
    try {
      const token = await resolveAccessToken();
      if (!token) throw new Error(t("sessionExpired"));
      const nextItems = speechSettingsFromResponse(await patchAdminSettings(token, { items: groupPatches }));
      const nextSaved = flattenSpeechSettings(nextItems);
      setItems(nextItems);
      setSaved(nextSaved);
      setValues((current) => mergeSpeechSettingsDraft(current, saved, groupPatches, nextSaved));
      for (const patch of groupPatches) explicitSources.current.delete(patch.key);
      invalidateSpeechCapabilities();
      toast.success(t("saved"));
    } catch (cause) {
      toast.error(t("saveFailed"), { description: resolveAdminErrorMessage(cause, t("unknownError")) });
    } finally {
      setSavingGroup(null);
    }
  }, [groupSaves, saved, saving, loading, error, t]);

  return { loading, saving, savingGroup, error, items, values, setValue, dirtyKeys, groupSaves, missingConnectionFields, load, save };
}
