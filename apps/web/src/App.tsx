import {
  Activity,
  Boxes,
  CircleAlert,
  Database,
  Eraser,
  GitBranch,
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

const scenarios: Array<{
  id: ScenarioId;
  label: string;
  hint: string;
}> = [
  {
    id: "stable",
    label: "Stable",
    hint: "Healthy baseline",
  },
  {
    id: "payment-timeout",
    label: "Payment timeout",
    hint: "Slow failing payment calls",
  },
  {
    id: "database-lock",
    label: "Database lock",
    hint: "Blocked transaction path",
  },
  {
    id: "cache-degradation",
    label: "Cache degradation",
    hint: "Redis latency and fallback",
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

const timeFormatter = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

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
          : "Could not reach the TraceForge API.",
      );
    } finally {
      setLoading(false);
    }
  }, []);

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
              : "Could not load trace.",
          );
        }
      });

    return () => {
      cancelled = true;
    };
  }, [selectedTraceId]);

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
          : "Scenario execution failed.",
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
          : "Could not clear data.",
      );
    }
  };

  const suspectedService =
    overview.hypotheses[0]?.service;

  return (
    <div className="app-shell">
      <header className="topbar">
        <a className="brand" href="#" aria-label="TraceForge home">
          <span className="brand-mark">
            <GitBranch size={19} />
          </span>
          <span>
            <strong>TraceForge</strong>
            <small>incident workbench</small>
          </span>
        </a>

        <div className="topbar__status">
          <span
            className={[
              "connection-dot",
              "connection-dot--" + connection,
            ].join(" ")}
          />
          <span>{connection}</span>
          <span className="topbar__divider" />
          <span>
            {overview.updatedAt
              ? "synced " +
                timeFormatter.format(overview.updatedAt)
              : "waiting for data"}
          </span>
        </div>

        <button
          type="button"
          className="ghost-button"
          onClick={() => void refresh()}
          disabled={loading}
        >
          <RefreshCw
            size={15}
            className={loading ? "spin" : ""}
          />
          Refresh
        </button>
      </header>

      <main>
        <section className="hero-strip">
          <div>
            <p className="eyebrow">Distributed systems observability</p>
            <h1>Find where the request really broke.</h1>
            <p className="hero-strip__copy">
              Reconstruct service calls, remove parallel wait time,
              replay traces, and rank incident hypotheses using the
              evidence already inside each span.
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
                <span>{scenario.label}</span>
                <small>{scenario.hint}</small>
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

        <section className="metrics-grid" aria-label="System metrics">
          <MetricCard
            label="Traces"
            value={String(overview.totals.traces)}
            meta={overview.totals.spans + " spans loaded"}
            icon={<GitBranch size={19} />}
          />
          <MetricCard
            label="Services"
            value={String(overview.totals.services)}
            meta={overview.edges.length + " dependencies"}
            icon={<Boxes size={19} />}
          />
          <MetricCard
            label="Error rate"
            value={
              (overview.totals.errorRate * 100).toFixed(1) + "%"
            }
            meta={overview.totals.errors + " failing spans"}
            icon={<Server size={19} />}
          />
          <MetricCard
            label="Top p95"
            value={
              overview.services[0]
                ? overview.services[0].p95Ms.toFixed(0) + " ms"
                : "—"
            }
            meta={
              overview.services[0]?.service ?? "no service data"
            }
            icon={<Database size={19} />}
          />
        </section>

        <section className="overview-grid">
          <article className="panel panel--map">
            <div className="panel-heading">
              <div>
                <p className="eyebrow">Live topology</p>
                <h2>Service map</h2>
              </div>
              <span className="panel-chip">
                {overview.edges.length} edges
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
                <p className="eyebrow">Evidence ranking</p>
                <h2>Incident hypothesis</h2>
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
                <p className="eyebrow">Request explorer</p>
                <h2>Recent traces</h2>
              </div>

              <button
                type="button"
                className="danger-ghost"
                onClick={() => void clearData()}
                disabled={overview.totals.spans === 0}
              >
                <Eraser size={14} />
                Clear
              </button>
            </div>

            <div className="trace-filters">
              <label className="search-field">
                <Search size={15} />
                <input
                  type="search"
                  placeholder="Search service, operation or trace"
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
                Errors only
              </button>
            </div>

            <div className="trace-list">
              {filteredTraces.length === 0 ? (
                <div className="empty-state">
                  {traces.length === 0
                    ? "Run a scenario to populate the explorer."
                    : "No traces match the current filters."}
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
                        {trace.durationMs.toFixed(0)} ms
                      </strong>
                      <small>
                        {trace.spanCount} spans
                      </small>
                    </span>
                    <span className="trace-row__stamp">
                      {timeFormatter.format(trace.startedAtMs)}
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
                    <p className="eyebrow">Selected request</p>
                    <h2>
                      {selectedTrace.status === "error"
                        ? "Failure replay"
                        : "Trace replay"}
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
                <h3>No trace selected</h3>
                <p>
                  Choose a request from the explorer to inspect
                  its call timeline and self-time.
                </p>
              </div>
            )}
          </article>
        </section>
      </main>

      <footer>
        <span>TraceForge</span>
        <span>
          deterministic scenarios · evidence-first analysis
        </span>
      </footer>
    </div>
  );
}
