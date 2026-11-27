"use client";
import * as React from "react";
import Link from "next/link";
import {
  Search,
  ExternalLink,
  Cpu,
  Sparkles,
  ShoppingBag,
  Boxes,
  Globe,
  Zap,
  MessageSquare,
  ImageIcon,
  Mic,
  ArrowRight,
  Check,
  Rocket,
} from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { GithubIcon } from "@/components/icons";
import { BrandLogo } from "@/components/brand-logo";
import { PageHeader } from "@/components/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import {
  REBELLIONS_MODELS,
  modelStats,
  originalUrl,
  type ModelCategory,
  type RblnHardware,
  type ZooModel,
} from "@/lib/model-zoo";

const CATEGORY_ORDER: ModelCategory[] = [
  "LLM",
  "Multimodal",
  "Vision",
  "Audio",
  "Embedding",
  "Diffusion",
];

export default function CatalogPage() {
  const [q, setQ] = React.useState("");
  const [cat, setCat] = React.useState<"all" | ModelCategory>("all");
  const [hw, setHw] = React.useState<"all" | RblnHardware>("all");
  const [wizard, setWizard] = React.useState<SignatureScenario | null>(null);

  const stats = modelStats();

  const items = REBELLIONS_MODELS.filter((m) => {
    if (cat !== "all" && m.category !== cat) return false;
    if (hw !== "all" && !m.hardware.includes(hw)) return false;
    if (q) {
      const lq = q.toLowerCase();
      return (
        m.name.toLowerCase().includes(lq) ||
        m.fullName.toLowerCase().includes(lq) ||
        m.author.toLowerCase().includes(lq) ||
        m.description.toLowerCase().includes(lq)
      );
    }
    return true;
  });

  // group by category for non-filter view
  const grouped = CATEGORY_ORDER.map((c) => ({
    category: c,
    models: items.filter((m) => m.category === c),
  })).filter((g) => g.models.length > 0);

  return (
    <div className="space-y-6">
      <PageHeader
        title="서비스 마켓플레이스"
        description={
          <span className="inline-flex items-center gap-1.5">
            Rebellions ModelZoo 연동 — {stats.total}개 모델 즉시 배포
            <Badge variant="outline" className="text-[10px]">
              powered by Rebellions
            </Badge>
          </span>
        }
        actions={
          <>
            <Button variant="outline" size="sm" asChild>
              <a
                href="https://github.com/rebellions-sw/rbln-model-zoo"
                target="_blank"
                rel="noopener noreferrer"
              >
                <GithubIcon className="size-3.5" /> rbln-model-zoo
              </a>
            </Button>
            <Button variant="outline" size="sm" asChild>
              <a
                href="https://rebellions.ai/developers/model-zoo/"
                target="_blank"
                rel="noopener noreferrer"
              >
                <Globe className="size-3.5" /> 공식 페이지
              </a>
            </Button>
            <Button size="sm">
              <ShoppingBag className="size-3.5" /> 내 구독
            </Button>
          </>
        }
      />

      {/* 시그니처 시나리오 — ETRI 260615 §5 반영 */}
      <SignatureShowcase onLaunch={(s) => setWizard(s)} />

      <DeploymentWizard scenario={wizard} onClose={() => setWizard(null)} />

      {/* Filter band */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input
              placeholder="모델 이름·작성자·태그 검색…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              className="w-72 pl-9"
            />
          </div>
          <Select value={cat} onValueChange={(v) => setCat(v as typeof cat)}>
            <SelectTrigger className="w-40">
              <SelectValue placeholder="카테고리" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 카테고리</SelectItem>
              {CATEGORY_ORDER.map((c) => (
                <SelectItem key={c} value={c}>
                  {c} ({stats.byCategory[c]})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={hw} onValueChange={(v) => setHw(v as typeof hw)}>
            <SelectTrigger className="w-40">
              <SelectValue placeholder="RBLN 하드웨어" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 하드웨어</SelectItem>
              <SelectItem value="ATOM">ATOM</SelectItem>
              <SelectItem value="ATOM+">ATOM+</SelectItem>
              <SelectItem value="REBEL">REBEL</SelectItem>
              <SelectItem value="REBEL Quad">REBEL Quad</SelectItem>
            </SelectContent>
          </Select>
          <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
            <span>{items.length} / {stats.total} 모델</span>
            <span>·</span>
            <span>{stats.byAuthor}개 기관</span>
          </div>
        </div>
      </Card>

      {/* Category bands */}
      <div className="space-y-8">
        {grouped.map(({ category, models }) => (
          <section key={category}>
            <div className="mb-3 flex items-baseline justify-between">
              <div className="flex items-center gap-2">
                <CategoryBadge category={category} />
                <h2 className="text-sm font-semibold text-foreground">{category}</h2>
                <span className="text-xs text-muted-foreground">
                  ({models.length}개 모델)
                </span>
              </div>
            </div>
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {models.map((m) => (
                <ModelCard key={m.id} model={m} />
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}

function ModelCard({ model }: { model: ZooModel }) {
  return (
    <a
      href={originalUrl(model)}
      target="_blank"
      rel="noopener noreferrer"
      className="group block focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:ring-offset-background rounded-xl"
    >
      <Card className="relative flex h-full flex-col overflow-hidden border-border/60 transition-all duration-200 hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-lg">
        {/* Gradient bar (category color) */}
        <div className={cn("absolute inset-x-0 top-0 h-0.5", categoryBarColor(model.category))} />

        {/* Highlight badge */}
        {model.highlight && (
          <div className="absolute right-3 top-3">
            <Badge
              variant={
                model.highlight === "Korean"
                  ? "warning"
                  : model.highlight === "Featured"
                    ? "default"
                    : "success"
              }
              className="gap-1 text-[10px]"
            >
              {model.highlight === "Featured" && <Sparkles className="size-3" />}
              {model.highlight === "Korean" && "🇰🇷"}
              {model.highlight}
            </Badge>
          </div>
        )}

        <CardContent className="flex flex-1 flex-col p-5">
          {/* ── Header (고정 높이) ── */}
          <div className="space-y-1.5 pr-12">
            <div className="flex items-start gap-2">
              <CategoryBadge category={model.category} size="sm" />
            </div>
            <div className="flex items-start gap-2.5">
              <BrandLogo author={model.author} size={32} />
              <div className="min-w-0 flex-1">
                <h3 className="line-clamp-2 min-h-[2.5rem] text-base font-bold leading-tight">{model.name}</h3>
                <div className="truncate font-mono text-[11px] text-muted-foreground">
                  {model.fullName}
                </div>
              </div>
            </div>
          </div>

          {/* ── Author + Size (고정 높이) ── */}
          <div className="mt-3 flex items-baseline justify-between border-y border-border py-2 text-xs">
            <div>
              <div className="text-[10px] uppercase text-muted-foreground">작성</div>
              <div className="font-medium">{model.author}</div>
            </div>
            <div className="text-right">
              <div className="text-[10px] uppercase text-muted-foreground">파라미터</div>
              <div className="font-mono font-bold tabular-nums">
                {model.paramsB < 1
                  ? `${Math.round(model.paramsB * 1000)}M`
                  : `${model.paramsB}B`}
              </div>
            </div>
          </div>

          {/* ── Description (2줄 고정) ── */}
          <p className="mt-3 line-clamp-2 min-h-[2.25rem] text-xs leading-tight text-muted-foreground">
            {model.description}
          </p>

          {/* ── Hardware chips (최소 높이로 줄 수 통일) ── */}
          <div className="mt-3 space-y-1.5">
            <div className="text-[10px] uppercase text-muted-foreground">호환 하드웨어</div>
            <div className="flex min-h-[1.75rem] flex-wrap gap-1">
              {model.hardware.map((h) => (
                <Badge
                  key={h}
                  variant="ghost"
                  className="gap-1 bg-brand-mint-50 text-brand-mint-700 text-[10px]"
                >
                  <Cpu className="size-2.5" />
                  RBLN {h}
                </Badge>
              ))}
            </div>
          </div>

          {/* ── Framework + license (최소 높이, mt-auto로 push) ── */}
          <div className="mt-auto flex min-h-[1.5rem] flex-wrap items-center gap-1 pt-3 text-[10px]">
            {model.framework.map((f) => (
              <Badge key={f} variant="outline" className="text-[10px]">
                {f}
              </Badge>
            ))}
            <span className="ml-auto truncate text-muted-foreground">{model.license}</span>
          </div>
        </CardContent>

        {/* ── CTA footer (카드 외부, 항상 하단) ── */}
        <div className="grid grid-cols-2 border-t border-border bg-secondary/30">
          <span className="flex items-center justify-center gap-1.5 px-2 py-2.5 text-[11px] font-medium text-muted-foreground transition-colors group-hover:bg-secondary/60 group-hover:text-foreground">
            <ExternalLink className="size-3" />
            원본 모델
          </span>
          <button
            type="button"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              window.open(model.externalUrl, "_blank", "noopener,noreferrer");
            }}
            className="flex items-center justify-center gap-1.5 border-l border-border px-2 py-2.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
            title="rbln-model-zoo GitHub 저장소로 이동"
          >
            <GithubIcon className="size-3" />
            rbln-model-zoo
          </button>
        </div>
      </Card>
    </a>
  );
}

function CategoryBadge({
  category,
  size = "default",
}: {
  category: ModelCategory;
  size?: "default" | "sm";
}) {
  const palette: Record<ModelCategory, string> = {
    LLM: "bg-brand-indigo-100 text-brand-indigo-800",
    Multimodal: "bg-violet-100 text-violet-800",
    Vision: "bg-sky-100 text-sky-800",
    Audio: "bg-amber-100 text-amber-800",
    Embedding: "bg-brand-mint-50 text-brand-mint-700",
    Diffusion: "bg-rose-100 text-rose-800",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center rounded font-medium uppercase tracking-wider",
        size === "sm" ? "px-1.5 py-0.5 text-[9px]" : "px-2 py-0.5 text-[10px]",
        palette[category],
      )}
    >
      {category}
    </span>
  );
}

function categoryBarColor(c: ModelCategory) {
  return (
    {
      LLM: "bg-gradient-to-r from-brand-indigo-500 to-brand-indigo-300",
      Multimodal: "bg-gradient-to-r from-violet-500 to-violet-300",
      Vision: "bg-gradient-to-r from-sky-500 to-sky-300",
      Audio: "bg-gradient-to-r from-amber-500 to-amber-300",
      Embedding: "bg-gradient-to-r from-brand-mint-500 to-brand-mint-300",
      Diffusion: "bg-gradient-to-r from-rose-500 to-rose-300",
    } as const
  )[c];
}

// ─────────────────────────────────────────────────────────────
// 시그니처 시나리오 — ETRI 260615 §5 반영
// ─────────────────────────────────────────────────────────────

type SignatureScenario = {
  id: "chatbot" | "image" | "voice";
  title: string;
  subtitle: string;
  desc: string;
  icon: React.ComponentType<{ className?: string }>;
  accent: string;
  ringAccent: string;
  defaultModel: string;
  modelOptions: string[];
  defaultHardware: string;
  hardwareOptions: string[];
  defaultInfra: "VM" | "Container";
  featured?: boolean;
};

const SCENARIOS: SignatureScenario[] = [
  {
    id: "chatbot",
    title: "원클릭 챗봇 배포",
    subtitle: "LLM + vLLM + Web UI",
    desc: "모델 · 가속기 · 인프라를 조합해 30초 안에 챗봇 API + 데모 웹을 배포",
    icon: MessageSquare,
    accent: "bg-gradient-to-br from-brand-indigo-500 to-brand-mint-500 text-white",
    ringAccent: "border-primary shadow-lg shadow-primary/20",
    defaultModel: "Llama-3.1-8B-Instruct",
    modelOptions: [
      "Llama-3.1-8B-Instruct",
      "Llama-3.1-70B-Instruct",
      "Qwen2.5-14B-Instruct",
      "Solar-10.7B",
      "Mistral-7B-v0.3",
    ],
    defaultHardware: "Rebellions ATOM+ × 4",
    hardwareOptions: [
      "Rebellions ATOM+ × 4",
      "Rebellions REBEL Quad × 1",
      "NVIDIA A100 40GB × 1",
      "NVIDIA H100 80GB × 1",
      "Furiosa RNGD × 2",
    ],
    defaultInfra: "Container",
    featured: true,
  },
  {
    id: "image",
    title: "이미지 생성 서비스",
    subtitle: "Stable Diffusion + Gradio",
    desc: "Diffusion 모델 카드에서 가속기 · 인프라 선택 후 원클릭 배포",
    icon: ImageIcon,
    accent: "bg-gradient-to-br from-violet-500 to-rose-500 text-white",
    ringAccent: "border-violet-400",
    defaultModel: "Stable Diffusion 3.5 Large",
    modelOptions: [
      "Stable Diffusion 3.5 Large",
      "Stable Diffusion XL 1.0",
      "FLUX.1 [schnell]",
    ],
    defaultHardware: "NVIDIA A100 40GB × 1",
    hardwareOptions: [
      "NVIDIA A100 40GB × 1",
      "NVIDIA H100 80GB × 1",
      "Rebellions REBEL Quad × 1",
    ],
    defaultInfra: "Container",
  },
  {
    id: "voice",
    title: "음성 STT · TTS",
    subtitle: "Whisper + XTTS",
    desc: "음성 인식(STT) + 음성 합성(TTS) 서비스를 함께 배포",
    icon: Mic,
    accent: "bg-gradient-to-br from-amber-500 to-rose-400 text-white",
    ringAccent: "border-amber-300",
    defaultModel: "Whisper-Large-v3 + XTTS-v2",
    modelOptions: [
      "Whisper-Large-v3 + XTTS-v2",
      "Whisper-Medium + Coqui XTTS",
    ],
    defaultHardware: "Furiosa RNGD × 2",
    hardwareOptions: [
      "Furiosa RNGD × 2",
      "NVIDIA A100 40GB × 1",
      "Rebellions ATOM+ × 4",
    ],
    defaultInfra: "Container",
  },
];

function SignatureShowcase({ onLaunch }: { onLaunch: (s: SignatureScenario) => void }) {
  return (
    <div className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Zap className="size-4 text-brand-mint-600" />
            openKcloud 시그니처 시나리오
          </div>
          <p className="text-xs text-muted-foreground">
            카드 → 모델 → 가속기 → 인프라 4단계로 이종 AI반도체 위에서 서비스를 조합·배포한다.
          </p>
        </div>
        <Badge variant="ghost" className="font-mono text-[10px]">
          통합 시연용
        </Badge>
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        {SCENARIOS.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => onLaunch(s)}
            className={cn(
              "relative overflow-hidden rounded-xl border-2 p-4 text-left transition-all hover:-translate-y-0.5",
              s.featured ? s.ringAccent : "border-border hover:border-primary/40",
            )}
          >
            {s.featured && (
              <div className="absolute right-3 top-3">
                <Badge variant="default" className="gap-1 text-[10px]">
                  <Rocket className="size-2.5" /> 대표
                </Badge>
              </div>
            )}
            <div className={cn("mb-3 flex size-11 items-center justify-center rounded-lg", s.accent)}>
              <s.icon className="size-5" />
            </div>
            <div className="space-y-0.5">
              <div className="text-base font-bold leading-tight">{s.title}</div>
              <div className="text-xs text-muted-foreground">{s.subtitle}</div>
            </div>
            <p className="mt-2 text-xs leading-relaxed text-muted-foreground">{s.desc}</p>
            <div className="mt-3 flex items-center gap-1.5 text-[11px] font-medium text-primary">
              배포 위저드 열기 <ArrowRight className="size-3" />
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────
// 배포 위저드 — 4단계 (모델 → 가속기 → 인프라 → 확인·실행)
// ─────────────────────────────────────────────────────────────

function DeploymentWizard({
  scenario, onClose,
}: {
  scenario: SignatureScenario | null;
  onClose: () => void;
}) {
  const [step, setStep] = React.useState(1);
  const [model, setModel] = React.useState("");
  const [hardware, setHardware] = React.useState("");
  const [infra, setInfra] = React.useState<"VM" | "Container">("Container");
  const [name, setName] = React.useState("");

  React.useEffect(() => {
    if (scenario) {
      setStep(1);
      setModel(scenario.defaultModel);
      setHardware(scenario.defaultHardware);
      setInfra(scenario.defaultInfra);
      setName(`${scenario.id}-${Date.now().toString().slice(-4)}`);
    }
  }, [scenario]);

  if (!scenario) return null;

  const STEPS = [
    { n: 1, label: "모델", icon: Sparkles },
    { n: 2, label: "가속기", icon: Cpu },
    { n: 3, label: "인프라", icon: Boxes },
    { n: 4, label: "확인·실행", icon: Rocket },
  ];

  const submit = () => {
    toast.success(`${scenario.title} 배포 시작 — ${name}`, {
      description: `${model} · ${hardware} · ${infra}`,
    });
    onClose();
  };

  return (
    <Dialog open={!!scenario} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-3">
            <div className={cn("flex size-10 items-center justify-center rounded-lg", scenario.accent)}>
              <scenario.icon className="size-5" />
            </div>
            <div>
              <DialogTitle>{scenario.title}</DialogTitle>
              <DialogDescription>{scenario.subtitle} — 4단계 배포 위저드</DialogDescription>
            </div>
          </div>
        </DialogHeader>

        {/* Step indicator */}
        <div className="flex items-center justify-between rounded-lg border border-border bg-secondary/30 p-3">
          {STEPS.map((s, i) => (
            <React.Fragment key={s.n}>
              <div className="flex flex-col items-center gap-1">
                <div
                  className={cn(
                    "flex size-8 items-center justify-center rounded-full border-2 transition-colors",
                    step > s.n
                      ? "border-brand-mint-500 bg-brand-mint-500 text-white"
                      : step === s.n
                        ? "border-primary bg-primary text-white"
                        : "border-border bg-background text-muted-foreground",
                  )}
                >
                  {step > s.n ? <Check className="size-4" /> : <s.icon className="size-3.5" />}
                </div>
                <div className={cn("text-[10px] font-medium", step >= s.n ? "text-foreground" : "text-muted-foreground")}>
                  {s.label}
                </div>
              </div>
              {i < STEPS.length - 1 && (
                <div
                  className={cn(
                    "flex-1 border-t-2 mx-2 transition-colors",
                    step > s.n ? "border-brand-mint-500" : "border-border",
                  )}
                />
              )}
            </React.Fragment>
          ))}
        </div>

        {/* Step content */}
        <div className="min-h-[180px] space-y-3 py-2">
          {step === 1 && (
            <div className="space-y-2">
              <Label>모델 선택</Label>
              <div className="space-y-1.5">
                {scenario.modelOptions.map((m) => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setModel(m)}
                    className={cn(
                      "flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition-colors",
                      model === m
                        ? "border-primary bg-primary/5 text-foreground"
                        : "border-border text-muted-foreground hover:border-primary/50",
                    )}
                  >
                    <span className="font-mono text-xs">{m}</span>
                    {model === m && <Check className="size-4 text-primary" />}
                  </button>
                ))}
              </div>
            </div>
          )}

          {step === 2 && (
            <div className="space-y-2">
              <Label>AI 가속기 선택</Label>
              <div className="space-y-1.5">
                {scenario.hardwareOptions.map((h) => (
                  <button
                    key={h}
                    type="button"
                    onClick={() => setHardware(h)}
                    className={cn(
                      "flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition-colors",
                      hardware === h
                        ? "border-primary bg-primary/5 text-foreground"
                        : "border-border text-muted-foreground hover:border-primary/50",
                    )}
                  >
                    <div className="flex items-center gap-2">
                      <Cpu className="size-3.5" />
                      <span className="font-mono text-xs">{h}</span>
                    </div>
                    {hardware === h && <Check className="size-4 text-primary" />}
                  </button>
                ))}
              </div>
            </div>
          )}

          {step === 3 && (
            <div className="space-y-3">
              <Label>인프라 유형</Label>
              <div className="grid grid-cols-2 gap-2">
                <button
                  type="button"
                  onClick={() => setInfra("Container")}
                  className={cn(
                    "rounded-lg border-2 p-4 text-left transition-colors",
                    infra === "Container"
                      ? "border-primary bg-primary/5"
                      : "border-border hover:border-primary/50",
                  )}
                >
                  <Boxes className="mb-2 size-6 text-brand-mint-600" />
                  <div className="text-sm font-semibold">Container (K8s Pod)</div>
                  <div className="text-[11px] text-muted-foreground">
                    빠른 배포·자동 스케일·GitOps 친화. 무상태 서비스 권장.
                  </div>
                </button>
                <button
                  type="button"
                  onClick={() => setInfra("VM")}
                  className={cn(
                    "rounded-lg border-2 p-4 text-left transition-colors",
                    infra === "VM"
                      ? "border-primary bg-primary/5"
                      : "border-border hover:border-primary/50",
                  )}
                >
                  <Sparkles className="mb-2 size-6 text-brand-indigo-600" />
                  <div className="text-sm font-semibold">VM (OpenStack)</div>
                  <div className="text-[11px] text-muted-foreground">
                    강한 격리·전용 GPU 패스스루. 대규모 학습 워크로드 권장.
                  </div>
                </button>
              </div>
              <div>
                <Label>서비스 이름</Label>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="mt-1 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs"
                />
              </div>
            </div>
          )}

          {step === 4 && (
            <div className="space-y-2">
              <Label>배포 요약</Label>
              <Card className="p-4">
                <dl className="grid grid-cols-3 gap-y-3 text-xs">
                  <dt className="text-muted-foreground">시나리오</dt>
                  <dd className="col-span-2 font-medium">{scenario.title}</dd>
                  <dt className="text-muted-foreground">모델</dt>
                  <dd className="col-span-2 font-mono">{model}</dd>
                  <dt className="text-muted-foreground">가속기</dt>
                  <dd className="col-span-2 font-mono">{hardware}</dd>
                  <dt className="text-muted-foreground">인프라</dt>
                  <dd className="col-span-2">
                    <Badge variant="outline" className="font-mono text-[10px]">{infra}</Badge>
                  </dd>
                  <dt className="text-muted-foreground">서비스 이름</dt>
                  <dd className="col-span-2 font-mono">{name}</dd>
                </dl>
                <div className="mt-3 rounded-md border border-brand-mint-200 bg-brand-mint-50/60 p-2 text-[11px] text-brand-mint-900">
                  💡 실행 시 자동으로 진행되는 단계: 프로젝트 쿼터 확인 → 이미지 준비 → 자원 할당 →
                  {infra === "Container" ? " Deployment + Service + Ingress" : " VM + Cloud-init 스크립트"} 배포 → API endpoint 게시.
                </div>
              </Card>
            </div>
          )}
        </div>

        <DialogFooter>
          {step > 1 && (
            <Button variant="outline" onClick={() => setStep(step - 1)} className="mr-auto">
              이전
            </Button>
          )}
          <Button variant="outline" onClick={onClose}>취소</Button>
          {step < 4 ? (
            <Button onClick={() => setStep(step + 1)}>
              다음 <ArrowRight className="size-3.5" />
            </Button>
          ) : (
            <Button onClick={submit}>
              <Rocket className="size-3.5" /> 배포 실행
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
