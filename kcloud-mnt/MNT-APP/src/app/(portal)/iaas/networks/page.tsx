"use client";
import * as React from "react";
import Link from "next/link";
import {
  Network as NetworkIcon,
  Wifi,
  Plug,
  ShieldCheck,
  Globe,
  Router,
  ChevronRight,
  Search,
  Copy,
  Plus,
  KeyRound,
  TerminalSquare,
  Server,
  Loader2,
} from "lucide-react";
import { useMyProjectIds } from "@/lib/member-api";
import {
  useNetworks,
  useNetwork,
  useProjects,
  useCreateNetwork,
  useFipTargets,
  useAllocateFip,
  useReleaseFip,
  useUpdateFip,
  useAddSubnet,
  useDeleteNetwork,
  byProjectThenName,
} from "@/lib/iaas-api";
import type { NetworkDetail } from "@/lib/iaas-api";
import { SgRulesDialog } from "@/components/iaas/sg-rules-dialog";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useConfirm } from "@/components/ui/confirm";
import { Label } from "@/components/ui/label";
import type { Tenant } from "@/lib/types";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Progress } from "@/components/ui/progress";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import type { IaasNetwork } from "@/lib/types";
import { useAuth } from "@/lib/auth";
import { ApiError } from "@/lib/http";
import RoutersPage from "../routers/page";
import SecurityGroupsPage from "../security-groups/page";

// 네트워크 영역 = 네트워크 / 라우터 / 보안그룹을 한 화면 탭으로 통합(라우터·SG는 프로젝트 자원이라 네트워크 하위가 아님 → 같은 영역에서 관리)
export default function NetworksPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  return (
    <Tabs defaultValue="networks" className="space-y-4">
      <TabsList>
        <TabsTrigger value="networks">네트워크</TabsTrigger>
        {isAdmin && <TabsTrigger value="routers">라우터</TabsTrigger>}
        <TabsTrigger value="security">보안 그룹</TabsTrigger>
      </TabsList>
      <TabsContent value="networks">
        <AdminNetworksView />
      </TabsContent>
      {isAdmin && (
        <TabsContent value="routers">
          <RoutersPage />
        </TabsContent>
      )}
      <TabsContent value="security">
        <SecurityGroupsPage />
      </TabsContent>
    </Tabs>
  );
}

