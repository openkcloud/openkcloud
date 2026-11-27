"use client";
/**
 * 신청 상세(스테퍼, 시안 Stepper D) — 가상머신 · 서비스 클러스터의 모든 상태를 한 창에서 본다.
 *  왼쪽 = 일생 스테퍼(01 신청 접수 · 02 관리자 승인 · 03 프로비저닝 설정 · 04 진행·완료 — 처리자(조직) + 날짜). 단계를 누르면 그 단계의
 *  화면으로: 신청 내용(01 · 02, 완료면 04) / 프로비저닝 설정(03 — 관리자의 승인됨 · 실패) / 진행(04 — 시작한 뒤).
 *  - 대기: 신청 내용 + 승인 · 반려(반려는 바닥 줄이 사유 입력으로). 승인됨: 관리자는 프로비저닝 설정, 신청자는 신청 내용.
 *  - 생성 중: 진행 타임라인(예약 대기면 대기 시간 · 예약 취소). 실패: 실패 단계 · 사유 먼저 → 그대로 재시도 · 설정 바꿔 재시도 · 반려.
 *  - 반려 · 완료: 신청 내용(완료면 실제 할당 값 + 진행 기록 보기).
 *  신청자는 읽기 전용 + '지금 단계' 안내(재시도 · 예약 취소 없음).
 */
import * as React from "react";
import { Boxes, CheckCircle2, Copy, Info, ListChecks, Loader2, RotateCcw, Server, X, XCircle } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { DialogContent } from "@/components/ui/dialog";
import { ReasonBanner } from "@/components/request-detail";
import {
  ClusterCards, QuotaBars, VmCards, accelText, quotaView, useClusterReviewData, useVmReviewData, vmAllocKey, type NodeView, type QuotaAdd, type Rows,
} from "@/components/request-review";
import {
  LeftStepper, RejectBar, STEPPER_CONTENT, StatusPill, StepFooter, StepMain, StepperGrid, StepperHeader, SumSection, type StepItem,
} from "@/components/request-stepper";
import { ProgressTimeline, fmtSec, fmtWait, useProvisionProgress, type ProvisionProgress } from "@/components/provision-progress";
import { VmSetupView } from "@/components/vm-ticket-review";
import { ClusterSetupView, type ClusterProvisionValue } from "@/components/cluster-ticket-review";
import { useCancelProvisionQueue } from "@/lib/ticket-api";
import type { ProvisionSpec, RequestStatus, ResourceRequest } from "@/lib/types";
import { formatDateTime } from "@/lib/utils";

/** 이 창으로 보는 신청 — 가상머신 · 서비스 클러스터(모든 상태). AI 워크스페이스는 기존 상세. */
export function isStepperDetail(r: ResourceRequest) {
  return r.type === "vm" || r.type === "cluster";
}

export type DetailView = "content" | "setup" | "progress";

const STATUS: Record<RequestStatus, [string, React.ComponentProps<typeof StatusPill>["tone"]]> = {
  pending: ["대기중", "wait"], approved: ["승인", "appr"], provisioning: ["생성중", "run"], completed: ["완료", "done"],
  failed: ["실패", "fail"], rejected: ["반려됨", "fail"],
};

// 처리 기록 — '이름(조직)', 날짜는 '10-27 14:02'(스테퍼) · 'YYYY-MM-DD HH:mm'(이름 카드)
const who = (name?: string, org?: string | null) => (name ? `${name}${org ? `(${org})` : ""}` : "—");
const short = (iso?: string) => formatDateTime(iso).slice(5);

interface DetailProps {
  r: ResourceRequest;
  admin: boolean;
  open: boolean;             // 창이 닫혀도 마운트된 채라(닫히는 동안 그대로 보이게) 닫힌 동안은 진행 기록을 다시 조회하지 않는다
  initialView?: DetailView;  // 목록의 '진행 보기'로 열면 진행부터
  rejectFirst?: boolean;     // 목록의 '반려' 버튼으로 열면 반려 사유 입력부터
  reviewing?: boolean;
  provisioning?: boolean;
  // 처리 — expected = 이 창이 보고 있는 지금 상태(서버가 다르면 409). 목록에 상태 필터가 걸려 신청이 빠져도 진행 기록의 상태로 맞춘다
  onApprove?: (expected: RequestStatus) => void;
  onReject?: (reason: string, expected: RequestStatus) => void;
  onProvisionVm?: (spec: ProvisionSpec, expected: RequestStatus) => void;
  onProvisionCluster?: (spec: ClusterProvisionValue, expected: RequestStatus) => void;
  onRetry?: (expected: RequestStatus) => void; // 그대로 재시도(지난 회차 설정)
  onClose: () => void;
}

