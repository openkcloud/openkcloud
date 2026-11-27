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
import { Badge } from "@/components/ui/badge";
import { ainbFetch } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import { cn } from "@/lib/utils";

interface StoredModel {
  name: string; files: number; sizeMb: number; uri: string;
  updatedAt: string; format: string; servable: boolean;
}

/** 모델 형식 — KServe가 지원하는 서버 종류 */
const FORMATS = [
  { value: "sklearn", label: "scikit-learn", hint: "model.joblib / model.pkl" },
  { value: "pytorch", label: "PyTorch (TorchServe)", hint: "model-store/*.mar" },
  { value: "tensorflow", label: "TensorFlow", hint: "saved_model 폴더" },
  { value: "xgboost", label: "XGBoost", hint: "model.bst" },
  { value: "triton", label: "Triton", hint: "모델 저장소 규격" },
];

// 쿠버네티스 이름 규칙 (RFC1123) — 소문자·숫자·하이픈만
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

/** 우리 장비(8~16GB VRAM)에서 실제로 도는 소형 LLM들 */
const LLM_PRESETS = [
  { id: "Qwen/Qwen2.5-0.5B-Instruct", label: "Qwen2.5 0.5B", vram: "약 2GB", util: 0.35, note: "가장 가벼움" },
  { id: "Qwen/Qwen2.5-1.5B-Instruct", label: "Qwen2.5 1.5B", vram: "약 4GB", util: 0.7, note: "품질·속도 균형" },
  { id: "meta-llama/Llama-3.2-1B-Instruct", label: "Llama 3.2 1B", vram: "약 3GB", util: 0.55, note: "" },
  { id: "meta-llama/Llama-3.2-3B-Instruct", label: "Llama 3.2 3B", vram: "약 7GB", util: 0.85, note: "16GB 카드 권장" },
  { id: "google/gemma-2-2b-it", label: "Gemma 2 2B", vram: "약 5GB", util: 0.8, note: "" },
];

