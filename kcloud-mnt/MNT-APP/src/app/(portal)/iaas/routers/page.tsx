"use client";
import * as React from "react";
import { Router as RouterIcon, Plus, Search, Trash2, Globe, Loader2, Settings } from "lucide-react";
import { toast } from "sonner";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  useRouters, useRouter, useCreateRouter, useDeleteRouter, useRouterInterface,
  useProjectSubnets, useProjects, useNetworks, byProjectThenName,
  type RouterSummary,
} from "@/lib/iaas-api";
import type { Tenant } from "@/lib/types";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";

// 라우터(L3) — OpenStack Neutron 실연동. 목록은 프로젝트 컬럼이 맨 앞(우리 기준 데이터).
export default function RoutersPage() {
  const { data, isLoading } = useRouters();
  const { data: projData } = useProjects();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<RouterSummary | undefined>();
  const del = useDeleteRouter();
  const confirm = useConfirm();

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] ?? id;
  }, [projData]);
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set((data ?? []).map((r) => r.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [data, projName]);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter(
    (r) =>
      (!q || r.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || r.tenantId === projFilter),
  ).sort(byProjectThenName(projName));

  async function remove(r: RouterSummary) {
    if (!(await confirm({
      title: "라우터 삭제", destructive: true, confirmLabel: "삭제",
      description: `${r.name} 을(를) 삭제할까요? 인터페이스·게이트웨이가 남아있으면 거부됩니다.`,
    }))) return;
    del.mutate(r.id, {
      onSuccess: () => toast.success(`라우터 ${r.name} 삭제됨`),
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
            <Input placeholder="라우터 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-routers"]]} />
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> 라우터 생성
            </Button>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>라우터</TableHead>
              <TableHead>외부망</TableHead>
              <TableHead>External IP</TableHead>
              <TableHead className="text-center">인터페이스</TableHead>
              <TableHead>상태</TableHead>
              <TableHead className="w-32" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((r) => (
              <TableRow key={r.id} className="cursor-pointer" onClick={() => setSelected(r)}>
                <TableCell className="text-xs font-medium">{r.tenantId ? projName(r.tenantId) : "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <RouterIcon className="size-3.5 text-brand-indigo-600" />
                    <span className="font-mono text-sm font-semibold">{r.name}</span>
                  </div>
                  <div className="font-mono text-[10px] text-muted-foreground">{r.id}</div>
                </TableCell>
                <TableCell className="text-xs">{r.externalNetwork || <span className="text-muted-foreground">내부 전용</span>}</TableCell>
                <TableCell className="font-mono text-xs">{r.externalIp || "—"}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{r.interfaces}</TableCell>
                <TableCell>
                  <Badge variant={r.status === "ACTIVE" ? "success" : "secondary"} className="text-[10px]">{r.status || "—"}</Badge>
                </TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setSelected(r)}>
                      <Settings className="size-3.5" /> 관리
                    </Button>
                    <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(r)}>
                      <Trash2 className="size-3.5 text-rose-600" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                  라우터가 없습니다.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(undefined)}>
        <DialogContent className="max-w-2xl">
          {selected && <RouterDetailBody router={selected} projectLabel={projName(selected.tenantId)} />}
        </DialogContent>
      </Dialog>

      <CreateRouterDialog open={createOpen} onClose={() => setCreateOpen(false)} projects={projData?.items ?? []} />
    </div>
  );
}

