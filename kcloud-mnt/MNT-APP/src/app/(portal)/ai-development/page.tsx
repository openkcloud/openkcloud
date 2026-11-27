"use client";
import { ChangeMark } from "@/components/change-mark";
import { useFeatureVisible } from "@/lib/scope";
import Link from "next/link";
import {
  BookOpen,
  Folder,
  GitBranch,
  FlaskConical,
  Send,
  Sparkles,
  ArrowRight,
  Activity,
} from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Server, Boxes } from "lucide-react";
import { useExperiments, useNotebooks, usePipelines, useServing } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import type { KFNotebook, KFPipeline, KFExperiment, KFInferenceService } from "@/lib/types";

const MENUS = [
  {
    href: "/ai-development/profiles",
    icon: Folder,
    color: "bg-brand-mint-50 text-brand-mint-700",
    title: "Profiles",
    subtitle: "워크스페이스 격리",
    label: "① 워크스페이스",
    desc: "사용자/팀별 K8s Namespace + RBAC + ResourceQuota + IAP 정책 묶음. 1 워크스페이스(Profile) = 1 프로젝트, 프로젝트별로 신청해 승인되면 생성.",
    example: "ai-research 팀에 vCPU 256 / GPU 8장 / 30TB 스토리지 할당",
  },
  {
    href: "/ai-development/notebooks",
    icon: BookOpen,
    color: "bg-brand-indigo-100 text-brand-indigo-700",
    title: "Notebooks",
    subtitle: "AI 노트북 (개발환경)",
    label: "② 개발환경",
    desc: "컨테이너 기반 IDE (VSCode Code Server). GPU/NPU/PIM 자원 직접 할당. 영구 저장소 PVC 마운트.",
    example: "research-llama-finetune 노트북 — GPU 2장 + 500GB 볼륨",
  },
  {
    href: "/ai-development/pipelines",
    icon: GitBranch,
    color: "bg-violet-100 text-violet-700",
    title: "Pipelines",
    subtitle: "ML 워크플로우",
    label: "③ 학습 · 파이프라인",
    desc: "데이터 → 학습 → 평가 → 배포를 DAG로 정의. 수동/Cron/Git 트리거. 각 단계 컨테이너 격리, 아티팩트 자동 보관.",
    example: "llama-3 finetune: 데이터 로드 → 토큰화 → LoRA 학습 → 평가 → KServe 배포 (6단계)",
  },
  {
    href: "/ai-development/experiments",
    icon: FlaskConical,
    color: "bg-amber-100 text-amber-700",
    title: "Experiments",
    subtitle: "하이퍼파라미터 탐색 (Katib)",
    label: "③ 학습 · 실험 (HPO)",
    desc: "Grid/Random/Bayesian/TPE 알고리즘으로 수십~수백 trial 자동 실행. 최적 학습률·배치 크기 발견.",
    example: "llama-lr-search: lr=1e-5~1e-3 × batch=8~64, 30 trials, 목표 eval_accuracy > 0.88",
  },
  {
    href: "/ai-development/serving",
    icon: Send,
    color: "bg-rose-100 text-rose-700",
    title: "Serving",
    subtitle: "모델 배포 (KServe)",
    label: "④ 모델 배포",
    desc: "학습된 모델을 REST/gRPC API로 배포. Auto-scaling (0→N), Canary 배포, A/B 테스트.",
    example: "solar-10b-vllm: 3 레플리카 · 평균 124 RPS · P95 980ms",
  },
  {
    href: "/ai-development/model-insights",
    icon: Activity,
    color: "bg-sky-100 text-sky-700",
    title: "병목 분석",
    subtitle: "워크플로우 성능 분석",
    label: "⑤ 성능 분석",
    desc: "ML 파이프라인 각 단계의 시간 비중·자원 사용·재실행 빈도 분석. 추론 단계별 latency breakdown.",
    example: "Attention Layer 22/28에서 KV cache 미스율 41% — 정상치 8% 대비 5배",
  },
];

