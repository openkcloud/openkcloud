"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import {
  Server,
  Layers,
  Activity,
  Eye,
} from "lucide-react";
import { PageHeader } from "@/components/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Switch } from "@/components/ui/switch";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Sankey } from "@/components/viz/sankey";
import { Sparkline, makeSeries } from "@/components/viz/sparkline";
import type {
  Accelerator,
  Node,
  ServiceCluster,
  VM,
  VMGpuPassthrough,
  KWorkload,
} from "@/lib/types";

type TopologyResp = {
  observed_at: string;
  nodes: Node[];
  accelerators: Accelerator[];
  vms: VM[];
  workloads: KWorkload[];
  serviceClusters: ServiceCluster[];
  passthroughs: VMGpuPassthrough[];
};

// ────────────────────────────────────────────────
// Technology inventory data (Dynatrace 영감)
// ────────────────────────────────────────────────
type Technology = {
  name: string;
  category: "language" | "web" | "db" | "msg" | "cloud" | "proxy" | "container" | "sdk" | "ai";
  processGroups: number;
  instances: number;
  k8sInstances: number;
  vmInstances: number;
  icon: string; // emoji or short
};

const TECHS: Technology[] = [
  // Languages/Runtimes
  { name: "Python", category: "language", processGroups: 24, instances: 412, k8sInstances: 380, vmInstances: 32, icon: "🐍" },
  { name: "Java", category: "language", processGroups: 18, instances: 286, k8sInstances: 210, vmInstances: 76, icon: "☕" },
  { name: "Go", category: "language", processGroups: 12, instances: 154, k8sInstances: 154, vmInstances: 0, icon: "🐹" },
  { name: "Node.js", category: "language", processGroups: 9, instances: 88, k8sInstances: 88, vmInstances: 0, icon: "⬢" },
  // Web Servers
  { name: "Nginx", category: "web", processGroups: 14, instances: 224, k8sInstances: 208, vmInstances: 16, icon: "N" },
  { name: "Envoy", category: "web", processGroups: 8, instances: 168, k8sInstances: 168, vmInstances: 0, icon: "E" },
  { name: "Apache HTTP Server", category: "web", processGroups: 4, instances: 38, k8sInstances: 28, vmInstances: 10, icon: "A" },
  // Databases
  { name: "PostgreSQL", category: "db", processGroups: 6, instances: 18, k8sInstances: 8, vmInstances: 10, icon: "🐘" },
  { name: "MongoDB", category: "db", processGroups: 3, instances: 12, k8sInstances: 9, vmInstances: 3, icon: "🍃" },
  { name: "Redis", category: "db", processGroups: 7, instances: 42, k8sInstances: 40, vmInstances: 2, icon: "R" },
  { name: "MariaDB", category: "db", processGroups: 2, instances: 8, k8sInstances: 6, vmInstances: 2, icon: "M" },
  { name: "etcd", category: "db", processGroups: 3, instances: 15, k8sInstances: 15, vmInstances: 0, icon: "ε" },
  // Messaging
  { name: "RabbitMQ", category: "msg", processGroups: 2, instances: 6, k8sInstances: 6, vmInstances: 0, icon: "🐰" },
  { name: "Kafka", category: "msg", processGroups: 4, instances: 24, k8sInstances: 24, vmInstances: 0, icon: "K" },
  // Cloud / Container
  { name: "OpenStack", category: "cloud", processGroups: 12, instances: 124, k8sInstances: 0, vmInstances: 124, icon: "O" },
  { name: "KVM/QEMU", category: "cloud", processGroups: 5, instances: 84, k8sInstances: 0, vmInstances: 84, icon: "K" },
  { name: "Docker", category: "container", processGroups: 6, instances: 22, k8sInstances: 22, vmInstances: 0, icon: "🐳" },
  { name: "containerd", category: "container", processGroups: 10, instances: 56, k8sInstances: 56, vmInstances: 0, icon: "C" },
  // Proxy
  { name: "HAProxy", category: "proxy", processGroups: 3, instances: 12, k8sInstances: 8, vmInstances: 4, icon: "H" },
  { name: "Traefik", category: "proxy", processGroups: 2, instances: 8, k8sInstances: 8, vmInstances: 0, icon: "T" },
  // AI / SDK
  { name: "CUDA Runtime", category: "ai", processGroups: 6, instances: 16, k8sInstances: 14, vmInstances: 2, icon: "🟢" },
  { name: "Rebellions RBLN SDK", category: "ai", processGroups: 4, instances: 18, k8sInstances: 16, vmInstances: 2, icon: "R" },
  { name: "Furiosa SDK", category: "ai", processGroups: 3, instances: 6, k8sInstances: 6, vmInstances: 0, icon: "F" },
  { name: "vLLM", category: "ai", processGroups: 5, instances: 14, k8sInstances: 14, vmInstances: 0, icon: "v" },
  { name: "Triton Inference", category: "ai", processGroups: 3, instances: 12, k8sInstances: 12, vmInstances: 0, icon: "T" },
  { name: "PyTorch", category: "ai", processGroups: 8, instances: 92, k8sInstances: 80, vmInstances: 12, icon: "🔥" },
  { name: "TensorFlow", category: "ai", processGroups: 4, instances: 28, k8sInstances: 22, vmInstances: 6, icon: "T" },
  { name: "Hugging Face TGI", category: "ai", processGroups: 3, instances: 10, k8sInstances: 10, vmInstances: 0, icon: "🤗" },
  // SDK
  { name: "OneAgent SDK", category: "sdk", processGroups: 7, instances: 38, k8sInstances: 32, vmInstances: 6, icon: "1" },
];