/** 화면별 내용이 받는 값 — 신청은 지금 상태로 맞춘 값, 처리 콜백은 그 상태를 붙인 것. */
interface ViewProps {
  r: ResourceRequest;
  admin: boolean;
  rejectFirst?: boolean;
  reviewing?: boolean;
  provisioning?: boolean;
  onApprove?: () => void;
  onReject?: (reason: string) => void;
  onRetry?: () => void;
  onClose: () => void;
}

/** 신청 상세 — DialogContent(1180 스테퍼). */
export function RequestDetailStepper(p: DetailProps) {
  return p.r.type === "vm" ? <VmDetail {...p} /> : <ClusterDetail {...p} />;
}

/** 신청 · 처리 화면이 함께 쓰는 값 — 이름 · 신청 내용 카드 · 실제 할당 값 · 총 자원 · 쿼터 신청분 · 사용자 희망. */
interface DetailInfo {
  kind: string; kindLabel: string; name: string; cards: React.ReactNode; alloc: Rows | null;
  totals: [string, React.ReactNode][]; add: QuotaAdd; wish?: Rows;
  // left: 왼쪽 일생 스테퍼 — 설정 화면이 STEP 03 에 덮어쓸 값(필수 n/m · 선택 필요)을 넘겨 그린다
  // content: 바닥 '신청 내용 보기(전체)' — 신청 내용 화면으로(고르던 설정은 남는다)
  setup: (ctx: {
    r: ResourceRequest; left: (cur?: Partial<StepItem>) => React.ReactNode; summaryTop: React.ReactNode; reject: (reason: string) => void; content: () => void;
  }) => React.ReactNode;
}

function VmDetail(p: DetailProps) {
  const { v, alloc } = useVmReviewData(p.r);
  const totals: [string, React.ReactNode][] = [["VM", "1대"], ["vCPU", v.vcpus != null ? `${v.vcpus}` : undefined],
    ["RAM", v.ramGb != null ? `${v.ramGb} GB` : undefined], ["Disk", v.diskGb ? `${v.diskGb} GB` : undefined], ["가속기", accelText(v.accel, 1)]];
  return (
    <Shell {...p} info={{
      kind: "VM", kindLabel: "가상머신", name: v.vmName || "가상머신", cards: <VmCards v={v} />, alloc, totals,
      add: { vcpu: v.vcpus ?? 0, ramGb: v.ramGb ?? 0, diskGb: v.diskGb },
      wish: [["네트워크(희망)", p.r.networkType === "external" ? "인터넷망" : "내부망"], ["Floating IP", p.r.floatingIp ? "희망" : "희망 안 함"],
        ["키페어", v.keypair || undefined]],
      setup: ({ r, left, summaryTop, reject, content }) => (
        <VmSetupView r={r} left={left} summaryTop={summaryTop} totals={totals} rejectFirst={p.rejectFirst} reviewing={p.reviewing}
          provisioning={p.provisioning} onReject={reject} onContent={content} onProvision={(spec) => p.onProvisionVm?.(spec, r.status)} />
      ),
    }} />
  );
}

/** 사용자 희망 노드 — 'n대' · 스펙 이름 · '(4CPU / 8GB / 80GB Disk)' 세 줄. */
function wishNode(n: NodeView) {
  const spec = [n.vcpus != null && `${n.vcpus}CPU`, n.ramGb != null && `${n.ramGb}GB`, n.diskGb != null && `${n.diskGb}GB Disk`].filter(Boolean).join(" / ");
  return <>{n.count}대<br />{n.name ?? "—"}{spec && <><br /><span className="text-[11px] text-muted-foreground">({spec})</span></>}</>;
}

