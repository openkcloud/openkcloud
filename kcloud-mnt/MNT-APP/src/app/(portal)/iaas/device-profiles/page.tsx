"use client";
import * as React from "react";
import { Cpu, Plus, Search, Trash2, X } from "lucide-react";
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
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import {
  useDeviceProfiles, useFlavors, useCreateDeviceProfile, useDeleteDeviceProfile, profileTraits,
  type DeviceProfile, type CreateDeviceProfileInput,
} from "@/lib/iaas-api";

// group 원본(resources:*/trait:*) 파싱 — 실제 식별자(CUSTOM_NVIDIA_26B9 등) 노출용
function parseGroup(g: Record<string, string>) {
  const resources: { rc: string; count: string }[] = [];
  const traits: { name: string; policy: string }[] = [];
  const others: { k: string; v: string }[] = [];
  for (const [k, v] of Object.entries(g)) {
    if (k.startsWith("resources:")) resources.push({ rc: k.slice(10), count: v });
    else if (k.startsWith("trait:")) traits.push({ name: k.slice(6), policy: v });
    else others.push({ k, v });
  }
  return { resources, traits, others };
}

// 디바이스 프로파일(Cyborg device-profile) — 전역(admin) 자원. 플레이버가 accel:device_profile 로 참조.
// Cyborg 는 불변이라 생성·삭제만(수정 없음).
export default function DeviceProfilesPage() {
  const { data, isLoading } = useDeviceProfiles();
  const { data: flavors } = useFlavors();
  const del = useDeleteDeviceProfile();
  const confirm = useConfirm();
  const [q, setQ] = React.useState("");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [detail, setDetail] = React.useState<DeviceProfile | null>(null);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter((d) => !q || d.name.toLowerCase().includes(q.toLowerCase()));
  const linkedCount = (name: string) => (flavors ?? []).filter((f) => f.accelerator === name).length;

  async function remove(d: DeviceProfile) {
    const linked = linkedCount(d.name);
    const warn = linked > 0 ? ` 이 프로파일을 쓰는 플레이버 ${linked}개가 '사용 불가'가 됩니다.` : "";
    if (!(await confirm({
      title: "디바이스 프로파일 삭제", destructive: true, confirmLabel: "삭제",
      description: `${d.name} 을(를) 삭제할까요?${warn}`,
    }))) return;
    if (!d.uuid) return toast.error("uuid 가 없어 삭제할 수 없습니다");
    // 참조 플레이버가 있어도 사용자가 경고를 확인했으면 force 로 진행(백엔드는 기본 409 로 막음)
    del.mutate({ uuid: d.uuid, force: linked > 0 }, {
      onSuccess: () => toast.success(`디바이스 프로파일 ${d.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="프로파일 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-device-profiles"]]} />
            <Button size="sm" onClick={() => setCreateOpen(true)}><Plus className="size-3.5" /> 프로파일 생성</Button>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로파일</TableHead>
              <TableHead>가속기</TableHead>
              <TableHead>설명</TableHead>
              <TableHead className="text-center">연결 플레이버</TableHead>
              <TableHead>UUID</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((d) => {
              const traits = profileTraits(d.groups);
              return (
              <TableRow key={d.uuid ?? d.name} className="cursor-pointer" onClick={() => setDetail(d)}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Cpu className="size-3.5 text-brand-indigo-600" />
                    <span className="font-mono text-sm font-semibold">{d.name}</span>
                  </div>
                </TableCell>
                <TableCell>
                  {(d.accelerators ?? []).length || traits.length ? (
                    <div className="flex flex-col gap-1">
                      {(d.accelerators ?? []).length > 0 && (
                        <div className="flex flex-wrap gap-1">{d.accelerators!.map((a) => (
                          <Badge key={a.kind} variant="default" className="font-mono text-[10px]">{a.kind} ×{a.count}</Badge>
                        ))}</div>
                      )}
                      {traits.length > 0 && (
                        <div className="flex flex-wrap gap-1">{traits.map((t) => (
                          <Badge key={t} variant="secondary" className="font-mono text-[10px]">{t}</Badge>
                        ))}</div>
                      )}
                    </div>
                  ) : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell className="max-w-56 truncate text-sm text-muted-foreground">{d.description || "—"}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{linkedCount(d.name)}</TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">{d.uuid ?? "—"}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(d)}>
                    <Trash2 className="size-3.5 text-rose-600" />
                  </Button>
                </TableCell>
              </TableRow>
              );
            })}
            {items.length === 0 && (
              <TableRow><TableCell colSpan={6} className="py-10 text-center text-sm text-muted-foreground">디바이스 프로파일이 없습니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      {createOpen && <CreateDialog onClose={() => setCreateOpen(false)} />}
      {detail && <DetailDialog d={detail} onClose={() => setDetail(null)} />}
    </div>
  );
}

// ── 상세 보기(읽기전용) — Cyborg 는 수정 API 가 없어 조회만. 원본 groups(resources:*, trait:*) 그대로 노출 ──
function DetailDialog({ d, onClose }: { d: DeviceProfile; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Cpu className="size-4 text-brand-indigo-600" /> <span className="font-mono">{d.name}</span></DialogTitle>
        </DialogHeader>
        <div className="space-y-3 text-xs">
          <Field label="UUID" value={d.uuid ?? "—"} />
          <Field label="설명" value={d.description || "—"} />
          <div>
            <div className="mb-1 text-[11px] font-medium text-muted-foreground">가속기(요약)</div>
            {(d.accelerators ?? []).length
              ? <div className="flex flex-wrap gap-1">{d.accelerators!.map((a) => (
                  <Badge key={a.kind} variant="default" className="font-mono text-[10px]">{a.kind} ×{a.count}</Badge>
                ))}</div>
              : <span className="text-muted-foreground">—</span>}
          </div>
          <div>
            <div className="mb-1 text-[11px] font-medium text-muted-foreground">그룹 — 장치 1개당 그룹 1개</div>
            <div className="space-y-2">
              {(d.groups ?? []).map((g, i) => {
                const { resources, traits, others } = parseGroup(g);
                return (
                  <div key={i} className="rounded-md border p-2 text-[11px]">
                    <div className="mb-1.5 text-[10px] text-muted-foreground">그룹 {i + 1}</div>
                    <div className="space-y-1.5">
                      <div className="flex items-start justify-between gap-2">
                        <span className="shrink-0 text-muted-foreground">리소스 클래스</span>
                        <div className="flex flex-wrap justify-end gap-1">
                          {resources.length
                            ? resources.map((r) => <Badge key={r.rc} variant="outline" className="font-mono text-[10px]">{r.rc} ×{r.count}</Badge>)
                            : <span className="text-muted-foreground">—</span>}
                        </div>
                      </div>
                      <div className="flex items-start justify-between gap-2">
                        <span className="shrink-0 text-muted-foreground">트레잇</span>
                        <div className="flex flex-wrap justify-end gap-1">
                          {traits.length
                            ? traits.map((t) => (
                                <Badge key={t.name} variant="secondary" className="font-mono text-[10px]">
                                  {t.name}{t.policy !== "required" ? ` (${t.policy})` : ""}
                                </Badge>
                              ))
                            : <span className="text-muted-foreground">—</span>}
                        </div>
                      </div>
                      {others.length > 0 && (
                        <div className="flex items-start justify-between gap-2">
                          <span className="shrink-0 text-muted-foreground">기타</span>
                          <div className="flex flex-col items-end gap-0.5 font-mono">
                            {others.map((o) => <span key={o.k}>{o.k}: {o.v}</span>)}
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}
              {!(d.groups ?? []).length && <span className="text-muted-foreground">그룹 정보 없음</span>}
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="break-all text-right font-mono">{value}</span>
    </div>
  );
}

// ── 생성 다이얼로그 — 이름·설명 + 가속기[](클래스·수량·트레잇) ──
// Cyborg 규칙: group 1개 = 장치 1개(개수 1 고정). 수량은 백엔드에서 group 복제로 처리하므로
// 화면은 "가속기 종류 + 수량 + 트레잇"으로 받는다.
type TraitRow = { name: string; policy: "required" | "forbidden" };
type AccRow = { cls: string; custom: string; quantity: string; traits: TraitRow[] };

const KNOWN_CLASSES = [
  { value: "PGPU", label: "PGPU (GPU)" },
  { value: "CUSTOM_AICHIP", label: "CUSTOM_AICHIP (NPU)" },
  { value: "__custom__", label: "직접 입력…" },
];
const newAcc = (): AccRow => ({ cls: "PGPU", custom: "", quantity: "1", traits: [] });

function CreateDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateDeviceProfile();
  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const [accs, setAccs] = React.useState<AccRow[]>(() => [newAcc()]);

  const patchAcc = (ai: number, fn: (a: AccRow) => AccRow) =>
    setAccs((as) => as.map((a, i) => (i === ai ? fn(a) : a)));

  function submit() {
    if (!name.trim()) return toast.error("이름을 입력하세요");
    const accelerators: CreateDeviceProfileInput["accelerators"] = [];
    for (const a of accs) {
      const resourceClass = (a.cls === "__custom__" ? a.custom : a.cls).trim();
      const quantity = Number(a.quantity);
      if (!resourceClass || quantity < 1) return toast.error("가속기 클래스와 1 이상의 수량이 필요합니다");
      const traits = a.traits.map((t) => ({ name: t.name.trim(), policy: t.policy })).filter((t) => t.name);
      accelerators.push({ resourceClass, quantity, traits });
    }
    create.mutate(
      { name: name.trim(), description: desc.trim() || undefined, accelerators },
      {
        onSuccess: (d) => { toast.success(`디바이스 프로파일 ${d.name} 생성됨`); onClose(); },
        onError: (e) => toast.error("생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>디바이스 프로파일 생성</DialogTitle>
          <DialogDescription>Cyborg device-profile. 가속기 종류와 수량(장치 수)·(선택) 트레잇을 지정합니다. 장치 1개당 그룹 1개로 만들어집니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>이름</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="dp-rngd" />
            </div>
            <div className="space-y-1.5">
              <Label>설명 (선택)</Label>
              <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Furiosa RNGD 1장" />
            </div>
          </div>

          {accs.map((a, ai) => (
            <div key={ai} className="overflow-hidden rounded-lg border">
              <div className="flex items-center justify-between border-b bg-muted/40 px-3 py-2">
                <span className="flex items-center gap-1.5 text-xs font-semibold">
                  <Cpu className="size-3.5 text-brand-indigo-600" /> 가속기 {ai + 1}
                </span>
                {accs.length > 1 && (
                  <Button variant="ghost" size="icon-sm" title="가속기 삭제" onClick={() => setAccs((as) => as.filter((_, i) => i !== ai))}>
                    <X className="size-3.5" />
                  </Button>
                )}
              </div>

              <div className="space-y-3 p-3">
                <div className="grid grid-cols-[1fr_7rem] gap-3">
                  <div className="space-y-1.5">
                    <Label className="text-[11px]">리소스 클래스</Label>
                    <Select value={a.cls} onValueChange={(v) => patchAcc(ai, (ar) => ({ ...ar, cls: v }))}>
                      <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                      <SelectContent>{KNOWN_CLASSES.map((c) => <SelectItem key={c.value} value={c.value}>{c.label}</SelectItem>)}</SelectContent>
                    </Select>
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-[11px]">수량(장치 수)</Label>
                    <Input type="number" min={1} className="w-full" value={a.quantity}
                      onChange={(e) => patchAcc(ai, (ar) => ({ ...ar, quantity: e.target.value }))} />
                  </div>
                </div>
                {a.cls === "__custom__" && (
                  <div className="space-y-1.5">
                    <Label className="text-[11px]">클래스명 직접 입력</Label>
                    <Input className="w-full font-mono text-xs" placeholder="CUSTOM_XPU" value={a.custom}
                      onChange={(e) => patchAcc(ai, (ar) => ({ ...ar, custom: e.target.value }))} />
                  </div>
                )}

                {/* 트레잇(고급) */}
                <div className="space-y-2 border-t pt-3">
                  <div className="flex items-center justify-between">
                    <Label className="text-[11px] text-muted-foreground">트레잇 (선택 · 특정 하드웨어 지정)</Label>
                    <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => patchAcc(ai, (ar) => ({ ...ar, traits: [...ar.traits, { name: "", policy: "required" }] }))}>
                      <Plus className="size-3.5" /> 트레잇 추가
                    </Button>
                  </div>
                  {a.traits.map((t, ti) => (
                    <div key={ti} className="grid grid-cols-[1fr_8rem_2rem] items-center gap-2">
                      <Input className="w-full font-mono text-xs" placeholder="CUSTOM_FURIOSA_0001" value={t.name}
                        onChange={(e) => patchAcc(ai, (ar) => ({ ...ar, traits: ar.traits.map((x, i) => i === ti ? { ...x, name: e.target.value } : x) }))} />
                      <Select value={t.policy} onValueChange={(v) => patchAcc(ai, (ar) => ({ ...ar, traits: ar.traits.map((x, i) => i === ti ? { ...x, policy: v as TraitRow["policy"] } : x) }))}>
                        <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="required">required</SelectItem>
                          <SelectItem value="forbidden">forbidden</SelectItem>
                        </SelectContent>
                      </Select>
                      <Button variant="ghost" size="icon-sm" onClick={() => patchAcc(ai, (ar) => ({ ...ar, traits: ar.traits.filter((_, i) => i !== ti) }))}>
                        <X className="size-3.5" />
                      </Button>
                    </div>
                  ))}
                  {a.traits.length === 0 && <p className="text-[11px] text-muted-foreground/70">지정한 트레잇이 없습니다.</p>}
                </div>
              </div>
            </div>
          ))}

          <div className="flex justify-end">
            <Button variant="outline" size="sm" onClick={() => setAccs((as) => [...as, newAcc()])}>
              <Plus className="size-3.5" /> 가속기 추가
            </Button>
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
