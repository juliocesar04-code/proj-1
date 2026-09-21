import type {
  Overview,
  TraceAnalysis,
  TraceSummary,
} from "./api.js";

export type DemoScenario =
  | "stable"
  | "payment-timeout"
  | "database-lock"
  | "cache-degradation";

export interface DemoState {
  scenario: DemoScenario;
  overview: Overview;
  traces: TraceSummary[];
  traceById: Record<string, TraceAnalysis>;
}

const serviceNames = [
  "edge-gateway",
  "auth",
  "catalog",
  "redis",
  "cart",
  "payment",
  "postgres",
];

function evidenceFor(
  errors: number,
  calls: number,
  p95Ms: number,
  selfTimeMs: number,
) {
  const evidence: string[] = [];
  if (errors > 0) {
    evidence.push(
      `${errors}/${calls} spans ended in error (${(
        (errors / calls) *
        100
      ).toFixed(1)}%).`,
    );
  }
  if (p95Ms >= 500) {
    evidence.push(`p95 latency reached ${p95Ms.toFixed(1)} ms.`);
  }
  if (selfTimeMs > 0) {
    evidence.push(
      `${selfTimeMs.toFixed(
        1,
      )} ms were spent inside the service after removing overlapping child calls.`,
    );
  }
  return evidence.length > 0
    ? evidence
    : ["No strong error or latency signal was detected."];
}

function scenarioProfile(scenario: DemoScenario) {
  switch (scenario) {
    case "stable":
      return {
        suspect: "edge-gateway",
        suspectErrors: 0,
        suspectP95: 472,
        suspectAverage: 391,
        suspectSelf: 1380,
        rootP95: 472,
        totalErrors: 0,
        errorRate: 0,
        score: 24.8,
        confidence: "low" as const,
      };
    case "database-lock":
      return {
        suspect: "postgres",
        suspectErrors: 6,
        suspectP95: 1810,
        suspectAverage: 544,
        suspectSelf: 4560,
        rootP95: 2100,
        totalErrors: 12,
        errorRate: 0.0714,
        score: 91.6,
        confidence: "high" as const,
      };
    case "cache-degradation":
      return {
        suspect: "redis",
        suspectErrors: 8,
        suspectP95: 760,
        suspectAverage: 391,
        suspectSelf: 6280,
        rootP95: 1090,
        totalErrors: 10,
        errorRate: 0.0595,
        score: 86.2,
        confidence: "high" as const,
      };
    case "payment-timeout":
    default:
      return {
        suspect: "payment",
        suspectErrors: 8,
        suspectP95: 2240,
        suspectAverage: 920,
        suspectSelf: 984,
        rootP95: 2410,
        totalErrors: 16,
        errorRate: 0.0952,
        score: 89.4,
        confidence: "high" as const,
      };
  }
}

function serviceMetrics(scenario: DemoScenario) {
  const p = scenarioProfile(scenario);
  const base = [
    {
      service: "edge-gateway",
      calls: 24,
      errors: scenario === "stable" ? 0 : 8,
      errorRate: scenario === "stable" ? 0 : 0.3333,
      averageMs:
        scenario === "stable"
          ? 391
          : scenario === "cache-degradation"
            ? 860
            : scenario === "database-lock"
              ? 1010
              : 1280,
      p95Ms: p.rootP95,
      selfTimeMs: 1880,
    },
    {
      service: "auth",
      calls: 24,
      errors: 0,
      errorRate: 0,
      averageMs: 42,
      p95Ms: 48,
      selfTimeMs: 1008,
    },
    {
      service: "catalog",
      calls: 24,
      errors: scenario === "cache-degradation" ? 8 : 0,
      errorRate: scenario === "cache-degradation" ? 0.3333 : 0,
      averageMs: scenario === "cache-degradation" ? 680 : 149,
      p95Ms: scenario === "cache-degradation" ? 812 : 171,
      selfTimeMs: 2820,
    },
    {
      service: "redis",
      calls: 24,
      errors: scenario === "cache-degradation" ? 8 : 0,
      errorRate: scenario === "cache-degradation" ? 0.3333 : 0,
      averageMs: scenario === "cache-degradation" ? 391 : 18,
      p95Ms: scenario === "cache-degradation" ? 760 : 22,
      selfTimeMs: scenario === "cache-degradation" ? 6280 : 432,
    },
    {
      service: "cart",
      calls: 24,
      errors: 0,
      errorRate: 0,
      averageMs: 128,
      p95Ms: 147,
      selfTimeMs: 3072,
    },
    {
      service: "payment",
      calls: 24,
      errors: scenario === "payment-timeout" ? 8 : scenario === "database-lock" ? 6 : 0,
      errorRate:
        scenario === "payment-timeout"
          ? 0.3333
          : scenario === "database-lock"
            ? 0.25
            : 0,
      averageMs:
        scenario === "payment-timeout"
          ? 920
          : scenario === "database-lock"
            ? 740
            : 164,
      p95Ms:
        scenario === "payment-timeout"
          ? 2240
          : scenario === "database-lock"
            ? 1880
            : 201,
      selfTimeMs: scenario === "payment-timeout" ? 984 : 1640,
    },
    {
      service: "postgres",
      calls: 24,
      errors: scenario === "database-lock" ? 6 : 0,
      errorRate: scenario === "database-lock" ? 0.25 : 0,
      averageMs: scenario === "database-lock" ? 544 : 66,
      p95Ms: scenario === "database-lock" ? 1810 : 79,
      selfTimeMs: scenario === "database-lock" ? 4560 : 1584,
    },
  ];

  return [...base].sort((a, b) => b.p95Ms - a.p95Ms);
}

