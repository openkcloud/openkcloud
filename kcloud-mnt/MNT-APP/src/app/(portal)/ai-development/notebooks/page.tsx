"use client";
import { toast } from "sonner";
import * as React from "react";
import Link from "next/link";
import {
  Cpu,
  Clock,
  Play,
  Square,
  Trash2,
  ExternalLink,
  Plus,
  Search,
  BookOpen,
  Code2,
  Container,
} from "lucide-react";
import { ainbFetch, useNotebooks } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { MoreHorizontal } from "lucide-react";
import { cn, relativeTime } from "@/lib/utils";
import type { KFNotebook } from "@/lib/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notebookAction } from "@/lib/notebook-api";
import { NotebookCreateDialog, type NotebookPreset } from "@/components/notebook-create-dialog";
import { SnapshotDialog, ResizeDialog } from "@/components/notebook-manage-dialogs";

/** 볼륨 칸 — 총 용량 + 실사용량(du 실측, 60초 갱신, Running일 때만) */
function VolumeCell({ id, storageGb, running }: { id: string; storageGb: number; running: boolean }) {
  const { data } = useQuery({
    queryKey: ["nb-volume", id],
    queryFn: async () => {
      const res = await ainbFetch(`/notebooks/${id}/volume`);
      if (!res.ok) throw new Error(`${res.status}`);
      return (await res.json()) as { usedHuman: string | null; usedPercent: number | null };
    },
    enabled: running && storageGb > 0,
    refetchInterval: 60000,
    staleTime: 55000,
  });
  if (storageGb <= 0) return <span className="text-muted-foreground">—</span>;
  return (
    <span>
      {storageGb} GiB
      {data?.usedHuman != null && (
        <span
          className={cn(
            "ml-1 text-[10px]",
            (data.usedPercent ?? 0) >= 90 ? "text-rose-600" : "text-muted-foreground",
          )}
        >
          · {data.usedHuman} 사용
          {data.usedPercent != null && ` (${data.usedPercent}%)`}
        </span>
      )}
    </span>
  );
}

