import {
  Background,
  Controls,
  MarkerType,
  ReactFlow,
  type Edge,
  type Node,
} from "@xyflow/react";
import type {
  ServiceEdge,
  ServiceNode,
} from "../api.js";
import { useI18n } from "../i18n.js";

interface ServiceMapProps {
  services: ServiceNode[];
  edges: ServiceEdge[];
  suspectedService?: string | undefined;
}

export function ServiceMap({
  services,
  edges,
  suspectedService,
}: ServiceMapProps) {
  const { t, formatNumber } = useI18n();
  const radius = Math.max(170, services.length * 34);
  const center = radius + 80;

  const nodes: Node[] = services.map((service, index) => {
    const angle =
      (index / Math.max(services.length, 1)) * Math.PI * 2 - Math.PI / 2;
    const suspected = service.service === suspectedService;

    return {
      id: service.service,
      position: {
        x: center + Math.cos(angle) * radius,
        y: center + Math.sin(angle) * radius,
      },
      data: {
        label: (
          <div className="service-node__content">
            <div className="service-node__topline">
              <span className="service-node__name">{service.service}</span>
              {suspected ? (
                <span className="service-node__badge">
                  {t("service.suspect")}
                </span>
              ) : null}
            </div>
            <div className="service-node__metrics">
              <span>
                {formatNumber(service.p95Ms, {
                  maximumFractionDigits: 0,
                })}{" "}
                ms p95
              </span>
              <span>
                {formatNumber(service.errorRate * 100, {
                  minimumFractionDigits: 1,
                  maximumFractionDigits: 1,
                })}
                % {t("service.err")}
              </span>
            </div>
          </div>
        ),
      },
      draggable: false,
      selectable: true,
      className: [
        "service-node",
        service.errors > 0 ? "service-node--error" : "",
        suspected ? "service-node--suspect" : "",
      ]
        .filter(Boolean)
        .join(" "),
    };
  });

  const flowEdges: Edge[] = edges.map((edge) => ({
    id: edge.source + "->" + edge.target,
    source: edge.source,
    target: edge.target,
    label: t("service.calls", {
      count: formatNumber(edge.calls),
    }),
    animated: edge.errors > 0,
    className: edge.errors > 0
      ? "service-edge service-edge--error"
      : "service-edge",
    markerEnd: {
      type: MarkerType.ArrowClosed,
    },
  }));

  if (services.length === 0) {
    return (
      <div className="empty-state empty-state--map">
        {t("service.empty")}
      </div>
    );
  }

  return (
    <div className="service-map" aria-label={t("service.aria")}>
      <ReactFlow
        nodes={nodes}
        edges={flowEdges}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.35}
        maxZoom={1.8}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={24} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
