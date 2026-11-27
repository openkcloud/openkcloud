"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

/* Isometric 좌표계 helpers
 * 화면 좌표: x' = (x - y) * cos(30°)
 *           y' = (x + y) * sin(30°) - z
 */
export const ISO_COS = 0.866; // cos(30°)
export const ISO_SIN = 0.5;   // sin(30°)

export function iso(x: number, y: number, z = 0): { x: number; y: number } {
  return {
    x: (x - y) * ISO_COS,
    y: (x + y) * ISO_SIN - z,
  };
}

// ────────────────────────────────────────────────
// IsometricFloor — 격자 바닥판 (다이아몬드)
// ────────────────────────────────────────────────
interface IsometricFloorProps {
  cols: number;
  rows: number;
  cellSize?: number;
  /** floor center (SVG coordinates) */
  cx?: number;
  cy?: number;
  fill?: string;
  stroke?: string;
  showGrid?: boolean;
}

export function IsometricFloor({
  cols,
  rows,
  cellSize = 32,
  cx = 0,
  cy = 0,
  fill = "rgba(255,255,255,0.04)",
  stroke = "rgba(255,255,255,0.15)",
  showGrid = true,
}: IsometricFloorProps) {
  const w = cols * cellSize;
  const h = rows * cellSize;
  const a = iso(0, 0);
  const b = iso(w, 0);
  const c = iso(w, h);
  const d = iso(0, h);
  const path = `M${cx + a.x},${cy + a.y} L${cx + b.x},${cy + b.y} L${cx + c.x},${cy + c.y} L${cx + d.x},${cy + d.y} Z`;
  return (
    <g>
      <path d={path} fill={fill} stroke={stroke} strokeWidth={1.5} />
      {showGrid && (
        <>
          {Array.from({ length: cols - 1 }).map((_, i) => {
            const x = (i + 1) * cellSize;
            const p1 = iso(x, 0);
            const p2 = iso(x, h);
            return (
              <line
                key={`v${i}`}
                x1={cx + p1.x}
                y1={cy + p1.y}
                x2={cx + p2.x}
                y2={cy + p2.y}
                stroke={stroke}
                strokeWidth={0.6}
                strokeDasharray="2 3"
              />
            );
          })}
          {Array.from({ length: rows - 1 }).map((_, i) => {
            const y = (i + 1) * cellSize;
            const p1 = iso(0, y);
            const p2 = iso(w, y);
            return (
              <line
                key={`h${i}`}
                x1={cx + p1.x}
                y1={cy + p1.y}
                x2={cx + p2.x}
                y2={cy + p2.y}
                stroke={stroke}
                strokeWidth={0.6}
                strokeDasharray="2 3"
              />
            );
          })}
        </>
      )}
    </g>
  );
}

// ────────────────────────────────────────────────
// IsometricCylinder — 원반 (자원 1개)
// ────────────────────────────────────────────────
interface IsometricCylinderProps {
  /** isometric grid 좌표 */
  gx: number;
  gy: number;
  gz?: number;
  cellSize?: number;
  radius?: number;
  height?: number;
  color: string;
  highlight?: boolean;
  /** floor center offset (SVG coordinates) */
  cx?: number;
  cy?: number;
  label?: string;
  onClick?: () => void;
  /** unique id for shadow/glow gradient */
  uid?: string;
}

