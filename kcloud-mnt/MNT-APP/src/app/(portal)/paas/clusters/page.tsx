"use client";
/**
 * 서비스 클러스터 — 사용자 관점(내 K8s 클러스터를 어떻게 쓰나). Magnum 라이브.
 * 생성·삭제는 자원 요청 메뉴(관리자 승인 → Magnum) — 여기엔 신청 버튼을 두지 않는다.
 * 레이아웃·폰트는 IaaS 목록 화면 규약(툴바 Card + 테이블 Card, font-mono 셀)과 동일.
 */
import * as React from "react";
import { Boxes, Download, Search, Loader2, Plus, Scale, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { useRouter, useSearchParams } from "next/navigation";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useProjects, useFlavors, useDeviceProfiles, useAccelModels, accelLabelResolver } from "@/lib/iaas-api";
import {
  useClusters, useCluster, useDeleteCluster, downloadKubeconfig,
  useCreateNodegroup, useResizeNodegroup, useDeleteNodegroup,
  type ClusterSummary, type ClusterNodegroup,
} from "@/lib/paas-api";
import { useAuth } from "@/lib/auth";
import {
  useMyClusterIds, useClusterAccess, useGrantClusterAccess, useRevokeClusterAccess,
  useReconcileClusterAccess, type ClusterAccess,
} from "@/lib/member-api";

/** Magnum 상태 → 배지 톤. *_IN_PROGRESS 는 진행 중(폴링 대상). */
function stVariant(s: string): "success" | "warning" | "destructive" | "secondary" {
  if (s.endsWith("_COMPLETE")) return "success";
  if (s.endsWith("_IN_PROGRESS")) return "warning";
  if (s.endsWith("_FAILED")) return "destructive";
  return "secondary";
}

/** UTC ISO → 로컬(KST) 표기 */
function fmtDateTime(iso?: string | null) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("ko-KR", { hour12: false });
}

/** kubeconfig 받기(목록 행·상세 모달 공용) — 실패하면 오류 안내. */
function saveKubeconfig(id: string, name: string) {
  downloadKubeconfig(id, name).catch((e) =>
    toast.error("kubeconfig 다운로드 실패", { description: String((e as Error).message) }));
}

// 신청 상세(완료된 클러스터)에서 ?id= 로 오면 그 클러스터 상세 창을 바로 연다 — useSearchParams 는 Suspense 안에서(정적 내보내기)
export default function ClustersPage() {
  return <React.Suspense fallback={<Skeleton className="h-96" />}><ClustersView /></React.Suspense>;
}

