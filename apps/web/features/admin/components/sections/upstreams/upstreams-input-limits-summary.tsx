"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import * as React from "react";
import { Button } from "@/components/ui/button";
import type { AdminLLMUpstreamView } from "@/features/admin/api/llm-types";
import { useAdminUpstreamsInputLimitsSummary } from "@/features/admin/hooks/use-admin-upstreams-input-limits-summary";
import { formatInputLimit, INPUT_LIMIT_FIELDS, resolveInputLimitProfile, resolveInputLimitValues, resolveUpstreamInputLimitProtocol } from "@/features/admin/model/file-input-limits";
import { resolveProtocolLabel } from "@/features/admin/utils/llm-display";

const SUMMARY_FIELDS = ["requestBytes", "imageCount", "nativeBytes", "pdfSingleBytes", "pdfPages"] as const;

export function UpstreamsInputLimitsSummary({ upstream, onNavigate }: { upstream: AdminLLMUpstreamView; onNavigate: () => void }) {
  const t = useTranslations("adminFiles.inputLimits");
  const { config, loading } = useAdminUpstreamsInputLimitsSummary(upstream.id);
  const protocol = resolveUpstreamInputLimitProtocol(upstream);
  const profile = resolveInputLimitProfile(protocol, upstream.baseURL);

  const effective = config ? resolveInputLimitValues(config, profile, String(upstream.id), protocol) : null;
  const href = `/admin/files?inputLimitsUpstream=${upstream.id}&inputLimitsProtocol=${protocol}#model-input-budgets`;

  return (
    <div className="space-y-2 border-t border-border/60 pt-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium">{t("title")}</p>
        <Button asChild size="xs" variant="ghost"><Link href={href} onClick={onNavigate}>{t("openSettings")}</Link></Button>
      </div>
      <p className="text-[11px] leading-5 text-muted-foreground">{t("summaryScope", { protocol: resolveProtocolLabel(protocol) })}</p>
      {loading ? <p className="text-[11px] text-muted-foreground" role="status">{t("loading")}</p> : !effective ? (
        <p className="text-[11px] text-destructive" role="alert">{t("summaryFailed")}</p>
      ) : (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 text-[11px]">
          {SUMMARY_FIELDS.map((key) => {
            const field = INPUT_LIMIT_FIELDS.find((item) => item.key === key);
            const value = effective[key];
            const source = config?.upstreams[String(upstream.id)]?.protocols?.[protocol]?.[key] != null ? "protocolOverride" : config?.upstreams[String(upstream.id)]?.[key] != null ? "upstreamOverride" : "channelDefault";
            const formatted = value == null ? t("systemDefault") : value === 0 ? t("noExtraLimit") : `${formatInputLimit(value, field?.unit === "mb")} ${t(`units.${field?.unit ?? "count"}`)}`;
            return <React.Fragment key={key}><dt className="text-muted-foreground">{t(`fields.${key}.label`)}</dt><dd className="text-right tabular-nums" title={t(source)}>{formatted}</dd></React.Fragment>;
          })}
        </dl>
      )}
    </div>
  );
}
