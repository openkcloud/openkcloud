"use client";
import * as React from "react";
import { HardDrive, Plus, Search, Settings, Trash2, Camera, Expand, Loader2, Database } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useAuth } from "@/lib/auth";
import { useMyProjectIds } from "@/lib/member-api";
import {
  useVolumes, useVolume, useVolumeTypes, useStorageCapacity,
  useCreateVolume, useDeleteVolume, useExtendVolume, useCreateSnapshot, useDeleteSnapshot, useProjects,
  type VolumeSummary, type VolumeDetail,
  byProjectThenName,
} from "@/lib/iaas-api";
import type { Tenant } from "@/lib/types";

const STATUS_VARIANT: Record<string, "success" | "secondary" | "warning"> = {
  available: "success", "in-use": "success", creating: "warning", error: "secondary",
};

// 볼륨·스토리지 — OpenStack Cinder 실연동(백엔드 Ceph). 목록은 프로젝트 컬럼이 맨 앞.
export default function VolumesPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername);
  const { data, isLoading } = useVolumes();
  const { data: projData } = useProjects();
  const { data: pools } = useStorageCapacity();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<VolumeSummary | undefined>();

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] ?? id;
  }, [projData]);
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set((data ?? []).map((v) => v.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [data, projName]);
  // ?detail=<id> 딥링크 — VM 상세 등에서 특정 볼륨 상세를 바로 열 때
  const autoOpened = React.useRef(false);
  React.useEffect(() => {
    if (autoOpened.current || !data) return;
    const id = new URLSearchParams(window.location.search).get("detail");
    if (id) { const v = data.find((x) => x.id === id); if (v) { setSelected(v); autoOpened.current = true; } }
  }, [data]);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter(
    (v) =>
      (isAdmin || myProjectIds?.has(v.tenantId)) &&
      (!q || v.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || v.tenantId === projFilter),
  ).sort(byProjectThenName(projName));

  return (
    <div className="space-y-4">
      {/* Ceph 용량 패널 */}
      {pools && pools.length > 0 && (
        <div className="grid gap-3 md:grid-cols-2">
          {pools.map((p) => {
            const used = Math.max(0, p.totalGb - p.freeGb);
            const pct = p.totalGb > 0 ? Math.round((used / p.totalGb) * 100) : 0;
            return (
              <Card key={p.name} className="p-4">
                <div className="mb-2 flex items-center gap-2">
                  <Database className="size-4 text-brand-indigo-600" />
                  <span className="text-sm font-semibold">스토리지 풀</span>
                  <Badge variant="outline" className="font-mono text-[10px] uppercase">{p.protocol || "ceph"}</Badge>
                  <span className="ml-auto font-mono text-xs text-muted-foreground">{p.backend}</span>
                </div>
                <Progress value={pct} indicatorClassName={pct > 85 ? "bg-rose-500" : pct > 70 ? "bg-amber-500" : "bg-brand-mint-500"} />
                <div className="mt-1.5 flex justify-between font-mono text-[11px] text-muted-foreground">
                  <span>사용 {(used / 1024).toFixed(2)}TB ({pct}%)</span>
                  <span>가용 {(p.freeGb / 1024).toFixed(2)}TB / 총 {(p.totalGb / 1024).toFixed(2)}TB</span>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <Card className="p-4">
        <div className="flex items-center gap-3">
          <Select value={projFilter} onValueChange={setProjFilter}>
            <SelectTrigger className="w-48"><SelectValue placeholder="프로젝트 필터" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 프로젝트</SelectItem>
              {projOptions.map((p) => (<SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>))}
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="볼륨 이름 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-volumes"], ["iaas-storage-capacity"]]} />
            {isAdmin && (
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" /> 볼륨 생성
              </Button>
            )}
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>볼륨</TableHead>
              <TableHead>유형</TableHead>
              <TableHead className="text-center">크기</TableHead>
              <TableHead>부팅</TableHead>
              <TableHead className="text-center">연결 VM</TableHead>
              <TableHead>상태</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((v) => (
              <TableRow key={v.id} className="cursor-pointer" onClick={() => setSelected(v)}>
                <TableCell className="text-xs font-medium">{v.tenantId ? projName(v.tenantId) : "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <HardDrive className="size-3.5 text-brand-indigo-600" />
                    <span className="max-w-[220px] truncate font-mono text-sm font-semibold">{v.name}</span>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs">{v.type}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{v.size}GB</TableCell>
                <TableCell>{v.bootable ? <Badge variant="outline" className="text-[10px]">부팅</Badge> : <span className="text-muted-foreground">—</span>}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{v.attachments.length}</TableCell>
                <TableCell><Badge variant={STATUS_VARIANT[v.status] ?? "secondary"} className="text-[10px]">{v.status}</Badge></TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="sm" onClick={() => setSelected(v)}><Settings className="size-3.5" /> 관리</Button>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow><TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">볼륨이 없습니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(undefined)}>
        <DialogContent className="max-w-2xl">
          {selected && <VolumeDetailBody volume={selected} projectLabel={projName(selected.tenantId)} canWrite={isAdmin} onClose={() => setSelected(undefined)} />}
        </DialogContent>
      </Dialog>

      <CreateVolumeDialog open={createOpen} onClose={() => setCreateOpen(false)} projects={projData?.items ?? []} />
    </div>
  );
}

function VolumeDetailBody({ volume, projectLabel, canWrite, onClose }: { volume: VolumeSummary; projectLabel: string; canWrite: boolean; onClose: () => void }) {
  const { data: d, isLoading, isError } = useVolume(volume.id);
  const del = useDeleteVolume();
  const confirm = useConfirm();
  const [extendOpen, setExtendOpen] = React.useState(false);
  const t = d ?? (volume as VolumeDetail);

  async function remove() {
    // in-use(VM 연결·부팅디스크) 볼륨은 Cinder가 삭제 거부 → 분리 먼저(분리는 VM 연동 후 제공)
    if (t.attachments.length > 0 || t.status === "in-use") {
      await confirm({
        title: "삭제할 수 없음",
        confirmLabel: "확인", cancelLabel: "닫기",
        description: "이 볼륨은 VM에 연결(in-use) 중입니다. 연결된 VM에서 먼저 분리해야 삭제할 수 있습니다. (볼륨 분리 기능은 가상서버 연동 시 제공)",
      });
      return;
    }
    const snapCount = d?.snapshots.length ?? 0;
    if (!(await confirm({
      title: "볼륨 삭제", destructive: true, confirmLabel: "삭제",
      description: snapCount > 0
        ? `${t.name} (${t.size}GB) 을(를) 삭제할까요? 이 볼륨에 스냅샷 ${snapCount}개가 있어 함께 삭제됩니다. 되돌릴 수 없습니다.`
        : `${t.name} (${t.size}GB) 을(를) 삭제할까요? 되돌릴 수 없습니다.`,
    }))) return;
    del.mutate({ id: t.id, cascade: snapCount > 0 }, {
      onSuccess: () => { toast.success(`볼륨 ${t.name} 삭제됨`); onClose(); },
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <>
      <DialogHeader>
        <div className="flex items-center gap-3 pr-8">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <HardDrive className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <DialogTitle className="truncate font-mono text-lg">{t.name}</DialogTitle>
              <Badge variant={STATUS_VARIANT[t.status] ?? "secondary"} className="shrink-0 text-[10px]">{t.status}</Badge>
            </div>
            <DialogDescription className="truncate text-xs">
              <span className="font-mono">{t.id}</span> · 프로젝트 {projectLabel}
            </DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <div className="max-h-[65vh] min-w-0 overflow-y-auto">
        {isLoading && !d ? (
          <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 조회 중…</div>
        ) : isError ? (
          <div className="p-6 text-sm text-rose-600">볼륨 상세 조회 실패</div>
        ) : (
          <Tabs defaultValue="info">
            <TabsList>
              <TabsTrigger value="info">정보</TabsTrigger>
              <TabsTrigger value="snapshots">스냅샷 ({d?.snapshots.length ?? 0})</TabsTrigger>
            </TabsList>

            <TabsContent value="info" className="pt-3">
              <Card className="space-y-2.5 p-4 text-xs">
                <KV label="볼륨 ID" value={t.id} mono />
                <KV label="프로젝트" value={projectLabel} />
                <KV label="유형(Ceph 풀)" value={t.type} mono />
                <KV label="크기" value={`${t.size} GB`} />
                <KV label="부팅 가능" value={t.bootable ? "예" : "아니오"} />
                <KV label="상태" value={t.status} />
                <div className="flex items-start justify-between gap-3">
                  <span className="shrink-0 text-muted-foreground">연결된 VM</span>
                  <span className="min-w-0 break-all text-right font-mono">
                    {t.attachments.length === 0 ? "—" : t.attachments.map((a) => a.serverId).join(", ")}
                  </span>
                </div>
                <KV label="설명" value={t.description || "—"} />
              </Card>
            </TabsContent>

            <TabsContent value="snapshots" className="pt-3">
              {d && <SnapshotsTab volumeId={d.id} volumeName={d.name} snapshots={d.snapshots} canWrite={canWrite} />}
            </TabsContent>
          </Tabs>
        )}
      </div>

      {canWrite && (
        <DialogFooter className="border-t border-border pt-4">
          <Button variant="outline" size="sm" onClick={() => setExtendOpen(true)}><Expand className="size-3.5" /> 확장</Button>
          <Button variant="destructive" size="sm" className="ml-auto" disabled={del.isPending} onClick={remove}><Trash2 className="size-3.5" /> 삭제</Button>
        </DialogFooter>
      )}

      {canWrite && <ExtendVolumeDialog volumeId={t.id} currentSize={t.size} open={extendOpen} onClose={() => setExtendOpen(false)} />}
    </>
  );
}

function SnapshotsTab({ volumeId, volumeName, snapshots, canWrite }: { volumeId: string; volumeName: string; snapshots: VolumeDetail["snapshots"]; canWrite: boolean }) {
  const create = useCreateSnapshot();
  const del = useDeleteSnapshot();
  const confirm = useConfirm();
  const [name, setName] = React.useState(`${volumeName}-snap`);

  function add() {
    if (!name.trim()) return toast.error("스냅샷 이름을 입력하세요");
    create.mutate({ volumeId, name: name.trim(), force: true }, {
      onSuccess: () => toast.success("스냅샷 생성됨"),
      onError: (e) => toast.error("스냅샷 생성 실패", { description: String((e as Error).message) }),
    });
  }
  async function remove(id: string, nm: string) {
    if (!(await confirm({ title: "스냅샷 삭제", destructive: true, confirmLabel: "삭제", description: `'${nm}' 스냅샷을 삭제할까요?` }))) return;
    del.mutate(id, { onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }

  return (
    <div className="space-y-3">
      {canWrite && (
        <div className="flex items-center gap-2">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="스냅샷 이름" className="h-8" />
          <Button size="sm" disabled={create.isPending} onClick={add}><Camera className="size-3.5" /> 생성</Button>
        </div>
      )}
      {snapshots.length === 0 ? (
        <Card className="p-6 text-center text-sm text-muted-foreground">스냅샷이 없습니다.</Card>
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader><TableRow><TableHead>이름</TableHead><TableHead className="text-center">크기</TableHead><TableHead>상태</TableHead><TableHead className="w-10" /></TableRow></TableHeader>
            <TableBody>
              {snapshots.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-mono text-xs">{s.name}</TableCell>
                  <TableCell className="text-center font-mono tabular-nums">{s.size}GB</TableCell>
                  <TableCell><Badge variant={STATUS_VARIANT[s.status] ?? "secondary"} className="text-[10px]">{s.status}</Badge></TableCell>
                  <TableCell>
                    {canWrite && (
                      <Button variant="ghost" size="icon-sm" disabled={del.isPending} title="삭제" onClick={() => remove(s.id, s.name)}>
                        <Trash2 className="size-3.5 text-rose-600" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}

function ExtendVolumeDialog({ volumeId, currentSize, open, onClose }: { volumeId: string; currentSize: number; open: boolean; onClose: () => void }) {
  const [size, setSize] = React.useState(String(currentSize + 10));
  const extend = useExtendVolume();
  React.useEffect(() => { if (open) setSize(String(currentSize + 10)); }, [open, currentSize]);

  function submit() {
    const n = Number(size);
    if (!n || n <= currentSize) return toast.error(`현재 크기(${currentSize}GB)보다 커야 합니다`);
    extend.mutate({ id: volumeId, newSize: n }, {
      onSuccess: () => { toast.success(`볼륨 확장 ${currentSize}→${n}GB`); onClose(); },
      onError: (e) => toast.error("확장 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !extend.isPending && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>볼륨 확장</DialogTitle>
          <DialogDescription>크기는 늘릴 수만 있습니다(축소 불가). 현재 {currentSize}GB.</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 py-2">
          <Label>새 크기 (GB)</Label>
          <Input type="number" value={size} onChange={(e) => setSize(e.target.value)} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={extend.isPending}>취소</Button>
          <Button onClick={submit} disabled={extend.isPending}>{extend.isPending ? "확장 중…" : "확장"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreateVolumeDialog({ open, onClose, projects }: { open: boolean; onClose: () => void; projects: Tenant[] }) {
  const { data: types } = useVolumeTypes();
  const [projectId, setProjectId] = React.useState("");
  const [name, setName] = React.useState("");
  const [size, setSize] = React.useState("10");
  const [type, setType] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const create = useCreateVolume();
  React.useEffect(() => { if (open) { setProjectId(""); setName(""); setSize("10"); setType(""); setDesc(""); } }, [open]);

  function submit() {
    if (!projectId) return toast.error("프로젝트를 선택하세요");
    if (!name.trim()) return toast.error("이름을 입력하세요");
    const n = Number(size);
    if (!n || n <= 0) return toast.error("크기(GB)를 입력하세요");
    create.mutate(
      { name: name.trim(), size: n, projectId, volumeType: type || undefined, description: desc },
      {
        onSuccess: (v) => { toast.success(`볼륨 ${v.name} 생성됨`, { description: `${v.size}GB ${v.type}` }); onClose(); },
        onError: (e) => toast.error("볼륨 생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>볼륨 생성</DialogTitle>
          <DialogDescription>OpenStack Cinder 볼륨(백엔드 Ceph). 선택한 프로젝트 소유로 생성됩니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>프로젝트</Label>
            <Select value={projectId} onValueChange={setProjectId}>
              <SelectTrigger><SelectValue placeholder="프로젝트 선택" /></SelectTrigger>
              <SelectContent>
                {projects.filter((p) => p.enabled !== false).map((p) => (
                  <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-[10px] text-muted-foreground">비활성(disabled) 프로젝트에는 생성할 수 없어 목록에서 제외됩니다.</p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>이름</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="data-vol-01" />
            </div>
            <div className="space-y-1.5">
              <Label>크기 (GB)</Label>
              <Input type="number" value={size} onChange={(e) => setSize(e.target.value)} />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>볼륨 유형 (Ceph 풀)</Label>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger><SelectValue placeholder="기본값" /></SelectTrigger>
              <SelectContent>
                {(types ?? []).map((t) => (<SelectItem key={t.id} value={t.name}>{t.name}</SelectItem>))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>설명 (선택)</Label>
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="용도 설명" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={create.isPending}>취소</Button>
          <Button onClick={submit} disabled={create.isPending}>{create.isPending ? "생성 중…" : "생성"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className={mono ? "min-w-0 break-all text-right font-mono" : "min-w-0 break-words text-right"}>{value}</span>
    </div>
  );
}
