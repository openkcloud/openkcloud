"use client";
import { ChangeMark } from "@/components/change-mark";
import * as React from "react";
import {
  Search,
  Filter,
  Clock,
  ChevronRight,
  ChevronDown,
  Cpu,
  Database,
  Network,
  Code2,
} from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FeatureGate } from "@/components/feature-gate";
import { useFeatureVisible } from "@/lib/scope";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";

// Dynatrace PurePath style: tree of spans with timeline bars
type Span = {
  name: string;
  service: string;
  startMs: number;
  durMs: number;
  kind: "http" | "compute" | "db" | "queue" | "model";
  children?: Span[];
};

const TRACE: Span = {
  name: "POST /v1/solar-10b/chat",
  service: "api-gateway",
  startMs: 0,
  durMs: 1042,
  kind: "http",
  children: [
    {
      name: "OAuth Authorize",
      service: "auth-svc",
      startMs: 2,
      durMs: 8,
      kind: "http",
    },
    {
      name: "vllm router → solar-10b",
      service: "vllm-router",
      startMs: 14,
      durMs: 1018,
      kind: "compute",
      children: [
        {
          name: "BPE Tokenize",
          service: "vllm-router",
          startMs: 14,
          durMs: 12,
          kind: "compute",
        },
        {
          name: "Embedding lookup",
          service: "vllm-solar-10b",
          startMs: 28,
          durMs: 28,
          kind: "model",
        },
        {
          name: "Attention layers 0-15",
          service: "vllm-solar-10b",
          startMs: 56,
          durMs: 184,
          kind: "model",
        },
        {
          name: "Attention layers 16-31",
          service: "vllm-solar-10b",
          startMs: 240,
          durMs: 322,
          kind: "model",
        },
        {
          name: "KV cache lookup (miss 41%)",
          service: "vllm-solar-10b",
          startMs: 256,
          durMs: 184,
          kind: "db",
        },
        {
          name: "FFN layers",
          service: "vllm-solar-10b",
          startMs: 562,
          durMs: 218,
          kind: "model",
        },
        {
          name: "Sampling (top_p=0.9)",
          service: "vllm-solar-10b",
          startMs: 780,
          durMs: 84,
          kind: "compute",
        },
        {
          name: "Detokenize",
          service: "vllm-router",
          startMs: 864,
          durMs: 32,
          kind: "compute",
        },
        {
          name: "Response stream → client",
          service: "vllm-router",
          startMs: 896,
          durMs: 136,
          kind: "http",
        },
      ],
    },
    {
      name: "Metrics emit (async)",
      service: "obs-collector",
      startMs: 1034,
      durMs: 6,
      kind: "queue",
    },
  ],
};

const KIND_COLOR: Record<Span["kind"], string> = {
  http: "bg-brand-indigo-500",
  compute: "bg-violet-500",
  db: "bg-rose-500",
  queue: "bg-amber-500",
  model: "bg-brand-mint-500",
};
const KIND_LABEL: Record<Span["kind"], string> = {
  http: "HTTP",
  compute: "Compute",
  db: "Cache",
  queue: "Queue",
  model: "Model",
};
const KIND_ICON: Record<Span["kind"], React.ComponentType<{ className?: string }>> = {
  http: Network,
  compute: Code2,
  db: Database,
  queue: Database,
  model: Cpu,
};

const RECENT_TRACES = [
  { id: "tr-001", svc: "vllm-solar-10b", op: "chat", dur: 1042, status: "WARN", ts: "2분 전" },
  { id: "tr-002", svc: "vllm-solar-10b", op: "chat", dur: 980, status: "OK", ts: "3분 전" },
  { id: "tr-003", svc: "stt-atom-edge", op: "transcribe", dur: 224, status: "OK", ts: "4분 전" },
  { id: "tr-004", svc: "vllm-solar-10b", op: "chat", dur: 1180, status: "WARN", ts: "5분 전" },
  { id: "tr-005", svc: "kserve-furiosa-rngd", op: "predict", dur: 382, status: "OK", ts: "6분 전" },
  { id: "tr-006", svc: "stt-rngd-prod", op: "transcribe", dur: 418, status: "OK", ts: "7분 전" },
];

