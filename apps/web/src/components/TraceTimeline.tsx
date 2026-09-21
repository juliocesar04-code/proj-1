import { Pause, Play, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { TraceAnalysis } from "../api.js";

interface TraceTimelineProps {
  trace: TraceAnalysis;
}

export function TraceTimeline({ trace }: TraceTimelineProps) {
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

  return (
    <section className="timeline-shell" aria-label="Trace replay timeline">
      <div className="timeline-toolbar">
        <div>
          <p className="eyebrow">Request replay</p>
          <div className="timeline-title-row">
            <strong>{trace.durationMs.toFixed(1)} ms</strong>
            <span>
              {trace.spans.length} spans · {trace.services.length} services
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
            aria-label="Restart replay"
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
            {playing ? "Pause" : "Replay"}
          </button>
        </div>
      </div>

      <div className="scrubber">
        <input
          aria-label="Replay position"
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
          <span>{((duration * progress) / 100).toFixed(0)} ms</span>
          <span>{duration.toFixed(0)} ms</span>
        </div>
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
                  paddingLeft:
                    10 + Math.min(span.depth, 5) * 12,
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
                    span.durationMs.toFixed(1) +
                    " ms"
                  }
                />
                <div
                  className="timeline-playhead"
                  style={{ left: progress + "%" }}
                />
              </div>

              <div className="timeline-row__numbers">
                <span>{span.durationMs.toFixed(0)} ms</span>
                <small>{span.selfTimeMs.toFixed(0)} self</small>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
