"use client";
import * as React from "react";
import { ChangeMark } from "@/components/change-mark";
import { FlaskConical, Target, Plus, Zap, Award, Cpu, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ainbFetch, useExperiments, useServing } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { PageHeader } from "@/components/page-header";
import { ExperimentDetailDialog } from "@/components/experiment-detail-dialog";
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
import type { KFExperiment } from "@/lib/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";

// ─────────────────────────────────────────────────────────────
// Benchmark 시연 데이터 — ETRI 260615 §6.C.iii 반영
// ─────────────────────────────────────────────────────────────
/** 자주 쓰는 평가 기준 — 이름이 학습 코드 출력과 달라지면 실험이 실패하므로
 *  직접 타이핑하지 않고 고르게 한다. objective는 높일지 낮출지의 기본값. */
/** 표에 짧게 표시할 알고리즘 이름 */
const ALGO_LABELS: Record<string, string> = {
  bayesianoptimization: "Bayesian",
  tpe: "TPE",
  random: "Random",
  grid: "Grid",
  hyperband: "Hyperband",
};

const METRIC_PRESETS = [
  { value: "accuracy", label: "accuracy — 정확도 (전체 중 맞힌 비율)", objective: "maximize" as const },
  { value: "val_accuracy", label: "val_accuracy — 검증 정확도", objective: "maximize" as const },
  { value: "loss", label: "loss — 손실 (낮을수록 좋음)", objective: "minimize" as const },
  { value: "val_loss", label: "val_loss — 검증 손실 (낮을수록 좋음)", objective: "minimize" as const },
  { value: "f1", label: "f1 — 정밀도·재현율 균형", objective: "maximize" as const },
  { value: "precision", label: "precision — 정밀도 (오검출 줄이기)", objective: "maximize" as const },
  { value: "recall", label: "recall — 재현율 (놓치지 않기)", objective: "maximize" as const },
  { value: "auc", label: "auc — 판별력 종합 지표", objective: "maximize" as const },
];

/** 처리량은 시험 종류마다 세는 대상이 달라 단위를 함께 적는다.
 *  추론은 초당 토큰, 학습은 초당 이미지·스텝을 센다. */
const HW_BENCHMARKS = [
  { name: "MLPerf Inference 4.1 · Llama-3.1 8B", kind: "추론", hw: "NVIDIA A100 40GB", tokens_s: 3421, unit: "tokens/s", latency_ms: 42, notes: "TP=1, batch=32" },
  { name: "MLPerf Inference 4.1 · Llama-3.1 8B", kind: "추론", hw: "NVIDIA H100 80GB", tokens_s: 8752, unit: "tokens/s", latency_ms: 18, notes: "TP=1, batch=64" },
  { name: "MLPerf Inference 4.1 · Llama-3.1 8B", kind: "추론", hw: "Rebellions REBEL Quad", tokens_s: 5218, unit: "tokens/s", latency_ms: 24, notes: "batch=32, INT8" },
  { name: "MLPerf Inference 4.1 · Llama-3.1 8B", kind: "추론", hw: "Furiosa RNGD × 2", tokens_s: 3892, unit: "tokens/s", latency_ms: 31, notes: "batch=16" },
  { name: "MLPerf Training 3.1 · ResNet-50", kind: "학습", hw: "NVIDIA A100 × 8", tokens_s: 2450, unit: "images/s", latency_ms: 0, notes: "mixed precision" },
  { name: "MLPerf Training 3.1 · BERT-Large", kind: "학습", hw: "NVIDIA H100 × 8", tokens_s: 1820, unit: "steps/s", latency_ms: 0, notes: "TF32" },
];

const MODEL_BENCHMARKS = [
  {
    model: "Llama-3.1 70B",
    bench: "MMLU (5-shot)",
    score: 82.3,
    unit: "%",
    target: 80,
  },
  {
    model: "Llama-3.1 70B",
    bench: "HumanEval",
    score: 78.4,
    unit: "%",
    target: 75,
  },
  {
    model: "Llama-3.1 70B",
    bench: "GSM8K",
    score: 87.1,
    unit: "%",
    target: 85,
  },
  {
    model: "Solar-10.7B",
    bench: "MMLU (5-shot)",
    score: 66.2,
    unit: "%",
    target: 65,
  },
  {
    model: "Qwen2.5-14B",
    bench: "MMLU (5-shot)",
    score: 72.1,
    unit: "%",
    target: 70,
  },
  {
    model: "Qwen2.5-14B",
    bench: "HumanEval",
    score: 71.8,
    unit: "%",
    target: 70,
  },
];

