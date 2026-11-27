import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const DATE_TIME_KST = new Intl.DateTimeFormat("sv-SE", {
  timeZone: "Asia/Seoul", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
});

/** 서버 시각(ISO, 시간대 포함) → 한국 시각 "YYYY-MM-DD HH:mm". DB·파드 시간대(테스트배드는 UTC)와 상관없이 같게 보인다. */
export function formatDateTime(iso?: string | null) {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : DATE_TIME_KST.format(d);
}

export function formatNumber(value: number, fractionDigits = 0) {
  return new Intl.NumberFormat("ko-KR", {
    maximumFractionDigits: fractionDigits,
  }).format(value);
}

export function formatPercent(value: number, fractionDigits = 1) {
  return new Intl.NumberFormat("ko-KR", {
    style: "percent",
    maximumFractionDigits: fractionDigits,
  }).format(value);
}

export function formatBytes(bytes: number, decimals = 1) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${(bytes / Math.pow(k, i)).toFixed(decimals)} ${sizes[i]}`;
}

export function formatWatts(watts: number) {
  if (watts >= 1000) return `${(watts / 1000).toFixed(2)} kW`;
  return `${Math.round(watts)} W`;
}

export function relativeTime(date: Date | string | number) {
  const d = typeof date === "object" ? date : new Date(date);
  const diff = (Date.now() - d.getTime()) / 1000;
  const units: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [60, "minute"],
    [24, "hour"],
    [7, "day"],
    [4.345, "week"],
    [12, "month"],
    [Number.POSITIVE_INFINITY, "year"],
  ];
  let value = diff;
  let unit: Intl.RelativeTimeFormatUnit = "second";
  for (const [div, u] of units) {
    if (Math.abs(value) < div) {
      unit = u;
      break;
    }
    value /= div;
    unit = u;
  }
  return new Intl.RelativeTimeFormat("ko-KR", { numeric: "auto" }).format(
    -Math.round(value),
    unit,
  );
}

// 가속기 종류 배지 색 — 플랫폼 공통(대시보드·토폴로지·서버 상세): GPU 인디고, NPU 민트
export function accKindClass(kind?: string) {
  return kind === "GPU" ? "bg-brand-indigo-100 text-brand-indigo-700"
    : kind === "NPU" ? "bg-brand-mint-100 text-brand-mint-700"
    : kind === "PIM" ? "bg-amber-100 text-amber-800"
    : "bg-slate-100 text-slate-700";
}
