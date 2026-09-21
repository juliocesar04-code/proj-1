import {
  AlertTriangle,
  CheckCircle2,
  Gauge,
} from "lucide-react";
import type { IncidentHypothesis } from "../api.js";

interface HypothesisPanelProps {
  hypotheses: IncidentHypothesis[];
}

export function HypothesisPanel({
  hypotheses,
}: HypothesisPanelProps) {
  const top = hypotheses[0];

  if (!top) {
    return (
      <div className="empty-state">
        No incident evidence yet.
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
            {calm ? "Current signal" : "Leading hypothesis"}
          </p>
          <h3>{top.service}</h3>
          <p>
            Evidence score {top.score.toFixed(1)} · {top.confidence} confidence
          </p>
        </div>
      </div>

      <div className="evidence-list">
        {top.evidence.map((evidence) => (
          <div className="evidence-item" key={evidence}>
            <Gauge size={15} />
            <span>{evidence}</span>
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
              <small>{hypothesis.score.toFixed(1)}</small>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
