"use client";
import * as React from "react";
import { Cpu, Search, Server, Monitor, Tag } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useAuth } from "@/lib/auth";
import {
  useAcceleratorDevices, useAccelModels, useUpsertAccelModel, useDeleteAccelModel,
  accelModelByProductId, type AcceleratorDevice,
} from "@/lib/iaas-api";

// 가속기 장치 현황(Cyborg 5-2) — 물리 노드별 가속기 인벤토리·Resource Class·Trait·연결 VM·사용 상태(조회 전용).
const USAGE: Record<AcceleratorDevice["usage"], { label: string; variant: "warning" | "success" | "secondary" }> = {
  used: { label: "사용 중", variant: "warning" },
  available: { label: "사용 가능", variant: "success" },
  unknown: { label: "알 수 없음", variant: "secondary" },
};

// Cyborg 가 주는 vendor 값은 PCI 벤더 ID(4자리 16진수). 알려진 값은 제조사명 + 원본 ID 로 표기.
const PCI_VENDORS: Record<string, string> = {
  "1ed2": "Furiosa",
  "1eff": "Rebellions",
  "10de": "NVIDIA",
  "1002": "AMD",
  "8086": "Intel",
};
function vendorLabel(v: string | null): string {
  if (!v) return "—";
  const name = PCI_VENDORS[v.toLowerCase().replace(/^0x/, "")];
  return name ? `${name} (${v})` : v;
}