export function ServingCreateDialog({
  open, onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [format, setFormat] = React.useState("sklearn");
  const [uri, setUri] = React.useState("");
  const [cpu, setCpu] = React.useState("1");
  const [memoryGi, setMemoryGi] = React.useState(2);
  const [gpu, setGpu] = React.useState(0);
  const [busy, setBusy] = React.useState(false);
  // 어느 워크스페이스에 올릴지 — 내가 볼 수 있는 워크스페이스 중에서 (지금 보고 있는 것이 기본)
  const { choices } = useProjectContext();
  const [picked, setPicked] = React.useState("");
  const namespace = choices.includes(picked) ? picked : (choices[0] ?? "");
  // 배포 유형 — 저장된 모델 파일 vs 대화형 LLM(모델 ID로 불러옴)
  const [kind, setKind] = React.useState<"file" | "llm">("file");
  const [modelId, setModelId] = React.useState(LLM_PRESETS[0].id);
  const [maxModelLen, setMaxModelLen] = React.useState(2048);

  // 가속기 여유 — 없으면 배포는 되지만 대기 상태가 된다
  const { data: gpuInfo } = useQuery({
    queryKey: ["kf", "gpu-availability"],
    queryFn: async () => {
      const res = await ainbFetch(`/resources/gpu-availability`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ total: number; used: number; available: number;
                                     maxPerPod: number; maxPerNode: number }>;
    },
    enabled: open,
    refetchInterval: 10000,
    retry: false,
  });
  // 한 서비스는 장비 한 대 안에서 돌기 때문에 노드 여유가 상한이다
  const freeGpu = gpuInfo?.maxPerPod ?? null;

  const { data: models } = useQuery({
    queryKey: ["kf", "models"],
    queryFn: async () => {
      const res = await ainbFetch(`/models`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: StoredModel[]; bucket: string }>;
    },
    enabled: open,
    retry: false,
  });

  const isLlm = kind === "llm";
  // 모델이 클수록 GPU 메모리를 더 잡아야 뜬다. 목록에 없는 모델은 넉넉히.
  const llmUtil = LLM_PRESETS.find((m) => m.id === modelId)?.util ?? 0.85;
  const nameOk = name === "" || NAME_RE.test(name);
  // vLLM은 GPU 필수 — 없이 배포하면 "Failed to infer device type"으로
  // 무한 재시작한다 (4시간 헛기다린 실사례). 생성 단계에서 막는다.
  const llmNeedsGpu = isLlm && gpu < 1;
  const canSubmit = !!name && nameOk && !busy && !llmNeedsGpu && !!namespace &&
    (isLlm ? !!modelId : !!uri);

  async function submit() {
    setBusy(true);
    try {
      const res = await ainbFetch(`/serving`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(
          isLlm
            ? { name, namespace, modelFormat: "vllm", modelId, maxModelLen,
                gpuMemoryUtilization: llmUtil, cpu, memoryGi, gpu }
            : { name, namespace, modelFormat: format, storageUri: uri,
                cpu, memoryGi, gpu },
        ),
      }, 20000);
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(typeof body.detail === "string" ? body.detail : `${res.status}`);
      }
      toast.success(`"${name}" 배포를 시작했습니다`, {
        description: "준비되기까지 1~3분 걸립니다 (모델 내려받기 + 서버 기동)",
      });
      qc.invalidateQueries({ queryKey: ["kf", "serving"] });
      setName(""); setUri("");
      onClose();
    } catch (e) {
      toast.error(`배포 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>서비스 배포</DialogTitle>
          <DialogDescription>
            저장된 모델을 API 서버로 띄웁니다. 배포되면 다른 프로그램이
            HTTP로 호출해 예측을 받아갈 수 있습니다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>무엇을 배포하나요</Label>
            <div className="grid grid-cols-2 gap-2">
              {([
                ["file", "학습한 모델", "우리가 만든 판정·예측 모델"],
                ["llm", "대화형 LLM", "질문에 답하는 언어 모델"],
              ] as const).map(([k, title, desc]) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => {
                    setKind(k);
                    // LLM은 GPU 필수 — 여유가 있으면 1장을 자동 선택하고,
                    // 메모리 기본값도 LLM이 뜰 수 있는 8GB로 올려준다
                    if (k === "llm") {
                      if (gpu < 1 && (freeGpu === null || freeGpu >= 1)) setGpu(1);
                      if (memoryGi < 8) setMemoryGi(8);
                    }
                  }}
                  className={cn(
                    "rounded-lg border p-2.5 text-left transition-colors",
                    kind === k
                      ? "border-brand-indigo-400 bg-brand-indigo-50"
                      : "border-border hover:bg-secondary/50",
                  )}
                >
                  <div className="text-[13px] font-semibold">{title}</div>
                  <div className="text-[11px] text-muted-foreground">{desc}</div>
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <Label>워크스페이스 (Profile)</Label>
            <Select value={namespace} onValueChange={setPicked}>
              <SelectTrigger><SelectValue placeholder="워크스페이스가 없습니다" /></SelectTrigger>
              <SelectContent>
                {choices.map((n) => (
                  <SelectItem key={n} value={n}>{n}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">
              이 서비스가 쓰는 자원은 여기 배정된 몫에서 차감됩니다.
            </p>
          </div>

          <div className="space-y-1.5">
            <Label>서비스 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)}
                   placeholder="dm-classifier-v2" />
            {!nameOk && (
              <p className="text-[11px] text-rose-600">
                소문자·숫자·하이픈(-)만 쓸 수 있습니다. 밑줄(_)과 대문자는 안 됩니다.
              </p>
            )}
          </div>

          {isLlm && (
            <>
              <div className="space-y-1.5">
                <Label>모델 고르기</Label>
                <Select value={modelId} onValueChange={setModelId}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {LLM_PRESETS.map((m) => (
                      <SelectItem key={m.id} value={m.id}>
                        {m.label} · VRAM {m.vram}{m.note ? ` · ${m.note}` : ""}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Input value={modelId} onChange={(e) => setModelId(e.target.value)}
                       className="font-mono text-[11px]" />
                <p className="text-[11px] text-muted-foreground">
                  HuggingFace 모델 ID입니다. 목록에 없는 모델도 직접 입력할 수 있습니다.
                  모델은 배포할 때 자동으로 내려받습니다(처음 한 번, 수 분 소요).
                  GPU 메모리는 모델에 맞춰 <b>{Math.round(llmUtil * 100)}%</b>를 배정합니다.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label>최대 문맥 길이 (토큰)</Label>
                <Input type="number" min={512} max={32768} step={512}
                       value={maxModelLen}
                       onChange={(e) => setMaxModelLen(Number(e.target.value) || 2048)} />
                <p className="text-[11px] text-muted-foreground">
                  한 번에 다룰 수 있는 글의 길이입니다. 길수록 메모리를 많이 씁니다.
                </p>
              </div>
              <div className="rounded-md border border-sky-200 bg-sky-50/70 p-2.5 text-[11px] leading-snug text-sky-900">
                <b>LLM은 가속기(GPU)가 필요합니다.</b> 아래에서 1장 이상 지정하세요.
                CPU만으로는 실행되지 않습니다.
              </div>
            </>
          )}

          {!isLlm && (
          <div className="space-y-1.5">
            <Label>배포할 모델</Label>
            <Select
              value={uri}
              onValueChange={(v) => {
                setUri(v);
                // 파일 구조로 알아낸 형식을 자동으로 맞춘다
                const m = models?.items.find((x) => x.uri === v);
                if (m?.format) setFormat(m.format);
              }}
            >
              <SelectTrigger>
                <SelectValue placeholder={
                  models?.items.length ? "저장소에서 고르세요" : "저장된 모델이 없습니다"
                } />
              </SelectTrigger>
              <SelectContent>
                {(models?.items ?? []).filter((m) => m.servable).map((m) => (
                  <SelectItem key={m.uri} value={m.uri}>
                    {m.name} · {m.format} · {m.sizeMb}MB · {m.updatedAt}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {uri && (
              <p className="font-mono text-[11px] text-muted-foreground">{uri}</p>
            )}
            <p className="text-[11px] text-muted-foreground">
              바로 배포할 수 있는 모델만 나옵니다
              {models?.bucket ? ` (s3://${models.bucket})` : ""}
            </p>
          </div>
          )}

          {!isLlm && (
          <div className="space-y-1.5">
            <Label>모델 형식</Label>
            <Select value={format} onValueChange={setFormat}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {FORMATS.map((f) => (
                  <SelectItem key={f.value} value={f.value}>
                    {f.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">
              필요한 파일 구조: {FORMATS.find((f) => f.value === format)?.hint}
            </p>
          </div>
          )}

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

          {llmNeedsGpu && (
            <div className="rounded-md border border-red-200 bg-red-50/70 p-2.5 text-[11px] leading-snug text-red-800">
              LLM(vLLM)은 <b>GPU가 반드시 필요합니다</b> — GPU 없이 배포하면
              서버가 뜨지 못하고 계속 재시작합니다. 위에서 GPU를 1장 이상 선택하세요.
            </div>
          )}

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
                  ? `지금 여유 GPU가 ${freeGpu}장뿐입니다. 배포는 등록되지만 자리가 날 때까지
                     대기 상태로 기다립니다 (실패 아님).`
                  : `여유 GPU ${freeGpu}장 — 바로 배정됩니다.`}
            </div>
          )}

          <div className="rounded-md border border-sky-200 bg-sky-50/70 p-2.5 text-[11px] leading-snug text-sky-900">
            <Badge variant="outline" className="mr-1 text-[10px]">참고</Badge>
            모델 형식과 실제 파일 구조가 맞지 않으면 배포는 되지만 상태가
            준비 완료로 바뀌지 않습니다. 그럴 땐 형식을 바꿔 다시 배포하세요.
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit} disabled={!canSubmit}>
            {busy ? "배포 중…" : "배포"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
