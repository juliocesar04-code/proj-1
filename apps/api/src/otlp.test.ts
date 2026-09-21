import { describe, expect, it } from "vitest";
import { parseOtlpJson } from "./otlp.js";

describe("parseOtlpJson", () => {
  it("maps OTLP resource and span fields into TraceForge spans", () => {
    const spans = parseOtlpJson({
      resourceSpans: [
        {
          resource: {
            attributes: [
              {
                key: "service.name",
                value: { stringValue: "checkout" },
              },
              {
                key: "deployment.environment",
                value: { stringValue: "prod" },
              },
            ],
          },
          scopeSpans: [
            {
              scope: { name: "http.instrumentation" },
              spans: [
                {
                  traceId: "abc123",
                  spanId: "span1",
                  parentSpanId: "",
                  name: "POST /checkout",
                  startTimeUnixNano: "1789960000000000000",
                  endTimeUnixNano: "1789960000125000000",
                  status: { code: "STATUS_CODE_ERROR" },
                  attributes: [
                    {
                      key: "http.response.status_code",
                      value: { intValue: "503" },
                    },
                  ],
                },
              ],
            },
          ],
        },
      ],
    });

    expect(spans).toHaveLength(1);
    expect(spans[0]).toMatchObject({
      traceId: "abc123",
      spanId: "span1",
      parentSpanId: null,
      service: "checkout",
      operation: "POST /checkout",
      durationMs: 125,
      status: "error",
      attributes: {
        "service.name": "checkout",
        "deployment.environment": "prod",
        "otel.scope.name": "http.instrumentation",
        "http.response.status_code": 503,
      },
    });
  });

  it("supports nested OTLP values and legacy instrumentation groups", () => {
    const spans = parseOtlpJson({
      resourceSpans: [
        {
          resource: {
            attributes: [
              {
                key: "service.name",
                value: { stringValue: "worker" },
              },
            ],
          },
          instrumentationLibrarySpans: [
            {
              instrumentationLibrary: { name: "legacy-lib" },
              spans: [
                {
                  traceId: "trace-2",
                  spanId: "span-2",
                  parentSpanId: "parent-1",
                  name: "consume",
                  startTimeUnixNano: "1000000",
                  endTimeUnixNano: "3500000",
                  status: { code: 1 },
                  attributes: [
                    {
                      key: "retry.count",
                      value: { intValue: 2 },
                    },
                    {
                      key: "flags",
                      value: {
                        arrayValue: {
                          values: [
                            { stringValue: "a" },
                            { boolValue: true },
                          ],
                        },
                      },
                    },
                  ],
                },
              ],
            },
          ],
        },
      ],
    });

    expect(spans[0]?.durationMs).toBe(2.5);
    expect(spans[0]?.status).toBe("ok");
    expect(spans[0]?.attributes["retry.count"]).toBe(2);
    expect(spans[0]?.attributes.flags).toBe('["a",true]');
  });

  it("rejects payloads without valid spans", () => {
    expect(() => parseOtlpJson({ resourceSpans: [] })).toThrow(
      "did not contain any valid spans",
    );
  });
});
