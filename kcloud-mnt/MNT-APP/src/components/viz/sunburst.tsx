"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

export interface SunburstNode {
  id: string;
  name: string;
  value?: number;
  color?: string;
  children?: SunburstNode[];
}

interface SunburstProps {
  root: SunburstNode;
  size?: number;
  onSegmentClick?: (id: string) => void;
}

interface Slice {
  id: string;
  name: string;
  startAngle: number;
  endAngle: number;
  innerRadius: number;
  outerRadius: number;
  color: string;
  level: number;
  value: number;
}

function flatten(node: SunburstNode, level: number, start: number, end: number, ringWidth: number, palette: string[]): Slice[] {
  const sliceArr: Slice[] = [];
  const total = (node.children ?? []).reduce((s, c) => s + (c.value ?? sumLeaves(c)), 0) || 1;

  if (level > 0) {
    sliceArr.push({
      id: node.id,
      name: node.name,
      startAngle: start,
      endAngle: end,
      innerRadius: (level - 1) * ringWidth,
      outerRadius: level * ringWidth,
      color: node.color ?? palette[level % palette.length],
      level,
      value: node.value ?? sumLeaves(node),
    });
  }

  let cursor = start;
  for (const child of node.children ?? []) {
    const v = child.value ?? sumLeaves(child);
    const angle = ((end - start) * v) / total;
    sliceArr.push(...flatten(child, level + 1, cursor, cursor + angle, ringWidth, palette));
    cursor += angle;
  }
  return sliceArr;
}

function sumLeaves(node: SunburstNode): number {
  if (!node.children?.length) return node.value ?? 1;
  return node.children.reduce((s, c) => s + sumLeaves(c), 0);
}

function arcPath(cx: number, cy: number, innerR: number, outerR: number, startA: number, endA: number): string {
  const x1 = cx + outerR * Math.cos(startA);
  const y1 = cy + outerR * Math.sin(startA);
  const x2 = cx + outerR * Math.cos(endA);
  const y2 = cy + outerR * Math.sin(endA);
  const x3 = cx + innerR * Math.cos(endA);
  const y3 = cy + innerR * Math.sin(endA);
  const x4 = cx + innerR * Math.cos(startA);
  const y4 = cy + innerR * Math.sin(startA);
  const largeArc = endA - startA > Math.PI ? 1 : 0;
  return [
    `M${x1},${y1}`,
    `A${outerR},${outerR} 0 ${largeArc} 1 ${x2},${y2}`,
    `L${x3},${y3}`,
    `A${innerR},${innerR} 0 ${largeArc} 0 ${x4},${y4}`,
    "Z",
  ].join(" ");
}

export function Sunburst({ root, size = 320, onSegmentClick }: SunburstProps) {
  const palette = ["var(--brand-indigo-400)", "var(--brand-mint-400)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)", "var(--chart-6)"];
  const ringWidth = size / 2 / 4;
  const slices = flatten(root, 0, -Math.PI / 2, Math.PI * 1.5, ringWidth, palette);
  const cx = size / 2, cy = size / 2;

  return (
    <svg viewBox={`0 0 ${size} ${size}`} width={size} height={size}>
      {slices.map((s, i) => (
        <g key={i} className={onSegmentClick ? "cursor-pointer" : ""} onClick={() => onSegmentClick?.(s.id)}>
          <path
            d={arcPath(cx, cy, s.innerRadius, s.outerRadius, s.startAngle, s.endAngle)}
            fill={s.color}
            stroke="white"
            strokeWidth={1.5}
            opacity={0.92}
            className="transition-opacity hover:opacity-100"
          >
            <title>{s.name}: {s.value}</title>
          </path>
        </g>
      ))}
      <circle cx={cx} cy={cy} r={ringWidth * 0.6} fill="white" />
      <text x={cx} y={cy - 5} textAnchor="middle" fontSize="10" className="fill-muted-foreground">total</text>
      <text x={cx} y={cy + 10} textAnchor="middle" fontSize="14" fontWeight="700" className="fill-foreground">
        {sumLeaves(root)}
      </text>
    </svg>
  );
}
