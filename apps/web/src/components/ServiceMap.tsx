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

interface ServiceMapProps {
  services: ServiceNode[];
  edges: ServiceEdge[];
  suspectedService?: string;
}

function serviceLabel(service: ServiceNode, suspected: boolean) {
  return (
    <div className="service-node__content">
      <div className="service-node__topline">
        <span className="service-node__name">{service.service}</span>
        {suspected ? (
          <span className="service-node__badge">suspect</span>
        ) : null}
      </div>
      <div className="service-node__metrics">
        <span>{service.p95Ms.toFixed(0)} ms p95</span>
        <span>{(service.errorRate * 100).toFixed(1)}% err</span>
      </div>
    </div>
  );
}

export function ServiceMap({
  services,
  edges,
  suspectedService,
}: ServiceMapProps) {
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
        label: serviceLabel(service, suspected),
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
    label: edge.calls + " calls",
    animated: edge.errors > 0,
    className: edge.errors > 0 ? "service-edge service-edge--error" : "service-edge",
    markerEnd: {
      type: MarkerType.ArrowClosed,
    },
  }));

  if (services.length === 0) {
    return (
      <div className="empty-state empty-state--map">
        Run a scenario or ingest spans to build the service graph.
      </div>
    );
  }

  return (
    <div className="service-map" aria-label="Service dependency map">
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