function ClusterDetail(p: DetailProps) {
  const { c, alloc } = useClusterReviewData(p.r);
  const { master: m, worker: w } = c;
  const sum = (a?: number, b?: number) => (a != null && b != null ? m.count * a + w.count * b : undefined);
  const vcpu = sum(m.vcpus, w.vcpus);
  const ram = sum(m.ramGb, w.ramGb);
  const disk = sum(m.diskGb, w.diskGb);
  // 사용자 희망 — 신청한 노드와 키페어(직접 고르지 않았으면 템플릿 키페어, 템플릿에도 없으면 '—')
  const wish: Rows = [["Control Plane", wishNode(m)], ["Worker", wishNode(w)], ["키페어", c.keyFromTemplate ? "템플릿 키페어" : c.keypair]];
  return (
    <Shell {...p} info={{
      kind: "K8s", kindLabel: "서비스 클러스터", name: c.clusterName || "서비스 클러스터", cards: <ClusterCards c={c} />, alloc, wish,
      totals: [["노드", `${m.count + w.count}대`], ["vCPU", vcpu != null ? `${vcpu}` : undefined], ["RAM", ram != null ? `${ram} GB` : undefined],
        ["Disk", disk != null ? `${disk} GB` : undefined], ["가속기", accelText(c.workerAccel, w.count)]],
      // 노드는 이미지 부팅(로컬 디스크) — Cinder(Disk) 쿼터를 쓰지 않아 Disk 막대는 없다
      add: { vcpu: vcpu ?? 0, ramGb: ram ?? 0 },
      setup: ({ r, left, summaryTop, reject, content }) => (
        <ClusterSetupView r={r} left={left} summaryTop={summaryTop} wish={wish} rejectFirst={p.rejectFirst} reviewing={p.reviewing}
          provisioning={p.provisioning} onReject={reject} onContent={content} onProvision={(spec) => p.onProvisionCluster?.(spec, r.status)} />
      ),
    }} />
  );
}

/** 상태별 '지금 단계' 안내(신청자). */
function guide(r: ResourceRequest, status: RequestStatus | undefined): [string, string] | null {
  const vm = r.type === "vm";
  const res = vm ? "가상머신" : "클러스터";
  const setup = vm ? "네트워크·보안그룹" : "네트워크·서브넷";
  switch (status) {
    case "pending": return ["관리자 승인 대기", `관리자가 신청 내용과 프로젝트 쿼터를 확인하고 있습니다. 승인되면 관리자가 ${setup}을 정해 ${res}을 생성합니다. 반려되면 사유가 이 창에 표시됩니다.`];
    case "approved": return ["프로비저닝 대기", `승인되었습니다. 관리자가 ${setup}을 정해 프로비저닝을 시작하면 ${res} 생성이 진행됩니다.`];
    case "provisioning": return ["생성 중", `${res}을 만들고 있습니다. 왼쪽 04 단계를 누르면 진행 상황을 볼 수 있습니다.`];
    case "failed": return ["생성 실패", "관리자가 원인을 확인한 뒤 재시도하거나 반려합니다."];
    case "rejected": return ["반려됨", "반려 사유를 확인하세요. 필요하면 내용을 고쳐 새로 신청할 수 있습니다."];
    case "completed": return ["완료", `${res}이 만들어졌습니다. 이름을 누르면 ${vm ? "가상서버" : "클러스터"} 상세로 이동합니다.`];
    default: return null;
  }
}

function defaultView(r: ResourceRequest, admin: boolean): DetailView {
  if (r.status === "approved") return admin ? "setup" : "content";
  return r.status === "provisioning" || r.status === "failed" ? "progress" : "content";
}

