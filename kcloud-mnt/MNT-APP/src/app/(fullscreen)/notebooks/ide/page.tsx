"use client";
import * as React from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  ChevronLeft,
  Square,
  RefreshCw,
  TerminalSquare,
  Cpu,
  Activity,
  HardDrive,
  Folder,
  Info,
  AlertTriangle,
  Globe,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui/tabs";
import { Sparkline } from "@/components/viz/sparkline";
import { useNotebookConnect, notebookAction } from "@/lib/notebook-api";
import { useQuery } from "@tanstack/react-query";
import { ainbFetch, ainbSessionOk } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import { toast } from "sonner";
import { cn } from "@/lib/utils";

/**
 * 📐 구조 — CMP Shell + Code Server iframe slot
 *
 * ┌──────────────────────────────────────────────────────┐
 * │  Top Bar (CMP)                                       │
 * ├────────┬─────────────────────────────────┬──────────┤
 * │ Side   │                                 │ Monitor  │
 * │ Panel  │   iframe(connectUrl)            │ (CMP)    │
 * │ (CMP)  │   = 실제 Code Server (Live)     │          │
 * │        │   = mock preview   (Demo)       │          │
 * └────────┴─────────────────────────────────┴──────────┘
 *
 * 실제 Kubeflow 설치 후에는 iframe src 만 변경됩니다.
 * 사이드 패널·헤더·모니터는 그대로 유지 — gap 최소화.
 *
 * 노트북 이름은 실행 중에 생기므로 정적 export 의 동적 경로 대신 쿼리 파라미터로 받는다 (/notebooks/ide/?id=)
 */
export default function Page() {
  return <React.Suspense fallback={<Skeleton className="h-screen" />}><NotebookIdePage /></React.Suspense>;
}

