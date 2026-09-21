import type {
  Overview,
  TraceAnalysis,
  TraceSummary,
} from "./api.js";

const now = Date.now();

export const demoOverview: Overview = {
  updatedAt: now,
  totals: {
    spans: 168,
    traces: 24,
    services: 7,
    errors: 16,
    errorRate: 0.0952,
  },
  services: [
    { service: "payment", calls: 24, errors: 8, errorRate: 0.3333, averageMs: 920, p95Ms: 2240, selfTimeMs: 984 },
    { service: "edge-gateway", calls: 24, errors: 8, errorRate: 0.3333, averageMs: 1280, p95Ms: 2410, selfTimeMs: 1880 },
    { service: "catalog", calls: 24, errors: 0, errorRate: 0, averageMs: 149, p95Ms: 171, selfTimeMs: 2820 },
    { service: "cart", calls: 24, errors: 0, errorRate: 0, averageMs: 128, p95Ms: 147, selfTimeMs: 3072 },
    { service: "postgres", calls: 24, errors: 0, errorRate: 0, averageMs: 66, p95Ms: 79, selfTimeMs: 1584 },
    { service: "auth", calls: 24, errors: 0, errorRate: 0, averageMs: 42, p95Ms: 48, selfTimeMs: 1008 },
    { service: "redis", calls: 24, errors: 0, errorRate: 0, averageMs: 18, p95Ms: 22, selfTimeMs: 432 },
  ],
  edges: [
    { source: "edge-gateway", target: "auth", calls: 24, errors: 0, averageMs: 42 },
    { source: "edge-gateway", target: "catalog", calls: 24, errors: 0, averageMs: 149 },
    { source: "catalog", target: "redis", calls: 24, errors: 0, averageMs: 18 },
    { source: "edge-gateway", target: "cart", calls: 24, errors: 0, averageMs: 128 },
    { source: "edge-gateway", target: "payment", calls: 24, errors: 8, averageMs: 920 },
    { source: "payment", target: "postgres", calls: 24, errors: 0, averageMs: 66 },
  ],
  hypotheses: [
    {
      service: "payment",
      score: 89.4,
      confidence: "high",
      evidence: [
        "8/24 spans ended in error (33.3%).",
        "p95 latency reached 2240 ms.",
        "984 ms were spent inside the service after removing overlapping child calls.",
      ],
    },
    {
      service: "edge-gateway",
      score: 67.2,
      confidence: "high",
      evidence: [
        "8/24 root spans ended in error (33.3%).",
        "p95 latency reached 2410 ms.",
      ],
    },
    {
      service: "catalog",
      score: 12.8,
      confidence: "low",
      evidence: ["No strong error or latency signal was detected."],
    },
  ],
};

export const demoTraces: TraceSummary[] = Array.from({ length: 12 }, (_, index) => {
  const failing = index % 3 === 0;
  return {
    traceId: "demo-trace-" + String(index + 1).padStart(2, "0"),
    startedAtMs: now - index * 21_000,
    durationMs: failing ? 2350 + index * 11 : 610 + index * 7,
    spanCount: 7,
    errorCount: failing ? 2 : 0,
    rootService: "edge-gateway",
    rootOperation: "POST /checkout",
  };
});

function span(
  spanId: string,
  parentSpanId: string | null,
  service: string,
  operation: string,
  startOffset: number,
  durationMs: number,
  status: "ok" | "error",
  depth: number,
) {
  return {
    traceId: demoTraces[0]!.traceId,
    spanId,
    parentSpanId,
    service,
    operation,
    startMs: now + startOffset,
    durationMs,
    status,
    attributes: {},
    endMs: now + startOffset + durationMs,
    selfTimeMs: Math.max(1, Math.round(durationMs * 0.18)),
    depth,
  };
}

export const demoTrace: TraceAnalysis = {
  traceId: demoTraces[0]!.traceId,
  durationMs: 2410,
  startedAtMs: now,
  endedAtMs: now + 2410,
  status: "error",
  spans: [
    span("root", null, "edge-gateway", "POST /checkout", 0, 2410, "error", 0),
    span("auth", "root", "auth", "verify-session", 18, 41, "ok", 1),
    span("catalog", "root", "catalog", "load-products", 72, 151, "ok", 1),
    span("redis", "catalog", "redis", "GET product:batch", 88, 21, "ok", 2),
    span("cart", "root", "cart", "price-cart", 94, 133, "ok", 1),
    span("payment", "root", "payment", "authorize", 250, 2020, "error", 1),
    span("db", "payment", "postgres", "SELECT account FOR UPDATE", 282, 79, "ok", 2),
  ],
  services: demoOverview.services,
  edges: demoOverview.edges,
  hypotheses: demoOverview.hypotheses,
};