function Shell({ info, ...p }: DetailProps & { info: DetailInfo }) {
  const { admin } = p;
  const qc = useQueryClient();
  // 진행 기록 — 시작한 신청만(1초마다는 생성 중일 때만). 상태가 바뀌면 목록도 갱신(이 창의 신청 값도 목록에서 다시 받는다)
  const started = !!p.r.provisioningAt || p.r.status === "provisioning";
  const prog = useProvisionProgress(started ? p.r.id : undefined, p.r.status, p.open);
  React.useEffect(() => {
    if (prog.serverStatus) qc.invalidateQueries({ queryKey: ["tickets", "list"] });
  }, [prog.serverStatus, qc]);
  // 생성 중으로 아는 신청이 끝났으면(목록 상태 필터로 목록에서 빠져 옛 값이 남는 경우 포함) 진행 기록의 상태 · 사유 · 결과로 맞춘다
  const r: ResourceRequest = p.r.status === "provisioning" && prog.data && prog.data.status !== "provisioning"
    ? { ...p.r, status: prog.data.status, errorMessage: prog.data.errorMessage ?? p.r.errorMessage, result: prog.data.result ?? p.r.result }
    : p.r;
  const act: ViewProps = {
    r, admin, rejectFirst: p.rejectFirst, reviewing: p.reviewing, provisioning: p.provisioning, onClose: p.onClose,
    onApprove: p.onApprove && (() => p.onApprove!(r.status)),
    onReject: p.onReject && ((reason: string) => p.onReject!(reason, r.status)),
    onRetry: p.onRetry && (() => p.onRetry!(r.status)),
  };
  // 고른 화면 — 상태가 바뀌면(시작 · 재시도) 기본 화면으로. 진행을 보던 중 끝나면(완료 · 실패) 진행에 머문다.
  // 처음 연 상태가 진행 전 · 중이면 완료돼도 진행에 머문다(완료를 열었을 때만 신청 내용부터)
  const [openedAs] = React.useState(r.status);
  const [pick, setPick] = React.useState<{ status: RequestStatus; view: DetailView } | null>(
    p.initialView ? { status: r.status, view: p.initialView } : null);
  const base = r.status === "completed" && openedAs !== "completed" ? "progress" : defaultView(r, admin);
  const view = !pick ? base : pick.status === r.status || pick.view === "progress" ? pick.view : base;
  // 프로비저닝 설정은 한 번 열면 다른 단계를 보는 동안에도 숨겨 둔 채 둔다 — 고르던 값이 지워지지 않게
  const [setupSeen, setSetupSeen] = React.useState(base === "setup");
  const go = (v: DetailView) => {
    if (v === "setup") setSetupSeen(true);
    setPick({ status: r.status, view: v });
  };
  const live = started ? prog.status ?? r.status : r.status;

  const rejectedAtApprove = r.status === "rejected" && !!r.approvedRejectByName;
  const rejectedAtProvision = r.status === "rejected" && !rejectedAtApprove;
  const canSetup = admin && (r.status === "approved" || r.status === "failed");
  const failedStep = prog.steps.find((s) => s.status === "failed");
  const steps: StepItem[] = [
    { no: "01", name: "신청 접수", state: "done", desc: `${who(r.requestedByName, r.requesterOrgName)}\n${short(r.requestedAt)}`,
      onClick: () => go("content"), selected: view === "content" && (r.status === "provisioning" || r.status === "failed") },
    { no: "02", name: "관리자 승인",
      state: r.status === "pending" ? "cur" : rejectedAtApprove ? "fail" : "done",
      desc: r.status === "pending" ? "대기중" : rejectedAtApprove ? `반려됨\n${short(r.approvedRejectAt)}`
        : `${who(r.approvedByName, r.approvedByOrgName)}\n${short(r.approvedAt)}`,
      onClick: () => go("content"), selected: view === "content" && (r.status === "pending" || rejectedAtApprove) },
    { no: "03", name: "프로비저닝 설정",
      state: r.status === "approved" ? "cur" : rejectedAtProvision ? "fail" : r.provisioningAt ? "done" : "todo",
      desc: r.status === "approved" ? "설정 후 시작" : rejectedAtProvision ? `반려됨\n${short(r.provisioningRejectAt)}`
        : r.provisioningAt ? `${who(r.provisioningByName, r.provisioningByOrgName)}\n${short(r.provisioningAt)}` : "승인 후 진행",
      onClick: canSetup ? () => go("setup") : r.status === "approved" || rejectedAtProvision ? () => go("content") : undefined,
      selected: view === "setup" || (view === "content" && (r.status === "approved" || rejectedAtProvision)) },
    { no: "04", name: "진행·완료",
      state: live === "provisioning" ? (prog.queue ? "cur" : "run") : live === "failed" ? "fail" : live === "completed" ? "done" : "todo",
      desc: live === "provisioning" ? (prog.queue ? `시작 대기\n약 ${fmtWait(prog.queue.expectedSec)} 예상`
          : prog.steps.length ? `${prog.finished} / ${prog.steps.length} 단계${prog.current ? `\n${prog.current.message} 중` : ""}` : "시작하는 중…")
        : live === "failed" ? `${prog.attempt}차 시도 실패${failedStep ? `\n${failedStep.message}` : ""}`
        : live === "completed" ? `완료\n${short(r.finishedAt)}` : "—",
      descTone: live === "failed" ? "red" : live === "provisioning" ? "blue" : undefined,
      onClick: started ? () => go("progress") : undefined,
      selected: view === "progress" || (view === "content" && r.status === "completed") },
  ];
  const leftWith = (cur?: Partial<StepItem>) => <LeftStepper steps={cur ? steps.map((s) => (s.no === "03" ? { ...s, ...cur } : s)) : steps} />;
  const left = leftWith();
  const summaryTop = (
    <SumSection title="신청 요약" rows={[
      ["유형", info.kindLabel], ["자원 이름", <span key="n" className="font-mono">{info.name}</span>],
      ["기관", r.orgName || undefined], ["프로젝트", r.projectName],
    ]} />
  );

  const [label, tone] = STATUS[live];
  return (
    <DialogContent hideClose className={STEPPER_CONTENT} onOpenAutoFocus={(e) => e.preventDefault()}>
      <StepperHeader kind={info.kind} title="신청 상세" onClose={p.onClose}
        badge={<StatusPill tone={tone}>{live === "provisioning" && <Loader2 className="mr-1 inline size-3 animate-spin align-[-2px]" />}{label}</StatusPill>}
        extra={<>
          {started && prog.attempt > 1 && <StatusPill tone="grey">{prog.attempt}차 시도</StatusPill>}
          {!admin && <StatusPill tone="grey">내 신청 · 읽기 전용</StatusPill>}
        </>}>
        {canSetup && (setupSeen || view === "setup") && (
          <div className={view === "setup" ? "contents" : "hidden"}>{info.setup({ r, left: leftWith, summaryTop, reject: act.onReject ?? (() => {}), content: () => go("content") })}</div>
        )}
        {view === "setup" && canSetup ? null
          : view === "progress" && started ? (
            <ProgressView {...act} info={info} left={left} prog={prog} live={live} onSetup={canSetup ? () => go("setup") : undefined}
              onContent={() => go("content")} />
          ) : (
            <ContentView {...act} info={info} left={left} summaryTop={summaryTop} live={live} onProgress={started ? () => go("progress") : undefined}
              onSetup={canSetup ? () => go("setup") : undefined} />
          )}
      </StepperHeader>
    </DialogContent>
  );
}