const CATEGORY_COLOR: Record<Technology["category"], string> = {
  language: "#1d4ed8",
  web: "#0369a1",
  db: "#7e22ce",
  msg: "#b45309",
  cloud: "#0e7490",
  proxy: "#be185d",
  container: "#1e40af",
  sdk: "#374151",
  ai: "#047857",
};

const CATEGORY_LABEL: Record<Technology["category"], string> = {
  language: "런타임",
  web: "웹 서버",
  db: "데이터베이스",
  msg: "메시징",
  cloud: "클라우드 인프라",
  proxy: "프록시",
  container: "컨테이너",
  sdk: "SDK",
  ai: "AI/ML",
};

export default function TopologyPage() {
  const { data, isLoading } = api.infra.topology() as { data: TopologyResp | undefined; isLoading: boolean };
  const [showMetrics, setShowMetrics] = React.useState(true);
  const [tab, setTab] = React.useState<"technology" | "smartscape" | "sankey">("technology");

  if (isLoading || !data) return <Skeleton className="h-[600px]" />;

  return (
    <div className="space-y-4">
      <PageHeader
        title="기술인벤토리 관리"
        description="Technology Overview + Smartscape — 모든 기술 스택과 자원을 한 화면에서"
        actions={
          <div className="flex items-center gap-2">
            <div className="flex items-center gap-2 rounded-md border border-border bg-card px-2 py-1">
              <Eye className="size-3.5 text-muted-foreground" />
              <span className="text-xs">메트릭 오버레이</span>
              <Switch checked={showMetrics} onCheckedChange={setShowMetrics} />
            </div>
          </div>
        }
      />

      <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)}>
        <TabsList>
          <TabsTrigger value="technology">
            <Layers className="size-3.5" /> Technology Overview
          </TabsTrigger>
          <TabsTrigger value="smartscape">
            <Server className="size-3.5" /> Smartscape
          </TabsTrigger>
          <TabsTrigger value="sankey">
            <Activity className="size-3.5" /> 자원 흐름
          </TabsTrigger>
        </TabsList>

        <TabsContent value="technology" className="mt-4">
          <TechnologyOverview />
        </TabsContent>

        <TabsContent value="smartscape" className="mt-4">
          <SmartscapeViz data={data} showMetrics={showMetrics} />
        </TabsContent>

        <TabsContent value="sankey" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle>자원 흐름 (Sankey)</CardTitle>
              <CardDescription>물리서버 → 가속기 → VM → 컨테이너</CardDescription>
            </CardHeader>
            <CardContent>
              <SankeyResourceFlow data={data} />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

