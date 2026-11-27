"use client";
import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Server, Play, Square, RotateCw, Trash2, Loader2, Plus, HardDrive, ArrowLeft,
  Cpu, MemoryStick, Network as NetIcon, Database, Gauge, SlidersHorizontal, MonitorPlay, ShieldCheck, X, Camera,
} from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { useAuth } from "@/lib/auth";
import {
  useServer, useServerDiagnostics, usePowerAction, useDeleteServer, useCreateServerSnapshot,
  useAttachVolume, useDetachVolume, useAttachInterface, useDetachInterface,
  useResizeServer, useResizeConfirm, useResizeRevert,
  useProjects, useVolumes, useImages, useNetworks, useFlavors,
  useDeviceProfiles, useAccelModels, accelLabelResolver,
  useProjectSecurityGroups, useAddServerSecurityGroup, useRemoveServerSecurityGroup,
  getServerConsoleUrl,
} from "@/lib/iaas-api";
import type { SecurityGroupRuleBrief } from "@/lib/types";

const SSH_USER = "ubuntu";
const STATUS_VARIANT: Record<string, "success" | "secondary" | "warning" | "outline"> = {
  ACTIVE: "success", SHUTOFF: "secondary", ERROR: "outline", PAUSED: "warning", SUSPENDED: "warning", VERIFY_RESIZE: "warning",
};
const stVariant = (s: string) => STATUS_VARIANT[s] ?? "warning";

