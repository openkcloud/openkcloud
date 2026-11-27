"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

interface FlameSegment {
  label: string;
  value: number; // ms or % share
  color?: string;
  drillId?: string;
}

interface FlameBlockProps {
  title?: string;
  segments: FlameSegment[];
  height?: number;
  onSegmentClick?: (id: string) => void;
}

export function FlameBlock({ title, segments, height = 40, onSegmentClick }: FlameBlockProps) {
  const total = segments.reduce((s, x) => s + x.value, 0);
  return (
    <div className="space-y-1.5">
      {title && (
        <div className="flex items-baseline justify-between text-xs">
          <span className="text-muted-foreground">{title}</span>
          <span className="font-mono tabular-nums">{total} total</span>
        </div>
      )}
      <div className="flex w-full overflow-hidden rounded-md" style={{ height }}>
        {segments.map((s, i) => {
          const pct = (s.value / total) * 100;
          return (
            <button
              key={i}
              onClick={() => s.drillId && onSegmentClick?.(s.drillId)}
              className={cn(
                "group relative flex items-center justify-center text-[10px] font-medium text-white transition-all",
                s.drillId && "cursor-pointer hover:brightness-110",
              )}
              style={{
                width: `${pct}%`,
                background: s.color ?? "var(--brand-indigo-500)",
                borderRight: i < segments.length - 1 ? "1px solid white" : "none",
              }}
              title={`${s.label}: ${s.value}`}
            >
              {pct > 8 && (
                <span className="truncate px-1">
                  {s.label}
                </span>
              )}
              <span className="pointer-events-none absolute bottom-full left-1/2 mb-1 hidden -translate-x-1/2 whitespace-nowrap rounded bg-foreground px-2 py-0.5 text-[10px] text-background group-hover:block">
                {s.label} · {s.value} ({pct.toFixed(0)}%)
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