function edgesFor(scenario: DemoScenario) {
  return [
    { source: "edge-gateway", target: "auth", calls: 24, errors: 0, averageMs: 42 },
    {
      source: "edge-gateway",
      target: "catalog",
      calls: 24,
      errors: scenario === "cache-degradation" ? 8 : 0,
      averageMs: scenario === "cache-degradation" ? 680 : 149,
    },
    {
      source: "catalog",
      target: "redis",
      calls: 24,
      errors: scenario === "cache-degradation" ? 8 : 0,
      averageMs: scenario === "cache-degradation" ? 391 : 18,
    },
    { source: "edge-gateway", target: "cart", calls: 24, errors: 0, averageMs: 128 },
    {
      source: "edge-gateway",
      target: "payment",
      calls: 24,
      errors: scenario === "payment-timeout" ? 8 : scenario === "database-lock" ? 6 : 0,
      averageMs:
        scenario === "payment-timeout"
          ? 920
          : scenario === "database-lock"
            ? 740
            : 164,
    },
    {
      source: "payment",
      target: "postgres",
      calls: 24,
      errors: scenario === "database-lock" ? 6 : 0,
      averageMs: scenario === "database-lock" ? 544 : 66,
    },
  ];
}

function hypothesesFor(scenario: DemoScenario) {
  const services = serviceMetrics(scenario);
  const p = scenarioProfile(scenario);
  const suspect = services.find((service) => service.service === p.suspect)!;

  const second =
    scenario === "stable"
      ? services.find((service) => service.service === "catalog")!
      : services.find((service) => service.service === "edge-gateway")!;

  return [
    {
      service: suspect.service,
      score: p.score,
      confidence: p.confidence,
      evidence: evidenceFor(
        suspect.errors,
        suspect.calls,
        suspect.p95Ms,
        suspect.selfTimeMs,
      ),
    },
    {
      service: second.service,
      score: scenario === "stable" ? 16.4 : 61.7,
      confidence: scenario === "stable" ? ("low" as const) : ("high" as const),
      evidence: evidenceFor(
        second.errors,
        second.calls,
        second.p95Ms,
        second.selfTimeMs,
      ),
    },
    {
      service: "catalog",
      score: scenario === "cache-degradation" ? 58.2 : 12.8,
      confidence: scenario === "cache-degradation" ? ("medium" as const) : ("low" as const),
      evidence:
        scenario === "cache-degradation"
          ? ["8/24 spans ended in error (33.3%).", "p95 latency reached 812.0 ms."]
          : ["No strong error or latency signal was detected."],
    },
  ];
}

function isFailure(scenario: DemoScenario, index: number) {
  if (scenario === "stable") return false;
  if (scenario === "payment-timeout") return index % 3 === 0;
  if (scenario === "database-lock") return index % 4 === 0;
  return index % 2 === 0;
}

function rootDuration(scenario: DemoScenario, index: number, failing: boolean) {
  if (scenario === "stable") return 390 + (index % 5) * 22;
  if (scenario === "payment-timeout") return failing ? 2350 + index * 11 : 610 + index * 7;
  if (scenario === "database-lock") return failing ? 1980 + index * 13 : 640 + index * 8;
  return failing ? 980 + index * 9 : 690 + index * 6;
}

function span(
  traceId: string,
  base: number,
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
    traceId,
    spanId,
    parentSpanId,
    service,
    operation,
    startMs: base + startOffset,
    durationMs,
    status,
    attributes: {},
    endMs: base + startOffset + durationMs,
    selfTimeMs: Math.max(1, Math.round(durationMs * 0.18)),
    depth,
  };
}

