"use client";
import * as React from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Camera, RotateCcw, Trash2, Cpu } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { ainbFetch } from "@/lib/ainb-api";
import type { KFNotebook } from "@/lib/types";

interface Snapshot { id: string; createdAt: string; sizeMb: number }

/* ─────────────────────────────────────────────────────────
 * 볼륨 스냅샷 — 작업 파일을 시점 백업하고 되돌리기
 * ───────────────────────────────────────────────────────── */
export function SnapshotDialog({
  nb, open, onOpenChange,
}: { nb: KFNotebook | null; open: boolean; onOpenChange: (o: boolean) => void }) {
  const [busy, setBusy] = React.useState(false);
  const qc = useQueryClient();
  const { data, refetch } = useQuery({
    queryKey: ["snapshots", nb?.id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${nb!.id}/snapshots`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: Snapshot[] }>;
    },
    enabled: !!nb && open,
  });

  // 스냅샷 id는 "노트북명/시각.tar.gz" 형태 — 경로로 그대로 전달
  async function call(path: string, method: string, okMsg: string) {
    setBusy(true);
    try {
      const res = await ainbFetch(`/notebooks/${nb!.id}${path}`, { method });
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      toast.success(okMsg);
      refetch();
      qc.invalidateQueries({ queryKey: ["kf", "notebooks"] });
    } catch (e) {
      toast.error(`${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  if (!nb) return null;
  const snaps = data?.items ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Camera className="size-4" /> 볼륨 스냅샷 — {nb.name}
          </DialogTitle>
          <DialogDescription>
            작업공간(/home/jovyan)의 파일을 현재 시점으로 백업하고, 필요할 때 되돌립니다.
            캐시·라이브러리 폴더는 제외됩니다.
          </DialogDescription>
        </DialogHeader>

        <Button size="sm" disabled={busy || nb.status !== "Running"}
                onClick={() => call("/snapshots", "POST", "스냅샷을 만들었습니다")}>
          <Camera className="size-3.5" /> {busy ? "처리 중…" : "지금 스냅샷 만들기"}
        </Button>
        {nb.status !== "Running" && (
          <p className="text-[11px] text-amber-700">노트북이 실행 중일 때만 스냅샷을 만들 수 있습니다.</p>
        )}

        <div className="max-h-64 space-y-2 overflow-y-auto">
          {snaps.length === 0 && (
            <p className="py-6 text-center text-xs text-muted-foreground">
              아직 스냅샷이 없습니다
            </p>
          )}
          {snaps.map((s) => (
            <div key={s.id}
                 className="flex items-center gap-2 rounded-md border border-border px-3 py-2">
              <div className="flex-1">
                <div className="font-mono text-xs">{s.createdAt}</div>
                <div className="text-[10px] text-muted-foreground">{s.sizeMb} MB</div>
              </div>
              <Button size="sm" variant="outline" disabled={busy || nb.status !== "Running"}
                      onClick={() => call(`/snapshots/${s.id}/restore`, "POST",
                                          "스냅샷 시점으로 되돌렸습니다")}>
                <RotateCcw className="size-3" /> 되돌리기
              </Button>
              <Button size="icon-sm" variant="ghost" disabled={busy}
                      onClick={() => call(`/snapshots/${s.id}`, "DELETE", "스냅샷을 삭제했습니다")}>
                <Trash2 className="size-3.5 text-rose-600" />
              </Button>
            </div>
          ))}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>닫기</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ─────────────────────────────────────────────────────────
 * 리소스 변경 — CPU/메모리/GPU 재할당 (재시작 동반, 파일 유지)
 * ───────────────────────────────────────────────────────── */
interface GpuAvailability { total: number; used: number; available: number }

export function ResizeDialog({
  nb, open, onOpenChange,
}: { nb: KFNotebook | null; open: boolean; onOpenChange: (o: boolean) => void }) {
  const [cpu, setCpu] = React.useState("1");
  const [memoryGi, setMemoryGi] = React.useState("2");
  const [gpu, setGpu] = React.useState("0");
  const [busy, setBusy] = React.useState(false);
  const qc = useQueryClient();

  const { data: avail } = useQuery({
    queryKey: ["gpu-availability"],
    queryFn: async () => {
      const res = await ainbFetch(`/resources/gpu-availability`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<GpuAvailability>;
    },
    enabled: open,
  });

  // 대상 노트북이 바뀌면 현재 할당값으로 채운다 — 채운 대상을 기억해 두고 바뀔 때만 (effect 대신 렌더 중 갱신)
  const [filledFor, setFilledFor] = React.useState<KFNotebook | null>(null);
  if (nb && nb !== filledFor) {
    setFilledFor(nb);
    setCpu(nb.cpu);
    setMemoryGi(String(nb.memGb));
    setGpu(String(nb.acceleratorCount));
  }

  if (!nb) return null;
  // 이 노트북이 이미 쓰고 있는 GPU는 다시 쓸 수 있으므로 여유에 더한다
  const selectable = (avail?.available ?? 0) + nb.acceleratorCount;

  async function submit() {
    setBusy(true);
    try {
      const res = await ainbFetch(`/notebooks/${nb!.id}/resources`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          cpu, memoryGi: Number(memoryGi),
          acceleratorKind: "nvidia.com/gpu",
          acceleratorCount: Number(gpu),
        }),
      });
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      toast.success("자원 변경 적용 중 — 노트북이 재시작됩니다 (작업 파일 유지)");
      qc.invalidateQueries({ queryKey: ["kf", "notebooks"] });
      onOpenChange(false);
    } catch (e) {
      toast.error(`${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Cpu className="size-4" /> 리소스 변경 — {nb.name}
          </DialogTitle>
          <DialogDescription>
            CPU·메모리·GPU 할당량을 조정합니다. 적용을 위해 노트북이 한 번 재시작되며,
            작업공간 파일은 그대로 유지됩니다.
          </DialogDescription>
        </DialogHeader>

        <div className="grid grid-cols-3 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="rz-cpu">vCPU</Label>
            <Input id="rz-cpu" value={cpu} onChange={(e) => setCpu(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rz-mem">MEM (Gi)</Label>
            <Input id="rz-mem" value={memoryGi} onChange={(e) => setMemoryGi(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label>GPU {avail && <span className="text-[10px] font-normal text-muted-foreground">(선택 가능 {selectable})</span>}</Label>
            <Select value={gpu} onValueChange={setGpu}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="0">사용 안 함</SelectItem>
                {Array.from({ length: avail?.total ?? 1 }, (_, i) => i + 1).map((n) => (
                  <SelectItem key={n} value={String(n)} disabled={n > selectable}>
                    {n}장{n > selectable ? " (여유 없음)" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        <p className="text-[11px] text-muted-foreground">
          현재 할당: {nb.cpu} vCPU · {nb.memGb} GiB · GPU {nb.acceleratorCount}장
        </p>
        {nb.storageGb > 0 && (
          <p className="text-[11px] leading-snug text-amber-700">
            이 노트북의 작업 볼륨은 특정 장비(노드)에 놓여 있어, GPU도 그 장비에
            남은 것만 쓸 수 있습니다. 다른 장비에 GPU가 남아 있어도 그쪽 GPU는
            쓰지 못해 &ldquo;GPU 부족&rdquo;으로 멈출 수 있습니다.
          </p>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>취소</Button>
          <Button onClick={submit} disabled={busy}>{busy ? "적용 중…" : "변경 적용"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
