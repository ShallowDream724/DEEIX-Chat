import * as React from "react";
import { listAdminSettings } from "@/features/admin/api";
import { type InputLimitsConfig, parseInputLimits } from "@/features/admin/model/file-input-limits";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";

export function useAdminUpstreamsInputLimitsSummary(upstreamID: number) {
  const [config, setConfig] = React.useState<InputLimitsConfig | null>(null);
  const [loading, setLoading] = React.useState(true);
  React.useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoading(true);
      try {
        const token = await resolveAccessToken();
        if (!token) throw new Error("Session expired");
        const settings = await listAdminSettings(token);
        const value = settings.file?.find((item) => item.key === "input_limits")?.value ?? "";
        if (!cancelled) setConfig(parseInputLimits(value));
      } catch {
        if (!cancelled) setConfig(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    void upstreamID;
    void load();
    return () => { cancelled = true; };
  }, [upstreamID]);
  return { config, loading };
}
