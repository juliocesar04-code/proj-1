export type SpanStatus = "ok" | "error" | "unset";

export interface Span {
  traceId: string;
  spanId: string;
  parentSpanId: string | null;
  service: string;
  operation: string;
  startMs: number;
  durationMs: number;
  status: SpanStatus;
  attributes: Record<string, string | number | boolean>;
}

export interface AnalyzedSpan extends Span {
  endMs: number;
  selfTimeMs: number;
  depth: number;
}

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

export interface TraceSummary {
  traceId: string;
  startedAtMs: number;
  durationMs: number;
  spanCount: number;
  errorCount: number;
  rootService: string;
  rootOperation: string;
}
