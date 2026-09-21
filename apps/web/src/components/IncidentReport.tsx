import {
  Check,
  Clipboard,
  Download,
  FileText,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import type {
  Overview,
  TraceAnalysis,
} from "../api.js";
import { useI18n } from "../i18n.js";

interface IncidentReportProps {
  overview: Overview;
  trace: TraceAnalysis | null;
  onClose: () => void;
}

export function IncidentReport({
  overview,
  trace,
  onClose,
}: IncidentReportProps) {
  const {
    t,
    formatNumber,
    formatTime,
    evidenceText,
  } = useI18n();
  const [copied, setCopied] = useState(false);

  const report = useMemo(() => {
    const hypothesis = overview.hypotheses[0];
    const service = hypothesis?.service ?? "—";
    const lines = [
      "# TraceForge — " + t("report.title"),
      "",
      "## " + t("report.summary"),
      "",
      "- " + t("report.generatedAt") + ": " + formatTime(Date.now()),
      "- " + t("metrics.traces") + ": " + formatNumber(overview.totals.traces),
      "- " + t("metrics.services") + ": " + formatNumber(overview.totals.services),
      "- " +
        t("metrics.errorRate") +
        ": " +
        formatNumber(overview.totals.errorRate * 100, {
          minimumFractionDigits: 1,
          maximumFractionDigits: 1,
        }) +
        "%",
      "- " + t("report.leadingService") + ": " + service,
    ];

    if (trace) {
      lines.push(
        "",
        "## " + t("report.selectedTrace"),
        "",
        "- Trace ID: " + trace.traceId,
        "- " +
          t("report.duration") +
          ": " +
          formatNumber(trace.durationMs, {
            maximumFractionDigits: 1,
          }) +
          " ms",
        "- " +
          t("report.status") +
          ": " +
          trace.status.toUpperCase(),
        "- " +
          t("report.spanCount") +
          ": " +
          formatNumber(trace.spans.length),
      );
    }

    if (hypothesis) {
      lines.push(
        "",
        "## " + t("report.evidence"),
        "",
        ...hypothesis.evidence.map(
          (line) => "- " + evidenceText(line),
        ),
      );
    }

    lines.push(
      "",
      "## " + t("report.serviceSnapshot"),
      "",
      "| " +
        t("metrics.services") +
        " | p95 | " +
        t("metrics.errorRate") +
        " | " +
        t("report.selfTime") +
        " |",
      "| --- | ---: | ---: | ---: |",
      ...overview.services.slice(0, 7).map(
        (item) =>
          "| " +
          item.service +
          " | " +
          formatNumber(item.p95Ms, {
            maximumFractionDigits: 1,
          }) +
          " ms | " +
          formatNumber(item.errorRate * 100, {
            maximumFractionDigits: 1,
          }) +
          "% | " +
          formatNumber(item.selfTimeMs, {
            maximumFractionDigits: 1,
          }) +
          " ms |",
      ),
      "",
      "> " + t("report.generatedBy"),
    );

    return lines.join("\n");
  }, [
    evidenceText,
    formatNumber,
    formatTime,
    overview,
    t,
    trace,
  ]);

  const copy = async () => {
    await navigator.clipboard.writeText(report);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  const download = () => {
    const blob = new Blob([report], {
      type: "text/markdown;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download =
      "traceforge-incident-" +
      new Date().toISOString().slice(0, 19).replace(/:/g, "-") +
      ".md";
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="overlay" role="presentation" onMouseDown={onClose}>
      <section
        className="drawer report-drawer"
        role="dialog"
        aria-modal="true"
        aria-label={t("report.title")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="drawer__header">
          <div>
            <p className="eyebrow">{t("report.forensics")}</p>
            <h2>{t("report.title")}</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label={t("report.close")}
          >
            <X size={17} />
          </button>
        </header>

        <div className="report-drawer__actions">
          <button type="button" className="secondary-button" onClick={() => void copy()}>
            {copied ? <Check size={15} /> : <Clipboard size={15} />}
            {copied ? t("report.copied") : t("report.copy")}
          </button>
          <button type="button" className="primary-button" onClick={download}>
            <Download size={15} />
            {t("report.download")}
          </button>
        </div>

        <div className="report-preview">
          <div className="report-preview__icon">
            <FileText size={20} />
          </div>
          <pre>{report}</pre>
        </div>
      </section>
    </div>
  );
}