export default function TracesPage() {
  const internalVisible = useFeatureVisible("monitoring.traces.internal");
  const [selectedTrace, setSelectedTrace] = React.useState(RECENT_TRACES[0]);
  return (
    <div className="space-y-4">
    <ChangeMark ids={["CR-OBS-07"]}>
<div className="rounded-md border border-sky-200 bg-sky-50/60 px-3 py-2 text-[11px] text-sky-900">
      <strong>범위</strong> — 분산 트레이스는 트레이스 식별자가 아직 구축되지 않았고 Tempo 연계가 3차년도 항목이라 <strong>2차년도 시범서비스에서는 메뉴 전체를 제외</strong>한다.
      이 화면은 최종 형상 설계안으로만 두며(환경설정 › 포털 형상에서 전환), 제공 범위는 서비스 흐름 모니터링과 통합한 공통 서비스 트레이스의 기본 정보(요청·서비스·소요 시간·상태)다.
    </div>
</ChangeMark>
    <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
      <div className="space-y-4">
        <Card className="p-4">
          <div className="space-y-2">
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input placeholder="trace ID, 모델, 사용자…" className="pl-9" />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Select defaultValue="all">
                <SelectTrigger className="text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">모든 서비스</SelectItem>
                  {internalVisible && <SelectItem value="vllm">vLLM (내부 span)</SelectItem>}
                  <SelectItem value="stt">STT</SelectItem>
                </SelectContent>
              </Select>
              <Select defaultValue="all">
                <SelectTrigger className="text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">모든 상태</SelectItem>
                  <SelectItem value="ok">OK</SelectItem>
                  <SelectItem value="warn">WARN</SelectItem>
                  <SelectItem value="err">ERROR</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-sm">최근 트레이스</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <div className="divide-y divide-border">
              {RECENT_TRACES.map((t, i) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => setSelectedTrace(t)}
                  className={cn(
                    "block w-full px-4 py-2.5 text-left transition-colors hover:bg-secondary/30",
                    (selectedTrace ? selectedTrace.id === t.id : i === 0) && "bg-secondary/40",
                  )}
                >
                  <div className="flex items-center justify-between">
                    <span className="font-mono text-xs">{t.id}</span>
                    <Badge
                      variant={t.status === "OK" ? "success" : "warning"}
                      className="text-[10px]"
                    >
                      {t.status}
                    </Badge>
                  </div>
                  <div className="mt-0.5 text-xs">{t.svc}</div>
                  <div className="flex items-center justify-between text-[10px] text-muted-foreground">
                    <span>{t.op}</span>
                    <span className="font-mono">{t.dur}ms · {t.ts}</span>
                  </div>
                </button>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="space-y-4">
        {/* Trace meta */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <span className="font-mono">{selectedTrace.id}</span>
              <Badge variant={selectedTrace.status === "OK" ? "success" : "warning"}>{selectedTrace.status === "OK" ? "OK · 목표 지연 이내" : "WARN · 목표 지연 초과"}</Badge>
            </CardTitle>
            <CardDescription>
              {selectedTrace.svc} · {selectedTrace.op} · {selectedTrace.dur}ms · 9개 서비스 통과 · 12개 span
            </CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 md:grid-cols-4">
            <Meta label="총 시간" value="1,042 ms" />
            <Meta label="가속기 시간" value="836 ms" subtext="80.2%" />
            <Meta label="네트워크" value="148 ms" subtext="14.2%" />
            <Meta label="대기/오버헤드" value="58 ms" subtext="5.6%" />
          </CardContent>
        </Card>

        {/* Trace waterfall */}
        <FeatureGate id="monitoring.traces.internal">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">PurePath — 호출 트리</CardTitle>
            <CardDescription>span 트리 + 타임라인 바 (Dynatrace 스타일)</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <div className="grid grid-cols-[60%_40%]">
              <div className="border-r border-border">
                <div className="border-b border-border bg-secondary/30 px-4 py-2 text-[10px] uppercase text-muted-foreground">
                  Span
                </div>
                <SpanTree span={TRACE} depth={0} totalDur={TRACE.durMs} />
              </div>
              <div>
                <div className="border-b border-border bg-secondary/30 px-3 py-2 text-[10px] uppercase text-muted-foreground">
                  Timeline · 0–{TRACE.durMs}ms
                </div>
                <TimelineColumn span={TRACE} totalDur={TRACE.durMs} />
              </div>
            </div>
          </CardContent>
        </Card>
        </FeatureGate>
      </div>
    </div>
    </div>
  );
}

function Meta({ label, value, subtext }: { label: string; value: string; subtext?: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase text-muted-foreground">{label}</div>
      <div className="font-mono text-lg font-bold tabular-nums">{value}</div>
      {subtext && <div className="text-[10px] text-muted-foreground">{subtext}</div>}
    </div>
  );
}

function SpanTree({ span, depth, totalDur }: { span: Span; depth: number; totalDur: number }) {
  const Icon = KIND_ICON[span.kind];
  return (
    <>
      <div
        className="flex items-center gap-2 border-b border-border px-3 py-1.5 text-xs"
        style={{ paddingLeft: `${12 + depth * 16}px` }}
      >
        {span.children?.length ? <ChevronDown className="size-3 text-muted-foreground" /> : <span className="w-3" />}
        <span className={cn("size-1.5 rounded-full", KIND_COLOR[span.kind])} />
        <Icon className="size-3 text-muted-foreground" />
        <span className="truncate font-medium">{span.name}</span>
        <span className="ml-auto font-mono text-[10px] text-muted-foreground">{span.service}</span>
      </div>
      {span.children?.map((c, i) => (
        <SpanTree key={i} span={c} depth={depth + 1} totalDur={totalDur} />
      ))}
    </>
  );
}

function TimelineColumn({ span, totalDur }: { span: Span; totalDur: number }) {
  const flat: Span[] = [];
  const collect = (s: Span) => {
    flat.push(s);
    s.children?.forEach(collect);
  };
  collect(span);
  return (
    <>
      {flat.map((s, i) => (
        <div key={i} className="relative h-[31px] border-b border-border px-3">
          <div className="absolute inset-y-0 flex items-center" style={{ left: `${(s.startMs / totalDur) * 100}%`, width: `${(s.durMs / totalDur) * 100}%`, minWidth: "2px" }}>
            <div
              className={cn(
                "h-3 w-full rounded-sm",
                KIND_COLOR[s.kind],
                s.durMs > 200 && s.kind === "db" && "bg-rose-500",
              )}
            />
          </div>
          <div className="absolute right-3 top-1/2 -translate-y-1/2 font-mono text-[10px] text-muted-foreground tabular-nums">
            {s.durMs} ms
          </div>
        </div>
      ))}
    </>
  );
}