export default function ExperimentsPage() {
  const { data, isLoading } = useExperiments() as {
    data: { items: KFExperiment[] } | undefined;
    isLoading: boolean;
  };
  const [createOpen, setCreateOpen] = React.useState(false);
  const [detail, setDetail] = React.useState<KFExperiment | null>(null);
  const qc = useQueryClient();
  const [busy, setBusy] = React.useState<string | null>(null);

  async function removeExperiment(name: string, namespace: string) {
    if (!window.confirm(`"${name}" 실험을 삭제할까요?\n실행된 시도(Trial) 기록도 함께 사라집니다.`))
      return;
    setBusy(name);
    try {
      const res = await ainbFetch(
        `/experiments/${name}?namespace=${encodeURIComponent(namespace)}`,
        { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(typeof body.detail === "string" ? body.detail : `${res.status}`);
      }
      toast.success(`"${name}" 실험을 삭제했습니다`);
      qc.invalidateQueries({ queryKey: ["kf", "experiments"] });
    } catch (e) {
      toast.error(`삭제 실패: ${(e as Error).message}`);
    } finally {
      setBusy(null);
    }
  }
  const { match } = useProjectContext();

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.items.filter((e) => match(e.namespace));

  return (
    <div className="space-y-4">
      <ExperimentDetailDialog experiment={detail} onClose={() => setDetail(null)} />
      <PageHeader
        title="실험 (HPO)"
        description="Katib 기반 하이퍼파라미터 최적화 실험 + 하드웨어·모델 벤치마크"
        actions={
          <ChangeMark ids={["CR-AID-03"]} inline>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="size-4" /> 실험 정의
            </Button>
          </ChangeMark>
        }
      />

      <Tabs defaultValue="hpo">
        <ChangeMark ids={["CR-AID-04", "CR-AID-05"]} inline>
          <TabsList>
            <TabsTrigger value="hpo">
              HPO 실험 ({items.length})
            </TabsTrigger>
            <TabsTrigger value="hw">하드웨어 벤치마크 (MLPerf)</TabsTrigger>
            <TabsTrigger value="model">모델 성능 벤치마크</TabsTrigger>
          </TabsList>
        </ChangeMark>

        <TabsContent value="hpo">
          <Card>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>실험</TableHead>
                  <TableHead>탐색 방식</TableHead>
                  <TableHead>목표</TableHead>
                  <TableHead>현재 최고치</TableHead>
                  <TableHead>찾아낸 설정값</TableHead>
                  <TableHead>진척률</TableHead>
                  <TableHead>시도</TableHead>
                  <TableHead>상태</TableHead>
                  <TableHead className="w-16 text-right">관리</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((e) => {
                  const isMax = e.objective.includes("maximize");
                  const progress = isMax
                    ? Math.min(100, Math.round((e.bestMetric / e.goal) * 100))
                    : Math.min(100, Math.round((e.goal / e.bestMetric) * 100));
                  return (
                    <TableRow key={e.id}>
                      <TableCell>
                        <button
                          type="button"
                          onClick={() => setDetail(e)}
                          className="flex items-center gap-2 rounded px-1 py-0.5 text-left
                                     transition-colors hover:bg-secondary/60"
                          title="실험 조건과 결과 보기"
                        >
                          <FlaskConical className="size-4 text-amber-600" />
                          <span className="font-medium underline-offset-2 hover:underline">
                            {e.name}
                          </span>
                        </button>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="text-xs">
                            {ALGO_LABELS[e.algorithm ?? ""] ?? e.algorithm ?? "-"}
                          </span>
                          {e.realTraining
                            ? <Badge variant="success" className="text-[10px]">실제 학습</Badge>
                            : <Badge variant="outline" className="text-[10px]">검증용</Badge>}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <Target className="size-3.5 text-muted-foreground" />
                          <span className="text-xs">{e.objective}</span>
                          <Badge
                            variant="outline"
                            className="font-mono text-[10px]"
                          >
                            {e.goal}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell className="font-mono tabular-nums">
                        {e.bestMetric}
                      </TableCell>
                      <TableCell>
                        {/* HPO의 진짜 결과물 — 이 값을 학습 코드에 넣으면 된다 */}
                        {e.bestParams?.length ? (
                          <div className="space-y-0.5">
                            {e.bestParams.map((p) => (
                              <div key={p.name} className="font-mono text-[11px]">
                                <span className="text-muted-foreground">{p.name}</span>
                                {" = "}
                                <b>{/^\d*\.\d{5,}$/.test(p.value)
                                  ? Number(p.value).toPrecision(4)
                                  : p.value}</b>
                              </div>
                            ))}
                          </div>
                        ) : (
                          <span className="text-[11px] text-muted-foreground">탐색 중</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Progress
                            value={progress}
                            className="w-24"
                            indicatorClassName="bg-amber-500"
                          />
                          <span className="font-mono text-xs tabular-nums">
                            {progress}%
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="font-mono tabular-nums">
                        {e.trials}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            e.status === "Succeeded"
                              ? "success"
                              : e.status === "Failed"
                                ? "destructive"
                                : "warning"
                          }
                        >
                          {e.status}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy === e.name}
                          className="text-rose-600 hover:bg-rose-50"
                          onClick={() => removeExperiment(e.name, e.namespace ?? "")}
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </Card>
        </TabsContent>

        <TabsContent value="hw" className="space-y-4">
          <ChangeMark ids={["CR-AID-05"]}>
          <LiveHwBenchmark />
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <Zap className="size-4 text-amber-600" />
                하드웨어 벤치마크 — MLPerf (참고 자료)
              </CardTitle>
              <CardDescription>
                이종 AI 가속기(NVIDIA A100/H100·Rebellions REBEL Quad·Furiosa RNGD)의 표준 벤치마크 결과 비교
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>벤치마크</TableHead>
                    <TableHead>종류</TableHead>
                    <TableHead>하드웨어</TableHead>
                    <TableHead className="text-right">처리량 (Throughput)</TableHead>
                    <TableHead className="text-right">지연 (Latency)</TableHead>
                    <TableHead>시험 조건</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {HW_BENCHMARKS.map((b, i) => (
                    <TableRow key={i}>
                      <TableCell className="text-xs">{b.name}</TableCell>
                      <TableCell>
                        <Badge variant={b.kind === "추론" ? "outline" : "ghost"}
                               className="text-[10px]">
                          {b.kind}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <Cpu className="size-3.5 text-brand-mint-600" />
                          <span className="font-mono text-xs">{b.hw}</span>
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums">
                        {b.tokens_s.toLocaleString()}
                        {/* 시험마다 세는 대상이 달라 단위를 함께 보여준다 */}
                        <span className="ml-1 text-[10px] text-muted-foreground">
                          {b.unit}
                        </span>
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums">
                        {b.latency_ms ? `${b.latency_ms}ms` : "—"}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">{b.notes}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
          </ChangeMark>
        </TabsContent>

        <TabsContent value="model" className="space-y-4">
          <LiveModelBenchmark />
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <Award className="size-4 text-brand-indigo-600" />
                모델 성능 벤치마크 (참고 자료)
              </CardTitle>
              <CardDescription>
                MMLU · HumanEval · GSM8K 등 대표 벤치마크로 모델 품질 비교
              </CardDescription>
            </CardHeader>
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>모델</TableHead>
                    <TableHead>벤치마크</TableHead>
                    <TableHead className="text-right">점수</TableHead>
                    <TableHead>목표 대비</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {MODEL_BENCHMARKS.map((b, i) => (
                    <TableRow key={i}>
                      <TableCell className="font-mono text-xs">
                        {b.model}
                      </TableCell>
                      <TableCell className="text-xs">{b.bench}</TableCell>
                      <TableCell className="text-right font-mono tabular-nums font-semibold">
                        {b.score}
                        {b.unit}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Progress
                            value={Math.min(100, (b.score / b.target) * 100)}
                            className="w-24"
                            indicatorClassName={
                              b.score >= b.target
                                ? "bg-brand-mint-500"
                                : "bg-amber-500"
                            }
                          />
                          <span
                            className={`font-mono text-[10px] tabular-nums ${b.score >= b.target ? "text-brand-mint-700" : "text-amber-600"}`}
                          >
                            목표 {b.target}
                            {b.unit}
                          </span>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <CreateExperimentDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
      />
    </div>
  );
}

// ─────────────────────────────────────────────────────────────
// 실측 하드웨어 벤치마크 — 클러스터 GPU에서 직접 실행 (matmul·대역폭)
// ─────────────────────────────────────────────────────────────
interface HwBenchResult {
  node: string;
  status: string;
  gpu: string;
  fp32Tflops: number | null;
  fp16Tflops: number | null;
  memBwGbs: number | null;
  vramGb: number | null;
}

function LiveHwBenchmark() {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ["bench", "hw"],
    queryFn: async () => {
      const res = await ainbFetch(`/benchmarks`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: HwBenchResult[] }>;
    },
    refetchInterval: 10000,
  });

  async function run() {
    try {
      const res = await ainbFetch(`/benchmarks/run`, { method: "POST" });
      if (!res.ok) throw new Error(`${res.status}`);
      toast.success("벤치마크 실행 시작 — 노드별 GPU Job 생성됨 (약 1분)");
      qc.invalidateQueries({ queryKey: ["bench", "hw"] });
    } catch (e) {
      toast.error(`실행 실패: ${(e as Error).message}`);
    }
  }

  return (
    <Card className="border-brand-mint-200">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle className="flex items-center gap-2 text-base">
            <Zap className="size-4 text-brand-mint-600" />
            클러스터 GPU 실측 벤치마크
            <Badge variant="success" className="text-[10px]">실측</Badge>
          </CardTitle>
          <CardDescription>
            현재 클러스터 가속기에서 직접 실행한 결과 — 행렬곱 연산 성능(TFLOPS)·메모리 대역폭
          </CardDescription>
        </div>
        <Button size="sm" onClick={run}>
          <Zap className="size-3.5" /> 벤치마크 실행
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>GPU</TableHead>
              <TableHead>노드</TableHead>
              <TableHead className="text-right">FP32 (TFLOPS)</TableHead>
              <TableHead className="text-right">FP16 (TFLOPS)</TableHead>
              <TableHead className="text-right">메모리 대역폭</TableHead>
              <TableHead className="text-right">VRAM</TableHead>
              <TableHead>상태</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data?.items ?? []).map((b) => (
              <TableRow key={b.node}>
                <TableCell className="font-mono text-xs">{b.gpu}</TableCell>
                <TableCell className="font-mono text-xs">{b.node}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">{b.fp32Tflops ?? "—"}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">{b.fp16Tflops ?? "—"}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">{b.memBwGbs ? `${b.memBwGbs} GB/s` : "—"}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">{b.vramGb ? `${b.vramGb} GB` : "—"}</TableCell>
                <TableCell>
                  <Badge variant={b.status === "Done" ? "success" : b.status === "Failed" ? "destructive" : "warning"}>
                    {b.status}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
            {(data?.items ?? []).length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-xs text-muted-foreground">
                  아직 실측 결과가 없습니다 — &quot;벤치마크 실행&quot;을 눌러주세요
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

// ─────────────────────────────────────────────────────────────
// 실측 모델 성능 벤치마크 — 배포된 InferenceService 부하 테스트
// ─────────────────────────────────────────────────────────────
interface ServingItem { id: string; name: string; status: string; modelName: string; namespace?: string }
interface ModelBenchResult {
  name: string; requests: number; concurrency: number; rps: number;
  avgMs: number; p50Ms: number; p95Ms: number; p99Ms: number;
  errorRate: number; at: string;
}

function LiveModelBenchmark() {
  const qc = useQueryClient();
  const [running, setRunning] = React.useState<string | null>(null);
  const { data: servings } = useServing() as {
    data: { items: ServingItem[] } | undefined;
  };
  const { match } = useProjectContext();
  const { data: results } = useQuery({
    queryKey: ["bench", "model"],
    queryFn: async () => {
      const res = await ainbFetch(`/benchmarks/model`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: ModelBenchResult[] }>;
    },
  });
  // 위에서 고른 프로젝트에 배포된 모델만
  const ready = (servings?.items ?? []).filter((s) => s.status === "Ready" && match(s.namespace));
  const byName = new Map((results?.items ?? []).map((r) => [r.name, r]));

  async function run(name: string) {
    setRunning(name);
    try {
      const res = await ainbFetch(
        `/benchmarks/model/${name}/run`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ requests: 100, concurrency: 10 }),
        },
      );
      if (!res.ok) throw new Error(`${res.status}`);
      const r = (await res.json()) as ModelBenchResult;
      toast.success(`${name} 부하 테스트 완료 — ${r.rps} RPS · p95 ${r.p95Ms}ms`);
      qc.invalidateQueries({ queryKey: ["bench", "model"] });
    } catch (e) {
      toast.error(`테스트 실패: ${(e as Error).message}`);
    } finally {
      setRunning(null);
    }
  }

  return (
    <Card className="border-brand-indigo-200">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Target className="size-4 text-brand-indigo-600" />
          배포 모델 부하 테스트
          <Badge variant="success" className="text-[10px]">실측</Badge>
        </CardTitle>
        <CardDescription>
          현재 Serving 중인 모델에 100회 동시 호출(동시성 10) — 처리량·지연 분포·오류율 실측
        </CardDescription>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>모델</TableHead>
              <TableHead className="text-right">RPS</TableHead>
              <TableHead className="text-right">p50</TableHead>
              <TableHead className="text-right">p95</TableHead>
              <TableHead className="text-right">p99</TableHead>
              <TableHead className="text-right">오류율</TableHead>
              <TableHead className="text-right">측정 시각</TableHead>
              <TableHead className="w-28"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ready.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-6 text-center text-xs text-muted-foreground">
                  Ready 상태의 배포 모델이 없습니다 — Serving 탭에서 먼저 배포하세요
                </TableCell>
              </TableRow>
            )}
            {ready.map((s) => {
              const r = byName.get(s.name);
              return (
                <TableRow key={s.id}>
                  <TableCell>
                    <span className="font-mono text-xs">{s.name}</span>
                    <span className="ml-1 text-[10px] text-muted-foreground">({s.modelName})</span>
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{r?.rps ?? "—"}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{r ? `${r.p50Ms}ms` : "—"}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{r ? `${r.p95Ms}ms` : "—"}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{r ? `${r.p99Ms}ms` : "—"}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {r ? `${(r.errorRate * 100).toFixed(1)}%` : "—"}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs">{r?.at ?? "—"}</TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="outline" disabled={running === s.name}
                            onClick={() => run(s.name)}>
                      {running === s.name ? "측정 중…" : "부하 테스트"}
                    </Button>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

// ─────────────────────────────────────────────────────────────
// 실험 정의 다이얼로그 — ETRI 260615 §6.C.i 반영
// ─────────────────────────────────────────────────────────────
function CreateExperimentDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [name, setName] = React.useState("");
  // 내가 볼 수 있는 워크스페이스 중에서 (지금 보고 있는 것이 기본)
  const { choices: workspaces } = useProjectContext();
  const [picked, setPicked] = React.useState("");
  const workspace = workspaces.includes(picked) ? picked : (workspaces[0] ?? "");
  const [objective, setObjective] = React.useState<"maximize" | "minimize">("maximize");
  const [metric, setMetric] = React.useState("accuracy");
  const [goal, setGoal] = React.useState("0.95");
  const [maxTrials, setMaxTrials] = React.useState("50");
  const [algorithm, setAlgorithm] = React.useState<
    "random" | "bayesian" | "grid" | "hyperband" | "tpe"
  >("bayesian");

  const qc = useQueryClient();
  const [busy, setBusy] = React.useState(false);

  // K8s 리소스 이름 규칙 — 노트북과 동일 (소문자·숫자·하이픈)
  const validName = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(name);
  const nameError =
    name.trim() === ""
      ? ""
      : /[A-Z]/.test(name)
        ? "대문자는 사용할 수 없습니다 — 소문자로 입력하세요"
        : /[^a-z0-9-]/.test(name)
          ? "영문 소문자·숫자·하이픈(-)만 사용할 수 있습니다 (밑줄·한글·공백 불가)"
          : !validName
            ? "이름은 영문/숫자로 시작하고 끝나야 합니다"
            : "";

  const submit = async () => {
    if (!validName || !workspace) return;
    setBusy(true);
    try {
      const algoMap: Record<string, string> = {
        bayesian: "bayesianoptimization", tpe: "tpe", random: "random",
        grid: "grid", hyperband: "hyperband",
      };
      const res = await ainbFetch(`/experiments`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name,
          namespace: workspace,
          metric,
          objectiveType: objective,
          goal: Number(goal),
          algorithm: algoMap[algorithm],
          maxTrials: Number(maxTrials),
          parallelTrials: 2,
        }),
      });
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
      toast.success(`HPO 실험 '${name}' 시작 — Trial 실행 중`);
      qc.invalidateQueries({ queryKey: ["kf", "experiments"] });
      setName("");
      onClose();
    } catch (e) {
      toast.error(`실험 등록 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>실험 정의 (Hyperparameter Optimization)</DialogTitle>
          <DialogDescription>
            Katib 기반 HPO 실험을 새로 정의한다. 파이프라인의 파라미터 공간을
            탐색해 최적값을 찾음.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div>
            <Label>실험 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)}
                   placeholder="lora-lr-tuning-v3 (소문자·숫자·하이픈)"
                   aria-invalid={!!nameError} />
            {nameError ? (
              <p className="mt-1 text-[11px] text-rose-600">{nameError}</p>
            ) : name.trim() === "" ? (
              <p className="mt-1 text-[11px] text-muted-foreground">
                이름을 입력하면 등록 버튼이 활성화됩니다
              </p>
            ) : null}
          </div>
          <div>
            <Label>실행 워크스페이스</Label>
            <Select value={workspace} onValueChange={setPicked}>
              <SelectTrigger><SelectValue placeholder="워크스페이스가 없습니다" /></SelectTrigger>
              <SelectContent>
                {workspaces.map((w) => (
                  <SelectItem key={w} value={w}>{w}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="mt-1 text-[11px] text-muted-foreground">
              선택한 워크스페이스의 자원으로 Trial이 실행됩니다
            </p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>탐색 알고리즘</Label>
              <Select
                value={algorithm}
                onValueChange={(v) => setAlgorithm(v as typeof algorithm)}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="bayesian">
                    Bayesian Optimization
                  </SelectItem>
                  <SelectItem value="tpe">
                    TPE (Tree-structured Parzen)
                  </SelectItem>
                  <SelectItem value="random">Random Search</SelectItem>
                  <SelectItem value="grid">Grid Search</SelectItem>
                  <SelectItem value="hyperband">Hyperband</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>최대 시도 횟수</Label>
              <Input
                type="number"
                value={maxTrials}
                onChange={(e) => setMaxTrials(e.target.value)}
                min="1"
              />
            </div>
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <Label>목표 유형</Label>
              <Select
                value={objective}
                onValueChange={(v) =>
                  setObjective(v as "maximize" | "minimize")
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="maximize">Maximize</SelectItem>
                  <SelectItem value="minimize">Minimize</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>메트릭</Label>
              <Select
                value={METRIC_PRESETS.some((m) => m.value === metric) ? metric : "custom"}
                onValueChange={(v) => {
                  if (v === "custom") return;
                  setMetric(v);
                  // 손실(loss)은 낮출수록 좋고 나머지는 높일수록 좋다
                  const p = METRIC_PRESETS.find((m) => m.value === v);
                  if (p) setObjective(p.objective);
                }}
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {METRIC_PRESETS.map((m) => (
                    <SelectItem key={m.value} value={m.value}>{m.label}</SelectItem>
                  ))}
                  <SelectItem value="custom">직접 입력</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>목표값</Label>
              <Input
                value={goal}
                onChange={(e) => setGoal(e.target.value)}
                className="font-mono text-xs"
              />
            </div>
          </div>
          {!METRIC_PRESETS.some((m) => m.value === metric) && (
            <div className="space-y-1">
              <Label>메트릭 이름 직접 입력</Label>
              <Input value={metric} onChange={(e) => setMetric(e.target.value)}
                     placeholder="예: val_f1" className="font-mono text-xs" />
              <p className="text-[11px] text-muted-foreground">
                학습 코드가 출력하는 이름과 <b>정확히 같아야</b> 합니다
                (예: <code>accuracy=0.95</code>로 찍으면 <code>accuracy</code>).
              </p>
            </div>
          )}
          <div className="rounded-md border border-amber-200 bg-amber-50/60 p-2 text-[11px] text-amber-900">
            파라미터 공간(learning rate, batch size 등)은 다음 단계에서 지정한다
            — 여기선 실험 뼈대만 등록.
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit} disabled={busy || !validName || !workspace}>
            {busy ? "등록 중…" : "실험 등록"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
