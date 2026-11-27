"use client";
import * as React from "react";
import { ChangeMark } from "@/components/change-mark";
import {
  GitBranch,
  Play,
  CheckCircle2,
  XCircle,
  Clock,
  Database,
  Upload,
  FileCode,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { ainbFetch, usePipelines } from "@/lib/ainb-api";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { uploadPipeline } from "@/lib/notebook-api";
import { useProjectContext } from "@/lib/project-context";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn, relativeTime } from "@/lib/utils";
import type { KFPipeline } from "@/lib/types";

/** 새 실행 이름 — 파이프라인 이름 + 시각 뒷자리 */
function newRunName(pipeline: string) {
  return `${pipeline}-run-${Date.now() % 10000}`;
}

interface KFRun {
  id: string;
  name: string;
  state: string;
  createdAt: string;
  finishedAt: string;
}

export default function PipelinesPage() {
  const { data, isLoading } = usePipelines() as {
    data: { items: KFPipeline[] } | undefined;
    isLoading: boolean;
  };
  const [selected, setSelected] = React.useState<string | undefined>();
  const [uploadOpen, setUploadOpen] = React.useState(false);
  const qc = useQueryClient();
  // 위에서 고른 프로젝트의 파이프라인만. KFP 등록본은 네임스페이스가 없어 공용으로 보인다
  const { match } = useProjectContext();
  const items = (data?.items ?? []).filter((p) => match(p.namespace));
  const active = (selected ? items.find((p) => p.id === selected) : undefined) ?? items[0];

  // 업로드된 정의에서 추출한 실제 단계 그래프
  const { data: dagData } = useQuery({
    queryKey: ["kf", "dag", active?.id],
    queryFn: async () => {
      const res = await ainbFetch(`/pipelines/${active!.id}/dag`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{
        nodes: { id: string; name: string; dependsOn: string[];
                 image: string; kind: string; level: number }[];
      }>;
    },
    enabled: !!active,
  });

  const liveDag = React.useMemo(
    () =>
      (dagData?.nodes ?? []).map((n) => ({
        id: n.id,
        name: n.name,
        type: (["data", "compute", "ml", "deploy"].includes(n.kind)
          ? n.kind
          : "compute") as "data" | "compute" | "ml" | "deploy",
        status: "ok" as const,
      })),
    [dagData],
  );

  const { data: artifacts } = useQuery({
    queryKey: ["kf", "artifacts", active?.id],
    queryFn: async () => {
      const res = await ainbFetch(`/pipelines/${active!.id}/artifacts`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{
        items: { name: string; step?: string; run?: string; path: string;
                 sizeMb: number; updatedAt: string }[];
      }>;
    },
    enabled: !!active,
    refetchInterval: 15000,
  });

  const { data: runsData } = useQuery({
    queryKey: ["kf", "runs", active?.id],
    queryFn: async () => {
      const res = await ainbFetch(`/pipelines/${active!.id}/runs`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: KFRun[] }>;
    },
    enabled: !!active,
    refetchInterval: 5000,
  });

  async function downloadArtifact(path: string, name: string) {
    try {
      const res = await ainbFetch(
        `/artifacts/download?path=${encodeURIComponent(path)}`);
      if (!res.ok) throw new Error(`${res.status}`);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = name;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error(`다운로드 실패: ${(e as Error).message}`);
    }
  }

  async function downloadYaml() {
    if (!active) return;
    try {
      const res = await ainbFetch(`/pipelines/${active.id}/yaml`);
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${active.name.replace(/\s+/g, "_")}.yaml`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("YAML 파일을 내려받았습니다");
    } catch (e) {
      toast.error(`다운로드 실패: ${(e as Error).message}`);
    }
  }

  async function startRun() {
    if (!active) return;
    try {
      const res = await ainbFetch(`/pipelines/${active.id}/runs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: newRunName(active.name) }),
      });
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
      toast.success("Run 생성 완료 — 워크플로우 실행 중");
      qc.invalidateQueries({ queryKey: ["kf", "runs", active.id] });
      qc.invalidateQueries({ queryKey: ["kf", "pipelines"] });
    } catch (e) {
      toast.error(`실행 실패: ${(e as Error).message}`);
    }
  }

  async function deletePipeline() {
    if (!active) return;
    if (!window.confirm(`"${active.name}" 파이프라인을 삭제할까요?`)) return;
    try {
      let res = await ainbFetch(`/pipelines/${active.id}`,
                                { method: "DELETE" });

      // 실행 기록·예약이 남아 있으면 함께 지울지 한 번 더 확인
      if (res.status === 409) {
        const d = (await res.json()).detail as {
          runs: number; schedules: number;
        };
        const parts = [
          d.runs ? `실행 기록 ${d.runs}건` : "",
          d.schedules ? `예약 ${d.schedules}건` : "",
        ].filter(Boolean).join("과 ");
        if (!window.confirm(
          `"${active.name}"에는 ${parts}이 남아 있습니다.\n` +
          `함께 삭제할까요? (되돌릴 수 없습니다)`)) return;
        res = await ainbFetch(`/pipelines/${active.id}?force=true`,
                              { method: "DELETE" });
      }
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(
          typeof body.detail === "string" ? body.detail : `${res.status}`);
      }
      const done = await res.json();
      const rm = done.removed ?? {};
      const extra = [
        rm.runs ? `실행 기록 ${rm.runs}건` : "",
        rm.schedules ? `예약 ${rm.schedules}건` : "",
      ].filter(Boolean).join(", ");
      toast.success(`"${done.name}" 삭제 완료${extra ? ` (${extra} 포함)` : ""}`);
      setSelected(undefined);
      qc.invalidateQueries({ queryKey: ["kf", "pipelines"] });
    } catch (e) {
      toast.error(`삭제 실패: ${(e as Error).message}`);
    }
  }

  if (isLoading || !data) return <Skeleton className="h-96" />;

  return (
    <div className="grid gap-4 lg:grid-cols-[360px_minmax(0,1fr)]">
      <UploadPipelineDialog open={uploadOpen} onClose={() => setUploadOpen(false)} />
      {/* Pipeline list */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-base">
            <span>파이프라인</span>
            <div className="flex items-center gap-2">
              <Badge variant="ghost">{items.length}</Badge>
              <ChangeMark ids={["CR-AID-02"]} inline><Button size="sm" onClick={() => setUploadOpen(true)}>
                <Upload className="size-3.5" /> YAML 업로드
              </Button></ChangeMark>
            </div>
          </CardTitle>
          <CardDescription className="text-[11px]">
            KFP Python DSL로 컴파일된 YAML 파일을 직접 업로드할 수 있다.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          <div className="divide-y divide-border">
            {items.map((p) => (
              <button
                key={p.id}
                onClick={() => setSelected(p.id)}
                className={cn(
                  "block w-full px-5 py-3 text-left transition-colors hover:bg-secondary/40",
                  active?.id === p.id && "bg-secondary/40",
                )}
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex items-start gap-2">
                    <GitBranch className="size-4 mt-0.5 text-violet-600" />
                    <div>
                      <div className="text-sm font-semibold">{p.name}</div>
                      <div className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{p.description}</div>
                    </div>
                  </div>
                  {p.acceleratorKind && (
                    <Badge variant="outline" className="font-mono text-[10px]">{p.acceleratorKind}</Badge>
                  )}
                </div>
                <div className="mt-2 grid grid-cols-3 gap-2 text-[11px]">
                  <span><strong className="font-mono">{p.steps}</strong> steps</span>
                  <span><strong className="font-mono">{p.runs}</strong> runs</span>
                  <span className={p.successRate > 0.9 ? "text-brand-mint-700" : "text-amber-600"}>
                    {(p.successRate * 100).toFixed(0)}% success
                  </span>
                </div>
              </button>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Active pipeline DAG */}
      {active && (
        <div className="min-w-0 space-y-4">
          <Card>
            <CardHeader className="flex-row items-center justify-between">
              <div>
                <CardTitle>{active.name}</CardTitle>
                <CardDescription>{active.description}</CardDescription>
              </div>
              <div className="flex items-center gap-2">
                <Button variant="outline" size="sm" onClick={downloadYaml}>YAML 다운로드</Button>
                <Button size="sm" onClick={startRun}>
                  <Play className="size-3.5" /> 새 실행
                </Button>
                <Button variant="ghost" size="sm" onClick={deletePipeline}
                        className="text-rose-600 hover:bg-rose-50">
                  <Trash2 className="size-3.5" /> 삭제
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              <div className="overflow-x-auto">
                {liveDag.length > 0 ? (
                  <>
                    <div className="flex min-w-max items-center gap-2">
                      {liveDag.map((step, i, arr) => (
                        <React.Fragment key={step.id}>
                          <PipelineStep step={step} />
                          {i < arr.length - 1 && (
                            <div className="flex h-px w-12 items-center justify-center bg-border" />
                          )}
                        </React.Fragment>
                      ))}
                    </div>
                    {dagData && (
                      <p className="mt-3 text-[11px] text-muted-foreground">
                        업로드된 정의에서 자동 추출한 실제 단계입니다 · 총 {liveDag.length}단계
                      </p>
                    )}
                  </>
                ) : (
                  <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">
                    단계 정보를 불러오는 중입니다
                  </div>
                )}
              </div>
            </CardContent>
          </Card>

          <Tabs defaultValue="runs">
            <TabsList>
              <TabsTrigger value="runs">실행 이력</TabsTrigger>
              <TabsTrigger value="schedule">스케줄</TabsTrigger>
              <TabsTrigger value="artifacts">아티팩트</TabsTrigger>
            </TabsList>
            <TabsContent value="runs">
              <Card>
                <CardContent className="p-0">
                  <div className="divide-y divide-border">
                    {(runsData?.items ?? []).length === 0 && (
                      <div className="px-5 py-6 text-center text-xs text-muted-foreground">
                        실행 이력이 없습니다 — 상단 &quot;새 실행&quot;으로 시작하세요
                      </div>
                    )}
                    {(runsData?.items ?? []).map((r) => (
                      <div key={r.id} className="flex items-center gap-3 px-5 py-3">
                        <div className={cn(
                          "flex size-7 items-center justify-center rounded-full",
                          r.state === "SUCCEEDED" ? "bg-brand-mint-100 text-brand-mint-700" :
                            r.state === "FAILED" ? "bg-rose-100 text-rose-700" : "bg-brand-indigo-100 text-brand-indigo-700",
                        )}>
                          {r.state === "FAILED" ? <XCircle className="size-3.5" /> : <CheckCircle2 className="size-3.5" />}
                        </div>
                        <div className="flex-1">
                          <div className="font-mono text-sm">{r.name}</div>
                          <div className="text-xs text-muted-foreground">
                            <span>{relativeTime(new Date(r.createdAt))}</span>
                            <span className="mx-1">·</span>
                            <span className="font-mono text-[10px]">{r.id.slice(0, 8)}</span>
                          </div>
                        </div>
                        <Badge variant={
                          r.state === "SUCCEEDED" ? "success" :
                            r.state === "FAILED" ? "destructive" : "warning"
                        }>
                          {r.state === "SUCCEEDED" ? "Succeeded" : r.state === "FAILED" ? "Failed" : "Running"}
                        </Badge>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            </TabsContent>
            <TabsContent value="schedule">
              <SchedulePanel pipelineId={active.id} pipelineName={active.name} />
            </TabsContent>
            <TabsContent value="artifacts">
              <Card>
                <CardContent className="p-0">
                  <div className="divide-y divide-border">
                    {(artifacts?.items ?? []).length === 0 && (
                      <div className="px-5 py-6 text-center text-xs text-muted-foreground">
                        이 파이프라인의 실행 결과물이 아직 없습니다 —
                        실행이 완료되면 생성된 파일이 여기에 표시됩니다
                      </div>
                    )}
                    {(artifacts?.items ?? []).map((a) => (
                      <div key={a.path} className="flex items-center gap-3 px-5 py-2.5">
                        <Database className="size-4 text-brand-indigo-600" />
                        <div className="flex-1">
                          <div className="font-mono text-sm">{a.name}</div>
                          <div className="text-[10px] text-muted-foreground">
                            {a.step ? `${a.step} · ` : ""}{a.updatedAt}
                          </div>
                        </div>
                        <span className="font-mono text-xs text-muted-foreground tabular-nums">
                          {a.sizeMb < 0.01 ? "< 0.01" : a.sizeMb} MB
                        </span>
                        <Button variant="ghost" size="sm"
                                onClick={() => downloadArtifact(a.path, a.name)}>
                          다운로드
                        </Button>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            </TabsContent>
          </Tabs>
        </div>
      )}
    </div>
  );
}

/* ─────────────────────────────────────────────────────────
 * 스케줄 — 정해진 시각에 파이프라인을 자동 실행 (KFP RecurringRun)
 * ───────────────────────────────────────────────────────── */
interface Schedule {
  id: string; name: string; cron: string; status: string; createdAt: string;
}

const CRON_PRESETS = [
  { label: "매일 새벽 2시", cron: "0 2 * * *" },
  { label: "매일 정오", cron: "0 12 * * *" },
  { label: "매주 월요일 오전 9시", cron: "0 9 * * 1" },
  { label: "매시간", cron: "0 * * * *" },
];

/** cron을 사람이 읽는 문장으로 (KFP는 6자리: 초 분 시 일 월 요일) */
function cronToKorean(cron: string): string {
  const p = cron.trim().split(/\s+/);
  const [min, hour, dom, , dow] = p.length === 6 ? p.slice(1) : p;
  const days = ["일", "월", "화", "수", "목", "금", "토"];
  const time = `${String(hour).padStart(2, "0")}:${String(min).padStart(2, "0")}`;
  if (hour === "*") return "매시간";
  if (dow !== "*" && dow !== undefined && !isNaN(Number(dow)))
    return `매주 ${days[Number(dow) % 7]}요일 ${time}`;
  if (dom !== "*") return `매월 ${dom}일 ${time}`;
  return `매일 ${time}`;
}

function SchedulePanel({ pipelineId, pipelineName }:
  { pipelineId: string; pipelineName: string }) {
  const qc = useQueryClient();
  const [cron, setCron] = React.useState("0 2 * * *");
  const [busy, setBusy] = React.useState(false);

  const { data } = useQuery({
    queryKey: ["kf", "schedules", pipelineId],
    queryFn: async () => {
      const res = await ainbFetch(`/pipelines/${pipelineId}/schedules`);
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json() as Promise<{ items: Schedule[] }>;
    },
    refetchInterval: 15000,
  });

  async function call(path: string, method: string, okMsg: string, body?: object) {
    setBusy(true);
    try {
      const res = await ainbFetch(path, {
        method,
        ...(body ? { headers: { "Content-Type": "application/json" },
                     body: JSON.stringify(body) } : {}),
      });
      if (!res.ok) throw new Error((await res.json()).detail ?? `${res.status}`);
      toast.success(okMsg);
      qc.invalidateQueries({ queryKey: ["kf", "schedules", pipelineId] });
    } catch (e) {
      toast.error(`${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  const items = data?.items ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">자동 실행 예약</CardTitle>
        <CardDescription>
          정해진 시각에 <b>{pipelineName}</b> 파이프라인이 자동으로 실행됩니다 —
          매일 새벽 재학습 같은 운영에 씁니다
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-secondary/20 p-3">
          <div className="flex-1 space-y-1.5">
            <Label htmlFor="cron">실행 주기</Label>
            <div className="flex flex-wrap gap-1.5">
              {CRON_PRESETS.map((p) => (
                <button key={p.cron} type="button" onClick={() => setCron(p.cron)}
                  className={cn(
                    "rounded-full border px-2.5 py-1 text-[11px] transition-colors",
                    cron === p.cron
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-border hover:bg-secondary",
                  )}>
                  {p.label}
                </button>
              ))}
            </div>
            <Input id="cron" value={cron} onChange={(e) => setCron(e.target.value)}
                   className="mt-1 font-mono text-xs" placeholder="분 시 일 월 요일" />
            <p className="text-[11px] text-muted-foreground">
              현재 설정: <b>{cronToKorean(cron)}</b>
            </p>
          </div>
          <Button size="sm" disabled={busy}
                  onClick={() => call(`/pipelines/${pipelineId}/schedules`,
                                      "POST", "예약을 등록했습니다", { cron })}>
            <Clock className="size-3.5" /> 예약 추가
          </Button>
        </div>

        <div className="divide-y divide-border rounded-lg border border-border">
          {items.length === 0 && (
            <div className="px-4 py-6 text-center text-xs text-muted-foreground">
              등록된 예약이 없습니다 — 위에서 주기를 골라 추가하세요
            </div>
          )}
          {items.map((s) => (
            <div key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <Clock className="size-4 text-violet-600" />
              <div className="flex-1">
                <div className="text-sm font-medium">{s.name}</div>
                <div className="text-[11px] text-muted-foreground">
                  {cronToKorean(s.cron)} · <span className="font-mono">{s.cron}</span> · 등록 {s.createdAt}
                </div>
              </div>
              <Badge variant={s.status === "ENABLED" ? "success" : "secondary"}>
                {s.status === "ENABLED" ? "동작 중" : "중지됨"}
              </Badge>
              <Button size="sm" variant="outline" disabled={busy}
                      onClick={() => call(
                        `/schedules/${s.id}/toggle?enable=${s.status !== "ENABLED"}`,
                        "POST", s.status === "ENABLED" ? "예약을 중지했습니다" : "예약을 다시 켰습니다")}>
                {s.status === "ENABLED" ? "중지" : "시작"}
              </Button>
              <Button size="sm" variant="ghost" disabled={busy}
                      className="text-rose-600 hover:bg-rose-50"
                      onClick={() => {
                        if (!window.confirm(`"${s.name}" 예약을 삭제할까요?`)) return;
                        call(`/schedules/${s.id}`, "DELETE", "예약을 삭제했습니다");
                      }}>
                삭제
              </Button>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function PipelineStep({
  step,
}: {
  step: { id: string; name: string; type: "data" | "compute" | "ml" | "deploy"; status: "ok" | "running" | "failed" | "pending" };
}) {
  const typeColor = {
    data: "border-sky-200 bg-sky-50 text-sky-800",
    compute: "border-violet-200 bg-violet-50 text-violet-800",
    ml: "border-brand-mint-200 bg-brand-mint-50 text-brand-mint-800",
    deploy: "border-rose-200 bg-rose-50 text-rose-800",
  };
  const statusIcon = {
    ok: <CheckCircle2 className="size-3.5 text-brand-mint-600" />,
    running: <span className="size-2 animate-pulse rounded-full bg-amber-500" />,
    failed: <XCircle className="size-3.5 text-rose-600" />,
    pending: <Clock className="size-3.5 text-muted-foreground" />,
  };
  return (
    <div
      className={cn(
        "flex min-w-[160px] flex-col gap-1 rounded-lg border px-3 py-2.5 transition-shadow hover:shadow-md",
        typeColor[step.type],
      )}
    >
      <div className="flex items-center justify-between text-[10px] uppercase tracking-wider opacity-80">
        <span>{step.type}</span>
        {statusIcon[step.status]}
      </div>
      <div className="text-sm font-semibold leading-tight">{step.name}</div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────
// YAML 업로드 다이얼로그 — ETRI 260615 §6.B 반영
// ─────────────────────────────────────────────────────────────

function UploadPipelineDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const [fileName, setFileName] = React.useState<string | null>(null);
  const [fileYaml, setFileYaml] = React.useState("");
  const [pastedYaml, setPastedYaml] = React.useState("");
  const [mode, setMode] = React.useState<"file" | "paste">("file");
  const [busy, setBusy] = React.useState(false);
  const qc = useQueryClient();

  const submit = async () => {
    if (!name.trim()) return;
    const yaml = mode === "file" ? fileYaml : pastedYaml;
    if (!yaml.trim()) return;
    setBusy(true);
    try {
      await uploadPipeline({ name, description: desc, yaml });
      toast.success(`Pipeline '${name}' 업로드 완료 — KFP 카탈로그 등록됨`);
      qc.invalidateQueries({ queryKey: ["kf", "pipelines"] });
      setName("");
      setFileName(null);
      setFileYaml("");
      setPastedYaml("");
      onClose();
    } catch (e) {
      toast.error(`업로드 실패: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FileCode className="size-5 text-brand-indigo-600" />
            Pipeline YAML 업로드
          </DialogTitle>
          <DialogDescription>
            KFP Python DSL로 컴파일된 YAML 파일 또는 매니페스트를 직접 붙여넣어 등록한다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>파이프라인 이름</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-training-pipeline" />
            </div>
            <div>
              <Label>설명 (선택)</Label>
              <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="학습·평가 파이프라인" />
            </div>
          </div>
          <p className="text-[11px] text-muted-foreground">
            파이프라인은 전체 워크스페이스가 공유합니다. 실행할 자원은 실행 시점에 결정됩니다.
          </p>

          <div className="flex gap-1 rounded-md border border-border p-0.5">
            <button
              type="button"
              onClick={() => setMode("file")}
              className={cn(
                "flex-1 rounded-sm px-2 py-1.5 text-xs font-medium transition-colors",
                mode === "file" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-secondary",
              )}
            >
              파일 업로드
            </button>
            <button
              type="button"
              onClick={() => setMode("paste")}
              className={cn(
                "flex-1 rounded-sm px-2 py-1.5 text-xs font-medium transition-colors",
                mode === "paste" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-secondary",
              )}
            >
              YAML 붙여넣기
            </button>
          </div>

          {mode === "file" && (
            <label className="block cursor-pointer rounded-lg border-2 border-dashed border-border p-6 text-center transition-colors hover:border-primary/50">
              <input
                type="file"
                accept=".yaml,.yml"
                className="hidden"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  setFileName(f?.name ?? null);
                  setFileYaml(f ? await f.text() : "");
                }}
              />
              <Upload className="mx-auto mb-2 size-6 text-muted-foreground" />
              <div className="text-sm font-medium">
                {fileName ?? "파일을 여기로 드래그하거나 클릭하여 선택"}
              </div>
              <div className="mt-1 text-[11px] text-muted-foreground">.yaml · .yml · KFP v2 매니페스트</div>
            </label>
          )}

          {mode === "paste" && (
            <div>
              <Label className="text-[11px]">YAML 내용</Label>
              <textarea
                value={pastedYaml}
                onChange={(e) => setPastedYaml(e.target.value)}
                className="mt-1 min-h-[160px] w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs"
                placeholder={`apiVersion: argoproj.io/v1alpha1\nkind: Workflow\nmetadata:\n  name: my-training-pipeline\n...`}
              />
            </div>
          )}

          <div className="rounded-md border border-amber-200 bg-amber-50/60 p-2 text-[11px] text-amber-900">
            업로드 후 자동 검증 — DSL 스키마 · 아티팩트 참조 · 자원 요구사항 유효성 확인 후 카탈로그에 등록된다.
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit} disabled={busy}>
            <Upload className="size-3.5" /> {busy ? "업로드 중…" : "업로드 · 등록"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
