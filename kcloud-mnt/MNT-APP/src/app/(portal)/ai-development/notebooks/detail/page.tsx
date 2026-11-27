"use client";
import { toast } from "sonner";
import * as React from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  Code2,
  Cpu,
  HardDrive,
  ChevronLeft,
  Square,
  RefreshCw,
  Play,
  FileCode,
  Folder,
  TerminalSquare,
  Activity,
  Clock,
} from "lucide-react";
import { ainbFetch, useNotebooks } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Area,
  AreaChart,
  CartesianGrid,
  XAxis,
  YAxis,
  Tooltip,
} from "recharts";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { relativeTime } from "@/lib/utils";
import type { KFNotebook } from "@/lib/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notebookAction } from "@/lib/notebook-api";

interface WsFile { name: string; size: string; modified: string }

/** 실측 파일 목록 — 숨김 파일 제외 */
function useWorkspaceFiles(id: string) {
  return useQuery({
    queryKey: ["nb-files", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/files`);
      if (!res.ok) throw new Error(`${res.status}`);
      const json = (await res.json()) as { items: WsFile[] };
      return json.items.filter((f) => !f.name.startsWith("."));
    },
    enabled: !!id,
    refetchInterval: 15000,
  });
}

/** 실측 리소스 샘플러 — 3초마다 CPU(mCore)·MEM(MiB) 수집, 최근 30개 유지 */
function useLiveStats(id: string) {
  const [samples, setSamples] = React.useState<{ t: string; cpu: number; mem: number }[]>([]);
  const { data, dataUpdatedAt } = useQuery({
    queryKey: ["nb-stats", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/stats`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ cpuMilli: number; memMi: number }>;
    },
    enabled: !!id,
    refetchInterval: 3000,
  });
  // 새 실측이 오면 쌓는다 — 직전 값을 기억해 두고 바뀔 때만 (effect 대신 렌더 중 갱신)
  const [seen, setSeen] = React.useState<typeof data>();
  if (data !== seen) {
    setSeen(data);
    if (data) {
      const t = new Date(dataUpdatedAt).toLocaleTimeString("ko-KR", { hour12: false, minute: "2-digit", second: "2-digit" });
      setSamples((prev) => [...prev.slice(-29), { t, cpu: data.cpuMilli, mem: data.memMi }]);
    }
  }
  return samples;
}

// 노트북 이름은 실행 중에 생기므로 정적 export 의 동적 경로 대신 쿼리 파라미터로 받는다 (/ai-development/notebooks/detail/?id=)
export default function Page() {
  return <React.Suspense fallback={<Skeleton className="h-96" />}><NotebookDetailPage /></React.Suspense>;
}

