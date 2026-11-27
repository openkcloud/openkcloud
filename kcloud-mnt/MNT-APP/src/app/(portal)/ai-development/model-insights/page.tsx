"use client";
import * as React from "react";
import { ChangeMark } from "@/components/change-mark";
import Link from "next/link";
import {
  Sparkles,
  Database,
  Zap,
  ChevronRight,
  Lightbulb,
  GitBranch,
} from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
  Tooltip as RTooltip,
  ReferenceLine,
} from "recharts";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { FeatureGate } from "@/components/feature-gate";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { cn } from "@/lib/utils";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ainbFetch } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";

// ─── Pipeline stage breakdown ───
// ─── Inference latency breakdown ───

interface GpuMetric {
  node: string;
  gpu: string;
  utilPct: number;
  memUsedMb: number;
  memTotalMb: number;
  tempC: number;
  powerW: number;
}

/** 실측 GPU 상태 스트립 — 클러스터 양쪽 노드의 nvidia-smi 라이브 (3s 폴링) */
function LiveGpuStrip() {
  // GPU 실측(nvidia-smi)은 노드마다 수 초 걸린다 — 페이지 진입을 늦추지 않도록
  // 버튼을 눌렀을 때만 수집을 시작한다
  const [on, setOn] = React.useState(false);
  const { data } = useQuery({
    queryKey: ["metrics", "gpu"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/gpu`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: GpuMetric[] }>;
    },
    enabled: on,
    refetchInterval: 3000,
  });
  if (!on || !data)
    return (
      <div>
        <Button variant="outline" size="sm" disabled={on && !data}
                onClick={() => setOn(true)}>
          {on ? "GPU 상태 측정 중…" : "GPU 실측 보기 (nvidia-smi · 3초 주기)"}
        </Button>
      </div>
    );
  return (
    <div className="grid gap-3 md:grid-cols-2">
      {data.items.map((g) => (
        <Card key={g.node} className="p-4">
          <div className="flex items-center justify-between">
            <div>
              <div className="flex items-center gap-2">
                <span className="size-2 animate-pulse rounded-full bg-brand-mint-500" />
                <span className="text-sm font-semibold">{g.gpu}</span>
              </div>
              <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">
                node/{g.node} · 실측 (nvidia-smi)
              </div>
            </div>
            <Badge variant="success" className="font-mono text-[10px]">LIVE</Badge>
          </div>
          <div className="mt-3 grid grid-cols-4 gap-2 text-center">
            <LiveStat label="사용률" value={`${g.utilPct}%`} />
            <LiveStat label="VRAM" value={`${(g.memUsedMb / 1024).toFixed(1)}/${(g.memTotalMb / 1024).toFixed(0)}GB`} />
            <LiveStat label="온도" value={`${g.tempC}°C`} />
            <LiveStat label="전력" value={`${Math.round(g.powerW)}W`} />
          </div>
          <Progress value={g.utilPct} className="mt-2 h-1.5" />
        </Card>
      ))}
    </div>
  );
}

function LiveStat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="font-mono text-sm font-bold tabular-nums">{value}</div>
    </div>
  );
}

interface LlmDetail {
  model: string;
  requests: number;
  latency: {
    ttftMs: number | null; tpotMs: number | null; e2eMs: number | null;
    p50Ms: number | null; p95Ms: number | null; p99Ms: number | null;
    ttftP95Ms: number | null; tpotP95Ms: number | null;
  };
  breakdown: { stage: string; ms: number; key: string }[];
  cache: { gpuUsagePct: number; prefixHitPct: number | null; cpuUsagePct: number };
  queue: { running: number; waiting: number; swapped: number; preemptions: number };
  throughput: { genTokPerSec: number; promptTokPerSec: number;
                avgOutputTokens: number; p95OutputTokens: number | null };
  series: { t: string; p50: number; p95: number; p99: number }[];
}

function useLlmDetail() {
  const svc = useLlm();
  return useQuery({
    queryKey: ["metrics", "llm-detail", svc],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/llm/detail${svcQ(svc)}`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<LlmDetail>;
    },
    refetchInterval: 10000,
    retry: false,
  });
}

interface ProfileHotspot {
  component: string; ms: number; pct: number; calls: number;
  reason: string; ops: string[];
}
interface ProfileResult {
  samples: number; maxTokens: number; totalMs: number; events: number;
  hotspots: ProfileHotspot[];
}
interface ProfileState {
  running: boolean; at: string | null; error: string | null;
  result: ProfileResult | null; kernelLevel: boolean; kernelNote: string;
  /** auto = 1분 주기 자동 샘플링 결과, manual = 버튼으로 측정한 결과 */
  mode?: "auto" | "manual" | null; auto?: boolean; intervalSec?: number;
}

interface InsightItem {
  severity: "critical" | "warning" | "info" | "ok";
  title: string;
  detail: string;
  evidence: { label: string; value: string; detail?: string }[];
  actions: string[];
}