export default function AIDevHomePage() {
  const experimentsOn = useFeatureVisible("ai.experiments");
  const menus = MENUS.filter((m) => experimentsOn || !m.href.endsWith("/experiments"));
  return (
    <div className="space-y-6">
      <ChangeMark ids={["CR-AID-01", "CR-AID-10"]}>
        <ProjectFlowBand />
      </ChangeMark>

      <Card className="border-brand-indigo-200 bg-gradient-to-br from-brand-indigo-50 to-transparent">
        <CardContent className="flex items-start gap-3 p-5">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <Sparkles className="size-5" />
          </div>
          <div className="space-y-1">
            <div className="font-semibold">AI 노트북이란?</div>
            <p className="text-sm text-muted-foreground">
              <strong className="text-foreground">Kubeflow</strong>는 K8s 위에서 ML 워크플로우 전 주기(데이터→학습→배포→모니터링)를 운영하는 오픈소스 플랫폼이다.
              AI 노트북은 <strong className="text-foreground">프로젝트 = 워크스페이스(Kubeflow Profile)</strong> 1:1 구조로
              격리·자원할당을 처리한다. 워크스페이스는 프로젝트별로 신청해 관리자가 승인하면 만들어진다.
            </p>
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {menus.map((m) => (
          <Link key={m.href} href={m.href}>
            <Card className="group h-full transition-all hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-lg">
              <CardContent className="space-y-3 p-5">
                <div className="flex items-start justify-between">
                  <div className={`flex size-10 items-center justify-center rounded-lg ${m.color}`}>
                    <m.icon className="size-5" />
                  </div>
                </div>
                <div>
                  <div className="text-base font-bold leading-tight">{m.label}</div>
                  <div className="text-xs text-muted-foreground">{m.title} · {m.subtitle}</div>
                </div>
                <p className="text-xs leading-relaxed text-muted-foreground">{m.desc}</p>
                <div className="rounded-md border border-dashed border-border bg-secondary/30 p-2 text-[11px] text-muted-foreground">
                  <span className="font-medium text-foreground">예시 ›</span> {m.example}
                </div>
                <div className="flex items-center justify-end text-xs text-muted-foreground group-hover:text-primary">
                  바로가기 <ArrowRight className="ml-1 size-3 transition-transform group-hover:translate-x-0.5" />
                </div>
              </CardContent>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────
// 자원 흐름 밴드 — 프로젝트가 받은 IaaS·PaaS 자원 위에 AI 노트북 자원을 올리는 흐름.
// 현재 프로젝트 컨텍스트의 실제 수치를 보여준다. 번호는 화면 탭 번호와 같다.
// ─────────────────────────────────────────────────────────────
function ProjectFlowBand() {
  const { match, project, isAll, label, projects } = useProjectContext();
  const nb = (useNotebooks() as { data?: { items: KFNotebook[] } }).data?.items ?? [];
  const pl = (usePipelines() as { data?: { items: KFPipeline[] } }).data?.items ?? [];
  const ex = (useExperiments() as { data?: { items: KFExperiment[] } }).data?.items ?? [];
  const sv = (useServing() as { data?: { items: KFInferenceService[] } }).data?.items ?? [];
  const experimentsOn = useFeatureVisible("ai.experiments");

  const tenants = isAll ? projects.map((p) => p.tenant) : project ? [project.tenant] : [];
  const sum = (f: (t: (typeof tenants)[number]) => number) => tenants.reduce((a, t) => a + f(t), 0);
  const myNb = nb.filter((n) => match(n.namespace));
  const mySv = sv.filter((x) => match(x.namespace));

  const steps = [
    { n: 1, label: "워크스페이스", href: "/ai-development/profiles", value: isAll ? `${tenants.length}개` : "active", sub: isAll ? "프로젝트마다 1개" : `ns ${label}` },
    { n: 2, label: "개발환경", href: "/ai-development/notebooks", value: `${myNb.length}개`, sub: `실행 ${myNb.filter((n) => n.status === "Running").length} · 가속기 ${myNb.reduce((a, n) => a + (n.acceleratorCount ?? 0), 0)}장` },
    {
      n: 3,
      label: "학습",
      href: "/ai-development/pipelines",
      value: `${pl.filter((p) => match(p.namespace)).length}개`,
      sub: experimentsOn ? `파이프라인 · 실험 ${ex.filter((e) => match(e.namespace)).length}` : "파이프라인",
    },
    { n: 4, label: "모델 배포", href: "/ai-development/serving", value: `${mySv.length}개`, sub: `평균 ${+mySv.reduce((a, x) => a + (x.rpsAvg ?? 0), 0).toFixed(1)} RPS` },
    { n: 5, label: "성능 분석", href: "/ai-development/model-insights", value: "GPU", sub: "실측·병목" },
  ];

  return (
    <Card className="overflow-hidden">
      <CardContent className="p-0">
        <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-border px-5 py-3">
          <div className="text-sm font-semibold">
            {isAll ? "전체 프로젝트" : <span className="font-mono">{label}</span>}
            <span className="font-normal text-muted-foreground"> — 받은 클라우드 자원 위에서 AI를 돌리는 흐름</span>
          </div>
          <span className="text-[11px] text-muted-foreground">번호는 위 탭 순서와 같다</span>
        </div>
        <div className="grid gap-0 lg:grid-cols-[minmax(220px,1fr)_auto_minmax(0,3.2fr)]">
          {/* 클라우드 자원 (IaaS·PaaS) */}
          <Link href="/iaas/projects" className="group space-y-2.5 bg-brand-indigo-50/60 p-4 transition-colors hover:bg-brand-indigo-50">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-brand-indigo-700">프로젝트 자원 · IaaS·PaaS</div>
            {/* IaaS·PaaS 자원 수는 여기서 집계하지 않는다 — 워크스페이스 ResourceQuota 실측만 보여준다 */}
            <div className="space-y-1 text-xs">
              <div className="flex items-center gap-1.5 whitespace-nowrap"><Boxes className="size-3.5 text-brand-indigo-600" /> 워크스페이스 <strong className="font-mono">{tenants.length}</strong></div>
              <div className="flex items-center gap-1.5 whitespace-nowrap"><Server className="size-3.5 text-brand-indigo-600" /> 메모리 <strong className="font-mono">{+sum((t) => t.usedRamGb).toFixed(1)} / {sum((t) => t.quotaRamGb)}GB</strong></div>
            </div>
            <div className="space-y-1 text-[11px] text-muted-foreground">
              <div className="flex justify-between"><span>가속기 쿼터</span><span className="font-mono text-foreground">{sum((t) => t.usedAcc)} / {sum((t) => t.quotaAcc)}장</span></div>
              <div className="flex justify-between"><span>vCPU 쿼터</span><span className="font-mono text-foreground">{+sum((t) => t.usedVcpu).toFixed(1)} / {sum((t) => t.quotaVcpu)}</span></div>
            </div>
            <div className="text-[11px] font-medium text-brand-indigo-700 group-hover:underline">프로젝트 보기 →</div>
          </Link>
          <div className="hidden flex-col items-center justify-center gap-1 px-2 text-[10px] text-muted-foreground lg:flex">
            <ArrowRight className="size-4" />
            <span className="whitespace-nowrap">위에 올림</span>
          </div>
          {/* AI 노트북 단계 */}
          <div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-3 md:grid-cols-5">
            {steps.map((s) => (
              <Link key={s.n} href={s.href} className="group flex flex-col gap-1 bg-card p-3.5 transition-colors hover:bg-secondary/40">
                <div className="flex items-center gap-1.5 text-xs font-semibold">
                  <span className="inline-flex size-[18px] items-center justify-center rounded-full bg-primary font-mono text-[10px] font-bold text-primary-foreground">{s.n}</span>
                  {s.label}
                </div>
                <div className="font-mono text-lg font-bold tabular-nums leading-tight">{s.value}</div>
                <div className="text-[11px] text-muted-foreground">{s.sub}</div>
              </Link>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
