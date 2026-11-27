"use client";
import * as React from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  Server,
  Cpu,
  Zap,
  ChevronLeft,
  Power,
  RefreshCw,
  Settings,
  Thermometer,
  HardDrive,
  Layers,
  Activity,
} from "lucide-react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ChartContainer, ChartTooltipContent } from "@/components/ui/chart";
import { HealthRing } from "@/components/viz/health-ring";
import { Sparkline, makeSeries } from "@/components/viz/sparkline";
import { FlameBlock } from "@/components/viz/flame-block";
import { cn, formatWatts } from "@/lib/utils";

type ServerDetail = {
  observed_at: string;
  node: any;
  accelerators: any[];
  vms: any[];
  stats: {
    cpu: { total: number; used: number; util: number };
    memory: { total: number; used: number; util: number };
    disk: { total: number; used: number; util: number };
    power: { watts: number; breakdown: { cpu: number; dram: number; gpu: number; other: number } };
  };
};

export default function ServerDetailPage() {
  const params = useParams<{ id: string }>();
  const { data, isLoading } = api.infra.server(params.id) as { data: ServerDetail | undefined; isLoading: boolean };
  if (isLoading || !data) return <Skeleton className="h-96" />;

  const cpuSeries = Array.from({ length: 60 }, (_, i) => ({
    t: `${(60 - i)}m`,
    cpu: 30 + Math.sin(i / 4) * 18 + Math.random() * 5,
    mem: data.stats.memory.util - 8 + Math.sin(i / 5) * 6 + Math.random() * 3,
    power: data.stats.power.watts - 80 + Math.sin(i / 3) * 60 + Math.random() * 20,
  })).reverse();

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-2">
          <Link href="/infrastructure/servers" className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            <ChevronLeft className="size-3" /> 물리 서버 목록
          </Link>
          <div className="flex items-center gap-3">
            <div className="flex size-11 items-center justify-center rounded-lg bg-slate-100 text-slate-700">
              <Server className="size-6" />
            </div>
            <div>
              <h1 className="text-xl font-bold leading-tight">{data.node.hostname}</h1>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-mono">{data.node.id} · {data.node.ipAddress}</span>
                <span>·</span>
                <span>uptime {data.node.uptimeDays}일</span>
              </div>
            </div>
            <Badge variant="success" className="ml-2 gap-1">
              <span className="size-1.5 rounded-full bg-brand-mint-500" />
              {data.node.status}
            </Badge>
            <Badge variant="ghost" className="font-mono text-[10px]">L0 / L1</Badge>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm">
            <Settings className="size-3.5" /> 설정
          </Button>
          <Button variant="outline" size="sm">
            <RefreshCw className="size-3.5" /> 재시작
          </Button>
          <Button variant="destructive" size="sm">
            <Power className="size-3.5" /> 종료
          </Button>
        </div>
      </div>

      {/* Health rings — Dynatrace style */}
      <Card>
        <CardContent className="grid grid-cols-2 gap-6 p-5 md:grid-cols-4">
          <HealthRingBlock label="CPU" value={data.stats.cpu.util} sub={`${data.stats.cpu.used}/${data.stats.cpu.total} cores`} />
          <HealthRingBlock label="메모리" value={data.stats.memory.util} sub={`${data.stats.memory.used}/${data.stats.memory.total} GB`} />
          <HealthRingBlock label="디스크" value={data.stats.disk.util} sub={`${data.stats.disk.used}/${data.stats.disk.total} TB`} />
          <HealthRingBlock label="가속기" value={Math.round(data.accelerators.reduce((s, a) => s + a.utilization, 0) / Math.max(1, data.accelerators.length))} sub={`${data.accelerators.length}장 부착`} />
        </CardContent>
      </Card>

      {/* Time-series — Datadog query value style */}
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>실시간 메트릭 (60분)</CardTitle>
            <CardDescription>CPU · 메모리 · 전력 추이</CardDescription>
          </CardHeader>
          <CardContent>
            <ChartContainer
              config={{
                cpu: { label: "CPU %", color: "var(--brand-indigo-500)" },
                mem: { label: "MEM %", color: "var(--brand-mint-500)" },
                power: { label: "Power W", color: "#f59e0b" },
              }}
              className="h-[260px] w-full"
            >
              <LineChart data={cpuSeries}>
                <CartesianGrid strokeDasharray="3 3" vertical={false} />
                <XAxis dataKey="t" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
                <YAxis yAxisId="left" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${Math.round(v)}%`} />
                <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 10 }} axisLine={false} tickLine={false} tickFormatter={(v) => `${Math.round(v)}W`} />
                <Tooltip content={<ChartTooltipContent indicator="line" />} />
                <Line yAxisId="left" type="monotone" dataKey="cpu" stroke="var(--brand-indigo-500)" strokeWidth={2} dot={false} />
                <Line yAxisId="left" type="monotone" dataKey="mem" stroke="var(--brand-mint-500)" strokeWidth={2} dot={false} />
                <Line yAxisId="right" type="monotone" dataKey="power" stroke="#f59e0b" strokeWidth={2} dot={false} />
              </LineChart>
            </ChartContainer>
          </CardContent>
        </Card>

        {/* Power breakdown - Datadog Flame block */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              전력 분해
              <Badge variant="ghost" className="font-mono text-[10px]">Kepler</Badge>
            </CardTitle>
            <CardDescription>{formatWatts(data.stats.power.watts)} 총량 · 컴포넌트별</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <FlameBlock
              segments={[
                { label: "GPU", value: data.stats.power.breakdown.gpu, color: "var(--brand-indigo-500)" },
                { label: "CPU", value: data.stats.power.breakdown.cpu, color: "var(--brand-mint-500)" },
                { label: "DRAM", value: data.stats.power.breakdown.dram, color: "#f59e0b" },
                { label: "기타", value: data.stats.power.breakdown.other, color: "#94a3b8" },
              ]}
              height={32}
            />
            <div className="space-y-2 text-xs">
              {Object.entries(data.stats.power.breakdown).map(([k, v]) => (
                <div key={k} className="flex items-center justify-between">
                  <span className="text-muted-foreground capitalize">{k}</span>
                  <span className="font-mono tabular-nums">{v}W ({Math.round((v / data.stats.power.watts) * 100)}%)</span>
                </div>
              ))}
            </div>
            <div className="rounded-md border border-amber-200 bg-amber-50/60 p-2.5 text-[11px] text-amber-900">
              💡 IPMI 측정(PSU 입력): ~{Math.round(data.stats.power.watts * 1.12)}W. 차이 ~{Math.round(data.stats.power.watts * 0.12)}W는 PSU 변환 손실.
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Tabs: 가속기 / VM / 하드웨어 센서 */}
      <Tabs defaultValue="accelerators">
        <TabsList>
          <TabsTrigger value="accelerators">
            <Cpu className="size-3.5" /> 가속기 ({data.accelerators.length})
          </TabsTrigger>
          <TabsTrigger value="vms">
            <Layers className="size-3.5" /> VM ({data.vms.length})
          </TabsTrigger>
          <TabsTrigger value="sensors">
            <Thermometer className="size-3.5" /> 하드웨어 센서
          </TabsTrigger>
        </TabsList>

        <TabsContent value="accelerators">
          <Card>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>가속기</TableHead>
                  <TableHead>모델</TableHead>
                  <TableHead>PCI</TableHead>
                  <TableHead>활용률</TableHead>
                  <TableHead>온도</TableHead>
                  <TableHead>전력</TableHead>
                  <TableHead>상태</TableHead>
                  <TableHead>패스스루</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.accelerators.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Cpu className={cn(
                          "size-4",
                          a.kind === "GPU" ? "text-brand-indigo-600" :
                            a.kind === "NPU" ? "text-brand-mint-600" : "text-violet-600",
                        )} />
                        <div>
                          <div className="font-mono text-xs font-medium">{a.id}</div>
                          <div className="font-mono text-[10px] text-muted-foreground">{a.kind} · {a.vendor}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="text-xs">{a.name}</TableCell>
                    <TableCell className="font-mono text-[10px]">{a.pciAddress ?? "—"}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Sparkline data={makeSeries(a.id.length, 12, a.utilization, 12)} width={48} height={14} color={a.utilization > 80 ? "#e11d48" : "var(--brand-mint-500)"} />
                        <span className="font-mono text-xs tabular-nums">{a.utilization}%</span>
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{a.temperatureC}°C</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{a.powerW}W</TableCell>
                    <TableCell>
                      <Badge variant={a.status === "Allocated" ? "default" : "secondary"}>
                        {a.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-[10px] text-muted-foreground">
                      {a.passthroughTo?.vmId ?? "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
        </TabsContent>

        <TabsContent value="vms">
          <Card>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>VM</TableHead>
                  <TableHead>프로젝트</TableHead>
                  <TableHead>스펙</TableHead>
                  <TableHead>GPU PT</TableHead>
                  <TableHead>서비스 클러스터</TableHead>
                  <TableHead>상태</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.vms.map((v) => (
                  <TableRow key={v.id} className="cursor-pointer" onClick={() => window.location.assign(`/iaas/instances/${v.id}`)}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <div className="size-6 rounded-md bg-rose-100 p-1 text-rose-700"><Server className="size-4" /></div>
                        <div>
                          <div className="font-medium">{v.name}</div>
                          <div className="font-mono text-[10px] text-muted-foreground">{v.id}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{v.openstackProject ?? v.tenantId}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{v.vcpu}vCPU · {v.ramGb}GB</TableCell>
                    <TableCell>
                      {v.gpuPassthroughCount > 0 ? (
                        <Badge variant="success" className="gap-1">
                          <Cpu className="size-3" /> {v.gpuPassthroughCount}
                        </Badge>
                      ) : "—"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{v.serviceClusterId ?? "—"}</TableCell>
                    <TableCell>
                      <Badge variant={v.status === "Running" ? "success" : "secondary"}>{v.status}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
        </TabsContent>

        <TabsContent value="sensors">
          <Card>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>센서</TableHead>
                    <TableHead>현재값</TableHead>
                    <TableHead>경고 임계</TableHead>
                    <TableHead>심각 임계</TableHead>
                    <TableHead>상태</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {[
                    { name: "CPU1 Temp", value: 62, warn: 85, crit: 95 },
                    { name: "CPU2 Temp", value: 58, warn: 85, crit: 95 },
                    { name: "Inlet Temp", value: 24, warn: 42, crit: 47 },
                    { name: "Outlet Temp", value: 38, warn: 70, crit: 75 },
                    { name: "PSU1 Power", value: 295, warn: 600, crit: 700 },
                    { name: "PSU2 Power", value: 285, warn: 600, crit: 700 },
                  ].map((s) => (
                    <TableRow key={s.name}>
                      <TableCell className="font-mono text-xs">{s.name}</TableCell>
                      <TableCell className="font-mono font-semibold tabular-nums">{s.value}{s.name.includes("Temp") ? "°C" : "W"}</TableCell>
                      <TableCell className="font-mono text-xs text-amber-700">{s.warn}</TableCell>
                      <TableCell className="font-mono text-xs text-rose-700">{s.crit}</TableCell>
                      <TableCell>
                        <Badge variant="success">OK</Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function HealthRingBlock({ label, value, sub }: { label: string; value: number; sub: string }) {
  return (
    <div className="flex flex-col items-center gap-2">
      <HealthRing value={value} size={100} thickness={8} label={label} />
      <div className="text-center font-mono text-[10px] text-muted-foreground">{sub}</div>
    </div>
  );
}
