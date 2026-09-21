# TraceForge

TraceForge is an evidence-first incident-investigation workbench for distributed systems. It ingests distributed traces, reconstructs service-to-service calls, calculates real self-time, ranks incident hypotheses from measurable signals, and lets engineers replay, compare and export a request investigation from one interface.

## Why this project exists

Production incidents are rarely caused by the service with the longest wall-clock duration alone. Parallel child calls, downstream failures, lock contention and cache degradation can make the obvious answer wrong.

TraceForge focuses on reconstructing what actually happened:

- trace ingestion from the native API or OpenTelemetry OTLP/HTTP JSON
- service dependency reconstruction
- overlap-safe self-time calculation
- evidence-backed incident hypotheses
- interactive service forensics
- request replay and waterfall diagnostics
- trace-to-trace comparison
- SLO and error-budget signals
- exportable incident reports
- shareable trace deep links

## Product experience

The web app includes:

- live topology with inspectable service nodes
- p95, average latency, calls, error rate and self-time per service
- operational-health strip with availability and error-budget burn
- latency trend sparkline
- deterministic failure scenarios
- trace explorer with filtering
- animated waterfall replay
- slowest-span, self-time, concurrency and error diagnostics
- differential trace comparison
- Markdown incident-report export
- Cmd/Ctrl + K command center
- 14 localized languages including Brazilian Portuguese
- RTL layout support for Arabic
- responsive desktop/tablet/mobile layouts
- installable PWA with offline shell caching

## Architecture

```
browser
  |
  v
apps/web  <---- SSE ----  apps/api
                         /    |     \
                     OTLP  analyze  persist
                                    |
                                 PostgreSQL
```

Ingestion, persistence and analysis are intentionally separated. The analysis functions can be tested without the browser or database, and the UI remains focused on investigation and visualization.

## Native OpenTelemetry ingestion

TraceForge accepts OTLP/HTTP JSON on both:

```
POST /v1/traces
POST /api/otlp/v1/traces
```

Example collector/exporter target:

```
http://localhost:4000/v1/traces
```

A minimal OTLP JSON payload follows the standard `resourceSpans -> scopeSpans -> spans` structure. TraceForge extracts `service.name`, span timing, status, scope metadata and primitive attributes, then maps them into its internal trace model.

The parser also supports the legacy `instrumentationLibrarySpans` shape.

## Built-in scenarios

1. Stable traffic
2. Payment timeout
3. Database lock
4. Cache degradation

The Vercel demo runs these scenarios entirely in the browser so the portfolio build remains interactive without requiring a hosted database.

## Local development

Requirements: Node.js 22+, npm 10+, Docker.

```bash
npm install
docker compose up -d
npm run db:init
npm run dev
```

Web: http://localhost:5173  
API: http://localhost:4000

## Quality gates

```bash
npm run typecheck
npm test
npm run build
```

GitHub Actions runs all three gates on every commit.

## Repository structure

```
apps/
  api/
    ingestion
    OTLP parsing
    persistence
    analysis
    scenario generation
  web/
    topology
    service forensics
    trace explorer
    replay
    comparison
    reporting
    i18n
```

## Engineering details worth reviewing

- parent/child relationships are keyed by `traceId + spanId`, avoiding cross-trace collisions
- self-time subtracts the union of overlapping child intervals instead of double-counting parallel calls
- OTLP nanosecond timestamps are converted without first coercing the full value to an unsafe JavaScript integer
- incident hypotheses combine error rate, normalized p95 latency and self-time
- demo scenarios are deterministic enough for reproducible review
- API ingestion is capped and validated
- the service graph and analysis pipeline are independent from rendering
- reduced-motion preferences and RTL direction are supported
- trace URLs preserve the selected trace for sharing and reloads

## Status

Implemented and passing CI:

- PostgreSQL-backed span ingestion
- native span API
- OTLP/HTTP JSON ingestion
- overlap-safe trace analysis
- service graph reconstruction
- evidence-ranked hypotheses
- SSE refresh
- interactive service map
- service inspector
- trace search and filters
- replayable waterfall
- trace diagnostics
- trace comparison
- SLO/error-budget surface
- exportable incident reports
- command palette
- multilingual UI
- PWA/offline shell
- responsive design
- automated type checking, tests and production builds

---

## Also in this repository

### CorvoDB (`corvodb/`)

A relational database engine written from scratch in Go, with no external
dependencies: 4 KB paged file, B+ tree, write-ahead log with crash recovery,
its own SQL parser, a planner that picks indexes, and an iterator-model
executor. It is a separate project with its own Go module and its own CI
workflow; see [corvodb/README.md](corvodb/README.md).