export default function NotebooksPage() {
  const [q, setQ] = React.useState("");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [preset, setPreset] = React.useState<NotebookPreset | undefined>();
  const [snapOpen, setSnapOpen] = React.useState(false);
  const [resizeOpen, setResizeOpen] = React.useState(false);
  const [target, setTarget] = React.useState<KFNotebook | null>(null);
  const qc = useQueryClient();
  // 네임스페이스 = 위에서 고른 프로젝트 (화면별 네임스페이스 선택은 두지 않는다)
  // 새 노트북은 지금 프로젝트에 만든다. 전체 보기면 볼 수 있는 워크스페이스 중에서 고른다
  const { match, label, isAll, choices } = useProjectContext();
  const { data, isLoading } = useNotebooks() as {
    data: { items: KFNotebook[] } | undefined;
    isLoading: boolean;
  };

  async function act(nb: KFNotebook, action: "start" | "stop" | "delete") {
    let keepVolume = false;
    if (action === "delete") {
      const hasVolume = nb.storageGb > 0;
      if (!hasVolume) {
        if (!window.confirm(`"${nb.name}" 노트북을 삭제할까요?\n\n이 노트북에는 볼륨이 없어 저장된 작업 파일이 없습니다.`)) return;
      } else {
        if (!window.confirm(`"${nb.name}" 노트북을 삭제합니다.\n\n데이터 볼륨(${nb.storageGb}GB) 처리는 다음 단계에서 선택합니다.`)) return;
        // 볼륨을 남기면 워크스페이스 몫을 계속 차지한다 — 쌓이면 새 노트북을
        // 만들 수 없게 되므로, 남길지 지울지 반드시 사용자가 고르게 한다
        keepVolume = window.confirm(
          `데이터 볼륨(${nb.storageGb}GB)을 남겨둘까요?\n\n[확인] 볼륨 유지 — 같은 이름으로 다시 만들면 작업 파일이 복구됩니다. 단, 워크스페이스 저장 몫(${nb.storageGb}GB)을 계속 차지합니다.\n[취소] 볼륨까지 완전 삭제 — 작업 파일이 사라지고 저장 몫이 반환됩니다.`,
        );
      }
    }
    try {
      await notebookAction(nb.id, action, { keepVolume });
      toast.success(`"${nb.name}" ${action === "start" ? "시작" : action === "stop" ? "정지" : "삭제"} 요청 완료`);
      qc.invalidateQueries({ queryKey: ["kf", "notebooks"] });
    } catch (e) {
      toast.error(`요청 실패: ${(e as Error).message}`);
    }
  }

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const mine = data.items.filter((n) => match(n.namespace));
  const items = mine.filter((n) => !q || n.name.toLowerCase().includes(q.toLowerCase()));

  const counts = {
    running: mine.filter((n) => n.status === "Running").length,
    starting: mine.filter((n) => n.status === "Starting").length,
    stopped: mine.filter((n) => n.status === "Stopped").length,
  };

  return (
    <div className="space-y-4">
      <NotebookCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        namespaces={choices}
        preset={preset}
      />
      <SnapshotDialog nb={target} open={snapOpen} onOpenChange={setSnapOpen} />
      <ResizeDialog nb={target} open={resizeOpen} onOpenChange={setResizeOpen} />
      {/* Quick stats — Kubeflow style */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatChip label="실행 중" value={counts.running} accent="mint" hint="활성 서버" />
        <StatChip label="시작 중" value={counts.starting} accent="amber" hint="프로비저닝" />
        <StatChip label="정지" value={counts.stopped} accent="slate" hint="유휴" />
        <StatChip
          label="총 가속기 할당"
          value={mine.reduce((s, n) => s + n.acceleratorCount, 0)}
          accent="indigo"
          hint="GPU·NPU·PIM"
        />
      </div>

      {/* Filter bar */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input
              placeholder="노트북 서버 이름…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              className="w-56 pl-9"
            />
          </div>
          <Badge variant="outline" className="h-9 gap-1.5 px-3 text-xs font-normal" title="위의 프로젝트 선택을 따른다">
            네임스페이스 <span className="font-mono font-semibold">{isAll ? "전체" : label}</span>
          </Badge>
          <span className="ml-auto text-xs text-muted-foreground">{items.length}개 서버</span>
        </div>
      </Card>

      {/* Notebook server table — Kubeflow Central Dashboard 스타일 */}
      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>노트북 서버</TableHead>
              <TableHead>네임스페이스</TableHead>
              <TableHead>이미지</TableHead>
              <TableHead>컴퓨트</TableHead>
              <TableHead>가속기</TableHead>
              <TableHead>볼륨</TableHead>
              <TableHead>마지막 활동</TableHead>
              <TableHead>상태</TableHead>
              <TableHead className="w-32 text-right">액션</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((nb) => (
              <TableRow key={nb.id}>
                <TableCell>
                  <Link
                    href={`/ai-development/notebooks/detail/?id=${encodeURIComponent(nb.id)}`}
                    className="flex items-center gap-2 hover:text-primary"
                  >
                    <ImageIcon image={nb.image} />
                    <div>
                      <div className="font-medium">{nb.name}</div>
                      <div className="font-mono text-[10px] text-muted-foreground">
                        kf-nb-{nb.id}
                      </div>
                    </div>
                  </Link>
                </TableCell>
                <TableCell className="font-mono text-xs">{nb.namespace}</TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">
                  {nb.image}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">
                  {nb.cpu} vCPU · {nb.memGb} GiB
                </TableCell>
                <TableCell>
                  {nb.acceleratorCount > 0 ? (
                    <Badge variant="success" className="gap-1">
                      <Cpu className="size-3" />
                      {nb.acceleratorKind} × {nb.acceleratorCount}
                    </Badge>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">
                  <VolumeCell id={nb.id} storageGb={nb.storageGb} running={nb.status === "Running"} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  <span className="inline-flex items-center gap-1">
                    <Clock className="size-3" /> {relativeTime(nb.lastActivity)}
                  </span>
                </TableCell>
                <TableCell>
                  <StatusBadge status={nb.status} />
                  {(nb as KFNotebook & { statusReason?: string }).statusReason && (
                    <div className="mt-1 max-w-52 text-[10px] leading-snug text-amber-700">
                      {(nb as KFNotebook & { statusReason?: string }).statusReason}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    {nb.status === "Running" ? (
                      <>
                        <Button size="sm" variant="outline" asChild>
                          <Link href={`/ai-development/notebooks/detail/?id=${encodeURIComponent(nb.id)}`}>
                            상세
                          </Link>
                        </Button>
                        <Button size="sm" asChild>
                          <a href={`/notebooks/ide/?id=${encodeURIComponent(nb.id)}`} target="_blank" rel="noopener noreferrer">
                            연결 <ExternalLink className="size-3" />
                          </a>
                        </Button>
                      </>
                    ) : nb.status === "Stopped" ? (
                      <Button size="sm" variant="outline" onClick={() => act(nb, "start")}>
                        <Play className="size-3" /> 시작
                      </Button>
                    ) : (
                      <Button size="sm" variant="outline" disabled>
                        준비 중…
                      </Button>
                    )}
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="icon-sm" variant="ghost">
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => act(nb, "stop")}>
                          <Square className="size-4" /> 정지
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => { setTarget(nb); setSnapOpen(true); }}>
                          볼륨 스냅샷
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => { setTarget(nb); setResizeOpen(true); }}>
                          리소스 변경
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem className="text-rose-600" onClick={() => act(nb, "delete")}>
                          <Trash2 className="size-4" /> 삭제
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>

      {/* New notebook quick start — Kubeflow image catalog 스타일 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Plus className="size-4" /> 새 노트북 서버
          </CardTitle>
          <CardDescription>
            이미지 템플릿을 선택하면 권장 컴퓨트·가속기 프리셋이 자동 적용됩니다
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
            {IMAGE_CATALOG.map((tpl) => (
              <button
                key={tpl.name}
                disabled={!tpl.preset}
                onClick={() => {
                  if (!tpl.preset) return;
                  setPreset(tpl.preset);
                  setCreateOpen(true);
                }}
                className={cn(
                  "group flex items-start gap-3 rounded-xl border border-border bg-card p-4 text-left transition-colors",
                  tpl.preset
                    ? "hover:border-primary/40 hover:bg-secondary/30"
                    : "cursor-not-allowed opacity-55",
                )}>
                <div
                  className={cn(
                    "flex size-10 shrink-0 items-center justify-center rounded-lg",
                    tpl.color,
                  )}
                >
                  <tpl.icon className="size-5" />
                </div>
                <div className="flex-1 space-y-1">
                  <div className="flex items-center gap-2">
                    <div className="font-semibold leading-tight">{tpl.name}</div>
                    {tpl.recommended && (
                      <Badge variant="success" className="text-[10px]">권장</Badge>
                    )}
                    {!tpl.preset && (
                      <Badge variant="secondary" className="text-[10px]">
                        {tpl.unavailableNote ?? "하드웨어 도입 후 제공"}
                      </Badge>
                    )}
                  </div>
                  <div className="text-xs text-muted-foreground">{tpl.description}</div>
                  <div className="flex flex-wrap gap-1 pt-1">
                    {tpl.tags.map((t) => (
                      <Badge key={t} variant="ghost" className="font-mono text-[10px]">
                        {t}
                      </Badge>
                    ))}
                  </div>
                </div>
              </button>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function ImageIcon({ image }: { image: string }) {
  if (image.includes("vllm")) return <Container className="size-4 text-violet-600" />;
  if (image.includes("rbln")) return <Cpu className="size-4 text-brand-mint-600" />;
  if (image.includes("pim")) return <Cpu className="size-4 text-rose-600" />;
  if (image.includes("cuda")) return <Cpu className="size-4 text-brand-indigo-600" />;
  return <Code2 className="size-4 text-sky-600" />;
}

function StatusBadge({ status }: { status: KFNotebook["status"] }) {
  if (status === "Running")
    return (
      <Badge variant="success" className="gap-1">
        <span className="size-1.5 animate-pulse rounded-full bg-brand-mint-500" />
        Running
      </Badge>
    );
  if (status === "Starting")
    return (
      <Badge variant="warning" className="gap-1">
        <span className="size-1.5 animate-pulse rounded-full bg-amber-500" />
        Starting
      </Badge>
    );
  if (status === "Error") return <Badge variant="destructive">Error</Badge>;
  return <Badge variant="secondary">Stopped</Badge>;
}

function StatChip({
  label,
  value,
  accent,
  hint,
}: {
  label: string;
  value: React.ReactNode;
  accent: "mint" | "amber" | "slate" | "indigo";
  hint: string;
}) {
  const map = {
    mint: "from-brand-mint-500/15 to-transparent",
    amber: "from-amber-500/15 to-transparent",
    slate: "from-slate-500/15 to-transparent",
    indigo: "from-brand-indigo-500/15 to-transparent",
  };
  return (
    <Card className={cn("relative overflow-hidden p-4")}>
      <div className={cn("pointer-events-none absolute inset-0 bg-gradient-to-br", map[accent])} />
      <div className="relative">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="mt-1 text-2xl font-bold tabular-nums">{value}</div>
        <div className="text-[11px] text-muted-foreground">{hint}</div>
      </div>
    </Card>
  );
}

const IMAGE_CATALOG: {
  name: string;
  description: string;
  tags: string[];
  icon: React.ComponentType<{ className?: string }>;
  color: string;
  recommended?: boolean;
  /** 실제 생성 가능한 템플릿 프리셋 — 없으면 해당 가속기 하드웨어 미도입 */
  preset?: NotebookPreset;
  /** 미제공 사유 배지 문구 */
  unavailableNote?: string;
}[] = [
  {
    name: "Kubeflow Code Server (Python)",
    description: "VSCode 기반 클라우드 IDE — Python 3.11 + Conda + Git",
    tags: ["Python 3.11", "VSCode", "code-server"],
    icon: Code2,
    color: "bg-sky-100 text-sky-700",
    recommended: true,
    preset: { template: "codeserver", cpu: "2", memoryGi: 4, gpu: 0, storageGb: 10 },
  },
  {
    name: "Kubeflow Notebook (NB)",
    description: "Notebook 서버 — interactive 코드 셀·문서 작성",
    tags: ["Python 3.11", "Notebook", "interactive"],
    icon: BookOpen,
    color: "bg-brand-indigo-100 text-brand-indigo-700",
    preset: { template: "jupyter", cpu: "2", memoryGi: 4, gpu: 0, storageGb: 10 },
  },
  {
    name: "RBLN ATOM SDK",
    description: "Rebellions ATOM NPU 컴파일러·런타임 포함",
    tags: ["rbln-sdk 1.4.0", "PyTorch", "ATOM"],
    icon: Cpu,
    color: "bg-brand-mint-50 text-brand-mint-700",
    unavailableNote: "ATOM NPU 도입 후 제공",
  },
  {
    name: "RBLN REBEL Quad SDK",
    description: "Rebellions REBEL 추론 가속 워크플로우",
    tags: ["rbln-sdk 1.4.0", "vLLM", "REBEL"],
    icon: Cpu,
    color: "bg-brand-mint-50 text-brand-mint-700",
    unavailableNote: "REBEL NPU 도입 후 제공",
  },
  {
    name: "CUDA 12.4 Training",
    description: "NVIDIA A100 학습 — DeepSpeed·Megatron-LM 포함",
    tags: ["CUDA 12.4", "DeepSpeed", "PyTorch 2.5"],
    icon: Cpu,
    color: "bg-brand-indigo-100 text-brand-indigo-700",
    preset: { template: "cuda", cpu: "4", memoryGi: 8, gpu: 1, storageGb: 50 },
  },
  {
    name: "PIM 실험 환경",
    description: "Samsung HBM-PIM SDK 베타 (GEMV 가속)",
    tags: ["pim-sdk 0.7", "beta", "GEMV"],
    icon: Cpu,
    color: "bg-rose-100 text-rose-700",
    unavailableNote: "HBM-PIM 도입 후 제공",
  },
];
