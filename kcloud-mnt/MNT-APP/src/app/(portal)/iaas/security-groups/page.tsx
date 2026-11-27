"use client";
import * as React from "react";
import { ShieldCheck, Plus, Search, Settings, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SgRulesDialog } from "@/components/iaas/sg-rules-dialog";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useAuth } from "@/lib/auth";
import { useMyProjectIds } from "@/lib/member-api";
import {
  useSecurityGroups, useCreateSecurityGroup, useDeleteSecurityGroup, useProjects, byProjectThenName,
  type SgSummary,
} from "@/lib/iaas-api";
import type { Tenant } from "@/lib/types";

// 보안 그룹 — OpenStack Neutron 실연동. 목록은 프로젝트 컬럼이 맨 앞(우리 기준 데이터). 사용자는 멤버 프로젝트만.
export default function SecurityGroupsPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername);
  const { data, isLoading } = useSecurityGroups();
  const { data: projData } = useProjects();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [rulesFor, setRulesFor] = React.useState<string | undefined>();
  const del = useDeleteSecurityGroup();
  const confirm = useConfirm();

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] ?? id;
  }, [projData]);
  const scoped = React.useMemo(
    () => (isAdmin ? (data ?? []) : (data ?? []).filter((s) => myProjectIds?.has(s.tenantId))),
    [data, isAdmin, myProjectIds],
  );
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set(scoped.map((s) => s.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [scoped, projName]);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = scoped.filter(
    (s) =>
      (!q || s.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || s.tenantId === projFilter),
  ).sort(byProjectThenName(projName));

  async function remove(sg: SgSummary) {
    if (!(await confirm({
      title: "보안 그룹 삭제", destructive: true, confirmLabel: "삭제",
      description: `${sg.name} 을(를) 삭제할까요? 되돌릴 수 없습니다.`,
    }))) return;
    del.mutate(sg.id, {
      onSuccess: () => toast.success(`보안 그룹 ${sg.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <Select value={projFilter} onValueChange={setProjFilter}>
            <SelectTrigger className="w-48"><SelectValue placeholder="프로젝트 필터" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 프로젝트</SelectItem>
              {projOptions.map((p) => (
                <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="보안 그룹 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-sgs"]]} />
            {isAdmin && (
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" /> 보안 그룹 생성
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
              <TableHead>보안 그룹</TableHead>
              <TableHead>설명</TableHead>
              <TableHead className="text-center">규칙</TableHead>
              <TableHead className="w-32" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((s) => (
              <TableRow key={s.id} className="cursor-pointer" onClick={() => setRulesFor(s.id)}>
                <TableCell className="text-xs font-medium">{s.tenantId ? projName(s.tenantId) : "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <ShieldCheck className="size-3.5 text-amber-600" />
                    <span className="font-mono text-sm font-semibold">{s.name}</span>
                  </div>
                  <div className="font-mono text-[10px] text-muted-foreground">{s.id}</div>
                </TableCell>
                <TableCell className="max-w-[280px] truncate text-xs text-muted-foreground">{s.desc || "—"}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{s.rules}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setRulesFor(s.id)}>
                      <Settings className="size-3.5" /> 규칙
                    </Button>
                    {isAdmin && ( // 보안그룹 변경은 관리자(사용자는 조회만)
                      <Button
                        variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending}
                        onClick={() => remove(s)}
                      >
                        <Trash2 className="size-3.5 text-rose-600" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">
                  보안 그룹이 없습니다.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <SgRulesDialog sgId={rulesFor} onClose={() => setRulesFor(undefined)} readOnly={!isAdmin} />
      <CreateSgDialog open={createOpen} onClose={() => setCreateOpen(false)} projects={projData?.items ?? []} />
    </div>
  );
}

function CreateSgDialog({ open, onClose, projects }: { open: boolean; onClose: () => void; projects: Tenant[] }) {
  const [projectId, setProjectId] = React.useState("");
  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const create = useCreateSecurityGroup();
  React.useEffect(() => { if (open) { setProjectId(""); setName(""); setDesc(""); } }, [open]);

  function submit() {
    if (!projectId) return toast.error("프로젝트를 선택하세요");
    if (!name.trim()) return toast.error("이름을 입력하세요");
    create.mutate(
      { name: name.trim(), projectId, description: desc },
      {
        onSuccess: (sg) => { toast.success(`보안 그룹 ${sg.name} 생성됨`); onClose(); },
        onError: (e) => toast.error("생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>보안 그룹 생성</DialogTitle>
          <DialogDescription>OpenStack Neutron 보안 그룹을 생성합니다. (규칙은 생성 후 편집)</DialogDescription>
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
          </div>
          <div className="space-y-1.5">
            <Label>이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-tier" />
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
