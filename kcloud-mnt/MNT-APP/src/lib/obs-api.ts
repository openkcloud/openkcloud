"use client";
/**
 * MNT-OBS 연동 — 옵저버빌리티(KCloud Monitor v2 중개)는 MNT-OBS 모듈을 통해 가져온다.
 *
 * 주소(NEXT_PUBLIC_OBS_API):
 *   - 미설정        → http://localhost:8003 (로컬 dev, MNT-OBS 직접)
 *   - ""(빈값)      → same-origin 상대경로 (/api/obs) — 게이트웨이 운영
 *   - "http://..."  → 고정 주소
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { usePollingInterval } from "@/lib/polling";
import { authFetch } from "@/lib/http";

const RAW = process.env.NEXT_PUBLIC_OBS_API;
export const OBS_BASE = RAW === undefined ? "http://localhost:8003" : RAW;

export interface VendorMetrics {
  vendor: string;
  model: string | null;
  label: string;
  count: number;
  temperature_celsius: number;
  power_watts: number;
  utilization_percent: number;
  memory_used_bytes: number | null;
  memory_total_bytes: number | null;
  healthy_count: number;
  // 파생 교체(2026-10): 전력모드/캡 · Throttling
  power_state: string | null;
  power_limit_percent: number;
  capped_count: number;
  throttled_count: number;
  throttle_reasons: string[];
}

export interface AcceleratorSummary {
  cluster: string;
  vendors: Record<string, VendorMetrics>;
  accelerator_count: number;
  throttled_count: number;
  source_clusters: string[];
}

export interface PowerBreakdownItem {
  key: string;
  watts: number;
  layer: string;
}
export interface PowerBreakdown {
  dimension: string;
  items: PowerBreakdownItem[];
}
export interface PowerSeriesRow {
  t: string;
  server?: number;
  cpu?: number;
  accelerator?: number;
}
export interface PowerTimeseries {
  series: PowerSeriesRow[];
}

// 토큰은 미리 붙여 보낸다(MNT-OBS 의 토큰 확인은 추후)
async function obsFetch<T>(path: string): Promise<T> {
  const res = await authFetch(`${OBS_BASE}/api/obs${path}`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`OBS ${res.status}`);
  return res.json() as Promise<T>;
}

async function obsSend<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${OBS_BASE}/api/obs${path}`, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`OBS ${res.status} ${await res.text()}`);
  return (res.status === 204 ? undefined : await res.json()) as T;
}

export function useAcceleratorSummary(cluster = "mgmt") {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-accel-summary", cluster],
    queryFn: () => obsFetch<AcceleratorSummary>(`/monitoring/accelerator-summary?cluster=${cluster}`),
    refetchInterval: pollMs || false,
  });
}

export interface MonitoringOverview {
  cluster: string;
  power_efficiency_pct_per_watt: number | null;
  accelerator_utilization_percent: number | null;
  total_power_watts: number | null;
  nodes_ready: number | null;
  nodes_total: number | null;
  storage_usage_percent: number | null;
  throttle_events: number | null;
  cards_throttled: number | null;
  cards_total: number | null;
}

export function useMonitoringOverview(cluster = "mgmt") {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-overview", cluster],
    queryFn: () => obsFetch<MonitoringOverview>(`/monitoring/overview?cluster=${cluster}`),
    refetchInterval: pollMs || false,
  });
}

export function usePowerBreakdown(dimension: string) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-power-breakdown", dimension],
    queryFn: () => obsFetch<PowerBreakdown>(`/monitoring/power/breakdown?dimension=${dimension}`),
    refetchInterval: pollMs || false,
  });
}

export function usePowerTimeseries(period = "24h", step = "1h") {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-power-timeseries", period, step],
    queryFn: () => obsFetch<PowerTimeseries>(`/monitoring/power/timeseries?period=${period}&step=${step}&aggregation=avg`),
    refetchInterval: pollMs || false,
  });
}

export interface ClusterRow {
  cluster: string;
  type: string | null;
  parent_cluster: string | null;
  nodes_ready: number | null;
  nodes_total: number | null;
  cpu_percent: number | null;
  memory_percent: number | null;
  accelerator_total: number | null;
  accelerator_util_percent: number | null;
  power_watts: number | null;
  status: string | null;
}
export interface ClusterResources {
  clusters: ClusterRow[];
}

export function useClusterResources() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-clusters"],
    queryFn: () => obsFetch<ClusterResources>(`/monitoring/clusters`),
    refetchInterval: pollMs || false,
  });
}

export interface TopHost {
  host: string;
  vendor: string;
  card_count: number;
  utilization_pct: number;
}
export function useTopHosts(limit = 5) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-top-hosts", limit],
    queryFn: () => obsFetch<{ hosts: TopHost[] }>(`/monitoring/top-hosts?limit=${limit}`),
    refetchInterval: pollMs || false,
  });
}

export interface NodeRow {
  nodename: string;
  role: string | null;
  up: boolean | null;
  power_watts: number | null;
  utilization_percent: number | null;
}
export function useNodes(cluster = "mgmt", enabled = true) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-nodes", cluster],
    enabled: enabled && !!cluster,
    queryFn: () => obsFetch<{ nodes: NodeRow[] }>(`/monitoring/nodes?cluster=${cluster}`),
    refetchInterval: pollMs || false,
  });
}

export function useNodesTimeseries(cluster = "mgmt") {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-nodes-timeseries", cluster],
    queryFn: () => obsFetch<{ series: { i: number; v: number }[] }>(`/monitoring/nodes/timeseries?cluster=${cluster}`),
    refetchInterval: pollMs || false,
  });
}

// 가속기 사용률 히트맵 — 호스트 × 시각(cells 는 times 인덱스와 정렬)
export interface UtilHeatmap {
  times: string[];
  hosts: { host: string; vendor: string; cells: (number | null)[] }[];
}
export function useUtilTimeseries() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-util-timeseries"],
    queryFn: () => obsFetch<UtilHeatmap>(`/monitoring/util-timeseries`),
    refetchInterval: pollMs || false,
  });
}

export interface SparkPoint {
  i: number;
  v: number;
}
export interface KpiTimeseries {
  power_efficiency: SparkPoint[];
  storage: SparkPoint[];
  accel_util: SparkPoint[];
  server_power: SparkPoint[];
  throttle: SparkPoint[];
}
export function useKpiTimeseries() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-kpi-timeseries"],
    queryFn: () => obsFetch<KpiTimeseries>(`/monitoring/kpi-timeseries`),
    refetchInterval: pollMs || false,
  });
}

export interface ErrorLog {
  timestamp: string;
  log_level: string;
  message: string;
  labels: Record<string, string>;
  detected_fields: { xid_code?: string; severity?: string; error_type?: string };
}
export function useErrorLogs(limit = 4) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-error-logs", limit],
    queryFn: () => obsFetch<{ logs: ErrorLog[] }>(`/monitoring/error-logs?limit=${limit}`),
    refetchInterval: pollMs || false,
  });
}

// ── 로그·이벤트 (LogQL 검색/라벨/볼륨/내보내기/스트림) ──
export interface LogRow {
  timestamp: string;
  log_level: string;
  message: string;
  labels: Record<string, string>;
  detected_fields: Record<string, string>;
  trace_id?: string;
  span_id?: string;
}
export interface LogSearchResult {
  logs: LogRow[];
  pagination: { total: number; limit: number; offset: number; has_next: boolean; next_cursor?: string };
  warnings: string[];
}
/** LogQL 검색 — params(쿼리스트링)는 '검색' 시점에 스냅샷으로 넘긴다(매 렌더 재계산 금지). */
export function useLogSearch(params: string | null) {
  return useQuery({
    queryKey: ["obs-logs-search", params],
    queryFn: () => obsFetch<LogSearchResult>(`/monitoring/logs/search?${params}`),
    enabled: !!params,
    refetchOnWindowFocus: false,
  });
}
export function useLogLabels() {
  return useQuery({
    queryKey: ["obs-log-labels"],
    queryFn: () => obsFetch<{ labels: string[] }>(`/monitoring/logs/labels`),
    staleTime: 5 * 60_000,
  });
}
export function useLogLabelValues(label: string | null) {
  return useQuery({
    queryKey: ["obs-log-label-values", label],
    queryFn: () => obsFetch<{ label: string; values: string[] }>(`/monitoring/logs/label-values?label=${encodeURIComponent(label!)}`),
    enabled: !!label,
    staleTime: 5 * 60_000,
  });
}
export interface LogVolumeRow {
  labels: Record<string, string>;
  volume: string; // bytes(문자열)
}
export function useLogVolume() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-log-volume"],
    queryFn: () => obsFetch<{ volume: LogVolumeRow[] }>(`/monitoring/logs/volume`),
    refetchInterval: pollMs || false,
  });
}
/** 스트림(EventSource)·내보내기(다운로드)는 fetch 가 아니라 브라우저 직접 접속 → 전체 URL 생성. */
export function logStreamUrl(params: URLSearchParams): string {
  return `${OBS_BASE}/api/obs/monitoring/logs/stream?${params.toString()}`;
}
export function logExportUrl(format: "csv" | "json", params: URLSearchParams): string {
  const p = new URLSearchParams(params);
  p.set("format", format);
  return `${OBS_BASE}/api/obs/monitoring/logs/export?${p.toString()}`;
}

