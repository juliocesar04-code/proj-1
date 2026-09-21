import type { ServerResponse } from "node:http";
import cors from "@fastify/cors";
import Fastify from "fastify";
import { z } from "zod";
import { analyzeTrace, analyzeWindow } from "./analyze.js";
import {
  clearSpans,
  getRecentSpans,
  getTrace,
  saveSpans,
  sql,
} from "./repository.js";
import {
  generateScenario,
  type ScenarioId,
} from "./scenarios.js";
import type { Span, TraceSummary } from "./types.js";

const app = Fastify({
  logger: {
    level: process.env.LOG_LEVEL ?? "info",
  },
});

const corsOrigin = process.env.CORS_ORIGIN ?? "http://localhost:5173";
await app.register(cors, {
  origin:
    corsOrigin === "*"
      ? true
      : corsOrigin.split(",").map((origin) => origin.trim()),
});

const attributeValueSchema = z.union([
  z.string(),
  z.number(),
  z.boolean(),
]);

const spanSchema = z.object({
  traceId: z.string().min(1).max(128),
  spanId: z.string().min(1).max(128),
  parentSpanId: z.string().min(1).max(128).nullable(),
  service: z.string().min(1).max(120),
  operation: z.string().min(1).max(240),
  startMs: z.number().finite().nonnegative(),
  durationMs: z.number().finite().nonnegative(),
  status: z.enum(["ok", "error", "unset"]),
  attributes: z.record(attributeValueSchema).default({}),
});

const ingestSchema = z.array(spanSchema).min(1).max(5_000);
const scenarioSchema = z.enum([
  "stable",
  "payment-timeout",
  "database-lock",
  "cache-degradation",
]);

const eventClients = new Set<ServerResponse>();

function broadcast(event: string, payload: unknown) {
  const frame =
    "event: " +
    event +
    "\n" +
    "data: " +
    JSON.stringify(payload) +
    "\n\n";

  for (const client of eventClients) {
    if (client.destroyed || client.writableEnded) {
      eventClients.delete(client);
      continue;
    }
    client.write(frame);
  }
}

function summarizeTraces(spans: Span[]): TraceSummary[] {
  const grouped = new Map<string, Span[]>();

  for (const span of spans) {
    const bucket = grouped.get(span.traceId) ?? [];
    bucket.push(span);
    grouped.set(span.traceId, bucket);
  }

  return [...grouped.entries()]
    .map(([traceId, items]) => {
      const sorted = [...items].sort(
        (a, b) => a.startMs - b.startMs || b.durationMs - a.durationMs,
      );
      const startedAtMs = Math.min(...items.map((item) => item.startMs));
      const endedAtMs = Math.max(
        ...items.map((item) => item.startMs + item.durationMs),
      );
      const root =
        sorted.find((item) => item.parentSpanId === null) ?? sorted[0];

      return {
        traceId,
        startedAtMs,
        durationMs: Number((endedAtMs - startedAtMs).toFixed(2)),
        spanCount: items.length,
        errorCount: items.filter((item) => item.status === "error").length,
        rootService: root?.service ?? "unknown",
        rootOperation: root?.operation ?? "unknown",
      };
    })
    .sort((a, b) => b.startedAtMs - a.startedAtMs);
}

app.get("/health", async () => {
  await sql`SELECT 1`;
  return {
    ok: true,
    service: "traceforge-api",
    time: new Date().toISOString(),
  };
});

app.get("/api/events", async (request, reply) => {
  reply.hijack();
  const raw = reply.raw;

  raw.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache, no-transform",
    Connection: "keep-alive",
    "X-Accel-Buffering": "no",
  });

  raw.write(
    "event: ready\ndata: " +
      JSON.stringify({ connectedAt: Date.now() }) +
      "\n\n",
  );

  eventClients.add(raw);

  const heartbeat = setInterval(() => {
    if (!raw.destroyed && !raw.writableEnded) {
      raw.write(": heartbeat\n\n");
    }
  }, 20_000);

  request.raw.on("close", () => {
    clearInterval(heartbeat);
    eventClients.delete(raw);
  });

  return reply;
});

