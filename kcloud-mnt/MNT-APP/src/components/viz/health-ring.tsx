"use client";
import * as React from "react";

interface HealthRingProps {
  value: number; // 0-100
  size?: number;
  thickness?: number;
  label?: string;
  unit?: string;
  thresholds?: { warning: number; critical: number };
}

export function HealthRing({
  value,
  size = 80,
  thickness = 8,
  label,
  unit = "%",
  thresholds = { warning: 70, critical: 85 },
}: HealthRingProps) {
  const radius = (size - thickness) / 2;
  const circ = 2 * Math.PI * radius;
  const offset = circ - (value / 100) * circ;
  const color =
    value >= thresholds.critical ? "#e11d48" :
      value >= thresholds.warning ? "#f59e0b" : "var(--brand-mint-500)";

  return (
    <div className="relative inline-flex flex-col items-center" style={{ width: size }}>
      <svg width={size} height={size} className="-rotate-90">
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          stroke="var(--secondary)"
          strokeWidth={thickness}
          fill="transparent"
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          stroke={color}
          strokeWidth={thickness}
          strokeDasharray={circ}
          strokeDashoffset={offset}
          strokeLinecap="round"
          fill="transparent"
          style={{ transition: "stroke-dashoffset 600ms ease-out" }}
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="font-mono text-base font-bold tabular-nums leading-tight" style={{ color }}>
          {Math.round(value)}
          <span className="text-[9px] font-normal text-muted-foreground">{unit}</span>
        </span>
      </div>
      {label && <div className="mt-1 text-center text-[10px] uppercase tracking-wider text-muted-foreground">{label}</div>}
    </div>
  );
}
