"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import * as React from "react";
import { Button } from "@/components/ui/button";
import type { AdminLLMUpstreamView } from "@/features/admin/api/llm-types";
import { useAdminUpstreamsInputLimitsSummary } from "@/features/admin/hooks/use-admin-upstreams-input-limits-summary";
import { formatInputLimit, INPUT_LIMIT_FIELDS, resolveInputLimitValues } from "@/features/admin/model/file-input-limits";

export function UpstreamsInputLimitsSummary({ upstream, onNavigate }: { upstream: AdminLLMUpstreamView; onNavigate: () => void }) {
  const t = useTranslations("adminFiles.inputLimits");
  const { config, loading } = useAdminUpstreamsInputLimitsSummary(upstream.id);
  const effective = config ? resolveInputLimitValues(config, String(upstream.id)) : null;
  const href = `/admin/files?inputLimitsUpstream=${upstream.id}#model-input-budgets`;

  return (
    <div className="space-y-2 border-t border-border/60 pt-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium">{t("title")}</p>
        <Button asChild size="xs" variant="ghost"><Link href={href} onClick={onNavigate}>{t("openSettings")}</Link></Button>
      </div>
      {loading ? <p className="text-[11px] text-muted-foreground" role="status">{t("loading")}</p> : !effective ? (
        <p className="text-[11px] text-destructive" role="alert">{t("summaryFailed")}</p>
      ) : (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 text-[11px]">
          {INPUT_LIMIT_FIELDS.filter(({ key }) => key === "requestBytes" || (effective[key] ?? 0) > 0).map((field) => {
            const key = field.key;
            const value = effective[key];
            const formatted = value == null ? t("systemDefault") : value === 0 ? t("unlimited") : `${formatInputLimit(value, field.unit === "mb")} ${t(`units.${field.unit}`)}`;
            return <React.Fragment key={key}><dt className="text-muted-foreground">{t(`fields.${key}.label`)}</dt><dd className="text-right tabular-nums">{formatted}</dd></React.Fragment>;
          })}
        </dl>
      )}
    </div>
  );
}
