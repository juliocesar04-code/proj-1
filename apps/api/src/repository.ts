import postgres from "postgres";
import type { Span } from "./types.js";

const databaseUrl =
  process.env.DATABASE_URL ??
  "postgres://traceforge:traceforge@localhost:5432/traceforge";

export const sql = postgres(databaseUrl, {
  max: 10,
  idle_timeout: 20,
  connect_timeout: 10,
});

type SpanRow = {
  trace_id: string;
  span_id: string;
  parent_span_id: string | null;
  service: string;
  operation: string;
  start_ms: string | number;
  duration_ms: string | number;
  status: Span["status"];
  attributes: Span["attributes"];
};

function fromRow(row: SpanRow): Span {
  return {
    traceId: row.trace_id,
    spanId: row.span_id,
    parentSpanId: row.parent_span_id,
    service: row.service,
    operation: row.operation,
    startMs: Number(row.start_ms),
    durationMs: Number(row.duration_ms),
    status: row.status,
    attributes: row.attributes ?? {},
  };
}

export async function saveSpans(spans: Span[]): Promise<void> {
  if (spans.length === 0) return;

  const rows = spans.map((span) => ({
    trace_id: span.traceId,
    span_id: span.spanId,
    parent_span_id: span.parentSpanId,
    service: span.service,
    operation: span.operation,
    start_ms: span.startMs,
    duration_ms: span.durationMs,
    status: span.status,
    attributes: span.attributes,
  }));

  await sql`
    INSERT INTO spans ${sql(
      rows,
      "trace_id",
      "span_id",
      "parent_span_id",
      "service",
      "operation",
      "start_ms",
      "duration_ms",
      "status",
      "attributes",
    )}
    ON CONFLICT (trace_id, span_id)
    DO UPDATE SET
      parent_span_id = EXCLUDED.parent_span_id,
      service = EXCLUDED.service,
      operation = EXCLUDED.operation,
      start_ms = EXCLUDED.start_ms,
      duration_ms = EXCLUDED.duration_ms,
      status = EXCLUDED.status,
      attributes = EXCLUDED.attributes
  `;
}

export async function getTrace(traceId: string): Promise<Span[]> {
  const rows = await sql<SpanRow[]>`
    SELECT
      trace_id,
      span_id,
      parent_span_id,
      service,
      operation,
      start_ms,
      duration_ms,
      status,
      attributes
    FROM spans
    WHERE trace_id = ${traceId}
    ORDER BY start_ms ASC, duration_ms DESC
  `;

  return rows.map(fromRow);
}

export async function getRecentSpans(limit = 3_000): Promise<Span[]> {
  const bounded = Math.min(Math.max(limit, 1), 10_000);
  const rows = await sql<SpanRow[]>`
    SELECT
      trace_id,
      span_id,
      parent_span_id,
      service,
      operation,
      start_ms,
      duration_ms,
      status,
      attributes
    FROM spans
    ORDER BY start_ms DESC
    LIMIT ${bounded}
  `;

  return rows.map(fromRow);
}

export async function clearSpans(): Promise<void> {
  await sql`TRUNCATE TABLE spans`;
}
