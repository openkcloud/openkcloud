"use client";
import * as React from "react";
import {
  Building2,
  Server,
  Boxes,
  Cpu,
  Container as ContainerIcon,
  ChevronRight,
  Sparkles,
  Network,
} from "lucide-react";
import { api } from "@/lib/api";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn, relativeTime } from "@/lib/utils";
import { LAYER_INFO, type LayerId, type ResourceMapNode, type ResourceMapRelationship } from "@/lib/types";

type ResourceMapResponse = {
  observed_at: string;
  is_stale: boolean;
  nodes: ResourceMapNode[];
  edges: ResourceMapRelationship[];
  stats: Record<LayerId | string, number>;
};

const LAYER_COLOR: Record<LayerId, { bg: string; border: string; text: string; chip: string }> = {
  L0: { bg: "bg-slate-50", border: "border-slate-300", text: "text-slate-800", chip: "bg-slate-200 text-slate-800" },
  L1: { bg: "bg-sky-50", border: "border-sky-300", text: "text-sky-900", chip: "bg-sky-200 text-sky-900" },
  L2: { bg: "bg-violet-50", border: "border-violet-300", text: "text-violet-900", chip: "bg-violet-200 text-violet-900" },
  L2b: { bg: "bg-violet-50", border: "border-violet-300", text: "text-violet-900", chip: "bg-violet-200 text-violet-900" },
  L3: { bg: "bg-rose-50", border: "border-rose-300", text: "text-rose-900", chip: "bg-rose-200 text-rose-900" },
  L4: { bg: "bg-amber-50", border: "border-amber-300", text: "text-amber-900", chip: "bg-amber-200 text-amber-900" },
  L5: { bg: "bg-brand-indigo-50", border: "border-brand-indigo-300", text: "text-brand-indigo-900", chip: "bg-brand-indigo-100 text-brand-indigo-900" },
  LA: { bg: "bg-brand-mint-50", border: "border-brand-mint-200", text: "text-brand-mint-800", chip: "bg-brand-mint-100 text-brand-mint-800" },
};

const REL_COLOR: Record<ResourceMapRelationship["kind"], string> = {
  attached_to: "stroke-slate-400",
  passthrough_to: "stroke-rose-500",
  allocated_to: "stroke-brand-indigo-500",
  runs_on: "stroke-sky-400",
  scheduled_to: "stroke-amber-500",
  partitioned_from: "stroke-brand-mint-500",
};

