"use client";
import { ChangeMark } from "@/components/change-mark";
import * as React from "react";
import {
  Sparkles,
  AlertTriangle,
  TrendingDown,
  TrendingUp,
  Cpu,
  Database,
  Zap,
  ChevronRight,
  Lightbulb,
  Activity,
} from "lucide-react";
import { FeatureGate } from "@/components/feature-gate";
import { useFeatureVisible } from "@/lib/scope";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
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
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";

/* Dynatrace style:
   - Davis-style root-cause card with bullet evidence
   - Service flow / dependency graph
   - Latency breakdown (token gen waterfall)
   - Hot spots ranking
*/

const SERVICES = [
  { name: "vllm-solar-10b", health: "degraded", latency: 980, rps: 124, error: 0.012 },
  { name: "stt-rngd-prod", health: "healthy", latency: 420, rps: 56, error: 0.001 },
  { name: "stt-atom-edge", health: "healthy", latency: 220, rps: 88, error: 0.002 },
  { name: "kserve-furiosa-rngd", health: "healthy", latency: 380, rps: 32, error: 0.0 },
];

// Token generation latency breakdown (model: vllm-solar-10b)
const LATENCY_BREAKDOWN = [
  { stage: "Tokenize", ms: 12, pct: 1, color: "var(--brand-indigo-300)" },
  { stage: "Embedding", ms: 28, pct: 2, color: "var(--brand-indigo-400)" },
  { stage: "Attention (Layer 0-15)", ms: 184, pct: 19, color: "var(--brand-indigo-500)" },
  { stage: "Attention (Layer 16-31)", ms: 322, pct: 33, color: "#e11d48" },
  { stage: "FFN", ms: 218, pct: 22, color: "var(--brand-indigo-600)" },
  { stage: "Sampling", ms: 84, pct: 9, color: "var(--brand-mint-500)" },
  { stage: "Detokenize", ms: 32, pct: 3, color: "var(--brand-mint-400)" },
  { stage: "기타 (overhead)", ms: 100, pct: 10, color: "#94a3b8" },
];

const LATENCY_TREND = Array.from({ length: 60 }, (_, i) => ({
  t: `${(60 - i)}m`,
  p50: 220 + Math.sin(i / 5) * 30 + Math.random() * 20,
  p95: 620 + Math.sin(i / 4) * 80 + Math.random() * 40,
  p99: 820 + Math.sin(i / 6) * 120 + (i > 45 ? 200 : 0) + Math.random() * 60,
})).reverse();

const HOTSPOTS = [
  { component: "Attention Layer 22 (GQA)", hostMs: 84, gpu: "acc-a100-1-3", reason: "KV cache 미스율 41%" },
  { component: "Attention Layer 28 (GQA)", hostMs: 76, gpu: "acc-a100-1-3", reason: "KV cache 미스율 38%" },
  { component: "FFN Layer 24 (SwiGLU)", hostMs: 58, gpu: "acc-a100-1-3", reason: "memory bandwidth 89%" },
  { component: "Tokenizer (BPE)", hostMs: 12, gpu: "CPU", reason: "single-thread bottleneck" },
];

