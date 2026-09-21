import { describe, expect, it } from "vitest";
import { analyzeSpans, analyzeTrace } from "./analyze.js";
import type { Span } from "./types.js";

const base = {
  traceId: "trace-1",
  status: "ok" as const,
  attributes: {},
};

describe("trace analysis", () => {
  it("subtracts overlapping child intervals only once", () => {
    const spans: Span[] = [
      {
        ...base,
        spanId: "root",
        parentSpanId: null,
        service: "gateway",
        operation: "GET /",
        startMs: 0,
        durationMs: 100,
      },
      {
        ...base,
        spanId: "a",
        parentSpanId: "root",
        service: "catalog",
        operation: "fetch",
        startMs: 20,
        durationMs: 40,
      },
      {
        ...base,
        spanId: "b",
        parentSpanId: "root",
        service: "payment",
        operation: "authorize",
        startMs: 50,
        durationMs: 40,
      },
    ];

    const root = analyzeSpans(spans).find((span) => span.spanId === "root");
    expect(root?.selfTimeMs).toBe(30);
  });

  it("reconstructs service edges and ranks failing services", () => {
    const spans: Span[] = [
      {
        ...base,
        spanId: "root",
        parentSpanId: null,
        service: "gateway",
        operation: "POST /checkout",
        startMs: 0,
        durationMs: 1_300,
        status: "error",
      },
      {
        ...base,
        spanId: "payment",
        parentSpanId: "root",
        service: "payment",
        operation: "authorize",
        startMs: 100,
        durationMs: 1_150,
        status: "error",
      },
      {
        ...base,
        spanId: "db",
        parentSpanId: "payment",
        service: "postgres",
        operation: "SELECT account",
        startMs: 120,
        durationMs: 80,
      },
    ];

    const result = analyzeTrace(spans);
    expect(result.edges).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ source: "gateway", target: "payment" }),
        expect.objectContaining({ source: "payment", target: "postgres" }),
      ]),
    );
    expect(result.hypotheses[0]?.service).toBe("payment");
  });
});
