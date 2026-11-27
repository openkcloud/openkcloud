"use client";
import * as React from "react";
import { Send, Activity, Cpu, Plus, Trash2, Settings2, MessageSquare, Code2 } from "lucide-react";
import { LlmChatDialog } from "@/components/llm-chat-dialog";
import { ServingCodeDialog } from "@/components/serving-code-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@/components/page-header";
import { ServingCreateDialog } from "@/components/serving-create-dialog";
import { ServingResourceDialog } from "@/components/serving-resource-dialog";
import { ainbFetch, useServing } from "@/lib/ainb-api";
import { useProjectContext } from "@/lib/project-context";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber } from "@/lib/utils";
import type { KFInferenceService } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";

/** 호출 테스트 — 모델 형식에 맞는 샘플 입력을 백엔드가 만들어 호출한다 */
async function testCall(s: KFInferenceService) {
  try {
    const res = await ainbFetch(`/serving/${s.name}/test`,
                                { method: "POST" });
    const json = await res.json();
    if (!res.ok) {
      throw new Error(typeof json.detail === "string" ? json.detail : `${res.status}`);
    }
    const first = json.result?.predictions?.[0];
    const summary = first && typeof first === "object" && "prediction" in first
      ? `판정 ${first.prediction} (확신도 ${first.confidence}%)`
      : JSON.stringify(json.result?.predictions ?? json.result).slice(0, 80);
    toast.success(`${s.name} 응답 ${json.latencyMs}ms`, {
      description: `${json.input} 입력 → ${summary}`,
    });
  } catch (e) {
    toast.error(`호출 실패: ${(e as Error).message}`);
  }
}

