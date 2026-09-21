import type {
  AnalyzedSpan,
  IncidentHypothesis,
  ServiceEdge,
  ServiceNode,
  Span,
  TraceAnalysis,
} from "./types.js";

const round = (value: number, digits = 2) =>
  Number(value.toFixed(digits));

const spanKey = (traceId: string, spanId: string) => traceId + ":" + spanId;

function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(
    sorted.length - 1,
    Math.max(0, Math.ceil(p * sorted.length) - 1),
  );
  return sorted[index] ?? 0;
}

function unionLength(
  intervals: Array<{ start: number; end: number }>,
): number {
  if (intervals.length === 0) return 0;

  const sorted = intervals
    .filter((interval) => interval.end > interval.start)
    .sort((a, b) => a.start - b.start);

  const first = sorted[0];
  if (!first) return 0;

  let total = 0;
  let currentStart = first.start;
  let currentEnd = first.end;

  for (let i = 1; i < sorted.length; i += 1) {
    const interval = sorted[i];
    if (!interval) continue;

    if (interval.start <= currentEnd) {
      currentEnd = Math.max(currentEnd, interval.end);
      continue;
    }

    total += currentEnd - currentStart;
    currentStart = interval.start;
    currentEnd = interval.end;
  }

  return total + currentEnd - currentStart;
}

function depthOf(
  span: Span,
  byId: Map<string, Span>,
  memo: Map<string, number>,
): number {
  const key = spanKey(span.traceId, span.spanId);
  const cached = memo.get(key);
  if (cached !== undefined) return cached;

  if (!span.parentSpanId) {
    memo.set(key, 0);
    return 0;
  }

  const parent = byId.get(spanKey(span.traceId, span.parentSpanId));
  if (!parent) {
    memo.set(key, 0);
    return 0;
  }

  const depth = depthOf(parent, byId, memo) + 1;
  memo.set(key, depth);
  return depth;
}

export function analyzeSpans(spans: Span[]): AnalyzedSpan[] {
  const byId = new Map(
    spans.map((span) => [spanKey(span.traceId, span.spanId), span]),
  );
  const children = new Map<string, Span[]>();

  for (const span of spans) {
    if (!span.parentSpanId) continue;
    const parentKey = spanKey(span.traceId, span.parentSpanId);
    const bucket = children.get(parentKey) ?? [];
    bucket.push(span);
    children.set(parentKey, bucket);
  }

  const depthMemo = new Map<string, number>();

  return [...spans]
    .sort((a, b) => a.startMs - b.startMs || b.durationMs - a.durationMs)
    .map((span) => {
      const start = span.startMs;
      const end = span.startMs + span.durationMs;
      const directChildren =
        children.get(spanKey(span.traceId, span.spanId)) ?? [];

      const covered = unionLength(
        directChildren.map((child) => ({
          start: Math.max(start, child.startMs),
          end: Math.min(end, child.startMs + child.durationMs),
        })),
      );

      return {
        ...span,
        endMs: end,
        selfTimeMs: round(Math.max(0, span.durationMs - covered)),
        depth: depthOf(span, byId, depthMemo),
      };
    });
}

