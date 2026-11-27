"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

export interface TreemapNode {
  id: string;
  name: string;
  value: number;
  color?: string;
  meta?: string;
}

interface TreemapProps {
  data: TreemapNode[];
  width?: number;
  height?: number;
  onClick?: (id: string) => void;
}

// Squarified treemap (simplified)
function layout(items: TreemapNode[], x: number, y: number, w: number, h: number): { item: TreemapNode; x: number; y: number; w: number; h: number }[] {
  if (items.length === 0) return [];
  if (items.length === 1) return [{ item: items[0], x, y, w, h }];

  const total = items.reduce((s, i) => s + i.value, 0);
  // Decide split direction by aspect ratio
  const horiz = w > h;
  // Greedy split: take first item proportionally
  const first = items[0];
  const rest = items.slice(1);
  const ratio = first.value / total;

  if (horiz) {
    const sw = w * ratio;
    return [
      { item: first, x, y, w: sw, h },
      ...layout(rest, x + sw, y, w - sw, h),
    ];
  } else {
    const sh = h * ratio;
    return [
      { item: first, x, y, w, h: sh },
      ...layout(rest, x, y + sh, w, h - sh),
    ];
  }
}

export function Treemap({ data, width = 600, height = 320, onClick }: TreemapProps) {
  const sorted = [...data].sort((a, b) => b.value - a.value);
  const cells = layout(sorted, 0, 0, width, height);
  const max = Math.max(...data.map((d) => d.value));

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full" style={{ aspectRatio: `${width}/${height}` }}>
      {cells.map((c, i) => {
        const intensity = 0.4 + (c.item.value / max) * 0.6;
        return (
          <g key={i} className={onClick ? "cursor-pointer" : ""} onClick={() => onClick?.(c.item.id)}>
            <rect
              x={c.x + 1}
              y={c.y + 1}
              width={Math.max(0, c.w - 2)}
              height={Math.max(0, c.h - 2)}
              rx={3}
              fill={c.item.color ?? "var(--brand-indigo-500)"}
              opacity={intensity}
              className="transition-opacity hover:opacity-100"
            >
              <title>{c.item.name}: {c.item.value}</title>
            </rect>
            {c.w > 50 && c.h > 28 && (
              <>
                <text
                  x={c.x + 8}
                  y={c.y + 18}
                  fontSize="11"
                  fontWeight="600"
                  fill="white"
                  className="pointer-events-none"
                >
                  {c.item.name.length > c.w / 7 ? c.item.name.slice(0, Math.floor(c.w / 7)) + "…" : c.item.name}
                </text>
                <text
                  x={c.x + 8}
                  y={c.y + 32}
                  fontSize="10"
                  fill="white"
                  opacity={0.85}
                  className="pointer-events-none"
                >
                  {c.item.meta ?? c.item.value}
                </text>
              </>
            )}
          </g>
        );
      })}
    </svg>
  );
}