export default function ServingPage() {
  const { data, isLoading } = useServing() as {
    data: { items: KFInferenceService[] } | undefined;
    isLoading: boolean;
  };
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<KFInferenceService | null>(null);
  const [chatting, setChatting] = React.useState<KFInferenceService | null>(null);
  const [coding, setCoding] = React.useState<KFInferenceService | null>(null);
  const [busy, setBusy] = React.useState<string | null>(null);

  async function removeServing(s: KFInferenceService) {
    if (!window.confirm(
      `"${s.name}" 서비스를 내릴까요?\n호출 중인 프로그램이 있으면 즉시 실패하게 됩니다.`))
      return;
    setBusy(s.name);
    try {
      const res = await ainbFetch(`/serving/${s.name}`, { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(typeof body.detail === "string" ? body.detail : `${res.status}`);
      }
      toast.success(`"${s.name}" 서비스를 내렸습니다`);
      qc.invalidateQueries({ queryKey: ["kf", "serving"] });
    } catch (e) {
      toast.error(`삭제 실패: ${(e as Error).message}`);
    } finally {
      setBusy(null);
    }
  }

  const { match } = useProjectContext();
  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.items.filter((x) => match(x.namespace));
  return (
    <div className="space-y-4">
      <ServingCreateDialog open={createOpen} onClose={() => setCreateOpen(false)} />
      <ServingResourceDialog service={editing} onClose={() => setEditing(null)} />
      <LlmChatDialog
        open={!!chatting}
        onOpenChange={(o) => !o && setChatting(null)}
        model={chatting?.name ?? ""}
        node={chatting?.namespace ?? ""}
        service={chatting?.name}
      />
      <ServingCodeDialog service={coding} onClose={() => setCoding(null)} />
      <PageHeader
        title="Serving — 모델 API 서비스"
        description="학습된 모델을 HTTP API로 배포합니다 (KServe)"
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-4" /> 서비스 배포
          </Button>
        }
      />

      {items.length === 0 && (
        <Card>
          <CardContent className="p-10 text-center text-sm text-muted-foreground">
            이 프로젝트에 배포된 서비스가 없습니다 — 오른쪽 위 &quot;서비스 배포&quot;로 시작하세요
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
      {items.map((s) => (
        <Card key={s.id}>
          <CardContent className="space-y-4 p-5">
            <div className="flex items-start justify-between">
              <div className="flex items-start gap-3">
                <div className="flex size-10 items-center justify-center rounded-lg bg-rose-100 text-rose-700">
                  <Send className="size-5" />
                </div>
                <div>
                  <div className="font-semibold">{s.name}</div>
                  <div className="font-mono text-[11px] text-muted-foreground">
                    {s.modelName} · {s.modelVersion}
                    {(s as { namespace?: string }).namespace && (
                      <> · <span title="워크스페이스(네임스페이스)">{(s as { namespace?: string }).namespace}</span></>
                    )}
                  </div>
                </div>
              </div>
              <Badge variant={s.status === "Ready" ? "success" : s.status === "Failed" ? "destructive" : "warning"} className="gap-1">
                {s.status === "Ready" && <span className="size-1.5 rounded-full bg-brand-mint-500" />}
                {s.status}
              </Badge>
            </div>

            {/* 안 되는 건 안 된다고 이유까지 보여준다 — Pending으로만 두면
                사용자가 몇 시간을 헛기다린다 */}
            {s.status !== "Ready" && (s as { statusReason?: string }).statusReason && (
              <p className={`rounded-md p-2 text-[11px] leading-snug ${
                s.status === "Failed"
                  ? "bg-red-50 text-red-700"
                  : "bg-amber-50 text-amber-700"}`}>
                {(s as { statusReason?: string }).statusReason}
              </p>
            )}

            <div className="grid grid-cols-4 gap-2 rounded-lg bg-secondary/40 p-3">
              <Metric label="서버 대수" value={s.replicas} />
              <Metric label="누적 호출" value={s.requests != null ? `${s.requests}건` : "—"} />
              {/* 호출 이력이 없으면 0이 아니라 '—'로 보여야 오해가 없다 */}
              <Metric label="초당 처리"
                      value={s.rpsAvg ? `${formatNumber(s.rpsAvg)}건` : "—"} />
              <Metric label="p95 지연"
                      value={s.p95LatencyMs ? `${s.p95LatencyMs}ms` : "—"} />
            </div>

            <div className="flex items-center justify-between text-xs">
              <button
                type="button"
                onClick={() => setEditing(s)}
                className="group inline-flex items-center gap-1 rounded-md border border-border px-2 py-1
                           transition-colors hover:border-brand-indigo-300 hover:bg-secondary/60"
                title="자원 변경"
              >
                <Cpu className="size-3" />
                <span className="font-mono">
                  {s.gpu ? `GPU ${s.gpu}장` : "CPU"}
                  {/* 어떤 장비의 어떤 GPU를 쓰는지까지 보여준다 */}
                  {s.gpu && (s as { gpuModel?: string }).gpuModel
                    ? ` (${(s as { gpuModel?: string }).gpuModel})`
                    : ""}
                  {s.cpu ? ` · ${s.cpu} vCPU` : ""}
                  {s.memoryGi ? ` · ${s.memoryGi}GB` : ""}
                </span>
                <Settings2 className="size-3 text-muted-foreground group-hover:text-brand-indigo-600" />
              </button>
              <div className="flex items-center gap-1 text-muted-foreground">
                <Activity className="size-3" />
                실시간
              </div>
            </div>

            <div className="rounded-md bg-card border border-border p-2 font-mono text-[11px] text-muted-foreground">
              {s.endpoint}
            </div>

            <div className="flex items-center gap-2">
              {s.status === "Ready" && (
                s.modelName === "vllm" ? (
                  // 대화형 LLM은 예측 API가 아니라 채팅으로 확인하는 게 자연스럽다
                  <Button size="sm" variant="outline" className="flex-1"
                          onClick={() => setChatting(s)}>
                    <MessageSquare className="size-3" /> AI와 대화하기
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" className="flex-1" onClick={() => testCall(s)}>
                    <Send className="size-3" /> API 호출 테스트
                  </Button>
                )
              )}
              {/* 삭제 후 재생성 없이 자원을 고칠 수 있게 — 눈에 보이는 버튼으로 */}
              <Button size="sm" variant="outline" onClick={() => setEditing(s)}
                      title="CPU·메모리·GPU 변경 (삭제 없이 적용)">
                <Settings2 className="size-3.5" /> 설정 변경
              </Button>
              <Button size="sm" variant="outline" onClick={() => setCoding(s)}
                      title="예제 코드 보기">
                <Code2 className="size-3.5" /> 예제 코드
              </Button>
              <Button size="sm" variant="ghost" disabled={busy === s.name}
                      className="text-rose-600 hover:bg-rose-50"
                      onClick={() => removeServing(s)}>
                <Trash2 className="size-3.5" />
              </Button>
            </div>
          </CardContent>
        </Card>
      ))}
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="text-center">
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="font-mono text-lg font-bold tabular-nums">{value}</div>
    </div>
  );
}