// ── 알람 (활성 알람·채널·정책) — Monitor v2 /alerts/* 중개 ──
export type AlertSeverity = "critical" | "warning" | "info";
export interface AlertTarget {
  cluster?: string | null;
  node?: string | null;
  acc_id?: string | null;
  model?: string | null;
}
export interface AlertActive {
  fingerprint: string;
  policy_id: string;
  state: string; // firing | resolved ...
  severity: AlertSeverity | string;
  target: AlertTarget;
  value: number;
  message: string;
  started_at: string;
  fired_at: string;
  resolved_at: string | null;
  last_notified_at: string | null;
  acked_at: string | null;
  duration_seconds: number;
}
export interface AlertChannel {
  id: string;
  name: string;
  type: "email" | "slack" | "webhook" | string;
  config: Record<string, unknown>;
  notify_resolved: boolean;
  enabled: boolean;
  created_at?: string;
}
export interface AlertMetric {
  key: string;
  kind: "resource" | "log" | "slo" | string;
  label: string;
  unit: string;
}
export interface AlertPolicy {
  id: string;
  name: string;
  description: string;
  kind: "resource" | "log" | "slo" | string;
  // 조건 컬럼은 conditions[] 를 파싱해 조합(최상위 metric/op/threshold/window_min/for_min 는 레거시 — 참조 금지)
  conditions?: PolicyCondition[];
  metric: string;
  op: string;
  threshold: number;
  window_min: number;
  for_min: number;
  severity: AlertSeverity | string;
  target: AlertTarget;
  selector: string;
  duration: string;
  channel_ids: string[];
  channels: Record<string, unknown>;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export function useActiveAlerts() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-alerts-active"],
    queryFn: () => obsFetch<{ alerts: AlertActive[]; total: number }>(`/monitoring/alerts/active`),
    refetchInterval: pollMs || false,
  });
}
export function useAlertMetrics() {
  return useQuery({
    queryKey: ["obs-alert-metrics"],
    queryFn: () => obsFetch<{ metrics: AlertMetric[] }>(`/monitoring/alerts/metrics`),
    staleTime: 10 * 60_000,
  });
}
export function useAlertChannels() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-alert-channels"],
    queryFn: () => obsFetch<{ channels: AlertChannel[] }>(`/monitoring/alerts/channels`),
    refetchInterval: pollMs || false,
  });
}
export function useAlertPolicies() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["obs-alert-policies"],
    queryFn: () => obsFetch<{ policies: AlertPolicy[] }>(`/monitoring/alerts/policies`),
    refetchInterval: pollMs || false,
  });
}