app.post("/api/spans", async (request, reply) => {
  const parsed = ingestSchema.safeParse(request.body);

  if (!parsed.success) {
    return reply.code(400).send({
      error: "invalid_span_batch",
      details: parsed.error.flatten(),
    });
  }

  await saveSpans(parsed.data);

  const traceIds = [...new Set(parsed.data.map((span) => span.traceId))];
  broadcast("ingest", {
    spanCount: parsed.data.length,
    traceCount: traceIds.length,
    at: Date.now(),
  });

  return reply.code(202).send({
    accepted: parsed.data.length,
    traces: traceIds.length,
  });
});

app.get("/api/overview", async () => {
  const spans = await getRecentSpans(3_000);
  const window = analyzeWindow(spans);
  const traceCount = new Set(spans.map((span) => span.traceId)).size;
  const errorCount = spans.filter((span) => span.status === "error").length;

  return {
    updatedAt: Date.now(),
    totals: {
      spans: spans.length,
      traces: traceCount,
      services: window.services.length,
      errors: errorCount,
      errorRate:
        spans.length === 0
          ? 0
          : Number((errorCount / spans.length).toFixed(4)),
    },
    ...window,
  };
});

app.get("/api/traces", async (request) => {
  const query = z
    .object({
      limit: z.coerce.number().int().min(1).max(200).default(80),
    })
    .parse(request.query);

  const spans = await getRecentSpans(10_000);
  return {
    traces: summarizeTraces(spans).slice(0, query.limit),
  };
});

app.get("/api/traces/:traceId", async (request, reply) => {
  const params = z
    .object({ traceId: z.string().min(1).max(128) })
    .parse(request.params);

  const spans = await getTrace(params.traceId);
  if (spans.length === 0) {
    return reply.code(404).send({
      error: "trace_not_found",
    });
  }

  return analyzeTrace(spans);
});

app.post("/api/scenarios/:scenario/run", async (request, reply) => {
  const params = z
    .object({ scenario: scenarioSchema })
    .parse(request.params);

  const body = z
    .object({
      traces: z.coerce.number().int().min(4).max(120).default(24),
    })
    .default({})
    .parse(request.body ?? {});

  const spans = generateScenario(params.scenario as ScenarioId, body.traces);
  await saveSpans(spans);

  const traceIds = [...new Set(spans.map((span) => span.traceId))];
  broadcast("scenario", {
    scenario: params.scenario,
    spanCount: spans.length,
    traceCount: traceIds.length,
    at: Date.now(),
  });

  return reply.code(201).send({
    scenario: params.scenario,
    spans: spans.length,
    traces: traceIds.length,
  });
});

app.delete("/api/data", async (_request, reply) => {
  await clearSpans();
  broadcast("reset", { at: Date.now() });
  return reply.code(204).send();
});

app.setErrorHandler((error, _request, reply) => {
  app.log.error(error);
  if (error instanceof z.ZodError) {
    return reply.code(400).send({
      error: "invalid_request",
      details: error.flatten(),
    });
  }

  return reply.code(500).send({
    error: "internal_error",
    message:
      process.env.NODE_ENV === "production"
        ? "Unexpected server error."
        : error instanceof Error
          ? error.message
          : "Unknown server error.",
  });
});

const port = Number(process.env.PORT ?? 4000);

await app.listen({
  port,
  host: "0.0.0.0",
});

async function shutdown(signal: string) {
  app.log.info({ signal }, "shutting down");
  for (const client of eventClients) {
    client.end();
  }
  await app.close();
  await sql.end({ timeout: 5 });
}

process.once("SIGINT", () => {
  void shutdown("SIGINT");
});

process.once("SIGTERM", () => {
  void shutdown("SIGTERM");
});
