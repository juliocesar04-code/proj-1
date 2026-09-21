import { randomUUID } from "node:crypto";
import type { Span, SpanStatus } from "./types.js";

export type ScenarioId =
  | "stable"
  | "payment-timeout"
  | "database-lock"
  | "cache-degradation";

export const scenarioIds: ScenarioId[] = [
  "stable",
  "payment-timeout",
  "database-lock",
  "cache-degradation",
];

function seeded(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (state * 1_664_525 + 1_013_904_223) >>> 0;
    return state / 0xffffffff;
  };
}

function jitter(random: () => number, value: number, spread: number) {
  return Math.max(1, value + (random() - 0.5) * spread);
}

function makeSpan(
  input: Omit<Span, "attributes"> & { attributes?: Span["attributes"] },
): Span {
  return {
    ...input,
    attributes: input.attributes ?? {},
  };
}

interface TraceProfile {
  rootDuration: number;
  rootStatus: SpanStatus;
  paymentDuration: number;
  paymentStatus: SpanStatus;
  dbDuration: number;
  dbStatus: SpanStatus;
  redisDuration: number;
  redisStatus: SpanStatus;
  catalogDuration: number;
}

export function generateScenario(
  scenario: ScenarioId,
  traceCount = 24,
): Span[] {
  const seed =
    scenario === "stable"
      ? 11
      : scenario === "payment-timeout"
        ? 29
        : scenario === "database-lock"
          ? 47
          : 71;

  const random = seeded(seed);
  const now = Date.now();
  const spans: Span[] = [];

  for (let index = 0; index < traceCount; index += 1) {
    const traceId = randomUUID();
    const base = now - (traceCount - index) * 1_350;
    const faulty =
      scenario === "stable"
        ? false
        : scenario === "payment-timeout"
          ? index % 3 === 0
          : scenario === "database-lock"
            ? index % 4 === 0
            : index % 2 === 0;

    const profile: TraceProfile = {
      rootDuration: jitter(random, 640, 120),
      rootStatus: "ok",
      paymentDuration: jitter(random, 260, 70),
      paymentStatus: "ok",
      dbDuration: jitter(random, 62, 24),
      dbStatus: "ok",
      redisDuration: jitter(random, 18, 10),
      redisStatus: "ok",
      catalogDuration: jitter(random, 145, 35),
    };

    if (faulty && scenario === "payment-timeout") {
      profile.paymentDuration = jitter(random, 2_250, 220);
      profile.paymentStatus = "error";
      profile.rootDuration = profile.paymentDuration + 170;
      profile.rootStatus = "error";
    }

    if (faulty && scenario === "database-lock") {
      profile.dbDuration = jitter(random, 1_650, 180);
      profile.dbStatus = "error";
      profile.paymentDuration = profile.dbDuration + 120;
      profile.paymentStatus = "error";
      profile.rootDuration = profile.paymentDuration + 190;
      profile.rootStatus = "error";
    }

    if (faulty && scenario === "cache-degradation") {
      profile.redisDuration = jitter(random, 720, 130);
      profile.redisStatus = "error";
      profile.catalogDuration = profile.redisDuration + 150;
      profile.rootDuration = profile.catalogDuration + 390;
    }

    const rootId = randomUUID();
    const authId = randomUUID();
    const catalogId = randomUUID();
    const redisId = randomUUID();
    const cartId = randomUUID();
    const paymentId = randomUUID();
    const dbId = randomUUID();

    spans.push(
      makeSpan({
        traceId,
        spanId: rootId,
        parentSpanId: null,
        service: "edge-gateway",
        operation: "POST /checkout",
        startMs: base,
        durationMs: profile.rootDuration,
        status: profile.rootStatus,
        attributes: {
          "http.method": "POST",
          "http.route": "/checkout",
          scenario,
        },
      }),
      makeSpan({
        traceId,
        spanId: authId,
        parentSpanId: rootId,
        service: "auth",
        operation: "verify-session",
        startMs: base + 18,
        durationMs: jitter(random, 42, 14),
        status: "ok",
      }),
      makeSpan({
        traceId,
        spanId: catalogId,
        parentSpanId: rootId,
        service: "catalog",
        operation: "load-products",
        startMs: base + 72,
        durationMs: profile.catalogDuration,
        status: "ok",
      }),
      makeSpan({
        traceId,
        spanId: redisId,
        parentSpanId: catalogId,
        service: "redis",
        operation: "GET product:batch",
        startMs: base + 88,
        durationMs: profile.redisDuration,
        status: profile.redisStatus,
        attributes: {
          "cache.hit": !(faulty && scenario === "cache-degradation"),
        },
      }),
      makeSpan({
        traceId,
        spanId: cartId,
        parentSpanId: rootId,
        service: "cart",
        operation: "price-cart",
        startMs: base + 94,
        durationMs: jitter(random, 128, 42),
        status: "ok",
      }),
      makeSpan({
        traceId,
        spanId: paymentId,
        parentSpanId: rootId,
        service: "payment",
        operation: "authorize",
        startMs: base + 250,
        durationMs: profile.paymentDuration,
        status: profile.paymentStatus,
        attributes: {
          provider: "sandbox-payments",
        },
      }),
      makeSpan({
        traceId,
        spanId: dbId,
        parentSpanId: paymentId,
        service: "postgres",
        operation: "SELECT account FOR UPDATE",
        startMs: base + 282,
        durationMs: profile.dbDuration,
        status: profile.dbStatus,
        attributes: {
          "db.system": "postgresql",
        },
      }),
    );
  }

  return spans;
}