function NotebookIdePage() {
  const id = useSearchParams().get("id") ?? "";
  const [sessionReady, setSessionReady] = React.useState(false);

  // IDE 프록시는 로그인 때 받은 세션 쿠키(okc_session)를 요구한다 — 없으면 다시 로그인하도록 안내
  React.useEffect(() => {
    ainbSessionOk()
      .then((ok) => {
        if (!ok) toast.error("AI 노트북 세션이 없습니다", {
          description: "로그아웃 후 다시 로그인하면 개발환경에 접속할 수 있습니다.",
        });
      })
      .finally(() => setSessionReady(true));
  }, []);

  const { data: nb, isLoading } = useNotebookConnect(id);
  const { canSee, loading: ctxLoading } = useProjectContext();

  async function action(act: "stop" | "restart") {
    await notebookAction(id, act);
    toast.success(`노트북 ${act === "stop" ? "정지" : "재시작"} 요청 완료`);
  }

  if (isLoading || !nb || !sessionReady || ctxLoading) return <Skeleton className="h-screen" />;
  // 내 워크스페이스(관리자는 전체)의 노트북만 연다
  if (!canSee(nb.namespace)) {
    return (
      <div className="flex h-screen w-screen items-center justify-center bg-slate-950 text-sm text-slate-300">
        이 노트북에 접근할 수 없습니다 —{" "}
        <Link href="/ai-development/notebooks" className="ml-1 underline">노트북 목록</Link>
      </div>
    );
  }

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-slate-950 text-slate-100">
      {/* ───── Top Bar (CMP Shell) ───── */}
      <div className="flex h-12 items-center justify-between border-b border-slate-800 bg-slate-900 px-3">
        <div className="flex items-center gap-3">
          <Link
            href="/ai-development/notebooks"
            className="inline-flex items-center gap-1 text-xs text-slate-400 hover:text-white"
          >
            <ChevronLeft className="size-3.5" /> 노트북 목록
          </Link>
          <div className="h-4 w-px bg-slate-700" />
          <div className="flex items-center gap-2">
            <div className="flex size-5 items-center justify-center rounded bg-gradient-to-br from-brand-mint-400 to-brand-indigo-400">
              <TerminalSquare className="size-3 text-white" />
            </div>
            <span className="text-sm font-semibold">openKcloud Code Server</span>
            <span className="text-xs text-slate-400">·</span>
            <span className="font-mono text-xs text-slate-300">
              {nb.namespace}/{nb.name}
            </span>
            <ConnectionBadge mode={nb.connectMode} status={nb.status} />
          </div>
        </div>

        <div className="flex items-center gap-2 text-xs">
          <div className="flex items-center gap-1 rounded border border-slate-700 bg-slate-800 px-2 py-1">
            <Cpu className="size-3 text-brand-mint-400" />
            <span className="font-mono">
              {nb.resources.accelerator
                ? `${nb.resources.accelerator.kind.split("/")[1]?.toUpperCase()} × ${nb.resources.accelerator.count}`
                : "CPU only"}
            </span>
          </div>
          <div className="flex items-center gap-1 rounded border border-slate-700 bg-slate-800 px-2 py-1">
            <Activity className="size-3 text-brand-indigo-400" />
            <span className="font-mono">
              {nb.resources.cpu} vCPU · {nb.resources.memory}
            </span>
          </div>
          <Button
            size="sm"
            variant="outline"
            onClick={() => action("restart")}
            className="border-slate-700 bg-transparent text-slate-300 hover:bg-slate-800 hover:text-white"
          >
            <RefreshCw className="size-3" /> 재시작
          </Button>
          <Button size="sm" variant="destructive" onClick={() => action("stop")}>
            <Square className="size-3" /> 정지
          </Button>
        </div>
      </div>

      {/* ───── Main Body ───── */}
      <div className="flex min-h-0 flex-1">
        <NotebookSidePanel nb={nb} />
        <CodeServerSlot nb={nb} />
        <AcceleratorMonitor nb={nb} />
      </div>

      {/* ───── Status Bar ───── */}
      <div className="flex h-6 items-center justify-between border-t border-slate-800 bg-brand-indigo-700 px-3 text-[10px] text-white">
        <div className="flex items-center gap-3">
          <span className="flex items-center gap-1">
            <span className="size-1.5 animate-pulse rounded-full bg-brand-mint-400" />
            connected via{" "}
            {nb.connectMode === "live"
              ? "Kubeflow Notebook Controller"
              : "Mock Preview"}
          </span>
        </div>
        <div className="flex items-center gap-3 font-mono">
          <span>K8s: {nb.namespace}</span>
          <span>PVC: {nb.workspaceVolume.pvcName}</span>
          <span>Image: {nb.image.split("/").pop()}</span>
        </div>
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────
// Connection badge
// ────────────────────────────────────────────────────────
function ConnectionBadge({
  mode,
  status,
}: {
  mode: "live" | "mock";
  status: "Running" | "Starting" | "Stopped" | "Error";
}) {
  if (status !== "Running")
    return (
      <Badge className="bg-amber-500/20 text-amber-300 gap-1 text-[9px]">
        <span className="size-1.5 animate-pulse rounded-full bg-amber-400" />
        {status.toUpperCase()}
      </Badge>
    );
  if (mode === "live")
    return (
      <Badge className="bg-brand-mint-500/20 text-brand-mint-300 gap-1 text-[9px]">
        <span className="size-1.5 animate-pulse rounded-full bg-brand-mint-400" />
        LIVE
      </Badge>
    );
  return (
    <Badge className="bg-amber-500/20 text-amber-300 gap-1 text-[9px]">
      <span className="size-1.5 rounded-full bg-amber-400" />
      MOCK PREVIEW
    </Badge>
  );
}

// ────────────────────────────────────────────────────────
// Left: Notebook side panel (CMP 고유 — 그대로 유지)
/** 볼륨 카드 — 총 용량(PVC) + 실제 사용량(du 실측, 30초 갱신) */
function VolumeUsageCard({
  nbId, live, pvcName, sizeGb, mountPath,
}: {
  nbId: string; live: boolean; pvcName: string; sizeGb: number; mountPath: string;
}) {
  const { data } = useQuery({
    queryKey: ["ide-nb-volume", nbId],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${nbId}/volume`);
      if (!res.ok) throw new Error(`${res.status}`);
      return (await res.json()) as {
        sizeGb: number; usedHuman: string | null; usedPercent: number | null;
      };
    },
    enabled: live,
    refetchInterval: 30000,
  });
  const totalGb = data?.sizeGb || sizeGb;
  const pct = data?.usedPercent ?? null;
  return (
    <div className="flex items-start gap-2 rounded border border-slate-800 bg-slate-950 p-2">
      <HardDrive className="size-3.5 text-brand-mint-500" />
      <div className="flex-1 space-y-1 text-[10px]">
        <div className="font-mono text-white">{pvcName}</div>
        <div className="text-slate-400">
          {totalGb > 0 ? `${totalGb} GB` : "용량 정보 없음"} · {mountPath}
        </div>
        {data?.usedHuman != null && (
          <>
            <div className="text-slate-300">
              사용 중 <span className="font-mono text-white">{data.usedHuman}</span>
              {pct != null && (
                <span className={cn("ml-1", pct >= 90 ? "text-rose-400" : "text-slate-400")}>
                  ({pct}%)
                </span>
              )}
            </div>
            {pct != null && (
              <div className="h-1 overflow-hidden rounded bg-slate-800">
                <div
                  className={cn("h-full rounded",
                    pct >= 90 ? "bg-rose-500" : pct >= 70 ? "bg-amber-400" : "bg-brand-mint-500")}
                  style={{ width: `${Math.min(100, pct)}%` }}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/** 실측 파일 목록 — live 모드에서만 조회, 데이터 없으면 안내 문구 (mock 목록 없음) */
function WorkspaceFileList({ nbId, live }: { nbId: string; live: boolean }) {
  const { data } = useQuery({
    queryKey: ["ide-nb-files", nbId],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${nbId}/files`);
      if (!res.ok) throw new Error(`${res.status}`);
      const json = (await res.json()) as {
        items: { name: string; modified?: string }[];
      };
      return json.items.filter((f) => !f.name.startsWith("."));
    },
    enabled: live,
    refetchInterval: 15000,
  });
  const files = live ? (data ?? []) : [];
  return (
    <div className="space-y-1">
      <div className="text-[9px] uppercase text-slate-500">워크스페이스 파일</div>
      {files.length === 0 && (
        <div className="px-1.5 py-1 text-[10px] text-slate-500">
          {live ? "파일 정보를 불러오는 중…" : "연결 후 표시됩니다"}
        </div>
      )}
      {files.slice(0, 8).map((f) => (
        <div
          key={f.name}
          className="flex items-center gap-1.5 rounded px-1.5 py-1 text-[10px] hover:bg-slate-800"
        >
          <Folder className="size-3 text-amber-400" />
          <span className="truncate font-mono">{f.name}</span>
        </div>
      ))}
    </div>
  );
}

