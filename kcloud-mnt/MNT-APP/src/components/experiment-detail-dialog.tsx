"use client";
import * as React from "react";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import type { KFExperiment } from "@/lib/types";

/** 탐색 알고리즘 — 한 줄 설명 (설명서와 같은 표현) */
const ALGO_HINT: Record<string, string> = {
  bayesianoptimization: "앞선 결과를 보고 유망한 쪽으로 좁혀갑니다",
  tpe: "잘 나온 자리를 닮은 곳을 우선 시험합니다",
  random: "무작위로 골라 시험합니다",
  grid: "가능한 조합을 빠짐없이 시험합니다",
  hyperband: "가망 없는 조합은 일찍 포기하고 유망한 쪽에 집중합니다",
};

const ALGO_LABEL: Record<string, string> = {
  bayesianoptimization: "Bayesian Optimization",
  tpe: "TPE",
  random: "Random Search",
  grid: "Grid Search",
  hyperband: "Hyperband",
};

/** 아주 긴 소수는 읽기 좋게 줄인다 */
function pretty(v: string) {
  return /^\d*\.\d{5,}$/.test(v) ? Number(v).toPrecision(4) : v;
}

export function ExperimentDetailDialog({
  experiment, onClose,
}: {
  experiment: KFExperiment | null;
  onClose: () => void;
}) {
  if (!experiment) return null;
  const e = experiment;
  const isMax = e.objective.includes("maximize");
  const progress = isMax
    ? Math.min(100, Math.round((e.bestMetric / e.goal) * 100))
    : Math.min(100, Math.round((e.goal / (e.bestMetric || 1)) * 100));

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-2">
            {e.name}
            <Badge variant={e.status === "Succeeded" ? "success"
                          : e.status === "Failed" ? "destructive" : "warning"}>
              {e.status}
            </Badge>
            {e.realTraining
              ? <Badge variant="success" className="text-[10px]">실제 학습</Badge>
              : <Badge variant="outline" className="text-[10px]">계산식 검증용</Badge>}
          </DialogTitle>
          <DialogDescription>
            어떤 조건으로 돌렸고, 무엇을 찾아냈는지 보여줍니다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* ── 찾아낸 값 (가장 중요) ── */}
          <div className="rounded-lg border border-brand-mint-200 bg-brand-mint-50/60 p-3">
            <div className="text-[11px] font-semibold uppercase text-brand-mint-800">
              찾아낸 설정값 — 학습 코드에 넣어 쓰는 값
            </div>
            {e.bestParams?.length ? (
              <div className="mt-1.5 space-y-1">
                {e.bestParams.map((p) => (
                  <div key={p.name} className="font-mono text-sm">
                    {p.name} = <b>{pretty(p.value)}</b>
                  </div>
                ))}
                <div className="pt-1 text-[11px] text-muted-foreground">
                  이 조합일 때 {e.objective.replace("maximize", "").replace("minimize", "").trim()}{" "}
                  <b>{e.bestMetric}</b> 를 얻었습니다
                  {e.bestTrial && <> · 시도 <span className="font-mono">{e.bestTrial}</span></>}
                </div>
              </div>
            ) : (
              <div className="mt-1 text-sm text-muted-foreground">아직 탐색 중입니다</div>
            )}
          </div>

          {/* ── 실험 조건 ── */}
          <div>
            <div className="mb-1.5 text-[11px] font-semibold uppercase text-muted-foreground">
              실험 조건 (정의할 때 지정한 값)
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <Row label="탐색 알고리즘"
                   value={ALGO_LABEL[e.algorithm ?? ""] ?? e.algorithm ?? "-"}
                   hint={ALGO_HINT[e.algorithm ?? ""]} />
              <Row label="목표" value={`${e.objective} → ${e.goal}`}
                   hint={isMax ? "높을수록 좋음" : "낮을수록 좋음"} />
              <Row label="최대 시도 횟수" value={`${e.maxTrials ?? "-"}회`}
                   hint={`동시 실행 ${e.parallelTrials ?? 1}개`} />
              <Row label="워크스페이스" value={e.namespace ?? "-"}
                   hint="이 팀 자원으로 실행됨" />
            </div>
          </div>

          {/* ── 탐색 범위 ── */}
          {!!e.searchSpace?.length && (
            <div>
              <div className="mb-1.5 text-[11px] font-semibold uppercase text-muted-foreground">
                탐색 범위 (이 안에서 찾으라고 준 범위)
              </div>
              <div className="space-y-1.5">
                {e.searchSpace.map((sp) => {
                  const best = e.bestParams?.find((b) => b.name === sp.name);
                  const isRange = sp.type === "double" || sp.type === "int";
                  return (
                    <div key={sp.name}
                         className="flex flex-wrap items-baseline gap-2 rounded-md bg-secondary/40 px-3 py-2 text-xs">
                      <span className="font-mono font-semibold">{sp.name}</span>
                      <span className="text-muted-foreground">
                        {isRange
                          ? `${sp.range[0]} ~ ${sp.range[1]} 사이`
                          : `${sp.range.join(" / ")} 중에서`}
                      </span>
                      {best && (
                        <span className="ml-auto font-mono">
                          선택됨 <b className="text-brand-mint-700">{pretty(best.value)}</b>
                        </span>
                      )}
                    </div>
                  );
                })}
              </div>
              <p className="mt-1.5 text-[11px] text-muted-foreground">
                선택된 값이 범위의 끝에 붙어 있으면, 범위를 더 넓혀 다시 찾아볼 만합니다.
              </p>
            </div>
          )}

          {/* ── 진행 상황 ── */}
          <div>
            <div className="mb-1.5 flex items-baseline justify-between text-[11px]">
              <span className="font-semibold uppercase text-muted-foreground">진행 상황</span>
              <span className="font-mono">
                {e.trials}회 시도 · 목표 대비 {progress}%
              </span>
            </div>
            <Progress value={progress} className="h-1.5" indicatorClassName="bg-amber-500" />
          </div>

          {!e.realTraining && (
            <div className="rounded-md border border-amber-200 bg-amber-50/70 p-2.5 text-[11px] leading-snug text-amber-900">
              이 실험은 <b>실제 학습을 하지 않습니다.</b> 설정값으로부터 점수를 계산해
              내는 검증용이라, 찾아낸 값을 실제 모델에 그대로 쓰기는 어렵습니다.
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Row({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-md bg-secondary/40 p-2.5">
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="text-sm font-semibold">{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
    </div>
  );
}
