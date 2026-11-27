"use client";
import * as React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { createNotebook } from "@/lib/notebook-api";
import { ainbFetch } from "@/lib/ainb-api";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@/lib/utils";

/** 실제 pull 가능한 노트북 이미지 템플릿 (Kubeflow 공식 이미지) */
export const NOTEBOOK_TEMPLATES = [
  {
    key: "codeserver",
    label: "Code Server (VSCode)",
    image: "kubeflownotebookswg/codeserver-python:v1.9.2",
    hint: "VSCode 기반 클라우드 IDE — Python",
  },
  {
    key: "jupyter",
    label: "JupyterLab (SciPy)",
    image: "kubeflownotebookswg/jupyter-scipy:v1.9.2",
    hint: "Notebook 서버 — Python 3.11 + SciPy",
  },
  {
    key: "cuda",
    label: "JupyterLab PyTorch CUDA",
    image: "kubeflownotebookswg/jupyter-pytorch-cuda-full:v1.9.2",
    hint: "GPU 학습용 — PyTorch + CUDA (이미지 큼, 최초 생성 오래 걸림)",
  },
] as const;

export interface NotebookPreset {
  template: string;
  cpu?: string;
  memoryGi?: number;
  gpu?: number;
  storageGb?: number;
}

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  namespaces: string[];
  /** IMAGE_CATALOG 카드에서 선택한 프리셋 (템플릿 + 권장 자원) */
  preset?: NotebookPreset;
}

interface GpuAvailability {
  total: number;
  used: number;
  available: number;
  /** 한 워크로드가 요청할 수 있는 최대 장수 — 노드 한 대의 여유가 상한이다 */
  maxPerPod: number;
  maxPerNode: number;
  perNode: { node: string; total: number; used: number; free: number }[];
  holders: { name: string; namespace: string; node?: string; count: number }[];
}

interface Quota { used: number; limit: number }
interface ProfileQuota {
  name: string;
  cpuQuota: Quota;
  memQuota: Quota;
  accQuota: Quota;
  storageQuota: Quota;
}

