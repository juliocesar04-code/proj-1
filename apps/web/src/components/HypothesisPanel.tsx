import {
  AlertTriangle,
  CheckCircle2,
  Gauge,
} from "lucide-react";
import type { IncidentHypothesis } from "../api.js";
import { useI18n } from "../i18n.js";

interface HypothesisPanelProps {
  hypotheses: IncidentHypothesis[];
}

export function HypothesisPanel({
  hypotheses,
}: HypothesisPanelProps) {
  const { t, formatNumber, evidenceText } = useI18n();
  const top = hypotheses[0];

  if (!top) {
    return (
      <div className="empty-state">
        {t("hyp.none")}
      </div>
    );
  }

  const calm =
    top.confidence === "low" &&
    !top.evidence.some((line) => line.includes("error"));

  return (
    <div className="hypothesis-panel">
      <div className="hypothesis-lead">
        <div
          className={[
            "hypothesis-icon",
            calm ? "hypothesis-icon--calm" : "",
          ]
            .filter(Boolean)
            .join(" ")}
        >
          {calm ? (
            <CheckCircle2 size={22} />
          ) : (
            <AlertTriangle size={22} />
          )}
        </div>
        <div>
          <p className="eyebrow">
            {calm ? t("hyp.currentSignal") : t("hyp.leading")}
          </p>
          <h3>{top.service}</h3>
          <p>
            {t("hyp.score", {
              score: formatNumber(top.score, {
                minimumFractionDigits: 1,
                maximumFractionDigits: 1,
              }),
              confidence: t("confidence." + top.confidence),
            })}
          </p>
        </div>
      </div>

      <div className="evidence-list">
        {top.evidence.map((evidence) => (
          <div className="evidence-item" key={evidence}>
            <Gauge size={15} />
            <span>{evidenceText(evidence)}</span>
          </div>
        ))}
      </div>

      {hypotheses.length > 1 ? (
        <div className="ranked-hypotheses">
          {hypotheses.slice(1, 4).map((hypothesis, index) => (
            <div
              className="ranked-hypothesis"
              key={hypothesis.service}
            >
              <span>0{index + 2}</span>
              <strong>{hypothesis.service}</strong>
              <small>
                {formatNumber(hypothesis.score, {
                  minimumFractionDigits: 1,
                  maximumFractionDigits: 1,
                })}
              </small>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