// ════════════════════════════════════════════════
// Technology Overview (Dynatrace 영감)
// ════════════════════════════════════════════════
function TechnologyOverview() {
  const [filter, setFilter] = React.useState("");
  const [selected, setSelected] = React.useState<Technology | undefined>(TECHS[0]);

  const filtered = TECHS.filter((t) => !filter || t.name.toLowerCase().includes(filter.toLowerCase()));
  const totalGroups = filtered.reduce((s, t) => s + t.processGroups, 0);
  const totalInstances = filtered.reduce((s, t) => s + t.instances, 0);
  const k8sTotal = filtered.reduce((s, t) => s + t.k8sInstances, 0);
  const vmTotal = filtered.reduce((s, t) => s + t.vmInstances, 0);

  // Treemap-like sizing: 분류
  const grouped = (["language", "web", "db", "ai", "cloud", "container", "msg", "proxy", "sdk"] as Technology["category"][])
    .map((cat) => ({
      cat,
      items: filtered.filter((t) => t.category === cat).sort((a, b) => b.instances - a.instances),
    }))
    .filter((g) => g.items.length > 0);

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Technology overview</CardTitle>
          <CardDescription>
            <strong className="text-foreground">{totalGroups}</strong>개 프로세스 그룹 ·
            <strong className="ml-1 text-foreground">{totalInstances}</strong>개 인스턴스
            (K8s {k8sTotal} · VM {vmTotal})
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Input
            placeholder="Filter technologies — start typing"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="max-w-md"
          />

          {/* Treemap grouped by category */}
          <div className="space-y-2">
            {grouped.map((g) => (
              <div key={g.cat}>
                <div className="mb-1.5 flex items-center gap-2 px-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                  <span className="inline-block size-2 rounded-sm" style={{ background: CATEGORY_COLOR[g.cat] }} />
                  {CATEGORY_LABEL[g.cat]}
                </div>
                <div className="grid auto-rows-[120px] grid-cols-2 gap-1.5 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
                  {g.items.map((t) => (
                    <TechTile
                      key={t.name}
                      tech={t}
                      color={CATEGORY_COLOR[t.category]}
                      onClick={() => setSelected(t)}
                      selected={selected?.name === t.name}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Detail panel */}
      {selected && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <span
                className="inline-flex size-7 items-center justify-center rounded-md font-mono text-xs text-white"
                style={{ background: CATEGORY_COLOR[selected.category] }}
              >
                {selected.icon}
              </span>
              {selected.name}
              <Badge variant="outline" className="font-mono text-[10px]">{CATEGORY_LABEL[selected.category]}</Badge>
            </CardTitle>
            <CardDescription>
              {selected.processGroups}개 프로세스 그룹 · {selected.instances}개 인스턴스
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-3">
              <DetailStat label="K8s 인스턴스" value={selected.k8sInstances} total={selected.instances} color="bg-brand-indigo-500" />
              <DetailStat label="VM 인스턴스" value={selected.vmInstances} total={selected.instances} color="bg-brand-mint-500" />
            </div>
            <div className="mt-3 flex justify-around rounded-lg bg-secondary/30 p-3">
              <Sparkline data={makeSeries(selected.name.length, 24, selected.instances, 5)} width={160} height={32} color={CATEGORY_COLOR[selected.category]} />
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function TechTile({
  tech,
  color,
  onClick,
  selected,
}: {
  tech: Technology;
  color: string;
  onClick: () => void;
  selected: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "group relative overflow-hidden rounded-md p-2.5 text-left text-white shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-md",
        selected && "ring-2 ring-white",
      )}
      style={{ background: color }}
    >
      <div className="flex h-full flex-col justify-between">
        <div>
          <div className="text-[11px] font-bold leading-tight">{tech.name}</div>
          <div className="text-[9px] opacity-80">
            {tech.processGroups} process group{tech.processGroups !== 1 ? "s" : ""}
          </div>
        </div>
        <div className="flex items-end justify-between">
          <span className="font-mono text-base font-bold tabular-nums">{tech.instances}</span>
          <span className="text-[8px] uppercase opacity-80">Instances</span>
        </div>
      </div>
      <div className="absolute right-1.5 top-1.5 font-mono text-[14px] font-bold opacity-25">
        {tech.icon}
      </div>
    </button>
  );
}

function DetailStat({ label, value, total, color }: { label: string; value: number; total: number; color: string }) {
  const pct = Math.round((value / total) * 100);
  return (
    <div className="rounded-lg border border-border p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-2xl font-bold tabular-nums">{value}</span>
        <span className="text-xs text-muted-foreground">/ {total} ({pct}%)</span>
      </div>
      <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-secondary">
        <div className={cn("h-full rounded-full", color)} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

// ════════════════════════════════════════════════
// Smartscape (kept simple)
// ════════════════════════════════════════════════
function SmartscapeViz({ data, showMetrics }: { data: TopologyResp; showMetrics: boolean }) {
  const router = useRouter();
  return (
    <div className="space-y-2">
      <LayerStrip title="물리 서버 (L0)" sublabel={`${data.nodes.length}대`} bgClass="bg-slate-50" borderClass="border-slate-300">
        {data.nodes.map((n) => (
          <SmartscapeNode
            key={n.id}
            label={n.hostname.split(".")[0]}
            badge={`${n.vcpu} vCPU`}
            health="ok"
            metric={`${n.powerW}W`}
            spark={makeSeries(parseInt(n.id.slice(-1) || "1") + 1, 24, n.powerW, 80)}
            color="bg-slate-700"
            showMetrics={showMetrics}
            onClick={() => router.push(`/infrastructure/servers/${n.id}`)}
          />
        ))}
      </LayerStrip>
      <ConnectorBar label={`${data.passthroughs.length} attached / passthrough`} />
      <LayerStrip title="가속기 (LA)" sublabel={`${data.accelerators.length}장`} bgClass="bg-brand-mint-50" borderClass="border-brand-mint-300">
        {data.accelerators.slice(0, 18).map((a) => (
          <SmartscapeNode
            key={a.id}
            label={a.kind}
            badge={a.vendor}
            health={a.utilization > 85 ? "crit" : a.utilization > 60 ? "warn" : "ok"}
            metric={`${a.utilization}%`}
            spark={makeSeries(a.id.length, 24, a.utilization, 18)}
            color={a.kind === "GPU" ? "bg-brand-indigo-600" : a.kind === "NPU" ? "bg-brand-mint-600" : "bg-violet-600"}
            showMetrics={showMetrics}
          />
        ))}
      </LayerStrip>
      <ConnectorBar label={`${data.vms.length} VM 노드`} />
      <LayerStrip title="OpenStack VM (L3) / K8s 노드 (L4)" sublabel={`${data.vms.length}개`} bgClass="bg-rose-50" borderClass="border-rose-300">
        {data.vms.map((v) => (
          <SmartscapeNode
            key={v.id}
            label={v.name.length > 14 ? v.name.slice(0, 12) + "…" : v.name}
            badge={v.clusterRole ?? "worker"}
            health={v.status === "Running" ? "ok" : "warn"}
            metric={`${v.vcpu}vCPU`}
            spark={makeSeries(v.name.length, 24, 50 + v.vcpu, 12)}
            color="bg-rose-500"
            showMetrics={showMetrics}
            onClick={() => router.push(`/iaas/instances/${v.id}`)}
          />
        ))}
      </LayerStrip>
      <ConnectorBar label="컨테이너" />
      <LayerStrip title="컨테이너 (L5)" sublabel="가속기 할당 컨테이너" bgClass="bg-brand-indigo-50" borderClass="border-brand-indigo-300">
        {data.workloads.map((w) => (
          <SmartscapeNode
            key={w.id}
            label={w.name.length > 16 ? w.name.slice(0, 14) + "…" : w.name}
            badge={w.namespace}
            health={w.ready === w.replicas ? "ok" : "warn"}
            metric={`${w.ready}/${w.replicas}`}
            spark={makeSeries(w.name.length + 1, 24, 60, 25)}
            color="bg-brand-indigo-500"
            showMetrics={showMetrics}
            onClick={() => router.push(`/paas/workloads/${w.id}`)}
          />
        ))}
      </LayerStrip>
    </div>
  );
}

function LayerStrip({ title, sublabel, children, bgClass, borderClass }: { title: string; sublabel?: string; children: React.ReactNode; bgClass: string; borderClass: string }) {
  return (
    <div className={cn("rounded-xl border p-3", bgClass, borderClass)}>
      <div className="mb-2 flex items-center gap-2 px-1">
        <div className="text-xs font-bold uppercase tracking-wider">{title}</div>
        {sublabel && <div className="text-[11px] text-muted-foreground">{sublabel}</div>}
      </div>
      <div className="flex flex-wrap gap-2">{children}</div>
    </div>
  );
}

function ConnectorBar({ label }: { label: string }) {
  return (
    <div className="relative flex h-6 items-center justify-center">
      <div className="h-px w-full bg-gradient-to-r from-transparent via-border to-transparent" />
      <span className="absolute rounded-full border border-border bg-card px-2.5 py-0.5 font-mono text-[10px] text-muted-foreground">↓ {label}</span>
    </div>
  );
}

function SmartscapeNode({ label, badge, health, metric, spark, color, showMetrics, onClick }: { label: string; badge: string; health: "ok" | "warn" | "crit"; metric: string; spark: number[]; color: string; showMetrics: boolean; onClick: () => void }) {
  const ringColor = health === "crit" ? "ring-rose-500/40" : health === "warn" ? "ring-amber-500/40" : "ring-brand-mint-500/30";
  const sparkColor = health === "crit" ? "#e11d48" : health === "warn" ? "#f59e0b" : "var(--brand-mint-500)";
  return (
    <button onClick={onClick} className={cn("group relative flex flex-col gap-1 rounded-lg border border-border bg-card p-2.5 text-left text-xs ring-2 transition-all hover:-translate-y-0.5 hover:shadow-md", ringColor)} style={{ minWidth: 124 }}>
      <div className="flex items-center gap-1.5">
        <span className={cn("size-1.5 rounded-full", color)} />
        <span className="font-medium leading-tight">{label}</span>
      </div>
      <div className="flex items-center justify-between text-[10px] text-muted-foreground">
        <span className="font-mono">{badge}</span>
        <span className="font-mono tabular-nums">{metric}</span>
      </div>
      {showMetrics && <Sparkline data={spark} width={108} height={16} color={sparkColor} />}
    </button>
  );
}

// ════════════════════════════════════════════════
// Sankey resource flow
// ════════════════════════════════════════════════
function SankeyResourceFlow({ data }: { data: TopologyResp }) {
  const nodes = [
    ...data.nodes.map((n) => ({ id: n.id, label: n.hostname.split(".")[0], layer: 0, color: "#475569", meta: `${data.accelerators.filter((a) => a.nodeId === n.id).length} acc` })),
    ...data.accelerators.slice(0, 14).map((a) => ({ id: a.id, label: a.kind, layer: 1, color: a.kind === "GPU" ? "var(--brand-indigo-500)" : a.kind === "NPU" ? "var(--brand-mint-500)" : "#a855f7", meta: a.vendor })),
    ...data.vms.slice(0, 10).map((v) => ({ id: v.id, label: v.name.slice(0, 10), layer: 2, color: "#fb7185", meta: v.serviceClusterId ?? v.tenantId })),
    ...data.workloads.slice(0, 6).map((w) => ({ id: w.id, label: w.name.slice(0, 12), layer: 3, color: "var(--brand-indigo-500)", meta: w.namespace })),
  ];

  const links: { source: string; target: string; value: number; color?: string }[] = [];
  data.accelerators.slice(0, 14).forEach((a) => {
    links.push({ source: a.nodeId, target: a.id, value: a.utilization || 1, color: "#475569" });
  });
  data.passthroughs.forEach((pt) => {
    if (data.vms.slice(0, 10).find((v) => v.id === pt.vmId)) {
      links.push({ source: pt.acceleratorId, target: pt.vmId, value: 40, color: "var(--brand-indigo-400)" });
    }
  });
  data.vms.slice(0, 6).forEach((v, i) => {
    const wl = data.workloads[i];
    if (wl) links.push({ source: v.id, target: wl.id, value: 30, color: "var(--brand-mint-400)" });
  });

  return <Sankey nodes={nodes} links={links} width={1100} height={500} />;
}
