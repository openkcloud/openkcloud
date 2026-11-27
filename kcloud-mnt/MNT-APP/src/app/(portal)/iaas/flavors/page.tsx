"use client";
import * as React from "react";
import { Cpu, Plus, Search, Trash2 } from "lucide-react";
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
  useFlavors, useDeviceProfiles, useCreateFlavor, useDeleteFlavor, profileTraits,
  useAccelModels, accelLabelResolver, type FlavorSummary,
} from "@/lib/iaas-api";

// 플레이버 — OpenStack Nova 실연동. 전역(admin) 자원이라 프로젝트 컬럼 없음. 가속기=Cyborg device-profile.
export default function FlavorsPage() {
  const { data, isLoading } = useFlavors();
  const dps = useDeviceProfiles();
  const accelModels = useAccelModels();
  const accelLabel = accelLabelResolver(dps.data, accelModels.data); // 가속기 배지 = dp 이름 → 모델명
  const del = useDeleteFlavor();
  const confirm = useConfirm();
  const [q, setQ] = React.useState("");
  const [createOpen, setCreateOpen] = React.useState(false);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter((f) => !q || f.name.toLowerCase().includes(q.toLowerCase()));

  async function remove(f: FlavorSummary) {
    if (!(await confirm({ title: "플레이버 삭제", destructive: true, confirmLabel: "삭제", description: `${f.name} 을(를) 삭제할까요? 사용 중인 VM에는 영향 없지만 신규 생성에 못 씁니다.` }))) return;
    del.mutate(f.id, {
      onSuccess: () => toast.success(`플레이버 ${f.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="플레이버 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-flavors"]]} />
            <Button size="sm" onClick={() => setCreateOpen(true)}><Plus className="size-3.5" /> 플레이버 생성</Button>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>플레이버</TableHead>
              <TableHead className="text-center">vCPU</TableHead>
              <TableHead className="text-center">RAM</TableHead>
              <TableHead className="text-center">디스크</TableHead>
              <TableHead>가속기</TableHead>
              <TableHead>공개</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((f) => (
              <TableRow key={f.id}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Cpu className="size-3.5 text-brand-indigo-600" />
                    <span className="font-mono text-sm font-semibold">{f.name}</span>
                  </div>
                </TableCell>
                <TableCell className="text-center font-mono tabular-nums">{f.vcpus}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{(f.ramMb / 1024).toFixed(f.ramMb % 1024 ? 1 : 0)}GB</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{f.diskGb}GB</TableCell>
                <TableCell>
                  {f.accelerator ? <Badge variant={f.deviceProfileMissing ? "warning" : "default"} className="font-mono text-[10px]" title={f.accelerator}>{accelLabel(f.accelerator)}</Badge> : <span className="text-muted-foreground">—</span>}
                  {f.deviceProfileMissing && <div className="mt-1 text-[11px] text-amber-700">연결된 디바이스 프로파일이 존재하지 않습니다.</div>}
                </TableCell>
                <TableCell>{f.isPublic ? <Badge variant="success" className="text-[10px]">public</Badge> : <Badge variant="outline" className="text-[10px]">private</Badge>}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(f)}>
                    <Trash2 className="size-3.5 text-rose-600" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow><TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">플레이버가 없습니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <CreateFlavorDialog open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  );
}

function CreateFlavorDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data: dps } = useDeviceProfiles();
  const [name, setName] = React.useState("");
  const [vcpus, setVcpus] = React.useState("2");
  const [ramGb, setRamGb] = React.useState("4");
  const [diskGb, setDiskGb] = React.useState("40");
  const [accel, setAccel] = React.useState("none");
  const create = useCreateFlavor();
  React.useEffect(() => { if (open) { setName(""); setVcpus("2"); setRamGb("4"); setDiskGb("40"); setAccel("none"); } }, [open]);

  function submit() {
    if (!name.trim()) return toast.error("이름을 입력하세요");
    const vc = Number(vcpus), rm = Number(ramGb), dk = Number(diskGb);
    if (!vc || !rm) return toast.error("vCPU/RAM을 입력하세요");
    create.mutate(
      { name: name.trim(), vcpus: vc, ramMb: rm * 1024, diskGb: dk, deviceProfile: accel !== "none" ? accel : undefined },
      {
        onSuccess: (f) => { toast.success(`플레이버 ${f.name} 생성됨`, { description: f.accelerator ? `가속기 ${f.accelerator}` : undefined }); onClose(); },
        onError: (e) => toast.error("플레이버 생성 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>플레이버 생성</DialogTitle>
          <DialogDescription>VM 사양 정의(Nova). 가속기는 Cyborg device-profile로 연결됩니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="m1.large.custom" />
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5"><Label>vCPU</Label><Input type="number" value={vcpus} onChange={(e) => setVcpus(e.target.value)} /></div>
            <div className="space-y-1.5"><Label>RAM (GB)</Label><Input type="number" value={ramGb} onChange={(e) => setRamGb(e.target.value)} /></div>
            <div className="space-y-1.5"><Label>디스크 (GB)</Label><Input type="number" value={diskGb} onChange={(e) => setDiskGb(e.target.value)} /></div>
          </div>
          <div className="space-y-1.5">
            <Label>가속기 (선택)</Label>
            <Select value={accel} onValueChange={setAccel}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="none">없음 (일반)</SelectItem>
                {(dps ?? []).map((d) => {
                  const traits = profileTraits(d.groups);
                  return (
                    <SelectItem key={d.name} value={d.name}>
                      <span className="font-mono">{d.name}</span>
                      {traits.length > 0 && (
                        <span className="ml-2 font-mono text-[10px] text-muted-foreground">{traits.join(", ")}</span>
                      )}
                    </SelectItem>
                  );
                })}
              </SelectContent>
            </Select>
            <p className="text-[10px] text-muted-foreground">가속기 선택 시 flavor extra_spec `accel:device_profile`로 연결됩니다.</p>
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
