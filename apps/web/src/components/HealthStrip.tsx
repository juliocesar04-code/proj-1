import {
  Activity,
  Gauge,
  ShieldCheck,
  Siren,
  Timer,
} from "lucide-react";
import type { Overview, TraceSummary } from "../api.js";
import { useI18n } from "../i18n.js";

interface HealthStripProps {
  overview: Overview;
  traces: TraceSummary[];
  onOpenReport: () => void;
}

function sparklinePath(values: number[]) {
  if (values.length < 2) return "";
  const max = Math.max(...values, 1);
  const min = Math.min(...values);
  const range = Math.max(1, max - min);

  return values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * 120;
      const y = 32 - ((value - min) / range) * 28;
      return (index === 0 ? "M" : "L") + x.toFixed(1) + " " + y.toFixed(1);
    })
    .join(" ");
}

export function HealthStrip({
  overview,
  traces,
  onOpenReport,
}: HealthStripProps) {
  const { t, formatNumber } = useI18n();
  const availability = Math.max(
    0,
    100 - overview.totals.errorRate * 100,
  );
  const burnRate = overview.totals.errorRate / 0.01;
  const topP95 = overview.services[0]?.p95Ms ?? 0;
  const severity =
    overview.totals.errorRate >= 0.08 || topP95 >= 1800
      ? "critical"
      : overview.totals.errorRate >= 0.03 || topP95 >= 700
        ? "degraded"
        : "healthy";

  const trend = traces
    .slice(0, 18)
    .reverse()
    .map((trace) => trace.durationMs);

  return (
    <section className="health-strip" aria-label={t("ops.health")}>
      <div className={"health-strip__state health-strip__state--" + severity}>
        <span className="health-strip__pulse" />
        <div>
          <small>{t("ops.status")}</small>
          <strong>{t("ops." + severity)}</strong>
        </div>
      </div>

      <div className="health-strip__metric">
        <ShieldCheck size={16} />
        <div>
          <small>{t("ops.availability")}</small>
          <strong>
            {formatNumber(availability, {
              minimumFractionDigits: 2,
              maximumFractionDigits: 2,
            })}
            %
          </strong>
        </div>
      </div>

      <div className="health-strip__metric">
        <Gauge size={16} />
        <div>
          <small>{t("ops.errorBudgetBurn")}</small>
          <strong>
            {formatNumber(burnRate, {
              minimumFractionDigits: 1,
              maximumFractionDigits: 1,
            })}
            ×
          </strong>
        </div>
      </div>

      <div className="health-strip__metric">
        <Timer size={16} />
        <div>
          <small>{t("ops.p95Latency")}</small>
          <strong>
            {formatNumber(topP95, {
              maximumFractionDigits: 0,
            })}{" "}
            ms
          </strong>
        </div>
      </div>

      <div className="health-strip__trend">
        <div>
          <small>{t("ops.latencyTrend")}</small>
          <span>{t("ops.lastRequests", { count: trend.length })}</span>
        </div>
        <svg viewBox="0 0 120 36" role="img" aria-label={t("ops.latencyTrend")}>
          <path className="health-strip__spark-base" d="M0 33 L120 33" />
          <path className="health-strip__spark" d={sparklinePath(trend)} />
        </svg>
      </div>

      <button
        type="button"
        className="health-strip__report"
        onClick={onOpenReport}
      >
        {severity === "healthy" ? (
          <Activity size={15} />
        ) : (
          <Siren size={15} />
        )}
        {t("ops.incidentReport")}
      </button>
    </section>
  );
}