export function buildServiceGraph(spans: AnalyzedSpan[]): {
  services: ServiceNode[];
  edges: ServiceEdge[];
} {
  const byId = new Map(
    spans.map((span) => [spanKey(span.traceId, span.spanId), span]),
  );
  const serviceBuckets = new Map<string, AnalyzedSpan[]>();
  const edgeBuckets = new Map<string, AnalyzedSpan[]>();

  for (const span of spans) {
    const bucket = serviceBuckets.get(span.service) ?? [];
    bucket.push(span);
    serviceBuckets.set(span.service, bucket);

    if (!span.parentSpanId) continue;
    const parent = byId.get(spanKey(span.traceId, span.parentSpanId));
    if (!parent || parent.service === span.service) continue;

    const key = `${parent.service}->${span.service}`;
    const edges = edgeBuckets.get(key) ?? [];
    edges.push(span);
    edgeBuckets.set(key, edges);
  }

  const services = [...serviceBuckets.entries()]
    .map(([service, items]) => {
      const errors = items.filter((item) => item.status === "error").length;
      return {
        service,
        calls: items.length,
        errors,
        errorRate: round(errors / items.length),
        averageMs: round(
          items.reduce((sum, item) => sum + item.durationMs, 0) / items.length,
        ),
        p95Ms: round(percentile(items.map((item) => item.durationMs), 0.95)),
        selfTimeMs: round(
          items.reduce((sum, item) => sum + item.selfTimeMs, 0),
        ),
      };
    })
    .sort((a, b) => b.p95Ms - a.p95Ms);

  const edges = [...edgeBuckets.entries()]
    .map(([key, items]) => {
      const [source = "", target = ""] = key.split("->");
      return {
        source,
        target,
        calls: items.length,
        errors: items.filter((item) => item.status === "error").length,
        averageMs: round(
          items.reduce((sum, item) => sum + item.durationMs, 0) / items.length,
        ),
      };
    })
    .sort((a, b) => b.calls - a.calls);

  return { services, edges };
}

export function rankHypotheses(
  services: ServiceNode[],
): IncidentHypothesis[] {
  const maxP95 = Math.max(1, ...services.map((service) => service.p95Ms));
  const maxSelf = Math.max(1, ...services.map((service) => service.selfTimeMs));

  return services
    .map((service) => {
      const latencyWeight = service.p95Ms / maxP95;
      const selfTimeWeight = service.selfTimeMs / maxSelf;
      const score =
        service.errorRate * 55 + latencyWeight * 30 + selfTimeWeight * 15;

      const evidence: string[] = [];
      if (service.errors > 0) {
        evidence.push(
          `${service.errors}/${service.calls} spans ended in error (${round(service.errorRate * 100, 1)}%).`,
        );
      }
      if (service.p95Ms >= 500) {
        evidence.push(`p95 latency reached ${round(service.p95Ms, 1)} ms.`);
      }
      if (service.selfTimeMs > 0) {
        evidence.push(
          `${round(service.selfTimeMs, 1)} ms were spent inside the service after removing overlapping child calls.`,
        );
      }

      const confidence =
        score >= 65 ? "high" : score >= 35 ? "medium" : "low";

      return {
        service: service.service,
        score: round(score, 1),
        confidence,
        evidence:
          evidence.length > 0
            ? evidence
            : ["No strong error or latency signal was detected."],
      } satisfies IncidentHypothesis;
    })
    .sort((a, b) => b.score - a.score)
    .slice(0, 5);
}

export function analyzeTrace(spans: Span[]): TraceAnalysis {
  if (spans.length === 0) {
    throw new Error("Cannot analyze an empty trace.");
  }

  const traceIds = new Set(spans.map((span) => span.traceId));
  if (traceIds.size !== 1) {
    throw new Error("analyzeTrace expects spans from exactly one trace.");
  }

  const analyzed = analyzeSpans(spans);
  const startedAtMs = Math.min(...analyzed.map((span) => span.startMs));
  const endedAtMs = Math.max(...analyzed.map((span) => span.endMs));
  const graph = buildServiceGraph(analyzed);
  const root =
    analyzed.find((span) => span.parentSpanId === null) ?? analyzed[0];

  return {
    traceId: analyzed[0]?.traceId ?? "",
    durationMs: round(endedAtMs - startedAtMs),
    startedAtMs,
    endedAtMs,
    status: analyzed.some((span) => span.status === "error")
      ? "error"
      : root?.status ?? "unset",
    spans: analyzed,
    services: graph.services,
    edges: graph.edges,
    hypotheses: rankHypotheses(graph.services),
  };
}

export function analyzeWindow(spans: Span[]) {
  const analyzed = analyzeSpans(spans);
  const graph = buildServiceGraph(analyzed);
  return {
    services: graph.services,
    edges: graph.edges,
    hypotheses: rankHypotheses(graph.services),
  };
}