export default function AIInsightsPage() {
  const aiopsVisible = useFeatureVisible("monitoring.insights.aiops");
  return (
    <div className="space-y-4">
      <ChangeMark ids={["CR-OBS-06", "CR-OBS-05", "CR-OBS-09"]}>
<div className="rounded-md border border-violet-200 bg-violet-50/60 px-3 py-2 text-[11px] text-violet-900">
        <strong>범위</strong> — AI 모델 분석은 단순 모니터링이 아닌 AIOps 성격의 기능이라 <strong>2차년도 시범서비스에서는 메뉴 전체를 제외</strong>한다.
        2차년도 옵저버빌리티는 지표·로그·규칙 기반 이상감지까지이며, 이 화면은 최종 형상 설계안으로만 둔다(환경설정 › 포털 형상에서 전환). SLO는 이번 과제에서 제외해 목표 지연(1초) 기준으로 표기한다.
      </div>
</ChangeMark>

      {/* Davis-style root cause alert — 최종 형상(AIOps) */}
      <FeatureGate id="monitoring.insights.aiops">
      <Card className="overflow-hidden border-rose-200 bg-gradient-to-br from-rose-50 to-transparent">
        <CardContent className="p-5">
          <div className="flex items-start gap-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-full bg-rose-100 text-rose-700">
              <Sparkles className="size-5" />
            </div>
            <div className="flex-1 space-y-3">
              <div>
                <div className="flex items-center gap-2">
                  <Badge variant="destructive">Critical</Badge>
                  <Badge variant="outline" className="font-mono text-[10px]">AI Davis Insight</Badge>
                  <span className="text-xs text-muted-foreground">12분 전 자동 감지</span>
                </div>
                <h2 className="mt-1 text-lg font-bold leading-tight">
                  vllm-solar-10b 추론 지연 P99이 목표 지연 1초를 18% 초과한다.
                </h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  Davis가 트레이스 1,240건과 호스트 메트릭 시리즈 32종을 상관 분석한 결과,
                  <strong className="text-foreground"> Attention Layer 22/28에서 KV cache 미스가 폭증</strong>한 것이 주 원인입니다.
                </p>
              </div>

              <div className="grid gap-2 md:grid-cols-3">
                <Evidence
                  label="근본 원인"
                  value="KV Cache 미스율"
                  detail="38-41% (정상: 8%)"
                  color="rose"
                />
                <Evidence
                  label="영향받는 요청"
                  value="2,184 req / 1h"
                  detail="전체 18.4%"
                  color="amber"
                />
                <Evidence
                  label="서비스 영향"
                  value="목표 지연 초과"
                  detail="P99 < 1s 미달"
                  color="rose"
                />
              </div>

              <div className="space-y-2 rounded-lg border border-rose-200 bg-card p-3">
                <div className="flex items-center gap-1.5 text-xs font-medium">
                  <Lightbulb className="size-3.5 text-amber-600" />
                  권장 조치
                </div>
                <ul className="space-y-1 text-xs text-muted-foreground">
                  <li className="flex items-start gap-1.5">
                    <ChevronRight className="size-3 mt-0.5 shrink-0 text-brand-indigo-500" />
                    KV cache 크기를 <strong className="text-foreground">32GB → 48GB</strong>로 확장 (acc-a100-1-3 유휴 메모리 16GB 활용)
                  </li>
                  <li className="flex items-start gap-1.5">
                    <ChevronRight className="size-3 mt-0.5 shrink-0 text-brand-indigo-500" />
                    vLLM <strong className="text-foreground">PagedAttention</strong> block size를 16 → 32로 조정
                  </li>
                  <li className="flex items-start gap-1.5">
                    <ChevronRight className="size-3 mt-0.5 shrink-0 text-brand-indigo-500" />
                    부하 분산: 동일 모델을 <strong className="text-foreground">Rebellions REBEL Quad</strong>로 한 레플리카 이전 (전성비 +22%)
                  </li>
                </ul>
              </div>

              <div className="flex items-center gap-2">
                <Button variant="outline" size="sm">조치 상세 보기</Button>
                <Button variant="outline" size="sm">트레이스 보기</Button>
                <Button variant="ghost" size="sm">유사 이슈 검색</Button>
                <span className="ml-auto text-[10px] text-muted-foreground">
                  자동 적용은 차차년도 과업 — 현재는 정보 제공까지
                </span>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
      </FeatureGate>

      {/* Service Flow */}
      <Card>
        <CardHeader>
          <CardTitle>서비스 플로우 — Token Generation</CardTitle>
          <CardDescription>
            요청이 통과하는 컴포넌트 — 폭 = 호출 빈도, 색 = 지연 ms
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ServiceFlow />
        </CardContent>
      </Card>

      {/* Latency breakdown + trend */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>응답 시간 트렌드</CardTitle>
            <CardDescription>지난 60분 · P50 / P95 / P99 · 목표 지연 1초 기준선</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                p50: { label: "P50", color: "var(--brand-mint-500)" },
                p95: { label: "P95", color: "var(--brand-indigo-500)" },
                p99: { label: "P99", color: "#e11d48" },
              }}
              className="h-[240px] w-full"
            >
              <LineChart data={LATENCY_TREND}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${v}ms`} />
                <RTooltip content={<ChartTooltipContent indicator="line" />} />
                <ReferenceLine y={1000} stroke="#e11d48" strokeDasharray="4 4" label={{ value: "목표 1s", fill: "#e11d48", fontSize: 10, position: "right" }} />
                <Line type="monotone" dataKey="p50" stroke="var(--brand-mint-500)" strokeWidth={1.5} dot={false} />
                <Line type="monotone" dataKey="p95" stroke="var(--brand-indigo-500)" strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="p99" stroke="#e11d48" strokeWidth={2} dot={false} />
              </LineChart>
            </ChartContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>지연 분해 (1 token 평균)</CardTitle>
            <CardDescription>vllm-solar-10b · 980ms 중 단계별 기여</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {LATENCY_BREAKDOWN.map((s) => (
                <div key={s.stage} className="space-y-1">
                  <div className="flex items-center justify-between text-xs">
                    <span className="flex items-center gap-1.5">
                      <span className="size-1.5 rounded-full" style={{ background: s.color }} />
                      {s.stage}
                    </span>
                    <span className="font-mono tabular-nums">
                      <strong>{s.ms}ms</strong>
                      <span className="ml-1 text-muted-foreground">({s.pct}%)</span>
                    </span>
                  </div>
                  <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-secondary">
                    <div
                      className="h-full rounded-full"
                      style={{ width: `${s.pct}%`, background: s.color }}
                    />
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Hot spots + Services */}
      <div className={cn("grid gap-4", aiopsVisible && "lg:grid-cols-3")}>
        <FeatureGate id="monitoring.insights.aiops" fallback="none">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>핫스팟 — Attention/FFN 부하 분석</CardTitle>
            <CardDescription>
              모델 그래프 노드별 GPU 시간 — Dynatrace Method Hotspots 스타일
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {HOTSPOTS.map((h, i) => (
                <div key={i} className="flex items-center gap-3 rounded-lg border border-border p-3">
                  <Badge variant="ghost" className="font-mono">#{i + 1}</Badge>
                  <div className="flex-1">
                    <div className="text-sm font-medium">{h.component}</div>
                    <div className="font-mono text-[11px] text-muted-foreground">
                      {h.gpu} · {h.reason}
                    </div>
                  </div>
                  <div className="w-32">
                    <div className="text-right font-mono text-sm font-semibold tabular-nums">{h.hostMs} ms</div>
                    <Progress
                      value={(h.hostMs / 84) * 100}
                      indicatorClassName={i < 2 ? "bg-rose-500" : i < 3 ? "bg-amber-500" : "bg-brand-mint-500"}
                    />
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
        </FeatureGate>

        <Card>
          <CardHeader>
            <CardTitle>서비스 헬스</CardTitle>
            <CardDescription>추론 엔드포인트 자동 분류</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {SERVICES.map((s) => (
              <div key={s.name} className="space-y-1.5 rounded-lg border border-border p-2.5">
                <div className="flex items-center justify-between">
                  <span className="font-mono text-xs font-medium">{s.name}</span>
                  <Badge
                    variant={s.health === "degraded" ? "destructive" : "success"}
                    className="text-[10px]"
                  >
                    {s.health}
                  </Badge>
                </div>
                <div className="grid grid-cols-3 gap-1 text-[10px] text-muted-foreground">
                  <span>P99 <strong className="ml-0.5 font-mono text-foreground">{s.latency}ms</strong></span>
                  <span>RPS <strong className="ml-0.5 font-mono text-foreground">{s.rps}</strong></span>
                  <span>Err <strong className={cn("ml-0.5 font-mono", s.error > 0.01 ? "text-rose-600" : "text-foreground")}>{(s.error * 100).toFixed(2)}%</strong></span>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
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
      <div className="font-mono text-[11px] text-muted-foreground">{detail}</div>
    </div>
  );
}

function ServiceFlow() {
  const nodes = [
    { id: "client", label: "Client", x: 40, y: 60, color: "fill-slate-300" },
    { id: "gw", label: "API Gateway", x: 200, y: 60, color: "fill-brand-indigo-300" },
    { id: "auth", label: "OAuth/OPA", x: 340, y: 20, color: "fill-brand-indigo-300", small: true },
    { id: "router", label: "vLLM Router", x: 380, y: 100, color: "fill-brand-indigo-400" },
    { id: "solar", label: "Solar 10B", x: 580, y: 60, color: "fill-rose-500" },
    { id: "rngd", label: "STT RNGD", x: 580, y: 140, color: "fill-brand-mint-400" },
    { id: "atom", label: "STT ATOM", x: 580, y: 200, color: "fill-brand-mint-400" },
    { id: "obs", label: "Observability", x: 800, y: 130, color: "fill-violet-300", small: true },
  ];
  const edges = [
    { from: "client", to: "gw", thick: 4, lat: 8 },
    { from: "gw", to: "auth", thick: 2, lat: 4 },
    { from: "gw", to: "router", thick: 4, lat: 12 },
    { from: "router", to: "solar", thick: 4, lat: 980 },
    { from: "router", to: "rngd", thick: 2, lat: 420 },
    { from: "router", to: "atom", thick: 3, lat: 220 },
    { from: "solar", to: "obs", thick: 1, lat: 2 },
    { from: "rngd", to: "obs", thick: 1, lat: 2 },
    { from: "atom", to: "obs", thick: 1, lat: 2 },
  ];

  const nodeMap = Object.fromEntries(nodes.map((n) => [n.id, n]));
  const edgeColor = (lat: number) => (lat > 500 ? "#e11d48" : lat > 200 ? "#f59e0b" : "#3a55d9");

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox="0 0 880 240" className="h-[260px] w-full min-w-[800px]">
        {edges.map((e, i) => {
          const a = nodeMap[e.from];
          const b = nodeMap[e.to];
          return (
            <g key={i}>
              <line
                x1={a.x + 40}
                y1={a.y}
                x2={b.x - 40}
                y2={b.y}
                stroke={edgeColor(e.lat)}
                strokeWidth={e.thick}
                strokeOpacity={0.7}
              />
              <text
                x={(a.x + b.x) / 2}
                y={(a.y + b.y) / 2 - 6}
                fontSize="9"
                fill={edgeColor(e.lat)}
                fontFamily="monospace"
                textAnchor="middle"
              >
                {e.lat}ms
              </text>
            </g>
          );
        })}
        {nodes.map((n) => (
          <g key={n.id}>
            <rect
              x={n.x - 40}
              y={n.y - (n.small ? 12 : 18)}
              width={80}
              height={n.small ? 24 : 36}
              rx={6}
              className={n.color}
              stroke="white"
              strokeWidth={1}
            />
            <text
              x={n.x}
              y={n.y + 4}
              fontSize={n.small ? "10" : "11"}
              fontWeight="600"
              fill="white"
              textAnchor="middle"
            >
              {n.label}
            </text>
          </g>
        ))}
      </svg>
    </div>
  );
}