export function NotebookCreateDialog({ open, onOpenChange, namespaces, preset }: Props) {
  const { data: avail } = useQuery({
    queryKey: ["gpu-availability"],
    queryFn: async () => {
      const res = await ainbFetch(`/resources/gpu-availability`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<GpuAvailability>;
    },
    enabled: open,
    refetchInterval: open ? 5000 : false,
  });
  // 워크스페이스(Profile)에 배정된 한도 — 초과하면 만들어져도 파드가 뜨지 않는다
  const { data: profiles } = useQuery({
    queryKey: ["kf", "profiles", "quota"],
    queryFn: async () => {
      const res = await ainbFetch(`/profiles`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: ProfileQuota[] }>;
    },
    enabled: open,
    refetchInterval: open ? 10000 : false,
  });
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  // 목록(워크스페이스)이 나중에 오거나 바뀌어도 고를 수 있는 값만 쓴다 — 없으면 첫 번째
  const [picked, setPicked] = React.useState("");
  const namespace = namespaces.includes(picked) ? picked : (namespaces[0] ?? "");
  const [template, setTemplate] = React.useState<string>(preset?.template ?? "codeserver");
  const [cpu, setCpu] = React.useState("1");
  const [memoryGi, setMemoryGi] = React.useState("2");
  const [gpu, setGpu] = React.useState("0");
  // 볼륨 기본 20GB — 0이면 노트북 재시작 시 작업 파일이 모두 사라진다
  const [storageGb, setStorageGb] = React.useState("20");

  // 템플릿 카드에서 고른 프리셋이 바뀌면 값을 채운다 — 적용한 프리셋을 기억해 두고 바뀔 때만 (effect 대신 렌더 중 갱신)
  const [appliedPreset, setAppliedPreset] = React.useState<NotebookPreset | undefined>();
  if (preset && preset !== appliedPreset) {
    setAppliedPreset(preset);
    setTemplate(preset.template);
    if (preset.cpu) setCpu(preset.cpu);
    if (preset.memoryGi) setMemoryGi(String(preset.memoryGi));
    if (preset.gpu !== undefined) setGpu(String(preset.gpu));
    if (preset.storageGb !== undefined) setStorageGb(String(preset.storageGb));
  }

  const create = useMutation({
    mutationFn: createNotebook,
    onSuccess: () => {
      toast.success(`노트북 "${name}" 생성 요청 완료 — 프로비저닝 중`);
      qc.invalidateQueries({ queryKey: ["kf", "notebooks"] });
      onOpenChange(false);
      setName("");
    },
    onError: (e: Error) => toast.error(`생성 실패: ${e.message}`),
  });

  // 고른 워크스페이스의 남은 몫 — 이 범위를 넘으면 파드가 뜨지 못한다
  const prof = profiles?.items.find((p) => p.name === namespace);
  const left = prof && {
    cpu: prof.cpuQuota.limit - prof.cpuQuota.used,
    mem: prof.memQuota.limit - prof.memQuota.used,
    gpu: prof.accQuota.limit - prof.accQuota.used,
    storage: prof.storageQuota.limit - prof.storageQuota.used,
  };
  // GPU는 워크스페이스 몫과 클러스터 여유 중 작은 쪽까지만 가능하다
  // 노드 한 대가 감당할 수 있는 만큼만 고를 수 있다.
  // (클러스터에 2장이 남아도 서로 다른 장비에 있으면 2장짜리는 못 뜬다)
  const gpuMax = Math.min(avail?.maxPerPod ?? 0, left ? Math.floor(left.gpu) : 0);
  // 프리셋이 GPU 1장인데 여유가 없는 경우 등 — 고를 수 있는 범위로 낮춰서 쓴다
  const gpuVal = Math.min(Number(gpu) || 0, gpuMax);

  const overs: string[] = [];
  if (left) {
    if (Number(cpu) > left.cpu) overs.push(`vCPU ${cpu}개 (남은 몫 ${left.cpu}개)`);
    if (Number(memoryGi) > left.mem) overs.push(`메모리 ${memoryGi}GB (남은 몫 ${left.mem}GB)`);
    if (Number(storageGb) > left.storage) overs.push(`볼륨 ${storageGb}GB (남은 몫 ${left.storage}GB)`);
    if (gpuVal > left.gpu) overs.push(`GPU ${gpuVal}장 (남은 몫 ${left.gpu}장)`);
  }
  const quotaError = overs.length > 0;

  const tpl = NOTEBOOK_TEMPLATES.find((t) => t.key === template) ?? NOTEBOOK_TEMPLATES[0];
  const valid = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(name);
  // 이름이 왜 안 되는지 사용자에게 알려준다 (버튼만 비활성이면 원인을 알 수 없음)
  const nameError =
    name.trim() === ""
      ? ""
      : /[A-Z]/.test(name)
        ? "대문자는 사용할 수 없습니다 — 소문자로 입력하세요"
        : /[^a-z0-9-]/.test(name)
          ? "영문 소문자·숫자·하이픈(-)만 사용할 수 있습니다 (한글·공백·밑줄 불가)"
          : !valid
            ? "이름은 영문/숫자로 시작하고 끝나야 합니다"
            : "";

  function submit() {
    create.mutate({
      name,
      namespace,
      image: tpl.image,
      cpu,
      memoryGi: Number(memoryGi),
      acceleratorKind: gpuVal > 0 ? "nvidia.com/gpu" : undefined,
      acceleratorCount: gpuVal,
      storageGb: Number(storageGb),
    });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>새 노트북 서버</DialogTitle>
          <DialogDescription>
            Notebook CRD가 생성되면 컨트롤러가 Pod·Service를 프로비저닝합니다
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="nb-name">이름</Label>
            <Input
              id="nb-name"
              placeholder="예: my-training-nb (소문자·숫자·하이픈)"
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={!!nameError}
            />
            {nameError ? (
              <p className="text-[11px] text-rose-600">{nameError}</p>
            ) : name.trim() === "" ? (
              <p className="text-[11px] text-muted-foreground">
                이름을 입력하면 생성 버튼이 활성화됩니다
              </p>
            ) : null}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>워크스페이스 (프로젝트)</Label>
              <Select value={namespace} onValueChange={setPicked}>
                <SelectTrigger><SelectValue placeholder="워크스페이스가 없습니다" /></SelectTrigger>
                <SelectContent>
                  {namespaces.map((n) => (
                    <SelectItem key={n} value={n}>{n}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>이미지 템플릿</Label>
              <Select value={template} onValueChange={setTemplate}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {NOTEBOOK_TEMPLATES.map((t) => (
                    <SelectItem key={t.key} value={t.key}>{t.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">{tpl.hint}</p>
          {left && (
            <div className="rounded-md border border-border bg-secondary/40 px-3 py-2 text-[11px]">
              <b>{namespace}</b> 워크스페이스에 남은 몫 —
              vCPU <b>{left.cpu}</b>개 · 메모리 <b>{left.mem}</b>GB ·
              GPU <b>{left.gpu}</b>장 · 볼륨 <b>{left.storage}</b>GB
              <div className="mt-0.5 text-muted-foreground">
                이 범위를 넘으면 노트북이 만들어져도 실행되지 않습니다.
              </div>
            </div>
          )}
          {avail && (
            <div className={cn(
              "rounded-md border px-3 py-2 text-[11px]",
              avail.available > 0
                ? "border-brand-mint-200 bg-brand-mint-50 text-brand-mint-800"
                : "border-amber-200 bg-amber-50 text-amber-800",
            )}>
              {avail.available > 0 ? (
                <>
                  클러스터 GPU <b>{avail.available}장 여유</b> (전체 {avail.total}장 중 {avail.used}장 사용 중)
                  {avail.perNode.length > 1 && (
                    <div className="mt-0.5">
                      {avail.perNode.map((n) => `${n.node} ${n.free}/${n.total}장`).join(" · ")}
                      {" — "}한 노트북은 장비 한 대 안에서만 돌기 때문에
                      <b> 최대 {avail.maxPerPod}장</b>까지 지정할 수 있습니다.
                    </div>
                  )}
                </>
              ) : (
                <>
                  <b>GPU {avail.total}장 모두 사용 중</b> — 지금은 CPU로만 만들 수 있습니다.
                </>
              )}
            </div>
          )}
          <div className="grid grid-cols-4 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="nb-cpu">vCPU</Label>
              <Input id="nb-cpu" value={cpu} onChange={(e) => setCpu(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="nb-mem">MEM (Gi)</Label>
              <Input id="nb-mem" value={memoryGi} onChange={(e) => setMemoryGi(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>GPU {avail && <span className="text-[10px] font-normal text-muted-foreground">({avail.available}/{avail.total} 여유)</span>}</Label>
              <Select value={String(gpuVal)} onValueChange={setGpu}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">사용 안 함</SelectItem>
                  {Array.from({ length: avail?.maxPerNode ?? 1 }, (_, i) => i + 1).map((n) => (
                    <SelectItem key={n} value={String(n)} disabled={!!avail && n > gpuMax}>
                      {n}장 요청
                      {avail && n > gpuMax
                        ? (left && n > left.gpu ? " (워크스페이스 몫 초과)" : " (여유 없음)")
                        : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="nb-vol">볼륨 (GB)</Label>
              <Input id="nb-vol" value={storageGb} onChange={(e) => setStorageGb(e.target.value)} />
            </div>
          </div>
          <div className={cn(
            "rounded-md border px-3 py-2 text-[11px]",
            Number(storageGb) > 0
              ? "border-brand-mint-200 bg-brand-mint-50 text-brand-mint-800"
              : "border-rose-200 bg-rose-50 text-rose-800",
          )}>
            {Number(storageGb) > 0 ? (
              <>영구 볼륨 <b>{storageGb}GB</b>가 <code>/home/jovyan</code>에 연결됩니다 — 정지·재시작·장애에도 작업 파일이 보존됩니다.</>
            ) : (
              <><b>⚠ 볼륨 없음 — 작업 파일이 저장되지 않습니다.</b> 노트북을 정지하거나 자원을 변경하면
                업로드한 데이터와 작성한 코드가 모두 사라집니다. 실습·학습용이라면 20GB 이상을 권장합니다.</>
            )}
          </div>
          {quotaError && (
            <div className="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-[11px] text-rose-800">
              <b>워크스페이스에 배정된 몫을 넘습니다</b> — {overs.join(", ")}.
              <div className="mt-0.5">
                값을 줄이거나, Profiles 화면에서 <b>{namespace}</b>의 한도를 늘린 뒤 다시 시도하세요.
              </div>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>취소</Button>
          <Button onClick={submit} disabled={!valid || !namespace || quotaError || create.isPending}>
            {create.isPending ? "생성 중…" : "생성"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