export function useCreateChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<AlertChannel>) => obsSend<AlertChannel>("POST", `/monitoring/alerts/channels`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-channels"] }),
  });
}
export function useUpdateChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<AlertChannel> }) =>
      obsSend<AlertChannel>("PATCH", `/monitoring/alerts/channels/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-channels"] }),
  });
}
export function useDeleteChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => obsSend<void>("DELETE", `/monitoring/alerts/channels/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-channels"] }),
  });
}
export function useUpdatePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<AlertPolicy> }) =>
      obsSend<AlertPolicy>("PATCH", `/monitoring/alerts/policies/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-policies"] }),
  });
}
export function useDeletePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => obsSend<void>("DELETE", `/monitoring/alerts/policies/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-policies"] }),
  });
}

// ── 정책 생성 폼 ──
export interface PolicyFieldError { field: string; message: string }
/** 422 검증 실패 — detail.errors[] 를 담아 폼이 칸별로 표시 */
export class PolicyApiError extends Error {
  status: number;
  errors: PolicyFieldError[];
  constructor(status: number, errors: PolicyFieldError[], message: string) {
    super(message);
    this.name = "PolicyApiError";
    this.status = status;
    this.errors = errors;
  }
}
export interface PolicyCondition { metric: string; op: string; threshold: number | null; window_min?: number }
export interface AlertPolicyInput {
  name: string;
  description: string;
  conditions: PolicyCondition[];
  selector: string;
  duration: string;
  severity: AlertSeverity;
  channels: string[];
  enabled: boolean;
}
export function useCreatePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: AlertPolicyInput) => {
      const res = await authFetch(`${OBS_BASE}/api/obs/monitoring/alerts/policies`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        let detail: unknown = null;
        try { detail = (await res.json()).detail; } catch { /* 비 JSON 응답 */ }
        const errors =
          detail && typeof detail === "object" && Array.isArray((detail as { errors?: unknown }).errors)
            ? ((detail as { errors: PolicyFieldError[] }).errors)
            : [];
        throw new PolicyApiError(res.status, errors, typeof detail === "string" ? detail : `정책 생성 실패 (${res.status})`);
      }
      return res.json();
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["obs-alert-policies"] }),
  });
}
export function usePreviewPolicy() {
  return useMutation({
    mutationFn: (id: string) => obsSend<Record<string, unknown>>("POST", `/monitoring/alerts/policies/${id}/preview`, {}),
  });
}

// ── 정책 생성 폼: 감시 대상(selector) 옵션 로드 ──
export interface NodeAccelerator { acc_id: string; vendor?: string; model?: string }
export function useNodeAccelerators(cluster: string, node: string) {
  return useQuery({
    queryKey: ["obs-node-accelerators", cluster, node],
    enabled: !!cluster && !!node,
    queryFn: () => obsFetch<{ accelerators: NodeAccelerator[] }>(`/monitoring/clusters/${cluster}/nodes/${node}/accelerators`),
    staleTime: 60_000,
  });
}
export interface SloService { model: string }
export function useSloServices(cluster: string) {
  return useQuery({
    queryKey: ["obs-slo-services", cluster],
    enabled: !!cluster,
    queryFn: () => obsFetch<{ items: SloService[]; warnings: string[] }>(`/monitoring/slo/services?cluster=${encodeURIComponent(cluster)}`),
    staleTime: 30_000,
    retry: 0,
  });
}
