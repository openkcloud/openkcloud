"use client";
import { ChangeMark } from "@/components/change-mark";
import * as React from "react";
import {
  Activity,
  AlertCircle,
  ArrowUpRight,
  ArrowDownRight,
  Cpu,
  Database,
  HardDrive,
  Zap,
  Info,
} from "lucide-react";
import {
  CartesianGrid,
  Line,
  LineChart,
  Tooltip as RTooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { cn, formatNumber } from "@/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@/components/ui/tooltip";
import {
  useAcceleratorSummary,
  useMonitoringOverview,
  usePowerBreakdown,
  usePowerTimeseries,
  useClusterResources,
  useTopHosts,
  useErrorLogs,
  useKpiTimeseries,
  useNodes,
  useNodesTimeseries,
  useUtilTimeseries,
  type VendorMetrics,
  type NodeRow,
  type UtilHeatmap,
} from "@/lib/obs-api";


// 가속기 3종 공통 지표 — 공통 6종 + 파생 2종
const COMMON_METRICS = [
  { name: "온도", unit: "℃", rngd: "54", atom: "47", l40s: "63", note: "임계 85" },
  { name: "전력", unit: "W", rngd: "118.6", atom: "61.2", l40s: "242.8", note: "실시간" },
  { name: "사용률", unit: "%", rngd: "48", atom: "39", l40s: "71", note: "카드 사용률" },
  { name: "메모리 사용량", unit: "", rngd: "22.6 GB", atom: "7.8 GB", l40s: "28.4 GiB", note: "총량 대비" },
  { name: "메모리 총량", unit: "", rngd: "51.0 GB", atom: "16.9 GB", l40s: "46.1 GiB", note: "장치 탑재 용량" },
  { name: "health", unit: "", rngd: "1 정상", atom: "1 정상", l40s: "1 정상", note: "1 = 정상" },
  { name: "전력 모드 / 캡", unit: "", rngd: "— · 미캡", atom: "— · 미캡", l40s: "P0 · 미캡", note: "P0~P15 · 캡 상태" },
  { name: "Throttling", unit: "", rngd: "정상", atom: "정상", l40s: "정상", note: "쓰로틀 장치 · 원인" },
];

// MNT-OBS 가속기 공통 지표 응답(벤더별)을 표 8행으로 변환. 데이터 없으면 목업 폴백.
// 가속기 공통지표 각 행의 원천 필드 (모두 /clusters/{c}/nodes/{n}/accelerators)
const ACCEL_FIELD: Record<string, string> = {
  "온도": "temperature_celsius",
  "전력": "power_watts",
  "사용률": "utilization_percent",
  "메모리 사용량": "memory_used_bytes",
  "메모리 총량": "memory_total_bytes",
  "health": "healthy",
  "전력 모드 / 캡": "power_state · power_capped · power_limit_percent",
  "Throttling": "throttled · throttle_reasons",
};

// 비고 칸 추가 설명(툴팁) — 벤더별 동작 차이
const NOTE_HINT: Record<string, string> = {
  "전력 모드 / 캡":
    "NVIDIA: P0~P15 성능 단계(P-state) — 부하에 따라 GPU 가 자동 변경(P0=최고 성능).\n" +
    "Furiosa: governor 모드명(현재 performance = 항상 최고 클럭, 사람이 수동 설정).\n" +
    "Rebellions: 전력 모드 미제공(— 처리).",
};

function fmtGB(bytes: number | null | undefined) {
  // 피드백: 0 은 "0.0 GB" 로 명확히, null/미수집만 "—"
  return bytes == null ? "—" : `${(bytes / 1e9).toFixed(1)} GB`;
}
function fmtN(x: number | null | undefined, digits: number) {
  return x == null ? "—" : x.toFixed(digits);
}
function buildAccelRows(v?: Record<string, VendorMetrics>) {
  if (!v) return COMMON_METRICS;
  const cell = (vendor: string, fn: (m: VendorMetrics) => string) => {
    const m = v[vendor];
    return m ? fn(m) : "—";
  };
  const defs: { name: string; unit: string; note: string; get: (m: VendorMetrics) => string }[] = [
    { name: "온도", unit: "℃", note: "임계 85", get: (m) => m.temperature_celsius.toFixed(1) },
    { name: "전력", unit: "W", note: "장치당 평균", get: (m) => m.power_watts.toFixed(1) },
    { name: "사용률", unit: "%", note: "카드 사용률", get: (m) => m.utilization_percent.toFixed(0) },
    { name: "메모리 사용량", unit: "", note: "총량 대비", get: (m) => fmtGB(m.memory_used_bytes) },
    { name: "메모리 총량", unit: "", note: "장치 탑재 용량", get: (m) => fmtGB(m.memory_total_bytes) },
    { name: "health", unit: "", note: "정상/전체", get: (m) => `${m.healthy_count}/${m.count} 정상` },
    { name: "전력 모드 / 캡", unit: "", note: "P0~P15 · 캡 상태", get: (m) => `${m.power_state ?? "—"}${m.capped_count ? ` · 캡 ${m.capped_count}` : " · 미캡"}` },
    { name: "Throttling", unit: "", note: "쓰로틀 장치 · 원인", get: (m) => (m.throttled_count ? `${m.throttled_count}대${m.throttle_reasons.length ? ` (${m.throttle_reasons.join(",")})` : ""}` : "정상") },
  ];
  return defs.map((d) => ({
    name: d.name,
    unit: d.unit,
    note: d.note,
    rngd: cell("furiosa", d.get),
    atom: cell("rebellions", d.get),
    l40s: cell("nvidia", d.get),
  }));
}

export default function MonitoringOverviewPage() {
  const { data: accel } = useAcceleratorSummary("mgmt");
  const commonMetrics = buildAccelRows(accel?.vendors);
  const { data: ov } = useMonitoringOverview("mgmt");
  const { data: powerTs } = usePowerTimeseries();
  const psLast = powerTs?.series?.[powerTs.series.length - 1];
  const { data: clusters } = useClusterResources();
  const { data: topHosts } = useTopHosts(5);
  const { data: errorLogs } = useErrorLogs(4);
  const { data: kpiTs } = useKpiTimeseries();
  const { data: nodesData } = useNodes("mgmt");
  const { data: nodesReadyTs } = useNodesTimeseries("mgmt");
  const { data: utilTs } = useUtilTimeseries();
  return (
    <div className="space-y-4">
      {/* KPI band — Datadog "Query Value" widgets */}
      <div className="grid gap-3 md:grid-cols-3 lg:grid-cols-6">
        <QueryValue label="전력 효율" value={fmtN(ov?.power_efficiency_pct_per_watt, 2)} unit="%/W" sparkData={kpiTs?.power_efficiency} color="mint" connected={!!ov} api="/api/v2/monitoring/power/efficiency(+/timeseries)" />
        <QueryValue label="저장소 사용률" value={fmtN(ov?.storage_usage_percent, 1)} unit="%" sparkData={kpiTs?.storage} color="indigo" connected={!!ov} api="/api/v2/storage/ceph/capacity(+/timeseries)" />
        <QueryValue label="쓰로틀링 횟수" value={ov?.throttle_events != null ? String(ov.throttle_events) : "—"} unit="회" sparkData={kpiTs?.throttle} color="rose" connected={!!ov} api="/api/v2/monitoring/throttling/timeseries (timeline.events)" />
        <QueryValue label="가속기 사용률" value={fmtN(ov?.accelerator_utilization_percent, 0)} unit="%" sparkData={kpiTs?.accel_util} color="violet" connected={!!ov} api="/api/v2/monitoring/accelerators/utilization(+/timeseries)" />
        <QueryValue label="서버 총 전력" value={ov?.total_power_watts != null ? (ov.total_power_watts / 1000).toFixed(2) : "—"} unit="kW" sparkData={kpiTs?.server_power} color="amber" connected={!!ov} api="/api/v2/monitoring/power/summary(+/timeseries)" />
        <QueryValue label="정상 노드" value={ov?.nodes_ready != null ? `${ov.nodes_ready} / ${ov.nodes_total}` : "—"} unit="" sparkData={nodesReadyTs?.series} color="sky" connected={!!ov} api="/api/v2/monitoring/nodes/timeseries" />
      </div>

      {/* 가속기 3종 공통 지표 — 2차 워크샵 이노그리드 p.22~23 (CR-OBS-04) */}
      <ChangeMark ids={["CR-OBS-04"]}>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            가속기 공통 지표
            <span className="flex items-center gap-1.5">
              <InfoHint
                text={
                  accel?.source_clusters?.length
                    ? `집계 클러스터 (${accel.source_clusters.length}개)\n${accel.source_clusters.join(", ")}\n\nmgmt 관리도메인 하위 서비스클러스터의 가속기를 벤더(RNGD·ATOM·L40S)로 묶어 집계합니다.`
                    : "mgmt 관리도메인 하위 서비스클러스터의 가속기를 벤더별로 집계합니다."
                }
              />
              <StatusTag connected={!!accel} api="/api/v2/clusters/{c}/nodes/{n}/accelerators" />
            </span>
          </CardTitle>
          <CardDescription>공통 6종 + 파생 2종을 GPU·NPU 같은 지표 이름으로 조회한다.</CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border text-left text-[11px] text-muted-foreground">
                <th className="py-1.5 pr-3 font-medium">지표</th>
                <th className="py-1.5 pr-3 font-medium">Furiosa RNGD (NPU)</th>
                <th className="py-1.5 pr-3 font-medium">Rebellions ATOM (NPU)</th>
                <th className="py-1.5 pr-3 font-medium">NVIDIA L40S (GPU)</th>
                <th className="py-1.5 font-medium">비고</th>
              </tr>
            </thead>
            <tbody className="[&_td]:py-1.5 [&_td]:pr-3 [&_td]:align-top">
              {commonMetrics.map((m) => (
                <tr key={m.name} className="border-b border-border/60 last:border-0">
                  <td className="font-medium">
                    <span className="inline-flex items-center gap-1">
                      {m.name}
                      {m.unit && <span className="text-muted-foreground">{m.unit}</span>}
                      {ACCEL_FIELD[m.name] && (
                        <InfoHint text={`원천: /api/v2/clusters/{c}/nodes/{n}/accelerators\n필드: ${ACCEL_FIELD[m.name]}`} />
                      )}
                    </span>
                  </td>
                  <td className="font-mono tabular-nums">{m.rngd}</td>
                  <td className="font-mono tabular-nums">{m.atom}</td>
                  <td className="font-mono tabular-nums">{m.l40s}</td>
                  <td className="text-muted-foreground">
                    <span className="inline-flex items-center gap-1">
                      {m.note}
                      {NOTE_HINT[m.name] && <InfoHint text={NOTE_HINT[m.name]} />}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
      </ChangeMark>

      {/* 호스트 맵 — 전체 폭 한 줄 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            호스트 맵
            <span className="flex items-center gap-1.5">
              <InfoHint text={"원래 설계: 노드별 사용률(utilization_percent) 색상.\n그러나 mgmt 물리노드는 사용률이 null(가속기가 서비스클러스터 VM에 존재)이라, 전력(power_watts) 기준으로 대체함.\n원천: /clusters/{c}/nodes"} />
              <StatusTag connected={!!nodesData} api="/api/v2/clusters/{c}/nodes (power_watts)" />
            </span>
          </CardTitle>
          <CardDescription>노드별 전력(power_watts) — 색상 = 전력 상대 부하</CardDescription>
        </CardHeader>
        <CardContent>
          <HostMap nodes={nodesData?.nodes ?? []} />
          <div className="mt-3 flex items-center gap-3 text-[10px] text-muted-foreground">
            <div className="flex items-center gap-1">
              <span className="size-2.5 rounded-sm bg-brand-mint-400" /> 전력 낮음
            </div>
            <div className="flex items-center gap-1">
              <span className="size-2.5 rounded-sm bg-amber-400" /> 중간
            </div>
            <div className="flex items-center gap-1">
              <span className="size-2.5 rounded-sm bg-rose-500" /> 높음 (최대 대비)
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 가속기 사용률 히트맵 — 전체 폭 한 줄 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            가속기 사용률 — 호스트 × 24시간
            <StatusTag connected={!!utilTs} api="/api/v2/monitoring/metrics/timeseries?by=hostname (gpu/furiosa/rebellions_utilization)" />
          </CardTitle>
          <CardDescription>호스트별 가속기 사용률(%)을 시각 격자로. 색이 진할수록 사용률이 높습니다.</CardDescription>
        </CardHeader>
        <CardContent>
          <UtilHeatmapGrid data={utilTs} />
        </CardContent>
      </Card>

      {/* Top lists */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              상위 호스트 — 가속기 사용률
              <StatusTag connected={!!topHosts} api="/api/v2/monitoring/accelerators/utilization?by=node&sort_order=desc" />
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {(topHosts?.hosts ?? []).map((h, i) => (
              <TopRow key={h.host} idx={i + 1} name={`${h.host}  ·  ${h.vendor}`} value={h.utilization_pct} unit="%" max={100} />
            ))}
            {(topHosts?.hosts?.length ?? 0) === 0 && (
              <div className="py-6 text-center text-xs text-muted-foreground">데이터 없음</div>
            )}
          </CardContent>
        </Card>

        <PowerBreakdownCard />
      </div>

      {/* 전력 시계열 + 최근 오류 로그 */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              전력 시계열 — 계층별
              <span className="flex items-center gap-1.5">
                <InfoHint text={"server(IPMI 실측) ⊃ cpu(Kepler 추정) + accelerator(벤더 exporter) + 기타.\n포함 관계라 합산하지 않고 독립 3라인으로 표시합니다.\n원천: /monitoring/power/timeseries (24h·1h·avg)"} />
                <StatusTag connected={!!powerTs} api="/api/v2/monitoring/power/timeseries" />
              </span>
            </CardTitle>
            <CardDescription>server(IPMI 실측) · cpu(추정) · accelerator(벤더 exporter) 세 계층을 같은 축에 둔다. period·step·aggregation으로 구간을 바꾼다.</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                server: { label: "server", color: "var(--brand-indigo-500)" },
                cpu: { label: "cpu", color: "#f59e0b" },
                accelerator: { label: "accelerator", color: "var(--brand-mint-500)" },
              }}
              className="h-[220px] w-full"
            >
              <LineChart data={powerTs?.series ?? []}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} interval={3} />
                <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${v}W`} />
                <RTooltip content={<ChartTooltipContent />} />
                <Line type="monotone" dataKey="server" stroke="var(--brand-indigo-500)" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="cpu" stroke="#f59e0b" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="accelerator" stroke="var(--brand-mint-500)" strokeWidth={2} dot={false} />
              </LineChart>
            </ChartContainer>
            <div className="mt-3 flex items-center gap-6 text-xs">
              <Marker label="server" value={psLast?.server != null ? `${Math.round(psLast.server).toLocaleString()} W` : "—"} color="bg-brand-indigo-500" />
              <Marker label="cpu" value={psLast?.cpu != null ? `${Math.round(psLast.cpu).toLocaleString()} W` : "—"} color="bg-amber-500" />
              <Marker label="accelerator" value={psLast?.accelerator != null ? `${Math.round(psLast.accelerator).toLocaleString()} W` : "—"} color="bg-brand-mint-500" />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              최근 오류 로그
              <StatusTag connected={!!errorLogs} api="/api/v2/logs/search (level=error)" />
            </CardTitle>
            <CardDescription>log_level=error 최신순. 자동 추출 필드(XID·OOM)는 배지로 표시.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {(errorLogs?.logs ?? []).map((e, i) => {
              const badge = e.detected_fields?.xid_code ? `XID ${e.detected_fields.xid_code}` : e.detected_fields?.error_type;
              return (
                <div key={i} className="flex items-start gap-2.5 rounded-lg border border-border p-2.5">
                  <AlertCircle className={cn("mt-0.5 size-4 shrink-0", e.log_level === "error" ? "text-rose-600" : "text-amber-600")} />
                  <div className="min-w-0 flex-1 space-y-0.5">
                    <div className="truncate text-[12px] leading-tight" title={e.message}>{e.message}</div>
                    <div className="flex items-center gap-1.5 font-mono text-[10px] text-muted-foreground">
                      <span>{e.timestamp?.replace("T", " ").slice(0, 19)}</span>
                      <span>·</span>
                      <span>{e.labels?.cluster ?? "—"}</span>
                      {e.labels?.node && <span>· {e.labels.node}</span>}
                      {badge && <Badge variant="warning" className="ml-0.5 px-1 py-0 text-[9px]">{badge}</Badge>}
                    </div>
                  </div>
                </div>
              );
            })}
            {(errorLogs?.logs?.length ?? 0) === 0 && (
              <div className="py-6 text-center text-xs text-muted-foreground">오류 로그 없음</div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* 클러스터 자원 현황 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            클러스터 자원 현황
            <StatusTag connected={!!clusters} api="/api/v2/clusters  ·  /api/v2/clusters/{c}/resource" />
          </CardTitle>
          <CardDescription>관리 클러스터 1개 + 서비스 클러스터 4개. 서비스 클러스터는 IPMI가 없어 서버 전력이 비어 있다(—).</CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border text-left text-[11px] text-muted-foreground">
                <th className="py-1.5 pr-3 font-medium">클러스터</th>
                <th className="py-1.5 pr-3 font-medium">종류</th>
                <th className="py-1.5 pr-3 font-medium">노드 (Ready/전체)</th>
                <th className="py-1.5 pr-3 font-medium">CPU 사용률</th>
                <th className="py-1.5 pr-3 font-medium">메모리 사용률</th>
                <th className="py-1.5 pr-3 font-medium">가속기</th>
                <th className="py-1.5 font-medium">전력</th>
              </tr>
            </thead>
            <tbody className="[&_td]:py-1.5 [&_td]:pr-3">
              {(clusters?.clusters ?? []).map((c) => (
                <tr key={c.cluster} className="border-b border-border/60 last:border-0">
                  <td className="font-mono font-medium">{c.cluster}</td>
                  <td>
                    <Badge variant={c.type === "management" ? "default" : "outline"} className="text-[10px]">
                      {c.type === "management" ? "관리" : "서비스"}
                    </Badge>
                  </td>
                  <td className="font-mono tabular-nums">{c.nodes_ready != null ? `${c.nodes_ready} / ${c.nodes_total}` : "—"}</td>
                  <td className="font-mono tabular-nums">{c.cpu_percent != null ? `${c.cpu_percent}%` : "—"}</td>
                  <td className="font-mono tabular-nums">{c.memory_percent != null ? `${c.memory_percent}%` : "—"}</td>
                  <td className="font-mono tabular-nums">{c.accelerator_total ? `${c.accelerator_total}장 · ${fmtN(c.accelerator_util_percent, 0)}%` : "—"}</td>
                  <td className="font-mono tabular-nums">{c.power_watts != null ? `${formatNumber(c.power_watts)} W` : "—"}</td>
                </tr>
              ))}
              {(clusters?.clusters?.length ?? 0) === 0 && (
                <tr><td colSpan={7} className="py-6 text-center text-muted-foreground">데이터 없음</td></tr>
              )}
            </tbody>
          </table>
        </CardContent>
      </Card>

    </div>
  );
}

// ────── Datadog-style components ──────
/** 전력 분해 — dimension 파라미터를 바꿔 같은 응답을 다른 기준으로 본다 */
function PowerBreakdownCard() {
  const DIMS = [
    { key: "vendor", label: "벤더별" },
    { key: "cluster", label: "클러스터별" },
    { key: "node", label: "노드별" },
    { key: "accelerator", label: "카드별" },
  ];
  const [dim, setDim] = React.useState("vendor");
  const { data } = usePowerBreakdown(dim);
  const items = data?.items ?? [];
  const max = items.length ? Math.max(...items.map((i) => i.watts)) : 1;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center justify-between text-base">
          전력 분해
          <span className="flex items-center gap-1.5">
            <InfoHint text={"dimension(벤더·클러스터·노드·카드)으로 쪼개는 기준을 바꿉니다.\nlayer: server(IPMI) / cpu(추정) / accelerator(벤더).\n원천: /monitoring/power/breakdown"} />
            <StatusTag connected={!!data} api="/api/v2/monitoring/power/breakdown" />
          </span>
        </CardTitle>
        <CardDescription>dimension 파라미터로 쪼개는 기준을 바꾼다.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="flex flex-wrap gap-1">
          {DIMS.map((d) => (
            <button
              key={d.key}
              type="button"
              onClick={() => setDim(d.key)}
              className={cn(
                "rounded-md border px-2 py-1 text-[11px] transition-colors",
                dim === d.key ? "border-primary bg-primary/10 text-primary" : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {d.label}
            </button>
          ))}
        </div>
        {items.length === 0 ? (
          <div className="py-6 text-center text-xs text-muted-foreground">데이터 없음</div>
        ) : (
          items.map((it, i) => (
            <TopRow key={`${it.key}-${i}`} idx={i + 1} name={`${it.key}  ·  ${it.layer}`} value={it.watts} unit="W" max={max} color="mint" />
          ))
        )}
      </CardContent>
    </Card>
  );
}

// 카드 우측 상단 연동 상태 뱃지 (API 경로 표기 대체)
function StatusTag({ connected, api, issue }: { connected?: boolean; api?: string; issue?: string }) {
  const badge = (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
        api && "cursor-help",
        connected
          ? "border-brand-mint-200 bg-brand-mint-50 text-brand-mint-700"
          : "border-border bg-muted text-muted-foreground",
      )}
      title={api ? undefined : connected ? "실 API 연동됨" : "목업 (연동 예정)"}
    >
      <span className={cn("size-1.5 rounded-full", connected ? "bg-brand-mint-500" : "bg-muted-foreground/40")} />
      {connected ? "연동" : "목업"}
    </span>
  );
  if (!api && !issue) return badge;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{badge}</TooltipTrigger>
      <TooltipContent className="max-w-sm whitespace-pre-line border border-border bg-popover text-[11px] text-popover-foreground shadow-md">
        <span className="font-medium text-muted-foreground">{connected ? "연동됨 · 데이터 원천" : "목업 · 연동 예정"}</span>
        {api && (<><br /><span className="font-mono">{api}</span></>)}
        {!connected && issue && (
          <>
            <br />
            <span className="text-amber-600">⚠ 미연동 이슈: {issue}</span>
          </>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

// 집계 범위/소스 안내 — 인포 아이콘에 올리면 툴팁으로 포함 클러스터·산출 기준 표시
function InfoHint({ text }: { text: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" className="inline-flex cursor-help text-muted-foreground hover:text-foreground" aria-label="집계 정보">
          <Info className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs whitespace-pre-line border border-border bg-popover text-xs leading-relaxed text-popover-foreground shadow-md">{text}</TooltipContent>
    </Tooltip>
  );
}

function QueryValue({
  label,
  value,
  unit,
  delta,
  sparkData,
  color,
  connected,
  api,
}: {
  label: string;
  value: string;
  unit: string;
  delta?: number;
  /** 스파크라인 시계열. 없으면 그래프 자체를 그리지 않음(생략) */
  sparkData?: { i: number; v: number }[];
  /** 실 API 연동 여부 */
  connected?: boolean;
  /** 데이터 원천 API 경로 */
  api?: string;
  color: "indigo" | "mint" | "rose" | "violet" | "amber" | "sky";
}) {
  const colorMap = {
    indigo: "var(--brand-indigo-500)",
    mint: "var(--brand-mint-500)",
    rose: "#e11d48",
    violet: "#7c3aed",
    amber: "#d97706",
    sky: "#0284c7",
  };
  const positive = (delta ?? 0) > 0;
  return (
    <Card className="relative p-3">
      <span className="absolute right-2 top-2">
        <StatusTag connected={connected} api={api} />
      </span>
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className="mt-0.5 flex items-baseline gap-1">
        <span className="text-2xl font-bold tabular-nums">{value}</span>
        <span className="text-xs text-muted-foreground">{unit}</span>
      </div>
      {sparkData && sparkData.length > 1 && (
        <div className="-mx-1 -mb-1 mt-1 h-10">
          <ChartContainer config={{ v: { label, color: colorMap[color] } }} className="h-full w-full">
            <LineChart data={sparkData}>
              <Line type="monotone" dataKey="v" stroke={colorMap[color]} strokeWidth={1.5} dot={false} />
            </LineChart>
          </ChartContainer>
        </div>
      )}
      {delta ? (
        <div className={cn("flex items-center gap-0.5 text-[10px]", positive ? "text-rose-600" : "text-brand-mint-700")}>
          {positive ? <ArrowUpRight className="size-2.5" /> : <ArrowDownRight className="size-2.5" />}
          {Math.abs(delta)}% vs 1h
        </div>
      ) : null}
    </Card>
  );
}

function TopRow({
  idx,
  name,
  value,
  unit,
  max,
  color = "indigo",
}: {
  idx: number;
  name: string;
  value: number;
  unit: string;
  max: number;
  color?: "indigo" | "mint";
}) {
  const pct = (value / max) * 100;
  const bg = color === "mint" ? "bg-brand-mint-500" : "bg-brand-indigo-500";
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="flex items-center gap-2">
          <span className="font-mono text-[10px] text-muted-foreground">#{idx}</span>
          <span className="font-mono">{name}</span>
        </span>
        <span className="font-mono font-medium tabular-nums">
          {formatNumber(value)} {unit}
        </span>
      </div>
      <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-secondary">
        <div className={cn("h-full rounded-full", bg)} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

// 호스트 맵 — 대체 형태(전력 기준). mgmt 노드 util 이 null 이라 power_watts 로 색상.
function HostMap({ nodes }: { nodes: NodeRow[] }) {
  if (!nodes.length) {
    return <div className="py-6 text-center text-xs text-muted-foreground">데이터 없음</div>;
  }
  const maxW = Math.max(...nodes.map((n) => n.power_watts ?? 0), 1);
  return (
    <div className="grid grid-cols-6 gap-1.5 sm:grid-cols-8 lg:grid-cols-12">
      {nodes.map((n) => {
        const w = n.power_watts ?? 0;
        const pct = w / maxW;
        const color = pct > 0.75 ? "bg-rose-500" : pct > 0.4 ? "bg-amber-400" : "bg-brand-mint-400";
        return (
          <div
            key={n.nodename}
            className={cn(
              "flex aspect-square flex-col items-center justify-center gap-0.5 rounded-md px-0.5 text-center text-white transition-transform hover:scale-105",
              color,
            )}
            title={`${n.nodename}: ${Math.round(w)}W${n.up === false ? " (down)" : ""}`}
          >
            <span className="w-full truncate font-mono text-[8px] leading-none">{n.nodename}</span>
            <span className="text-[11px] font-bold leading-none tabular-nums">{Math.round(w)}W</span>
          </div>
        );
      })}
    </div>
  );
}

// 가속기 사용률 히트맵 — 호스트(행) × 시각(열). 색 농도로 사용률 표현(by=hostname 라벨 수신).
function UtilHeatmapGrid({ data }: { data?: UtilHeatmap }) {
  const hosts = data?.hosts ?? [];
  const times = data?.times ?? [];
  if (!hosts.length) {
    return <div className="py-6 text-center text-xs text-muted-foreground">데이터 없음</div>;
  }
  const cellColor = (v: number | null) => {
    if (v == null) return "bg-muted";
    if (v >= 75) return "bg-rose-500";
    if (v >= 40) return "bg-amber-400";
    if (v > 0) return "bg-brand-mint-400";
    return "bg-brand-mint-100";
  };
  const vendorLabel: Record<string, string> = { gpu: "L40S", furiosa: "RNGD", rebellions: "ATOM" };
  return (
    <div className="space-y-2">
      <div className="overflow-x-auto">
        <div className="min-w-[640px] space-y-1">
          {hosts.map((h) => (
            <div key={`${h.vendor}:${h.host}`} className="flex items-center gap-2">
              <div className="flex w-52 shrink-0 items-center justify-end gap-1 truncate text-right font-mono text-[10px] text-muted-foreground" title={h.host}>
                <span className="rounded bg-secondary px-1 py-0.5 text-[9px]">{vendorLabel[h.vendor] ?? h.vendor}</span>
                <span className="truncate">{h.host}</span>
              </div>
              <div className="flex flex-1 gap-0.5">
                {h.cells.map((v, i) => (
                  <div
                    key={i}
                    className={cn("h-7 flex-1 rounded-sm", cellColor(v))}
                    title={`${h.host} · ${times[i] ?? i} · ${v == null ? "—" : v + "%"}`}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="flex items-center justify-between pl-[calc(13rem+0.5rem)] text-[10px] font-mono text-muted-foreground">
        <span>{times[0] ?? ""}</span>
        <span>{times[Math.floor(times.length / 2)] ?? ""}</span>
        <span>{times[times.length - 1] ?? ""}</span>
      </div>
      <div className="flex items-center gap-3 text-[10px] text-muted-foreground">
        <span className="flex items-center gap-1"><span className="size-2.5 rounded-sm bg-brand-mint-400" /> 낮음</span>
        <span className="flex items-center gap-1"><span className="size-2.5 rounded-sm bg-amber-400" /> 중간</span>
        <span className="flex items-center gap-1"><span className="size-2.5 rounded-sm bg-rose-500" /> 높음(≥75%)</span>
        <span className="flex items-center gap-1"><span className="size-2.5 rounded-sm bg-muted" /> 값 없음</span>
      </div>
    </div>
  );
}

function Marker({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className={cn("h-2 w-3 rounded-sm", color)} />
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono font-medium">{value}</span>
    </div>
  );
}
