"use client";
import * as React from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Bell,
  BrainCircuit,
  Boxes,
  Cpu,
  GitBranch,
  Layers,
  ScrollText,
  Server,
  Sparkles,
  Zap,
} from "lucide-react";
import Link from "next/link";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "@/lib/api";
import { useAuth, roleLabel } from "@/lib/auth";
import { formatNumber, formatPercent, relativeTime } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Separator } from "@/components/ui/separator";

type SummaryResponse = {
  kpis: {
    vmCount: number;
    runningVmCount: number;
    nodeCount: number;
    clusterCount: number;
    podCount: number;
    tenantCount: number;
    acceleratorTotal: number;
    acceleratorActive: number;
    cpuPct: number;
    ramPct: number;
    diskPct: number;
    avgAccUtil: number;
    powerKw: number;
    estimatedCostMonth: number;
    powerSavedPct: number;
  };
  tenantsByUtil: { name: string; vcpuPct: number; ramPct: number; accPct: number }[];
  acceleratorBreakdown: { kind: string; total: number; allocated: number; avgUtil: number }[];
  powerSeries: { time: string; kw: number; cost: number }[];
  utilSeries: { day: string; gpu: number; npu: number; pim: number }[];
};

type ActivitiesResponse = { items: { id: string; ts: string; actor: string; action: string; target: string; result: string }[] };
type AlertsResponse = {
  items: { id: string; severity: "critical" | "warning" | "info"; ts: string; source: string; message: string; acknowledged: boolean }[];
  counts: { critical: number; warning: number; info: number };
};