export function IsometricCylinder({
  gx,
  gy,
  gz = 0,
  cellSize = 32,
  radius,
  height = 26,
  color,
  highlight = false,
  cx = 0,
  cy = 0,
  label,
  onClick,
  uid,
}: IsometricCylinderProps) {
  const r = radius ?? cellSize * 0.42;
  const center = iso(gx * cellSize + cellSize / 2, gy * cellSize + cellSize / 2, gz);
  const px = cx + center.x;
  const py = cy + center.y;
  const ry = r * 0.5; // isometric ellipse vertical radius
  const id = uid ?? Math.random().toString(36).slice(2, 8);

  return (
    <g className={onClick ? "cursor-pointer" : ""} onClick={onClick}>
      <defs>
        <radialGradient id={`cyl-top-${id}`} cx="0.4" cy="0.3" r="0.7">
          <stop offset="0%" stopColor="white" stopOpacity={0.45} />
          <stop offset="60%" stopColor={color} stopOpacity={1} />
          <stop offset="100%" stopColor={color} stopOpacity={1} />
        </radialGradient>
        <linearGradient id={`cyl-side-${id}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity={1} />
          <stop offset="100%" stopColor={color} stopOpacity={0.55} />
        </linearGradient>
      </defs>
      {/* shadow on floor */}
      <ellipse
        cx={px}
        cy={py + 4}
        rx={r * 1.1}
        ry={ry * 1.1}
        fill="rgba(0,0,0,0.5)"
        filter="blur(2px)"
      />
      {/* cylinder side */}
      <path
        d={`
          M ${px - r} ${py - height / 2}
          A ${r} ${ry} 0 0 0 ${px + r} ${py - height / 2}
          L ${px + r} ${py + height / 2}
          A ${r} ${ry} 0 0 1 ${px - r} ${py + height / 2}
          Z
        `}
        fill={`url(#cyl-side-${id})`}
        stroke={highlight ? "white" : "rgba(0,0,0,0.4)"}
        strokeWidth={highlight ? 2 : 0.6}
      />
      {/* cylinder top ellipse */}
      <ellipse
        cx={px}
        cy={py - height / 2}
        rx={r}
        ry={ry}
        fill={`url(#cyl-top-${id})`}
        stroke={highlight ? "white" : "rgba(255,255,255,0.3)"}
        strokeWidth={highlight ? 1.8 : 0.6}
      />
      {label && (
        <text
          x={px}
          y={py - height / 2 + 3}
          textAnchor="middle"
          fontSize="8"
          fontWeight="700"
          fill="white"
          style={{ pointerEvents: "none" }}
        >
          {label}
        </text>
      )}
    </g>
  );
}

// ────────────────────────────────────────────────
// IsometricRack — 2.5D 서버 랙
// ────────────────────────────────────────────────
interface IsometricRackProps {
  gx: number;
  gy: number;
  units?: number; // 1U slot count
  rackWidth?: number;
  rackDepth?: number;
  unitHeight?: number;
  cellSize?: number;
  cx?: number;
  cy?: number;
  label?: string;
  /** Per-unit status: green/red/dim */
  unitStates?: ("running" | "warning" | "error" | "down")[];
  onClick?: () => void;
  highlight?: boolean;
}

export function IsometricRack({
  gx,
  gy,
  units = 42,
  rackWidth = 1.4,
  rackDepth = 1.2,
  unitHeight = 4,
  cellSize = 32,
  cx = 0,
  cy = 0,
  label,
  unitStates = [],
  onClick,
  highlight,
}: IsometricRackProps) {
  const w = rackWidth * cellSize;
  const d = rackDepth * cellSize;
  const totalH = units * unitHeight + 28; // bezel
  // Base position
  const baseX = gx * cellSize;
  const baseY = gy * cellSize;

  // 4 bottom corners in iso projection
  const blf = iso(baseX, baseY);            // front-left bottom
  const brf = iso(baseX + w, baseY);        // front-right bottom (right=along +x)
  const blb = iso(baseX, baseY + d);        // back-left
  const brb = iso(baseX + w, baseY + d);    // back-right

  // Tops
  const tlf = { x: blf.x, y: blf.y - totalH };
  const trf = { x: brf.x, y: brf.y - totalH };
  const tlb = { x: blb.x, y: blb.y - totalH };
  const trb = { x: brb.x, y: brb.y - totalH };

  const shift = (p: { x: number; y: number }) => ({ x: cx + p.x, y: cy + p.y });
  const TLF = shift(tlf), TRF = shift(trf), TLB = shift(tlb), TRB = shift(trb);
  const BLF = shift(blf), BRF = shift(brf), BLB = shift(blb), BRB = shift(brb);

  const uid = `rack-${gx}-${gy}`;

  return (
    <g className={onClick ? "cursor-pointer" : ""} onClick={onClick}>
      <defs>
        <linearGradient id={`face-front-${uid}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#1f2937" />
          <stop offset="100%" stopColor="#0f172a" />
        </linearGradient>
        <linearGradient id={`face-side-${uid}`} x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor="#0f172a" />
          <stop offset="100%" stopColor="#020617" />
        </linearGradient>
        <linearGradient id={`face-top-${uid}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#374151" />
          <stop offset="100%" stopColor="#1f2937" />
        </linearGradient>
      </defs>

      {/* shadow */}
      <ellipse
        cx={(BLF.x + BRB.x) / 2}
        cy={Math.max(BLF.y, BRB.y) + 5}
        rx={Math.abs(BRB.x - BLF.x) / 2 + 10}
        ry={8}
        fill="rgba(0,0,0,0.5)"
        filter="blur(3px)"
      />

      {/* RIGHT side */}
      <path
        d={`M${BRF.x},${BRF.y} L${BRB.x},${BRB.y} L${TRB.x},${TRB.y} L${TRF.x},${TRF.y} Z`}
        fill={`url(#face-side-${uid})`}
        stroke={highlight ? "white" : "rgba(0,0,0,0.6)"}
        strokeWidth={highlight ? 1.5 : 0.6}
      />

      {/* FRONT face */}
      <path
        d={`M${BLF.x},${BLF.y} L${BRF.x},${BRF.y} L${TRF.x},${TRF.y} L${TLF.x},${TLF.y} Z`}
        fill={`url(#face-front-${uid})`}
        stroke={highlight ? "white" : "rgba(0,0,0,0.6)"}
        strokeWidth={highlight ? 1.5 : 0.6}
      />

      {/* TOP */}
      <path
        d={`M${TLF.x},${TLF.y} L${TRF.x},${TRF.y} L${TRB.x},${TRB.y} L${TLB.x},${TLB.y} Z`}
        fill={`url(#face-top-${uid})`}
        stroke={highlight ? "white" : "rgba(0,0,0,0.5)"}
        strokeWidth={highlight ? 1.5 : 0.6}
      />

      {/* Unit LEDs on front face — 2 columns per unit, status color */}
      {Array.from({ length: units }).map((_, i) => {
        const state = unitStates[i] ?? "running";
        const color =
          state === "error" ? "#ef4444" :
            state === "warning" ? "#f59e0b" :
              state === "down" ? "#475569" : "#10b981";
        // y position from bottom (rack-1U)
        const yFromBottom = 14 + i * unitHeight;
        // interpolate along front face
        const tx0 = TLF.x + (BLF.x - TLF.x) * (yFromBottom / totalH);
        const ty0 = TLF.y + (BLF.y - TLF.y) * (yFromBottom / totalH);
        const tx1 = TRF.x + (BRF.x - TRF.x) * (yFromBottom / totalH);
        const ty1 = TRF.y + (BRF.y - TRF.y) * (yFromBottom / totalH);
        // 2-3 LED columns evenly spaced
        return (
          <g key={i}>
            {[0.18, 0.42, 0.66, 0.84].map((t, j) => {
              const lx = tx0 + (tx1 - tx0) * t;
              const ly = ty0 + (ty1 - ty0) * t;
              return (
                <rect
                  key={j}
                  x={lx - 2.5}
                  y={ly - 0.8}
                  width={5}
                  height={1.6}
                  fill={color}
                  rx={0.4}
                  opacity={state === "down" ? 0.4 : 0.9}
                />
              );
            })}
          </g>
        );
      })}

      {/* Bezel label */}
      <text
        x={(TLF.x + TRF.x) / 2}
        y={TLF.y + 10}
        fontSize="8"
        textAnchor="middle"
        fontWeight="700"
        fill="rgba(255,255,255,0.9)"
        fontFamily="monospace"
      >
        {label}
      </text>
    </g>
  );
}

// ────────────────────────────────────────────────
// IsometricInfoCard — 호버/선택 시 우측 상단 카드
// ────────────────────────────────────────────────
export function IsometricInfoCard({
  title,
  fields,
  cx,
  cy,
  width = 220,
  status,
}: {
  title: string;
  fields: { label: string; value: React.ReactNode; highlight?: boolean }[];
  cx: number;
  cy: number;
  width?: number;
  status?: "running" | "warning" | "error" | "down";
}) {
  const lineH = 16;
  const padding = 12;
  const h = padding * 2 + lineH * (fields.length + 1);
  const statusColor =
    status === "error" ? "#ef4444" : status === "warning" ? "#f59e0b" : status === "down" ? "#94a3b8" : "#10b981";

  return (
    <g>
      <rect x={cx} y={cy} width={width} height={h} rx={6} fill="rgba(30,41,59,0.95)" stroke="rgba(255,255,255,0.2)" strokeWidth={1} />
      <text x={cx + padding} y={cy + padding + 10} fontSize="10" fontWeight="700" fill="white" fontFamily="monospace">
        {title}
      </text>
      {fields.map((f, i) => (
        <g key={i}>
          <text x={cx + padding} y={cy + padding + lineH + 14 + i * lineH} fontSize="9" fill="rgba(255,255,255,0.5)">
            {f.label}
          </text>
          <text
            x={cx + width - padding}
            y={cy + padding + lineH + 14 + i * lineH}
            fontSize="9"
            fontWeight="600"
            fill={f.highlight ? statusColor : "white"}
            textAnchor="end"
            fontFamily="monospace"
          >
            {f.value}
          </text>
        </g>
      ))}
    </g>
  );
}