// ────────────────────────────────────────────────────────
function NotebookSidePanel({
  nb,
}: {
  nb: NonNullable<ReturnType<typeof useNotebookConnect>["data"]>;
}) {
  return (
    <aside className="hidden w-64 flex-col border-r border-slate-800 bg-slate-900 lg:flex">
      <div className="border-b border-slate-800 px-3 py-2 text-[10px] font-bold uppercase tracking-wider text-slate-400">
        Notebook 정보
      </div>
      <Tabs defaultValue="spec" className="flex-1 px-1.5 pt-1.5">
        <TabsList className="grid h-7 w-full grid-cols-3 bg-slate-800 text-[10px]">
          <TabsTrigger
            value="spec"
            className="data-[state=active]:bg-slate-700 data-[state=active]:text-white"
          >
            Spec
          </TabsTrigger>
          <TabsTrigger
            value="pvc"
            className="data-[state=active]:bg-slate-700 data-[state=active]:text-white"
          >
            볼륨
          </TabsTrigger>
          <TabsTrigger
            value="info"
            className="data-[state=active]:bg-slate-700 data-[state=active]:text-white"
          >
            Info
          </TabsTrigger>
        </TabsList>

        <TabsContent value="spec" className="space-y-2 px-1 pt-2">
          <Kv label="Image" value={nb.image} mono small />
          <Kv label="vCPU" value={nb.resources.cpu} mono />
          <Kv label="Memory" value={nb.resources.memory} mono />
          {nb.resources.accelerator && (
            <Kv
              label="가속기"
              value={`${nb.resources.accelerator.kind.split("/")[1]} × ${nb.resources.accelerator.count}`}
              mono
            />
          )}
          <Kv label="Namespace" value={nb.namespace} mono />
          <Kv label="Pod 상태" value={nb.status} mono highlight />
        </TabsContent>

        <TabsContent value="pvc" className="space-y-2 px-1 pt-2">
          <VolumeUsageCard
            nbId={nb.name}
            live={nb.connectMode === "live"}
            pvcName={nb.workspaceVolume.pvcName}
            sizeGb={nb.workspaceVolume.sizeGb}
            mountPath={nb.workspaceVolume.mountPath}
          />
          <WorkspaceFileList nbId={nb.name} live={nb.connectMode === "live"} />
        </TabsContent>

        <TabsContent value="info" className="space-y-2 px-1 pt-2 text-[10px]">
          <div className="rounded border border-amber-700/40 bg-amber-900/20 p-2 text-amber-200">
            <div className="mb-1 flex items-center gap-1 font-semibold">
              <Info className="size-3" /> 연결 모드
            </div>
            <p className="leading-relaxed">
              {nb.connectMode === "live" ? (
                <>Kubeflow Notebook Controller가 발급한 URL로 실제 Code Server에 iframe 연결됩니다.</>
              ) : (
                <>
                  현재는 <strong>Mock Preview</strong> 모드입니다. Kubeflow가
                  설치되면 자동으로 실제 Code Server로 연결됩니다.
                </>
              )}
            </p>
          </div>
          <Kv label="Connect URL" value={nb.connectUrl} mono small />
          {nb.jwtExpiresAt && <Kv label="JWT 만료" value={nb.jwtExpiresAt} mono />}
        </TabsContent>
      </Tabs>
    </aside>
  );
}