// ── 신청 내용 ─────────────────────────────────────────────────────

function ContentView({ r, admin, rejectFirst, reviewing, onApprove, onReject, onClose, info, left, summaryTop, live, onProgress, onSetup }: ViewProps & {
  info: DetailInfo; left: React.ReactNode; summaryTop: React.ReactNode; live: RequestStatus; onProgress?: () => void;
  onSetup?: () => void; // 관리자 · 설정할 수 있는 상태(승인됨 · 실패) — 신청 내용에서 설정 화면으로 돌아가기
}) {
  const router = useRouter();
  const canReview = admin && r.status === "pending";
  const [rejecting, setRejecting] = React.useState(!!rejectFirst && canReview); // 반려 입력은 대기 상태에서만(승인됨은 설정 화면에서)
  const rejectedAtApprove = r.status === "rejected" && !!r.approvedRejectByName;
  // 생성 완료 — 이름을 누르면 만든 자원으로(VM = IaaS > 가상서버 상세, 클러스터 = PaaS > 클러스터 상세 창)
  const done = r.status === "completed" ? r.result : undefined;
  const openPath = r.type === "vm" ? done?.serverId && `/iaas/instances/detail/?id=${done.serverId}` : done?.clusterId && `/paas/clusters/?id=${done.clusterId}`;
  const quota = quotaView(r);
  const tip = admin ? null : guide(r, live);

  const at: [string, string, string] = r.status === "pending" ? ["02", "관리자 승인", canReview ? "신청 내용을 확인하고 승인하거나 반려하세요." : ""]
    : r.status === "approved" ? (onSetup ? ["03", "신청 내용", "신청 내용 전체입니다. 프로비저닝 설정으로 돌아가 이어서 설정하세요."] : ["03", "프로비저닝 설정", ""])
    : r.status === "rejected" ? (rejectedAtApprove ? ["02", "관리자 승인", ""] : ["03", "프로비저닝 설정", ""])
    : r.status === "completed" ? ["04", "신청 내용", "오른쪽 요약에서 신청 값과 실제 할당 값을 비교합니다."]
    : ["01", "신청 내용", ""];

  // 처리 기록 — 단계별 누가(조직)·언제. 마감(완료)은 그 회차를 시작한 관리자 · 끝난 시각
  const history = ([
    ["신청", who(r.requestedByName, r.requesterOrgName), r.requestedAt, "text-foreground"],
    ["승인", r.approvedByName && who(r.approvedByName, r.approvedByOrgName), r.approvedAt, "text-primary"],
    ["반려(승인 단계)", r.approvedRejectByName && who(r.approvedRejectByName, r.approvedRejectByOrgName), r.approvedRejectAt, "text-[#d92d20]"],
    ["프로비저닝", r.provisioningByName && who(r.provisioningByName, r.provisioningByOrgName), r.provisioningAt, "text-primary"],
    ["반려(프로비저닝 단계)", r.provisioningRejectByName && who(r.provisioningRejectByName, r.provisioningRejectByOrgName), r.provisioningRejectAt, "text-[#d92d20]"],
    ["완료", r.status === "completed" && r.finishedByName && who(r.finishedByName, r.finishedByOrgName), r.finishedAt, "text-brand-mint-700"],
  ] as [string, string | false | undefined, string | undefined, string][]).filter(([, by]) => by);

  const summary = (
    <>
      {summaryTop}
      <SumSection title="총 자원" rows={info.totals} />
      {quota && (
        <SumSection title={<>프로젝트 쿼터 <span className="font-normal text-muted-foreground">({quota.note ?? "사용 → 신청 후"})</span></>}>
          <QuotaBars projectId={r.projectId} add={info.add} note={quota.warn} />
        </SumSection>
      )}
      {info.alloc && <SumSection title="실제 할당 값" rows={info.alloc} />}
    </>
  );

  const [label, tone] = STATUS[live];
  return (
    <StepperGrid left={left} summary={summary} summaryTitle="신청 요약">
      <StepMain step={[at[0], "04"]} title={at[1]} desc={at[2] || undefined}
        footer={rejecting ? (
          <RejectBar onCancel={() => setRejecting(false)} onConfirm={(reason) => onReject?.(reason)} busy={reviewing} />
        ) : (
          <StepFooter>
            {canReview ? (
              <>
                <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" onClick={() => setRejecting(true)}>
                  <XCircle className="size-3.5" /> 반려
                </Button>
                <Button onClick={onApprove} disabled={reviewing}><CheckCircle2 className="size-3.5" /> 승인</Button>
              </>
            ) : (
              <>
                {onProgress && <Button variant="outline" onClick={onProgress}><ListChecks className="size-3.5" /> 진행 기록 보기</Button>}
                {onSetup && <Button variant="outline" onClick={onSetup}>{r.status === "failed" ? "설정 바꿔 재시도 ›" : "프로비저닝 설정 ›"}</Button>}
                <Button variant="outline" onClick={onClose}>닫기</Button>
              </>
            )}
          </StepFooter>
        )}>
        {tip && <GuideBanner tip={tip} />}
        <div className="flex flex-none gap-3.5 rounded-xl border border-border px-4 pt-4 pb-3">
          <div className="flex size-10 flex-none items-center justify-center rounded-[10px] bg-brand-indigo-50 text-primary">
            {r.type === "vm" ? <Server className="size-5" /> : <Boxes className="size-5" />}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <span onClick={openPath ? () => router.push(openPath) : undefined} title={openPath ? "만든 자원 열기" : undefined}
                className={openPath ? "min-w-0 cursor-pointer font-mono text-[17px] font-bold [overflow-wrap:anywhere] underline-offset-2 hover:underline"
                  : "min-w-0 font-mono text-[17px] font-bold [overflow-wrap:anywhere]"}>{info.name}</span>
              <StatusPill tone={tone}>{label}</StatusPill>
            </div>
            <button type="button" title="클릭해서 신청 ID 복사"
              onClick={() => { navigator.clipboard.writeText(r.id); toast.success("신청 ID 복사됨", { description: r.id }); }}
              className="-mx-1 mt-0.5 inline-flex cursor-pointer items-center gap-1.5 rounded-md px-1 py-px font-mono text-xs text-muted-foreground hover:bg-brand-indigo-50 hover:text-primary">
              {r.id}<Copy className="size-3" />
            </button>
            <div className="text-xs text-muted-foreground">{[r.orgName, r.projectName].filter(Boolean).join(" / ")}</div>
            <div className="mt-2.5 grid grid-cols-[130px_minmax(0,1fr)] gap-x-2.5 gap-y-1 border-t border-dashed border-border pt-2.5 text-xs">
              {history.map(([k, by, t, cls]) => (
                <React.Fragment key={k}>
                  <span className={`font-bold ${cls}`}>{k}</span>
                  <span className="text-muted-foreground">{by} / <span className="font-mono">{formatDateTime(t)}</span></span>
                </React.Fragment>
              ))}
            </div>
          </div>
        </div>
        <ReasonBanner r={r} />
        <div className="flex-none text-[13px] font-bold">신청 내용</div>
        {info.cards}
      </StepMain>
    </StepperGrid>
  );
}

