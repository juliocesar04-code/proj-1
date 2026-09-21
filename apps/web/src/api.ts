export type SpanStatus = "ok" | "error" | "unset";

export interface ServiceNode {
  service: string;
  calls: number;
  errors: number;
  errorRate: number;
  averageMs: number;
  p95Ms: number;
  selfTimeMs: number;
}

export interface ServiceEdge {
  source: string;
  target: string;
  calls: number;
  errors: number;
  averageMs: number;
}

export interface IncidentHypothesis {
  service: string;
  score: number;
  confidence: "low" | "medium" | "high";
  evidence: string[];
}

export interface TraceSummary {
  traceId: string;
  startedAtMs: number;
  durationMs: number;
  spanCount: number;
  errorCount: number;
  rootService: string;
  rootOperation: string;
}

export interface AnalyzedSpan {
  traceId: string;
  spanId: string;
  parentSpanId: string | null;
  service: string;
  operation: string;
  startMs: number;
  durationMs: number;
  status: SpanStatus;
  attributes: Record<string, string | number | boolean>;
  endMs: number;
  selfTimeMs: number;
  depth: number;
}

export interface TraceAnalysis {
  traceId: string;
  durationMs: number;
  startedAtMs: number;
  endedAtMs: number;
  status: SpanStatus;
  spans: AnalyzedSpan[];
  services: ServiceNode[];
  edges: ServiceEdge[];
  hypotheses: IncidentHypothesis[];
}

export interface Overview {
  updatedAt: number;
  totals: {
    spans: number;
    traces: number;
    services: number;
    errors: number;
    errorRate: number;
  };
  services: ServiceNode[];
  edges: ServiceEdge[];
  hypotheses: IncidentHypothesis[];
}

export type ScenarioId =
  | "stable"
  | "payment-timeout"
  | "database-lock"
  | "cache-degradation";

const apiBase =
  (import.meta.env.VITE_API_URL as string | undefined)?.replace(/\/$/, "") ??
  "http://localhost:4000";

async function request<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await fetch(apiBase + path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(
      text || "Request failed with status " + response.status,
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}

export const api = {
  overview: () => request<Overview>("/api/overview"),

  traces: (limit = 80) =>
    request<{ traces: TraceSummary[] }>(
      "/api/traces?limit=" + encodeURIComponent(String(limit)),
    ),

  trace: (traceId: string) =>
    request<TraceAnalysis>(
      "/api/traces/" + encodeURIComponent(traceId),
    ),

  runScenario: (scenario: ScenarioId, traces = 24) =>
    request<{
      scenario: ScenarioId;
      spans: number;
      traces: number;
    }>("/api/scenarios/" + scenario + "/run", {
      method: "POST",
      body: JSON.stringify({ traces }),
    }),

  clear: () =>
    request<void>("/api/data", {
      method: "DELETE",
    }),

  events(onRefresh: () => void) {
    const source = new EventSource(apiBase + "/api/events");

    for (const event of ["ingest", "scenario", "reset"]) {
      source.addEventListener(event, onRefresh);
    }

    return source;
  },
};