function RouterDetailBody({ router, projectLabel }: { router: RouterSummary; projectLabel: string }) {
  const { data: detail, isLoading, isError } = useRouter(router.id);
  const iface = useRouterInterface(router.id);
  const [addOpen, setAddOpen] = React.useState(false);
  const confirm = useConfirm();

  return (
    <>
      <DialogHeader>
        <div className="flex items-center gap-3 pr-8">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <RouterIcon className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate font-mono text-lg">{router.name}</DialogTitle>
            <DialogDescription className="truncate text-xs">
              <span className="font-mono">{router.id}</span> · 프로젝트 {projectLabel}
            </DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <div className="max-h-[65vh] min-w-0 space-y-4 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> 라우터 상세 조회 중…
          </div>
        ) : isError || !detail ? (
          <div className="p-6 text-sm text-rose-600">라우터 상세 조회 실패 (OpenStack 연결 확인)</div>
        ) : (
          <>
            <div className="grid grid-cols-3 gap-3">
              <InfoBox label="외부망" value={detail.externalNetwork || "내부 전용"} />
              <InfoBox label="External IP" value={detail.externalIp || "—"} mono />
              <InfoBox label="상태" value={detail.status || "—"} />
            </div>

            {/* 인터페이스 */}
            <div>
              <div className="mb-2 flex items-center justify-between">
                <div className="text-sm font-semibold">인터페이스 ({detail.interfaces.length})</div>
                <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
                  <Plus className="size-3.5" /> 서브넷 연결
                </Button>
              </div>
              <Card className="overflow-hidden">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>서브넷</TableHead>
                      <TableHead>CIDR</TableHead>
                      <TableHead>IP</TableHead>
                      <TableHead className="w-10" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {detail.interfaces.length === 0 && (
                      <TableRow><TableCell colSpan={4} className="py-6 text-center text-xs text-muted-foreground">연결된 서브넷이 없습니다.</TableCell></TableRow>
                    )}
                    {detail.interfaces.map((i) => (
                      <TableRow key={i.portId}>
                        <TableCell className="font-mono text-xs">{i.subnetName}</TableCell>
                        <TableCell className="font-mono text-xs">{i.cidr}</TableCell>
                        <TableCell className="font-mono text-xs">{i.ip}</TableCell>
                        <TableCell>
                          <Button
                            variant="ghost" size="icon-sm" title="연결 해제" disabled={iface.isPending}
                            onClick={async () => {
                              if (!(await confirm({
                                title: "서브넷 연결 해제", destructive: true, confirmLabel: "해제",
                                description: `${i.subnetName || i.cidr} 을(를) 이 라우터에서 해제할까요?`,
                              }))) return;
                              iface.mutate({ subnetId: i.subnetId, add: false }, {
                                onSuccess: () => toast.success("서브넷 연결 해제됨"),
                                onError: (e) => toast.error("해제 실패", { description: String((e as Error).message) }),
                              });
                            }}
                          >
                            <Trash2 className="size-3.5 text-rose-600" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Card>
            </div>

            {/* 정적 라우트 (조회) */}
            <div>
              <div className="mb-2 text-sm font-semibold">정적 라우트 ({detail.routes.length})</div>
              <Card className="overflow-hidden">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Destination</TableHead>
                      <TableHead>Next Hop</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {detail.routes.length === 0 ? (
                      <TableRow><TableCell colSpan={2} className="py-6 text-center text-xs text-muted-foreground">등록된 정적 라우트 없음</TableCell></TableRow>
                    ) : (
                      detail.routes.map((rt, idx) => (
                        <TableRow key={idx}>
                          <TableCell className="font-mono text-xs">{rt.destination}</TableCell>
                          <TableCell className="font-mono text-xs">{rt.nexthop}</TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </Card>
            </div>

            <AddInterfaceDialog
              projectId={detail.tenantId}
              attached={new Set(detail.interfaces.map((i) => i.subnetId))}
              open={addOpen}
              onClose={() => setAddOpen(false)}
              onPick={(subnetId) => {
                setAddOpen(false);
                iface.mutate({ subnetId, add: true }, {
                  onSuccess: () => toast.success("서브넷이 연결되었습니다"),
                  onError: (e) => toast.error("연결 실패", { description: String((e as Error).message) }),
                });
              }}
            />
          </>
        )}
      </div>
    </>
  );
}

function AddInterfaceDialog({
  projectId, attached, open, onClose, onPick,
}: {
  projectId: string;
  attached: Set<string>;
  open: boolean;
  onClose: () => void;
  onPick: (subnetId: string) => void;
}) {
  const { data: subnets, isLoading } = useProjectSubnets(open ? projectId : undefined);
  const candidates = (subnets ?? []).filter((s) => !attached.has(s.id));

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>서브넷 연결</DialogTitle>
          <DialogDescription>라우터에 연결할 서브넷을 선택하세요.</DialogDescription>
        </DialogHeader>
        <div className="max-h-[50vh] space-y-2 overflow-y-auto py-1">
          {isLoading ? (
            <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> 서브넷 조회 중…
            </div>
          ) : candidates.length === 0 ? (
            <Card className="p-6 text-center text-sm text-muted-foreground">연결 가능한 서브넷이 없습니다.</Card>
          ) : (
            candidates.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => onPick(s.id)}
                className="flex w-full cursor-pointer items-center justify-between rounded-md border border-border p-3 text-left hover:bg-secondary"
              >
                <div>
                  <div className="font-mono text-sm">{s.name}</div>
                  <div className="text-[11px] text-muted-foreground">{s.networkName} · {s.cidr}</div>
                </div>
                <Plus className="size-4 text-muted-foreground" />
              </button>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function CreateRouterDialog({ open, onClose, projects }: { open: boolean; onClose: () => void; projects: Tenant[] }) {
  const { data: netData } = useNetworks();
  const [projectId, setProjectId] = React.useState("");
  const [name, setName] = React.useState("");
  const [extNet, setExtNet] = React.useState("none");
  const create = useCreateRouter();
  React.useEffect(() => { if (open) { setProjectId(""); setName(""); setExtNet("none"); } }, [open]);

  const externalNets = (netData?.items ?? []).filter((n) => n.type === "external");

  function submit() {
    if (!projectId) return toast.error("프로젝트를 선택하세요");
    if (!name.trim()) return toast.error("이름을 입력하세요");
    create.mutate(
      { name: name.trim(), projectId, externalNetworkId: extNet !== "none" ? extNet : undefined },
      {
        onSuccess: (r) => { toast.success(`라우터 ${r.name} 생성됨`); onClose(); },
        onError: (e) => toast.error("라우터 생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>라우터 생성</DialogTitle>
          <DialogDescription>OpenStack Neutron L3 라우터를 생성합니다. (서브넷 연결은 생성 후)</DialogDescription>
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
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-router" />
          </div>
          <div className="space-y-1.5">
            <Label>외부망 (선택)</Label>
            <Select value={extNet} onValueChange={setExtNet}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="none">내부 전용 (외부망 없음)</SelectItem>
                {externalNets.map((n) => (
                  <SelectItem key={n.id} value={n.id}>{n.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
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

function InfoBox({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Card className="p-3">
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className={mono ? "truncate font-mono text-sm" : "truncate text-sm"}>{value}</div>
    </Card>
  );
}
