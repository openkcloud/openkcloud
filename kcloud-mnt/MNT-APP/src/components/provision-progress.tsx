"use client";
/**
 * 프로비저닝 진행 — 진행 기록(MNT-CORE tkt_provision_step, 생성 중이면 1초마다 다시 조회) 공용 로직과 스테퍼 진행 타임라인.
 * 진행 시트(AI 워크스페이스)와 신청 상세 스테퍼(가상머신 · 서비스 클러스터)가 함께 쓴다.
 * 화면 시계: 진행 중엔 서버 시각보다 1.5초 늦게 그린다 — 조회(1초)가 먼저 와 있어, 끝난 단계를 모른 채 시간을 더 세다 되돌아가지 않게.
 */
import * as React from "react";
import { Check, ChevronDown, X } from "lucide-react";
import { toast } from "sonner";
import type { QueryClient } from "@tanstack/react-query";
import { useTicketSteps } from "@/lib/ticket-api";
import type { ProvisionStep, ProvisionStepsResp, ProvStepStatus, RequestStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

/** 소요 시간 — 1분 미만은 0.1초 단위(59.9초), 그 위는 분 · 초(1분 15초, 2분 01초) */
export function fmtSec(ms: number) {
  const tenths = Math.round(Math.max(0, ms) / 100);
  if (tenths < 600) return `${(tenths / 10).toFixed(1)}초`;
  const sec = Math.floor(tenths / 10);
  return `${Math.floor(sec / 60)}분 ${String(sec % 60).padStart(2, "0")}초`;
}

/** 보이는 단계의 소요 시간 — 끝났으면 기록된 시각끼리, 진행 중이면 화면 시계(t)까지. */
export function stepDurationMs(s: ProvisionStep, t: number) {
  if (!s.startedAt || s.status === "waiting") return undefined;
  const end = s.status === "running" ? t : s.endedAt ? Date.parse(s.endedAt) : NaN;
  return Number.isFinite(end) ? end - Date.parse(s.startedAt) : undefined;
}

// 진행 중엔 서버 시각보다 1.5초 늦게 그린다 — 조회(1초)가 먼저 와 있어, 끝난 단계를 모른 채 시간을 더 세다 되돌아가지 않게
const LIVE_DELAY_MS = 1500;

export type StepsData = ProvisionStepsResp & { receivedAt: number };

/** 화면 시계 t 에서 보이는 상태 — 기록된 시작·종료 시각으로 정한다(Infinity 면 기록 그대로). */
export function shownStatus(x: { status: ProvStepStatus; startedAt?: string; endedAt?: string }, t: number): ProvStepStatus {
  if (t === Infinity || x.status === "waiting" || x.status === "skipped" || !x.startedAt) return x.status;
  if (Date.parse(x.startedAt) > t) return "waiting";
  return x.endedAt && Date.parse(x.endedAt) <= t ? x.status : "running";
}

/** 진행 창의 화면 시계(서버 시각, ms) — 진행 중에만 서버 시각보다 LIVE_DELAY_MS 늦게 따라간다.
 *  서버가 확인해 준 시각(응답의 now)을 넘지 않고(응답이 늦으면 잠깐 멈춤), 뒤로 가지 않는다.
 *  끝난 신청을 열었거나 끝난 뒤 다 따라잡으면 Infinity — 기록된 값을 그대로 보여 준다. */
export function useShownClock(data: StepsData | undefined) {
  const [clock, setClock] = React.useState<{ attempt?: number; t: number }>({ t: Infinity });
  const live = data?.status === "provisioning";
  const following = live || Number.isFinite(clock.t);
  React.useEffect(() => {
    if (!data || !following) return;
    const horizon = Date.parse(data.now);
    const times = data.items.flatMap((s) => [s, ...s.details]).flatMap((x) => [x.startedAt, x.endedAt]);
    const lastEvent = Math.max(-Infinity, ...times.filter((v): v is string => !!v).map(Date.parse));
    const tick = () => setClock((c) => {
      const target = Math.min(horizon + (performance.now() - data.receivedAt) - LIVE_DELAY_MS, horizon);
      const fresh = c.attempt !== data.attempt || !Number.isFinite(c.t);  // 새로 진행 중 — 지금부터 따라간다
      const t = fresh ? target : Math.max(c.t, target);
      return { attempt: data.attempt, t: !live && t >= lastEvent ? Infinity : t };  // 끝났고 다 따라잡음
    });
    const id = window.setInterval(tick, 100);
    const first = window.requestAnimationFrame(tick);
    return () => {
      window.clearInterval(id);
      window.cancelAnimationFrame(first);
    };
  }, [data, following, live]);
  // 진행 중인데 아직 따라가기 전(첫 응답 직후 한 번) — 응답 기준 시각으로 그려 앞선 상태가 비치지 않게
  if (live && (clock.attempt !== data.attempt || !Number.isFinite(clock.t))) return Date.parse(data.now) - LIVE_DELAY_MS;
  return clock.t;
}

/** 시작·재시도 알림 — 시작 직후 받아 둔 진행 기록으로: 시작 대기(예약)면 '프로비저닝을 예약했습니다'(시작한 것으로
 *  오해하지 않게), 바로 시작했으면 started. 설명은 신청 ID 와 몇 번째 시도인지(2회차부터). */
export function toastStarted(qc: QueryClient, id: string, started: string) {
  const s = qc.getQueryData<ProvisionStepsResp>(["tickets", "steps", id]);
  const attempt = s?.attempt ?? 1;
  toast.success(s?.status === "provisioning" && s.queue ? "프로비저닝을 예약했습니다" : started, {
    description: `신청 ID: ${id}${attempt > 1 ? ` · ${attempt}차 시도` : ""}`,
  });
}

export function fmtWait(sec: number) {
  const m = Math.floor(sec / 60);
  return m ? `${m}분 ${String(sec % 60).padStart(2, "0")}초` : `${sec}초`;
}

/** 진행 기록 + 화면 시계로 보이는 단계 · 상태 · 진행률. fallbackStatus — 첫 조회 전(신청에 저장된 상태), enabled — 닫힌 창이면 false. */
export function useProvisionProgress(id: string | undefined, fallbackStatus?: RequestStatus, enabled = true) {
  const { data } = useTicketSteps(id, enabled);
  const t = useShownClock(data);
  // 화면 시계 기준으로 보이는 단계 — 아직 오지 않은 시각의 값·오류는 감춘다
  const steps = (data?.items ?? []).map((s) => {
    const st = shownStatus(s, t);
    return {
      ...s,
      status: st,
      error: st === "failed" ? s.error : undefined,
      details: s.details.map((d) => {
        const ds = shownStatus(d, t);
        return { ...d, status: ds, value: ds === d.status ? d.value : undefined };
      }),
    };
  });
  const serverStatus = data?.status ?? fallbackStatus;
  // 화면 시계가 따라가는 동안은 끝났어도 '생성 중' — 마지막 단계가 보이게 끝난 뒤 완료·실패로
  const status = Number.isFinite(t) ? "provisioning" : serverStatus;
  // 진행률: 단계 수 · 퍼센트 모두 단계 기준 — 끝난 · 건너뛴 단계 + 진행 중(실패) 단계는 세부 단계 완료 비율만큼
  const finished = steps.filter((s) => s.status === "done" || s.status === "skipped").length;
  const cur = steps.find((s) => s.status === "running" || s.status === "failed");
  const curPart = cur?.details.length ? cur.details.filter((d) => d.status === "done").length / cur.details.length : 0;
  const pct = steps.length ? Math.round(((finished + curPart) / steps.length) * 100) : 0;
  const totalMs = status === "completed" && steps.length
    ? Math.max(...steps.map((s) => (s.endedAt ? Date.parse(s.endedAt) : 0))) - Math.min(...steps.filter((s) => s.startedAt).map((s) => Date.parse(s.startedAt!)))
    : undefined;
  const startedAt = steps.find((s) => s.startedAt)?.startedAt;
  return {
    data, t, steps, status, serverStatus, attempt: data?.attempt ?? 1, finished, pct, totalMs, startedAt,
    queue: data?.status === "provisioning" ? data.queue ?? null : null,
    current: steps.find((s) => s.status === "running") ?? steps.find((s) => s.status === "failed"),
  };
}

export type ProvisionProgress = ReturnType<typeof useProvisionProgress>;

function Circle({ status, no }: { status: ProvStepStatus; no: number }) {
  const base = "flex size-7 flex-none items-center justify-center rounded-full font-mono text-[11px] leading-none font-semibold";
  if (status === "done") return <span className={cn(base, "bg-brand-mint-500 text-white")}><Check className="size-3.5" strokeWidth={3} /></span>;
  if (status === "running") return <span className="size-7 flex-none animate-spin rounded-full border-[3px] border-brand-indigo-100 border-t-primary" />;
  if (status === "failed") return <span className={cn(base, "bg-red-600 text-white")}><X className="size-3.5" strokeWidth={3} /></span>;
  if (status === "skipped") return <span className={cn(base, "border-[1.5px] border-dashed border-slate-300! bg-card text-slate-400")}>–</span>;
  return <span className={cn(base, "border-[1.5px] border-slate-300! bg-card text-slate-400")}>{String(no).padStart(2, "0")}</span>;
}

function SubMark({ status }: { status: ProvStepStatus }) {
  if (status === "done") return <Check className="size-3.5 text-brand-mint-600" strokeWidth={3} />;
  if (status === "running") return <span className="size-3.5 animate-spin rounded-full border-2 border-brand-indigo-100 border-t-primary" />;
  if (status === "failed") return <span className="size-3 rounded-full bg-red-600" />;
  return <span className="mx-[5px] size-1 rounded-full bg-muted-foreground/40" />;
}

/** 진행 타임라인(시안 진행 화면) — 진행률 막대(실패면 빨강) + 큰 단계 세로 목록. 진행 중엔 진행·실패 단계만 펼치고,
 *  모두 끝나면 닫는다(누른 값은 같은 상태 동안만 유지). 건너뛴 단계는 이유를 보인다. */
export function ProgressTimeline({ p }: { p: ProvisionProgress }) {
  const { steps, status, t } = p;
  const done = status === "completed";
  const failed = status === "failed";
  const [toggled, setToggled] = React.useState<{ status?: RequestStatus; map: Record<number, boolean> }>({ map: {} });
  const isOpen = (s: ProvisionStep) =>
    (toggled.status === status ? toggled.map[s.seq] : undefined) ?? (!done && (s.status === "running" || s.status === "failed"));
  return (
    <div className="flex flex-none flex-col">
      <div className="mb-1.5 flex justify-between text-xs font-bold">
        <span>{p.finished} / {steps.length} 단계</span>
        <span className={cn("font-mono", failed ? "text-red-600" : "text-primary")}>{p.pct}%</span>
      </div>
      <div className="mb-4 h-1.5 overflow-hidden rounded-full bg-secondary">
        <div className={cn("h-full rounded-full transition-all duration-500", failed ? "bg-red-600" : done ? "bg-brand-mint-500" : "bg-primary")} style={{ width: `${p.pct}%` }} />
      </div>
      {steps.map((s, i) => {
        const expanded = isOpen(s);
        const dur = stepDurationMs(s, t);
        const muted = s.status === "waiting" || s.status === "skipped";
        return (
          <div key={s.seq} className="flex gap-3">
            <div className="flex flex-none flex-col items-center">
              <Circle status={s.status} no={i + 1} />
              {i < steps.length - 1 && <span className={cn("my-1 min-h-5 w-0.5 flex-1 rounded-sm", s.status === "done" ? "bg-brand-mint-500" : "bg-border")} />}
            </div>
            <div className="min-w-0 flex-1 pb-3">
              <button type="button" onClick={() => setToggled((x) => ({ status, map: { ...(x.status === status ? x.map : {}), [s.seq]: !expanded } }))}
                className="flex min-h-7 w-full cursor-pointer items-center gap-2 text-left">
                <span className={cn("text-sm font-bold", muted && "text-muted-foreground", s.status === "failed" && "text-red-600")}>{s.message}</span>
                <span className="font-mono text-[10px] tracking-[.04em] text-slate-400">{s.code}</span>
                <span className={cn("ml-auto flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground tabular-nums", s.status === "failed" && "text-red-600")}>
                  {/* 1초 안에 끝난 단계 — 상태 확인(1초) 사이에 끝나 0.0s 인 것도, 0.5s 처럼 기록된 것도 */}
                  {s.status === "skipped" ? "건너뜀" : s.status === "failed" ? "실패"
                    : s.status === "waiting" ? "대기" : dur !== undefined && (s.status !== "running" && dur < 1000 ? "1초 미만" : fmtSec(dur))}
                  <ChevronDown className={cn("size-3.5 transition-transform", expanded && "rotate-180")} />
                </span>
              </button>
              {expanded && (
                <div className={cn("mt-1.5 flex flex-col gap-1.5 rounded-[10px] border px-3 py-2.5",
                  s.status === "failed" ? "border-rose-200! bg-rose-50/40" : "border-border bg-secondary/30")}>
                  {s.status === "skipped" && <p className="text-xs text-muted-foreground">가속기가 없는 스펙이라 이 단계를 건너뜁니다.</p>}
                  {s.details.map((d) => (
                    <div key={d.code} className="flex items-center gap-2 text-xs">
                      <span className="flex size-4 flex-none items-center justify-center"><SubMark status={d.status} /></span>
                      <span className={cn("flex-1", d.status === "waiting" || d.status === "skipped" ? "text-muted-foreground" : "text-foreground",
                        d.status === "failed" && "text-red-600", d.status === "running" && "font-semibold text-primary")}>{d.label}</span>
                      <span className="font-mono text-muted-foreground">{d.status === "waiting" ? "—" : d.value}</span>
                    </div>
                  ))}
                  {s.error && <div className="mt-1 rounded-md border border-rose-200 bg-card px-2.5 py-1.5 font-mono text-xs text-red-700">{s.error}</div>}
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
