"use client";
import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Server, Cpu, Zap, Activity, Search, Layers } from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Sparkline, makeSeries } from "@/components/viz/sparkline";
import { HealthRing } from "@/components/viz/health-ring";
import { cn, formatNumber, formatWatts } from "@/lib/utils";

type Server = {
  id: string;
  hostname: string;
  poolId: string;
  zoneId: string;
  status: string;
  vcpu: number;
  ramGb: number;
  diskTb: number;
  acceleratorIds: string[];
  acceleratorCount: number;
  acceleratorKinds: string[];
  vmCount: number;
  runningVms: number;
  ipAddress: string;
  powerW: number;
  uptimeDays: number;
  cpuUtil: number;
  memUtil: number;
  accelUtil: number;
};

export default function ServersPage() {
  const router = useRouter();
  const { data, isLoading } = api.infra.servers() as {
    data: { observed_at: string; items: Server[] } | undefined;
    isLoading: boolean;
  };
  const [q, setQ] = React.useState("");

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.items.filter((s) => !q || s.hostname.toLowerCase().includes(q.toLowerCase()));

  const totals = {
    servers: data.items.length,
    accelerators: data.items.reduce((s, x) => s + x.acceleratorCount, 0),
    vms: data.items.reduce((s, x) => s + x.vmCount, 0),
    powerKw: Math.round(data.items.reduce((s, x) => s + x.powerW, 0) / 100) / 10,
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="인프라 관리"
        description={`Layer 0/L1 물리 인프라 · ${totals.servers}대 · ${totals.accelerators}장 가속기 · ${totals.vms} VM · ${totals.powerKw}kW`}
        actions={
          <Button variant="outline" size="sm">
            <Activity className="size-3.5" /> Discovery 트리거
          </Button>
        }
      />

      {/* Filter band */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input
              placeholder="호스트명 검색…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              className="w-56 pl-9"
            />
          </div>
          <span className="ml-auto text-xs text-muted-foreground">
            {items.length}/{data.items.length}대 · observed_at <span className="font-mono">{data.observed_at}</span>
          </span>
        </div>
      </Card>

      {/* Server cards grid — Datadog Host Map 영감 */}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {items.map((s) => (
          <ServerCard key={s.id} server={s} onClick={() => router.push(`/infrastructure/servers/${s.id}`)} />
        ))}
      </div>
    </div>
  );
}

function ServerCard({ server: s, onClick }: { server: Server; onClick: () => void }) {
  const overallHealth = Math.max(s.cpuUtil, s.memUtil, s.accelUtil);
  const healthColor = overallHealth > 85 ? "border-rose-200" : overallHealth > 65 ? "border-amber-200" : "border-brand-mint-200";

  return (
    <button onClick={onClick} className="text-left">
      <Card className={cn("group relative overflow-hidden transition-all hover:-translate-y-0.5 hover:shadow-lg", healthColor)}>
        <div
          className="absolute right-0 top-0 h-full w-1"
          style={{
            background:
              overallHealth > 85 ? "linear-gradient(180deg, #e11d48 0%, transparent 100%)" :
                overallHealth > 65 ? "linear-gradient(180deg, #f59e0b 0%, transparent 100%)" :
                  "linear-gradient(180deg, #00c896 0%, transparent 100%)",
          }}
        />
        <CardContent className="space-y-4 p-5">
          {/* Header */}
          <div className="flex items-start justify-between">
            <div className="flex items-start gap-2.5">
              <div className="flex size-10 items-center justify-center rounded-lg bg-slate-100 text-slate-700">
                <Server className="size-5" />
              </div>
              <div>
                <div className="font-semibold leading-tight">{s.hostname}</div>
                <div className="font-mono text-[11px] text-muted-foreground">
                  {s.ipAddress} · uptime {s.uptimeDays}d
                </div>
              </div>
            </div>
            <Badge variant={s.status === "Up" ? "success" : "warning"} className="gap-1">
              <span className="size-1.5 rounded-full bg-current opacity-80" />
              {s.status}
            </Badge>
          </div>

          {/* Health rings — Dynatrace 영감 */}
          <div className="flex items-center justify-around rounded-lg bg-secondary/30 py-3">
            <HealthRing value={s.cpuUtil} size={64} thickness={6} label="CPU" />
            <HealthRing value={s.memUtil} size={64} thickness={6} label="MEM" />
            {s.acceleratorCount > 0 && <HealthRing value={s.accelUtil} size={64} thickness={6} label={s.acceleratorKinds[0]} />}
          </div>

          {/* Sparklines row */}
          <div className="grid grid-cols-3 gap-2 text-[10px]">
            <SparkCell label="CPU" data={makeSeries(s.id.length, 20, s.cpuUtil, 8)} value={`${s.cpuUtil}%`} color="var(--brand-indigo-500)" />
            <SparkCell label="MEM" data={makeSeries(s.id.length + 1, 20, s.memUtil, 6)} value={`${s.memUtil}%`} color="var(--brand-mint-500)" />
            <SparkCell label="POWER" data={makeSeries(s.id.length + 2, 20, s.powerW, 80)} value={formatWatts(s.powerW)} color="#f59e0b" />
          </div>

          {/* Allocations */}
          <div className="grid grid-cols-2 gap-2 border-t border-border pt-3 text-xs">
            <div className="flex items-center gap-1.5">
              <Cpu className="size-3.5 text-brand-mint-600" />
              <span>
                <strong className="font-mono">{s.acceleratorCount}</strong>장 가속기
                {s.acceleratorKinds.length > 0 && (
                  <span className="ml-1 text-muted-foreground">({s.acceleratorKinds.join(", ")})</span>
                )}
              </span>
            </div>
            <div className="flex items-center gap-1.5">
              <Layers className="size-3.5 text-rose-600" />
              <span>
                <strong className="font-mono">{s.runningVms}/{s.vmCount}</strong> VM 실행
              </span>
            </div>
          </div>

          <div className="flex items-center justify-between text-[11px] text-muted-foreground">
            <span className="font-mono">{s.id}</span>
            <span className="opacity-0 transition-opacity group-hover:opacity-100">
              상세 →
            </span>
          </div>
        </CardContent>
      </Card>
    </button>
  );
}

function SparkCell({ label, data, value, color }: { label: string; data: number[]; value: string; color: string }) {
  return (
    <div className="rounded-md border border-border bg-card/60 p-1.5">
      <div className="flex items-center justify-between">
        <span className="text-[9px] uppercase text-muted-foreground">{label}</span>
        <span className="font-mono text-[10px] font-semibold tabular-nums">{value}</span>
      </div>
      <Sparkline data={data} width={84} height={16} color={color} />
    </div>
  );
}
