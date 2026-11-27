"use client";
/**
 * 자원 신청 상세의 공용 틀(AI 워크스페이스 상세) — 머리(DetailHeader) → 이름 카드(RequestHero, 오른쪽에 진행 결과) → 사유 띠
 *  (ReasonBanner) → 섹션 카드(Section·InfoRow) → 고정 바닥(DetailFooter). 본문만 스크롤하고 바닥은 늘 보인다.
 *  가상머신 · 서비스 클러스터는 스테퍼 상세(request-detail-stepper)가 사유 띠(ReasonBanner)만 함께 쓴다.
 * 폭: 읽기·승인 760px, 처리 화면 1000px.
 */
import * as React from "react";
import { BrainCircuit, Boxes, Building2, Copy, FolderKanban, Server, XCircle } from "lucide-react";
import { toast } from "sonner";
import { DialogDescription, DialogTitle } from "@/components/ui/dialog";
import type { ResourceRequest } from "@/lib/types";
import { cn, formatDateTime } from "@/lib/utils";

export const DETAIL_W = { read: "max-w-[760px]", process: "max-w-[1000px]" } as const;

/** DialogContent 공통 — 머리·본문·바닥 세로 배치(본문만 스크롤). 폭은 DETAIL_W 를 더한다. */
export const DETAIL_CONTENT = "flex max-h-[92vh] flex-col gap-0 overflow-hidden rounded-[14px] p-0 outline-none";
/** 본문 — 스크롤 영역(카드 사이 14px). */
export const DETAIL_BODY = "grid min-h-0 flex-1 content-start gap-[14px] overflow-y-auto px-7 pt-4 pb-6";

/** 머리 — 제목(신청 상세 + 상태 배지) + 설명. 설명이 없으면 DialogContent 에 aria-describedby={undefined} 를 준다. */
export function DetailHeader({ badge, description }: { badge: React.ReactNode; description?: React.ReactNode }) {
  return (
    <div className="flex-none px-7 pt-6 pr-12">
      <DialogTitle className="flex items-center gap-2 text-xl font-bold">신청 상세 {badge}</DialogTitle>
      {description && <DialogDescription className="mt-1.5 text-sm">{description}</DialogDescription>}
    </div>
  );
}

// 진행 기록 — 단계별 누가·언제(반려·실패 사유는 ReasonBanner). 마감(완료 · 실패)은 그 회차를 시작한 관리자 · 끝난 시각 —
// 완료가 아니면 실패(실패 뒤 반려한 신청 포함). AI 워크스페이스는 화면 통일 대상이 아니라 넣지 않는다
function processLines(r: ResourceRequest) {
  const finished = r.type !== "ai-workspace" && r.finishedAt;
  return [
    ["승인", r.approvedByName, r.approvedAt],
    ["프로비저닝", r.provisioningByName, r.provisioningAt],
    [r.status === "completed" ? "완료" : "실패", finished && r.finishedByName, r.finishedAt],
    ["반려 (승인 단계)", r.approvedRejectByName, r.approvedRejectAt],
    ["반려 (프로비저닝 단계)", r.provisioningRejectByName, r.provisioningRejectAt],
  ].filter(([, by]) => by) as [string, string, string | undefined][];
}

/** 신청 ID — 누르면 복사(신청 목록의 신청 ID 칸과 같음). */
function CopyId({ id }: { id: string }) {
  return (
    <button
      type="button"
      title="클릭해서 신청 ID 복사"
      onClick={() => { navigator.clipboard.writeText(id); toast.success("신청 ID 복사됨", { description: id }); }}
      className="inline-flex flex-none cursor-pointer items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-xs text-muted-foreground transition-colors hover:bg-border hover:text-primary"
    >
      {id}<Copy className="size-3" />
    </button>
  );
}

/** 이름 카드 — 자원 이름(+ 신청 ID 복사) · 신청 제목 · 조직(프로젝트) · 프로젝트 · 신청자(신청자 조직) · 신청 일시, 오른쪽에 진행 결과.
 *  onOpen 이 있으면 이름을 눌러 만든 자원으로 간다(완료된 VM · 클러스터). hideTitle — 신청 제목을 '신청 정보' 카드에서 보는 상세. */