function buildTrace(
  scenario: DemoScenario,
  trace: TraceSummary,
  index: number,
): TraceAnalysis {
  const failing = trace.errorCount > 0;
  const base = trace.startedAtMs;
  const duration = trace.durationMs;

  const paymentDuration =
    scenario === "payment-timeout" && failing
      ? Math.max(1200, duration - 350)
      : scenario === "database-lock" && failing
        ? Math.max(900, duration - 500)
        : 164;

  const dbDuration =
    scenario === "database-lock" && failing ? 1710 : 79;

  const redisDuration =
    scenario === "cache-degradation" && failing ? 735 : 21;

  const catalogDuration =
    scenario === "cache-degradation" && failing ? 805 : 151;

  const spans = [
    span(
      trace.traceId,
      base,
      "root-" + index,
      null,
      "edge-gateway",
      "POST /checkout",
      0,
      duration,
      failing ? "error" : "ok",
      0,
    ),
    span(trace.traceId, base, "auth-" + index, "root-" + index, "auth", "verify-session", 18, 41, "ok", 1),
    span(
      trace.traceId,
      base,
      "catalog-" + index,
      "root-" + index,
      "catalog",
      "load-products",
      72,
      catalogDuration,
      scenario === "cache-degradation" && failing ? "error" : "ok",
      1,
    ),
    span(
      trace.traceId,
      base,
      "redis-" + index,
      "catalog-" + index,
      "redis",
      "GET product:batch",
      88,
      redisDuration,
      scenario === "cache-degradation" && failing ? "error" : "ok",
      2,
    ),
    span(trace.traceId, base, "cart-" + index, "root-" + index, "cart", "price-cart", 94, 133, "ok", 1),
    span(
      trace.traceId,
      base,
      "payment-" + index,
      "root-" + index,
      "payment",
      "authorize",
      scenario === "cache-degradation" ? 430 : 250,
      paymentDuration,
      (scenario === "payment-timeout" || scenario === "database-lock") && failing
        ? "error"
        : "ok",
      1,
    ),
    span(
      trace.traceId,
      base,
      "db-" + index,
      "payment-" + index,
      "postgres",
      "SELECT account FOR UPDATE",
      scenario === "cache-degradation" ? 452 : 282,
      dbDuration,
      scenario === "database-lock" && failing ? "error" : "ok",
      2,
    ),
  ];

  return {
    traceId: trace.traceId,
    durationMs: duration,
    startedAtMs: base,
    endedAtMs: base + duration,
    status: failing ? "error" : "ok",
    spans,
    services: serviceMetrics(scenario),
    edges: edgesFor(scenario),
    hypotheses: hypothesesFor(scenario),
  };
}

export function buildDemoScenario(
  scenario: DemoScenario,
  count = 24,
): DemoState {
  const now = Date.now();
  const p = scenarioProfile(scenario);
  const traces: TraceSummary[] = Array.from(
    { length: Math.min(Math.max(count, 4), 120) },
    (_, index) => {
      const failing = isFailure(scenario, index);
      return {
        traceId:
          "demo-" +
          scenario +
          "-" +
          String(index + 1).padStart(3, "0"),
        startedAtMs: now - index * 21_000,
        durationMs: rootDuration(scenario, index, failing),
        spanCount: 7,
        errorCount: failing ? 2 : 0,
        rootService: "edge-gateway",
        rootOperation: "POST /checkout",
      };
    },
  );

  const traceById = Object.fromEntries(
    traces.map((trace, index) => [
      trace.traceId,
      buildTrace(scenario, trace, index),
    ]),
  );

  const overview: Overview = {
    updatedAt: now,
    totals: {
      spans: traces.length * 7,
      traces: traces.length,
      services: serviceNames.length,
      errors:
        scenario === "stable"
          ? 0
          : traces.reduce(
              (sum, trace) => sum + trace.errorCount,
              0,
            ),
      errorRate:
        scenario === "stable"
          ? 0
          : traces.reduce(
                (sum, trace) => sum + trace.errorCount,
                0,
              ) /
            (traces.length * 7),
    },
    services: serviceMetrics(scenario),
    edges: edgesFor(scenario),
    hypotheses: hypothesesFor(scenario),
  };

  return {
    scenario,
    overview: {
      ...overview,
      totals: {
        ...overview.totals,
        errorRate:
          scenario === "stable"
            ? 0
            : Number(overview.totals.errorRate.toFixed(4)),
        errors:
          scenario === "stable" ? 0 : overview.totals.errors,
      },
    },
    traces,
    traceById,
  };
}

export function buildEmptyDemo(): DemoState {
  return {
    scenario: "stable",
    overview: {
      updatedAt: Date.now(),
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
    },
    traces: [],
    traceById: {},
  };
}