function AdminNetworksView() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername);
  const { data, isLoading } = useNetworks();
  const { data: projData } = useProjects();
  const [selected, setSelected] = React.useState<IaasNetwork | undefined>();
  const [createOpen, setCreateOpen] = React.useState(false);
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState<string>("all");
  // project_id(UUID) → 프로젝트 이름. 없으면 UUID 그대로.
  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] ?? id;
  }, [projData]);
  // 사용자는 멤버인 프로젝트의 네트워크만 (관리자는 전체)
  const scoped = React.useMemo(
    () => (isAdmin ? (data?.items ?? []) : (data?.items ?? []).filter((n) => myProjectIds?.has(n.tenantId))),
    [data, isAdmin, myProjectIds],
  );
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set(scoped.map((n) => n.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [scoped, projName]);
  // ?detail=<id> 딥링크 — 다른 화면(VM 상세 등)에서 특정 네트워크 상세를 바로 열 때
  const autoOpened = React.useRef(false);
  React.useEffect(() => {
    if (autoOpened.current || !data) return;
    const id = new URLSearchParams(window.location.search).get("detail");
    if (id) { const n = data.items.find((x) => x.id === id); if (n) { setSelected(n); autoOpened.current = true; } }
  }, [data]);
  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = scoped.filter(
    (n) =>
      (!q || n.name.toLowerCase().includes(q.toLowerCase())) &&
      (projFilter === "all" || n.tenantId === projFilter),
  ).sort(byProjectThenName(projName));

  return (
    <div className="space-y-4">
      {/* Stats */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="네트워크" value={scoped.length} icon={NetworkIcon} accent="indigo" />
        <StatCard label="총 서브넷" value={scoped.reduce((s, n) => s + n.subnets, 0)} icon={Wifi} accent="sky" />
        <StatCard label="총 포트" value={scoped.reduce((s, n) => s + n.ports, 0)} icon={Plug} accent="mint" />
        <StatCard label="External 네트워크" value={scoped.filter((n) => n.type === "external").length} icon={Globe} accent="amber" />
      </div>

      <Card className="p-4">
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-3">
            <Select value={projFilter} onValueChange={setProjFilter}>
              <SelectTrigger className="w-48">
                <SelectValue placeholder="프로젝트 필터" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">전체 프로젝트</SelectItem>
                {projOptions.map((p) => (
                  <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input placeholder="네트워크 이름 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
            </div>
            <div className="ml-auto flex items-center gap-2">
              <RefreshControls queryKeys={[["iaas-networks"]]} />
              {isAdmin && (
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  <Plus className="size-3.5" /> 네트워크 생성
                </Button>
              )}
            </div>
          </div>
          <div className="rounded-md border border-brand-indigo-200 bg-brand-indigo-50/50 p-2.5 text-[11px] text-brand-indigo-900">
            <strong>생성 주체 정책:</strong> 네트워크 생성·관리는 <strong>관리자 전용</strong>이다. 사용자는 가상머신 신청 시
            서비스망(내부망/인터넷망)을 선택하거나 Floating IP를 요청하는 수준으로 제한된다.
            <span className="text-muted-foreground"> (네트워크 생성 기능은 명세서 추가 검토 예정)</span>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>네트워크</TableHead>
              <TableHead>유형</TableHead>
              <TableHead>공유</TableHead>
              <TableHead>상태</TableHead>
              <TableHead>CIDR</TableHead>
              <TableHead>VLAN</TableHead>
              <TableHead>서브넷</TableHead>
              <TableHead>포트</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((n) => (
              <TableRow key={n.id} className="cursor-pointer" onClick={() => setSelected(n)}>
                <TableCell className="text-xs font-medium">
                  {n.tenantId ? projName(n.tenantId) : "—"}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <div className="size-6 rounded-md bg-sky-100 p-1 text-sky-700">
                      <NetworkIcon className="size-4" />
                    </div>
                    <div>
                      <span className="font-medium">{n.name}</span>
                      <div className="font-mono text-[10px] text-muted-foreground">{n.id}</div>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant={n.type === "external" ? "default" : n.type === "shared" ? "warning" : "outline"}>
                    {n.type}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs">
                  {n.shared ? <Badge variant="warning">공유</Badge> : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell>
                  <Badge variant={n.status === "ACTIVE" ? "success" : "secondary"} className="text-[10px]">
                    {n.status || "—"}
                  </Badge>
                </TableCell>
                <TableCell className="font-mono text-xs">{n.cidr || "—"}</TableCell>
                <TableCell className="font-mono text-xs">{n.vlan ?? "—"}</TableCell>
                <TableCell className="font-mono tabular-nums">{n.subnets}</TableCell>
                <TableCell className="font-mono tabular-nums">{n.ports}</TableCell>
                <TableCell>
                  <ChevronRight className="size-4 text-muted-foreground" />
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow>
                <TableCell colSpan={10} className="py-10 text-center text-sm text-muted-foreground">
                  네트워크가 없습니다.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      {/* 상세 — 가운데 모달 */}
      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(undefined)}>
        <DialogContent className="max-w-5xl">
          {selected && <NetworkDetailPanel network={selected} projectLabel={projName(selected.tenantId)} onClose={() => setSelected(undefined)} />}
        </DialogContent>
      </Dialog>

      <CreateNetworkDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        projects={projData?.items ?? []}
        defaultProjectId={projFilter !== "all" ? projFilter : ""}
      />
    </div>
  );
}

function CreateNetworkDialog({
  open, onClose, projects, defaultProjectId,
}: { open: boolean; onClose: () => void; projects: Tenant[]; defaultProjectId: string }) {
  const [projectId, setProjectId] = React.useState(defaultProjectId);
  const [name, setName] = React.useState("");
  const [subnetName, setSubnetName] = React.useState("");
  const [cidr, setCidr] = React.useState("10.0.0.0/24");
  const [gateway, setGateway] = React.useState("");
  const [dhcp, setDhcp] = React.useState(true);
  const [dns, setDns] = React.useState("");
  const create = useCreateNetwork();

  React.useEffect(() => {
    if (open) {
      setProjectId(defaultProjectId);
      setName(""); setSubnetName(""); setCidr("10.0.0.0/24"); setGateway(""); setDhcp(true); setDns("");
    }
  }, [open, defaultProjectId]);

  function submit() {
    if (!projectId) return toast.error("프로젝트를 선택하세요");
    if (!name.trim()) return toast.error("네트워크 이름을 입력하세요");
    if (!cidr.trim()) return toast.error("CIDR을 입력하세요");
    create.mutate(
      {
        name: name.trim(),
        projectId,
        cidr: cidr.trim(),
        subnetName: subnetName.trim() || undefined,
        gatewayIp: gateway.trim() || undefined,
        enableDhcp: dhcp,
        dnsNameservers: dns.split(",").map((s) => s.trim()).filter(Boolean),
      },
      {
        onSuccess: (n) => {
          toast.success(`네트워크 ${n.name} 생성됨`, { description: `서브넷 ${cidr}` });
          onClose();
        },
        onError: (e) => toast.error("네트워크 생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>네트워크 생성</DialogTitle>
          <DialogDescription>테넌트 네트워크 + 서브넷을 함께 생성합니다 (OpenStack Neutron).</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>프로젝트</Label>
            <Select value={projectId} onValueChange={setProjectId}>
              <SelectTrigger>
                <SelectValue placeholder="프로젝트 선택" />
              </SelectTrigger>
              <SelectContent>
                {projects.filter((p) => p.enabled !== false).map((p) => (
                  <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>네트워크 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-network" />
          </div>
          <div className="rounded-md border border-border p-3">
            <div className="mb-2 text-[10px] uppercase text-muted-foreground">서브넷</div>
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2 space-y-1.5">
                <Label>이름 (선택)</Label>
                <Input value={subnetName} onChange={(e) => setSubnetName(e.target.value)} placeholder="비우면 자동(네트워크명-subnet)" />
              </div>
              <div className="space-y-1.5">
                <Label>CIDR</Label>
                <Input value={cidr} onChange={(e) => setCidr(e.target.value)} placeholder="10.0.0.0/24" />
              </div>
              <div className="space-y-1.5">
                <Label>게이트웨이 (선택)</Label>
                <Input value={gateway} onChange={(e) => setGateway(e.target.value)} placeholder="자동" />
              </div>
              <div className="col-span-2 space-y-1.5">
                <Label>DNS 서버 (선택, 쉼표 구분)</Label>
                <Input value={dns} onChange={(e) => setDns(e.target.value)} placeholder="8.8.8.8, 1.1.1.1" />
              </div>
              <label className="col-span-2 flex cursor-pointer items-center gap-2 text-sm">
                <input type="checkbox" checked={dhcp} onChange={(e) => setDhcp(e.target.checked)} className="size-4 cursor-pointer" />
                DHCP 활성화
              </label>
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

function NetworkDetailPanel({ network, projectLabel, onClose }: { network: IaasNetwork; projectLabel: string; onClose: () => void }) {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const { data: detail, isLoading, isError, error } = useNetwork(network.id);
  const [addSubnetOpen, setAddSubnetOpen] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [sgEditId, setSgEditId] = React.useState<string | undefined>();
  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    toast.success(`${label} 복사됨`);
  };

  return (
    <>
      <DialogHeader>
        <div className="flex items-center gap-3 pr-8">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-sky-100 text-sky-700">
            <NetworkIcon className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <DialogTitle className="truncate text-lg">{network.name}</DialogTitle>
              <Badge
                variant={network.type === "external" ? "default" : network.type === "shared" ? "warning" : "outline"}
                className="shrink-0"
              >
                {network.type}
              </Badge>
            </div>
            <DialogDescription className="truncate text-xs">
              <span className="font-mono">{network.id}</span> · 프로젝트 {projectLabel}
              {network.poolId && <> · {network.poolId}</>}
            </DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <div className="max-h-[65vh] min-w-0 space-y-4 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Neutron 상세 조회 중…
          </div>
        ) : isError || !detail ? (
          <div className="p-6 text-sm text-rose-600">
            {/* 사용자가 공유·외부망(다른 프로젝트 포트가 보임) 상세를 열면 서버가 403 */}
            {error instanceof ApiError && error.status === 403
              ? "관리자만 볼 수 있는 네트워크입니다."
              : "네트워크 상세 조회 실패 (OpenStack 연결 확인)"}
          </div>
        ) : (
          <>
        {/* Quick info */}
        <div className="grid grid-cols-3 gap-3">
          <InfoBox label="CIDR" value={detail.subnets[0]?.cidr ?? "—"} mono onCopy={() => copy(detail.subnets[0]?.cidr ?? "", "CIDR")} />
          <InfoBox label="VLAN" value={detail.vlan?.toString() ?? "—"} mono />
          <InfoBox label="Provider" value="OpenStack Neutron" />
        </div>

        {/* Quotas */}
        <Card className="space-y-3 p-4">
          <div className="text-xs font-semibold">테넌트 Quota</div>
          <QuotaBar label="Ports" used={detail.quotas.ports.used} max={detail.quotas.ports.max} color="indigo" />
          <QuotaBar label="Floating IPs" used={detail.quotas.floatingIps.used} max={detail.quotas.floatingIps.max} color="mint" />
          <QuotaBar label="Subnets" used={detail.quotas.subnets.used} max={detail.quotas.subnets.max} color="amber" />
        </Card>

        {/* Tabs */}
        <Tabs defaultValue="subnets">
          <TabsList>
            <TabsTrigger value="subnets">서브넷 ({detail.subnets.length})</TabsTrigger>
            <TabsTrigger value="ports">포트 ({detail.ports.length})</TabsTrigger>
            <TabsTrigger value="security">보안 그룹 ({detail.securityGroups.length})</TabsTrigger>
            <TabsTrigger value="routers">라우터 ({detail.routers.length})</TabsTrigger>
            <TabsTrigger value="fip">Floating IP ({detail.floatingIps.length})</TabsTrigger>
          </TabsList>

          <TabsContent value="subnets" className="mt-3 space-y-2">
            {detail.subnets.map((s) => (
              <Card key={s.id} className="p-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="space-y-1">
                    <div className="flex items-center gap-2">
                      <Wifi className="size-4 text-sky-600" />
                      <span className="font-mono font-semibold">{s.name}</span>
                      <Badge variant="ghost" className="font-mono text-[10px]">{s.id}</Badge>
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                      <KV label="CIDR" value={s.cidr} />
                      <KV label="Gateway" value={s.gateway} />
                      <KV label="DHCP" value={s.dhcp ? "ON" : "OFF"} />
                      <KV label="DNS" value={s.dns.join(", ")} />
                    </div>
                    <div className="text-[11px] text-muted-foreground">
                      Allocation Pool: <span className="font-mono">{s.allocationPool}</span>
                    </div>
                  </div>
                  <Button variant="ghost" size="icon-sm" onClick={() => copy(s.cidr, "Subnet CIDR")}>
                    <Copy className="size-3.5" />
                  </Button>
                </div>
              </Card>
            ))}
          </TabsContent>

          <TabsContent value="ports" className="mt-3">
            <Card className="overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>포트</TableHead>
                    <TableHead>IP</TableHead>
                    <TableHead>MAC</TableHead>
                    <TableHead>Owner</TableHead>
                    <TableHead>상태</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.ports.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Plug className="size-3.5 text-brand-mint-600" />
                          <div>
                            <div className="font-mono text-xs">{p.name}</div>
                            <div className="font-mono text-[10px] text-muted-foreground">{p.id}</div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{p.ip}</TableCell>
                      <TableCell className="font-mono text-[10px]">{p.mac}</TableCell>
                      <TableCell className="font-mono text-[10px] text-muted-foreground">
                        {p.deviceOwner}
                        {p.vm && (
                          p.deviceOwner.startsWith("compute:") ? (
                            <Link
                              href={`/iaas/instances/detail/?id=${p.vm}`}
                              className="ml-1 text-brand-indigo-600 hover:underline"
                              title="인스턴스 상세로 이동"
                            >
                              → {p.vm}
                            </Link>
                          ) : (
                            <span className="ml-1 text-brand-indigo-600">→ {p.vm}</span>
                          )
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant={p.status === "ACTIVE" ? "success" : "secondary"} className="text-[10px]">
                          {p.status}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          <TabsContent value="security" className="mt-3 space-y-2">
            {detail.securityGroups.map((sg) => (
              <Card key={sg.id} className="p-3">
                <div className="flex items-start gap-3">
                  <ShieldCheck className="mt-0.5 size-4 text-amber-600" />
                  <div className="flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-semibold">{sg.name}</span>
                      <Badge variant="outline" className="whitespace-nowrap text-[10px]">{sg.rules} 규칙</Badge>
                    </div>
                    <div className="text-xs text-muted-foreground">{sg.desc}</div>
                  </div>
                  {isAdmin && <Button variant="outline" size="sm" className="shrink-0" onClick={() => setSgEditId(sg.id)}>규칙 편집</Button>}
                </div>
              </Card>
            ))}
          </TabsContent>

          <TabsContent value="routers" className="mt-3 space-y-2">
            {detail.routers.map((r) => (
              <Card key={r.id} className="p-3">
                <div className="flex items-start gap-3">
                  <Router className="mt-0.5 size-4 text-brand-indigo-600" />
                  <div className="flex-1 space-y-1">
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-semibold">{r.name}</span>
                      <Badge variant="success" className="text-[10px]">{r.status}</Badge>
                      {r.ha && <Badge variant="default" className="text-[10px]">HA</Badge>}
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                      <KV label="External IP" value={r.gatewayIp} />
                      <KV label="라우팅" value={`${r.routes} routes`} />
                    </div>
                  </div>
                </div>
              </Card>
            ))}
          </TabsContent>

          <TabsContent value="fip" className="mt-3">
            <FipTab projectId={detail.tenantId} fips={detail.floatingIps} copy={copy} canWrite={isAdmin} />
          </TabsContent>
        </Tabs>

        {/* Actions — 네트워크 생성·관리는 관리자 기능(회의록). 사용자는 조회·선택만. */}
        {isAdmin && (
          <div className="flex items-center gap-2 border-t border-border pt-4">
            <Button variant="outline" size="sm" onClick={() => setAddSubnetOpen(true)}>서브넷 추가</Button>
            <Button variant="destructive" size="sm" className="ml-auto" onClick={() => setDeleteOpen(true)}>네트워크 삭제</Button>
          </div>
        )}

        <AddSubnetDialog networkId={network.id} open={addSubnetOpen} onClose={() => setAddSubnetOpen(false)} />
        <DeleteNetworkDialog
          network={network}
          open={deleteOpen}
          onClose={() => setDeleteOpen(false)}
          onDeleted={() => { setDeleteOpen(false); onClose(); }}
        />
        <SgRulesDialog sgId={sgEditId} onClose={() => setSgEditId(undefined)} />
          </>
        )}
      </div>
    </>
  );
}

function AddSubnetDialog({ networkId, open, onClose }: { networkId: string; open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [cidr, setCidr] = React.useState("");
  const [gateway, setGateway] = React.useState("");
  const [dhcp, setDhcp] = React.useState(true);
  const [dns, setDns] = React.useState("");
  const add = useAddSubnet(networkId);
  React.useEffect(() => { if (open) { setName(""); setCidr(""); setGateway(""); setDhcp(true); setDns(""); } }, [open]);

  function submit() {
    if (!cidr.trim()) return toast.error("CIDR을 입력하세요");
    add.mutate(
      {
        name: name.trim() || undefined,
        cidr: cidr.trim(),
        gatewayIp: gateway.trim() || undefined,
        enableDhcp: dhcp,
        dnsNameservers: dns.split(",").map((s) => s.trim()).filter(Boolean),
      },
      {
        onSuccess: () => { toast.success(`서브넷 추가됨`, { description: cidr }); onClose(); },
        onError: (e) => toast.error("서브넷 추가 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !add.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>서브넷 추가</DialogTitle>
          <DialogDescription>이 네트워크에 서브넷을 추가합니다 (OpenStack Neutron).</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-3 py-2">
          <div className="col-span-2 space-y-1.5">
            <Label>이름 (선택)</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="비우면 자동(네트워크명-subnet)" />
          </div>
          <div className="space-y-1.5">
            <Label>CIDR</Label>
            <Input value={cidr} onChange={(e) => setCidr(e.target.value)} placeholder="10.0.1.0/24" />
          </div>
          <div className="space-y-1.5">
            <Label>게이트웨이 (선택)</Label>
            <Input value={gateway} onChange={(e) => setGateway(e.target.value)} placeholder="자동" />
          </div>
          <div className="col-span-2 space-y-1.5">
            <Label>DNS 서버 (선택, 쉼표 구분)</Label>
            <Input value={dns} onChange={(e) => setDns(e.target.value)} placeholder="8.8.8.8, 1.1.1.1" />
          </div>
          <label className="col-span-2 flex cursor-pointer items-center gap-2 text-sm">
            <input type="checkbox" checked={dhcp} onChange={(e) => setDhcp(e.target.checked)} className="size-4 cursor-pointer" />
            DHCP 활성화
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={add.isPending}>취소</Button>
          <Button onClick={submit} disabled={add.isPending}>{add.isPending ? "추가 중…" : "추가"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteNetworkDialog({
  network, open, onClose, onDeleted,
}: { network: IaasNetwork; open: boolean; onClose: () => void; onDeleted: () => void }) {
  const [confirm, setConfirm] = React.useState("");
  const del = useDeleteNetwork();
  React.useEffect(() => { if (open) setConfirm(""); }, [open]);

  function submit() {
    del.mutate(network.id, {
      onSuccess: () => { toast.success(`네트워크 ${network.name} 삭제됨`); onDeleted(); },
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !del.isPending && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="text-rose-600">네트워크 삭제</DialogTitle>
          <DialogDescription>
            되돌릴 수 없습니다. 연결된 포트(가상서버 등)가 남아있으면 삭제가 거부됩니다.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2 py-2 text-sm">
          <p>삭제하려면 네트워크 이름 <strong className="font-mono">{network.name}</strong> 을(를) 입력하세요.</p>
          <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder={network.name} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={del.isPending}>취소</Button>
          <Button variant="destructive" onClick={submit} disabled={del.isPending || confirm !== network.name}>
            {del.isPending ? "삭제 중…" : "삭제"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function FipTab({
  projectId, fips, copy, canWrite,
}: {
  projectId: string;
  fips: NetworkDetail["floatingIps"];
  copy: (text: string, label: string) => void;
  canWrite: boolean;
}) {
  const allocate = useAllocateFip();
  const release = useReleaseFip();
  const update = useUpdateFip();
  const confirm = useConfirm();
  const [assocFor, setAssocFor] = React.useState<string | undefined>(); // 연결 대상 선택 중인 FIP id
  const busy = allocate.isPending || release.isPending || update.isPending;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">공인 IP {fips.length}개</span>
        {canWrite && (
          <Button
            size="sm"
            disabled={busy}
            onClick={async () => {
              if (!(await confirm({
                title: "공인 IP 확보",
                description: "외부 풀에서 공인 IP를 1개 확보합니다. (프로젝트 쿼터를 소비)",
                confirmLabel: "확보",
              }))) return;
              allocate.mutate(projectId, {
                onSuccess: (f) => toast.success(`공인 IP 확보됨`, { description: (f as { floating_ip_address?: string }).floating_ip_address }),
                onError: (e) => toast.error("확보 실패", { description: String((e as Error).message) }),
              });
            }}
          >
            <Plus className="size-3.5" /> 공인 IP 확보
          </Button>
        )}
      </div>

      {fips.length === 0 ? (
        <Card className="p-6 text-center text-sm text-muted-foreground">확보된 공인 IP가 없습니다.</Card>
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Public IP</TableHead>
                <TableHead>연결된 내부 IP</TableHead>
                <TableHead>상태</TableHead>
                <TableHead className="w-44" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {fips.map((f) => {
                const associated = !!f.portId;
                return (
                  <TableRow key={f.id}>
                    <TableCell>
                      <div className="flex items-center gap-1.5">
                        <Globe className="size-3.5 text-brand-mint-600" />
                        <span className="font-mono">{f.ip}</span>
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{associated ? f.mappedTo : "—"}</TableCell>
                    <TableCell>
                      <Badge variant={associated ? "success" : "secondary"} className="text-[10px]">
                        {associated ? "연결됨" : "미연결"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Button variant="ghost" size="icon-sm" onClick={() => copy(f.ip, "공인 IP")} title="IP 복사">
                          <Copy className="size-3.5" />
                        </Button>
                        {canWrite && (associated ? (
                          <Button
                            variant="ghost" size="sm" className="h-7 text-[11px]" disabled={busy}
                            onClick={async () => {
                              if (!(await confirm({
                                title: "연결 해제", destructive: true, confirmLabel: "해제",
                                description: `${f.ip} 를 ${f.mappedTo} 에서 연결 해제할까요?`,
                              }))) return;
                              update.mutate({ fipId: f.id, portId: null }, {
                                onSuccess: () => toast.success(`${f.ip} 연결 해제됨`),
                                onError: (e) => toast.error("연결 해제 실패", { description: String((e as Error).message) }),
                              });
                            }}
                          >
                            연결 해제
                          </Button>
                        ) : (
                          <Button variant="ghost" size="sm" className="h-7 text-[11px]" disabled={busy} onClick={() => setAssocFor(f.id)}>
                            연결
                          </Button>
                        ))}
                        {canWrite && (
                          <Button
                            variant="ghost" size="sm" className="h-7 text-[11px] text-rose-600" disabled={busy}
                            onClick={async () => {
                              if (!(await confirm({
                                title: "공인 IP 반납", destructive: true, confirmLabel: "반납",
                                description: `${f.ip} 를 외부 풀로 반납할까요? 되돌릴 수 없습니다.`,
                              }))) return;
                              release.mutate(f.id, {
                                onSuccess: () => toast.success(`${f.ip} 반납됨`),
                                onError: (e) => toast.error("반납 실패", { description: String((e as Error).message) }),
                              });
                            }}
                          >
                            반납
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}

      <AssociateFipDialog
        projectId={projectId}
        fipId={assocFor}
        onClose={() => setAssocFor(undefined)}
        onPick={(portId) => {
          const id = assocFor!;
          setAssocFor(undefined);
          update.mutate({ fipId: id, portId }, {
            onSuccess: () => toast.success("공인 IP가 연결되었습니다"),
            onError: (e) => toast.error("연결 실패", { description: String((e as Error).message) }),
          });
        }}
      />
    </div>
  );
}

function AssociateFipDialog({
  projectId, fipId, onClose, onPick,
}: {
  projectId: string;
  fipId: string | undefined;
  onClose: () => void;
  onPick: (portId: string) => void;
}) {
  const { data: targets, isLoading } = useFipTargets(fipId ? projectId : undefined);
  return (
    <Dialog open={!!fipId} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>연결할 대상 선택</DialogTitle>
          <DialogDescription>공인 IP를 붙일 VM의 내부 포트를 고르세요.</DialogDescription>
        </DialogHeader>
        <div className="max-h-[50vh] space-y-2 overflow-y-auto py-1">
          {isLoading ? (
            <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> 대상 포트 조회 중…
            </div>
          ) : !targets || targets.length === 0 ? (
            <Card className="p-6 text-center text-sm text-muted-foreground">연결 가능한 포트가 없습니다.</Card>
          ) : (
            targets.map((t) => (
              <button
                key={t.portId}
                type="button"
                onClick={() => onPick(t.portId)}
                className="flex w-full cursor-pointer items-center justify-between rounded-md border border-border p-3 text-left hover:bg-secondary"
              >
                <div className="flex items-center gap-2">
                  <Plug className="size-4 text-brand-mint-600" />
                  <div>
                    <div className="font-mono text-sm">{t.ip}</div>
                    <div className="text-[11px] text-muted-foreground">{t.networkName}</div>
                  </div>
                </div>
                <ChevronRight className="size-4 text-muted-foreground" />
              </button>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function StatCard({ label, value, icon: Icon, accent }: { label: string; value: React.ReactNode; icon: React.ComponentType<{ className?: string }>; accent: "indigo" | "mint" | "amber" | "sky" }) {
  const map = {
    indigo: "bg-brand-indigo-100 text-brand-indigo-700",
    mint: "bg-brand-mint-50 text-brand-mint-700",
    amber: "bg-amber-100 text-amber-700",
    sky: "bg-sky-100 text-sky-700",
  };
  return (
    <Card className="flex items-center justify-between p-4">
      <div>
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="text-2xl font-bold tabular-nums">{value}</div>
      </div>
      <div className={cn("flex size-10 items-center justify-center rounded-md", map[accent])}>
        <Icon className="size-5" />
      </div>
    </Card>
  );
}

function InfoBox({ label, value, mono, onCopy }: { label: string; value: string; mono?: boolean; onCopy?: () => void }) {
  return (
    <div className="rounded-md border border-border bg-secondary/30 p-3">
      <div className="flex items-center justify-between text-[10px] uppercase text-muted-foreground">
        {label}
        {onCopy && (
          <button onClick={onCopy} className="hover:text-foreground">
            <Copy className="size-3" />
          </button>
        )}
      </div>
      <div className={cn("mt-0.5 font-semibold", mono && "font-mono")}>{value}</div>
    </div>
  );
}

function QuotaBar({ label, used, max, color }: { label: string; used: number; max: number; color: "indigo" | "mint" | "amber" }) {
  const pct = Math.round((used / max) * 100);
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono tabular-nums">{used} / {max} ({pct}%)</span>
      </div>
      <Progress value={pct} className="h-1.5" indicatorClassName={color === "indigo" ? "bg-brand-indigo-500" : color === "mint" ? "bg-brand-mint-500" : "bg-amber-500"} />
    </div>
  );
}

function KV({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span className="text-muted-foreground">{label}: </span>
      <span className="font-mono">{value}</span>
    </div>
  );
}
