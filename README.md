# TraceForge

TraceForge is an incident-investigation workbench for distributed systems. It ingests trace spans, reconstructs service-to-service calls, calculates real self-time, ranks evidence-backed incident hypotheses, and lets engineers replay a request across the full timeline.

## What it demonstrates

- Trace ingestion with validation and persistence
- Service dependency reconstruction
- Self-time calculation that removes overlapping child intervals
- Trace timeline and request replay
- Live service map and health metrics
- Evidence-backed incident hypotheses instead of opaque "AI says so" answers
- Reproducible failure scenarios for demos and testing
- Responsive UI for desktop, tablet, and mobile

## Architecture

```
browser
  |
  v
apps/web  <---- SSE ----  apps/api
                         /   |    \
                    ingest analyze persist
                               |
                            PostgreSQL
```

The API keeps ingestion, persistence and analysis separate. That makes the analysis code testable without a browser or database and keeps the UI focused on visualization.

## Built-in scenarios

1. Stable traffic
2. Payment timeout
3. Database lock
4. Cache degradation

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

## Repository structure

```
apps/
  api/   ingestion, persistence, analysis and scenario generation
  web/   topology, traces, timeline and replay UI
```

## Status

Core trace model, persistence, analysis, scenario engine and responsive explorer are being implemented first. Exportable incident reports and OpenTelemetry ingestion are the next milestones.