function NotebookDetailPage() {
  const id = useSearchParams().get("id") ?? "";
  const { data, isLoading } = useNotebooks() as {
    data: { items: KFNotebook[] } | undefined;
    isLoading: boolean;
  };
  const { canSee, loading: ctxLoading } = useProjectContext();
  const liveSeries = useLiveStats(id);
  const { data: filesData } = useWorkspaceFiles(id);
  const qc = useQueryClient();

  // Pod 이벤트 (10초) · 환경 변수 (30초) 실측
  const { data: eventsData } = useQuery({
    queryKey: ["nb-events", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/events`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{
        items: { time: string; reason: string; message: string; type: string; count: number }[];
      }>;
    },
    enabled: !!id,
    refetchInterval: 10000,
  });

  const { data: envData } = useQuery({
    queryKey: ["nb-env", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/env`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{
        items: { name: string; value: string; source: string }[];
        pod: Record<string, string>;
      }>;
    },
    enabled: !!id,
    refetchInterval: 30000,
  });

  async function act(action: "start" | "stop" | "restart") {
    try {
      await notebookAction(id, action);
      toast.success(`${action === "start" ? "시작" : action === "stop" ? "정지" : "재시작"} 요청 완료`);
      qc.invalidateQueries({ queryKey: ["kf", "notebooks"] });
    } catch (e) {
      toast.error(`요청 실패: ${(e as Error).message}`);
    }
  }

  if (isLoading || !data || ctxLoading) return <Skeleton className="h-96" />;
  // 내 워크스페이스(관리자는 전체)의 노트북만 연다
  const nb = data.items.find((n) => n.id === id && canSee(n.namespace));
  if (!nb) return <div>찾을 수 없습니다.</div>;

  const series = liveSeries.map((s) => ({ t: s.t, gpu: s.cpu, mem: s.mem }));
  const files = filesData ?? [];

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-2">
          <Link
            href="/ai-development/notebooks"
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          >
            <ChevronLeft className="size-3" /> 노트북 목록
          </Link>
          <div className="flex items-center gap-3">
            <div className="flex size-10 items-center justify-center rounded-lg bg-brand-mint-50 text-brand-mint-700">
              <Code2 className="size-5" />
            </div>
            <div>
              <h1 className="text-xl font-bold leading-tight">{nb.name}</h1>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-mono">{nb.namespace}</span>
                <span>·</span>
                <span>마지막 활동 {relativeTime(nb.lastActivity)}</span>
              </div>
            </div>
            <Badge variant="success" className="ml-2 gap-1">
              <span className="size-1.5 animate-pulse rounded-full bg-brand-mint-500" />
              {nb.status}
            </Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {/* 정지된 노트북은 재시작(파드 재생성)으로 켜지지 않는다 — 시작(정지 해제)을 보여 준다 */}
          {nb.status === "Stopped" ? (
            <Button variant="outline" size="sm" onClick={() => act("start")}>
              <Play className="size-3.5" /> 시작
            </Button>
          ) : (
            <>
              <Button variant="outline" size="sm" onClick={() => act("restart")}>
                <RefreshCw className="size-3.5" /> 재시작
              </Button>
              <Button variant="outline" size="sm" onClick={() => act("stop")}>
                <Square className="size-3.5" /> 정지
              </Button>
            </>
          )}
          <Button size="sm" asChild>
            <a href={`/notebooks/ide/?id=${encodeURIComponent(nb.id)}`} target="_blank" rel="noopener noreferrer">
              <TerminalSquare className="size-3.5" /> 개발환경 열기
            </a>
          </Button>
        </div>
      </div>

      {/* Resource panel */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>리소스 사용량 <Badge variant="success" className="ml-1 text-[10px]">실측</Badge></CardTitle>
            <CardDescription>metrics-server 실측 — CPU(mCore)·메모리(MiB), 3초 주기</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                gpu: { label: "CPU (mCore)", color: "var(--brand-mint-500)" },
                mem: { label: "MEM (MiB)", color: "var(--brand-indigo-500)" },
              }}
              className="h-[200px] w-full"
            >
              <AreaChart data={series}>
                <defs>
                  <linearGradient id="g1" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--brand-mint-500)" stopOpacity={0.45} />
                    <stop offset="100%" stopColor="var(--brand-mint-500)" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="g2" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--brand-indigo-500)" stopOpacity={0.3} />
                    <stop offset="100%" stopColor="var(--brand-indigo-500)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <Tooltip content={<ChartTooltipContent indicator="line" />} />
                <Area type="monotone" dataKey="gpu" stroke="var(--brand-mint-500)" strokeWidth={2} fill="url(#g1)" />
                <Area type="monotone" dataKey="mem" stroke="var(--brand-indigo-500)" strokeWidth={2} fill="url(#g2)" />
              </AreaChart>
            </ChartContainer>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>스펙</CardTitle>
            <CardDescription>K8s Pod 명세</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Pair label="이미지" value={<span className="font-mono text-[11px]">{nb.image}</span>} />
            <Pair label="vCPU" value={`${nb.cpu} cores`} />
            <Pair label="메모리" value={`${nb.memGb} GiB`} />
            {nb.acceleratorCount > 0 && (
              <Pair
                label="가속기"
                value={
                  <span className="inline-flex items-center gap-1">
                    <Cpu className="size-3" />
                    {nb.acceleratorKind} × {nb.acceleratorCount}
                  </span>
                }
              />
            )}
            <Pair label="볼륨" value={`${nb.storageGb} GiB · PVC`} />
            <Pair label="네임스페이스" value={<span className="font-mono">{nb.namespace}</span>} />
            <Pair label="생성" value={nb.createdAt} />
          </CardContent>
        </Card>
      </div>

      {/* Workspace + Logs */}
      <Tabs defaultValue="workspace">
        <TabsList>
          <TabsTrigger value="workspace">
            <Folder className="size-3.5" /> 워크스페이스
          </TabsTrigger>
          <TabsTrigger value="events">
            <Activity className="size-3.5" /> Pod 이벤트
          </TabsTrigger>
          <TabsTrigger value="env">
            <FileCode className="size-3.5" /> 환경 변수
          </TabsTrigger>
        </TabsList>

        <TabsContent value="workspace">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <HardDrive className="size-4" />
                /home/jovyan
              </CardTitle>
              <CardDescription>작업공간 실측 파일 목록 (15초 갱신)</CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <div className="divide-y divide-border">
                {files.length === 0 && (
                  <div className="px-5 py-6 text-center text-xs text-muted-foreground">
                    작업공간이 비어있습니다
                  </div>
                )}
                {files.map((f) => (
                  <div key={f.name} className="flex items-center gap-3 px-5 py-2.5 hover:bg-secondary/30">
                    {f.name.endsWith("/") ? (
                      <Folder className="size-4 text-brand-indigo-600" />
                    ) : (
                      <FileCode className="size-4 text-muted-foreground" />
                    )}
                    <span className="flex-1 font-mono text-sm">{f.name}</span>
                    <span className="font-mono text-xs text-muted-foreground tabular-nums">{f.size}</span>
                    <span className="text-xs text-muted-foreground">
                      <Clock className="mr-1 inline size-3" />
                      {relativeTime(f.modified)}
                    </span>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="events">
          <Card>
            <CardContent className="p-0">
              <div className="divide-y divide-border">
                {(eventsData?.items ?? []).length === 0 && (
                  <div className="px-5 py-6 text-center text-xs text-muted-foreground">
                    최근 이벤트가 없습니다 — 정상 운영 중입니다
                    <div className="mt-1 text-[10px]">
                      (쿠버네티스는 이벤트를 약 1시간만 보관합니다)
                    </div>
                  </div>
                )}
                {(eventsData?.items ?? []).map((e, i) => (
                  <div key={i} className="flex items-start gap-3 px-5 py-2 text-xs">
                    <span className="shrink-0 font-mono text-muted-foreground tabular-nums">{e.time}</span>
                    <Badge variant={e.type === "Warning" ? "warning" : "ghost"} className="shrink-0 text-[10px]">
                      {e.type}
                    </Badge>
                    <span className="shrink-0 font-medium">{e.reason}</span>
                    <span className="text-muted-foreground">{e.message}</span>
                    {e.count > 1 && (
                      <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">×{e.count}</span>
                    )}
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="env">
          <Card>
            <CardContent className="p-0">
              {envData?.pod && (
                <div className="grid grid-cols-2 gap-x-6 gap-y-1 border-b border-border bg-secondary/30 px-5 py-3 text-xs md:grid-cols-3">
                  <Pair label="Pod" value={<span className="font-mono text-[11px]">{envData.pod.podName}</span>} />
                  <Pair label="노드" value={<span className="font-mono text-[11px]">{envData.pod.node}</span>} />
                  <Pair label="Pod IP" value={<span className="font-mono text-[11px]">{envData.pod.podIP}</span>} />
                  <Pair label="런타임" value={<span className="font-mono text-[11px]">{envData.pod.runtimeClass}</span>} />
                  <Pair label="ServiceAccount" value={<span className="font-mono text-[11px]">{envData.pod.serviceAccount}</span>} />
                  <Pair label="시작 시각" value={<span className="font-mono text-[11px]">{envData.pod.startedAt}</span>} />
                </div>
              )}
              <div className="divide-y divide-border">
                {(envData?.items ?? []).length === 0 && (
                  <div className="px-5 py-6 text-center text-xs text-muted-foreground">
                    설정된 환경 변수가 없습니다
                  </div>
                )}
                {(envData?.items ?? []).map((e) => (
                  <div key={e.name} className="flex items-center gap-4 px-5 py-2 font-mono text-xs">
                    <span className="w-56 shrink-0 font-medium">{e.name}</span>
                    <span className="flex-1 break-all text-muted-foreground">{e.value}</span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">{e.source}</span>
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

function Pair({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="text-right">{value}</span>
    </div>
  );
}
