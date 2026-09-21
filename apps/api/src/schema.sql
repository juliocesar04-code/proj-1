CREATE TABLE IF NOT EXISTS spans (
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  parent_span_id TEXT,
  service TEXT NOT NULL,
  operation TEXT NOT NULL,
  start_ms BIGINT NOT NULL,
  duration_ms DOUBLE PRECISION NOT NULL CHECK (duration_ms >= 0),
  status TEXT NOT NULL CHECK (status IN ('ok', 'error', 'unset')),
  attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
  ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX IF NOT EXISTS spans_started_at_idx
  ON spans (start_ms DESC);

CREATE INDEX IF NOT EXISTS spans_service_idx
  ON spans (service, start_ms DESC);

CREATE INDEX IF NOT EXISTS spans_trace_idx
  ON spans (trace_id, start_ms ASC);