export default function DashboardPage() {
  const user = useAuth((s) => s.user);
  const summary = api.dashboard.summary() as { data: SummaryResponse | undefined; isLoading: boolean };
  const activities = api.dashboard.activities() as { data: ActivitiesResponse | undefined };
  const alerts = api.dashboard.alerts() as { data: AlertsResponse | undefined };

  if (summary.isLoading || !summary.data) {
    return <DashboardSkeleton />;
  }
  const k = summary.data.kpis;

  return (
    <div className="space-y-6">
      <PageHeader
        title={`안녕하세요, ${user?.displayName}님 👋`}
        description={`${roleLabel(user?.role ?? "admin")} · ${user?.org} · openKcloud 통합관리 콘솔`}
        actions={
          <div className="flex items-center gap-2">
            <Badge variant="success" className="gap-1.5">
              <span className="size-1.5 rounded-full bg-brand-mint-500" />
              플랫폼 정상
            </Badge>
            <Button variant="outline" size="sm">
              운영 리포트 내보내기
            </Button>
          </div>
        }
      />

      {/* KPI Grid */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">
        <StatCard
          label="가상서버"
          value={formatNumber(k.vmCount)}
          unit="대"
          icon={Server}
          accent="indigo"
          footer={
            <span>
              실행 중 <strong className="text-foreground">{k.runningVmCount}</strong>대
            </span>
          }
        />
        <StatCard
          label="K8s 클러스터"
          value={formatNumber(k.clusterCount)}
          unit="개"
          icon={Boxes}
          accent="violet"
          footer={
            <span>
              파드 <strong className="text-foreground">{formatNumber(k.podCount)}</strong>개
            </span>
          }
        />
        <StatCard
          label="AI반도체"
          value={formatNumber(k.acceleratorTotal)}
          unit="장"
          icon={Cpu}
          accent="mint"
          footer={
            <span>
              할당 <strong className="text-foreground">{k.acceleratorActive}</strong> · 평균 가동률 <strong className="text-foreground">{k.avgAccUtil}%</strong>
            </span>
          }
        />
        <StatCard
          label="물리 노드"
          value={formatNumber(k.nodeCount)}
          unit="대"
          icon={Layers}
          accent="sky"
          footer={
            <span>
              테넌트 <strong className="text-foreground">{k.tenantCount}</strong>개
            </span>
          }
        />
        <StatCard
          label="실시간 전력"
          value={k.powerKw.toFixed(1)}
          unit="kW"
          icon={Zap}
          accent="amber"
          delta={{ value: k.powerSavedPct, trend: "down", label: "vs.목표", positive: "down" }}
        />
        <StatCard
          label="월 운영비용 (예상)"
          value={`₩${formatNumber(k.estimatedCostMonth / 10000)}`}
          unit="만원"
          icon={Activity}
          accent="rose"
          footer={<span>최적화 정책 적용 중</span>}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        {/* Power chart */}
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-row items-center justify-between">
            <div>
              <CardTitle>플랫폼 전력 사용량 (24시간)</CardTitle>
              <CardDescription>
                실시간 모니터링 — 모든 노드 및 AI반도체 합산 (kcloud-opt 옵저버빌리티)
              </CardDescription>
            </div>
            <Badge variant="ghost">실시간</Badge>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                kw: { label: "전력 (kW)", color: "var(--brand-indigo-500)" },
                cost: { label: "비용 (원)", color: "var(--brand-mint-500)" },
              }}
              className="h-[260px] w-full"
            >
              <AreaChart data={summary.data.powerSeries}>
                <defs>
                  <linearGradient id="powerGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--brand-indigo-500)" stopOpacity={0.4} />
                    <stop offset="100%" stopColor="var(--brand-indigo-500)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="time" tickLine={false} axisLine={false} tick={{ fontSize: 11 }} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tick={{ fontSize: 11 }}
                  tickFormatter={(v) => `${v} kW`}
                />
                <Tooltip content={<ChartTooltipContent indicator="line" />} />
                <Area
                  type="monotone"
                  dataKey="kw"
                  stroke="var(--brand-indigo-500)"
                  strokeWidth={2}
                  fill="url(#powerGrad)"
                />
              </AreaChart>
            </ChartContainer>
          </CardContent>
        </Card>

        {/* Alerts */}
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>활성 알람</CardTitle>
              <CardDescription>
                심각 {alerts.data?.counts.critical ?? 0} · 경고 {alerts.data?.counts.warning ?? 0} · 정보 {alerts.data?.counts.info ?? 0}
              </CardDescription>
            </div>
            <Link href="/monitoring/alerts">
              <Button variant="ghost" size="sm">
                전체 보기 <ArrowRight className="size-3" />
              </Button>
            </Link>
          </CardHeader>
          <CardContent className="space-y-2">
            {alerts.data?.items.slice(0, 5).map((a) => (
              <div
                key={a.id}
                className="flex items-start gap-3 rounded-lg border border-border p-3"
              >
                <AlertIcon severity={a.severity} />
                <div className="flex-1 space-y-0.5">
                  <div className="text-sm font-medium leading-tight">{a.message}</div>
                  <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <span className="font-mono">{a.source}</span>
                    <span>·</span>
                    <span>{relativeTime(a.ts)}</span>
                  </div>
                </div>
              </div>
            ))}
            {!alerts.data?.items.length && (
              <div className="py-8 text-center text-sm text-muted-foreground">
                활성 알람이 없습니다.
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* AI Accelerator utilization */}
        <Card>
          <CardHeader>
            <CardTitle>AI반도체 가동률 추이 (7일)</CardTitle>
            <CardDescription>GPU · NPU · PIM 평균 가동률</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                gpu: { label: "GPU", color: "var(--brand-indigo-500)" },
                npu: { label: "NPU", color: "var(--brand-mint-500)" },
                pim: { label: "PIM", color: "var(--chart-3)" },
              }}
              className="h-[220px] w-full"
            >
              <LineChart data={summary.data.utilSeries}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="day" tickLine={false} axisLine={false} tick={{ fontSize: 11 }} />
                <YAxis tickLine={false} axisLine={false} tick={{ fontSize: 11 }} domain={[0, 100]} tickFormatter={(v) => `${v}%`} />
                <Tooltip content={<ChartTooltipContent indicator="dot" />} />
                <Legend iconType="circle" iconSize={8} wrapperStyle={{ fontSize: 11 }} />
                <Line type="monotone" dataKey="gpu" stroke="var(--brand-indigo-500)" strokeWidth={2} dot={{ r: 3 }} />
                <Line type="monotone" dataKey="npu" stroke="var(--brand-mint-500)" strokeWidth={2} dot={{ r: 3 }} />
                <Line type="monotone" dataKey="pim" stroke="var(--chart-3)" strokeWidth={2} dot={{ r: 3 }} />
              </LineChart>
            </ChartContainer>
            <Separator className="my-4" />
            <div className="grid grid-cols-3 gap-3">
              {summary.data.acceleratorBreakdown.map((b) => (
                <div key={b.kind} className="rounded-lg border border-border p-3">
                  <div className="text-[11px] font-medium uppercase text-muted-foreground">{b.kind}</div>
                  <div className="mt-1 text-xl font-bold tabular-nums">
                    {b.allocated}<span className="text-sm text-muted-foreground"> / {b.total}</span>
                  </div>
                  <div className="mt-1 text-[11px] text-muted-foreground">평균 가동 {b.avgUtil}%</div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        {/* Tenant utilization */}
        <Card>
          <CardHeader>
            <CardTitle>테넌트별 자원 사용률</CardTitle>
            <CardDescription>쿼터 대비 vCPU · RAM · 가속기 사용 비율</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {summary.data.tenantsByUtil.map((t) => (
              <div key={t.name} className="space-y-2">
                <div className="flex items-center justify-between text-sm">
                  <span className="font-medium font-mono">{t.name}</span>
                  <span className="text-muted-foreground">
                    vCPU {t.vcpuPct}% · RAM {t.ramPct}% · 가속기 {t.accPct}%
                  </span>
                </div>
                <div className="grid grid-cols-3 gap-1.5">
                  <Progress value={t.vcpuPct} indicatorClassName="bg-brand-indigo-500" />
                  <Progress value={t.ramPct} indicatorClassName="bg-brand-mint-500" />
                  <Progress value={t.accPct} indicatorClassName="bg-amber-500" />
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        {/* Resource utilization summary */}
        <Card className="lg:col-span-1">
          <CardHeader>
            <CardTitle>플랫폼 자원 종합</CardTitle>
            <CardDescription>전체 풀(센터·존·자원풀) 기준</CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <ResourceRow label="vCPU" pct={k.cpuPct} accent="indigo" />
            <ResourceRow label="메모리" pct={k.ramPct} accent="mint" />
            <ResourceRow label="디스크" pct={k.diskPct} accent="amber" />
            <ResourceRow label="AI반도체" pct={k.avgAccUtil} accent="violet" />
          </CardContent>
        </Card>

        {/* Activity feed */}
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>최근 활동</CardTitle>
              <CardDescription>플랫폼 전역 운영 이벤트 (감사 로그)</CardDescription>
            </div>
            <Link href="/iam/audit">
              <Button variant="ghost" size="sm">
                감사 로그 전체 <ArrowRight className="size-3" />
              </Button>
            </Link>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {activities.data?.items.map((a) => (
                <div key={a.id} className="flex items-start gap-3 border-b border-border pb-3 last:border-0">
                  <Avatar className="size-8">
                    <AvatarFallback className="bg-brand-indigo-100 text-[11px] font-medium text-brand-indigo-700">
                      {a.actor.startsWith("system") ? "S" : a.actor.slice(0, 1)}
                    </AvatarFallback>
                  </Avatar>
                  <div className="flex-1 space-y-0.5">
                    <div className="flex items-baseline gap-1.5 text-sm">
                      <span className="font-medium">{a.actor}</span>
                      <span className="text-muted-foreground">·</span>
                      <span>{a.action}</span>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      <span className="font-mono">{a.target}</span>
                      <span className="ml-2">{relativeTime(a.ts)}</span>
                    </div>
                  </div>
                  <Badge variant={a.result === "success" ? "success" : a.result === "warning" ? "warning" : "destructive"}>
                    {a.result === "success" ? "성공" : a.result === "warning" ? "경고" : "실패"}
                  </Badge>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Observability quick links — ETRI 260615 §3.i.1 (개요 → 옵저버빌리티 연계) */}
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>옵저버빌리티 바로가기</CardTitle>
            <CardDescription>모니터링·트레이스·로그·알람으로 즉시 이동</CardDescription>
          </div>
          <Link href="/monitoring">
            <Button variant="ghost" size="sm">
              전체 옵저버빌리티 <ArrowRight className="size-3" />
            </Button>
          </Link>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-5">
            <QuickLink href="/monitoring" icon={Activity} label="인프라 모니터링" desc="CPU·MEM·전력" accent="indigo" />
            <QuickLink href="/monitoring/insights" icon={Sparkles} label="AI 모델 분석" desc="추론 성능·권장조치" accent="mint" />
            <QuickLink href="/monitoring/traces" icon={GitBranch} label="분산 트레이스" desc="서비스 호출 흐름" accent="violet" />
            <QuickLink href="/monitoring/logs" icon={ScrollText} label="로그·이벤트" desc="플랫폼 이벤트" accent="amber" />
            <QuickLink href="/monitoring/alerts" icon={Bell} label="알람·SLO" desc={`활성 ${(alerts.data?.counts.critical ?? 0) + (alerts.data?.counts.warning ?? 0)}건`} accent="rose" />
          </div>
        </CardContent>
      </Card>

      {/* Quick links — 주요 자원 */}
      <Card>
        <CardHeader>
          <CardTitle>주요 자원 바로가기</CardTitle>
          <CardDescription>자주 접근하는 자원 관리 페이지</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
            <QuickLink href="/iaas/instances" icon={Server} label="가상서버 관리" desc="OpenStack VM" accent="indigo" />
            <QuickLink href="/paas/workloads" icon={Boxes} label="K8s 워크로드" desc="Deployment · Pod" accent="violet" />
            <QuickLink href="/ai-development/notebooks" icon={BrainCircuit} label="AI 개발환경" desc="Kubeflow Notebook" accent="amber" />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function AlertIcon({ severity }: { severity: "critical" | "warning" | "info" }) {
  const map = {
    critical: "bg-red-100 text-red-700",
    warning: "bg-amber-100 text-amber-700",
    info: "bg-sky-100 text-sky-700",
  };
  return (
    <div className={`flex size-7 shrink-0 items-center justify-center rounded-md ${map[severity]}`}>
      <AlertTriangle className="size-3.5" />
    </div>
  );
}

function ResourceRow({
  label,
  pct,
  accent,
}: {
  label: string;
  pct: number;
  accent: "indigo" | "mint" | "amber" | "violet";
}) {
  const colorMap = {
    indigo: "bg-brand-indigo-500",
    mint: "bg-brand-mint-500",
    amber: "bg-amber-500",
    violet: "bg-violet-500",
  };
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between text-sm">
        <span className="font-medium">{label}</span>
        <span className="font-mono text-foreground tabular-nums">{pct}%</span>
      </div>
      <Progress value={pct} indicatorClassName={colorMap[accent]} />
    </div>
  );
}

function QuickLink({
  href,
  icon: Icon,
  label,
  desc,
  accent,
}: {
  href: string;
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  desc: string;
  accent: "indigo" | "mint" | "amber" | "violet" | "rose";
}) {
  const iconMap = {
    indigo: "bg-brand-indigo-100 text-brand-indigo-700",
    mint: "bg-brand-mint-50 text-brand-mint-700",
    amber: "bg-amber-50 text-amber-700",
    violet: "bg-violet-50 text-violet-700",
    rose: "bg-rose-50 text-rose-700",
  };
  return (
    <Link
      href={href}
      className="group flex items-center gap-3 rounded-xl border border-border p-4 transition-colors hover:border-primary/40 hover:bg-secondary/40"
    >
      <div className={`flex size-10 items-center justify-center rounded-lg ${iconMap[accent]}`}>
        <Icon className="size-5" />
      </div>
      <div className="flex-1">
        <div className="text-sm font-medium">{label}</div>
        <div className="text-xs text-muted-foreground">{desc}</div>
      </div>
      <ArrowRight className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
    </Link>
  );
}

function DashboardSkeleton() {
  return (
    <div className="space-y-6">
      <Skeleton className="h-16 w-full" />
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-32" />
        ))}
      </div>
      <Skeleton className="h-80 w-full" />
    </div>
  );
}