// ────────────────────────────────────────────────────────
// Center: Code Server iframe slot ⭐ 핵심
// 실제 구현 시 iframe.src 만 nb.connectUrl 로 바뀝니다.
// ────────────────────────────────────────────────────────
function CodeServerSlot({
  nb,
}: {
  nb: NonNullable<ReturnType<typeof useNotebookConnect>["data"]>;
}) {
  const [forceMock, setForceMock] = React.useState(false);
  const showLive = nb.connectMode === "live" && !forceMock;

  return (
    <div className="relative flex min-w-0 flex-1 flex-col bg-slate-950">
      {/* Slot header */}
      <div className="flex h-7 items-center justify-between border-b border-slate-800 bg-slate-900/60 px-3 text-[10px] text-slate-400">
        <div className="flex items-center gap-2">
          <Globe className="size-3" />
          <span className="font-mono">
            {showLive ? nb.connectUrl : "mock-preview://notebook"}
          </span>
          <Badge variant="outline" className="border-slate-700 text-[9px] text-slate-300">
            {showLive ? "iframe" : "preview"}
          </Badge>
        </div>
        {/* 데모 전환 스위치는 개발용 — 실접속 상태에서는 노출하지 않는다 */}
        {nb.connectMode !== "live" && (
          <div className="flex items-center gap-1.5">
            <span>미리보기 모드</span>
            <Switch checked={forceMock} onCheckedChange={setForceMock} />
          </div>
        )}
      </div>

      {/* Slot body */}
      {showLive ? (
        <iframe
          src={nb.connectUrl}
          className="block h-full w-full border-0"
          title="Code Server"
          /* 실제 구현 시 옵션:
             sandbox="allow-scripts allow-same-origin allow-forms allow-downloads allow-popups"
             referrerPolicy="strict-origin-when-cross-origin"
             allow="clipboard-read; clipboard-write" */
        />
      ) : (
        <MockCodeServerPreview nb={nb} />
      )}
    </div>
  );
}

