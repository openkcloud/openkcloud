"use client";
import * as React from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  Container as ContainerIcon,
  Boxes,
  ChevronLeft,
  ArrowRight,
  Cpu,
  Activity,
  RefreshCw,
  Settings,
  Scale,
  Server,
} from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  LineChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Progress } from "@/components/ui/progress";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { HealthRing } from "@/components/viz/health-ring";
import { Sparkline, makeSeries } from "@/components/viz/sparkline";
import { FlameBlock } from "@/components/viz/flame-block";
import { cn } from "@/lib/utils";

export default function WorkloadDetailPage() {
  const params = useParams<{ id: string }>();
  const { data, isLoading } = api.infra.workloadDetail(params.id) as { data: any; isLoading: boolean };
  if (isLoading || !data) return <Skeleton className="h-96" />;
  const w = data.workload;

  const series = Array.from({ length: 60 }, (_, i) => ({
    t: `${(60 - i)}m`,
    cpu: 40 + Math.sin(i / 4) * 18,
    mem: 60 + Math.cos(i / 5) * 8,
    p95: 120 + Math.sin(i / 6) * 40 + Math.random() * 8,
    rps: 80 + Math.sin(i / 3) * 30 + Math.random() * 6,
  })).reverse();

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-2">
          <Link href="/paas/workloads" className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            <ChevronLeft className="size-3" /> 워크로드 목록
          </Link>
          <div className="flex items-center gap-3">
            <div className="flex size-11 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
              <ContainerIcon className="size-6" />
            </div>
            <div>
              <h1 className="text-xl font-bold leading-tight">{w.name}</h1>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-mono">{w.namespace} · {w.kind}</span>
                <span>·</span>
                <span className="font-mono">{w.image}</span>
              </div>
            </div>
            <Badge variant={w.status === "Running" ? "success" : "warning"} className="ml-2 gap-1">
              <span className="size-1.5 rounded-full bg-current opacity-80" />
              {w.status}
            </Badge>
            <Badge variant="ghost" className="font-mono text-[10px]">L5</Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm">
            <Scale className="size-3.5" /> 스케일
          </Button>
          <Button variant="outline" size="sm">
            <Settings className="size-3.5" /> 매니페스트 편집
          </Button>
          <Button variant="outline" size="sm">
            <RefreshCw className="size-3.5" /> 롤아웃 재시작
          </Button>
        </div>
      </div>

      {/* Hierarchy breadcrumb */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 p-4 text-xs">
          <Link href={`/paas/clusters`} className="group flex items-center gap-1.5 rounded-md border border-amber-200 bg-amber-50 px-2 py-1 hover:border-primary/40">
            <Boxes className="size-3 text-amber-700" />
            <span className="font-mono">L4 · 서비스 클러스터</span>
            <ArrowRight className="size-3" />
          </Link>
          <span className="text-muted-foreground">›</span>
          <div className="flex items-center gap-1.5 rounded-md border border-brand-indigo-200 bg-brand-indigo-50 px-2 py-1">
            <ContainerIcon className="size-3 text-brand-indigo-700" />
            <span className="font-mono">L5 · {w.name}</span>
          </div>
          {w.accelerators > 0 && (
            <>
              <span className="text-muted-foreground">› allocated_to ›</span>
              <div className="flex items-center gap-1.5 rounded-md border border-brand-mint-200 bg-brand-mint-50 px-2 py-1">
                <Cpu className="size-3 text-brand-mint-700" />
                <span className="font-mono">LA · {w.accelerators}장 가속기</span>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {/* KPI */}
      <Card>
        <CardContent className="grid grid-cols-2 gap-6 p-5 md:grid-cols-4">
          <HealthBlock label="레플리카" value={(w.ready / w.replicas) * 100} sub={`${w.ready}/${w.replicas} ready`} />
          <HealthBlock label="CPU" value={62} sub={`요청 ${w.cpu}`} />
          <HealthBlock label="메모리" value={71} sub={`요청 ${w.mem}`} />
          <HealthBlock label="가속기" value={w.accelerators > 0 ? 78 : 0} sub={`${w.accelerators}장 할당`} />
        </CardContent>
      </Card>

      {/* Time series */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>리소스 사용량 (60분)</CardTitle>
            <CardDescription>CPU · 메모리 사용률</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{ cpu: { label: "CPU %", color: "var(--brand-indigo-500)" }, mem: { label: "MEM %", color: "var(--brand-mint-500)" } }}
              className="h-[200px] w-full"
            >
              <AreaChart data={series}>
                <defs>
                  <linearGradient id="cpug" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--brand-indigo-500)" stopOpacity={0.35} />
                    <stop offset="100%" stopColor="var(--brand-indigo-500)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${v}%`} />
                <Tooltip content={<ChartTooltipContent indicator="line" />} />
                <Area type="monotone" dataKey="cpu" stroke="var(--brand-indigo-500)" strokeWidth={2} fill="url(#cpug)" />
                <Line type="monotone" dataKey="mem" stroke="var(--brand-mint-500)" strokeWidth={2} dot={false} />
              </AreaChart>
            </ChartContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>처리량·지연시간</CardTitle>
            <CardDescription>RPS · P95 ms</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{ rps: { label: "RPS", color: "var(--brand-mint-500)" }, p95: { label: "P95 ms", color: "#f59e0b" } }}
              className="h-[200px] w-full"
            >
              <LineChart data={series}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis yAxisId="left" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${v}ms`} />
                <Tooltip content={<ChartTooltipContent indicator="line" />} />
                <Line yAxisId="left" type="monotone" dataKey="rps" stroke="var(--brand-mint-500)" strokeWidth={2} dot={false} />
                <Line yAxisId="right" type="monotone" dataKey="p95" stroke="#f59e0b" strokeWidth={2} dot={false} />
              </LineChart>
            </ChartContainer>
          </CardContent>
        </Card>
      </div>

      <Tabs defaultValue="pods">
        <TabsList>
          <TabsTrigger value="pods">파드 ({w.replicas})</TabsTrigger>
          <TabsTrigger value="containers">컨테이너 명세</TabsTrigger>
          <TabsTrigger value="logs">로그</TabsTrigger>
          <TabsTrigger value="events">이벤트</TabsTrigger>
        </TabsList>

        <TabsContent value="pods">
          <Card>
            <CardContent className="space-y-2 p-4">
              {Array.from({ length: w.replicas }).map((_, i) => {
                const ready = i < w.ready;
                return (
                  <div key={i} className="flex items-center gap-3 rounded-lg border border-border p-3">
                    <div className={cn("flex size-8 items-center justify-center rounded-md", ready ? "bg-brand-mint-50 text-brand-mint-700" : "bg-amber-100 text-amber-700")}>
                      <ContainerIcon className="size-4" />
                    </div>
                    <div className="flex-1">
                      <div className="font-mono text-sm">{w.name}-{Math.random().toString(36).slice(2, 8)}</div>
                      <div className="font-mono text-[10px] text-muted-foreground">
                        node: gpu-node-0{(i % 2) + 1} · {w.cpu} cpu · {w.mem} mem
                      </div>
                    </div>
                    <Sparkline data={makeSeries(i + 1, 14, 60, 12)} width={64} height={18} color="var(--brand-indigo-500)" />
                    <Badge variant={ready ? "success" : "warning"}>{ready ? "Ready" : "Pending"}</Badge>
                  </div>
                );
              })}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="containers">
          <Card>
            <CardContent className="p-5 space-y-3">
              <Pair label="이미지" value={w.image} mono />
              <Pair label="CPU 요청" value={w.cpu} mono />
              <Pair label="메모리 요청" value={w.mem} mono />
              <Pair label="가속기 요청" value={`${w.accelerators}장 ${w.accelerators > 0 ? "(rebellions.ai/atom)" : ""}`} mono />
              {Object.entries(w.labels ?? {}).map(([k, v]) => (
                <Pair key={k} label={`라벨: ${k}`} value={String(v)} mono />
              ))}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="logs">
          <Card className="overflow-hidden bg-slate-950 text-slate-100">
            <div className="divide-y divide-slate-800 font-mono text-[11px]">
              {[
                { ts: "14:32:18.412", lvl: "INFO", msg: `${w.name} startup complete, listening on :8080` },
                { ts: "14:32:17.118", lvl: "INFO", msg: "Loaded model from /models/v1.2 (4.2GB)" },
                { ts: "14:32:16.812", lvl: "INFO", msg: "RBLN runtime initialized, devices=[atom-1]" },
                { ts: "14:32:16.412", lvl: "INFO", msg: "K8s healthz: /livez OK /readyz OK" },
                { ts: "14:32:15.118", lvl: "DEBUG", msg: "Configmap mounted: /etc/config" },
              ].map((l, i) => (
                <div key={i} className="grid grid-cols-[120px_60px_1fr] gap-3 px-4 py-1.5">
                  <span className="text-slate-500">{l.ts}</span>
                  <span className="rounded bg-sky-500/20 px-1 text-center text-[9px] font-semibold uppercase text-sky-300">{l.lvl}</span>
                  <span>{l.msg}</span>
                </div>
              ))}
            </div>
          </Card>
        </TabsContent>

        <TabsContent value="events">
          <Card>
            <CardContent className="p-0">
              <div className="divide-y divide-border">
                {[
                  { ts: "14:22", reason: "ScalingReplicaSet", msg: `Scaled up replica set ${w.name} to ${w.replicas}` },
                  { ts: "14:18", reason: "Pulled", msg: `Image ${w.image} pulled` },
                  { ts: "14:17", reason: "Scheduled", msg: "Pod assigned to gpu-node-02" },
                  { ts: "14:14", reason: "Created", msg: "Created container" },
                ].map((e, i) => (
                  <div key={i} className="flex items-center gap-3 px-5 py-2 font-mono text-xs">
                    <span className="text-muted-foreground">{e.ts}</span>
                    <Badge variant="ghost" className="text-[10px]">{e.reason}</Badge>
                    <span className="text-muted-foreground">{e.msg}</span>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function HealthBlock({ label, value, sub }: { label: string; value: number; sub: string }) {
  return (
    <div className="flex flex-col items-center gap-2">
      <HealthRing value={Math.min(100, value)} size={86} thickness={7} label={label} />
      <div className="text-center font-mono text-[10px] text-muted-foreground">{sub}</div>
    </div>
  );
}

function Pair({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={cn("text-right text-sm", mono && "font-mono")}>{value}</span>
    </div>
  );
}
