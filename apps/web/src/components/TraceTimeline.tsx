import { Pause, Play, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { TraceAnalysis } from "../api.js";
import { useI18n } from "../i18n.js";

interface TraceTimelineProps {
  trace: TraceAnalysis;
}

export function TraceTimeline({ trace }: TraceTimelineProps) {
  const { t, formatNumber } = useI18n();
  const [progress, setProgress] = useState(0);
  const [playing, setPlaying] = useState(false);

  useEffect(() => {
    setProgress(0);
    setPlaying(false);
  }, [trace.traceId]);

  useEffect(() => {
    if (!playing) return undefined;

    const timer = window.setInterval(() => {
      setProgress((current) => {
        const next = Math.min(100, current + 1.25);
        if (next >= 100) {
          window.setTimeout(() => setPlaying(false), 0);
        }
        return next;
      });
    }, 45);

    return () => window.clearInterval(timer);
  }, [playing]);

  const duration = Math.max(1, trace.durationMs);
  const playheadMs =
    trace.startedAtMs + (duration * progress) / 100;

  const rows = useMemo(
    () =>
      trace.spans.map((span) => ({
        ...span,
        left:
          ((span.startMs - trace.startedAtMs) / duration) * 100,
        width: Math.max(
          1.25,
          (span.durationMs / duration) * 100,
        ),
      })),
    [duration, trace.spans, trace.startedAtMs],
  );

  const diagnostics = useMemo(() => {
    const slowest = [...trace.spans].sort(
      (a, b) => b.durationMs - a.durationMs,
    )[0];
    const selfLeader = [...trace.spans].sort(
      (a, b) => b.selfTimeMs - a.selfTimeMs,
    )[0];

    const events = trace.spans.flatMap((span) => [
      { at: span.startMs, delta: 1 },
      { at: span.endMs, delta: -1 },
    ]);
    events.sort(
      (a, b) => a.at - b.at || a.delta - b.delta,
    );

    let active = 0;
    let peakConcurrency = 0;
    for (const event of events) {
      active += event.delta;
      peakConcurrency = Math.max(peakConcurrency, active);
    }

    return {
      slowest,
      selfLeader,
      peakConcurrency,
      errors: trace.spans.filter(
        (span) => span.status === "error",
      ).length,
    };
  }, [trace.spans]);

  return (
    <section className="timeline-shell" aria-label={t("timeline.aria")}>
      <div className="timeline-toolbar">
        <div>
          <p className="eyebrow">{t("timeline.requestReplay")}</p>
          <div className="timeline-title-row">
            <strong>
              {formatNumber(trace.durationMs, {
                minimumFractionDigits: 1,
                maximumFractionDigits: 1,
              })}{" "}
              ms
            </strong>
            <span>
              {t("timeline.spansServices", {
                spans: formatNumber(trace.spans.length),
                services: formatNumber(trace.services.length),
              })}
            </span>
          </div>
        </div>

        <div className="timeline-controls">
          <button
            type="button"
            className="icon-button"
            onClick={() => {
              setProgress(0);
              setPlaying(false);
            }}
            aria-label={t("timeline.restart")}
          >
            <RotateCcw size={16} />
          </button>
          <button
            type="button"
            className="replay-button"
            onClick={() => {
              if (progress >= 100) setProgress(0);
              setPlaying((value) => !value);
            }}
          >
            {playing ? <Pause size={16} /> : <Play size={16} />}
            {playing ? t("timeline.pause") : t("timeline.replay")}
          </button>
        </div>
      </div>

      <div className="scrubber">
        <input
          aria-label={t("timeline.position")}
          type="range"
          min="0"
          max="100"
          step="0.1"
          value={progress}
          onChange={(event) => {
            setProgress(Number(event.target.value));
            setPlaying(false);
          }}
        />
        <div className="scrubber__meta">
          <span>0 ms</span>
          <span>
            {formatNumber((duration * progress) / 100, {
              maximumFractionDigits: 0,
            })}{" "}
            ms
          </span>
          <span>
            {formatNumber(duration, {
              maximumFractionDigits: 0,
            })}{" "}
            ms
          </span>
        </div>
      </div>

      <div className="timeline-diagnostics">
        <article>
          <small>{t("timeline.slowest")}</small>
          <strong>{diagnostics.slowest?.service ?? "—"}</strong>
          <span>
            {diagnostics.slowest
              ? formatNumber(diagnostics.slowest.durationMs, {
                  maximumFractionDigits: 0,
                }) + " ms"
              : "—"}
          </span>
        </article>
        <article>
          <small>{t("timeline.selfLeader")}</small>
          <strong>{diagnostics.selfLeader?.service ?? "—"}</strong>
          <span>
            {diagnostics.selfLeader
              ? formatNumber(diagnostics.selfLeader.selfTimeMs, {
                  maximumFractionDigits: 0,
                }) + " ms"
              : "—"}
          </span>
        </article>
        <article>
          <small>{t("timeline.peakConcurrency")}</small>
          <strong>{formatNumber(diagnostics.peakConcurrency)}×</strong>
          <span>{t("timeline.parallelSpans")}</span>
        </article>
        <article>
          <small>{t("timeline.errors")}</small>
          <strong>{formatNumber(diagnostics.errors)}</strong>
          <span>{t("timeline.errorSpans")}</span>
        </article>
      </div>

      <div className="timeline-list">
        {rows.map((span) => {
          const active =
            span.startMs <= playheadMs &&
            span.endMs >= playheadMs;
          const passed = span.startMs <= playheadMs;

          return (
            <div
              className={[
                "timeline-row",
                active ? "timeline-row--active" : "",
                passed ? "timeline-row--passed" : "",
              ]
                .filter(Boolean)
                .join(" ")}
              key={span.spanId}
            >
              <div
                className="timeline-row__label"
                style={{
                  paddingInlineStart:
                    10 + Math.min(span.depth, 5) * 12,
                  paddingInlineEnd: 8,
                }}
              >
                <span
                  className={[
                    "status-dot",
                    span.status === "error"
                      ? "status-dot--error"
                      : "status-dot--ok",
                  ].join(" ")}
                />
                <div>
                  <strong>{span.service}</strong>
                  <small>{span.operation}</small>
                </div>
              </div>

              <div className="timeline-track">
                <div
                  className={[
                    "timeline-bar",
                    span.status === "error"
                      ? "timeline-bar--error"
                      : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  style={{
                    left: span.left + "%",
                    width: span.width + "%",
                  }}
                  title={
                    span.service +
                    " · " +
                    formatNumber(span.durationMs, {
                      minimumFractionDigits: 1,
                      maximumFractionDigits: 1,
                    }) +
                    " ms"
                  }
                />
                <div
                  className="timeline-playhead"
                  style={{ left: progress + "%" }}
                />
              </div>

              <div className="timeline-row__numbers">
                <span>
                  {formatNumber(span.durationMs, {
                    maximumFractionDigits: 0,
                  })}{" "}
                  ms
                </span>
                <small>
                  {t("timeline.self", {
                    value: formatNumber(span.selfTimeMs, {
                      maximumFractionDigits: 0,
                    }) + " ms",
                  })}
                </small>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