// ────────────────────────────────────────────────────────
// Mock Preview — 실제 Code Server가 없을 때 보여주는 정적 미리보기
// ────────────────────────────────────────────────────────
function MockCodeServerPreview({
  nb,
}: {
  nb: NonNullable<ReturnType<typeof useNotebookConnect>["data"]>;
}) {
  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Fake VS Code title bar */}
      <div className="flex h-6 items-center gap-2 bg-[#3c3c3c] px-3 text-[10px] text-slate-300">
        <span className="size-2 rounded-full bg-rose-500" />
        <span className="size-2 rounded-full bg-amber-500" />
        <span className="size-2 rounded-full bg-brand-mint-500" />
        <span className="ml-3 font-mono">Visual Studio Code — {nb.name}</span>
      </div>
      <div className="flex min-h-0 flex-1 bg-[#1e1e1e] text-[#d4d4d4]">
        <div className="w-48 shrink-0 border-r border-[#2d2d2d] bg-[#252526] p-2 font-mono text-[10.5px]">
          <div className="mb-1 text-[9px] uppercase tracking-wider text-[#cccccc]/60">
            {nb.workspaceVolume.mountPath.replace("/home/", "")}
          </div>
          <div className="space-y-0.5">
            <div className="flex items-center gap-1">
              <span className="text-amber-400">▾</span> src
            </div>
            <div className="pl-3 text-brand-mint-400">📄 train.py</div>
            <div className="pl-3 text-brand-mint-400">📄 data_loader.py</div>
            <div className="pl-3 text-rose-400">📄 config.yaml</div>
            <div className="flex items-center gap-1">
              <span className="text-amber-400">▸</span> datasets
            </div>
            <div className="flex items-center gap-1">
              <span className="text-amber-400">▸</span> checkpoints
            </div>
            <div className="flex items-center gap-1">
              <span className="text-amber-400">▸</span> logs
            </div>
            <div className="text-slate-400">📄 requirements.txt</div>
            <div className="text-sky-400">📄 README.md</div>
          </div>
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex h-7 items-center gap-1 border-b border-[#2d2d2d] bg-[#2d2d2d] px-2 text-[10px]">
            <div className="rounded-t bg-[#1e1e1e] px-2 py-1 text-brand-mint-400">train.py</div>
            <div className="px-2 py-1 text-slate-400">config.yaml</div>
          </div>
          <pre className="overflow-x-auto p-4 font-mono text-[11px] leading-relaxed text-[#d4d4d4]">
{`# Llama 3.1 8B Korean Finetune on Rebellions ATOM
import torch
from transformers import AutoModelForCausalLM, AutoTokenizer
from peft import LoraConfig, get_peft_model
import rbln_runtime as rbln

device = rbln.get_device(0)
print(f"Using device: {device}, memory: {device.memory_gb}GB")

MODEL = "meta-llama/Llama-3.1-8B-Instruct"
tokenizer = AutoTokenizer.from_pretrained(MODEL)
model = AutoModelForCausalLM.from_pretrained(
    MODEL,
    torch_dtype=torch.bfloat16,
    device_map="cuda:0",
)

lora_config = LoraConfig(
    r=16,
    lora_alpha=32,
    target_modules=["q_proj", "v_proj"],
    lora_dropout=0.05,
)
model = get_peft_model(model, lora_config)
model.print_trainable_parameters()

# Training loop ...`}
          </pre>
        </div>
      </div>

      {/* Notice band */}
      <div className="border-t border-amber-700/40 bg-amber-900/30 px-4 py-2.5 text-[11px] text-amber-200">
        <div className="flex items-start gap-2">
          <AlertTriangle className="size-3.5 mt-0.5 shrink-0" />
          <div>
            <div className="font-semibold">Mock Preview Mode</div>
            <p className="mt-0.5 text-amber-200/80">
              이 영역에 실제 Code Server iframe이 들어갑니다. Kubeflow Notebook
              Controller가 설치되어 백엔드가{" "}
              <code className="mx-1 rounded bg-amber-900/50 px-1 font-mono text-amber-100">
                connectMode: &quot;live&quot;
              </code>
              로 응답하면 자동으로 실제 IDE로 전환됩니다. UI 코드 변경 없음.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────
// Right: Accelerator Monitor (CMP 고유 — 그대로 유지)
// ────────────────────────────────────────────────────────
interface NbGpu {
  node: string;
  podPhase: string;
  allocated: { kind: string; count: number } | null;
  nodeGpu: {
    gpu: string; utilPct: number; memUsedMb: number;
    memTotalMb: number; tempC: number; powerW: number;
  } | null;
}

/** 최근 값들을 모아 스파크라인용 시리즈로 (실측 샘플링) */
function useGpuSamples(id: string) {
  const [hist, setHist] = React.useState<NbGpu["nodeGpu"][]>([]);
  const { data } = useQuery({
    queryKey: ["ide-gpu", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/gpu`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<NbGpu>;
    },
    enabled: !!id,
    refetchInterval: 3000,
  });
  // 새 실측이 오면 이력에 쌓는다 — 직전 값을 기억해 두고 바뀔 때만 (effect 대신 렌더 중 갱신)
  const [seen, setSeen] = React.useState<NbGpu | undefined>();
  if (data !== seen) {
    setSeen(data);
    if (data?.nodeGpu) setHist((p) => [...p.slice(-29), data.nodeGpu]);
  }
  return { data, hist };
}

function AcceleratorMonitor({
  nb,
}: {
  nb: NonNullable<ReturnType<typeof useNotebookConnect>["data"]>;
}) {
  const { data: gpuData, hist } = useGpuSamples(nb.name);
  const hasAcc = !!nb.resources.accelerator;
  const g = gpuData?.nodeGpu;
  // 샘플이 쌓이기 전에는 그래프를 그리지 않는다 — "실측" 배지 아래 가짜 곡선 금지
  const series = (pick: (x: NonNullable<NbGpu["nodeGpu"]>) => number) =>
    hist.filter(Boolean).map((h) => pick(h!));

  return (
    <aside className="hidden w-64 flex-col border-l border-slate-800 bg-slate-900 lg:flex">
      <div className="flex items-center justify-between border-b border-slate-800 px-3 py-2">
        <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400">
          가속기 모니터
        </span>
        {g && (
          <span className="rounded bg-brand-mint-500/20 px-1.5 text-[9px] font-bold text-brand-mint-300">
            실측
          </span>
        )}
      </div>
      <div className="space-y-3 p-3">
        {hasAcc && g ? (
          <>
            <div className="rounded border border-slate-800 bg-slate-950 px-2 py-1.5">
              <div className="truncate text-[10px] font-semibold text-slate-200">{g.gpu}</div>
              <div className="font-mono text-[9px] text-slate-500">
                node/{gpuData?.node} · 할당 {gpuData?.allocated?.count ?? 0}장
              </div>
            </div>
            <MonitorMetric
              label="GPU 활용률"
              value={`${g.utilPct}%`}
              color="#10b981"
              data={series((h) => h.utilPct)}
            />
            <MonitorMetric
              label="VRAM"
              value={`${(g.memUsedMb / 1024).toFixed(1)} / ${(g.memTotalMb / 1024).toFixed(0)} GB`}
              color="#3b82f6"
              data={series((h) => Math.round((h.memUsedMb / h.memTotalMb) * 100))}
            />
            <MonitorMetric
              label="온도"
              value={`${g.tempC}°C`}
              color="#f59e0b"
              data={series((h) => h.tempC)}
            />
            <MonitorMetric
              label="전력"
              value={`${Math.round(g.powerW)} W`}
              color="#a855f7"
              data={series((h) => Math.round(h.powerW))}
            />
            <p className="text-[9px] leading-snug text-slate-500">
              노드 전체 GPU 상태입니다. 다른 작업이 같은 GPU를 쓰면 함께 반영됩니다.
            </p>
          </>
        ) : hasAcc ? (
          <div className="rounded border border-slate-800 bg-slate-950 p-3 text-center text-[10px] text-slate-400">
            GPU 상태를 불러오는 중…
          </div>
        ) : (
          <div className="rounded border border-slate-800 bg-slate-950 p-3 text-center text-xs text-slate-500">
            가속기 없음
          </div>
        )}

        <div className="mt-4 space-y-1.5 rounded border border-slate-800 bg-slate-950 p-2">
          <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400">
            메트릭 소스
          </div>
          <Kv label="Source" value={g ? "nvidia-smi (DaemonSet)" : "-"} mono small />
          <Kv label="Endpoint" value="/api/ainb/notebooks/{id}/gpu" mono small />
          <Kv label="주기" value="3s" mono small />
          <Kv label="Pod 상태" value={gpuData?.podPhase ?? "-"} mono small />
        </div>
      </div>
    </aside>
  );
}

function MonitorMetric({
  label,
  value,
  color,
  data,
}: {
  label: string;
  value: string;
  color: string;
  data: number[];
}) {
  return (
    <div className="rounded border border-slate-800 bg-slate-950 p-2">
      <div className="flex items-baseline justify-between text-[10px]">
        <span className="text-slate-400">{label}</span>
        <span className="font-mono font-semibold text-white">{value}</span>
      </div>
      <div className="-mx-0.5 mt-1">
        <Sparkline data={data} width={216} height={26} color={color} />
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────
function Kv({
  label,
  value,
  mono,
  small,
  highlight,
}: {
  label: string;
  value: string;
  mono?: boolean;
  small?: boolean;
  highlight?: boolean;
}) {
  return (
    <div className="space-y-0.5">
      <div className="text-[9px] uppercase text-slate-500">{label}</div>
      <div
        className={cn(
          "break-all text-[10px]",
          mono && "font-mono",
          small && "text-[9.5px]",
          highlight ? "text-brand-mint-300" : "text-white",
        )}
      >
        {value}
      </div>
    </div>
  );
}