// OpenStack 시각(UTC)을 브라우저 로컬 시간대로 변환해 "YYYY-MM-DD HH:mm" 표기.
function fmtDateTime(iso?: string | null) {
  if (!iso) return "—";
  const s = /[zZ]|[+-]\d\d:?\d\d$/.test(iso) ? iso : `${iso}Z`; // tz 표기 없으면 UTC로 간주
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export default function Page() {
  return <React.Suspense fallback={<Skeleton className="h-96" />}><ServerDetailPage /></React.Suspense>;
}

function ServerDetailPage() {
  const router = useRouter();
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const id = useSearchParams().get("id") ?? undefined;
  const { data: s, isLoading, isError } = useServer(id);
  const { data: projData } = useProjects();
  const dps = useDeviceProfiles();
  const accelModels = useAccelModels();
  const accelLabel = accelLabelResolver(dps.data, accelModels.data);
  const { data: images } = useImages();
  const { data: nets } = useNetworks();
  const { data: vols } = useVolumes();
  const power = usePowerAction();
  const del = useDeleteServer();
  const detach = useDetachVolume();
  const detachNic = useDetachInterface();
  const rzConfirm = useResizeConfirm();
  const rzRevert = useResizeRevert();
  const confirm = useConfirm();
  const [attachVolOpen, setAttachVolOpen] = React.useState(false);
  const [attachNetOpen, setAttachNetOpen] = React.useState(false);
  const [resizeOpen, setResizeOpen] = React.useState(false);
  const [snapshotOpen, setSnapshotOpen] = React.useState(false);

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (pid: string) => m[pid] || pid.slice(0, 8) || "—";
  }, [projData]);
  const netName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const n of nets?.items ?? []) m[n.id] = n.name;
    return (nid: string) => m[nid] || nid.slice(0, 8);
  }, [nets]);
  const volInfo = React.useMemo(() => {
    const m: Record<string, { name: string; size: number; type: string }> = {};
    for (const v of vols ?? []) m[v.id] = { name: v.name, size: v.size, type: v.type };
    return (vid: string) => m[vid];
  }, [vols]);

  if (!id) return <Card className="p-6 text-sm text-muted-foreground">가상서버 ID가 없습니다.</Card>;
  if (isLoading || (!s && !isError)) return <Skeleton className="h-96" />;
  if (isError || !s) return <Card className="p-6 text-sm text-rose-600">가상서버를 찾을 수 없습니다.</Card>;

  const busy = power.isPending || del.isPending || detach.isPending || detachNic.isPending;
  // 볼륨 부팅 VM 은 부팅 볼륨의 원본 이미지 이름을 함께 표시 — "(볼륨 부팅)"만 보이던 것 보완(ETRI 검토의견)
  const srcImage = s.imageId ? (images?.find((i) => i.id === s.imageId)?.name ?? s.imageName ?? s.imageId) : null;
  const imageName = s.bootVolumeId ? `${srcImage ?? ""} (볼륨 부팅)`.trim() : (srcImage ?? "(볼륨 부팅)");
  const sshIp = s.addresses.find((a) => a.type === "floating")?.ip ?? s.addresses[0]?.ip;
  const copy = (txt: string, label: string) => { navigator.clipboard.writeText(txt); toast.success(`${label} 복사됨`); };

  async function doPower(action: "start" | "stop" | "reboot") {
    const label = action === "start" ? "시작" : action === "stop" ? "정지" : "재부팅";
    if (!(await confirm({ title: `가상서버 ${label}`, destructive: action !== "start", confirmLabel: label, description: `${s!.name} 을(를) ${label}할까요?` }))) return;
    power.mutate({ id: s!.id, action }, { onSuccess: () => toast.success(`${s!.name} ${label} 요청됨`), onError: (e) => toast.error(`${label} 실패`, { description: String((e as Error).message) }) });
  }
  async function doDelete() {
    if (!(await confirm({ title: "가상서버 삭제", destructive: true, confirmLabel: "삭제", description: `${s!.name} 을(를) 삭제(terminate)할까요? 되돌릴 수 없습니다.` }))) return;
    del.mutate(s!.id, { onSuccess: () => { toast.success(`${s!.name} 삭제 요청됨`); router.push("/iaas/instances/"); }, onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }
  async function openConsole() {
    // 팝업 차단 회피: 클릭 즉시 빈 창을 열고, URL 받으면 그 창에 세팅
    const w = window.open("", "_blank");
    try {
      const url = await getServerConsoleUrl(s!.id);
      if (w) w.location.href = url;
      else window.open(url, "_blank");
      toast.success("웹 콘솔을 엽니다");
    } catch (e) {
      w?.close();
      toast.error("웹 콘솔 실패", { description: String((e as Error).message) });
    }
  }
  async function doDetachVol(volumeId: string) {
    if (!(await confirm({ title: "볼륨 분리", destructive: true, confirmLabel: "분리", description: "이 볼륨을 분리할까요?" }))) return;
    detach.mutate({ serverId: s!.id, volumeId }, { onSuccess: () => toast.success("볼륨 분리됨"), onError: (e) => toast.error("분리 실패", { description: String((e as Error).message) }) });
  }
  async function doDetachNic(portId: string, label: string) {
    if (!(await confirm({ title: "네트워크 분리", destructive: true, confirmLabel: "분리", description: `${label} 인터페이스를 분리할까요?` }))) return;
    detachNic.mutate({ serverId: s!.id, portId }, { onSuccess: () => toast.success("네트워크 분리됨"), onError: (e) => toast.error("분리 실패", { description: String((e as Error).message) }) });
  }

  return (
    <div className="space-y-4">
      {/* 헤더 */}
      <div className="flex items-center gap-3">
        <Button variant="outline" size="icon-sm" onClick={() => router.push("/iaas/instances/")} title="목록"><ArrowLeft className="size-4" /></Button>
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700"><Server className="size-5" /></div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="break-all font-mono text-lg font-semibold">{s.name}</h1>
            <Badge variant={stVariant(s.status)} className="shrink-0 text-[10px]">{s.status}</Badge>
          </div>
          <div className="truncate font-mono text-xs text-muted-foreground">{s.id} · 프로젝트 {projName(s.tenantId)}</div>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={openConsole} title="noVNC 웹 콘솔 새 탭으로 열기"><MonitorPlay className="size-3.5" /> 웹 콘솔</Button>
          <Button variant="outline" size="sm" disabled={busy} onClick={() => setSnapshotOpen(true)} title="현재 상태를 이미지로 저장 — 그 이미지로 새 가상서버를 만들 수 있습니다"><Camera className="size-3.5" /> 스냅샷</Button>
          {isAdmin && <Button variant="outline" size="sm" disabled={busy} onClick={() => setResizeOpen(true)}><SlidersHorizontal className="size-3.5" /> 스펙 변경</Button>}
          {s.status === "SHUTOFF" ? (
            <Button variant="outline" size="sm" disabled={busy} onClick={() => doPower("start")}><Play className="size-3.5" /> 시작</Button>
          ) : (
            <Button variant="outline" size="sm" disabled={busy} onClick={() => doPower("stop")}><Square className="size-3.5" /> 정지</Button>
          )}
          <Button variant="outline" size="sm" disabled={busy} onClick={() => doPower("reboot")}><RotateCw className="size-3.5" /> 재부팅</Button>
          {isAdmin && <Button variant="destructive" size="sm" disabled={busy} onClick={doDelete}><Trash2 className="size-3.5" /> 삭제</Button>}
        </div>
      </div>

      {/* 스펙 변경 확정 대기 배너 (resize는 관리자 전용) */}
      {isAdmin && s.status === "VERIFY_RESIZE" && (
        <Card className="flex items-center gap-3 border-amber-300 bg-amber-50 p-3 text-sm text-amber-900">
          <Gauge className="size-4 shrink-0" />
          <span className="flex-1">스펙 변경이 적용되었습니다. <strong>확정</strong>하거나 <strong>되돌리기</strong>를 선택하세요.</span>
          <Button size="sm" onClick={() => rzConfirm.mutate(s.id, { onSuccess: () => toast.success("스펙 변경 확정됨"), onError: (e) => toast.error("확정 실패", { description: String((e as Error).message) }) })} disabled={rzConfirm.isPending}>확정</Button>
          <Button size="sm" variant="outline" onClick={() => rzRevert.mutate(s.id, { onSuccess: () => toast.success("되돌렸습니다"), onError: (e) => toast.error("되돌리기 실패", { description: String((e as Error).message) }) })} disabled={rzRevert.isPending}>되돌리기</Button>
        </Card>
      )}

      {/* 상단 스탯 타일 */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatTile icon={Cpu} label="사양" value={`vCPU ${s.vcpus}`} sub={`RAM ${Math.round(s.ramMb / 1024)}GB`} />
        <StatTile icon={Gauge} label="플레이버" value={s.flavorName} mono />
        <StatTile icon={Server} label="가속기" value={s.accelerator ? (accelLabel(s.accelerator) ?? s.accelerator) : "없음"} mono />
        <StatTile icon={Database} label="호스트" value={s.host || "—"} mono />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* 정보 */}
        <Card className="p-4">
          <div className="mb-3 text-sm font-semibold">정보</div>
          <div className="grid grid-cols-2 gap-x-6 gap-y-3">
            <Field label="상태" value={`${s.status}${s.vmState ? ` · ${s.vmState}` : ""}`} />
            <Field label="프로젝트" value={projName(s.tenantId)} />
            <Field label="이미지" value={imageName} mono />
            <Field label="키페어" value={s.keyName || "—"} mono />
            <Field label="생성일" value={fmtDateTime(s.createdAt)} mono />
          </div>
          {sshIp && (
            <div className="mt-4 space-y-1.5 border-t border-border pt-3">
              <div className="text-[11px] font-semibold text-muted-foreground">SSH 접속</div>
              <button type="button" onClick={() => copy(`ssh ${SSH_USER}@${sshIp}${s.keyName ? ` -i ${s.keyName}.pem` : ""}`, "SSH 명령")}
                className="w-full cursor-pointer truncate rounded-md bg-muted/50 px-2 py-1.5 text-left font-mono text-[11px] hover:bg-muted" title="클릭하여 복사">
                ssh {SSH_USER}@{sshIp}{s.keyName ? ` -i ${s.keyName}.pem` : ""}
              </button>
            </div>
          )}
        </Card>

        {/* 네트워크 + 볼륨 */}
        <div className="space-y-4">
          {/* 네트워크 */}
          <Card className="p-4">
            <div className="mb-2 flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold"><NetIcon className="size-4 text-sky-600" /> 네트워크 ({s.interfaces.length})</div>
              <Button variant="outline" size="sm" onClick={() => setAttachNetOpen(true)}><Plus className="size-3.5" /> 연결</Button>
            </div>
            {s.interfaces.length === 0 ? (
              <div className="p-3 text-center text-xs text-muted-foreground">연결된 네트워크가 없습니다.</div>
            ) : (
              <div className="space-y-1.5">
                {s.interfaces.map((i) => (
                  <div
                    key={i.portId}
                    onClick={() => router.push(`/iaas/networks/?detail=${i.netId}`)}
                    className="flex cursor-pointer items-center gap-2 rounded-md border border-border px-3 py-2 text-xs transition-colors hover:bg-secondary/60"
                    title="네트워크 상세 열기"
                  >
                    <NetIcon className="size-3.5 shrink-0 text-sky-600" />
                    <span className="font-medium underline-offset-2 hover:underline">{netName(i.netId)}</span>
                    <span className="font-mono text-muted-foreground">{i.ips.join(", ")}</span>
                    <Button variant="ghost" size="icon-sm" className="ml-auto" title="분리" disabled={busy} onClick={(e) => { e.stopPropagation(); doDetachNic(i.portId, netName(i.netId)); }}>
                      <Trash2 className="size-3.5 text-rose-600" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </Card>

          {/* 볼륨 */}
          <Card className="p-4">
            <div className="mb-2 flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold"><HardDrive className="size-4 text-brand-indigo-600" /> 볼륨 ({s.attachedVolumes.length})</div>
              <Button variant="outline" size="sm" onClick={() => setAttachVolOpen(true)}><Plus className="size-3.5" /> 연결</Button>
            </div>
            {s.attachedVolumes.length === 0 ? (
              <div className="p-3 text-center text-xs text-muted-foreground">연결된 볼륨이 없습니다.</div>
            ) : (
              <div className="space-y-1.5">
                {s.attachedVolumes.map((v) => {
                  const info = volInfo(v.volumeId);
                  return (
                    <div
                      key={v.volumeId}
                      onClick={() => router.push(`/iaas/volumes/?detail=${v.volumeId}`)}
                      className="flex cursor-pointer items-center gap-2 rounded-md border border-border px-3 py-2 text-xs transition-colors hover:bg-secondary/60"
                      title="볼륨 상세 열기"
                    >
                      <HardDrive className="size-3.5 shrink-0 text-brand-indigo-600" />
                      <span className="font-mono font-medium underline-offset-2 hover:underline">{info?.name ?? v.volumeId.slice(0, 8)}</span>
                      {info && <Badge variant="outline" className="text-[10px]">{info.size}GB · {info.type}</Badge>}
                      <span className="font-mono text-muted-foreground">{v.device}</span>
                      <Button variant="ghost" size="icon-sm" className="ml-auto" title="분리" disabled={busy} onClick={(e) => { e.stopPropagation(); doDetachVol(v.volumeId); }}>
                        <Trash2 className="size-3.5 text-rose-600" />
                      </Button>
                    </div>
                  );
                })}
              </div>
            )}
          </Card>

          {/* 보안그룹 — 서버에 직접 추가/제거(Nova). 클러스터 노드 접속 등에서 별도 SG 할당용 */}
          <SecurityGroupsCard serverId={s.id} projectId={s.tenantId} current={s.securityGroups} busy={busy} />
        </div>
      </div>

      {/* 실시간 메트릭 — 맨 아래(상태 변화로 위 카드가 흔들리지 않게) */}
      <MetricsPanel serverId={s.id} active={s.status === "ACTIVE"} ramMb={s.ramMb} />

      <AttachVolumeDialog serverId={s.id} projectId={s.tenantId} attached={new Set(s.attachedVolumes.map((v) => v.volumeId))} open={attachVolOpen} onClose={() => setAttachVolOpen(false)} />
      <AttachNetworkDialog serverId={s.id} projectId={s.tenantId} attached={new Set(s.interfaces.map((i) => i.netId))} open={attachNetOpen} onClose={() => setAttachNetOpen(false)} />
      {isAdmin && <ResizeDialog serverId={s.id} currentFlavor={s.flavorName} open={resizeOpen} onClose={() => setResizeOpen(false)} />}
      {/* open 일 때만 마운트 — 열릴 때마다 이름이 서버명 기본값으로 리셋된다(effect 불필요) */}
      {snapshotOpen && <SnapshotDialog serverId={s.id} serverName={s.name} onClose={() => setSnapshotOpen(false)} />}
    </div>
  );
}

function SnapshotDialog({ serverId, serverName, onClose }: { serverId: string; serverName: string; onClose: () => void }) {
  const snap = useCreateServerSnapshot();
  const [name, setName] = React.useState(`${serverName}-snapshot`);
  const submit = () => snap.mutate(
    { id: serverId, name: name.trim() },
    {
      onSuccess: () => { toast.success("스냅샷 생성 요청됨 — 이미지 메뉴에서 확인할 수 있습니다", { description: "완료까지 수 분 걸릴 수 있습니다" }); onClose(); },
      onError: (e) => toast.error("스냅샷 생성 실패", { description: String((e as Error).message) }),
    },
  );
  return (
    <Dialog open onOpenChange={(o) => !o && !snap.isPending && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Camera className="size-4 text-brand-indigo-600" /> 스냅샷 생성</DialogTitle>
          <DialogDescription className="text-xs">현재 가상서버 상태를 이미지로 저장합니다. 그 이미지로 같은 상태의 가상서버를 새로 만들 수 있습니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 py-1">
          <Label>스냅샷(이미지) 이름</Label>
          <Input value={name} onChange={(e) => setName(e.target.value)} className="font-mono text-xs" />
          <p className="text-[11px] text-muted-foreground">실행 중에도 생성되지만, 디스크 정합성을 위해 정지 상태를 권장합니다.</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={snap.isPending}>취소</Button>
          <Button onClick={submit} disabled={!name.trim() || snap.isPending}>
            {snap.isPending && <Loader2 className="size-3.5 animate-spin" />} 생성
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// 규칙 요약 한 줄 — 예: "인바운드 TCP 22 · 0.0.0.0/0"
function fmtSgRule(r: SecurityGroupRuleBrief): string {
  const dir = r.direction === "ingress" ? "인바운드" : "아웃바운드";
  const proto = (r.protocol || "any").toUpperCase();
  const port = r.portMin == null ? "전체" : r.portMin === r.portMax ? `${r.portMin}` : `${r.portMin}-${r.portMax}`;
  const src = r.remoteCidr || (r.remoteGroup ? "보안그룹" : "모두");
  return `${dir} ${proto} ${port} · ${src}`;
}

// 규칙 전부를 한 줄씩 표시(생략 없음)
function SgRuleLines({ rules }: { rules?: SecurityGroupRuleBrief[] }) {
  if (!rules || rules.length === 0) return <span className="text-[10px] text-muted-foreground">규칙 없음</span>;
  return (
    <div className="mt-1 flex flex-col gap-0.5">
      {rules.map((r, i) => <span key={i} className="font-mono text-[10px] text-muted-foreground">· {fmtSgRule(r)}</span>)}
    </div>
  );
}

// 가상서버 보안그룹 편집 — 현재 할당된 SG 배지(제거) + '추가'로 목록 다이얼로그(정보 확인 후 선택).
function SecurityGroupsCard({ serverId, projectId, current, busy }: { serverId: string; projectId: string; current: string[]; busy: boolean }) {
  const { data: sgs } = useProjectSecurityGroups(projectId);
  const remove = useRemoveServerSecurityGroup();
  const confirm = useConfirm();
  const [addOpen, setAddOpen] = React.useState(false);
  const pending = remove.isPending || busy;
  const byName = new Map((sgs ?? []).map((g) => [g.name, g])); // 이름 → 보안그룹(규칙 정보)
  const names = Array.from(new Set(current)); // Nova 가 NIC 마다 같은 SG 를 중복으로 주므로 중복 제거

  async function doRemove(name: string) {
    if (!(await confirm({ title: "보안그룹 제거", destructive: true, confirmLabel: "제거", description: `${name} 을(를) 이 가상서버에서 제거할까요?` }))) return;
    remove.mutate({ serverId, name }, {
      onSuccess: () => toast.success(`보안그룹 ${name} 제거됨`),
      onError: (e) => toast.error("보안그룹 제거 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Card className="p-4">
      <div className="mb-2 flex items-center justify-between">
        <div className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="size-4 text-emerald-600" /> 보안그룹 ({names.length})</div>
        <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}><Plus className="size-3.5" /> 추가</Button>
      </div>
      {names.length === 0 ? (
        <div className="p-3 text-center text-xs text-muted-foreground">할당된 보안그룹이 없습니다.</div>
      ) : (
        <div className="space-y-1.5">
          {names.map((name) => {
            const g = byName.get(name);
            return (
              <div key={name} className="rounded-md border border-border p-2.5">
                <div className="flex items-center gap-2">
                  <ShieldCheck className="size-3.5 shrink-0 text-emerald-600" />
                  <span className="truncate font-mono text-xs font-semibold">{name}</span>
                  {g && <Badge variant="secondary" className="shrink-0 text-[10px]">규칙 {g.rules ?? g.ruleList?.length ?? 0}</Badge>}
                  <Button variant="ghost" size="icon-sm" className="ml-auto shrink-0" title="제거" disabled={pending} onClick={() => doRemove(name)}>
                    <X className="size-3.5 text-rose-600" />
                  </Button>
                </div>
                {g
                  ? <SgRuleLines rules={g.ruleList} />
                  : <span className="mt-1 block text-[10px] text-muted-foreground">이 프로젝트에서 규칙 정보를 가져올 수 없는 보안그룹입니다(예: 클러스터 전용).</span>}
              </div>
            );
          })}
        </div>
      )}
      <AttachSecurityGroupDialog serverId={serverId} projectId={projectId} attached={new Set(current)} open={addOpen} onClose={() => setAddOpen(false)} />
    </Card>
  );
}

// 보안그룹 추가 다이얼로그 — 프로젝트 보안그룹 목록(이름·설명·규칙 요약)에서 골라 추가. 네트워크/볼륨 연결과 동일 방식.
function AttachSecurityGroupDialog({ serverId, projectId, attached, open, onClose }: { serverId: string; projectId: string; attached: Set<string>; open: boolean; onClose: () => void }) {
  const { data: sgs, isLoading } = useProjectSecurityGroups(projectId);
  const add = useAddServerSecurityGroup();
  const candidates = (sgs ?? []).filter((g) => !attached.has(g.name));
  function pick(name: string) {
    add.mutate({ serverId, name }, { onSuccess: () => { toast.success(`보안그룹 ${name} 추가됨`); onClose(); }, onError: (e) => toast.error("보안그룹 추가 실패", { description: String((e as Error).message) }) });
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !add.isPending && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>보안그룹 추가</DialogTitle><DialogDescription>이 가상서버에 추가할 보안그룹을 선택하세요. 이미 할당된 것은 제외됩니다.</DialogDescription></DialogHeader>
        <div className="max-h-[55vh] space-y-2 overflow-y-auto py-1">
          {isLoading ? (<div className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 조회 중…</div>)
            : candidates.length === 0 ? (<Card className="p-6 text-center text-sm text-muted-foreground">추가할 수 있는 보안그룹이 없습니다.</Card>)
            : candidates.map((g) => (
              <button key={g.id} type="button" disabled={add.isPending} onClick={() => pick(g.name)}
                className="group flex w-full cursor-pointer items-start gap-3 rounded-lg border border-border p-3 text-left transition-colors hover:border-emerald-300 hover:bg-emerald-50/40 disabled:opacity-50">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-emerald-100 text-emerald-700"><ShieldCheck className="size-4" /></div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-mono text-sm font-semibold">{g.name}</span>
                    <Badge variant="secondary" className="shrink-0 text-[10px]">규칙 {g.rules ?? g.ruleList?.length ?? 0}</Badge>
                  </div>
                  {g.description && <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{g.description}</div>}
                  <SgRuleLines rules={g.ruleList} />
                </div>
                <Plus className="size-4 shrink-0 text-muted-foreground group-hover:text-emerald-600" />
              </button>
            ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function StatTile({ icon: Icon, label, value, sub, mono }: { icon: React.ComponentType<{ className?: string }>; label: string; value: string; sub?: string; mono?: boolean }) {
  return (
    <Card className="p-3">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><Icon className="size-3.5" /> {label}</div>
      <div className={`mt-1 truncate text-sm font-semibold ${mono ? "font-mono" : ""}`} title={value}>{value}</div>
      {sub && <div className="truncate text-[11px] text-muted-foreground">{sub}</div>}
    </Card>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0 space-y-0.5">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className={`break-words text-sm ${mono ? "font-mono" : ""}`}>{value}</div>
    </div>
  );
}

// ── 실시간 메트릭 ──
function MetricsPanel({ serverId, active, ramMb }: { serverId: string; active: boolean; ramMb: number }) {
  const { data: diag, isError } = useServerDiagnostics(serverId, active);
  const prev = React.useRef<{ t: number; cpu: number; rx: number; tx: number; dr: number; dw: number } | null>(null);
  const [m, setM] = React.useState<{ cpu: number; rx: number; tx: number; dr: number; dw: number; uptime: number } | null>(null);
  const [spark, setSpark] = React.useState<number[]>([]);

  React.useEffect(() => {
    if (!diag) return;
    const now = Date.now();
    const p = prev.current;
    let cpu = 0, rx = 0, tx = 0, dr = 0, dw = 0;
    if (p && now > p.t) {
      const dtS = (now - p.t) / 1000;
      const cpuPct = ((diag.cpuTimeNs - p.cpu) / (dtS * 1e9 * Math.max(1, diag.numCpus))) * 100;
      cpu = Math.max(0, Math.min(100, Math.round(cpuPct * 10) / 10));
      rx = Math.max(0, (diag.nicRxBytes - p.rx) / dtS);
      tx = Math.max(0, (diag.nicTxBytes - p.tx) / dtS);
      dr = Math.max(0, (diag.diskReadBytes - p.dr) / dtS);
      dw = Math.max(0, (diag.diskWriteBytes - p.dw) / dtS);
    }
    prev.current = { t: now, cpu: diag.cpuTimeNs, rx: diag.nicRxBytes, tx: diag.nicTxBytes, dr: diag.diskReadBytes, dw: diag.diskWriteBytes };
    if (p) { setM({ cpu, rx, tx, dr, dw, uptime: diag.uptime }); setSpark((x) => [...x.slice(-29), cpu]); }
  }, [diag]);

  if (!active) return <Card className="p-4 text-sm text-muted-foreground">실행 중(ACTIVE)이 아니라 메트릭이 없습니다.</Card>;
  if (isError) return <Card className="p-4 text-sm text-muted-foreground">메트릭을 가져올 수 없습니다(진단 미지원/권한).</Card>;
  if (!m) return <Card className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 메트릭 수집 중… (3초 샘플)</Card>;

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm font-semibold">실시간 메트릭</div>
        <Badge variant="ghost" className="font-mono text-[10px]"><span className="mr-1 inline-block size-1.5 animate-pulse rounded-full bg-brand-mint-500" />live · 3s</Badge>
      </div>
      <div className="grid gap-4 md:grid-cols-4">
        <Metric icon={Cpu} label="CPU" value={`${m.cpu.toFixed(1)}%`} pct={m.cpu} spark={spark} />
        <Metric icon={MemoryStick} label="메모리 (할당)" value={`${(ramMb / 1024).toFixed(ramMb % 1024 ? 1 : 0)}GB`} sub="사용량은 OBS 연동 시" />
        <Metric icon={NetIcon} label="네트워크" value={`↓${fmtRate(m.rx)}`} sub={`↑${fmtRate(m.tx)}`} />
        <Metric icon={Database} label="디스크 IO" value={`R ${fmtRate(m.dr)}`} sub={`W ${fmtRate(m.dw)}`} />
      </div>
      <div className="mt-2 text-[10px] text-muted-foreground">uptime {fmtUptime(m.uptime)} · 하이퍼바이저 진단 기반 샘플 델타. 시계열 차트는 옵저버빌리티(Prometheus) 연동 시.</div>
    </Card>
  );
}

function Metric({ icon: Icon, label, value, sub, pct, spark }: { icon: React.ComponentType<{ className?: string }>; label: string; value: string; sub?: string; pct?: number; spark?: number[] }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><Icon className="size-3.5" /> {label}</div>
      <div className="font-mono text-lg font-semibold tabular-nums">{value}</div>
      {sub && <div className="font-mono text-[11px] text-muted-foreground">{sub}</div>}
      {pct !== undefined && <Progress value={pct} indicatorClassName={pct > 85 ? "bg-rose-500" : pct > 60 ? "bg-amber-500" : "bg-brand-mint-500"} />}
      {spark && spark.length > 1 && <Sparkline data={spark} />}
    </div>
  );
}
function Sparkline({ data }: { data: number[] }) {
  const w = 100, h = 22, max = Math.max(10, ...data);
  const pts = data.map((v, i) => `${(i / (data.length - 1)) * w},${h - (v / max) * h}`).join(" ");
  return <svg viewBox={`0 0 ${w} ${h}`} className="h-5 w-full text-brand-indigo-400" preserveAspectRatio="none"><polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>;
}

function AttachVolumeDialog({ serverId, projectId, attached, open, onClose }: { serverId: string; projectId: string; attached: Set<string>; open: boolean; onClose: () => void }) {
  const { data: vols, isLoading } = useVolumes();
  const attach = useAttachVolume();
  const candidates = (vols ?? []).filter((v) => v.tenantId === projectId && v.status === "available" && !attached.has(v.id));
  function pick(volumeId: string) {
    attach.mutate({ serverId, volumeId }, { onSuccess: () => { toast.success("볼륨이 연결되었습니다"); onClose(); }, onError: (e) => toast.error("연결 실패", { description: String((e as Error).message) }) });
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !attach.isPending && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>볼륨 연결</DialogTitle><DialogDescription>같은 프로젝트의 available 볼륨만 연결할 수 있습니다.</DialogDescription></DialogHeader>
        <div className="max-h-[50vh] space-y-2 overflow-y-auto py-1">
          {isLoading ? (<div className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 조회 중…</div>)
            : candidates.length === 0 ? (<Card className="p-6 text-center text-sm text-muted-foreground">연결 가능한 볼륨이 없습니다.</Card>)
            : candidates.map((v) => (
              <button key={v.id} type="button" disabled={attach.isPending} onClick={() => pick(v.id)}
                className="group flex w-full cursor-pointer items-center gap-3 rounded-lg border border-border p-3 text-left transition-colors hover:border-brand-indigo-300 hover:bg-brand-indigo-50/40 disabled:opacity-50">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-brand-indigo-100 text-brand-indigo-700"><HardDrive className="size-4" /></div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-mono text-sm font-semibold" title={v.name}>{v.name}</span>
                    {v.bootable && <Badge variant="warning" className="shrink-0 text-[10px]">부팅 가능</Badge>}
                  </div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
                    <Badge variant="outline" className="text-[10px]">{v.size}GB</Badge>
                    <Badge variant="secondary" className="text-[10px]">{v.type}</Badge>
                    <span className="font-mono text-[10px] text-muted-foreground">{v.id.slice(0, 8)}</span>
                    <span className="text-[10px] text-muted-foreground">{fmtDateTime(v.createdAt)}</span>
                  </div>
                  {v.description && <div className="mt-0.5 truncate text-[11px] text-muted-foreground" title={v.description}>{v.description}</div>}
                </div>
                <Plus className="size-4 shrink-0 text-muted-foreground group-hover:text-brand-indigo-600" />
              </button>
            ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AttachNetworkDialog({ serverId, projectId, attached, open, onClose }: { serverId: string; projectId: string; attached: Set<string>; open: boolean; onClose: () => void }) {
  const { data: nets, isLoading } = useNetworks();
  const attach = useAttachInterface();
  // 같은 프로젝트 or 공유/외부 네트워크 중 아직 미연결
  const candidates = (nets?.items ?? []).filter((n) => (n.tenantId === projectId || n.type !== "tenant") && !attached.has(n.id));
  function pick(networkId: string) {
    attach.mutate({ serverId, networkId }, { onSuccess: () => { toast.success("네트워크가 연결되었습니다"); onClose(); }, onError: (e) => toast.error("연결 실패", { description: String((e as Error).message) }) });
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !attach.isPending && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader><DialogTitle>네트워크 연결</DialogTitle><DialogDescription>이 VM에 연결할 네트워크를 선택하세요(새 NIC 추가).</DialogDescription></DialogHeader>
        <div className="max-h-[50vh] space-y-2 overflow-y-auto py-1">
          {isLoading ? (<div className="flex items-center gap-2 p-4 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 조회 중…</div>)
            : candidates.length === 0 ? (<Card className="p-6 text-center text-sm text-muted-foreground">연결 가능한 네트워크가 없습니다.</Card>)
            : candidates.map((n) => (
              <button key={n.id} type="button" disabled={attach.isPending} onClick={() => pick(n.id)}
                className="group flex w-full cursor-pointer items-center gap-3 rounded-lg border border-border p-3 text-left transition-colors hover:border-sky-300 hover:bg-sky-50/40 disabled:opacity-50">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-sky-100 text-sky-700"><NetIcon className="size-4" /></div>
                <div className="min-w-0 flex-1">
                  <div className="truncate font-mono text-sm font-semibold">{n.name}</div>
                  <div className="mt-0.5 flex items-center gap-1.5">
                    <Badge variant={n.type === "external" ? "default" : n.type === "shared" ? "warning" : "outline"} className="text-[10px]">{n.type}</Badge>
                    {n.cidr && <Badge variant="secondary" className="font-mono text-[10px]">{n.cidr}</Badge>}
                  </div>
                </div>
                <Plus className="size-4 shrink-0 text-muted-foreground group-hover:text-sky-600" />
              </button>
            ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function ResizeDialog({ serverId, currentFlavor, open, onClose }: { serverId: string; currentFlavor: string; open: boolean; onClose: () => void }) {
  const { data: flavors } = useFlavors();
  const dps = useDeviceProfiles();
  const accelModels = useAccelModels();
  const accelLabel = accelLabelResolver(dps.data, accelModels.data);
  const resize = useResizeServer();
  const [flavorId, setFlavorId] = React.useState("");
  React.useEffect(() => { if (open) setFlavorId(""); }, [open]);

  function submit() {
    if (!flavorId) return toast.error("플레이버를 선택하세요");
    resize.mutate({ id: serverId, flavorId }, {
      onSuccess: () => { toast.success("스펙 변경 시작됨", { description: "완료 후 '확정'하면 적용됩니다." }); onClose(); },
      onError: (e) => toast.error("스펙 변경 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !resize.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>스펙 변경 (resize)</DialogTitle>
          <DialogDescription>현재 <span className="font-mono">{currentFlavor}</span>. 변경 시 <strong>재부팅</strong>되며, 완료 후 확정이 필요합니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 py-2">
          <div className="text-[11px] text-muted-foreground">새 플레이버</div>
          <Select value={flavorId} onValueChange={setFlavorId}>
            <SelectTrigger><SelectValue placeholder="플레이버 선택" /></SelectTrigger>
            <SelectContent>
              {(flavors ?? []).filter((f) => f.name !== currentFlavor).map((f) => (
                <SelectItem key={f.id} value={f.id}>{f.name} · vCPU {f.vcpus} / {Math.round(f.ramMb / 1024)}GB{f.accelerator ? ` · ${accelLabel(f.accelerator) ?? f.accelerator}` : ""}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={resize.isPending}>취소</Button>
          <Button onClick={submit} disabled={resize.isPending}>{resize.isPending ? "변경 중…" : "변경"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function fmtRate(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
  if (bytesPerSec < 1024 ** 2) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
  return `${(bytesPerSec / 1024 ** 2).toFixed(1)} MB/s`;
}
function fmtUptime(sec: number): string {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), mm = Math.floor((sec % 3600) / 60);
  return d > 0 ? `${d}일 ${h}시간` : h > 0 ? `${h}시간 ${mm}분` : `${mm}분`;
}
