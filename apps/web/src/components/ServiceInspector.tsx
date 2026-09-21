import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Clock3,
  Gauge,
  Network,
  PhoneCall,
  X,
} from "lucide-react";
import type { Overview } from "../api.js";
import { useI18n } from "../i18n.js";

interface ServiceInspectorProps {
  serviceName: string;
  overview: Overview;
  onClose: () => void;
}

export function ServiceInspector({
  serviceName,
  overview,
  onClose,
}: ServiceInspectorProps) {
  const { t, formatNumber } = useI18n();
  const service = overview.services.find(
    (item) => item.service === serviceName,
  );

  if (!service) return null;

  const inbound = overview.edges.filter(
    (edge) => edge.target === serviceName,
  );
  const outbound = overview.edges.filter(
    (edge) => edge.source === serviceName,
  );

  const hypothesis = overview.hypotheses.find(
    (item) => item.service === serviceName,
  );

  return (
    <div className="overlay" role="presentation" onMouseDown={onClose}>
      <section
        className="drawer service-inspector"
        role="dialog"
        aria-modal="true"
        aria-label={t("serviceInspector.title")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="drawer__header">
          <div>
            <p className="eyebrow">{t("serviceInspector.forensics")}</p>
            <h2>{service.service}</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label={t("serviceInspector.close")}
          >
            <X size={17} />
          </button>
        </header>

        <div className="service-inspector__score">
          <div>
            <small>{t("serviceInspector.signal")}</small>
            <strong>
              {hypothesis
                ? formatNumber(hypothesis.score, {
                    minimumFractionDigits: 1,
                    maximumFractionDigits: 1,
                  })
                : "—"}
            </strong>
          </div>
          <span
            className={[
              "service-inspector__confidence",
              hypothesis
                ? "service-inspector__confidence--" +
                  hypothesis.confidence
                : "",
            ]
              .filter(Boolean)
              .join(" ")}
          >
            {hypothesis
              ? t("confidence." + hypothesis.confidence)
              : t("serviceInspector.noHypothesis")}
          </span>
        </div>

        <div className="service-inspector__metrics">
          <article>
            <Gauge size={16} />
            <small>p95</small>
            <strong>
              {formatNumber(service.p95Ms, {
                maximumFractionDigits: 1,
              })}{" "}
              ms
            </strong>
          </article>
          <article>
            <Clock3 size={16} />
            <small>{t("serviceInspector.average")}</small>
            <strong>
              {formatNumber(service.averageMs, {
                maximumFractionDigits: 1,
              })}{" "}
              ms
            </strong>
          </article>
          <article>
            <PhoneCall size={16} />
            <small>{t("serviceInspector.calls")}</small>
            <strong>{formatNumber(service.calls)}</strong>
          </article>
          <article>
            <Network size={16} />
            <small>{t("metrics.errorRate")}</small>
            <strong>
              {formatNumber(service.errorRate * 100, {
                maximumFractionDigits: 1,
              })}
              %
            </strong>
          </article>
        </div>

        <div className="service-inspector__self">
          <span>{t("serviceInspector.selfTime")}</span>
          <strong>
            {formatNumber(service.selfTimeMs, {
              maximumFractionDigits: 1,
            })}{" "}
            ms
          </strong>
        </div>

        <div className="dependency-section">
          <div className="dependency-section__title">
            <ArrowDownToLine size={14} />
            <span>{t("serviceInspector.inbound")}</span>
          </div>
          {inbound.length ? (
            inbound.map((edge) => (
              <div
                className="dependency-row"
                key={edge.source + "->" + edge.target}
              >
                <strong>{edge.source}</strong>
                <span>
                  {formatNumber(edge.calls)} ·{" "}
                  {formatNumber(edge.averageMs, {
                    maximumFractionDigits: 1,
                  })}{" "}
                  ms
                </span>
              </div>
            ))
          ) : (
            <div className="dependency-empty">
              {t("serviceInspector.none")}
            </div>
          )}
        </div>

        <div className="dependency-section">
          <div className="dependency-section__title">
            <ArrowUpFromLine size={14} />
            <span>{t("serviceInspector.outbound")}</span>
          </div>
          {outbound.length ? (
            outbound.map((edge) => (
              <div
                className="dependency-row"
                key={edge.source + "->" + edge.target}
              >
                <strong>{edge.target}</strong>
                <span>
                  {formatNumber(edge.calls)} ·{" "}
                  {formatNumber(edge.averageMs, {
                    maximumFractionDigits: 1,
                  })}{" "}
                  ms
                </span>
              </div>
            ))
          ) : (
            <div className="dependency-empty">
              {t("serviceInspector.none")}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