export default function ResourceMapPage() {
  const { data, isLoading } = api.resourceMap.graph() as { data: ResourceMapResponse | undefined; isLoading: boolean };
  const [highlighted, setHighlighted] = React.useState<string | undefined>();
  const [filterLayer, setFilterLayer] = React.useState<"all" | LayerId>("all");

  return (
    <div className="space-y-6">
      <PageHeader
        title="리소스 현황"
        description="이노그리드 KCloud Monitor v2 — 6계층 자원 모델 (L0→L5 + LA)로 모든 리소스 관계 추적"
        actions={
          <Button variant="outline" size="sm">
            <Network className="size-3.5" /> Discovery 트리거
          </Button>
        }
      />

      {/* Innogrid spec callout */}
      <Card className="border-brand-mint-200 bg-gradient-to-br from-brand-mint-50 to-transparent">
        <CardContent className="flex items-start gap-3 p-4">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-brand-mint-100 text-brand-mint-700">
            <Sparkles className="size-4" />
          </div>
          <div className="space-y-1 text-sm">
            <div className="font-semibold">자원 흐름 핵심 원칙</div>
            <p className="text-muted-foreground">
              GPU·NPU는 <strong className="text-foreground">물리 서버에 부착(attached_to)</strong>되고,
              {" "}<strong className="text-foreground">VM으로 패스스루(passthrough_to)</strong>되며,
              {" "}최종적으로 <strong className="text-foreground">컨테이너에 할당(allocated_to)</strong>됩니다.
              서비스 클러스터는 OpenStack Magnum이 <strong className="text-foreground">프로젝트 단위(과금 단위)</strong>로 생성합니다.
            </p>
          </div>
        </CardContent>
      </Card>

      {/* Layer stats band */}
      <div className="grid grid-cols-3 gap-3 md:grid-cols-6">
        {(["L0", "L1", "L3", "L4", "L5", "LA"] as LayerId[]).map((l) => {
          const c = LAYER_COLOR[l];
          return (
            <Card key={l} className={cn("p-3", c.bg, c.border, "border")}>
              <div className={cn("text-[10px] font-mono uppercase", c.text)}>{l}</div>
              <div className="mt-0.5 text-2xl font-bold tabular-nums">
                {data?.stats[l] ?? "—"}
              </div>
              <div className="text-[10px] text-muted-foreground line-clamp-1">
                {LAYER_INFO[l].name.replace(/^Layer \w+ · /, "")}
              </div>
            </Card>
          );
        })}
      </div>

      {/* Tabs: Tree view / Graph view */}
      <Tabs defaultValue="tree">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="tree">계층 트리</TabsTrigger>
            <TabsTrigger value="graph">관계 그래프</TabsTrigger>
            <TabsTrigger value="relationships">관계 정의</TabsTrigger>
            <TabsTrigger value="json">JSON 응답</TabsTrigger>
          </TabsList>
          <div className="flex items-center gap-2">
            <Select value={filterLayer} onValueChange={(v) => setFilterLayer(v as typeof filterLayer)}>
              <SelectTrigger className="w-44 text-xs">
                <SelectValue placeholder="Layer 필터" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">전체 계층</SelectItem>
                {(["L0", "L1", "L3", "L4", "L5", "LA"] as LayerId[]).map((l) => (
                  <SelectItem key={l} value={l}>
                    {LAYER_INFO[l].name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {data && (
              <span className="font-mono text-[10px] text-muted-foreground">
                observed_at: {data.observed_at}
              </span>
            )}
          </div>
        </div>

        <TabsContent value="tree" className="mt-4">
          {isLoading || !data ? (
            <Skeleton className="h-96" />
          ) : (
            <LayerTreeView data={data} filterLayer={filterLayer} onSelect={setHighlighted} highlighted={highlighted} />
          )}
        </TabsContent>

        <TabsContent value="graph" className="mt-4">
          {isLoading || !data ? <Skeleton className="h-96" /> : <RelationshipGraph data={data} />}
        </TabsContent>

        <TabsContent value="relationships" className="mt-4">
          <RelationshipsTable />
        </TabsContent>

        <TabsContent value="json" className="mt-4">
          <Card className="overflow-hidden bg-slate-950 text-slate-100">
            <pre className="overflow-x-auto p-4 font-mono text-[11px] leading-relaxed">
              {JSON.stringify(
                data
                  ? {
                      observed_at: data.observed_at,
                      is_stale: data.is_stale,
                      stats: data.stats,
                      nodes: data.nodes.slice(0, 6),
                      edges: data.edges.slice(0, 8),
                    }
                  : {},
                null,
                2,
              )}
            </pre>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

function LayerTreeView({
  data,
  filterLayer,
  onSelect,
  highlighted,
}: {
  data: ResourceMapResponse;
  filterLayer: "all" | LayerId;
  onSelect: (id: string) => void;
  highlighted?: string;
}) {
  const layers: LayerId[] = ["L0", "L1", "L3", "L4", "L5", "LA"];
  const filtered = filterLayer === "all" ? layers : layers.filter((l) => l === filterLayer);

  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      {filtered.map((l) => {
        const c = LAYER_COLOR[l];
        const items = data.nodes.filter((n) => n.layer === l);
        return (
          <Card key={l} className={cn("overflow-hidden", c.border, "border")}>
            <CardHeader className={cn("py-3", c.bg)}>
              <CardTitle className={cn("flex items-center justify-between text-sm", c.text)}>
                <span className="flex items-center gap-2">
                  <LayerIcon layer={l} />
                  {LAYER_INFO[l].name}
                </span>
                <Badge variant="outline" className="font-mono text-[10px]">{items.length}</Badge>
              </CardTitle>
              <CardDescription className="text-xs">{LAYER_INFO[l].desc}</CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <div className="max-h-[360px] divide-y divide-border overflow-y-auto">
                {items.map((n) => (
                  <button
                    key={n.id}
                    onClick={() => onSelect(n.id)}
                    className={cn(
                      "block w-full px-4 py-2.5 text-left text-xs transition-colors hover:bg-secondary/40",
                      highlighted === n.id && "bg-brand-indigo-50/60",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium leading-tight">{n.label}</span>
                      <span className="font-mono text-[10px] text-muted-foreground">{n.id.split(":")[1]}</span>
                    </div>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {Object.entries(n.meta).slice(0, 3).map(([k, v]) => (
                        <span
                          key={k}
                          className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[9px] text-muted-foreground"
                        >
                          {k}=<strong className="text-foreground">{String(v)}</strong>
                        </span>
                      ))}
                    </div>
                  </button>
                ))}
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}

function RelationshipGraph({ data }: { data: ResourceMapResponse }) {
  // Layout nodes by layer columns
  const layerOrder: LayerId[] = ["L0", "L1", "L3", "L4", "L5", "LA"];
  const colW = 160;
  const rowH = 36;
  const padX = 60;
  const padY = 40;

  // Compute positions: group by layer, vertically stack
  const positions = new Map<string, { x: number; y: number; node: ResourceMapNode }>();
  layerOrder.forEach((l, li) => {
    const items = data.nodes.filter((n) => n.layer === l).slice(0, 14);
    items.forEach((n, i) => {
      positions.set(n.id, {
        x: padX + li * colW,
        y: padY + i * rowH,
        node: n,
      });
    });
  });
  const width = padX * 2 + (layerOrder.length - 1) * colW + 200;
  const height = padY * 2 + Math.max(...layerOrder.map((l) => data.nodes.filter((n) => n.layer === l).slice(0, 14).length)) * rowH;

  return (
    <Card>
      <CardContent className="overflow-x-auto p-4">
        <svg viewBox={`0 0 ${width} ${height}`} className="h-[500px] w-full" style={{ minWidth: width }}>
          {/* Layer headers */}
          {layerOrder.map((l, li) => (
            <text
              key={l}
              x={padX + li * colW + 70}
              y={20}
              fontSize="11"
              fontWeight="700"
              textAnchor="middle"
              className="fill-muted-foreground"
            >
              {l}
            </text>
          ))}

          {/* Edges */}
          {data.edges.map((e, i) => {
            const a = positions.get(e.source);
            const b = positions.get(e.target);
            if (!a || !b) return null;
            const cls = REL_COLOR[e.kind];
            return (
              <g key={i}>
                <line
                  x1={a.x + 140}
                  y1={a.y + 14}
                  x2={b.x}
                  y2={b.y + 14}
                  className={cls}
                  strokeWidth={1.4}
                  strokeOpacity={0.6}
                />
              </g>
            );
          })}

          {/* Nodes */}
          {Array.from(positions.entries()).map(([id, { x, y, node }]) => {
            const c = LAYER_COLOR[node.layer];
            return (
              <g key={id}>
                <rect
                  x={x}
                  y={y}
                  width={140}
                  height={28}
                  rx={5}
                  className={cn("fill-card stroke-current", c.border, c.text)}
                  strokeWidth={1}
                />
                <text x={x + 8} y={y + 17} fontSize="10" fontFamily="monospace" className="fill-foreground">
                  {truncate(node.label, 18)}
                </text>
              </g>
            );
          })}
        </svg>
        <div className="mt-4 flex flex-wrap items-center gap-3 text-[11px]">
          {Object.entries(REL_COLOR).map(([kind, cls]) => (
            <div key={kind} className="flex items-center gap-1.5">
              <div className="h-0.5 w-4">
                <svg width={16} height={2}>
                  <line x1={0} y1={1} x2={16} y2={1} strokeWidth={2} className={cls} />
                </svg>
              </div>
              <span className="font-mono text-muted-foreground">{kind}</span>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function RelationshipsTable() {
  const rels: { kind: string; src: string; tgt: string; desc: string }[] = [
    { kind: "attached_to", src: "L0 · 물리서버", tgt: "LA · 가속기", desc: "가속기가 물리 서버 PCIe에 부착됨 (정적)" },
    { kind: "passthrough_to", src: "L0/LA", tgt: "L3 · OpenStack VM", desc: "물리 GPU가 VM으로 PCI Passthrough됨" },
    { kind: "runs_on", src: "L3 · VM", tgt: "L0 · 물리서버 (hypervisor)", desc: "VM이 실행 중인 하이퍼바이저" },
    { kind: "runs_on", src: "L4 · 서비스 K8s 노드", tgt: "L3 · OpenStack VM", desc: "K8s 노드 = VM 내부" },
    { kind: "scheduled_to", src: "L5 · Pod/Container", tgt: "L4 · K8s 노드", desc: "Pod가 어느 노드에 스케줄됨" },
    { kind: "allocated_to", src: "LA · 가속기/파티션", tgt: "L5 · 컨테이너", desc: "최종 GPU/NPU가 컨테이너에 할당" },
    { kind: "partitioned_from", src: "LA · 가속기", tgt: "Partition (MIG/Slice)", desc: "가속기 파티셔닝 (MIG/vGPU/NPU Slice)" },
  ];
  return (
    <Card>
      <CardContent className="p-0">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-secondary/30 text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 text-left font-medium">관계</th>
              <th className="px-4 py-2 text-left font-medium">From</th>
              <th className="px-4 py-2 text-left font-medium">To</th>
              <th className="px-4 py-2 text-left font-medium">설명</th>
            </tr>
          </thead>
          <tbody>
            {rels.map((r, i) => (
              <tr key={i} className="border-b border-border last:border-0">
                <td className="px-4 py-2.5">
                  <Badge variant="outline" className="font-mono text-[10px]">{r.kind}</Badge>
                </td>
                <td className="px-4 py-2.5 text-xs">{r.src}</td>
                <td className="px-4 py-2.5 text-xs">
                  <span className="inline-flex items-center gap-1">
                    <ChevronRight className="size-3" /> {r.tgt}
                  </span>
                </td>
                <td className="px-4 py-2.5 text-xs text-muted-foreground">{r.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </CardContent>
    </Card>
  );
}

function LayerIcon({ layer }: { layer: LayerId }) {
  const map: Record<LayerId, React.ReactNode> = {
    L0: <Building2 className="size-3.5" />,
    L1: <Server className="size-3.5" />,
    L2: <Boxes className="size-3.5" />,
    L2b: <Boxes className="size-3.5" />,
    L3: <Server className="size-3.5" />,
    L4: <Boxes className="size-3.5" />,
    L5: <ContainerIcon className="size-3.5" />,
    LA: <Cpu className="size-3.5" />,
  };
  return <>{map[layer]}</>;
}

function truncate(s: string, n: number) {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
