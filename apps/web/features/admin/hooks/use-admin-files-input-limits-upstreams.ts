import * as React from "react";
import { listAdminLLMUpstreams } from "@/features/admin/api";
import type { AdminLLMUpstreamView } from "@/features/admin/api/llm-types";
import { resolveAccessToken } from "@/shared/auth/resolve-access-token";

export function useAdminFilesInputLimitsUpstreams() {
  const [upstreams, setUpstreams] = React.useState<AdminLLMUpstreamView[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState(false);
  const [loadVersion, setLoadVersion] = React.useState(0);

  React.useEffect(() => {
    let cancelled = false;
    async function load() {
      setLoading(true);
      setError(false);
      try {
        const token = await resolveAccessToken();
        if (!token) throw new Error("Session expired");
        const items: AdminLLMUpstreamView[] = [];
        for (let page = 1; ; page += 1) {
          const result = await listAdminLLMUpstreams(token, { page, pageSize: 100, sort: "name_asc" });
          if (cancelled) return;
          items.push(...result.results);
          if (items.length >= result.total || result.results.length === 0) break;
        }
        if (!cancelled) setUpstreams(items);
      } catch {
        if (!cancelled) setError(true);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    void loadVersion;
    void load();
    return () => { cancelled = true; };
  }, [loadVersion]);

  return { upstreams, loading, error, retry: () => setLoadVersion((current) => current + 1) };
}