const SEV: Record<InsightItem["severity"], {
  badge: "destructive" | "warning" | "outline" | "success";
  label: string; card: string; icon: string;
}> = {
  critical: { badge: "destructive", label: "심각", card: "border-rose-200 bg-gradient-to-br from-rose-50 to-transparent", icon: "bg-rose-100 text-rose-700" },
  warning: { badge: "warning", label: "주의", card: "border-amber-200 bg-gradient-to-br from-amber-50 to-transparent", icon: "bg-amber-100 text-amber-700" },
  info: { badge: "outline", label: "참고", card: "border-sky-200 bg-gradient-to-br from-sky-50 to-transparent", icon: "bg-sky-100 text-sky-700" },
  ok: { badge: "success", label: "정상", card: "border-brand-mint-200 bg-gradient-to-br from-brand-mint-50 to-transparent", icon: "bg-brand-mint-100 text-brand-mint-700" },
};

/** 실측 파이프라인 소견 — Argo 실행 기록에서 직접 도출 (임의 수치 없음) */
function LivePipelineInsight() {
  const { data } = useQuery({
    queryKey: ["metrics", "pipeline-runs"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/pipeline-runs`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: WfRun[] }>;
    },
    refetchInterval: 15000,
    retry: false,
  });
  const runs = data?.items ?? [];
  if (!runs.length) return null;

  const failed = runs.filter((r) => r.phase === "Failed");
  // 캐시로 건너뛴 실행은 단계 시간이 실제 계산 시간이 아니라 준비 시간이다.
  // 이런 실행을 병목 근거로 쓰면 엉뚱한 단계를 지목하게 되므로 뒤로 미룬다.
  const real = runs.filter((r) => !r.cachedSteps);
  const worst = [...(real.length ? real : runs)]
    .sort((a, b) => b.bottleneckPct - a.bottleneckPct)[0];
  const avgSec = Math.round(runs.reduce((a, r) => a + r.totalSec, 0) / runs.length);
  const critical = failed.length > 0;

  return (
    <Card className={cn(
      "overflow-hidden",
      critical ? "border-rose-200 bg-gradient-to-br from-rose-50 to-transparent"
               : "border-amber-200 bg-gradient-to-br from-amber-50 to-transparent",
    )}>
      <CardContent className="p-5">
        <div className="flex items-start gap-4">
          <div className={cn(
            "flex size-10 shrink-0 items-center justify-center rounded-full",
            critical ? "bg-rose-100 text-rose-700" : "bg-amber-100 text-amber-700",
          )}>
            <Sparkles className="size-5" />
          </div>
          <div className="flex-1 space-y-3">
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={critical ? "destructive" : "warning"}>
                  {critical ? "심각" : "주의"}
                </Badge>
                <Badge variant="success" className="text-[10px]">실측 기반 자동 진단</Badge>
              </div>
              <h2 className="mt-1 text-lg font-bold leading-tight">
                {critical
                  ? `최근 실행 ${runs.length}건 중 ${failed.length}건이 실패했습니다`
                  : `${worst.pipeline || worst.workflow} — 학습 시간의 ${worst.bottleneckPct}%가 '${worst.bottleneck}' 단계에 몰려 있습니다`}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {critical
                  ? `실패한 워크플로우: ${failed.map((r) => r.workflow).join(", ")} — 해당 단계 로그를 먼저 확인하세요.`
                  : `${worst.workflow} 실행 기준 총 ${worst.totalSec}초 중 ${worst.bottleneck} 단계가 가장 오래 걸렸습니다. 이 단계를 줄이는 것이 전체 시간 단축에 가장 효과가 큽니다.`}
              </p>
            </div>

            <div className="grid gap-2 md:grid-cols-3">
              <Evidence label="병목 단계" value={worst.bottleneck}
                        detail={`전체의 ${worst.bottleneckPct}%`} color="amber" />
              <Evidence label="평균 실행 시간" value={`${avgSec}초`}
                        detail={`최근 ${runs.length}회 기준`} color="indigo" />
              <Evidence label="성공 / 실패"
                        value={`${runs.length - failed.length} / ${failed.length}`}
                        detail="최근 실행" color={critical ? "rose" : "indigo"} />
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" asChild>
                <Link href="/ai-development/pipelines">파이프라인 편집</Link>
              </Button>
              <Button size="sm" variant="outline" asChild>
                <Link href="/monitoring/logs">실행 로그 보기</Link>
              </Button>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

interface ServingMetric {
  name: string; namespace: string; format: string; accelerator: string;
  requests: number; totalMs: number; p95Ms: number | null;
  breakdown: { stage: string; ms: number; key: string }[];
  /** 지표를 실제로 읽어왔는지 — false면 배포는 됐으나 아직 측정 전 */
  measured?: boolean;
}

/** 배포된 모델 서비스의 추론 지표 — KServe 래퍼 실측 */
function LiveServingMetrics() {
  const { match } = useProjectContext();
  const { data } = useQuery({
    queryKey: ["metrics", "serving"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/serving`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: ServingMetric[] }>;
    },
    refetchInterval: 10000,
    retry: false,
    // 폴링이 한 번 실패해도 직전 값을 그대로 둔다 —
    // 그렇지 않으면 카드가 나왔다 사라졌다 해서 배포가 풀린 것처럼 보인다
    placeholderData: (prev) => prev,
  });
  // 위에서 고른 프로젝트(워크스페이스)에 배포된 모델만
  const items = (data?.items ?? []).filter((m) => match(m.namespace));
  if (!items.length) return null;

  const COLORS: Record<string, string> = {
    pre: "#94a3b8",
    infer: "var(--brand-indigo-500)",
    post: "var(--brand-mint-500)",
  };

  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Database className="size-4 text-brand-mint-600" />
          배포 모델 추론 성능
          <Badge variant="success" className="text-[10px]">실측</Badge>
        </CardTitle>
        <CardDescription>
          우리가 학습시켜 배포한 판정·예측 모델 — 요청 1건이 전처리·추론·후처리에
          각각 얼마나 쓰는지 보여줍니다 (대화형 AI는 아래 카드에서 따로 봅니다)
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {items.map((m) => {
          const total = m.breakdown.reduce((a, b) => a + b.ms, 0) || 1;
          return (
            <div key={`${m.namespace}/${m.name}`} className="space-y-2">
              <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
                <span className="font-medium">{m.name}</span>
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Badge variant="outline" className="text-[10px]">{m.format}</Badge>
                  <Badge variant="outline" className="text-[10px]">{m.accelerator}</Badge>
                  {m.measured === false && (
                    <Badge variant="outline" className="text-[10px] text-amber-700">
                      측정 대기
                    </Badge>
                  )}
                  누적 {m.requests}건 · 평균{" "}
                  <span className="font-mono font-semibold text-foreground">
                    {m.totalMs}ms
                  </span>
                  {m.p95Ms != null && <>· P95 {m.p95Ms}ms</>}
                </span>
              </div>
              <div className="flex h-6 w-full overflow-hidden rounded-md">
                {m.breakdown.map((b) => (
                  <div key={b.key}
                       style={{ width: `${Math.max((b.ms / total) * 100, 1)}%`,
                                background: COLORS[b.key] }}
                       className="flex items-center justify-center text-[10px] font-semibold text-white"
                       title={`${b.stage}: ${b.ms}ms`}>
                    {(b.ms / total) * 100 >= 15 ? `${Math.round((b.ms / total) * 100)}%` : ""}
                  </div>
                ))}
              </div>
              <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground">
                {m.breakdown.map((b) => (
                  <span key={b.key} className="inline-flex items-center gap-1">
                    <span className="size-2 rounded-sm" style={{ background: COLORS[b.key] }} />
                    {b.stage} {b.ms}ms
                  </span>
                ))}
              </div>
            </div>
          );
        })}
        <p className="text-[11px] leading-snug text-muted-foreground">
          추론 구간이 대부분이면 모델이나 가속기가 병목이고, 전처리가 길면
          입력 데이터 처리(이미지 변환 등)가 병목입니다.
        </p>
      </CardContent>
    </Card>
  );
}

