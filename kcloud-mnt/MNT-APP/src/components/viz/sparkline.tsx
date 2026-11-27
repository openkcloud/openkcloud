"use client";
import * as React from "react";

interface SparklineProps {
  data: number[];
  width?: number;
  height?: number;
  color?: string;
  filled?: boolean;
  showDots?: boolean;
  thresholdHigh?: number;
  thresholdLow?: number;
}

export function Sparkline({
  data,
  width = 80,
  height = 24,
  color = "var(--brand-indigo-500)",
  filled = true,
  showDots = false,
  thresholdHigh,
  thresholdLow,
}: SparklineProps) {
  if (!data.length) return null;
  const min = Math.min(...data, thresholdLow ?? Infinity);
  const max = Math.max(...data, thresholdHigh ?? -Infinity);
  const range = max - min || 1;
  const step = width / Math.max(1, data.length - 1);

  const points = data.map((v, i) => {
    const x = i * step;
    const y = height - ((v - min) / range) * height;
    return { x, y, v };
  });

  const pathD = points.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(" ");
  const areaD = `${pathD} L${width},${height} L0,${height} Z`;
  const gradId = React.useId();

  return (
    <svg width={width} height={height} className="overflow-visible">
      {filled && (
        <>
          <defs>
            <linearGradient id={`spark-${gradId}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.35} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <path d={areaD} fill={`url(#spark-${gradId})`} />
        </>
      )}
      <path d={pathD} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
      {showDots && points.map((p, i) => (
        <circle key={i} cx={p.x} cy={p.y} r={1.5} fill={color} />
      ))}
      {thresholdHigh !== undefined && (
        <line
          x1={0}
          y1={height - ((thresholdHigh - min) / range) * height}
          x2={width}
          y2={height - ((thresholdHigh - min) / range) * height}
          stroke="#e11d48"
          strokeWidth={0.5}
          strokeDasharray="2 2"
        />
      )}
    </svg>
  );
}

export function makeSeries(seed: number, count = 30, base = 50, amp = 20): number[] {
  return Array.from({ length: count }, (_, i) =>
    Math.max(0, base + Math.sin((i + seed) / 3) * amp + Math.cos((i + seed) / 5) * (amp / 2)),
  );
}
