"use client";
import * as React from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Settings, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { ainbFetch } from "@/lib/ainb-api";

export interface EditableProfile {
  name: string;
  ownerEmail: string;
  cpuQuota: { used: number; limit: number };
  memQuota: { used: number; limit: number };
  accQuota: { used: number; limit: number };
  storageQuota: { used: number; limit: number };
  notebooks?: number;
}

/** Profile 쿼터·소유자 편집 — 사용 중인 양보다 적게 줄이는 것을 막는다 */
export function ProfileEditDialog({
  profile, open, onOpenChange,
}: {
  profile: EditableProfile | null;
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const qc = useQueryClient();
  const [owner, setOwner] = React.useState("");
  const [cpu, setCpu] = React.useState("");
  const [mem, setMem] = React.useState("");
  const [gpu, setGpu] = React.useState("");
  const [storage, setStorage] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  // 편집 대상이 바뀌면 현재 값으로 채운다 — 채운 대상을 기억해 두고 바뀔 때만 (effect 대신 렌더 중 갱신)
  const [filledFor, setFilledFor] = React.useState<EditableProfile | null>(null);
  if (profile && profile !== filledFor) {
    setFilledFor(profile);
    setOwner(profile.ownerEmail === "-" ? "" : profile.ownerEmail);
    setCpu(String(profile.cpuQuota.limit));
    setMem(String(profile.memQuota.limit));
    setGpu(String(profile.accQuota.limit));
    setStorage(String(profile.storageQuota.limit));
  }

  if (!profile) return null;

  // 이미 쓰고 있는 양보다 작게 줄이면 K8s가 거부하므로 미리 막는다
  const tooSmall =
    Number(cpu) < profile.cpuQuota.used ||
    Number(mem) < profile.memQuota.used ||
    Number(gpu) < profile.accQuota.used ||
    Number(storage) < profile.storageQuota.used;

  const invalid =
    [cpu, mem, gpu, storage].some((v) => v === "" || isNaN(Number(v)) || Number(v) < 0) ||
    tooSmall;

  async function submit() {
    setBusy(true);
    try {
      const res = await ainbFetch(`/profiles/${profile!.name}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...(owner ? { ownerEmail: owner } : {}),
          cpu: Number(cpu),
          memoryGi: Number(mem),
          acceleratorCount: Number(gpu),
          storageGi: Number(storage),
        }),
      });
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      toast.success(`"${profile!.name}" 할당량을 변경했습니다`);
      qc.invalidateQueries({ queryKey: ["kf", "profiles"] });
      onOpenChange(false);
    } catch (e) {
      toast.error(`변경 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    const nb = profile!.notebooks ?? 0;
    const msg =
      `"${profile!.name}" 워크스페이스를 삭제합니다.\n\n` +
      `⚠ 이 안의 노트북 ${nb}개와 저장된 데이터가 모두 삭제됩니다.\n` +
      `되돌릴 수 없습니다. 정말 삭제할까요?`;
    if (!window.confirm(msg)) return;
    setBusy(true);
    try {
      const res = await ainbFetch(`/profiles/${profile!.name}`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error(`${res.status}`);
      toast.success(`"${profile!.name}" 삭제 요청 완료`);
      qc.invalidateQueries({ queryKey: ["kf", "profiles"] });
      onOpenChange(false);
    } catch (e) {
      toast.error(`삭제 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Settings className="size-4" /> 워크스페이스 설정 — {profile.name}
          </DialogTitle>
          <DialogDescription>
            팀이 사용할 수 있는 자원의 한도를 조정합니다. 변경 즉시 반영됩니다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="pf-owner">소유자 이메일</Label>
            <Input id="pf-owner" value={owner} placeholder="owner@okestro.com"
                   onChange={(e) => setOwner(e.target.value)} />
          </div>

          <div className="grid grid-cols-4 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="pf-cpu">vCPU</Label>
              <Input id="pf-cpu" value={cpu} onChange={(e) => setCpu(e.target.value)} />
              <p className="text-[10px] text-muted-foreground">사용 {profile.cpuQuota.used}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-mem">MEM (Gi)</Label>
              <Input id="pf-mem" value={mem} onChange={(e) => setMem(e.target.value)} />
              <p className="text-[10px] text-muted-foreground">사용 {profile.memQuota.used}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-gpu">GPU</Label>
              <Input id="pf-gpu" value={gpu} onChange={(e) => setGpu(e.target.value)} />
              <p className="text-[10px] text-muted-foreground">사용 {profile.accQuota.used}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-sto">Storage (Gi)</Label>
              <Input id="pf-sto" value={storage} onChange={(e) => setStorage(e.target.value)} />
              <p className="text-[10px] text-muted-foreground">사용 {profile.storageQuota.used}</p>
            </div>
          </div>

          {tooSmall && (
            <p className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-[11px] text-amber-800">
              현재 사용 중인 양보다 적게 줄일 수 없습니다. 먼저 노트북을 정지하거나 삭제하세요.
            </p>
          )}
        </div>

        <DialogFooter className="sm:justify-between">
          <Button variant="ghost" size="sm" disabled={busy} onClick={remove}
                  className="text-rose-600 hover:bg-rose-50 hover:text-rose-700">
            <Trash2 className="size-3.5" /> 워크스페이스 삭제
          </Button>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>취소</Button>
            <Button onClick={submit} disabled={busy || invalid}>
              {busy ? "적용 중…" : "변경 적용"}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
