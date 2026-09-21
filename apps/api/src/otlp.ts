import type { Span, SpanStatus } from "./types.js";

type JsonRecord = Record<string, unknown>;

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function toArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function nanoToMs(value: unknown): number {
  if (typeof value !== "string" && typeof value !== "number") {
    throw new Error("OTLP span is missing a valid nanosecond timestamp.");
  }

  const raw = String(value);
  if (!/^\d+$/.test(raw)) {
    throw new Error("OTLP timestamp must be an unsigned integer.");
  }

  const nanos = BigInt(raw);
  const whole = nanos / 1_000_000n;
  const remainder = nanos % 1_000_000n;
  return Number(whole) + Number(remainder) / 1_000_000;
}

function otlpValue(value: unknown): string | number | boolean | undefined {
  if (!isRecord(value)) return undefined;

  if (typeof value.stringValue === "string") return value.stringValue;
  if (typeof value.boolValue === "boolean") return value.boolValue;

  if (
    typeof value.intValue === "string" ||
    typeof value.intValue === "number"
  ) {
    const parsed = Number(value.intValue);
    return Number.isFinite(parsed) ? parsed : String(value.intValue);
  }

  if (typeof value.doubleValue === "number") return value.doubleValue;
  if (typeof value.bytesValue === "string") return value.bytesValue;

  if (isRecord(value.arrayValue)) {
    const values = toArray(value.arrayValue.values)
      .map((entry) => otlpValue(entry))
      .filter((entry) => entry !== undefined);
    return JSON.stringify(values);
  }

  if (isRecord(value.kvlistValue)) {
    const object: Record<string, string | number | boolean> = {};
    for (const entry of toArray(value.kvlistValue.values)) {
      if (!isRecord(entry) || typeof entry.key !== "string") continue;
      const parsed = otlpValue(entry.value);
      if (parsed !== undefined) object[entry.key] = parsed;
    }
    return JSON.stringify(object);
  }

  return undefined;
}

function parseAttributes(
  value: unknown,
): Record<string, string | number | boolean> {
  const attributes: Record<string, string | number | boolean> = {};

  for (const entry of toArray(value)) {
    if (!isRecord(entry) || typeof entry.key !== "string") continue;
    const parsed = otlpValue(entry.value);
    if (parsed !== undefined) attributes[entry.key] = parsed;
  }

  return attributes;
}

function statusFromOtlp(value: unknown): SpanStatus {
  if (!isRecord(value)) return "unset";
  const code = value.code;

  if (code === 2 || code === "2" || code === "STATUS_CODE_ERROR") {
    return "error";
  }

  if (code === 1 || code === "1" || code === "STATUS_CODE_OK") {
    return "ok";
  }

  return "unset";
}

function readServiceName(resource: unknown): string {
  if (!isRecord(resource)) return "unknown-service";
  const attributes = parseAttributes(resource.attributes);
  const service = attributes["service.name"];
  return typeof service === "string" && service.trim()
    ? service
    : "unknown-service";
}

function parseSpan(
  raw: unknown,
  service: string,
  resourceAttributes: Record<string, string | number | boolean>,
  scopeName: string | undefined,
): Span | null {
  if (!isRecord(raw)) return null;

  const traceId = typeof raw.traceId === "string" ? raw.traceId : "";
  const spanId = typeof raw.spanId === "string" ? raw.spanId : "";
  if (!traceId || !spanId) return null;

  const startMs = nanoToMs(raw.startTimeUnixNano);
  const endMs = nanoToMs(raw.endTimeUnixNano);

  if (endMs < startMs) {
    throw new Error("OTLP span end time precedes start time.");
  }

  const spanAttributes = parseAttributes(raw.attributes);
  const attributes = {
    ...resourceAttributes,
    ...spanAttributes,
    ...(scopeName ? { "otel.scope.name": scopeName } : {}),
  };

  return {
    traceId,
    spanId,
    parentSpanId:
      typeof raw.parentSpanId === "string" && raw.parentSpanId.length > 0
        ? raw.parentSpanId
        : null,
    service,
    operation:
      typeof raw.name === "string" && raw.name.trim()
        ? raw.name
        : "unnamed-span",
    startMs,
    durationMs: Number((endMs - startMs).toFixed(6)),
    status: statusFromOtlp(raw.status),
    attributes,
  };
}

export function parseOtlpJson(input: unknown): Span[] {
  if (!isRecord(input)) {
    throw new Error("OTLP payload must be a JSON object.");
  }

  const result: Span[] = [];

  for (const resourceSpan of toArray(input.resourceSpans)) {
    if (!isRecord(resourceSpan)) continue;

    const resourceAttributes = isRecord(resourceSpan.resource)
      ? parseAttributes(resourceSpan.resource.attributes)
      : {};
    const service = readServiceName(resourceSpan.resource);

    const groups = [
      ...toArray(resourceSpan.scopeSpans),
      ...toArray(resourceSpan.instrumentationLibrarySpans),
    ];

    for (const group of groups) {
      if (!isRecord(group)) continue;

      const scope =
        isRecord(group.scope) && typeof group.scope.name === "string"
          ? group.scope.name
          : isRecord(group.instrumentationLibrary) &&
              typeof group.instrumentationLibrary.name === "string"
            ? group.instrumentationLibrary.name
            : undefined;

      for (const rawSpan of toArray(group.spans)) {
        const span = parseSpan(
          rawSpan,
          service,
          resourceAttributes,
          scope,
        );
        if (span) result.push(span);
      }
    }
  }

  if (result.length === 0) {
    throw new Error("OTLP payload did not contain any valid spans.");
  }

  return result;
}