export default function AcceleratorDevicesPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const { data, isLoading } = useAcceleratorDevices();
  const accelModels = useAccelModels();
  const byPid = accelModelByProductId(accelModels.data); // product_id → 모델명
  const [q, setQ] = React.useState("");
  const [type, setType] = React.useState("all");
  const [vendor, setVendor] = React.useState("all");
  const [vmView, setVmView] = React.useState<AcceleratorDevice | null>(null); // 연결 VM 목록 보기

  if (isLoading || !data) return <Skeleton className="h-96" />;
  // 모델명 = 매핑(product_id) 우선, 미매핑 시 product_id 원본, 그마저 없으면 Cyborg model
  const modelOf = (d: AcceleratorDevice) =>
    (d.productId && byPid[d.productId]) || d.productId || d.model || "—";

  const types = Array.from(new Set(data.map((d) => d.type).filter(Boolean))) as string[];
  const vendors = Array.from(new Set(data.map((d) => d.vendor).filter(Boolean))) as string[];
  const items = data.filter((d) =>
    (!q || [d.hostname, d.model, d.productId, d.uuid].some((v) => (v || "").toLowerCase().includes(q.toLowerCase())))
    && (type === "all" || d.type === type)
    && (vendor === "all" || d.vendor === vendor),
  );
  // 노드(hostname)별 그룹
  const byHost = new Map<string, AcceleratorDevice[]>();
  for (const d of items) {
    const h = d.hostname || "(알 수 없음)";
    byHost.set(h, [...(byHost.get(h) ?? []), d]);
  }
  const hosts = Array.from(byHost.keys()).sort();

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="호스트·모델·UUID 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-64 pl-9" />
          </div>
          <Select value={type} onValueChange={setType}>
            <SelectTrigger className="w-36"><SelectValue placeholder="종류" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 종류</SelectItem>
              {types.map((t) => <SelectItem key={t} value={t}>{t}</SelectItem>)}
            </SelectContent>
          </Select>
          <Select value={vendor} onValueChange={setVendor}>
            <SelectTrigger className="w-40"><SelectValue placeholder="벤더" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 벤더</SelectItem>
              {vendors.map((v) => <SelectItem key={v} value={v}>{vendorLabel(v)}</SelectItem>)}
            </SelectContent>
          </Select>
          <span className="text-xs text-muted-foreground">총 {items.length}개 장치</span>
          <div className="ml-auto"><RefreshControls queryKeys={[["iaas-accel-devices"]]} /></div>
        </div>
      </Card>

      {isAdmin && <ModelMappingCard devices={data} byPid={byPid} />}

      {hosts.length === 0 ? (
        <Card><div className="py-10 text-center text-sm text-muted-foreground">가속기 장치가 없습니다.</div></Card>
      ) : hosts.map((host) => (
        <Card key={host}>
          <div className="flex items-center gap-2 border-b px-4 py-2.5">
            <Server className="size-4 text-brand-indigo-600" />
            <span className="font-mono text-sm font-semibold">{host}</span>
            <span className="text-xs text-muted-foreground">장치 {byHost.get(host)!.length}개</span>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>종류</TableHead>
                <TableHead>모델</TableHead>
                <TableHead>벤더 (PCI ID)</TableHead>
                <TableHead>Resource Class</TableHead>
                <TableHead>Trait</TableHead>
                <TableHead>상태</TableHead>
                <TableHead>연결 VM</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {byHost.get(host)!.map((d) => (
                <TableRow key={d.uuid}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Cpu className="size-3.5 text-brand-indigo-600" />
                      <span className="font-mono text-xs font-semibold">{d.type || "—"}</span>
                    </div>
                  </TableCell>
                  <TableCell className="text-sm" title={[d.productId && `product_id=${d.productId}`, d.model && `cyborg=${d.model}`].filter(Boolean).join(" · ") || undefined}>
                    {modelOf(d)}
                    {d.productId && !byPid[d.productId] && <span className="ml-1.5 text-[10px] text-amber-700">미매핑</span>}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">{vendorLabel(d.vendor)}</TableCell>
                  <TableCell>
                    {d.resourceClasses.length
                      ? <div className="flex flex-wrap gap-1">{d.resourceClasses.map((rc) => <Badge key={rc} variant="outline" className="font-mono text-[10px]">{rc}</Badge>)}</div>
                      : <span className="text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell>
                    {d.traits.length
                      ? <div className="flex flex-wrap gap-1">{d.traits.map((t) => <Badge key={t} variant="secondary" className="font-mono text-[10px]">{t}</Badge>)}</div>
                      : <span className="text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell><Badge variant={USAGE[d.usage].variant} className="text-[10px]">{USAGE[d.usage].label}</Badge></TableCell>
                  <TableCell>
                    {d.instances.length
                      ? <Button variant="outline" size="sm" className="h-7 gap-1.5 text-xs" onClick={() => setVmView(d)}>
                          <Monitor className="size-3.5" /> {d.instances.length}개 보기
                        </Button>
                      : <span className="text-xs text-muted-foreground">—</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      ))}

      <Dialog open={!!vmView} onOpenChange={(o) => !o && setVmView(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2"><Monitor className="size-4 text-brand-indigo-600" /> 연결된 VM</DialogTitle>
            <DialogDescription className="font-mono text-xs">
              {vmView?.type} · {vmView?.model || vmView?.uuid} · {vmView?.hostname}
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[60vh] overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>이름</TableHead>
                  <TableHead>인스턴스 ID</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(vmView?.instances ?? []).map((i) => (
                  <TableRow key={i.id}>
                    <TableCell className="whitespace-nowrap text-sm">{i.name || "—"}</TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-[11px] text-muted-foreground">{i.id}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// 모델명 매핑(관리자) — 장치에서 추출한 고유 product_id 에 모델명을 지정. Cyborg 가 모델명을 주지 않아 포털에서 관리(ETRI 협의).
function ModelMappingCard({ devices, byPid }: { devices: AcceleratorDevice[]; byPid: Record<string, string> }) {
  const upsert = useUpsertAccelModel();
  const del = useDeleteAccelModel();
  const [edits, setEdits] = React.useState<Record<string, string>>({});

  // 고유 (product_id, vendor) — product_id 가 있는 장치만
  const rows = React.useMemo(() => {
    const m = new Map<string, { productId: string; vendor: string | null }>();
    for (const d of devices) if (d.productId) m.set(d.productId, { productId: d.productId, vendor: d.vendor ?? null });
    return Array.from(m.values()).sort((a, b) => a.productId.localeCompare(b.productId));
  }, [devices]);

  const valueOf = (pid: string) => edits[pid] ?? byPid[pid] ?? "";
  function save(pid: string, vendor: string | null) {
    const model = valueOf(pid).trim();
    if (!model) return toast.error("모델명을 입력하세요");
    upsert.mutate({ productId: pid, vendor, model }, {
      onSuccess: () => toast.success(`${pid} → ${model} 저장됨`),
      onError: (e) => toast.error("저장 실패", { description: String((e as Error).message) }),
    });
  }
  function remove(pid: string) {
    del.mutate(pid, {
      onSuccess: () => { toast.success("매핑 해제됨"); setEdits((s) => { const n = { ...s }; delete n[pid]; return n; }); },
      onError: (e) => toast.error("매핑 해제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Card>
      <div className="flex items-center gap-2 border-b px-4 py-2.5">
        <Tag className="size-4 text-brand-indigo-600" />
        <span className="text-sm font-semibold">모델명 매핑</span>
        <span className="text-xs text-muted-foreground">product_id → 모델명. 여기서 지정한 모델명이 플레이버·가상서버·클러스터 배지에 표기됩니다.</span>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>벤더 (PCI ID)</TableHead>
            <TableHead>product_id</TableHead>
            <TableHead>모델명</TableHead>
            <TableHead className="w-44" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(({ productId, vendor }) => (
            <TableRow key={productId}>
              <TableCell className="text-sm text-muted-foreground">{vendorLabel(vendor)}</TableCell>
              <TableCell className="font-mono text-xs">{productId}</TableCell>
              <TableCell>
                <Input value={valueOf(productId)} onChange={(e) => setEdits((s) => ({ ...s, [productId]: e.target.value }))}
                  placeholder="예: L40S" className="h-8 w-40" />
              </TableCell>
              <TableCell>
                <div className="flex gap-1">
                  <Button size="sm" className="h-8" disabled={upsert.isPending} onClick={() => save(productId, vendor)}>저장</Button>
                  {byPid[productId] && <Button size="sm" variant="outline" className="h-8" disabled={del.isPending} onClick={() => remove(productId)}>매핑 해제</Button>}
                </div>
              </TableCell>
            </TableRow>
          ))}
          {rows.length === 0 && (
            <TableRow><TableCell colSpan={4} className="py-6 text-center text-sm text-muted-foreground">product_id 를 가진 가속기 장치가 없습니다.</TableCell></TableRow>
          )}
        </TableBody>
      </Table>
    </Card>
  );
}
