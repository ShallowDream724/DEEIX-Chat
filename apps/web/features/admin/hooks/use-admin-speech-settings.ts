import * as React from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { listAdminSettingsByNamespace, patchAdminSettings } from "@/features/admin/api/settings";
import { buildSpeechSettingsPatch, changeSpeechSetting, flattenSpeechSettings, speechSettingsFromResponse, speechSettingsMissingConnectionFields } from "@/features/admin/model/speech-settings";
import { resolveAdminErrorMessage } from "@/features/admin/utils/admin-error";
import type { SettingItem } from "@/shared/api/settings-types";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";
import { invalidateSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";

export function useAdminSpeechSettings() {
  const t = useTranslations("adminSpeech");
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
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
  const invalidConnection = missingConnectionFields.size > 0;
  const setValue = React.useCallback((key: string, value: string) => {
    if (key.endsWith("_credentials_source")) explicitSources.current.add(key);
    setValues((current) => changeSpeechSetting(current, key, value, items, explicitSources.current));
  }, [items]);
  const dirtyKeys = React.useMemo(() => new Set(patches.map((item) => item.key)), [patches]);
  const save = React.useCallback(async () => {
    if (!patches.length || saving || loading || error || invalidConnection) return;
    setSaving(true);
    try {
      const token = await resolveAccessToken();
      if (!token) throw new Error(t("sessionExpired"));
      applyItems(speechSettingsFromResponse(await patchAdminSettings(token, { items: patches })));
      invalidateSpeechCapabilities();
      toast.success(t("saved"));
    } catch (cause) {
      toast.error(t("saveFailed"), { description: resolveAdminErrorMessage(cause, t("unknownError")) });
    } finally {
      setSaving(false);
    }
  }, [applyItems, patches, saving, loading, error, invalidConnection, t]);

  return { loading, saving, error, items, values, setValue, dirtyKeys, invalidConnection, missingConnectionFields, load, save };
}
