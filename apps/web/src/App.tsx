import {
  Activity,
  Boxes,
  CircleAlert,
  Database,
  Eraser,
  GitBranch,
  Globe2,
  RefreshCw,
  Search,
  Server,
  TimerReset,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import {
  api,
  type Overview,
  type ScenarioId,
  type TraceAnalysis,
  type TraceSummary,
} from "./api.js";
import { HypothesisPanel } from "./components/HypothesisPanel.js";
import { ServiceMap } from "./components/ServiceMap.js";
import { TraceTimeline } from "./components/TraceTimeline.js";
import {
  localeOptions,
  useI18n,
  type Locale,
} from "./i18n.js";

const scenarios: Array<{
  id: ScenarioId;
  labelKey: string;
  hintKey: string;
}> = [
  {
    id: "stable",
    labelKey: "scenario.stable.label",
    hintKey: "scenario.stable.hint",
  },
  {
    id: "payment-timeout",
    labelKey: "scenario.payment.label",
    hintKey: "scenario.payment.hint",
  },
  {
    id: "database-lock",
    labelKey: "scenario.database.label",
    hintKey: "scenario.database.hint",
  },
  {
    id: "cache-degradation",
    labelKey: "scenario.cache.label",
    hintKey: "scenario.cache.hint",
  },
];

const emptyOverview: Overview = {
  updatedAt: 0,
  totals: {
    spans: 0,
    traces: 0,
    services: 0,
    errors: 0,
    errorRate: 0,
  },
  services: [],
  edges: [],
  hypotheses: [],
};

function MetricCard({
  label,
  value,
  meta,
  icon,
}: {
  label: string;
  value: string;
  meta: string;
  icon: React.ReactNode;
}) {
  return (
    <article className="metric-card">
      <div className="metric-card__icon">{icon}</div>
      <div>
        <p>{label}</p>
        <strong>{value}</strong>
        <small>{meta}</small>
      </div>
    </article>
  );
}

export function App() {
  const {
    locale,
    setLocale,
    dir,
    t,
    formatNumber,
    formatTime,
  } = useI18n();

  const [overview, setOverview] =
    useState<Overview>(emptyOverview);
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [selectedTraceId, setSelectedTraceId] =
    useState<string | null>(null);
  const [selectedTrace, setSelectedTrace] =
    useState<TraceAnalysis | null>(null);
  const [query, setQuery] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [busyScenario, setBusyScenario] =
    useState<ScenarioId | null>(null);
  const [loading, setLoading] = useState(true);
  const [connection, setConnection] = useState<
    "connecting" | "live" | "offline"
  >("connecting");
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [nextOverview, traceResponse] =
        await Promise.all([
          api.overview(),
          api.traces(),
        ]);

      setOverview(nextOverview);
      setTraces(traceResponse.traces);
      setError(null);
      setSelectedTraceId((current) => {
        if (
          current &&
          traceResponse.traces.some(
            (trace) => trace.traceId === current,
          )
        ) {
          return current;
        }
        return traceResponse.traces[0]?.traceId ?? null;
      });
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : t("error.api"),
      );
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void refresh();

    const events = api.events(() => {
      void refresh();
    });

    events.onopen = () => setConnection("live");
    events.onerror = () => setConnection("offline");

    return () => events.close();
  }, [refresh]);

  useEffect(() => {
    if (!selectedTraceId) {
      setSelectedTrace(null);
      return;
    }

    let cancelled = false;

    void api
      .trace(selectedTraceId)
      .then((trace) => {
        if (!cancelled) setSelectedTrace(trace);
      })
      .catch((caught) => {
        if (!cancelled) {
          setError(
            caught instanceof Error
              ? caught.message
              : t("error.trace"),
          );
        }
      });

    return () => {
      cancelled = true;
    };
  }, [selectedTraceId, t]);

  const filteredTraces = useMemo(() => {
    const normalized = query.trim().toLowerCase();

    return traces.filter((trace) => {
      if (errorsOnly && trace.errorCount === 0) return false;
      if (!normalized) return true;

      return [
        trace.traceId,
        trace.rootService,
        trace.rootOperation,
      ].some((value) =>
        value.toLowerCase().includes(normalized),
      );
    });
  }, [errorsOnly, query, traces]);

  const runScenario = async (scenario: ScenarioId) => {
    setBusyScenario(scenario);
    setError(null);

    try {
      await api.clear();
      await api.runScenario(scenario);
      await refresh();
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : t("error.scenario"),
      );
    } finally {
      setBusyScenario(null);
    }
  };

  const clearData = async () => {
    try {
      await api.clear();
      await refresh();
    } catch (caught) {
      setError(
        caught instanceof Error
          ? caught.message
          : t("error.clear"),
      );
    }
  };

  const suspectedService =
    overview.hypotheses[0]?.service;

  return (
    <div className="app-shell" dir={dir}>
      <header className="topbar">
        <a className="brand" href="#" aria-label="TraceForge">
          <span className="brand-mark">
            <GitBranch size={19} />
          </span>
          <span>
            <strong>TraceForge</strong>
            <small>{t("brand.subtitle")}</small>
          </span>
        </a>

        <div className="topbar__status">
          <span
            className={[
              "connection-dot",
              "connection-dot--" + connection,
            ].join(" ")}
          />
          <span>{t("connection." + connection)}</span>
          <span className="topbar__divider" />
          <span>
            {overview.updatedAt
              ? t("synced", {
                  time: formatTime(overview.updatedAt),
                })
              : t("waitingForData")}
          </span>
        </div>

        <label className="language-picker" title={t("language")}>
          <Globe2 size={15} />
          <select
            value={locale}
            aria-label={t("language")}
            onChange={(event) =>
              setLocale(event.target.value as Locale)
            }
          >
            {localeOptions.map((option) => (
              <option key={option.code} value={option.code}>
                {option.label}
              </option>
            ))}
          </select>
        </label>

        <button
          type="button"
          className="ghost-button"
          onClick={() => void refresh()}
          disabled={loading}
          aria-label={t("refresh")}
        >
          <RefreshCw
            size={15}
            className={loading ? "spin" : ""}
          />
          <span>{t("refresh")}</span>
        </button>
      </header>

      <main>
        <section className="hero-strip">
          <div>
            <p className="eyebrow">{t("hero.eyebrow")}</p>
            <h1>{t("hero.title")}</h1>
            <p className="hero-strip__copy">
              {t("hero.copy")}
            </p>
          </div>

          <div className="scenario-grid">
            {scenarios.map((scenario) => (
              <button
                key={scenario.id}
                type="button"
                className="scenario-button"
                onClick={() =>
                  void runScenario(scenario.id)
                }
                disabled={busyScenario !== null}
              >
                <span>{t(scenario.labelKey)}</span>
                <small>{t(scenario.hintKey)}</small>
                {busyScenario === scenario.id ? (
                  <RefreshCw size={15} className="spin" />
                ) : (
                  <Activity size={15} />
                )}
              </button>
            ))}
          </div>
        </section>

        {error ? (
          <div className="error-banner" role="alert">
            <CircleAlert size={17} />
            <span>{error}</span>
          </div>
        ) : null}

        <section className="metrics-grid" aria-label={t("metrics.aria")}>
          <MetricCard
            label={t("metrics.traces")}
            value={formatNumber(overview.totals.traces)}
            meta={t("metrics.spansLoaded", {
              count: formatNumber(overview.totals.spans),
            })}
            icon={<GitBranch size={19} />}
          />
          <MetricCard
            label={t("metrics.services")}
            value={formatNumber(overview.totals.services)}
            meta={t("metrics.dependencies", {
              count: formatNumber(overview.edges.length),
            })}
            icon={<Boxes size={19} />}
          />
          <MetricCard
            label={t("metrics.errorRate")}
            value={
              formatNumber(overview.totals.errorRate * 100, {
                minimumFractionDigits: 1,
                maximumFractionDigits: 1,
              }) + "%"
            }
            meta={t("metrics.failingSpans", {
              count: formatNumber(overview.totals.errors),
            })}
            icon={<Server size={19} />}
          />
          <MetricCard
            label={t("metrics.topP95")}
            value={
              overview.services[0]
                ? formatNumber(overview.services[0].p95Ms, {
                    maximumFractionDigits: 0,
                  }) + " ms"
                : "—"
            }
            meta={
              overview.services[0]?.service ??
              t("metrics.noServiceData")
            }
            icon={<Database size={19} />}
          />
        </section>

        <section className="overview-grid">
          <article className="panel panel--map">
            <div className="panel-heading">
              <div>
                <p className="eyebrow">{t("topology")}</p>
                <h2>{t("serviceMap")}</h2>
              </div>
              <span className="panel-chip">
                {t("edges", {
                  count: formatNumber(overview.edges.length),
                })}
              </span>
            </div>
            <ServiceMap
              services={overview.services}
              edges={overview.edges}
              suspectedService={suspectedService}
            />
          </article>

          <article className="panel">
            <div className="panel-heading">
              <div>
                <p className="eyebrow">{t("evidenceRanking")}</p>
                <h2>{t("incidentHypothesis")}</h2>
              </div>
            </div>
            <HypothesisPanel
              hypotheses={overview.hypotheses}
            />
          </article>
        </section>

        <section className="explorer-grid">
          <article className="panel trace-browser">
            <div className="panel-heading panel-heading--stackable">
              <div>
                <p className="eyebrow">{t("requestExplorer")}</p>
                <h2>{t("recentTraces")}</h2>
              </div>

              <button
                type="button"
                className="danger-ghost"
                onClick={() => void clearData()}
                disabled={overview.totals.spans === 0}
              >
                <Eraser size={14} />
                {t("clear")}
              </button>
            </div>

            <div className="trace-filters">
              <label className="search-field">
                <Search size={15} />
                <input
                  type="search"
                  placeholder={t("searchPlaceholder")}
                  value={query}
                  onChange={(event) =>
                    setQuery(event.target.value)
                  }
                />
              </label>

              <button
                type="button"
                className={[
                  "filter-button",
                  errorsOnly ? "filter-button--active" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                onClick={() =>
                  setErrorsOnly((value) => !value)
                }
              >
                <CircleAlert size={14} />
                {t("errorsOnly")}
              </button>
            </div>

            <div className="trace-list">
              {filteredTraces.length === 0 ? (
                <div className="empty-state">
                  {traces.length === 0
                    ? t("empty.runScenario")
                    : t("empty.noMatches")}
                </div>
              ) : (
                filteredTraces.map((trace) => (
                  <button
                    type="button"
                    className={[
                      "trace-row",
                      selectedTraceId === trace.traceId
                        ? "trace-row--selected"
                        : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                    key={trace.traceId}
                    onClick={() =>
                      setSelectedTraceId(trace.traceId)
                    }
                  >
                    <span
                      className={[
                        "trace-status",
                        trace.errorCount > 0
                          ? "trace-status--error"
                          : "trace-status--ok",
                      ].join(" ")}
                    />
                    <span className="trace-row__primary">
                      <strong>{trace.rootOperation}</strong>
                      <small>{trace.rootService}</small>
                    </span>
                    <span className="trace-row__time">
                      <strong>
                        {formatNumber(trace.durationMs, {
                          maximumFractionDigits: 0,
                        })}{" "}
                        ms
                      </strong>
                      <small>
                        {formatNumber(trace.spanCount)} spans
                      </small>
                    </span>
                    <span className="trace-row__stamp">
                      {formatTime(trace.startedAtMs)}
                    </span>
                  </button>
                ))
              )}
            </div>
          </article>

          <article className="panel trace-detail">
            {selectedTrace ? (
              <>
                <div className="panel-heading">
                  <div>
                    <p className="eyebrow">{t("selectedRequest")}</p>
                    <h2>
                      {selectedTrace.status === "error"
                        ? t("failureReplay")
                        : t("traceReplay")}
                    </h2>
                  </div>
                  <span className="trace-id-chip">
                    {selectedTrace.traceId.slice(0, 8)}
                  </span>
                </div>
                <TraceTimeline trace={selectedTrace} />
              </>
            ) : (
              <div className="trace-detail__empty">
                <TimerReset size={28} />
                <h3>{t("noTraceSelected")}</h3>
                <p>{t("chooseRequest")}</p>
              </div>
            )}
          </article>
        </section>
      </main>

      <footer>
        <span>TraceForge</span>
        <span>{t("footer")}</span>
      </footer>
    </div>
  );
}
