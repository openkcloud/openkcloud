"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

export interface FlowNode {
  id: string;
  label: string;
  type: "client" | "gateway" | "service" | "data" | "storage" | "accelerator";
  health?: "ok" | "warn" | "crit";
  metric?: string; // e.g. "124 req/s"
  href?: string;
}
export interface FlowEdge {
  from: string;
  to: string;
  label?: string; // latency
  weight?: number; // 1-5 line width
  color?: string;
}

interface ServiceFlowProps {
  nodes: FlowNode[];
  edges: FlowEdge[];
  width?: number;
  height?: number;
  positions: Record<string, { x: number; y: number }>;
  onNodeClick?: (id: string) => void;
}

const TYPE_COLOR: Record<FlowNode["type"], string> = {
  client: "fill-slate-400",
  gateway: "fill-brand-indigo-400",
  service: "fill-brand-indigo-500",
  data: "fill-sky-500",
  storage: "fill-amber-500",
  accelerator: "fill-brand-mint-500",
};

const HEALTH_RING: Record<NonNullable<FlowNode["health"]>, string> = {
  ok: "stroke-brand-mint-500",
  warn: "stroke-amber-500",
  crit: "stroke-rose-500",
};

export function ServiceFlowGraph({ nodes, edges, positions, width = 880, height = 360, onNodeClick }: ServiceFlowProps) {
  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full" style={{ minWidth: 700 }}>
      <defs>
        <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" fill="#94a3b8" />
        </marker>
      </defs>
      {edges.map((e, i) => {
        const a = positions[e.from];
        const b = positions[e.to];
        if (!a || !b) return null;
        const mx = (a.x + b.x) / 2;
        const my = (a.y + b.y) / 2 - 14;
        return (
          <g key={i}>
            <path
              d={`M${a.x + 35},${a.y} Q${mx},${my} ${b.x - 35},${b.y}`}
              fill="none"
              stroke={e.color ?? "#94a3b8"}
              strokeWidth={e.weight ?? 1.5}
              strokeOpacity={0.7}
              markerEnd="url(#arrow)"
            />
            {e.label && (
              <text
                x={mx}
                y={my - 4}
                fontSize="9"
                textAnchor="middle"
                className="fill-muted-foreground"
                fontFamily="monospace"
              >
                {e.label}
              </text>
            )}
          </g>
        );
      })}

      {nodes.map((n) => {
        const p = positions[n.id];
        if (!p) return null;
        return (
          <g key={n.id} className={onNodeClick ? "cursor-pointer" : ""} onClick={() => onNodeClick?.(n.id)}>
            {n.health && (
              <circle
                cx={p.x}
                cy={p.y}
                r={32}
                fill="none"
                className={HEALTH_RING[n.health]}
                strokeWidth={2}
                opacity={0.6}
              />
            )}
            <circle
              cx={p.x}
              cy={p.y}
              r={28}
              className={TYPE_COLOR[n.type]}
            />
            <text
              x={p.x}
              y={p.y - 2}
              fontSize="10"
              fontWeight="600"
              fill="white"
              textAnchor="middle"
            >
              {n.label}
            </text>
            {n.metric && (
              <text
                x={p.x}
                y={p.y + 10}
                fontSize="8"
                fill="white"
                textAnchor="middle"
                fontFamily="monospace"
                opacity={0.9}
              >
                {n.metric}
              </text>
            )}
          </g>
        );
      })}
    </svg>
  );
}
