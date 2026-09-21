import {
  ArrowDownRight,
  ArrowUpRight,
  Equal,
  GitCompareArrows,
  X,
} from "lucide-react";
import {
  useEffect,
  useMemo,
  useState,
} from "react";
import {
  api,
  type TraceAnalysis,
  type TraceSummary,
} from "../api.js";
import { useI18n } from "../i18n.js";

interface TraceCompareProps {
  baseTrace: TraceAnalysis;
  traces: TraceSummary[];
  onClose: () => void;
}

function Delta({
  value,
  suffix = "",
  invert = false,
}: {
  value: number;
  suffix?: string;
  invert?: boolean;
}) {
  const { formatNumber } = useI18n();
  const neutral = Math.abs(value) < 0.05;
  const positive = value > 0;
  const good = invert ? !positive : positive;

  return (
    <span
      className={[
        "compare-delta",
        neutral
          ? "compare-delta--neutral"
          : good
            ? "compare-delta--good"
            : "compare-delta--bad",
      ].join(" ")}
    >
      {neutral ? (
        <Equal size={12} />
      ) : positive ? (
        <ArrowUpRight size={12} />
      ) : (
        <ArrowDownRight size={12} />
      )}
      {formatNumber(Math.abs(value), {
        maximumFractionDigits: 1,
      })}
      {suffix}
    </span>
  );
}

export function TraceCompare({
  baseTrace,
  traces,
  onClose,
}: TraceCompareProps) {
  const { t, formatNumber, formatTime } = useI18n();
  const alternatives = traces.filter(
    (trace) => trace.traceId !== baseTrace.traceId,
  );
  const [compareId, setCompareId] = useState(
    alternatives[0]?.traceId ?? "",
  );
  const [compareTrace, setCompareTrace] =
    useState<TraceAnalysis | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!compareId) {
      setCompareTrace(null);
      return;
    }

    let cancelled = false;
    setLoading(true);

    void api
      .trace(compareId)
      .then((trace) => {
        if (!cancelled) setCompareTrace(trace);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [compareId]);

  const serviceRows = useMemo(() => {
    if (!compareTrace) return [];

    const names = new Set([
      ...baseTrace.services.map((service) => service.service),
      ...compareTrace.services.map((service) => service.service),
    ]);

    return [...names]
      .map((service) => {
        const base = baseTrace.services.find(
          (item) => item.service === service,
        );
        const compare = compareTrace.services.find(
          (item) => item.service === service,
        );

        return {
          service,
          baseP95: base?.p95Ms ?? 0,
          compareP95: compare?.p95Ms ?? 0,
          baseError: base?.errorRate ?? 0,
          compareError: compare?.errorRate ?? 0,
        };
      })
      .sort(
        (a, b) =>
          Math.abs(b.compareP95 - b.baseP95) -
          Math.abs(a.compareP95 - a.baseP95),
      )
      .slice(0, 7);
  }, [baseTrace.services, compareTrace]);

  return (
    <div className="overlay" role="presentation" onMouseDown={onClose}>
      <section
        className="drawer compare-drawer"
        role="dialog"
        aria-modal="true"
        aria-label={t("compare.title")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="drawer__header">
          <div>
            <p className="eyebrow">{t("compare.forensics")}</p>
            <h2>{t("compare.title")}</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label={t("compare.close")}
          >
            <X size={17} />
          </button>
        </header>

        <div className="compare-picker">
          <div className="compare-picker__base">
            <small>{t("compare.baseline")}</small>
            <strong>{baseTrace.traceId.slice(0, 14)}</strong>
            <span>{formatTime(baseTrace.startedAtMs)}</span>
          </div>
          <GitCompareArrows size={18} />
          <label>
            <small>{t("compare.compareWith")}</small>
            <select
              value={compareId}
              onChange={(event) =>
                setCompareId(event.target.value)
              }
            >
              {alternatives.map((trace) => (
                <option key={trace.traceId} value={trace.traceId}>
                  {trace.traceId.slice(0, 18)} ·{" "}
                  {formatNumber(trace.durationMs, {
                    maximumFractionDigits: 0,
                  })}{" "}
                  ms
                </option>
              ))}
            </select>
          </label>
        </div>

        {loading || !compareTrace ? (
          <div className="drawer__loading">
            {t("compare.loading")}
          </div>
        ) : (
          <>
            <div className="compare-summary">
              <article>
                <small>{t("compare.durationDelta")}</small>
                <strong>
                  {formatNumber(compareTrace.durationMs, {
                    maximumFractionDigits: 0,
                  })}{" "}
                  ms
                </strong>
                <Delta
                  value={
                    compareTrace.durationMs -
                    baseTrace.durationMs
                  }
                  suffix=" ms"
                  invert
                />
              </article>
              <article>
                <small>{t("compare.spanDelta")}</small>
                <strong>
                  {formatNumber(compareTrace.spans.length)}
                </strong>
                <Delta
                  value={
                    compareTrace.spans.length -
                    baseTrace.spans.length
                  }
                />
              </article>
              <article>
                <small>{t("compare.status")}</small>
                <strong
                  className={
                    compareTrace.status === "error"
                      ? "compare-status compare-status--error"
                      : "compare-status compare-status--ok"
                  }
                >
                  {compareTrace.status.toUpperCase()}
                </strong>
                <span className="compare-reference">
                  {t("compare.baseline")}:{" "}
                  {baseTrace.status.toUpperCase()}
                </span>
              </article>
            </div>

            <div className="compare-table-wrap">
              <div className="compare-table__head">
                <span>{t("metrics.services")}</span>
                <span>{t("compare.baseline")} p95</span>
                <span>{t("compare.current")} p95</span>
                <span>{t("compare.delta")}</span>
              </div>
              {serviceRows.map((row) => (
                <div className="compare-table__row" key={row.service}>
                  <strong>{row.service}</strong>
                  <span>
                    {formatNumber(row.baseP95, {
                      maximumFractionDigits: 0,
                    })}{" "}
                    ms
                  </span>
                  <span>
                    {formatNumber(row.compareP95, {
                      maximumFractionDigits: 0,
                    })}{" "}
                    ms
                  </span>
                  <Delta
                    value={row.compareP95 - row.baseP95}
                    suffix=" ms"
                    invert
                  />
                </div>
              ))}
            </div>
          </>
        )}
      </section>
    </div>
  );
}