/** 실측 병목 소견 — 살아있는 지표에 규칙을 적용해 자동 생성 (임의 수치 없음) */
function LiveInsights() {
  const svc = useLlm();
  const { data } = useQuery({
    queryKey: ["metrics", "llm-insights", svc],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/llm/insights${svcQ(svc)}`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{
        sloP99Ms: number; model: string; at: string; items: InsightItem[];
      }>;
    },
    refetchInterval: 15000,
    retry: false,
  });
  if (!data?.items?.length) return null;

  return (
    <div className="space-y-3">
      {data.items.map((it, i) => {
        const sv = SEV[it.severity];
        return (
          <Card key={i} className={cn("overflow-hidden", sv.card)}>
            <CardContent className="p-5">
              <div className="flex items-start gap-4">
                <div className={cn("flex size-10 shrink-0 items-center justify-center rounded-full", sv.icon)}>
                  <Sparkles className="size-5" />
                </div>
                <div className="flex-1 space-y-3">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={sv.badge}>{sv.label}</Badge>
                      <Badge variant="success" className="text-[10px]">실측 기반 자동 진단</Badge>
                      <span className="text-xs text-muted-foreground">{data.at} 기준</span>
                    </div>
                    <h2 className="mt-1 text-lg font-bold leading-tight">{it.title}</h2>
                    <p className="mt-1 text-sm text-muted-foreground">{it.detail}</p>
                  </div>

                  {it.evidence.length > 0 && (
                    <div className="grid gap-2 md:grid-cols-3">
                      {it.evidence.map((e) => (
                        <div key={e.label} className="rounded-lg border border-border bg-card p-2.5">
                          <div className="text-[10px] uppercase text-muted-foreground">{e.label}</div>
                          <div className="font-mono text-base font-bold tabular-nums">{e.value}</div>
                          {e.detail && <div className="text-[11px] text-muted-foreground">{e.detail}</div>}
                        </div>
                      ))}
                    </div>
                  )}

                  {it.actions.length > 0 && (
                    <div className="space-y-2 rounded-lg border border-border bg-card p-3">
                      <div className="flex items-center gap-1.5 text-xs font-medium">
                        <Lightbulb className="size-3.5 text-amber-600" />
                        권장 조치
                      </div>
                      <ul className="space-y-1 text-xs text-muted-foreground">
                        {it.actions.map((a) => (
                          <li key={a} className="flex items-start gap-1.5">
                            <ChevronRight className="mt-0.5 size-3 shrink-0 text-brand-indigo-500" />
                            {a}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}

                  {i === 0 && (
                    <div className="flex flex-wrap items-center gap-2">
                      <Button variant="outline" size="sm" asChild>
                        <Link href="/ai-development/serving">서빙 상태 보기</Link>
                      </Button>
                      <Button variant="outline" size="sm" asChild>
                        <Link href="/monitoring/traces">트레이스 보기</Link>
                      </Button>
                    </div>
                  )}
                </div>
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}

/** 실측 핫스팟 — 화면을 열어 둔 동안 1분 주기로 추론 1회를 짧게 추적(샘플링), 버튼은 3회 즉시 측정 */
function LiveHotspots() {
  const svc = useLlm();
  const qc = useQueryClient();
  const { data: st } = useQuery({
    queryKey: ["metrics", "llm-profile", svc],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/llm/profile${svcQ(svc)}`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<ProfileState>;
    },
    retry: false,
    // 조회하는 동안에만 백엔드가 1분 주기 샘플링을 이어간다 — 새 결과도 이 주기로 받아온다
    refetchInterval: 15000,
  });
  const run = useMutation({
    mutationFn: async () => {
      const res = await ainbFetch(
        `/metrics/llm/profile?samples=3&max_tokens=48`
        + (svc ? `&service=${encodeURIComponent(svc)}` : ""),
        { method: "POST" },
      );
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      return res.json() as Promise<ProfileResult>;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["metrics", "llm-profile"] }),
  });

  // 최신 결과는 백엔드 상태가 들고 있다(수동 측정 후에도 invalidate로 갱신) — 자동 샘플이 오면 그걸로 바뀐다
  const r = st?.result ?? run.data ?? null;
  const max = r?.hotspots?.[0]?.ms || 1;

  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              핫스팟 — 연산별 소요 시간
              <Badge variant="success" className="text-[10px]">실측</Badge>
            </CardTitle>
            <CardDescription>
              이 화면을 열어 두면 <b>1분 주기로 샘플링</b>합니다 — 추론 1회를 짧게 추적해 어떤 연산이 시간을 쓰는지 봅니다
              (실시간 아님, 추론 오버헤드 최소화). 화면을 닫으면 멈춥니다. 더 정확히 보려면 버튼으로 3회 측정합니다.
            </CardDescription>
          </div>
          <Button size="sm" onClick={() => run.mutate()} disabled={run.isPending}>
            {run.isPending ? "측정 중… (약 40초)" : "지금 3회 측정"}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        {run.isError && (
          <div className="rounded-md border border-rose-200 bg-rose-50 p-3 text-xs text-rose-800">
            측정 실패: {(run.error as Error).message}
          </div>
        )}
        {!r && !run.isPending && (
          <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
            {st?.running ? "첫 샘플을 측정하는 중입니다 — 잠시 후 결과가 나타납니다." : "아직 측정 기록이 없습니다 — 1분 안에 자동으로 첫 샘플을 측정합니다. 바로 보려면 버튼을 누르세요."}
          </div>
        )}
        {r && (
          <>
            <div className="pb-1 text-[11px] text-muted-foreground">
              {st?.mode === "auto" ? `자동 샘플링(${Math.round((st.intervalSec ?? 60) / 60)}분 주기)` : "수동 측정"} ·
              샘플 {r.samples}회 · 최대 {r.maxTokens}토큰 · 추적 이벤트 {r.events.toLocaleString()}건 ·
              합계 {r.totalMs}ms{st?.at ? ` · 측정 시각 ${st.at}` : ""}{st?.running ? " · 새 샘플 측정 중…" : ""}
            </div>
            {r.hotspots.map((h, i) => (
              <div key={h.component} className="flex items-center gap-3 rounded-lg border border-border p-3">
                <Badge variant="ghost" className="font-mono">#{i + 1}</Badge>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{h.component}</div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">
                    {h.reason} · {h.calls.toLocaleString()}회 호출 · {h.ops.join(", ")}
                  </div>
                </div>
                <div className="w-36">
                  <div className="text-right font-mono text-sm font-semibold tabular-nums">
                    {h.ms} ms <span className="text-muted-foreground">({h.pct}%)</span>
                  </div>
                  <Progress
                    value={(h.ms / max) * 100}
                    indicatorClassName={i === 0 ? "bg-rose-500" : i < 3 ? "bg-amber-500" : "bg-brand-mint-500"}
                  />
                </div>
              </div>
            ))}
          </>
        )}
        {st && !st.kernelLevel && (
          <p className="rounded-md border border-sky-200 bg-sky-50/70 p-2 text-[11px] leading-snug text-sky-900">
            <b>측정 범위 안내:</b> {st.kernelNote}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

/** 실측 응답 시간 분포 — vLLM 히스토그램에서 계산한 분위수 */
function LiveLatencySection() {
  const { data: d } = useLlmDetail();
  if (!d) return null;
  const BR_COLORS: Record<string, string> = {
    ttft: "var(--brand-indigo-500)",
    decode: "var(--brand-mint-500)",
    other: "#94a3b8",
  };
  const total = d.breakdown.reduce((a, b) => a + b.ms, 0) || 1;

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card className="border-brand-mint-200">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            응답 시간 분포
            <Badge variant="success" className="text-[10px]">실측</Badge>
          </CardTitle>
          <CardDescription>
            누적 {d.requests}건의 실제 요청 · P50/P95/P99 (백엔드 수집 시계열)
          </CardDescription>
        </CardHeader>
        <CardContent>
          {/* 아래 그래프의 선과 같은 색을 찍어 범례를 겸한다 */}
          <div className="mb-3 grid grid-cols-3 gap-2 text-center">
            {([
              ["P50", d.latency.p50Ms, "var(--brand-mint-500)", "절반은 이 시간 안에"],
              ["P95", d.latency.p95Ms, "var(--brand-indigo-500)", "100명 중 95명은 이 안에"],
              ["P99", d.latency.p99Ms, "#e11d48", "가장 느린 경우까지"],
            ] as const).map(([k, v, color, hint]) => (
              <div key={k} className="rounded-lg bg-secondary/40 p-2">
                <div className="flex items-center justify-center gap-1.5">
                  <span className="size-2 rounded-full" style={{ background: color }} />
                  <span className="text-[10px] uppercase text-muted-foreground">{k}</span>
                </div>
                <div className="font-mono text-lg font-bold tabular-nums">
                  {v != null ? `${v}ms` : "—"}
                </div>
                <div className="text-[10px] leading-tight text-muted-foreground">{hint}</div>
              </div>
            ))}
          </div>
          <p className="mb-2 text-[11px] leading-snug text-muted-foreground">
            응답 시간을 느린 순으로 줄 세운 값입니다. 평균은 아주 느린 몇 건을 숨기기 때문에,
            <b> 가장 느린 쪽까지</b> 확인하는 방식을 씁니다.
          </p>
          {d.series.length > 1 ? (
            <ChartContainer
              config={{
                p50: { label: "P50", color: "var(--brand-mint-500)" },
                p95: { label: "P95", color: "var(--brand-indigo-500)" },
                p99: { label: "P99", color: "#e11d48" },
              }}
              className="h-[200px] w-full"
            >
              <LineChart data={d.series}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 9 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false}
                       tickFormatter={(v) => `${v}ms`} />
                <RTooltip content={<ChartTooltipContent indicator="line" />} />
                <ReferenceLine
                  y={1000}
                  stroke="#e11d48"
                  strokeDasharray="4 4"
                  label={{ value: "목표 1s", fill: "#e11d48", fontSize: 10, position: "right" }}
                />
                <Line type="monotone" dataKey="p50" stroke="var(--brand-mint-500)" strokeWidth={1.5} dot={false} />
                <Line type="monotone" dataKey="p95" stroke="var(--brand-indigo-500)" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="p99" stroke="#e11d48" strokeWidth={2} dot={false} />
              </LineChart>
            </ChartContainer>
          ) : (
            <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
              시계열을 모으는 중입니다 — 잠시 후 그래프가 그려집니다
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="border-brand-mint-200">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            응답 지연 구간 분해
            <Badge variant="success" className="text-[10px]">실측</Badge>
          </CardTitle>
          <CardDescription>
            평균 {d.latency.e2eMs ?? "—"}ms가 어디서 소요되는지 —
            TTFT {d.latency.ttftMs ?? "—"}ms · TPOT {d.latency.tpotMs ?? "—"}ms
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid grid-cols-3 gap-2 rounded-md border border-border bg-secondary/30 p-2 text-[11px]">
            <div>
              <div className="font-mono font-semibold">TTFT</div>
              <div className="text-muted-foreground">Time To First Token — 첫 토큰까지 (프롬프트 처리 포함)</div>
              <div className="mt-0.5 font-mono">{d.latency.ttftMs ?? "—"}ms</div>
            </div>
            <div>
              <div className="font-mono font-semibold">TPOT</div>
              <div className="text-muted-foreground">Time Per Output Token — 토큰 1개 생성 평균</div>
              <div className="mt-0.5 font-mono">{d.latency.tpotMs ?? "—"}ms</div>
            </div>
            <div>
              <div className="font-mono font-semibold">E2E</div>
              <div className="text-muted-foreground">요청 전체 완료 시간 (TTFT + TPOT × 출력 토큰)</div>
              <div className="mt-0.5 font-mono">
                {d.latency.e2eMs ?? "—"}ms / {d.throughput.avgOutputTokens} tok
              </div>
            </div>
          </div>
          <div className="flex h-7 w-full overflow-hidden rounded-md">
            {d.breakdown.map((b) => (
              <div key={b.key}
                   style={{ width: `${Math.max((b.ms / total) * 100, 2)}%`,
                            background: BR_COLORS[b.key] }}
                   className="flex items-center justify-center text-[10px] font-semibold text-white"
                   title={`${b.stage}: ${b.ms}ms`}>
                {(b.ms / total) * 100 >= 15 ? `${Math.round((b.ms / total) * 100)}%` : ""}
              </div>
            ))}
          </div>
          {d.breakdown.map((b) => (
            <div key={b.key} className="flex items-center gap-2 text-xs">
              <span className="size-2.5 rounded-sm" style={{ background: BR_COLORS[b.key] }} />
              <span className="flex-1">{b.stage}</span>
              <span className="font-mono tabular-nums">{b.ms}ms</span>
              <span className="w-10 text-right font-mono text-muted-foreground tabular-nums">
                {Math.round((b.ms / total) * 100)}%
              </span>
            </div>
          ))}
          <p className="text-[11px] leading-snug text-muted-foreground">
            첫 토큰까지가 길면 대기·프롬프트 처리가 병목이고, 디코드가 길면 생성 속도가 병목입니다.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

/** KV cache·대기열·처리량 실측 — ETRI 검토의견(KV cache 지표 검증) 대응 */
function LiveCacheQueueSection() {
  const { data: d } = useLlmDetail();
  if (!d) return null;
  const cells: { label: string; value: string; hint: string }[] = [
    { label: "KV Cache 사용률", value: `${d.cache.gpuUsagePct}%`, hint: "GPU 캐시 점유" },
    { label: "Prefix 적중률",
      value: d.cache.prefixHitPct == null ? "측정 대기" : `${d.cache.prefixHitPct}%`,
      hint: "반복 프롬프트 재사용" },
    { label: "처리 중", value: `${d.queue.running}건`, hint: "동시 처리 요청" },
    { label: "대기열", value: `${d.queue.waiting}건`, hint: "순서 기다리는 요청" },
    { label: "선점 발생", value: `${d.queue.preemptions}회`, hint: "메모리 부족 중단" },
    { label: "생성 처리량", value: `${d.throughput.genTokPerSec} tok/s`, hint: "초당 토큰 생성" },
  ];
  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Database className="size-4 text-brand-mint-600" />
          KV Cache · 대기열 · 처리량
          <Badge variant="success" className="text-[10px]">실측</Badge>
        </CardTitle>
        <CardDescription>
          vLLM이 제공하는 표준 지표입니다 — 요청당 평균 출력 {d.throughput.avgOutputTokens} 토큰.
          캐시 부족 판단은 vLLM 표준 메트릭 <code className="font-mono text-[11px]">gpu_cache_usage_perc</code>(사용률)와{" "}
          <code className="font-mono text-[11px]">num_preemptions_total</code>(선점 횟수)를 함께 본다 — 사용률이 높고 선점이 늘면 캐시가 모자란 것이다.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
          {cells.map((c) => (
            <div key={c.label} className="rounded-lg bg-secondary/40 p-3 text-center">
              <div className="text-[10px] uppercase text-muted-foreground">{c.label}</div>
              <div className="mt-0.5 font-mono text-base font-bold tabular-nums">{c.value}</div>
              <div className="text-[10px] text-muted-foreground">{c.hint}</div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

/** 파이프라인 실행 시간 추이 — KFP 실행 기록 실측 */
function LiveRunHistory() {
  const { data } = useQuery({
    queryKey: ["metrics", "run-history"],
    queryFn: async () => {
      const list = await ainbFetch(`/pipelines`).then((r) => r.json());
      const withRuns = (list.items ?? []).filter((p: { runs: number }) => p.runs > 0);
      if (!withRuns.length) return { name: "", items: [] };
      const target = withRuns[withRuns.length - 1];
      const res = await ainbFetch(`/pipelines/${target.id}/run-history`);
      if (!res.ok) throw new Error(`${res.status}`);
      const d = await res.json();
      return { name: target.name as string, items: d.items };
    },
    refetchInterval: 30000,
    retry: false,
  });
  if (!data?.items?.length) return null;
  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          실행 시간 추이
          <Badge variant="success" className="text-[10px]">실측</Badge>
        </CardTitle>
        <CardDescription>{data.name} · 최근 {data.items.length}회 실행의 총 소요시간</CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer config={{ durSec: { label: "소요(초)", color: "var(--brand-indigo-500)" } }}
                        className="h-[200px] w-full">
          <BarChart data={data.items}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} />
            <XAxis dataKey="at" tick={{ fontSize: 9 }} axisLine={false} tickLine={false} />
            <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false}
                   tickFormatter={(v) => `${v}s`} />
            <RTooltip content={<ChartTooltipContent />} />
            <Bar dataKey="durSec" fill="var(--brand-indigo-500)" radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  );
}

/** 추론 탭의 LLM 영역 — 모델을 하나 고르면 아래 카드들이 모두 그 모델을 본다 */
function InferenceLlmSection() {
  const [picked, setPicked] = React.useState<string | undefined>();
  const { match } = useProjectContext();
  const { data } = useQuery({
    queryKey: ["metrics", "llms"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/llms`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: LlmEntry[] }>;
    },
    refetchInterval: 10000,
    retry: false,
  });
  // 위에서 고른 프로젝트(워크스페이스)에 배포된 LLM만
  const items = (data?.items ?? []).filter((m) => match(m.namespace));
  if (!items.length) return null;

  // 아직 안 골랐으면 첫 번째(준비된 것 우선)를 본다 — 고른 것이 목록에서 빠지면(프로젝트 전환) 다시 첫 번째
  const current = (picked && items.some((m) => m.name === picked) ? picked : undefined)
    ?? (items.find((m) => m.ready) ?? items[0]).name;

  return (
    <LlmCtx.Provider value={current}>
      <Card className="border-brand-mint-200">
        <CardContent className="flex flex-wrap items-center gap-3 p-4">
          <span className="text-sm font-medium">보고 있는 AI</span>
          <Select value={current} onValueChange={setPicked}>
            <SelectTrigger className="w-[280px]"><SelectValue /></SelectTrigger>
            <SelectContent>
              {items.map((m) => (
                <SelectItem key={m.name} value={m.name}>
                  {m.name} · {m.modelId}{m.ready ? "" : " (준비 중)"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-[11px] text-muted-foreground">
            아래 지표는 모두 <b>{current}</b> 하나를 측정한 값입니다.
            AI가 {items.length}개 배포돼 있어 골라서 볼 수 있습니다.
          </span>
        </CardContent>
      </Card>

      <LiveLlmCard only={current} />
      <ChangeMark ids={["CR-AID-08"]}>
        <LiveLatencySection />
      </ChangeMark>
      <ChangeMark ids={["CR-AID-07"]}>
        <LiveCacheQueueSection />
      </ChangeMark>
      {/* 자동 진단·핫스팟 프로파일링은 최종 형상 (CR-AID-09, scope ai.model-insights.aiops) */}
      <FeatureGate id="ai.model-insights.aiops">
        <LiveInsights />
      </FeatureGate>
      <FeatureGate id="ai.model-insights.aiops" fallback="none">
        <ChangeMark ids={["CR-AID-06"]}>
          <LiveHotspots />
        </ChangeMark>
      </FeatureGate>
    </LlmCtx.Provider>
  );
}

/** 지금 보고 있는 LLM — 추론 탭의 카드들이 같은 모델을 보도록 공유한다 */
const LlmCtx = React.createContext<string | undefined>(undefined);
const useLlm = () => React.useContext(LlmCtx);
/** 쿼리 문자열로 붙일 대상 지정 */
const svcQ = (s?: string) => (s ? `?service=${encodeURIComponent(s)}` : "");

interface LlmEntry {
  name: string; namespace: string; modelId: string; ready: boolean;
  requests: number; ttftMs: number | null; tpotMs: number | null;
  e2eMs: number | null; tokensPerSec: number | null;
  kvCacheUsagePct: number; runningReqs: number;
}

/** 실측 LLM 추론 지표 — 배포된 LLM 전부를 각각 보여준다 */
function LiveLlmCard({ only }: { only?: string }) {
  const [testing, setTesting] = React.useState<string | null>(null);
  const { data, refetch } = useQuery({
    queryKey: ["metrics", "llms"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/llms`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: LlmEntry[] }>;
    },
    refetchInterval: 5000,
    retry: false,
  });
  // 선택된 모델만 본다 (지정이 없으면 전부)
  const shown = (data?.items ?? []).filter((m) => !only || m.name === only);
  if (!shown.length) return null;

  async function fireTest(name: string) {
    setTesting(name);
    try {
      const res = await ainbFetch(
        `/metrics/llm/chat?service=${encodeURIComponent(name)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            prompt: "클라우드에서 GPU를 효율적으로 쓰는 팁을 한 문장으로.",
            maxTokens: 64,
          }),
        });
      if (!res.ok) throw new Error(`${res.status}`);
      await res.json();
      refetch();
    } finally {
      setTesting(null);
    }
  }

  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Zap className="size-4 text-brand-mint-600" />
          실측 LLM 추론 지표
          <Badge variant="success" className="text-[10px]">LIVE</Badge>
        </CardTitle>
        <CardDescription>
          {shown[0].modelId} · vLLM — 누적 {shown[0].requests}건 기준
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {shown.map((m) => (
          <div key={`${m.namespace}/${m.name}`} className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="text-sm">
                <span className="font-semibold">{m.name}</span>
                <span className="ml-2 font-mono text-[11px] text-muted-foreground">
                  {m.modelId}
                </span>
                {!m.ready && (
                  <Badge variant="warning" className="ml-2 text-[10px]">준비 중</Badge>
                )}
              </div>
              <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                누적 {m.requests}건
                <Button size="sm" variant="outline" disabled={!m.ready || testing === m.name}
                        onClick={() => fireTest(m.name)}>
                  {testing === m.name ? "호출 중…" : "테스트 요청 보내기"}
                </Button>
              </div>
            </div>
            <div className="grid grid-cols-3 gap-3 md:grid-cols-6">
              <LlmStat label="TTFT" hint="첫 토큰까지" value={m.ttftMs != null ? `${m.ttftMs}ms` : "—"} />
              <LlmStat label="TPOT" hint="토큰당 생성" value={m.tpotMs != null ? `${m.tpotMs}ms` : "—"} />
              <LlmStat label="E2E 지연" hint="요청→완료" value={m.e2eMs != null ? `${m.e2eMs}ms` : "—"} />
              <LlmStat label="처리량" hint="tokens/sec" value={m.tokensPerSec != null ? `${m.tokensPerSec}` : "—"} />
              <LlmStat label="KV Cache" hint="사용률" value={`${m.kvCacheUsagePct}%`} />
              <LlmStat label="처리 중" hint="동시 요청" value={`${m.runningReqs}건`} />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function LlmStat({ label, hint, value }: { label: string; hint: string; value: string }) {
  return (
    <div className="rounded-lg bg-secondary/40 p-3 text-center">
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="font-mono text-lg font-bold tabular-nums">{value}</div>
      <div className="text-[10px] text-muted-foreground">{hint}</div>
    </div>
  );
}

interface WfStep {
  name: string; durSec: number; pct: number; phase: string;
  /** 이전 실행 결과를 재사용한 단계 — 다시 계산하지 않았다 */
  cached?: boolean;
}
interface WfRun {
  workflow: string; workflowId?: string; phase: string; startedAt: string;
  totalSec: number; steps: WfStep[]; bottleneck: string; bottleneckPct: number;
  cachedSteps?: number;
  /** 어느 모델(파이프라인)의 실행인지 */
  pipeline?: string;
  /** 이 파이프라인이 만든 모델을 실제로 서비스하는 곳 */
  services?: string[];
}

/** 실측 파이프라인 병목 — Argo Workflow 단계별 실행시간 분해 */
function LivePipelineBottleneck() {
  const { data } = useQuery({
    queryKey: ["metrics", "pipeline-runs"],
    queryFn: async () => {
      const res = await ainbFetch(`/metrics/pipeline-runs`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: WfRun[] }>;
    },
    refetchInterval: 10000,
    retry: false,
    placeholderData: (prev) => prev,
  });
  if (!data?.items.length) return null;
  const barColors = ["bg-brand-indigo-400", "bg-brand-mint-500", "bg-violet-400",
                     "bg-amber-400", "bg-sky-400"];

  // 추론 성능 카드와 같은 축으로 읽히도록 '배포된 모델' 하나당 한 줄로 묶는다.
  // 한 모델은 여러 번 실행될 수 있으므로, 실제로 계산을 수행한 최신 실행을
  // 대표로 쓴다(캐시로 건너뛴 실행은 단계 시간이 학습 시간이 아니다).
  const byService = (() => {
    const pick = new Map<string, WfRun>();
    for (const run of data.items) {
      for (const sv of run.services ?? []) {
        const cur = pick.get(sv);
        const better =
          !cur ||
          (!!cur.cachedSteps && !run.cachedSteps) ||
          (!!cur.cachedSteps === !!run.cachedSteps &&
            run.startedAt > cur.startedAt);
        if (better) pick.set(sv, run);
      }
    }
    return [...pick.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([service, run]) => ({ service, run }));
  })();
  if (!byService.length) return null;
  return (
    <Card className="border-brand-mint-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <GitBranch className="size-4 text-brand-mint-600" />
          배포 모델 학습 성능
          <Badge variant="success" className="text-[10px]">실측</Badge>
        </CardTitle>
        <CardDescription>
          배포된 모델을 만든 학습이 각 단계에 얼마나 쓰는지 보여줍니다 —
          가장 긴 단계가 병목입니다
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {byService.map(({ service, run }) => (
          <div key={service} className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-sm font-bold">{service}</span>
              <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <Badge variant="outline" className="text-[10px]">{run.pipeline}</Badge>
                <span className="font-mono text-[11px]">{run.workflow}</span>
                총 <b className="font-mono text-foreground">{run.totalSec}초</b> ·{" "}
                <span className="font-semibold text-amber-700">
                  병목: {run.bottleneck} ({run.bottleneckPct}%)
                </span>
                <Badge className="text-[10px]"
                       variant={run.phase === "Succeeded" ? "success"
                                : run.phase === "Failed" ? "destructive" : "warning"}>
                  {run.phase}
                </Badge>
              </span>
            </div>
            <div className="flex h-6 w-full overflow-hidden rounded-md">
              {run.steps.map((s, i) => (
                <div key={s.name}
                     className={cn("flex items-center justify-center text-[10px] font-semibold text-white",
                                   barColors[i % barColors.length])}
                     style={{ width: `${Math.max(s.pct, 4)}%` }}
                     title={`${s.name}: ${s.durSec}s (${s.pct}%)`}>
                  {s.pct >= 12 ? `${s.name} ${s.pct}%` : ""}
                </div>
              ))}
            </div>
            <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground">
              {run.steps.map((s, i) => (
                <span key={s.name} className="inline-flex items-center gap-1">
                  <span className={cn("size-2 rounded-sm", barColors[i % barColors.length])} />
                  {s.name} {s.durSec}s
                  {s.cached && <span className="text-sky-600">(캐시)</span>}
                </span>
              ))}
            </div>
            {!!run.cachedSteps && (
              <p className="text-[11px] text-sky-700">
                {run.steps.length}개 단계 중 {run.cachedSteps}개가 <b>캐시 재사용</b>입니다 —
                입력이 지난번과 같아 다시 계산하지 않고 이전 결과를 그대로 썼습니다.
                따라서 위 시간은 <b>실제 학습 시간이 아니라 준비·확인에 걸린 시간</b>입니다.
              </p>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export default function ModelInsightsPage() {
  return (
    <div className="space-y-4">
      {/* 실측 GPU 라이브 */}
      <LiveGpuStrip />
      <Tabs defaultValue="inference">
        <TabsList>
          <TabsTrigger value="inference">추론 병목 (서비스 응답)</TabsTrigger>
          <TabsTrigger value="pipeline">파이프라인 병목 (학습→배포)</TabsTrigger>
        </TabsList>

        {/* ── Inference ── */}
        <TabsContent value="inference" className="mt-4 space-y-4">
          <LiveServingMetrics />
          <InferenceLlmSection />
        </TabsContent>

        {/* ── Pipeline ── */}
        <TabsContent value="pipeline" className="mt-4 space-y-4">
          <LivePipelineBottleneck />
          <LiveRunHistory />
          <FeatureGate id="ai.model-insights.aiops">
            <LivePipelineInsight />
          </FeatureGate>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function Evidence({
  label,
  value,
  detail,
  color,
}: {
  label: string;
  value: string;
  detail: string;
  color: "rose" | "amber" | "indigo";
}) {
  const map = {
    rose: "border-rose-200 bg-rose-50/60",
    amber: "border-amber-200 bg-amber-50/60",
    indigo: "border-brand-indigo-200 bg-brand-indigo-50/60",
  };
  return (
    <div className={cn("rounded-lg border p-3", map[color])}>
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="mt-0.5 text-sm font-semibold">{value}</div>
      <div className="font-mono text-[11px] text-muted-foreground">
        {detail}
      </div>
    </div>
  );
}
