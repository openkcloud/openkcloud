"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { Server, Play, Square, RotateCw, Trash2, Search, Settings } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useAuth } from "@/lib/auth";
import { useMyProjectIds } from "@/lib/member-api";
import {
  useServers, usePowerAction, useDeleteServer, useProjects, byProjectThenName,
  useDeviceProfiles, useAccelModels, accelLabelResolver, type ServerSummary,
} from "@/lib/iaas-api";

const STATUS_VARIANT: Record<string, "success" | "secondary" | "warning" | "outline"> = {
  ACTIVE: "success", SHUTOFF: "secondary", ERROR: "outline", PAUSED: "warning", SUSPENDED: "warning",
};
const stVariant = (s: string) => STATUS_VARIANT[s] ?? "warning";

// 가상서버 — OpenStack Nova 실연동(운영). 상세는 별도 페이지(/iaas/instances/detail?id=). 생성은 자원요청 흐름에서만.
export default function InstancesPage() {
  const router = useRouter();
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername);
  const { data, isLoading } = useServers();
  const { data: projData } = useProjects();
  const dps = useDeviceProfiles();
  const accelModels = useAccelModels();
  const accelLabel = accelLabelResolver(dps.data, accelModels.data);
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [statusFilter, setStatusFilter] = React.useState("all");
  const power = usePowerAction();
  const del = useDeleteServer();
  const confirm = useConfirm();

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] || id.slice(0, 8) || "—";
  }, [projData]);
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set((data ?? []).map((s) => s.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [data, projName]);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter(
    (s) =>
      (isAdmin || myProjectIds?.has(s.tenantId)) &&
      (!q || s.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || s.tenantId === projFilter) &&
      (statusFilter === "all" || s.status === statusFilter),
  ).sort(byProjectThenName(projName));
  const busy = power.isPending || del.isPending;
  const open = (id: string) => router.push(`/iaas/instances/detail/?id=${id}`);

  async function doPower(s: ServerSummary, action: "start" | "stop" | "reboot") {
    const label = action === "start" ? "시작" : action === "stop" ? "정지" : "재부팅";
    if (!(await confirm({ title: `가상서버 ${label}`, destructive: action !== "start", confirmLabel: label, description: `${s.name} 을(를) ${label}할까요?` }))) return;
    power.mutate({ id: s.id, action }, { onSuccess: () => toast.success(`${s.name} ${label} 요청됨`), onError: (e) => toast.error(`${label} 실패`, { description: String((e as Error).message) }) });
  }
  async function doDelete(s: ServerSummary) {
    if (!(await confirm({ title: "가상서버 삭제", destructive: true, confirmLabel: "삭제", description: `${s.name} 을(를) 삭제(terminate)할까요? 되돌릴 수 없습니다.` }))) return;
    del.mutate(s.id, { onSuccess: () => toast.success(`${s.name} 삭제 요청됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <Select value={projFilter} onValueChange={setProjFilter}>
            <SelectTrigger className="w-44"><SelectValue placeholder="프로젝트 필터" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 프로젝트</SelectItem>
              {projOptions.map((p) => (<SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>))}
            </SelectContent>
          </Select>
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className="w-36"><SelectValue placeholder="상태" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 상태</SelectItem>
              <SelectItem value="ACTIVE">ACTIVE</SelectItem>
              <SelectItem value="SHUTOFF">SHUTOFF</SelectItem>
              <SelectItem value="ERROR">ERROR</SelectItem>
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="가상서버 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-52 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto"><RefreshControls queryKeys={[["iaas-servers"]]} /></div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>가상서버</TableHead>
              <TableHead>상태</TableHead>
              <TableHead>플레이버</TableHead>
              <TableHead>가속기</TableHead>
              <TableHead>IP</TableHead>
              <TableHead>호스트</TableHead>
              <TableHead className="w-40" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((s) => (
              <TableRow key={s.id} className="cursor-pointer" onClick={() => open(s.id)}>
                <TableCell className="text-xs font-medium">{s.tenantId ? projName(s.tenantId) : "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Server className="size-3.5 text-brand-indigo-600" />
                    <span className="max-w-[200px] truncate font-mono text-sm font-semibold" title={s.name}>{s.name}</span>
                  </div>
                </TableCell>
                <TableCell><Badge variant={stVariant(s.status)} className="text-[10px]">{s.status}</Badge></TableCell>
                <TableCell className="font-mono text-xs">{s.flavorName}</TableCell>
                <TableCell>{s.accelerator ? <Badge variant="default" className="font-mono text-[10px]" title={s.accelerator}>{accelLabel(s.accelerator)}</Badge> : <span className="text-muted-foreground">—</span>}</TableCell>
                <TableCell className="font-mono text-[11px]">{s.addresses.map((a) => a.ip).join(", ") || "—"}</TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">{s.host || "—"}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-0.5">
                    {s.status === "SHUTOFF" ? (
                      <Button variant="ghost" size="icon-sm" title="시작" disabled={busy} onClick={() => doPower(s, "start")}><Play className="size-3.5 text-emerald-600" /></Button>
                    ) : (
                      <Button variant="ghost" size="icon-sm" title="정지" disabled={busy} onClick={() => doPower(s, "stop")}><Square className="size-3.5 text-amber-600" /></Button>
                    )}
                    <Button variant="ghost" size="icon-sm" title="재부팅" disabled={busy} onClick={() => doPower(s, "reboot")}><RotateCw className="size-3.5" /></Button>
                    <Button variant="ghost" size="icon-sm" title="상세" onClick={() => open(s.id)}><Settings className="size-3.5" /></Button>
                    {isAdmin && <Button variant="ghost" size="icon-sm" title="삭제" disabled={busy} onClick={() => doDelete(s)}><Trash2 className="size-3.5 text-rose-600" /></Button>}
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow><TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">가상서버가 없습니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}
