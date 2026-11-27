"use client";
import * as React from "react";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ainbFetch } from "@/lib/ainb-api";
import { cn } from "@/lib/utils";
import type { KFInferenceService } from "@/lib/types";

/** 배포된 서비스의 자원(CPU·메모리·GPU) 변경 */
export function ServingResourceDialog({
  service, onClose,
}: {
  service: KFInferenceService | null;
  onClose: () => void;
}) {
  // key로 서비스가 바뀔 때마다 새로 그려서 현재 값으로 초기화한다
  if (!service) return null;
  return <ResourceForm key={service.name} service={service} onClose={onClose} />;
}

function ResourceForm({
  service, onClose,
}: {
  service: KFInferenceService;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [cpu, setCpu] = React.useState(service.cpu || "1");
  const [memoryGi, setMemoryGi] = React.useState(service.memoryGi || 1);
  const [gpu, setGpu] = React.useState(service.gpu ?? 0);
  const [busy, setBusy] = React.useState(false);

  const { data: gpuInfo } = useQuery({
    queryKey: ["kf", "gpu-availability"],
    queryFn: async () => {
      const res = await ainbFetch(`/resources/gpu-availability`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ total: number; used: number; available: number;
                                     maxPerPod: number; maxPerNode: number }>;
    },
    refetchInterval: 10000,
    retry: false,
  });

  const current = service.gpu ?? 0;
  // 이미 이 서비스가 쓰고 있는 GPU는 여유분에 더해서 계산한다
  const freeGpu = gpuInfo ? gpuInfo.maxPerPod + current : null;
  const changed =
    cpu !== (service.cpu || "1") || memoryGi !== (service.memoryGi || 1)
    || gpu !== current;

  async function submit() {
    setBusy(true);
    try {
      const res = await ainbFetch(`/serving/${service.name}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cpu, memoryGi, gpu }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(typeof body.detail === "string" ? body.detail : `${res.status}`);
      }
      toast.success(`"${service.name}" 자원을 변경했습니다`, {
        description: "새 설정으로 서버를 다시 띄웁니다 — 1~2분 뒤 반영됩니다",
      });
      qc.invalidateQueries({ queryKey: ["kf", "serving"] });
      onClose();
    } catch (e) {
      toast.error(`변경 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>자원 변경 — {service.name}</DialogTitle>
          <DialogDescription>
            서비스에 배정할 CPU·메모리·가속기를 바꿉니다. 저장하면 새 설정으로
            서버를 다시 띄우며, 준비될 때까지 기존 서버가 계속 응답합니다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <Label>CPU 한도</Label>
              <Input value={cpu} onChange={(e) => setCpu(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>메모리 (GB)</Label>
              <Input type="number" min={1} max={16} value={memoryGi}
                     onChange={(e) => setMemoryGi(Number(e.target.value) || 1)} />
            </div>
            <div className="space-y-1.5">
              <Label>가속기 (GPU)</Label>
              <Select value={String(gpu)} onValueChange={(v) => setGpu(Number(v))}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">사용 안 함</SelectItem>
                  {Array.from({ length: gpuInfo?.maxPerNode ?? 1 }, (_, i) => i + 1)
                    .map((n) => (
                      <SelectItem key={n} value={String(n)}>{n}장</SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {gpu > 0 && (
            <div className={cn(
              "rounded-md border p-2.5 text-[11px] leading-snug",
              freeGpu !== null && freeGpu < gpu
                ? "border-amber-200 bg-amber-50/70 text-amber-900"
                : "border-brand-mint-200 bg-brand-mint-50/70 text-brand-mint-800",
            )}>
              {freeGpu === null
                ? "가속기 여유를 확인하는 중입니다"
                : freeGpu < gpu
                  ? `쓸 수 있는 GPU가 ${freeGpu}장뿐입니다. 변경은 접수되지만 자리가
                     날 때까지 새 서버가 대기합니다 (기존 서버는 계속 동작).`
                  : `쓸 수 있는 GPU ${freeGpu}장 — 바로 배정됩니다.`}
            </div>
          )}

          {gpu === 0 && current > 0 && (
            <div className="rounded-md border border-amber-200 bg-amber-50/70 p-2.5 text-[11px] leading-snug text-amber-900">
              가속기를 회수합니다. 이 서비스는 CPU로만 추론하게 되어 응답이
              느려질 수 있습니다.
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit} disabled={!changed || busy}>
            {busy ? "변경 중…" : "저장"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