function GuideBanner({ tip }: { tip: [string, string] }) {
  return (
    <div className="flex flex-none gap-2.5 rounded-[10px] border border-brand-indigo-200! bg-[#f5f7ff] px-3.5 py-3 text-xs text-brand-indigo-900">
      <Info className="mt-px size-4 flex-none text-primary" />
      <div><b>지금 단계 — {tip[0]}</b><br />{tip[1]}</div>
    </div>
  );
}

// ── 진행 ──────────────────────────────────────────────────────────

/**
 * 진행 화면(STEP 04) — 진행 타임라인. 예약 대기면 대기 시간 두 칸 + 예약 취소(관리자). 실패면 실패 상자 + 그대로 재시도 · 설정 바꿔
 * 재시도 · 반려(관리자), 완료면 완료 상자 + 만든 자원으로. 오른쪽은 할당 정보(실제 할당 값 · 사용자 희망 · 총 자원).
 */
function ProgressView({ r, admin, reviewing, provisioning, onReject, onRetry, onClose, info, left, prog, live, onSetup, onContent }: ViewProps & {
  info: DetailInfo; left: React.ReactNode; prog: ProvisionProgress; live: RequestStatus; onSetup?: () => void; onContent: () => void;
}) {
  const [rejecting, setRejecting] = React.useState(false);
  const cancelQueue = useCancelProvisionQueue();
  const isVm = r.type === "vm";
  const failed = live === "failed";
  const done = live === "completed";
  const queue = prog.queue;
  const result = prog.data?.result ?? r.result;

  // 예약 취소 결과 — 되돌려졌으면(생성 중이 아님) 닫고, 그사이 시작했으면 알린다
  const cancelSent = cancelQueue.isSuccess;
  const resetCancel = cancelQueue.reset;
  const data = prog.data;
  React.useEffect(() => {
    if (!cancelSent || !data) return;
    if (data.status !== "provisioning") {
      toast.success("예약을 취소했습니다", { description: `신청 ID: ${r.id}` });
      resetCancel();
      onClose();
    } else if (data.items.length) {
      toast.info("이미 시작되어 취소하지 못했습니다");
      resetCancel();
    }
  }, [cancelSent, data, r.id, resetCancel, onClose]);

  const head = queue ? ["워크플로 예약 중…", "동시에 진행할 수 있는 VM 프로비저닝이 모두 진행 중입니다. 자리가 나면 자동으로 시작합니다."]
    : failed ? ["프로비저닝 실패", admin ? "실패한 단계와 사유입니다. 같은 설정으로 다시 시도하거나, 설정을 바꿔 재시도하거나, 반려하세요." : "실패한 단계와 사유입니다."]
    : done ? ["프로비저닝 완료", prog.totalMs !== undefined ? `총 소요 시간 ${fmtSec(prog.totalMs)}` : ""]
    : ["프로비저닝 진행", `${prog.startedAt ? `시작 ${formatDateTime(prog.startedAt).slice(11)} · ` : ""}이 창을 닫아도 진행은 계속됩니다.`];

  const summary = (
    <>
      {info.alloc ? <SumSection title="실제 할당 값" rows={info.alloc} /> : <SumSection title="실제 할당 값" rows={[["설정", "—"]]} />}
      {info.wish && <SumSection title="사용자 희망" rows={info.wish} />}
      <SumSection title="총 자원" rows={info.totals} />
    </>
  );

  return (
    <StepperGrid left={left} summary={summary} summaryTitle="할당 정보">
      <StepMain step={["04", "04"]} title={head[0]} desc={head[1] || undefined}
        footer={rejecting ? (
          <RejectBar onCancel={() => setRejecting(false)} onConfirm={(reason) => onReject?.(reason)} busy={reviewing} />
        ) : (
          <StepFooter status={queue && !admin ? "예약 취소는 관리자만 할 수 있습니다" : undefined}
            left={admin && failed && onSetup && <Button variant="outline" onClick={onSetup}>설정 바꿔 재시도 ›</Button>}>
            {admin && failed ? (
              <>
                <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" onClick={() => setRejecting(true)}>
                  <XCircle className="size-3.5" /> 반려
                </Button>
                <Button onClick={onRetry} disabled={provisioning || !r.provisionSpec}>
                  <RotateCcw className="size-3.5" /> {provisioning ? "시작 중…" : "그대로 재시도"}
                </Button>
              </>
            ) : (
              <>
                {admin && queue && (
                  <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" disabled={cancelQueue.isPending || cancelQueue.isSuccess}
                    onClick={() => cancelQueue.mutate(r.id, { onError: (e) => toast.error("예약을 취소하지 못했습니다", { description: e.message }) })}>
                    <X className="size-3.5" /> {cancelQueue.isPending || cancelQueue.isSuccess ? "취소하는 중…" : "예약 취소"}
                  </Button>
                )}
                {done && <Button variant="outline" onClick={onContent}>신청 내용 보기</Button>}
                <Button variant="outline" onClick={onClose}>닫기</Button>
              </>
            )}
          </StepFooter>
        )}>
        {queue ? (
          <>
            <div className="grid flex-none grid-cols-2 gap-3.5">
              <div className="rounded-[10px] border border-border px-4 py-3.5">
                <div className="text-xs text-muted-foreground">예상 대기 시간</div>
                <div className="font-mono text-[22px] font-bold">약 {fmtWait(queue.expectedSec)}</div>
              </div>
              <div className="rounded-[10px] border border-border px-4 py-3.5">
                <div className="text-xs text-muted-foreground">현재 대기 시간</div>
                <div className="font-mono text-[22px] font-bold text-primary">{fmtWait(queue.waitedSec)}</div>
              </div>
            </div>
            <div className="flex flex-none gap-2.5 rounded-[10px] border border-brand-indigo-200! bg-[#f5f7ff] px-3.5 py-3 text-xs text-brand-indigo-900">
              <Info className="mt-px size-4 flex-none text-primary" />
              5분 안에 시작하지 못하면 실패로 끝나고, 재시도할 수 있습니다. 이 창을 닫아도 예약은 유지됩니다.
            </div>
          </>
        ) : prog.steps.length === 0 ? (
          <div className="flex flex-none items-center gap-2 rounded-lg bg-secondary/50 px-3 py-2.5 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" />
            {!prog.data ? "진행 기록을 불러오는 중…" : prog.attempt > 1 ? `${prog.attempt}차 시도를 시작하는 중…` : "프로비저닝을 시작하는 중…"}
          </div>
        ) : (
          <ProgressTimeline p={prog} />
        )}
        {failed && (
          <div className="flex-none rounded-[10px] border border-rose-200 bg-rose-50 px-4 py-3 text-xs text-rose-700">
            <div className="text-[13px] font-bold">프로비저닝 실패</div>
            <p className="mt-0.5 whitespace-pre-line">{prog.data?.errorMessage ?? r.errorMessage ?? "실패한 단계를 확인하세요."}</p>
          </div>
        )}
        {done && (
          <div className="flex-none rounded-[10px] border border-brand-mint-200 bg-brand-mint-50/60 px-4 py-3">
            <div className="flex items-center gap-2 text-[13px] font-bold text-brand-mint-800"><CheckCircle2 className="size-4" /> 프로비저닝 완료</div>
            <div className="mt-2 grid grid-cols-[110px_minmax(0,1fr)] gap-y-1 text-xs">
              {isVm ? (
                <>
                  {result?.fixedIp && <><span className="text-muted-foreground">IP</span><span className="font-mono">{result.fixedIp} ({r.provisionSpec?.fixedIp ? "고정" : "DHCP"})</span></>}
                  {result?.floatingIp && <><span className="text-muted-foreground">Floating IP</span><span className="font-mono">{result.floatingIp}</span></>}
                  <span className="text-muted-foreground">키페어</span><span className="font-mono">{vmAllocKey(r) ?? "—"}</span>
                </>
              ) : (
                <>
                  <span className="text-muted-foreground">클러스터</span><span className="font-mono">{result?.clusterName ?? r.clusterName ?? "—"}</span>
                  <span className="text-muted-foreground">네임스페이스</span><span className="font-mono">{result?.namespace ?? r.namespace ?? "—"}</span>
                  {result?.apiAddress && <><span className="text-muted-foreground">API 주소</span><span className="font-mono">{result.apiAddress}</span></>}
                </>
              )}
              {prog.totalMs !== undefined && <><span className="text-muted-foreground">총 소요 시간</span><span className="font-mono">{fmtSec(prog.totalMs)}</span></>}
            </div>
            <Link href={isVm ? (result?.serverId ? `/iaas/instances/detail/?id=${result.serverId}` : "/iaas/instances")
              : result?.clusterId ? `/paas/clusters/?id=${result.clusterId}` : "/paas/clusters"} onClick={onClose}
              className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90">
              {isVm ? <><Server className="size-3.5" /> 생성된 가상머신 보기</> : <><Boxes className="size-3.5" /> 클러스터 보기</>}
            </Link>
          </div>
        )}
      </StepMain>
    </StepperGrid>
  );
}
