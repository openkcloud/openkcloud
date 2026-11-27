"use client";
import * as React from "react";
import {
  Users,
  FolderKanban,
  Edit,
  Trash2,
  Settings,
  Search,
  Plus,
  ChevronLeft,
  ChevronRight,
  Loader2,
} from "lucide-react";
import { useProjects, useProject, useCreateProject, useUpdateProject, useDeleteProject } from "@/lib/iaas-api";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { useAuth } from "@/lib/auth";
import { cn } from "@/lib/utils";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import {
  useProjectMembers, usePortalUsers, useAddProjectMember, useRemoveProjectMember, useMyProjectIds,
  useProjectMemberCounts, type ProjectMember,
} from "@/lib/member-api";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "sonner";
import type { Tenant } from "@/lib/types";

const PAGE_SIZE = 20;

const pct = (used: number, max: number) => (max > 0 ? Math.round((used / max) * 100) : 0);

export default function ProjectsPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername); // 사용자는 멤버인 프로젝트만
  const { data: memberCounts } = useProjectMemberCounts(); // 멤버 수 = 포털 DB(iam_project_member), OpenStack 아님
  const { data, isLoading } = useProjects() as {
    data: { items: Tenant[]; live: boolean } | undefined;
    isLoading: boolean;
  };
  const [selected, setSelected] = React.useState<Tenant | undefined>();
  const [createOpen, setCreateOpen] = React.useState(false);
  const [q, setQ] = React.useState("");
  const [page, setPage] = React.useState(1);
  React.useEffect(() => setPage(1), [q]); // 검색 바뀌면 첫 페이지로

  if (isLoading || !data) return <Skeleton className="h-96" />;

  // 사용자는 자신이 멤버인 프로젝트만 (관리자는 전체) — 멤버십은 포털 DB(iam_project_member)
  const mine = isAdmin ? data.items : data.items.filter((t) => myProjectIds?.has(t.id));
  const filtered = mine
    .filter((t) => !q || t.name.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => a.name.localeCompare(b.name, "ko"));

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const cur = Math.min(page, totalPages);
  const pageItems = filtered.slice((cur - 1) * PAGE_SIZE, cur * PAGE_SIZE);

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="프로젝트 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {filtered.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-projects"]]} />
            {isAdmin && (
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" /> 프로젝트 생성
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
              <TableHead>조직(도메인)</TableHead>
              <TableHead>상태</TableHead>
              <TableHead>멤버</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {pageItems.map((t) => (
              <TableRow
                key={t.id}
                className="cursor-pointer"
                onClick={() => setSelected(t)}
              >
                <TableCell className="max-w-[360px]">
                  <div className="flex items-center gap-2.5">
                    <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-brand-indigo-100 text-brand-indigo-700">
                      <FolderKanban className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <div className="truncate font-mono text-sm font-semibold">{t.name}</div>
                      <div className="truncate text-xs text-muted-foreground">{t.description || "—"}</div>
                    </div>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{t.orgId || "—"}</TableCell>
                <TableCell>
                  <Badge variant={t.enabled === false ? "outline" : "success"}>
                    {t.enabled === false ? "비활성" : "활성"}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  <span className="flex items-center gap-1"><Users className="size-3.5" />{memberCounts?.[t.id] ?? 0}</span>
                </TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="sm" onClick={() => setSelected(t)}>
                    <Settings className="size-3.5" /> {isAdmin ? "관리" : "보기"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {pageItems.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">
                  프로젝트가 없습니다.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>

        {totalPages > 1 && (
          <div className="flex items-center justify-between border-t border-border px-4 py-3 text-xs">
            <span className="text-muted-foreground">
              {(cur - 1) * PAGE_SIZE + 1}–{Math.min(cur * PAGE_SIZE, filtered.length)} / {filtered.length}
            </span>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="icon-sm" disabled={cur <= 1} onClick={() => setPage(cur - 1)}>
                <ChevronLeft className="size-4" />
              </Button>
              <span className="tabular-nums">{cur} / {totalPages}</span>
              <Button variant="outline" size="icon-sm" disabled={cur >= totalPages} onClick={() => setPage(cur + 1)}>
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}
      </Card>

      {/* 상세 — 가운데 모달. 쿼터는 여기서 on-demand 라이브 조회 */}
      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(undefined)}>
        <DialogContent className="max-w-3xl">
          {selected && (
            <ProjectDetailBody
              project={selected}
              isAdmin={isAdmin}
              onClose={() => setSelected(undefined)}
            />
          )}
        </DialogContent>
      </Dialog>

      <CreateProjectDialog open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  );
}

function ProjectDetailBody({ project, isAdmin, onClose }: { project: Tenant; isAdmin: boolean; onClose: () => void }) {
  // 선택된 프로젝트의 쿼터·사용량을 그때 조회 (목록에선 안 불러옴)
  const { data: detail, isLoading, isError } = useProject(project.id);
  const { data: members } = useProjectMembers(project.id); // 멤버는 포털 DB(MNT-CORE)
  const t = detail ?? project; // 로딩 중엔 목록 메타로 헤더 먼저 표시
  const [editOpen, setEditOpen] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);

  return (
    <>
      <DialogHeader className="min-w-0">
        <div className="flex items-center gap-3 pr-8">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <FolderKanban className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate font-mono text-lg">{t.name}</DialogTitle>
            <DialogDescription className="truncate">{t.description || "—"}</DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <div className="max-h-[70vh] min-w-0 overflow-y-auto">
        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTrigger value="overview">개요</TabsTrigger>
            <TabsTrigger value="quota">쿼터</TabsTrigger>
            <TabsTrigger value="members">멤버 ({members?.length ?? 0})</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="space-y-2 pt-3">
            <Card className="space-y-2.5 p-4 text-xs">
              <KV label="프로젝트 ID" value={t.id} mono />
              <KV label="이름" value={t.name} mono />
              <KV label="조직(도메인)" value={t.orgId || "—"} mono />
              <KV
                label="상태"
                value={
                  <Badge variant={t.enabled === false ? "outline" : "success"}>
                    {t.enabled === false ? "비활성" : "활성"}
                  </Badge>
                }
              />
              <div className="space-y-1.5">
                <span className="text-muted-foreground">설명</span>
                <div className="h-24 min-w-0 overflow-y-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/30 p-2 leading-relaxed">
                  {t.description || "—"}
                </div>
              </div>
            </Card>
          </TabsContent>

          <TabsContent value="quota" className="space-y-3 pt-3">
            {isLoading ? (
              <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> OpenStack 쿼터 조회 중…
              </div>
            ) : isError ? (
              <div className="p-6 text-sm text-rose-600">쿼터 조회 실패 (OpenStack 연결 확인)</div>
            ) : (
              <>
                <Card className="space-y-3 p-4">
                  <QuotaRow label="vCPU" used={t.usedVcpu} max={t.quotaVcpu} unit="cores" />
                  <QuotaRow label="메모리" used={t.usedRamGb} max={t.quotaRamGb} unit="GB" />
                  <QuotaRow label="디스크" used={t.usedDiskTb} max={t.quotaDiskTb} unit="TB" />
                  <QuotaRow label="AI 가속기" used={t.usedAcc} max={t.quotaAcc} unit="장" />
                </Card>
                <div className="flex items-center justify-between gap-3">
                  <p className="text-[11px] text-muted-foreground">
                    * 쿼터·사용량은 OpenStack(Nova·Cinder) 실시간 조회. AI 가속기는 Cyborg 연동 시 반영.
                  </p>
                  {isAdmin && (
                    <Button variant="outline" size="sm" className="shrink-0" onClick={() => setEditOpen(true)}>
                      <Edit className="size-3.5" /> 쿼터 수정
                    </Button>
                  )}
                </div>
              </>
            )}
          </TabsContent>

          <TabsContent value="members" className="pt-3">
            <MembersTab projectId={project.id} members={members} isAdmin={isAdmin} />
          </TabsContent>
        </Tabs>
      </div>

      <DialogFooter className="border-t border-border pt-4">
        {isAdmin && (
          <>
            <Button variant="outline" size="sm" onClick={() => setEditOpen(true)}><Edit className="size-3.5" /> 편집</Button>
            <Button variant="destructive" size="sm" onClick={() => setConfirmOpen(true)}><Trash2 className="size-3.5" /> 삭제</Button>
          </>
        )}
        <Button variant="outline" size="sm" className="ml-auto" onClick={onClose}>닫기</Button>
      </DialogFooter>

      {isAdmin && (
        <>
          <EditProjectDialog project={t} open={editOpen} onClose={() => setEditOpen(false)} />
          <DeleteProjectDialog
            project={t}
            open={confirmOpen}
            onClose={() => setConfirmOpen(false)}
            onDeleted={() => { setConfirmOpen(false); onClose(); }}
          />
        </>
      )}
    </>
  );
}

// 프로젝트 멤버 = 포털 DB. 역할 구분 없이 "현재 멤버 ↔ 후보" 전송 목록(화살표)+각 목록 검색.
function MembersTab({ projectId, members, isAdmin }: { projectId: string; members: ProjectMember[] | undefined; isAdmin: boolean }) {
  const { data: users } = usePortalUsers(isAdmin); // 사용자 목록은 관리자 전용(멤버 추가 후보) — 사용자 화면은 부르지 않음
  const add = useAddProjectMember(projectId);
  const remove = useRemoveProjectMember(projectId);
  const [qCand, setQCand] = React.useState("");
  const [qMem, setQMem] = React.useState("");
  const [selCand, setSelCand] = React.useState<Set<string>>(new Set());
  const [selMem, setSelMem] = React.useState<Set<string>>(new Set());
  const busy = add.isPending || remove.isPending;
  const toggle = (set: React.Dispatch<React.SetStateAction<Set<string>>>, id: string) =>
    set((prev) => { const n = new Set(prev); if (n.has(id)) n.delete(id); else n.add(id); return n; });

  if (!members || (isAdmin && !users)) return <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> 멤버 조회 중…</div>;

  const matchU = (u: { username: string; name: string }, q: string) => {
    const s = q.trim().toLowerCase();
    return !s || u.username.toLowerCase().includes(s) || u.name.toLowerCase().includes(s);
  };
  const memberIds = new Set(members.map((m) => m.userId));
  const candFiltered = (users ?? []).filter((u) => !memberIds.has(u.id) && matchU(u, qCand)); // 사용자 화면은 users 없음
  const memFiltered = members.filter((m) => matchU(m, qMem));

  async function moveToMembers() {
    const ids = [...selCand];
    try { for (const id of ids) await add.mutateAsync({ userId: Number(id), role: "member" }); toast.success(`${ids.length}명 추가됨`); }
    catch (e) { toast.error("추가 실패", { description: String((e as Error).message) }); }
    setSelCand(new Set());
  }
  async function moveToCandidates() {
    const ids = [...selMem];
    try { for (const id of ids) await remove.mutateAsync(id); toast.success(`${ids.length}명 제거됨`); }
    catch (e) { toast.error("제거 실패", { description: String((e as Error).message) }); }
    setSelMem(new Set());
  }

  if (!isAdmin) {
    return members.length === 0
      ? <Card className="p-8 text-center text-sm text-muted-foreground">등록된 멤버가 없습니다.</Card>
      : (
        <Card className="divide-y divide-border">
          {members.map((m) => (
            <div key={m.userId} className="flex items-center gap-2 px-3 py-2">
              <div className="flex size-7 items-center justify-center rounded-full bg-brand-indigo-100 text-[11px] font-semibold text-brand-indigo-700">{m.name.slice(0, 1)}</div>
              <div><div className="text-sm font-medium">{m.name}</div><div className="font-mono text-[10px] text-muted-foreground">{m.username}{m.org ? ` · ${m.org}` : ""}</div></div>
            </div>
          ))}
        </Card>
      );
  }

  return (
    <div className="grid grid-cols-[1fr_auto_1fr] items-stretch gap-3">
      <TransferPanel title="사용자" count={candFiltered.length} q={qCand} setQ={setQCand}
        items={candFiltered.map((u) => ({ id: u.id, name: u.name, username: u.username, org: u.org }))}
        selected={selCand} onToggle={(id) => toggle(setSelCand, id)} emptyText="추가할 사용자가 없습니다" />
      <div className="flex flex-col items-center justify-center gap-2">
        <Button size="icon-sm" variant="outline" disabled={!selCand.size || busy} title="멤버로 추가" onClick={moveToMembers}><ChevronRight className="size-4" /></Button>
        <Button size="icon-sm" variant="outline" disabled={!selMem.size || busy} title="멤버에서 제거" onClick={moveToCandidates}><ChevronLeft className="size-4" /></Button>
      </div>
      <TransferPanel title="현재 멤버" count={memFiltered.length} q={qMem} setQ={setQMem}
        items={memFiltered.map((m) => ({ id: m.userId, name: m.name, username: m.username, org: m.org }))}
        selected={selMem} onToggle={(id) => toggle(setSelMem, id)} emptyText="멤버가 없습니다" />
    </div>
  );
}

function TransferPanel({ title, count, q, setQ, items, selected, onToggle, emptyText }: {
  title: string; count: number; q: string; setQ: (v: string) => void;
  items: { id: string; name: string; username: string; org: string }[];
  selected: Set<string>; onToggle: (id: string) => void; emptyText: string;
}) {
  return (
    <Card className="flex flex-col overflow-hidden">
      <div className="border-b border-border p-2">
        <div className="mb-1.5 px-1 text-[11px] font-semibold text-muted-foreground">{title} ({count})</div>
        <div className="relative">
          <Search className="absolute left-2 top-2 size-3.5 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="아이디·이름 검색" className="h-8 pl-7 text-xs" />
        </div>
      </div>
      <div className="max-h-64 min-h-40 space-y-0.5 overflow-y-auto p-1.5">
        {items.length === 0 ? <div className="p-6 text-center text-xs text-muted-foreground">{emptyText}</div> :
          items.map((u) => (
            <button key={u.id} type="button" onClick={() => onToggle(u.id)}
              className={cn("flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-secondary",
                selected.has(u.id) && "bg-brand-indigo-50 ring-1 ring-brand-indigo-200")}>
              <div className="flex size-6 shrink-0 items-center justify-center rounded-full bg-brand-indigo-100 text-[10px] font-semibold text-brand-indigo-700">{u.name.slice(0, 1)}</div>
              <div className="min-w-0">
                <div className="truncate text-xs font-medium">{u.name}</div>
                <div className="truncate font-mono text-[10px] text-muted-foreground">{u.username}{u.org ? ` · ${u.org}` : ""}</div>
              </div>
            </button>
          ))}
      </div>
    </Card>
  );
}

function EditProjectDialog({ project, open, onClose }: { project: Tenant; open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState(project.name);
  const [desc, setDesc] = React.useState(project.description);
  const [enabled, setEnabled] = React.useState(project.enabled !== false);
  const [vcpu, setVcpu] = React.useState(String(project.quotaVcpu));
  const [ram, setRam] = React.useState(String(project.quotaRamGb));
  const [disk, setDisk] = React.useState(String(Math.round(project.quotaDiskTb * 1024)));
  const update = useUpdateProject();

  // 다른 프로젝트로 모달이 다시 열릴 때 폼을 현재 값으로 초기화
  React.useEffect(() => {
    if (open) {
      setName(project.name);
      setDesc(project.description);
      setEnabled(project.enabled !== false);
      setVcpu(String(project.quotaVcpu));
      setRam(String(project.quotaRamGb));
      setDisk(String(Math.round(project.quotaDiskTb * 1024)));
    }
  }, [open, project]);

  function submit() {
    if (!name.trim()) return toast.error("이름을 입력하세요");
    update.mutate(
      {
        id: project.id,
        name: name.trim(),
        description: desc,
        enabled,
        quotaVcpu: Number(vcpu),
        quotaRamGb: Number(ram),
        quotaDiskGb: Number(disk),
      },
      {
        onSuccess: () => { toast.success(`${name} 수정됨`); onClose(); },
        onError: (e) => toast.error("수정 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !update.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="font-mono">프로젝트 편집</DialogTitle>
          <DialogDescription>OpenStack Keystone 메타 + Nova/Cinder 쿼터 수정</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>프로젝트 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>설명</Label>
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="프로젝트 용도 설명" />
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} className="size-4 cursor-pointer" />
            활성 상태
          </label>
          <div className="rounded-md border border-border p-3">
            <div className="mb-2 text-[10px] uppercase text-muted-foreground">쿼터</div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label>vCPU</Label>
                <Input type="number" value={vcpu} onChange={(e) => setVcpu(e.target.value)} />
                {Number(vcpu) < project.usedVcpu && (
                  <p className="text-[10px] text-amber-600">현재 사용량({project.usedVcpu} cores)보다 작습니다 — 기존 자원은 유지되나 신규 생성이 막힙니다</p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label>RAM (GB)</Label>
                <Input type="number" value={ram} onChange={(e) => setRam(e.target.value)} />
                {Number(ram) < project.usedRamGb && (
                  <p className="text-[10px] text-amber-600">현재 사용량({project.usedRamGb}GB)보다 작습니다</p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label>디스크 (GB)</Label>
                <Input type="number" value={disk} onChange={(e) => setDisk(e.target.value)} />
                {Number(disk) < (project.usedDiskGb ?? Math.round(project.usedDiskTb * 1024)) && (
                  <p className="text-[10px] text-amber-600">현재 사용량({project.usedDiskGb ?? Math.round(project.usedDiskTb * 1024)}GB)보다 작습니다</p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label className="text-muted-foreground">가속기</Label>
                <Input type="number" value="-1" disabled title="가속기 쿼터는 포털에서 관리 예정 (OpenStack 미지원)" />
                <p className="text-[10px] text-muted-foreground">포털 관리 예정 (OpenStack 쿼터 없음)</p>
              </div>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={update.isPending}>취소</Button>
          <Button onClick={submit} disabled={update.isPending}>
            {update.isPending ? "저장 중…" : "저장"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteProjectDialog({
  project, open, onClose, onDeleted,
}: { project: Tenant; open: boolean; onClose: () => void; onDeleted: () => void }) {
  const [confirm, setConfirm] = React.useState("");
  const del = useDeleteProject();
  React.useEffect(() => { if (open) setConfirm(""); }, [open]);

  function submit() {
    del.mutate(project.id, {
      onSuccess: () => { toast.success(`프로젝트 ${project.name} 삭제됨`); onDeleted(); },
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !del.isPending && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="text-rose-600">프로젝트 삭제</DialogTitle>
          <DialogDescription>
            이 작업은 되돌릴 수 없습니다. 프로젝트 안의 OpenStack 자원(가상서버·볼륨 등)은 사전에 정리되어 있어야 합니다.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2 py-2 text-sm">
          <p>
            삭제하려면 프로젝트 이름 <strong className="font-mono">{project.name}</strong> 을(를) 입력하세요.
          </p>
          <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder={project.name} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={del.isPending}>취소</Button>
          <Button
            variant="destructive"
            onClick={submit}
            disabled={del.isPending || confirm !== project.name}
          >
            {del.isPending ? "삭제 중…" : "삭제"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreateProjectDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const [vcpu, setVcpu] = React.useState("8");
  const [ram, setRam] = React.useState("32");
  const [disk, setDisk] = React.useState("100");
  const acc = "-1"; // 가속기 쿼터는 OpenStack 미지원 → 비활성(-1, 포털 DB 관리 예정)
  const create = useCreateProject();

  function reset() { setName(""); setDesc(""); setVcpu("8"); setRam("32"); setDisk("100"); }

  function submit() {
    if (!name.trim()) return toast.error("이름을 입력하세요");
    create.mutate(
      {
        name: name.trim(),
        description: desc,
        quotaVcpu: Number(vcpu),
        quotaRamGb: Number(ram),
        quotaDiskGb: Number(disk),
        quotaAcc: -1,
      },
      {
        onSuccess: (t) => {
          toast.success(`프로젝트 ${t.name} 생성됨`, { description: `vCPU ${vcpu} / RAM ${ram}GB / Disk ${disk}GB` });
          reset();
          onClose();
        },
        onError: (e) => toast.error("프로젝트 생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>프로젝트 생성</DialogTitle>
          <DialogDescription>OpenStack Keystone Project 생성</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>프로젝트 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-project" />
          </div>
          <div className="space-y-1.5">
            <Label>설명</Label>
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="프로젝트 용도 설명" />
          </div>
          <div className="rounded-md border border-border p-3">
            <div className="mb-2 text-[10px] uppercase text-muted-foreground">초기 쿼터</div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label>vCPU</Label>
                <Input type="number" value={vcpu} onChange={(e) => setVcpu(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>RAM (GB)</Label>
                <Input type="number" value={ram} onChange={(e) => setRam(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>디스크 (GB)</Label>
                <Input type="number" value={disk} onChange={(e) => setDisk(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label className="text-muted-foreground">가속기</Label>
                <Input type="number" value={acc} disabled title="가속기 쿼터는 포털에서 관리 예정 (OpenStack 미지원)" />
                <p className="text-[10px] text-muted-foreground">포털 관리 예정 (OpenStack 쿼터 없음)</p>
              </div>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={create.isPending}>취소</Button>
          <Button onClick={submit} disabled={create.isPending}>
            {create.isPending ? "생성 중…" : "생성"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function QuotaRow({ label, used, max, unit }: { label: string; used: number; max: number; unit: string }) {
  const p = pct(used, max);
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between text-xs">
        <span className="font-medium">{label}</span>
        <span className="font-mono tabular-nums">
          <strong>{used}</strong> / {max} {unit} ({p}%)
        </span>
      </div>
      <Progress value={p} indicatorClassName={p > 80 ? "bg-rose-500" : p > 60 ? "bg-amber-500" : "bg-brand-mint-500"} />
    </div>
  );
}

function KV({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className={mono ? "min-w-0 break-all text-right font-mono" : "min-w-0 break-words text-right"}>{value}</span>
    </div>
  );
}