function ClustersView() {
  const router = useRouter();
  const openId = useSearchParams().get("id");
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  // 가시성 = 포털 DB 매핑(paas_project_cluster) 기준. 관리자는 전체, 사용자는 내 프로젝트에 부여된 클러스터만.
  const { data: myClusterIds } = useMyClusterIds(isAdmin ? undefined : myUsername);
  const clusters = useClusters();
  const projects = useProjects();
  const flavors = useFlavors();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [detailId, setDetailId] = React.useState<string | null>(openId);

  const del = useDeleteCluster();
  const confirm = useConfirm();
  async function handleDelete(c: { id: string; name: string }, onDone?: () => void) {
    if (!(await confirm({
      title: "클러스터 삭제", destructive: true, confirmLabel: "삭제",
      description: `클러스터 "${c.name}" 을(를) 삭제할까요? 되돌릴 수 없으며, 클러스터와 그 위의 모든 워크로드가 함께 삭제됩니다.`,
    }))) return;
    del.mutate(c.id, {
      onSuccess: () => { toast.success("클러스터 삭제를 시작했습니다"); onDone?.(); },
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  // 고아 매핑 정리(sweep) — 관리자가 목록을 열 때마다 실재 ID 집합으로 1회 reconcile.
  // 삭제 완료가 비동기라 "삭제 추적" 대신 자가 치유 방식. 에러·빈 목록이면 호출 안 함(전체 삭제 사고 방지).
  const reconcile = useReconcileClusterAccess();
  const reconciledKey = React.useRef("");
  React.useEffect(() => {
    if (!isAdmin || !clusters.isSuccess) return;
    const ids = (clusters.data ?? []).map((c) => c.id);
    if (ids.length === 0) return;
    const key = ids.slice().sort().join(",");
    if (key === reconciledKey.current) return;
    reconciledKey.current = key;
    reconcile.mutate(ids);
  }, [isAdmin, clusters.isSuccess, clusters.data, reconcile]);

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projects.data?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] || id.slice(0, 8) || "—";
  }, [projects.data]);

  /** flavor 이름 → 가속기 모델명(매핑). dp-rngd 등 device-profile 이름을 모델명으로 치환. */
  const dps = useDeviceProfiles();
  const accelModels = useAccelModels();
  const flavorAccel = React.useMemo(() => {
    const label = accelLabelResolver(dps.data, accelModels.data);
    const m = new Map<string, string>();
    for (const f of flavors.data ?? []) if (f.accelerator) m.set(f.name, label(f.accelerator) ?? f.accelerator);
    return m;
  }, [flavors.data, dps.data, accelModels.data]);

  // 내가 볼 수 있는 클러스터 = 관리자 전체 / 사용자는 내 프로젝트에 접근 부여된 클러스터만(DB 매핑)
  const visible = React.useMemo(
    () => (clusters.data ?? []).filter((c) => isAdmin || myClusterIds?.has(c.id)),
    [clusters.data, isAdmin, myClusterIds],
  );

  // 드롭다운 옵션은 '프로젝트 목록(실이름)'에서 — UUID 가 라벨로 새지 않도록.
  // 이름을 못 찾은 projectId 는 옵션에서 제외(전체 프로젝트로 여전히 조회 가능).
  const projOptions = React.useMemo(() => {
    const ids = new Set(visible.map((c) => c.projectId).filter(Boolean));
    return (projects.data?.items ?? [])
      .filter((p) => ids.has(p.id))
      .map((p) => ({ id: p.id, name: p.name }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [visible, projects.data]);

  if (clusters.isLoading) return <Skeleton className="h-96" />;

  const items = visible.filter(
    (c) =>
      (!q || c.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || c.projectId === projFilter),
  );

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
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="클러스터 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-52 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto"><RefreshControls queryKeys={[["paas-clusters"]]} /></div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>클러스터</TableHead>
              <TableHead>상태</TableHead>
              <TableHead>K8s 버전</TableHead>
              <TableHead>노드(Control Plane+Worker)</TableHead>
              <TableHead>Worker 사양</TableHead>
              <TableHead>생성일</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {clusters.isError && (
              <TableRow>
                <TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">
                  클러스터 목록 조회 실패 — MNT-PAAS 응답 오류.
                </TableCell>
              </TableRow>
            )}
            {!clusters.isError && items.map((c) => (
              <ClusterRow
                key={c.id}
                c={c}
                projectName={projName(c.projectId)}
                accel={flavorAccel.get(c.flavorId) ?? null}
                onDetail={() => setDetailId(c.id)}
                isAdmin={isAdmin}
                deleting={del.isPending}
                onDelete={() => handleDelete(c)}
              />
            ))}
            {!clusters.isError && items.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">
                  서비스 클러스터가 없습니다. 클러스터가 필요하면 자원 요청 메뉴에서 신청하세요.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      {detailId && (
        <ClusterDetailDialog
          id={detailId}
          isAdmin={isAdmin}
          deleting={del.isPending}
          onDelete={(c) => handleDelete(c, () => setDetailId(null))}
          onClose={() => { setDetailId(null); if (openId) router.replace("/paas/clusters/"); }}
        />
      )}
    </div>
  );
}

function ClusterRow({
  c, projectName, accel, onDetail, isAdmin, deleting, onDelete,
}: {
  c: ClusterSummary; projectName: string; accel: string | null; onDetail: () => void;
  isAdmin: boolean; deleting: boolean; onDelete: () => void;
}) {
  const ready = c.status.endsWith("_COMPLETE") && !!c.apiAddress;
  return (
    <TableRow className="cursor-pointer" onClick={onDetail}>
      <TableCell className="text-xs font-medium">{projectName}</TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <Boxes className="size-3.5 text-brand-indigo-600" />
          <span className="max-w-[200px] truncate font-mono text-sm font-semibold" title={c.name}>{c.name}</span>
        </div>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-1.5">
          <Badge variant={stVariant(c.status)} className="text-[10px]">
            {c.status.endsWith("_IN_PROGRESS") && <Loader2 className="size-3 animate-spin" />}
            {c.status}
          </Badge>
          {c.healthStatus && c.healthStatus !== "HEALTHY" && (
            <Badge variant="outline" className="text-[10px]">{c.healthStatus}</Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="font-mono text-xs">{c.k8sVersion || "—"}</TableCell>
      <TableCell className="font-mono text-xs">{c.masterCount} + {c.nodeCount}</TableCell>
      <TableCell>
        <span className="font-mono text-xs">{c.flavorId}</span>
        {accel && <Badge variant="default" className="ml-1.5 font-mono text-[10px]">{accel}</Badge>}
      </TableCell>
      <TableCell className="font-mono text-[11px] text-muted-foreground">{fmtDateTime(c.createdAt)}</TableCell>
      <TableCell onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-end gap-0.5">
          {ready ? (
            <Button variant="ghost" size="icon-sm" title="kubeconfig 다운로드" onClick={() => saveKubeconfig(c.id, c.name)}>
              <Download className="size-3.5" />
            </Button>
          ) : (
            <Button variant="ghost" size="icon-sm" disabled title="클러스터가 준비되면 받을 수 있습니다">
              <Download className="size-3.5" />
            </Button>
          )}
          {isAdmin && (
            <Button
              variant="ghost" size="icon-sm" title="클러스터 삭제"
              disabled={deleting || c.status.startsWith("DELETE_")}
              onClick={onDelete}
            >
              <Trash2 className="size-3.5 text-rose-600" />
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

function ClusterDetailDialog({
  id, isAdmin, deleting, onDelete, onClose,
}: {
  id: string; isAdmin: boolean; deleting: boolean;
  onDelete: (c: { id: string; name: string }) => void; onClose: () => void;
}) {
  const { data: d, isLoading, isError } = useCluster(id);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      {/* grid 자식이 min-content 로 모달을 뚫지 않게 max-h+세로 스크롤, 노드풀 표 여유폭 3xl */}
      <DialogContent className="max-h-[85vh] max-w-3xl overflow-y-auto">
        {isLoading || !d ? (
          isError ? (
            <div className="py-10 text-center text-sm text-muted-foreground">상세 조회 실패</div>
          ) : (
            <Skeleton className="h-72" />
          )
        ) : (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <Boxes className="size-4 text-brand-indigo-600" />
                <span className="font-mono">{d.name}</span>
                <Badge variant={stVariant(d.status)} className="text-[10px]">{d.status}</Badge>
                {d.healthStatus && (
                  <Badge variant={d.healthStatus === "HEALTHY" ? "success" : "outline"} className="text-[10px]">
                    {d.healthStatus}
                  </Badge>
                )}
              </DialogTitle>
              <DialogDescription className="font-mono text-xs">{d.id}</DialogDescription>
            </DialogHeader>

            <Card className="min-w-0 space-y-2.5 p-4 text-xs">
              <Field label="K8s 버전" value={d.k8sVersion || "—"} mono />
              <Field label="API 주소" value={d.apiAddress || "—"} mono />
              <Field label="생성일" value={fmtDateTime(d.createdAt)} mono />
              <Field label="변경일" value={fmtDateTime(d.updatedAt)} mono />
              {d.statusReason && <Field label="상태 사유" value={d.statusReason} />}
              {Object.keys(d.healthReason).length > 0 && (
                <div className="flex items-start justify-between gap-3">
                  <span className="shrink-0 text-muted-foreground">헬스 체크</span>
                  <div className="flex flex-wrap justify-end gap-1">
                    {Object.entries(d.healthReason).map(([k, v]) => (
                      <Badge key={k} variant={v === "Ready" ? "success" : "outline"} className="font-mono text-[10px]">
                        {k}: {v}
                      </Badge>
                    ))}
                  </div>
                </div>
              )}
            </Card>

            <NodegroupSection clusterId={d.id} status={d.status} nodegroups={d.nodegroups} isAdmin={isAdmin} />

            {isAdmin && <ClusterAccessSection clusterId={d.id} />}

            <div className="flex items-center justify-between">
              {isAdmin ? (
                <Button
                  variant="destructive" size="sm"
                  disabled={deleting || d.status.startsWith("DELETE_")}
                  onClick={() => onDelete({ id: d.id, name: d.name })}
                >
                  <Trash2 className="size-3.5" /> 클러스터 삭제
                </Button>
              ) : <span />}
              {d.apiAddress ? (
                <Button size="sm" onClick={() => saveKubeconfig(d.id, d.name)}>
                  <Download className="size-3.5" /> kubeconfig 다운로드
                </Button>
              ) : (
                <Button size="sm" disabled>
                  <Download className="size-3.5" /> kubeconfig 다운로드
                </Button>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ── 노드그룹(노드풀) — 조회는 전체, 추가·크기 조정·삭제는 관리자(Magnum 비동기 — UPDATE_IN_PROGRESS 뒤 반영) ──
function NodegroupSection({
  clusterId, status, nodegroups, isAdmin,
}: {
  clusterId: string; status: string; nodegroups: ClusterNodegroup[]; isAdmin: boolean;
}) {
  const [addOpen, setAddOpen] = React.useState(false);
  const [resizeTarget, setResizeTarget] = React.useState<ClusterNodegroup | null>(null);
  const delNg = useDeleteNodegroup(clusterId);
  const confirm = useConfirm();
  // 클러스터가 작업 중(*_IN_PROGRESS)이면 Magnum 이 변경을 거부하므로 버튼을 잠근다
  const busy = status.endsWith("_IN_PROGRESS") || delNg.isPending;

  async function handleDelete(g: ClusterNodegroup) {
    if (!(await confirm({
      title: "노드그룹 삭제", destructive: true, confirmLabel: "삭제",
      description: `${g.name} 노드그룹과 그 노드 ${g.nodeCount}대를 삭제할까요? 노드에서 실행 중인 파드는 다른 노드로 밀려나거나 중단됩니다.`,
    }))) return;
    delNg.mutate(g.name, {
      onSuccess: () => toast.success(`${g.name} 삭제 요청됨 — 반영까지 시간이 걸립니다`),
      onError: (e) => toast.error("노드그룹 삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="min-w-0">
      <div className="mb-1.5 flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">노드풀</span>
        {isAdmin && (
          <Button variant="outline" size="sm" className="h-7" disabled={busy} onClick={() => setAddOpen(true)}>
            <Plus className="size-3.5" /> 노드그룹 추가
          </Button>
        )}
      </div>
      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>이름</TableHead>
              <TableHead>역할</TableHead>
              <TableHead>노드 수</TableHead>
              <TableHead>Flavor</TableHead>
              <TableHead>이미지</TableHead>
              <TableHead>상태</TableHead>
              {isAdmin && <TableHead className="w-20" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {nodegroups.map((g) => (
              <TableRow key={g.id}>
                <TableCell className="text-xs font-medium">{g.name}</TableCell>
                <TableCell>
                  <Badge variant={g.role === "master" ? "secondary" : "outline"} className="text-[10px]">{g.role === "master" ? "Control Plane" : g.role === "worker" ? "Worker" : g.role}</Badge>
                </TableCell>
                <TableCell className="font-mono text-xs">{g.nodeCount}</TableCell>
                <TableCell className="font-mono text-xs">{g.flavorId}</TableCell>
                <TableCell className="break-all font-mono text-[11px] text-muted-foreground">{g.imageId}</TableCell>
                <TableCell>
                  <Badge variant={stVariant(g.status)} className="text-[10px]">{g.status}</Badge>
                </TableCell>
                {isAdmin && (
                  <TableCell>
                    <div className="flex items-center gap-0.5">
                      {g.role !== "master" && (
                        <Button variant="ghost" size="icon-sm" title="노드 수 조정" disabled={busy} onClick={() => setResizeTarget(g)}>
                          <Scale className="size-3.5" />
                        </Button>
                      )}
                      {!g.isDefault && (
                        <Button variant="ghost" size="icon-sm" title="노드그룹 삭제" disabled={busy} onClick={() => handleDelete(g)}>
                          <Trash2 className="size-3.5 text-rose-600" />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      {addOpen && <AddNodegroupDialog clusterId={clusterId} existing={nodegroups.map((g) => g.name)} onClose={() => setAddOpen(false)} />}
      {resizeTarget && <ResizeNodegroupDialog clusterId={clusterId} g={resizeTarget} onClose={() => setResizeTarget(null)} />}
    </div>
  );
}

function AddNodegroupDialog({ clusterId, existing, onClose }: { clusterId: string; existing: string[]; onClose: () => void }) {
  const create = useCreateNodegroup(clusterId);
  const flavors = useFlavors();
  const [name, setName] = React.useState("");
  const [flavorId, setFlavorId] = React.useState("default");
  const [nodeCount, setNodeCount] = React.useState("1");
  const dup = existing.includes(name.trim());
  const submit = () => create.mutate(
    {
      name: name.trim(), nodeCount: Math.max(1, Number(nodeCount) || 1),
      ...(flavorId !== "default" ? { flavorId } : {}),
    },
    {
      onSuccess: () => { toast.success(`${name.trim()} 노드그룹 추가 요청됨 — 반영까지 시간이 걸립니다`); onClose(); },
      onError: (e) => toast.error("노드그룹 추가 실패", { description: String((e as Error).message) }),
    },
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>노드그룹 추가</DialogTitle>
          <DialogDescription className="text-xs">워커 노드그룹을 추가합니다. 반영까지 몇 분 걸립니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-1">
          <div className="space-y-1.5">
            <Label>이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="gpu-pool" className="font-mono text-xs" />
            {dup && <p className="text-[11px] text-rose-600">같은 이름의 노드그룹이 이미 있습니다.</p>}
          </div>
          <div className="space-y-1.5">
            <Label>워커 Flavor</Label>
            <Select value={flavorId} onValueChange={setFlavorId}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="default">클러스터 기본 Flavor</SelectItem>
                {(flavors.data ?? []).map((f) => (
                  <SelectItem key={f.id} value={f.id} className="font-mono text-xs">
                    {f.name} — {f.vcpus}vCPU · {Math.round(f.ramMb / 1024)}GiB{f.accelerator ? ` · ${f.accelerator}` : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>노드 수</Label>
            <Input type="number" min={1} value={nodeCount} onChange={(e) => setNodeCount(e.target.value)} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button disabled={!name.trim() || dup || create.isPending} onClick={submit}>
            {create.isPending && <Loader2 className="size-3.5 animate-spin" />} 추가
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ResizeNodegroupDialog({ clusterId, g, onClose }: { clusterId: string; g: ClusterNodegroup; onClose: () => void }) {
  const resize = useResizeNodegroup(clusterId);
  const [nodeCount, setNodeCount] = React.useState(String(g.nodeCount));
  const submit = () => resize.mutate(
    { nodegroup: g.name, nodeCount: Math.max(1, Number(nodeCount) || 1) },
    {
      onSuccess: () => { toast.success(`${g.name} → ${nodeCount}대 조정 요청됨 — 반영까지 시간이 걸립니다`); onClose(); },
      onError: (e) => toast.error("노드 수 조정 실패", { description: String((e as Error).message) }),
    },
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>노드 수 조정</DialogTitle>
          <DialogDescription className="font-mono text-xs">{g.name} · 현재 {g.nodeCount}대</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 py-1">
          <Label>노드 수</Label>
          <Input type="number" min={1} value={nodeCount} onChange={(e) => setNodeCount(e.target.value)} />
          <p className="text-[11px] text-muted-foreground">줄이면 Magnum 이 고른 노드가 제거됩니다 — 실행 중인 파드는 다른 노드로 밀려납니다.</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button disabled={resize.isPending || Number(nodeCount) === g.nodeCount} onClick={submit}>
            {resize.isPending && <Loader2 className="size-3.5 animate-spin" />} 적용
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── 접근 프로젝트 관리(관리자 전용) — 이 클러스터를 어떤 프로젝트가 볼 수 있나(paas_project_cluster) ──
function ClusterAccessSection({ clusterId }: { clusterId: string }) {
  const access = useClusterAccess(clusterId);
  const projects = useProjects();
  const grant = useGrantClusterAccess(clusterId);
  const revoke = useRevokeClusterAccess(clusterId);
  const confirm = useConfirm();
  const [projectId, setProjectId] = React.useState("");
  const [namespace, setNamespace] = React.useState("");

  const projName = (id: string) => (projects.data?.items ?? []).find((p) => p.id === id)?.name || id;
  // 이미 (전체 접근으로) 부여된 프로젝트는 picker 에서 제외
  const granted = new Set((access.data ?? []).filter((a) => !a.namespace).map((a) => a.projectId));
  const options = (projects.data?.items ?? []).filter((p) => !granted.has(p.id));

  function add() {
    if (!projectId) return toast.error("프로젝트를 선택하세요");
    grant.mutate(
      { projectId, namespace: namespace.trim() || undefined },
      {
        onSuccess: () => { toast.success("접근 권한 부여됨"); setProjectId(""); setNamespace(""); },
        onError: (e) => toast.error("부여 실패", { description: String((e as Error).message) }),
      },
    );
  }
  async function remove(a: ClusterAccess) {
    if (!(await confirm({
      title: "접근 권한 회수", destructive: true, confirmLabel: "회수",
      description: `${projName(a.projectId)}${a.namespace ? ` (네임스페이스 ${a.namespace})` : ""} 의 이 클러스터 접근을 회수할까요? 해당 프로젝트 사용자는 더 이상 이 클러스터를 보지 못합니다.`,
    }))) return;
    revoke.mutate({ projectId: a.projectId, namespace: a.namespace ?? undefined }, {
      onSuccess: () => toast.success("회수됨"),
      onError: (e) => toast.error("회수 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="min-w-0">
      <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Users className="size-3.5" /> 접근 프로젝트 <span className="font-normal">— 이 클러스터를 볼 수 있는 프로젝트(관리자 설정)</span>
      </div>
      <Card className="space-y-3 p-3">
        {/* 추가 */}
        <div className="flex items-end gap-2">
          <div className="min-w-0 flex-1 space-y-1">
            <label className="text-[11px] text-muted-foreground">프로젝트</label>
            <Select value={projectId} onValueChange={setProjectId}>
              <SelectTrigger><SelectValue placeholder="프로젝트 선택" /></SelectTrigger>
              <SelectContent>
                {options.map((p) => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="w-44 space-y-1">
            <label className="text-[11px] text-muted-foreground">네임스페이스(선택·공유 시)</label>
            <Input value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="비우면 전체" className="font-mono text-xs" />
          </div>
          <Button size="sm" onClick={add} disabled={grant.isPending || !projectId}>
            {grant.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />} 추가
          </Button>
        </div>
        {/* 목록 */}
        <Table>
          <TableHeader>
            <TableRow className="[&>th]:whitespace-nowrap">
              <TableHead>프로젝트</TableHead><TableHead>네임스페이스</TableHead><TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(access.data ?? []).map((a) => (
              <TableRow key={`${a.projectId}:${a.namespace ?? ""}`}>
                <TableCell className="text-sm">{projName(a.projectId)}</TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">{a.namespace || "전체(전용)"}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon-sm" title="회수" disabled={revoke.isPending} onClick={() => remove(a)}>
                    <Trash2 className="size-3.5 text-rose-600" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {(access.data ?? []).length === 0 && (
              <TableRow><TableCell colSpan={3} className="py-6 text-center text-xs text-muted-foreground">접근 가능한 프로젝트가 없습니다. 추가하면 해당 프로젝트 사용자에게 이 클러스터가 보입니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className={mono ? "min-w-0 break-all text-right font-mono" : "min-w-0 break-words text-right"}>{value}</span>
    </div>
  );
}