export function RequestHero({ r, onOpen, hideTitle }: { r: ResourceRequest; onOpen?: () => void; hideTitle?: boolean }) {
  const isVm = r.type === "vm";
  const isWs = r.type === "ai-workspace";
  const lines = processLines(r);
  return (
    <div className="flex items-center gap-3.5 rounded-xl border border-border bg-gradient-to-br from-brand-indigo-50/70 to-transparent px-[18px] py-4">
      <div className="flex size-11 flex-none items-center justify-center rounded-[10px] bg-brand-indigo-100/70 text-primary">
        {isVm ? <Server className="size-5" /> : isWs ? <BrainCircuit className="size-5" /> : <Boxes className="size-5" />}
      </div>
      <div className="min-w-0 flex-1">
        {/* 긴 자원 이름은 자르지 않고 줄바꿈해 전체를 보인다(ETRI 검토 3번) — 신청 ID 는 첫 줄 옆 */}
        <div className="flex min-w-0 items-baseline gap-1.5">
          <div
            onClick={onOpen}
            title={onOpen ? "만든 자원 열기" : undefined}
            className={cn("min-w-0 text-[17px] font-bold [overflow-wrap:anywhere]", onOpen && "cursor-pointer underline-offset-2 hover:underline")}
          >
            {isVm ? (r.vmName || "가상머신") : isWs ? (r.workspaceName || "AI 워크스페이스") : (r.clusterName || "서비스 클러스터")}
          </div>
          {/* AI 워크스페이스는 화면 통일 대상이 아니라 그대로 */}
          {!isWs && <CopyId id={r.id} />}
        </div>
        {r.title && !hideTitle && <div className="mt-0.5 break-words text-sm">{r.title}</div>}
        <div className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
          {r.orgName && <><Building2 className="size-3" /><span>{r.orgName}</span><span className="text-muted-foreground/40">·</span></>}
          <FolderKanban className="size-3" />
          <span className="font-mono">{r.projectName}</span>
          <span className="text-muted-foreground/40">·</span>
          <span>{r.requestedByName}{r.requesterOrgName ? ` (${r.requesterOrgName})` : ""}</span>
          <span className="text-muted-foreground/40">·</span>
          <span className="font-mono">{formatDateTime(r.requestedAt)}</span>
        </div>
      </div>
      {lines.length > 0 && (
        <div className="flex-none self-stretch border-l border-border pl-4">
          <div className="text-right text-[11px] text-muted-foreground">진행 결과</div>
          <div className="mt-1 grid grid-cols-[auto_auto] items-baseline gap-x-3 gap-y-1">
            {lines.map(([label, by, at]) => (
              <React.Fragment key={label}>
                <span className="text-[11px] text-muted-foreground">{label}</span>
                <span className="text-right text-xs font-semibold">{by} · <span className="font-mono font-medium">{formatDateTime(at)}</span></span>
              </React.Fragment>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/** 사유 띠 — 반려 사유, 실패 사유(실패 상태일 때). 없으면 그리지 않는다. */
export function ReasonBanner({ r }: { r: ResourceRequest }) {
  const reject = r.approvedRejectReason ?? r.provisioningRejectReason;
  const failure = r.status === "failed" ? r.errorMessage : undefined;
  if (!reject && !failure) return null;
  return (
    <div className="grid gap-1 rounded-[10px] border border-rose-200 bg-rose-50 px-3.5 py-2.5 text-xs text-rose-700">
      {reject && <div className="flex gap-2"><XCircle className="mt-px size-3.5 flex-none" /><span className="whitespace-pre-line">반려 사유: {reject}</span></div>}
      {failure && <div className="flex gap-2"><XCircle className="mt-px size-3.5 flex-none" /><span className="whitespace-pre-line">실패 사유: {failure}</span></div>}
    </div>
  );
}

/** 섹션 카드 — 제목(아이콘) + 행. input 은 관리자가 값을 정하는 카드(남색 바탕). */
export function Section({ icon, title, aside, input, children, className }: {
  icon: React.ReactNode; title: string; aside?: React.ReactNode; input?: boolean; children: React.ReactNode; className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-3 rounded-xl border px-[18px] py-4",
      input ? "border-brand-indigo-200 bg-brand-indigo-50/30" : "border-border", className)}>
      <div className="flex items-center gap-1.5 text-sm font-bold">
        {icon}{title}
        {aside && <span className="ml-auto text-xs font-normal">{aside}</span>}
      </div>
      {children}
    </div>
  );
}

/** 섹션 행 — 왼쪽 라벨, 오른쪽 값(sub 는 값 아래 작은 줄). */
export function InfoRow({ label, sub, children }: { label: React.ReactNode; sub?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="grid min-h-6 grid-cols-[auto_minmax(0,1fr)] items-start gap-3">
      <span className="pt-px text-[13px] whitespace-nowrap text-muted-foreground">{label}</span>
      <span className="flex min-w-0 flex-col items-end gap-0.5 text-right text-[13px]">
        {children}
        {sub && <span className="font-mono text-[11px] whitespace-nowrap text-muted-foreground">{sub}</span>}
      </span>
    </div>
  );
}

/** 고정 바닥 — 왼쪽 상태 문구(ok 면 초록, 아니면 빨강), 오른쪽 버튼. */
export function DetailFooter({ status, ok, children }: { status?: React.ReactNode; ok?: boolean; children: React.ReactNode }) {
  return (
    <div className="flex flex-none items-center gap-2 border-t border-border bg-[#fafbfc] px-7 py-3.5">
      <span className={cn("flex-1 text-[13px]", ok ? "text-[#0f7a58]" : "text-[#d92d20]")}>{status}</span>
      {children}
    </div>
  );
}

/** 반려 사유 입력(필수). */
export function RejectInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">반려 사유 <span className="text-rose-500">(필수)</span></label>
      <textarea
        className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
        rows={3}
        placeholder="예: 프로젝트 쿼터 초과, 가속기 미할당 등"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoFocus
      />
    </div>
  );
}
