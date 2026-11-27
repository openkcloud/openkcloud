"use client";
import * as React from "react";
import {
  Building2,
  Layers,
  Server,
  Plus,
  Edit,
  Trash2,
  MoreVertical,
  Activity,
  CheckCircle2,
  AlertCircle,
} from "lucide-react";
import { api } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import type { Center, Pool, Zone } from "@/lib/types";

export default function ProvidersPage() {
  const centers = api.iaas.centers() as { data: { items: Center[] } | undefined; isLoading: boolean };
  const zones = api.iaas.zones() as { data: { items: Zone[] } | undefined; isLoading: boolean };
  const pools = api.iaas.pools() as { data: { items: Pool[] } | undefined; isLoading: boolean };

  const [selectedCenter, setSelectedCenter] = React.useState<Center | undefined>();
  const [selectedPool, setSelectedPool] = React.useState<Pool | undefined>();
  const [centerCreateOpen, setCenterCreateOpen] = React.useState(false);
  const [poolCreateOpen, setPoolCreateOpen] = React.useState(false);

  if (centers.isLoading || zones.isLoading || pools.isLoading) {
    return <Skeleton className="h-96" />;
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Building2 className="size-4" /> 센터·존·망
              </CardTitle>
              <CardDescription>물리 위치 계층 구조</CardDescription>
            </div>
            <Button size="sm" onClick={() => setCenterCreateOpen(true)}>
              <Plus className="size-3.5" /> 센터 추가
            </Button>
          </CardHeader>
          <CardContent className="space-y-3">
            {centers.data?.items.map((c) => (
              <div
                key={c.id}
                className="cursor-pointer rounded-lg border border-border bg-card p-3 transition-colors hover:border-primary/40"
                onClick={() => setSelectedCenter(c)}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Building2 className="size-4 text-brand-indigo-600" />
                    <span className="font-semibold hover:text-primary">{c.name}</span>
                    <Badge variant="outline" className="text-[10px]">{c.code}</Badge>
                  </div>
                  <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                    <span className="text-xs text-muted-foreground">{c.location}</span>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm"><MoreVertical className="size-3.5" /></Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setSelectedCenter(c)}><Activity className="size-3.5" /> 상세</DropdownMenuItem>
                        <DropdownMenuItem onClick={() => toast.success(`${c.name} 편집`)}><Edit className="size-3.5" /> 편집</DropdownMenuItem>
                        <DropdownMenuItem onClick={() => toast.success(`${c.name}에 존 추가`)}><Plus className="size-3.5" /> 존 추가</DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem className="text-rose-600" onClick={() => toast.error(`${c.name} 삭제`)}><Trash2 className="size-3.5" /> 삭제</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
                <div className="mt-2 flex flex-wrap gap-2 pl-6">
                  {zones.data?.items.filter((z) => z.centerId === c.id).map((z) => (
                    <button
                      key={z.id}
                      onClick={(e) => { e.stopPropagation(); toast.info(`Zone ${z.name} 정보 — 물리 노드 5대, 가속기 12장`); }}
                      className="flex items-center gap-1.5 rounded-md border border-border bg-card px-2.5 py-1 text-xs hover:border-primary/40"
                    >
                      <Layers className="size-3 text-sky-600" />
                      <span className="font-mono">{z.name}</span>
                    </button>
                  ))}
                </div>
              </div>
            ))}
            {!centers.data?.items.length && (
              <div className="py-8 text-center text-sm text-muted-foreground">등록된 센터가 없습니다.</div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Server className="size-4" /> 자원풀
              </CardTitle>
              <CardDescription>OpenStack 멀티 리전 자원풀</CardDescription>
            </div>
            <Button size="sm" onClick={() => setPoolCreateOpen(true)}>
              <Plus className="size-3.5" /> 풀 추가
            </Button>
          </CardHeader>
          <CardContent className="space-y-3">
            {pools.data?.items.map((p) => {
              const cpuPct = Math.round((p.used.vcpu / p.capacity.vcpu) * 100);
              const ramPct = Math.round((p.used.ramGb / p.capacity.ramGb) * 100);
              return (
                <div
                  key={p.id}
                  className="cursor-pointer space-y-2 rounded-lg border border-border bg-card p-3 transition-colors hover:border-primary/40"
                  onClick={() => setSelectedPool(p)}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <div className={cn(
                        "size-2 rounded-full",
                        p.status === "정상" ? "bg-brand-mint-500" : p.status === "주의" ? "bg-amber-500" : "bg-rose-500",
                      )} />
                      <span className="font-semibold hover:text-primary">{p.name}</span>
                      <Badge variant="outline">{p.type}</Badge>
                    </div>
                    <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                      <Badge variant={p.status === "정상" ? "success" : p.status === "주의" ? "warning" : "destructive"}>
                        {p.status}
                      </Badge>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm"><MoreVertical className="size-3.5" /></Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setSelectedPool(p)}><Activity className="size-3.5" /> 상세</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => toast.success(`${p.name} 연결 테스트 성공`)}><CheckCircle2 className="size-3.5" /> 연결 테스트</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => toast.success(`${p.name} 동기화 시작`)}><Edit className="size-3.5" /> 동기화</DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem className="text-rose-600" onClick={() => toast.error(`${p.name} 제거 — VM 마이그레이션 필요`)}><Trash2 className="size-3.5" /> 제거</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </div>
                  <div className="font-mono text-[11px] text-muted-foreground">{p.endpoint}</div>
                  <div className="grid grid-cols-2 gap-3 pt-1">
                    <div className="space-y-1">
                      <div className="flex justify-between text-[11px]">
                        <span className="text-muted-foreground">vCPU</span>
                        <span className="font-mono tabular-nums">{p.used.vcpu}/{p.capacity.vcpu}</span>
                      </div>
                      <Progress value={cpuPct} indicatorClassName="bg-brand-indigo-500" />
                    </div>
                    <div className="space-y-1">
                      <div className="flex justify-between text-[11px]">
                        <span className="text-muted-foreground">RAM (GB)</span>
                        <span className="font-mono tabular-nums">{p.used.ramGb}/{p.capacity.ramGb}</span>
                      </div>
                      <Progress value={ramPct} indicatorClassName="bg-brand-mint-500" />
                    </div>
                  </div>
                </div>
              );
            })}
            {!pools.data?.items.length && (
              <div className="py-8 text-center text-sm text-muted-foreground">등록된 자원풀이 없습니다.</div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Sheets */}
      <Sheet open={!!selectedCenter} onOpenChange={(o) => !o && setSelectedCenter(undefined)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {selectedCenter && (
            <CenterDetail center={selectedCenter} zones={zones.data?.items.filter((z) => z.centerId === selectedCenter.id) ?? []} />
          )}
        </SheetContent>
      </Sheet>

      <Sheet open={!!selectedPool} onOpenChange={(o) => !o && setSelectedPool(undefined)}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {selectedPool && <PoolDetail pool={selectedPool} />}
        </SheetContent>
      </Sheet>

      <CreateCenterDialog open={centerCreateOpen} onClose={() => setCenterCreateOpen(false)} />
      <CreatePoolDialog open={poolCreateOpen} onClose={() => setPoolCreateOpen(false)} />
    </div>
  );
}

function CenterDetail({ center, zones }: { center: Center; zones: Zone[] }) {
  return (
    <>
      <SheetHeader className="border-b border-border">
        <div className="flex items-start gap-3">
          <div className="flex size-10 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <Building2 className="size-5" />
          </div>
          <div className="flex-1">
            <SheetTitle className="text-lg">{center.name}</SheetTitle>
            <SheetDescription>{center.location}</SheetDescription>
          </div>
          <Badge variant="outline" className="font-mono">{center.code}</Badge>
        </div>
      </SheetHeader>
      <div className="space-y-4 p-6 text-sm">
        <Card className="space-y-2 p-4 text-xs">
          <KV label="센터 ID" value={center.id} mono />
          <KV label="코드" value={center.code} mono />
          <KV label="위치" value={center.location} />
          <KV label="생성일" value={center.createdAt} />
          <KV label="존 개수" value={zones.length.toString()} />
        </Card>
        <Tabs defaultValue="zones">
          <TabsList>
            <TabsTrigger value="zones">존 ({zones.length})</TabsTrigger>
            <TabsTrigger value="network">네트워크</TabsTrigger>
          </TabsList>
          <TabsContent value="zones" className="space-y-2 pt-3">
            {zones.map((z) => (
              <Card key={z.id} className="p-3 text-xs">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Layers className="size-3.5 text-sky-600" />
                    <span className="font-mono font-semibold">{z.name}</span>
                    <Badge variant="outline" className="text-[10px]">{z.code}</Badge>
                  </div>
                  <Button variant="ghost" size="sm" onClick={() => toast.info(`${z.name} 상세`)}>상세</Button>
                </div>
              </Card>
            ))}
            <Button variant="outline" size="sm" className="w-full" onClick={() => toast.success(`${center.name}에 새 존 추가`)}>
              <Plus className="size-3.5" /> 새 존 추가
            </Button>
          </TabsContent>
          <TabsContent value="network" className="space-y-2 pt-3">
            <Card className="p-3 text-xs space-y-1">
              <KV label="관리 네트워크" value="10.10.0.0/16" mono />
              <KV label="서비스 네트워크" value="172.16.0.0/16" mono />
              <KV label="스토리지 네트워크" value="192.168.10.0/24" mono />
              <KV label="외부 네트워크" value="203.0.113.0/24" mono />
            </Card>
          </TabsContent>
        </Tabs>
        <div className="flex gap-2 border-t border-border pt-4">
          <Button variant="outline" size="sm" onClick={() => toast.success(`${center.name} 편집`)}><Edit className="size-3.5" /> 편집</Button>
          <Button variant="destructive" size="sm" className="ml-auto" onClick={() => toast.error(`${center.name} 삭제 — 자원 정리 후`)}><Trash2 className="size-3.5" /> 삭제</Button>
        </div>
      </div>
    </>
  );
}

function PoolDetail({ pool: p }: { pool: Pool }) {
  return (
    <>
      <SheetHeader className="border-b border-border">
        <div className="flex items-start gap-3">
          <div className="flex size-10 items-center justify-center rounded-lg bg-rose-100 text-rose-700">
            <Server className="size-5" />
          </div>
          <div className="flex-1">
            <SheetTitle className="text-lg">{p.name}</SheetTitle>
            <SheetDescription className="font-mono text-xs">{p.endpoint}</SheetDescription>
          </div>
          <Badge variant={p.status === "정상" ? "success" : p.status === "주의" ? "warning" : "destructive"}>{p.status}</Badge>
        </div>
      </SheetHeader>
      <div className="space-y-4 p-6">
        <Card className="space-y-2 p-4 text-xs">
          <KV label="유형" value={p.type} mono />
          <KV label="존" value={p.zoneId} mono />
          <KV label="vCPU 용량" value={`${p.used.vcpu}/${p.capacity.vcpu} cores`} mono />
          <KV label="RAM 용량" value={`${p.used.ramGb}/${p.capacity.ramGb} GB`} mono />
          <KV label="디스크 용량" value={`${p.used.diskTb}/${p.capacity.diskTb} TB`} mono />
        </Card>
        <Tabs defaultValue="nodes">
          <TabsList>
            <TabsTrigger value="nodes">노드</TabsTrigger>
            <TabsTrigger value="services">서비스</TabsTrigger>
            <TabsTrigger value="health">상태 점검</TabsTrigger>
          </TabsList>
          <TabsContent value="nodes" className="space-y-1 pt-3">
            {["compute-01", "compute-02", "compute-03"].map((n) => (
              <Card key={n} className="flex items-center gap-2 p-2.5 text-xs">
                <CheckCircle2 className="size-3.5 text-brand-mint-600" />
                <span className="font-mono">{n}</span>
                <span className="ml-auto text-muted-foreground">96 vCPU · 512 GB · 12 GPU</span>
              </Card>
            ))}
          </TabsContent>
          <TabsContent value="services" className="space-y-1 pt-3">
            {p.type === "OpenStack"
              ? ["Nova (compute)", "Neutron (networking)", "Cinder (block)", "Glance (image)", "Keystone (identity)"].map((s) => (
                  <Card key={s} className="flex items-center gap-2 p-2.5 text-xs">
                    <CheckCircle2 className="size-3.5 text-brand-mint-600" />
                    <span className="font-mono">{s}</span>
                    <Badge variant="success" className="ml-auto text-[10px]">UP</Badge>
                  </Card>
                ))
              : ["API Server", "etcd", "scheduler", "controller-manager"].map((s) => (
                  <Card key={s} className="flex items-center gap-2 p-2.5 text-xs">
                    <CheckCircle2 className="size-3.5 text-brand-mint-600" />
                    <span className="font-mono">{s}</span>
                    <Badge variant="success" className="ml-auto text-[10px]">UP</Badge>
                  </Card>
                ))}
          </TabsContent>
          <TabsContent value="health" className="space-y-2 pt-3">
            <Button size="sm" onClick={() => toast.success(`${p.name} 종합 헬스체크 완료 — 모든 서비스 정상`)}>
              <Activity className="size-3.5" /> 헬스체크 실행
            </Button>
            <Card className="p-3 text-xs space-y-1">
              <div className="flex items-center gap-2">
                <CheckCircle2 className="size-3.5 text-brand-mint-600" />
                <span>API 응답시간 정상 (12ms)</span>
              </div>
              <div className="flex items-center gap-2">
                <CheckCircle2 className="size-3.5 text-brand-mint-600" />
                <span>데이터베이스 연결 정상</span>
              </div>
              {p.status === "주의" && (
                <div className="flex items-center gap-2">
                  <AlertCircle className="size-3.5 text-amber-600" />
                  <span>Neutron API 지연 감지 (1.4s)</span>
                </div>
              )}
            </Card>
          </TabsContent>
        </Tabs>
        <div className="flex gap-2 border-t border-border pt-4">
          <Button variant="outline" size="sm" onClick={() => toast.success(`${p.name} 연결 테스트 성공`)}><CheckCircle2 className="size-3.5" /> 연결 테스트</Button>
          <Button variant="outline" size="sm" onClick={() => toast.success(`${p.name} 동기화 시작`)}><Edit className="size-3.5" /> 동기화</Button>
          <Button variant="destructive" size="sm" className="ml-auto" onClick={() => toast.error(`${p.name} 제거`)}><Trash2 className="size-3.5" /> 제거</Button>
        </div>
      </div>
    </>
  );
}

function CreateCenterDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [code, setCode] = React.useState("");
  const [loc, setLoc] = React.useState("");
  function submit() {
    if (!name) return toast.error("이름 입력");
    toast.success(`센터 ${name} 추가됨`);
    onClose();
    setName("");
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>센터 추가</DialogTitle>
          <DialogDescription>물리 데이터센터를 등록합니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5"><Label>센터 이름</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="여의도 IDC" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5"><Label>코드</Label><Input value={code} onChange={(e) => setCode(e.target.value)} placeholder="YDP" /></div>
            <div className="space-y-1.5"><Label>위치</Label><Input value={loc} onChange={(e) => setLoc(e.target.value)} placeholder="서울 영등포구" /></div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit}>추가</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function CreatePoolDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [type, setType] = React.useState("OpenStack");
  const [endpoint, setEndpoint] = React.useState("");
  function submit() {
    if (!name || !endpoint) return toast.error("이름과 엔드포인트 입력");
    toast.success(`자원풀 ${name} 추가됨 — 연결 테스트 진행 중`);
    onClose();
    setName(""); setEndpoint("");
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>자원풀 등록</DialogTitle>
          <DialogDescription>OpenStack 클러스터를 자원풀로 등록한다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5"><Label>풀 이름</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="DJ-OpenStack-Edge" /></div>
          <div className="space-y-1.5">
            <Label>유형</Label>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="OpenStack">OpenStack</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>엔드포인트</Label>
            <Input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} placeholder="https://controller.example.local:5000/v3" />
          </div>
          <div className="rounded-md border border-amber-200 bg-amber-50/60 p-2 text-xs text-amber-900">
            💡 등록 직후 자동 연결 테스트를 수행하고, 성공하면 노드/서비스 디스커버리를 시작합니다.
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit}>등록</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-right" : "text-right"}>{value}</span>
    </div>
  );
}
