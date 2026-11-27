"use client";
/**
 * 자원 신청 스테퍼 공용 틀(시안 Stepper D) — 신청 작성과 신청 상세가 같은 모달로 본다.
 *  3열(왼쪽 스테퍼 248px / 본문 / 오른쪽 요약 300px ↔ 접힘 44px) × 2줄(머리 칸 / 몸통) — 모달 전체 머리 줄 없이 열마다 머리 칸.
 *  왼쪽 머리 = 유형 배지 · 제목 · 상태 배지, 본문 머리 = STEP 0N / 04 · 제목 + 안내 + 오른쪽 조작, 요약 머리 = 제목 · 요약 접기 · 닫기
 *  (요약 열이 없으면 닫기는 본문 머리 끝). 세 머리 칸은 같은 높이(본문 열은 subgrid).
 *  본문 몸통 = 스크롤 영역 + 바닥 줄 위에 떠 있는 진행 알림 + 바닥 줄.
 * 크기 1180 × min(736px, 100vh − 32px) — 화면이 낮으면 본문만 스크롤한다(페이지는 스크롤하지 않음).
 * 요약 접힘은 보는 사람마다 브라우저에 기억한다.
 */
import * as React from "react";
import { AlertCircle, ArrowDown, ArrowUp, Check, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/** DialogContent 공통 — hideClose 와 함께 쓴다(닫기는 틀의 ✕ — 작성 중 확인 창을 거친다). */
export const STEPPER_CONTENT =
  "flex h-[min(736px,calc(100vh-32px))] w-[1180px] max-w-[calc(100vw-32px)] flex-col gap-0 overflow-hidden rounded-xl p-0 shadow-[0_24px_60px_-16px_rgba(15,23,42,.35)] outline-none";

// ── 머리 ──────────────────────────────────────────────────────────

interface HeadProps {
  kind: string;               // VM · K8s · NEW
  title: string;
  badge?: React.ReactNode;    // 상태 배지
  extra?: React.ReactNode;    // 배지 옆(N차 시도 · 내 신청 등)
  right?: React.ReactNode;    // 제목 아래 줄(유형 · 유형 바꾸기)
  onClose: () => void;
}
const HeadCtx = React.createContext<HeadProps | null>(null);
// 요약 열이 없으면 StepperGrid 가 닫기를 본문 머리 끝에 그리게 넘긴다
const MainCloseCtx = React.createContext<(() => void) | null>(null);

/** 모달 머리 정보 — 감싼 StepperGrid 가 왼쪽 머리 칸(유형 · 제목 · 상태)과 닫기 버튼 자리(요약 머리 · 본문 머리)에 그린다. */
export function StepperHeader({ children, ...head }: HeadProps & { children: React.ReactNode }) {
  return <HeadCtx.Provider value={head}>{children}</HeadCtx.Provider>;
}

function CloseButton({ onClose }: { onClose: () => void }) {
  return (
    <button type="button" onClick={onClose} title="닫기" aria-label="닫기"
      className="flex size-[30px] flex-none cursor-pointer items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-secondary">
      <X className="size-[18px]" />
    </button>
  );
}

/** 상태 배지(머리) — 신청 목록과 같은 색. */
export function StatusPill({ tone, children }: { tone: "wait" | "appr" | "run" | "done" | "fail" | "grey"; children: React.ReactNode }) {
  const cls = {
    wait: "bg-amber-100 text-amber-800", appr: "bg-sky-100 text-sky-800", run: "bg-brand-indigo-50 text-primary",
    done: "bg-brand-mint-50 text-brand-mint-700", fail: "bg-rose-100 text-rose-700", grey: "bg-secondary text-slate-700",
  }[tone];
  return <span className={cn("whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-semibold", cls)}>{children}</span>;
}

// ── 3열 ──────────────────────────────────────────────────────────

const SUMMARY_KEY = "okc.stepper.summary"; // "closed" 면 접힘

// 모달 내용은 열 때(브라우저에서)만 그려지므로 처음 값을 저장소에서 바로 읽는다
function useSummaryOpen(): [boolean, (open: boolean) => void] {
  const [open, setOpen] = React.useState(() => {
    try { return typeof window === "undefined" || localStorage.getItem(SUMMARY_KEY) !== "closed"; } catch { return true; } // 저장소를 못 쓰면 펼친 채
  });
  const set = (o: boolean) => {
    setOpen(o);
    try { localStorage.setItem(SUMMARY_KEY, o ? "open" : "closed"); } catch { /* 기억만 못 함 */ }
  };
  return [open, set];
}

/**
 * 왼쪽 스테퍼 · 본문 · 오른쪽 요약(없으면 2열) × 머리 칸 / 몸통. 요약을 접으면 열 폭이 바뀌어 본문이 넓어진다(겹쳐 덮지 않음).
 * 본문 열은 두 줄에 걸친 subgrid — StepMain 의 머리가 첫 줄, 몸통이 둘째 줄에 들어가 세 머리 칸 높이가 같다.
 */
export function StepperGrid({ left, summary, summaryTitle = "입력 정보 요약", children }: {
  left: React.ReactNode; summary?: React.ReactNode; summaryTitle?: string; children: React.ReactNode;
}) {
  const head = React.useContext(HeadCtx);
  const [open, setOpen] = useSummaryOpen();
  const cols = !summary ? "grid-cols-[248px_minmax(0,1fr)]" : open ? "grid-cols-[248px_minmax(0,1fr)_300px]" : "grid-cols-[248px_minmax(0,1fr)_44px]";
  return (
    <div className={cn("grid min-h-0 flex-1 grid-rows-[minmax(60px,auto)_minmax(0,1fr)]", cols)}>
      <div className="col-start-1 row-start-1 flex min-w-0 flex-wrap content-center items-center gap-x-2.5 gap-y-1.5 border-r border-b border-border px-5 py-3">
        {head && (
          <>
            <span className="rounded-[5px] bg-brand-indigo-50 px-1.5 py-1 font-mono text-[11px] leading-none font-semibold text-primary">{head.kind}</span>
            <DialogTitle className="text-base font-bold tracking-normal">{head.title}</DialogTitle>
            {head.badge}
            {head.extra}
            {head.right && <span className="flex basis-full items-center gap-3 text-xs">{head.right}</span>}
          </>
        )}
      </div>
      <div className="col-start-1 row-start-2 grid min-h-0 grid-rows-[minmax(0,1fr)]">{left}</div>
      <MainCloseCtx.Provider value={summary ? null : head?.onClose ?? null}>
        <div className="col-start-2 row-span-2 row-start-1 grid min-h-0 min-w-0 grid-rows-subgrid">{children}</div>
      </MainCloseCtx.Provider>
      {summary && (open ? (
        <>
          <div className="col-start-3 row-start-1 border-l border-border px-4">
            <div className="flex h-full items-center justify-between gap-2 border-b border-border">
              <span className="text-sm font-bold">{summaryTitle}</span>
              <span className="flex items-center gap-1">
                <button type="button" onClick={() => setOpen(false)}
                  className="cursor-pointer rounded-md px-1.5 py-0.5 text-xs font-semibold text-brand-indigo-400 hover:bg-brand-indigo-50">요약 접기 ▸</button>
                {head && <CloseButton onClose={head.onClose} />}
              </span>
            </div>
          </div>
          <div className="col-start-3 row-start-2 min-h-0 overflow-y-auto border-l border-border px-4 pb-4 [scrollbar-width:thin]">{summary}</div>
        </>
      ) : (
        <>
          <div className="col-start-3 row-start-1 flex items-center justify-center border-b border-l border-border">
            {head && <CloseButton onClose={head.onClose} />}
          </div>
          <button type="button" onClick={() => setOpen(true)} title="요약 펼치기"
            className="col-start-3 row-start-2 flex cursor-pointer flex-col items-center gap-1 border-l border-border pt-4 text-xs font-semibold text-brand-indigo-400 [writing-mode:vertical-rl] hover:bg-brand-indigo-50">
            ◂ 요약 펼치기
          </button>
        </>
      ))}
    </div>
  );
}

// ── 왼쪽 스테퍼 ──────────────────────────────────────────────────

export type StepState = "done" | "cur" | "todo" | "run" | "fail" | "skip";
export interface StepItem {
  no: string;                // "01" · 시작 대기처럼 번호가 없으면 label
  label?: string;            // 기본 "STEP {no}"
  name: string;
  desc?: React.ReactNode;    // 입력 요약 · 처리자(조직) + 날짜 — 줄바꿈 그대로
  state: StepState;
  descTone?: "red" | "blue";
  onClick?: () => void;      // 끝난 단계로 돌아가기 · 단계 내용 보기
  selected?: boolean;        // 지금 본문이 보여 주는 단계(신청 상세 — 다른 단계를 눌러 볼 때)
  missing?: { label: string; onClick: () => void }[]; // 빨간 '선택 필요 - 항목'(누르면 그 카드로)
}

function StepCircle({ s }: { s: StepItem }) {
  const base = "flex size-7 flex-none items-center justify-center rounded-full font-mono text-[11px] leading-none font-semibold";
  if (s.state === "done") return <span className={cn(base, "bg-brand-mint-500 text-white")}><Check className="size-3.5" strokeWidth={3} /></span>;
  if (s.state === "cur") return <span className={cn(base, "bg-primary text-white shadow-[0_0_0_4px_#e0e7ff]")}>{s.no}</span>;
  if (s.state === "run") return <span className="size-7 flex-none animate-spin rounded-full border-[3px] border-brand-indigo-100 border-t-primary" />;
  if (s.state === "fail") return <span className={cn(base, "bg-red-600 text-white")}><X className="size-3.5" strokeWidth={3} /></span>;
  if (s.state === "skip") return <span className={cn(base, "border-[1.5px] border-dashed border-slate-300! bg-card text-slate-400")}>–</span>;
  return <span className={cn(base, "border-[1.5px] border-slate-300! bg-card text-slate-400")}>{s.no}</span>;
}

export function LeftStepper({ steps, note }: { steps: StepItem[]; note?: React.ReactNode }) {
  return (
    <div className="flex min-h-0 flex-col gap-3.5 overflow-y-auto border-r border-border p-5 [scrollbar-width:thin]">
      <div className="flex flex-col">
        {steps.map((s, i) => {
          const active = s.state === "cur" || s.state === "run";
          return (
            <div key={s.no + s.name} onClick={s.onClick}
              className={cn("-mx-1.5 flex gap-3 rounded-lg px-1.5", (s.selected ?? active) && "bg-[#f5f7ff]",
                s.selected && "shadow-[inset_0_0_0_1px_#c7d2fe]", s.onClick && "cursor-pointer hover:bg-secondary/60")}>
              <div className="flex flex-none flex-col items-center pt-1.5">
                <StepCircle s={s} />
                {i < steps.length - 1 && <span className={cn("my-1 min-h-[22px] w-0.5 flex-1 rounded-sm", s.state === "done" ? "bg-brand-mint-500" : "bg-border")} />}
              </div>
              <div className="min-w-0 flex-1 pt-1.5 pb-[18px]">
                <div className={cn("font-mono text-[10px] leading-tight font-semibold tracking-[.08em]",
                  s.state === "fail" ? "text-red-600" : active ? "text-primary" : "text-slate-400")}>{s.label ?? `STEP ${s.no}`}</div>
                <div className={cn("mt-[3px] text-sm font-bold", s.state === "fail" ? "text-red-600" : s.state === "todo" || s.state === "skip" ? "text-muted-foreground" : "text-foreground")}>
                  {s.name}
                </div>
                {s.desc && (
                  <div className={cn("mt-px text-xs whitespace-pre-line [overflow-wrap:anywhere]",
                    s.descTone === "red" ? "text-[#d92d20]" : s.descTone === "blue" ? "text-primary" : "text-muted-foreground")}>{s.desc}</div>
                )}
                {s.missing && s.missing.length > 0 && (
                  <div className="mt-1 flex flex-col items-start gap-0.5 text-xs text-[#d92d20]">
                    <span>선택 필요</span>
                    {s.missing.map((m) => (
                      <button key={m.label} type="button" title={`${m.label} 위치로 이동`}
                        onClick={(e) => { e.stopPropagation(); m.onClick(); }}
                        className="cursor-pointer rounded px-0.5 font-semibold underline underline-offset-2 hover:bg-rose-100">- {m.label}</button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
      {note && <div className="mt-auto border-t border-border pt-3 text-[11px] text-muted-foreground">{note}</div>}
    </div>
  );
}

// ── 본문 ──────────────────────────────────────────────────────────

// 본문 스크롤 영역 — 떠 있는 진행 알림이 카드 위치(↑ · ↓)를 재고 스크롤을 따라간다
const ScrollCtx = React.createContext<HTMLElement | null>(null);

function assignRef<T>(ref: React.Ref<T> | undefined, el: T | null) {
  if (typeof ref === "function") ref(el);
  else if (ref) (ref as React.RefObject<T | null>).current = el;
}

/**
 * 본문 — 머리 칸(STEP 0N / 04 · 제목, 안내, 오른쪽 extra · 요약 열이 없으면 닫기) + 몸통(스크롤 영역 children · 떠 있는 진행 알림 progress · 바닥 footer).
 * StepperGrid 본문 열(subgrid)의 두 줄에 머리 · 몸통이 하나씩 들어간다.
 */
export function StepMain({ step, title, desc, extra, progress, footer, scrollRef, children }: {
  step?: [string, string];   // ["02", "04"]
  title: React.ReactNode;
  desc?: React.ReactNode;
  extra?: React.ReactNode;   // 머리 오른쪽(신청 값 그대로 스위치 등)
  progress?: React.ReactNode; // FloatProgress
  footer: React.ReactNode;
  scrollRef?: React.Ref<HTMLDivElement>;
  children: React.ReactNode;
}) {
  const close = React.useContext(MainCloseCtx);
  const [box, setBox] = React.useState<HTMLDivElement | null>(null);
  const setScroll = React.useCallback((el: HTMLDivElement | null) => { setBox(el); assignRef(scrollRef, el); }, [scrollRef]);
  // 단계가 바뀌면 맨 위부터 — 단계마다 같은 자리의 StepMain 이라 스크롤 영역(위치)이 그대로 남는다
  const at = step?.[0];
  React.useLayoutEffect(() => { box?.scrollTo({ top: 0 }); }, [box, at]);
  return (
    <>
      <div className="flex min-w-0 items-center gap-4 border-b border-border px-7 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2.5">
            {step && <span className="flex-none font-mono text-[11px] font-semibold tracking-[.08em] text-primary">STEP {step[0]} <span className="text-slate-400">/ {step[1]}</span></span>}
            <span className="min-w-0 text-[17px] font-extrabold">{title}</span>
          </div>
          {desc && <DialogDescription className="mt-0.5 text-[13px] text-muted-foreground">{desc}</DialogDescription>}
        </div>
        {extra && <div className="flex flex-none items-center gap-3.5 text-xs">{extra}</div>}
        {close && <CloseButton onClose={close} />}
      </div>
      <div className="flex min-h-0 min-w-0 flex-col">
        <div className="relative flex min-h-0 flex-1 flex-col">
          <div ref={setScroll} className={cn("relative flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto px-7 py-[18px] [scrollbar-width:thin]", progress && "pb-[72px]")}>
            {children}
          </div>
          {progress && <ScrollCtx.Provider value={box}>{progress}</ScrollCtx.Provider>}
        </div>
        {footer}
      </div>
    </>
  );
}

/** 진행 항목 — value 는 알림 칩의 툴팁(옵션 항목의 지금 값). */
export interface ProgressItem { key: string; label: string; value?: string; ok: boolean; optional?: boolean }

/** 진행 항목 → 왼쪽 스테퍼의 지금 단계에 덮어쓸 값(필수 n/m 완료 · 선택 필요 - 항목, 누르면 그 카드로) — 신청 작성 화면과 같은 모양. */
export function progressStep(items: ProgressItem[], onJump: (key: string) => void): Partial<StepItem> {
  const req = items.filter((x) => !x.optional);
  const done = req.filter((x) => x.ok).length;
  return {
    desc: `필수 ${done}/${req.length} 완료`, descTone: done < req.length ? "red" : undefined,
    // 문제가 있는 옵션(OpenStack 에 없는 키페어)도 프로비저닝을 막으므로 함께 보인다
    missing: items.filter((x) => !x.ok).map((x) => ({ label: x.label, onClick: () => onJump(x.key) })),
  };
}

/**
 * 바닥 줄 위에 떠 있는 진행 알림 — 필수 n/m · 아직 안 된 필수 항목(빨간 칩) | 선택 사항(점선 칩, 문제가 있으면 빨강).
 * 칩을 누르면 그 카드로 스크롤하고, 화살표는 카드가 알림보다 위(↑)인지 아래(↓)인지 — 카드는 useCardScroll 의 data-card 로 찾는다.
 */
export function FloatProgress({ items, onJump }: { items: ProgressItem[]; onJump: (key: string) => void }) {
  const box = React.useContext(ScrollCtx);
  const [sides, setSides] = React.useState<Record<string, "up" | "down">>({});
  const measure = React.useCallback(() => {
    if (!box) return;
    const line = box.getBoundingClientRect().bottom - 56; // 알림이 떠 있는 높이
    const next: Record<string, "up" | "down"> = {};
    box.querySelectorAll<HTMLElement>("[data-card]").forEach((el) => {
      next[el.dataset.card!] = el.getBoundingClientRect().top < line ? "up" : "down";
    });
    setSides((cur) => (Object.keys(next).length === Object.keys(cur).length && Object.entries(next).every(([k, v]) => cur[k] === v) ? cur : next));
  }, [box]);
  // 스크롤 · 크기 변화 · 내용 변화(카드가 늘거나 펼쳐져 위치가 바뀜) 때마다 다음 프레임에 잰다
  React.useEffect(() => {
    if (!box) return;
    let raf = 0;
    const on = () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(measure); };
    on();
    box.addEventListener("scroll", on, { passive: true });
    const ro = new ResizeObserver(on);
    ro.observe(box);
    const mo = new MutationObserver(on);
    mo.observe(box, { childList: true, subtree: true });
    return () => { box.removeEventListener("scroll", on); ro.disconnect(); mo.disconnect(); cancelAnimationFrame(raf); };
  }, [box, measure]);

  const req = items.filter((x) => !x.optional);
  const todo = req.filter((x) => !x.ok);
  const opts = items.filter((x) => x.optional);
  const arrow = (key: string) => sides[key] === "up" ? <ArrowUp className="size-3" strokeWidth={2.5} />
    : sides[key] === "down" ? <ArrowDown className="size-3" strokeWidth={2.5} /> : null;
  const chip = "flex cursor-pointer items-center gap-0.5 rounded-full border px-2.5 py-[3px] font-semibold whitespace-nowrap";
  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center px-7">
      <div className="pointer-events-auto flex max-w-full flex-wrap items-center justify-center gap-1.5 rounded-full border border-[#dfe3f5]! bg-card px-3 py-1.5 text-xs shadow-[0_8px_24px_-6px_rgba(15,23,42,.28)]">
        {todo.length ? <AlertCircle className="size-4 flex-none fill-[#d92d20] text-white" /> : <CheckCircle2 className="size-4 flex-none fill-brand-mint-500 text-white" />}
        <span className="mr-0.5 font-bold whitespace-nowrap">필수 <span className="font-mono">{req.length - todo.length}/{req.length}</span></span>
        {todo.map((x) => (
          <button key={x.key} type="button" onClick={() => onJump(x.key)} title={`${x.label} 위치로 이동`}
            className={cn(chip, "border-[#f1aca5]! bg-[#fef2f2] text-[#d92d20] hover:bg-rose-100")}>
            {x.label}{arrow(x.key)}
          </button>
        ))}
        {opts.length > 0 && (
          <>
            <span className="mx-1 h-4 w-px bg-border" />
            <span className="whitespace-nowrap text-muted-foreground">선택 사항</span>
            {opts.map((x) => (
              <button key={x.key} type="button" onClick={() => onJump(x.key)} title={x.value ? `${x.label}: ${x.value}` : `${x.label} 위치로 이동`}
                className={cn(chip, "border-dashed hover:bg-secondary", x.ok ? "border-slate-300! text-foreground" : "border-[#f1aca5]! text-[#d92d20]")}>
                {x.label}{arrow(x.key)}
              </button>
            ))}
          </>
        )}
      </div>
    </div>
  );
}

/** 바닥 줄 — 왼쪽(또는 버튼 바로 옆) 상태 문구, 오른쪽 버튼. tone: 상태 문구 색. */
export function StepFooter({ left, status, tone = "muted", children }: {
  left?: React.ReactNode; status?: React.ReactNode; tone?: "muted" | "ok" | "bad"; children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-[60px] flex-none items-center gap-2 border-t border-border px-5 py-2.5">
      {left}
      <span className="flex-1" />
      {status && (
        <span className={cn("flex min-w-0 items-center gap-1.5 text-xs", tone === "ok" ? "text-brand-mint-700" : tone === "bad" ? "text-[#d92d20]" : "text-muted-foreground")}>
          <Info className="size-3.5 flex-none" /><span className="line-clamp-2">{status}</span>
        </span>
      )}
      {children}
    </div>
  );
}

/** 반려 바닥 줄 — '반려 사유 (필수)' 입력(여러 줄, Enter 는 줄바꿈) + 취소 · 반려 확정(사유가 없으면 비활성). 바닥 줄 자리에 바꿔 그린다. */
export function RejectBar({ onCancel, onConfirm, busy }: { onCancel: () => void; onConfirm: (reason: string) => void; busy?: boolean }) {
  const [reason, setReason] = React.useState("");
  return (
    <div className="flex flex-none items-end gap-2 border-t border-rose-200 bg-[#fef2f2] px-5 py-3">
      <label className="flex min-w-0 flex-1 flex-col gap-1.5">
        <span className="text-xs font-bold text-[#b42318]">반려 사유 (필수)</span>
        <textarea autoFocus rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="예: 프로젝트 쿼터 초과, 가속기 미할당 등"
          className="w-full resize-none rounded-md border border-[#f1aca5]! bg-card px-3 py-2 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring" />
      </label>
      <Button variant="outline" onClick={onCancel}>취소</Button>
      <Button variant="destructive" onClick={() => onConfirm(reason.trim())} disabled={busy || !reason.trim()}>
        <XCircle className="size-3.5" /> 반려 확정
      </Button>
    </div>
  );
}

// ── 오른쪽 요약 ──────────────────────────────────────────────────

/** 요약 묶음 — 제목(+ STEP 0N › 바로가기) + 행(값이 없으면 '—'). */
export function SumSection({ title, step, onStep, rows, children }: {
  title: React.ReactNode; step?: string; onStep?: () => void; rows?: [string, React.ReactNode][]; children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-[3px] border-b border-border py-3 last:border-b-0">
      <div className="mb-0.5 flex items-center justify-between">
        <span className="text-xs font-bold">{title}</span>
        {step && onStep && (
          <button type="button" onClick={onStep} className="cursor-pointer font-mono text-[10px] font-semibold tracking-[.06em] text-brand-indigo-400 hover:underline">STEP {step} ›</button>
        )}
      </div>
      {rows?.map(([k, v]) => (
        <div key={k} className="flex items-start justify-between gap-3 text-xs">
          <span className="flex-none text-muted-foreground">{k}</span>
          <span className="min-w-0 text-right [overflow-wrap:anywhere]">{v ?? <span className="text-slate-400">—</span>}</span>
        </div>
      ))}
      {children}
    </div>
  );
}

// ── 입력 카드(구성 단계 · 프로비저닝 설정) ─────────────────────────

// 목적격 조사 — 마지막 글자가 받침 있는 한글이면 '을', 아니면 '를'
const eul = (word: string) => { const c = word.charCodeAt(word.length - 1) - 0xac00; return c >= 0 && c < 11172 && c % 28 ? "을" : "를"; };

/**
 * 입력 카드 — 머리(아이콘 · 제목 · 필수/옵션 · 태그 · 오른쪽 값 · 상태 · 오른쪽 조작 + 아래 줄) + 내용. missing 이면 테두리와 머리만 빨갛게(내용은 흰 바탕).
 * missing 인데 값이 없으면 값 자리에 빨간 안내 ask(기본 '{제목}을/를 고르세요').
 */
export const FormCard = React.forwardRef<HTMLDivElement, {
  icon?: React.ReactNode; title: string; required?: boolean; optional?: boolean; tag?: React.ReactNode; range?: string;
  value?: React.ReactNode; valueMuted?: boolean; status?: "done" | "missing"; action?: React.ReactNode; ask?: string;
  line?: React.ReactNode; children?: React.ReactNode; className?: string;
}>(function FormCard({ icon, title, required, optional, tag, range, value, valueMuted, status, action, ask, line, children, className }, ref) {
  const missing = status === "missing";
  const body = React.Children.toArray(children).length > 0;
  return (
    <div ref={ref} className={cn("flex flex-none scroll-mt-3 flex-col overflow-hidden rounded-xl border-[1.5px] bg-card",
      missing ? "border-[#f1aca5]!" : "border-border", className)}>
      <div className={cn("flex flex-col gap-1 px-4 pt-4", body ? "pb-3" : "pb-4", missing && "border-b border-[#f6d0cb]! bg-[#fff5f4]")}>
        <div className="flex min-h-7 items-center gap-1.5">
          {icon}
          <span className={cn("flex-none text-[15px] font-bold", missing && "text-[#b42318]")}>
            {title}{required && <span className="ml-0.5 text-[#d92d20]">*</span>}
          </span>
          {optional && <span className="rounded bg-[#eceff3] px-[5px] py-px text-[10px] font-semibold text-[#5b6470]">옵션</span>}
          {tag}
          {range && <span className="text-[11px] whitespace-nowrap text-muted-foreground">{range}</span>}
          <span className="flex-1" />
          {value ? <span className={cn("min-w-0 truncate text-[13px]", valueMuted ? "text-muted-foreground" : "font-mono font-semibold")}>{value}</span>
            : missing && <span className="min-w-0 truncate text-[13px] font-semibold text-[#d92d20]">{ask ?? `${title}${eul(title)} 고르세요`}</span>}
          {status === "done" && <CheckCircle2 className="size-[18px] flex-none fill-brand-mint-500 text-white" />}
          {missing && <AlertCircle className="size-[18px] flex-none fill-[#d92d20] text-white" />}
          {action}
        </div>
        {line && <div className={cn("text-xs", missing ? "text-[#d92d20]" : "text-muted-foreground")}>{line}</div>}
      </div>
      {body && <div className={cn("flex flex-col gap-3 px-4 pb-4", missing && "pt-3")}>{children}</div>}
    </div>
  );
});

/** 카드 위치 기억 + 그 카드로 스크롤(본문 스크롤 영역 안에서). 카드에 data-card 를 달아 떠 있는 진행 알림이 위치를 잰다. */
export function useCardScroll<K extends string>() {
  const scrollRef = React.useRef<HTMLDivElement>(null);
  const cards = React.useRef<Partial<Record<K, HTMLElement | null>>>({});
  const cardRef = React.useCallback((key: K) => (el: HTMLElement | null) => {
    cards.current[key] = el;
    if (el) el.dataset.card = key;
  }, []);
  const scrollTo = React.useCallback((key: K) => {
    const box = scrollRef.current;
    const el = cards.current[key];
    if (!box || !el) return;
    const top = el.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop - 12;
    box.scrollTo({ top, behavior: "smooth" });
  }, []);
  return { scrollRef, cardRef, scrollTo };
}

// ── 작성 중 닫기 확인 ─────────────────────────────────────────────

/**
 * 작성 중 닫기 — 입력이 있으면(dirty) ✕ · 바깥 클릭 · Esc 가 확인 창을 띄우고, 없으면 바로 닫는다.
 * 입력이 없을 때 바깥 클릭 · Esc 는 onDismiss 가 있으면 그것을 한다(✕ 는 늘 닫기).
 * 확인 창에서는 Enter · [닫기]가 닫기(작성 취소)이고, Esc · 바깥 클릭 · [계속 작성]은 작성으로 돌아간다.
 * 돌려주는 contentProps 를 DialogContent 에, confirm 을 그 안에 그린다.
 */
export function useCloseGuard(dirty: boolean, onClose: () => void,
  { detail = "입력한 내용은 저장되지 않습니다.", onDismiss }: { detail?: string; onDismiss?: () => void } = {}) {
  const [asking, setAsking] = React.useState(false);
  const closeBtn = React.useRef<HTMLButtonElement>(null);
  const requestClose = () => (dirty ? setAsking(true) : onClose());
  const requestDismiss = () => (dirty ? setAsking(true) : (onDismiss ?? onClose)());
  const closeAll = () => { setAsking(false); onClose(); };
  const contentProps = {
    onEscapeKeyDown: (e: KeyboardEvent) => { e.preventDefault(); requestDismiss(); },
    onInteractOutside: (e: Event) => {
      if (e.defaultPrevented) return; // 토스트 클릭 — 닫지 않는다(DialogContent 가 먼저 막아 둠)
      e.preventDefault();
      requestDismiss();
    },
  };
  const confirm = (
    <Dialog open={asking} onOpenChange={(o) => !o && setAsking(false)}>
      <DialogContent hideClose className="max-w-[440px] gap-0 p-6" onOpenAutoFocus={(e) => {
        e.preventDefault();
        // Enter = 닫기. 바깥 클릭으로 열리면 같은 클릭의 mousedown 이 포커스를 body 로 옮기므로 그 뒤에 잡는다
        setTimeout(() => closeBtn.current?.focus());
      }}>
        <DialogTitle className="text-[17px] font-bold">작성 중인 신청을 닫을까요?</DialogTitle>
        <DialogDescription className="mt-1.5 text-[13px]">{detail}</DialogDescription>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" onClick={() => setAsking(false)}>계속 작성</Button>
          <Button ref={closeBtn} variant="destructive" onClick={closeAll}>닫기</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
  return { requestClose, contentProps, confirm };
}
