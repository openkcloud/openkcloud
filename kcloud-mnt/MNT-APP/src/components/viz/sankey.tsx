"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

interface Node {
  id: string;
  label: string;
  layer: number;
  color?: string;
  meta?: string;
}
interface Link {
  source: string;
  target: string;
  value: number;
  color?: string;
}

interface SankeyProps {
  nodes: Node[];
  links: Link[];
  width?: number;
  height?: number;
  onNodeClick?: (id: string) => void;
}

export function Sankey({ nodes, links, width = 800, height = 420, onNodeClick }: SankeyProps) {
  // group nodes by layer
  const layers = Array.from(new Set(nodes.map((n) => n.layer))).sort();
  const nodeW = 12;
  const layerGap = (width - nodeW * layers.length) / Math.max(1, layers.length - 1);

  // Calc node sizes based on incoming/outgoing flows
  const nodeFlow = new Map<string, number>();
  links.forEach((l) => {
    nodeFlow.set(l.source, (nodeFlow.get(l.source) ?? 0) + l.value);
    nodeFlow.set(l.target, (nodeFlow.get(l.target) ?? 0) + l.value);
  });

  // Layer layout
  const nodesByLayer = new Map<number, Node[]>();
  layers.forEach((l) => nodesByLayer.set(l, nodes.filter((n) => n.layer === l)));

  // Compute positions
  const positions = new Map<string, { x: number; y: number; h: number; node: Node }>();
  layers.forEach((l, li) => {
    const arr = nodesByLayer.get(l)!;
    const total = arr.reduce((s, n) => s + (nodeFlow.get(n.id) ?? 1), 0);
    const padding = 8;
    let yOffset = padding;
    const availH = height - padding * (arr.length + 1);
    arr.forEach((n) => {
      const flow = nodeFlow.get(n.id) ?? 1;
      const h = Math.max(20, (flow / total) * availH);
      const x = li * (nodeW + layerGap);
      positions.set(n.id, { x, y: yOffset, h, node: n });
      yOffset += h + padding;
    });
  });

  // Link computations — bezier curves
  const linkPaths = links.map((l) => {
    const a = positions.get(l.source);
    const b = positions.get(l.target);
    if (!a || !b) return null;
    const x1 = a.x + nodeW;
    const x2 = b.x;
    const totalA = nodeFlow.get(l.source) ?? 1;
    const totalB = nodeFlow.get(l.target) ?? 1;
    const yA = a.y + (l.value / totalA) * a.h / 2 + a.h / 4;
    const yB = b.y + (l.value / totalB) * b.h / 2 + b.h / 4;
    const w = Math.max(1, (l.value / Math.max(totalA, totalB)) * Math.min(a.h, b.h) * 0.6);
    const midX = (x1 + x2) / 2;
    const d = `M${x1},${yA} C${midX},${yA} ${midX},${yB} ${x2},${yB}`;
    return { ...l, path: d, strokeWidth: w };
  }).filter((x) => x !== null);

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full" style={{ minHeight: height }}>
      {/* Links first (behind nodes) */}
      {linkPaths.map((l, i) => (
        <path
          key={i}
          d={l!.path}
          stroke={l!.color ?? "var(--brand-indigo-300)"}
          strokeWidth={l!.strokeWidth}
          fill="none"
          opacity={0.4}
          className="transition-opacity hover:opacity-80"
        >
          <title>{l!.source} → {l!.target}: {l!.value}</title>
        </path>
      ))}

      {/* Nodes */}
      {Array.from(positions.entries()).map(([id, { x, y, h, node }]) => (
        <g key={id} className={onNodeClick ? "cursor-pointer" : ""} onClick={() => onNodeClick?.(id)}>
          <rect
            x={x}
            y={y}
            width={nodeW}
            height={h}
            rx={2}
            fill={node.color ?? "var(--brand-indigo-500)"}
          />
          <text
            x={x + nodeW + 4}
            y={y + h / 2 + 3}
            fontSize="10"
            className="fill-foreground"
          >
            {node.label}
          </text>
          {node.meta && (
            <text x={x + nodeW + 4} y={y + h / 2 + 14} fontSize="9" className="fill-muted-foreground">
              {node.meta}
            </text>
          )}
        </g>
      ))}
    </svg>
  );
}
