"use client";
import * as React from "react";
import {
  CheckCircle2,
  XCircle,
  Server,
  Boxes,
  Plus,
  Search,
  Loader2,
  AlertCircle,
  Network,
  Cpu,
  HelpCircle,
  Disc3,
  KeyRound,
  RotateCcw,
  ChevronDown,
  CircleCheck,
  CircleDashed,
  CircleMinus,
  CircleX,
  Check,
  BrainCircuit,
  Copy,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/lib/auth";
import {
  useCancelProvisionQueue, useOrgs, useProvisionAiWorkspaceTicket, useProvisionClusterTicket, useProvisionTicket,
  useReviewTicket, useTickets, useTicketSteps, type ProvisionClusterInput,
} from "@/lib/ticket-api";
import { PageHeader } from "@/components/page-header";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogDescription,
} from "@/components/ui/dialog";
import {
  Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle,
} from "@/components/ui/sheet";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ResourceRequestWizard } from "@/components/resource-request-wizard";
import {
  WorkspaceLimitFields, limitsBody, limitsOf, limitsOk, limitsText, toLimitsInput, type WorkspaceLimitsInput,
} from "@/components/workspace-requests";
import { ClusterRequestSections, type ClusterProvisionValue } from "@/components/cluster-ticket-review";
import { RequestDetailStepper, isStepperDetail, type DetailView } from "@/components/request-detail-stepper";
import { fmtSec, fmtWait, shownStatus, stepDurationMs, toastStarted, useShownClock } from "@/components/provision-progress";
import {
  DETAIL_BODY, DETAIL_CONTENT, DETAIL_W, DetailFooter, DetailHeader, InfoRow, ReasonBanner, RejectInput, RequestHero, Section,
} from "@/components/request-detail";
import { AccelBadge, NetBadge, useAccelModel } from "@/components/cluster-node-fields";
import { cn, formatDateTime } from "@/lib/utils";
import type {
  ClusterProvisionSpec, ProvisionSpec, ProvisionStep, ProvStepStatus, ResourceRequest, RequestStatus,
  RequestType,
} from "@/lib/types";

// ── 상태 표시 ──
const STATUS_LABEL: Record<RequestStatus, string> = {
  pending: "대기중",
  approved: "승인",
  provisioning: "생성중",
  completed: "완료",
  failed: "실패",
  rejected: "반려됨",
};

// 진행 보기 가능한 상태 — 프로비저닝(Temporal)·진행 팝업 연결 전이라 approved 는 제외 (연결 시 추가)
const IN_PROVISION: RequestStatus[] = ["provisioning", "completed", "failed"];

function StatusBadge({ status }: { status: RequestStatus }) {
  const variant =
    status === "completed" ? "success" :
    status === "rejected" || status === "failed" ? "destructive" :
    status === "provisioning" || status === "approved" ? "default" :
    "outline";
  return (
    <Badge variant={variant} className="gap-1 whitespace-nowrap">
      {status === "provisioning" && <Loader2 className="size-3 animate-spin" />}
      {STATUS_LABEL[status]}
    </Badge>
  );
}

// 상태 컬럼 헤더 — 물음표 호버 시 상태 프로세스 안내
function StatusHelpHead() {
  return (
    <span className="inline-flex items-center gap-1">
      상태
      <Tooltip>
        <TooltipTrigger asChild>
          <button type="button" aria-label="상태 안내" className="cursor-help text-muted-foreground/50 hover:text-muted-foreground">
            <HelpCircle className="size-3.5" />
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-[270px] px-3 py-2.5">
          <div className="space-y-2">
            <div className="font-semibold">신청 처리 상태 흐름</div>
            <div className="flex flex-wrap items-center gap-x-1 text-background/60">
              대기중 <span>→</span> 승인 <span>→</span> 생성중 <span>→</span> 완료/실패
            </div>
            <ul className="space-y-1 text-background/80">
              <li><span className="font-medium text-background">대기중</span> · 신청 접수, 관리자 승인 대기</li>
              <li><span className="font-medium text-background">승인</span> · 승인됨, 관리자 프로비저닝 대기</li>
              <li><span className="font-medium text-background">생성중</span> · 자원 생성·연결 진행</li>
              <li><span className="font-medium text-background">완료</span> · 생성 완료, 사용 가능</li>
              <li><span className="font-medium text-background">실패</span> · 프로비저닝 실패 (재시도 가능)</li>
              <li><span className="font-medium text-background">반려됨</span> · 승인 또는 프로비저닝 단계에서 거부 (사유 표시)</li>
            </ul>
          </div>
        </TooltipContent>
      </Tooltip>
    </span>
  );
}

// 승인 설정 → 프로비저닝 API 요청 형식(네트워크 이름·종류 등은 서버가 라이브 조회로 다시 채운다)
// 클러스터 프로비저닝 설정(provision_spec) → 프로비저닝(재시도) 요청 본문
function clusterSpecOf(r: ResourceRequest) {
  return r.provisionSpec as unknown as ClusterProvisionSpec | undefined; // 클러스터 신청은 provision_spec 형태가 다르다
}
function toClusterProvisionBody(p: ClusterProvisionSpec): ProvisionClusterInput["provisionSpec"] {
  return {
    networkId: p.networkId, subnetId: p.subnetId, masterCount: p.masterCount, masterFlavorId: p.masterFlavorId,
    nodeCount: p.nodeCount, flavorId: p.flavorId,
    // 지난 회차에 넣은 키페어 — 기록이 있을 때만(없으면 서버가 신청 값을 쓴다)
    ...("keypairName" in p ? { novaKeypair: p.keypairName ?? null } : {}),
  };
}

function toProvisionBody(spec: ProvisionSpec) {
  return {
    networkId: spec.networkId, floatingNetworkId: spec.floatingNetworkId, fixedIp: spec.fixedIp, securityGroups: spec.securityGroups,
    host: spec.host,
    // 바꾼 키페어 — 기록이 있을 때만(없으면 서버가 신청 값을 쓴다)
    ...("novaKeypair" in spec ? { novaKeypair: spec.novaKeypair ?? null } : {}),
  };
}

// 신청 ID · 자원 이름 — 누르면 복사(행 클릭의 상세 열기 대신). after: 값 뒤에 붙는 부가 요소(유형 배지 등)
function CopyCell({ value, label, className, after }: { value: string; label: string; className?: string; after?: React.ReactNode }) {
  return (
    <TableCell
      title={`클릭해서 ${label} 복사`}
      className={cn("group cursor-pointer font-mono text-xs hover:text-primary", className)}
      onClick={(e) => {
        e.stopPropagation();
        navigator.clipboard.writeText(value);
        toast.success(`${label} 복사됨`, { description: value });
      }}
    >
      {/* 칸 전체가 복사 영역 — 마우스를 올리면 값·아이콘에 배경(테마 색 border: 라이트 #e2e8f0) */}
      <span className="inline-flex items-center gap-1.5">
        <span className="-mx-1.5 rounded-md px-1.5 py-1 transition-colors group-hover:bg-border">
          {value}
          <Copy className="ml-1 inline-block size-3 align-[-2px] text-muted-foreground group-hover:text-primary" />
        </span>
        {after}
      </span>
    </TableCell>
  );
}

function TypeBadge({ type }: { type: RequestType }) {
  return type === "vm" ? (
    <Badge variant="ghost" className="gap-1 font-mono">
      <Server className="size-3" /> VM
    </Badge>
  ) : type === "cluster" ? (
    <Badge variant="ghost" className="gap-1 font-mono">
      <Boxes className="size-3" /> K8s
    </Badge>
  ) : (
    <Badge variant="ghost" className="gap-1 whitespace-nowrap">
      <BrainCircuit className="size-3" /> AI 워크스페이스
    </Badge>
  );
}

// 목록 '자원 이름' 칸 — VM 이름 · 클러스터 이름 · 워크스페이스 이름
function nameOf(r: ResourceRequest) {
  return (r.type === "vm" ? r.vmName : r.type === "cluster" ? r.clusterName : r.workspaceName) || "—";
}

// 목록 '신청 제목' 칸 — 한 줄 말줄임(…), 전체는 상세에서. 가장 넓게 쓰는 열
function TitleCell({ r }: { r: ResourceRequest }) {
  return r.title
    ? <div className="truncate text-sm" title={r.title}>{r.title}</div>
    : <span className="text-xs text-muted-foreground">—</span>;
}

// 목록 '프로젝트 · 신청자' 칸 — 두 줄: 프로젝트 / 신청자 (신청자 본인 조직)
function ProjectRequesterCell({ r }: { r: ResourceRequest }) {
  return (
    <div className="space-y-0.5">
      <div className="font-mono text-xs">{r.projectName}</div>
      <div className="text-[11px] text-muted-foreground">{r.requestedByName}{r.requesterOrgName ? ` (${r.requesterOrgName})` : ""}</div>
    </div>
  );
}

// 목록 스펙 칸 — VM: 플레이버 + 가속기 배지 / 클러스터: 노드 수 + Worker 플레이버 + 가속기 배지 / AI 워크스페이스: 한도
function SpecCell({ r }: { r: ResourceRequest }) {
  const modelOf = useAccelModel();
  if (r.type === "ai-workspace") return <>{limitsText(limitsOf(r))}</>;
  const accels = r.accelerators?.length ? r.accelerators
    : r.acceleratorKind ? [{ kind: r.acceleratorKind, count: r.acceleratorCount ?? 1 }] // 목업 티켓
    : [];
  const badges = <>
    {accels.map((a) => <AccelBadge key={a.kind} kind={a.kind} model={modelOf(r.deviceProfile)} count={a.count} />)}
    {/* 가속기는 있으나 종류를 모를 때(종류 저장 전 티켓) */}
    {!accels.length && r.deviceProfile && <AccelBadge />}
  </>;
  // 클러스터 — 1행 CP × 수 / 스펙, 2행 WK(Worker) × 수 / 스펙, 3행 가속기
  if (r.type === "cluster") return (
    <span className="flex flex-col items-start gap-1">
      <span><span className="text-muted-foreground">CP × {r.masterCount ?? 0} /</span> {r.masterFlavorName ?? r.masterFlavorId ?? "—"}</span>
      <span><span className="text-muted-foreground">WK × {r.nodeCount ?? 0} /</span> {r.flavorName ?? "—"}</span>
      {(accels.length > 0 || r.deviceProfile) && <span className="flex flex-wrap gap-1">{badges}</span>}
    </span>
  );
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      {r.flavorName ?? "—"}
      {badges}
    </span>
  );
}

function fmtDt(iso: string) {
  return formatDateTime(iso); // 한국 시각 — 문자열을 잘라 쓰면 서버 시간대(UTC) 그대로 보인다
}


// 사용자 희망 망 배지
function NetworkWish({ r }: { r: ResourceRequest }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <NetBadge type={r.networkType === "external" ? "internet" : "internal"} />
      {r.floatingIp && <Badge variant="success">공인 IP 희망</Badge>}
    </span>
  );
}

// 가속기 배지 묶음 — VM·클러스터 Worker 스펙 공용
function AccelBadges({ r }: { r: ResourceRequest }) {
  const modelOf = useAccelModel();
  return r.accelerators?.length
    ? (
      <span className="inline-flex flex-wrap items-center justify-end gap-1">
        {r.accelerators.map((a) => <AccelBadge key={a.kind} kind={a.kind} model={modelOf(r.deviceProfile)} count={a.count} />)}
      </span>
    )
    : r.deviceProfile
    ? <AccelBadge />
    : <span className="text-muted-foreground">없음</span>;
}

// 신청 상세 본문(공용 틀) — 이름 카드(처리 결과) · 사유 띠 · 유형별 신청 내용 · 관리자 할당/결과. DETAIL_BODY 안에 놓는다.
function RequestDetailBody({ r }: { r: ResourceRequest }) {
  const router = useRouter();
  const isVm = r.type === "vm";
  const isWs = r.type === "ai-workspace";
  const p = r.provisionSpec;
  const cp = r.type === "cluster" ? clusterSpecOf(r) : undefined;
  // 생성 완료된 VM — 이름을 누르면 IaaS > 가상서버 상세로(가상서버 상세의 네트워크·볼륨 행과 같은 방식)
  const serverId = isVm && r.status === "completed" ? r.result?.serverId : undefined;
  return (
    <>
      <RequestHero r={r} onOpen={serverId ? () => router.push(`/iaas/instances/detail/?id=${serverId}`) : undefined} />
      <ReasonBanner r={r} />

      {isVm ? (
        <div className="grid grid-cols-2 gap-[14px]">
          <Section icon={<Cpu className="size-4 text-rose-600" />} title="스펙">
            <InfoRow label="스펙"><span className="font-mono font-semibold">{r.flavorName ?? "—"}</span></InfoRow>
            <InfoRow label="vCPU / RAM"><span className="font-mono">{r.vcpu ?? "—"} vCPU / {r.ramGb ?? "—"} GB RAM</span></InfoRow>
            <InfoRow label="디스크">
              <span className="font-mono">{r.diskGb ? `${r.diskGb} GB` : r.volumeGb ? `부팅 볼륨 ${r.volumeGb} GB` : "—"}</span>
            </InfoRow>
            <InfoRow label="가속기">
              {r.accelerators?.length || r.deviceProfile
                ? <AccelBadges r={r} />
                : r.acceleratorKind
                ? <AccelBadge kind={r.acceleratorKind} count={r.acceleratorCount ?? 1} />
                : <span className="text-muted-foreground">없음</span>}
            </InfoRow>
          </Section>
          <Section icon={<Disc3 className="size-4 text-indigo-600" />} title="템플릿 · 접속">
            <InfoRow label="템플릿"><span className="font-mono [overflow-wrap:anywhere]">{r.imageName ?? "—"}</span></InfoRow>
            <InfoRow label="cloud-init">
              {r.cloudInit === false
                ? <Badge variant="warning">없음 · 키페어 미적용</Badge>
                : <span className="text-muted-foreground">포함</span>}
            </InfoRow>
            <InfoRow label="키페어">
              {r.keypairName
                ? <span className="inline-flex items-center gap-1 font-mono"><KeyRound className="size-3 text-amber-600" />{r.keypairName}</span>
                : <span className="text-muted-foreground">사용 안 함</span>}
            </InfoRow>
            <InfoRow label="희망 네트워크"><NetworkWish r={r} /></InfoRow>
            <InfoRow label="용도"><span title={r.purpose} className="max-w-full truncate">{r.purpose || "—"}</span></InfoRow>
          </Section>
        </div>
      ) : isWs ? (
        <Section icon={<BrainCircuit className="size-4 text-brand-indigo-600" />} title="AI 워크스페이스">
          <InfoRow label="워크스페이스"><span className="font-mono">{r.result?.namespace ?? r.workspaceName ?? "—"}</span></InfoRow>
          {(r.clusterName || r.clusterId) && (
            <InfoRow label="서비스 클러스터"><span className="font-mono">{r.clusterName ?? r.clusterId}</span></InfoRow>
          )}
          <InfoRow label="사용 목적">{r.purpose || <span className="text-muted-foreground">—</span>}</InfoRow>
          <InfoRow label="신청 한도"><span className="font-mono">{limitsText(r)}</span></InfoRow>
          {/* 관리자가 생성 시작 때 조정한 한도 */}
          {r.provisionSpec && <InfoRow label="생성 한도"><span className="font-mono">{limitsText(limitsOf(r))}</span></InfoRow>}
        </Section>
      ) : (
        <ClusterRequestSections r={r} />
      )}

      {/* 클러스터 — 관리자가 고른 망·서브넷, 조정한 노드, 결과 */}
      {cp && (
        <Section icon={<Network className="size-4 text-sky-600" />} title="프로비저닝 설정">
          <InfoRow label="네트워크" sub={`서브넷 ${cp.subnetCidr}`}><span className="font-mono">{cp.networkName}</span></InfoRow>
          <InfoRow label="Control Plane"><span className="font-mono">{cp.masterCount}대 / {cp.masterFlavorName}</span></InfoRow>
          <InfoRow label="Worker"><span className="font-mono">{cp.nodeCount}대 / {cp.flavorName}</span></InfoRow>
          {r.result?.apiAddress && <InfoRow label="API 주소"><span className="font-mono text-brand-mint-700">{r.result.apiAddress}</span></InfoRow>}
          {r.result?.clusterId && <InfoRow label="클러스터 ID"><span className="font-mono text-xs [overflow-wrap:anywhere]">{r.result.clusterId}</span></InfoRow>}
        </Section>
      )}

      {/* VM — 관리자 할당 · 프로비저닝 결과 */}
      {isVm && p && (
        <Section icon={<Network className="size-4 text-sky-600" />} title="할당 네트워크">
          <InfoRow label="네트워크">
            <span className="inline-flex items-center gap-1.5">
              <NetBadge type={p.networkKind === "external" ? "internet" : "internal"} /><span className="font-mono">{p.networkName}</span>
            </span>
          </InfoRow>
          <InfoRow label="보안그룹"><span className="font-mono">{p.securityGroups.length ? p.securityGroups.join(", ") : "—"}</span></InfoRow>
          {/* 받은 IP(완료 전이면 직접 입력한 IP) — 직접 입력이면 고정 IP, 아니면 DHCP 자동 할당 */}
          {(r.result?.fixedIp ?? p.fixedIp) && (
            <InfoRow label="IP">
              <span className="inline-flex items-center gap-1.5">
                <Badge variant="secondary" className="text-[10px]">{p.fixedIp ? "고정 IP" : "DHCP"}</Badge>
                <span className="font-mono">{r.result?.fixedIp ?? p.fixedIp}</span>
              </span>
            </InfoRow>
          )}
          {r.result?.floatingIp && <InfoRow label="Floating IP"><span className="font-mono text-brand-mint-700">{r.result.floatingIp}</span></InfoRow>}
        </Section>
      )}
    </>
  );
}

// 프로비저닝 진행 사이드 시트(AI 워크스페이스) — 큰 단계 + 세부 단계, MNT-CORE(tkt_provision_step) 폴링
function StepIcon({ status }: { status: ProvStepStatus }) {
  return status === "done" ? <CircleCheck className="size-5 text-foreground" />
    : status === "running" ? <Loader2 className="size-5 animate-spin text-brand-mint-500" />
    : status === "failed" ? <CircleX className="size-5 text-rose-600" />
    : status === "skipped" ? <CircleMinus className="size-5 text-muted-foreground/50" />
    : <CircleDashed className="size-5 text-muted-foreground/40" />;
}

function SubIcon({ status }: { status: ProvStepStatus }) {
  return status === "done" ? <Check className="size-3.5 text-brand-mint-600" />
    : status === "running" ? <Loader2 className="size-3.5 animate-spin text-brand-mint-500" />
    : status === "failed" ? <XCircle className="size-3.5 text-rose-600" />
    : <span className="mx-[5px] size-1 rounded-full bg-muted-foreground/40" />;
}

/** VM 시작 대기(예약) 모달 — 동시에 진행할 수 있는 프로비저닝이 모두 진행 중이라 자리를 기다리는 동안.
 *  자리가 나면 진행 화면으로 바뀐다. 닫아도(바깥·ESC) 예약은 그대로 — 목록의 진행 버튼으로 다시 본다.
 *  예상 대기 시간은 프로비저닝을 누른 시점의 값 그대로(서버가 신청에 남김) — 현재 대기 시간이 넘어가도 둔다. */
function QueueDialog({ queue, open, onClose, onCancel, cancelling }: {
  queue: { waitedSec: number; expectedSec: number }; open: boolean; onClose: () => void;
  onCancel?: () => void; cancelling: boolean;
}) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent hideClose className="max-w-md">
        <DialogHeader>
          <DialogTitle>워크플로 예약 중…</DialogTitle>
          <DialogDescription>동시에 진행할 수 있는 VM 프로비저닝이 모두 진행 중입니다. 자리가 나면 자동으로 시작합니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 rounded-lg bg-secondary/50 px-3 py-2.5 text-sm">
          <div className="flex justify-between">
            <span className="text-muted-foreground">예상 대기 시간</span>
            <span className="font-mono tabular-nums">약 {fmtWait(queue.expectedSec)}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-muted-foreground">현재 대기 시간</span>
            <span className="font-mono tabular-nums">{fmtWait(queue.waitedSec)}</span>
          </div>
        </div>
        {onCancel && (
          <DialogFooter>
            <Button onClick={onCancel} disabled={cancelling}>
              <X className="size-4" /> {cancelling ? "취소하는 중…" : "예약 취소"}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ProvisioningSheet({ req, open, onClose, canRetry }: {
  req: ResourceRequest | null; open: boolean; onClose: () => void; canRetry?: boolean;
}) {
  const qc = useQueryClient();
  const { data } = useTicketSteps(open ? req?.id : undefined);
  const isWs = req?.type === "ai-workspace";
  const isCluster = req?.type === "cluster";
  const retry = useProvisionTicket();
  const retryWs = useProvisionAiWorkspaceTicket();
  const retryCluster = useProvisionClusterTicket();
  const user = useAuth((s) => s.user);
  const retrying = isWs ? retryWs : isCluster ? retryCluster : retry;
  const cancelQueue = useCancelProvisionQueue();
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
  // 재시도는 새 진행 기록을 받을 때까지 요청 중(이전 화면 그대로) — 기록을 끝내 받지 못했으면 '생성 중'으로 본다
  const serverStatus = data?.status ?? (retrying.isSuccess ? "provisioning" : req?.status);
  // 화면 시계가 따라가는 동안은 끝났어도 '생성 중' — 마지막 단계가 보이게 끝난 뒤 완료·실패로
  const status = Number.isFinite(t) ? "provisioning" : serverStatus;
  const attempt = data?.attempt ?? 1;
  const done = status === "completed";
  const failed = status === "failed";

  // 진행률: 단계 수는 완료·건너뜀 기준, 퍼센트는 세부 단계 기준
  const finishedSteps = steps.filter((s) => s.status === "done" || s.status === "skipped").length;
  const subs = steps.filter((s) => s.status !== "skipped").flatMap((s) => s.details);
  const pct = subs.length ? Math.round((subs.filter((d) => d.status === "done").length / subs.length) * 100) : 0;
  const totalMs = done && steps.length
    ? Math.max(...steps.map((s) => (s.endedAt ? Date.parse(s.endedAt) : 0))) - Math.min(...steps.filter((s) => s.startedAt).map((s) => Date.parse(s.startedAt!)))
    : undefined;

  // 펼침: 진행 중엔 진행·실패 단계만, 모두 완료되면 전부 닫힘.
  // 사용자가 누른 값은 같은 티켓 상태 동안만 유지 — 완료로 바뀌면 초기화되어 모두 닫힌다.
  // (티켓이 바뀌면 key 로 다시 마운트되어 초기화)
  const [toggled, setToggled] = React.useState<{ status?: RequestStatus; map: Record<number, boolean> }>({ map: {} });
  const isOpen = (s: ProvisionStep) =>
    (toggled.status === status ? toggled.map[s.seq] : undefined) ?? (!done && (s.status === "running" || s.status === "failed"));

  // 상태가 바뀌면 목록도 갱신
  React.useEffect(() => {
    if (serverStatus) qc.invalidateQueries({ queryKey: ["tickets", "list"] });
  }, [serverStatus, qc]);

  // 예약 취소 결과 — 되돌려졌으면(생성 중이 아님) 닫고, 그사이 시작했으면 알리고 진행 화면으로
  const cancelSent = cancelQueue.isSuccess;
  const resetCancel = cancelQueue.reset;
  React.useEffect(() => {
    if (!cancelSent || !data) return;
    if (data.status !== "provisioning") {
      toast.success("예약을 취소했습니다", { description: `신청 ID: ${req?.id}` });
      resetCancel();
      onClose();
    } else if (data.items.length) {
      toast.info("이미 시작되어 취소하지 못했습니다");
      resetCancel();
    }
  }, [cancelSent, data, req?.id, resetCancel, onClose]);

  // 재시도 — 이전 승인 설정 그대로 새 회차로 처음부터(실패한 회차의 자원은 이미 정리됨)
  function handleRetry() {
    // AI 워크스페이스 — 지난 회차에 확정한 한도를 그대로 보낸다(빼면 서버가 신청 값으로 시작한다)
    if (req?.type === "ai-workspace") {
      retryWs.mutate(
        { id: req.id, processedBy: user?.username ?? "", expectedStatus: "failed", ...limitsOf(req) },
        {
          onSuccess: () => toastStarted(qc, req.id, "재시도를 시작했습니다"),
          onError: (e) => toast.error("재시도하지 못했습니다", {
            description: <span className="whitespace-pre-line">{e.message}</span>,
          }),
        },
      );
      return;
    }
    // 클러스터 — 지난 회차의 망·서브넷·노드 구성 그대로
    if (req?.type === "cluster") {
      const cp = clusterSpecOf(req);
      if (!cp) return;
      retryCluster.mutate(
        { id: req.id, expectedStatus: "failed", provisionSpec: toClusterProvisionBody(cp) },
        {
          onSuccess: () => toastStarted(qc, req.id, "재시도를 시작했습니다"),
          onError: (e) => toast.error("재시도하지 못했습니다", {
            description: <span className="whitespace-pre-line">{e.message}</span>,
          }),
        },
      );
      return;
    }
    if (!req?.provisionSpec) return;
    retry.mutate(
      { id: req.id, expectedStatus: "failed", provisionSpec: toProvisionBody(req.provisionSpec) },
      {
        onSuccess: () => toastStarted(qc, req.id, "재시도를 시작했습니다"),
        onError: (e) => toast.error("재시도하지 못했습니다", {
          description: e instanceof Error ? <span className="whitespace-pre-line">{e.message}</span> : undefined,
        }),
      },
    );
  }

  // 생성 중인데 첫 조회 전 — 예약 모달인지 진행 화면인지 아직 모른다(잘못 띄웠다가 바꾸지 않게)
  if (open && req?.status === "provisioning" && !data) return null;
  // 시작 대기(예약) 중 — 진행 화면 대신 예약 모달(취소는 관리자 화면에서만)
  if (data?.status === "provisioning" && data.queue) {
    return (
      <QueueDialog
        queue={data.queue}
        open={open}
        onClose={onClose}
        cancelling={cancelQueue.isPending || cancelQueue.isSuccess}
        onCancel={canRetry && req ? () => cancelQueue.mutate(req.id, {
          onError: (e) => toast.error("예약을 취소하지 못했습니다", { description: e.message }),
        }) : undefined}
      />
    );
  }

  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 overflow-y-auto p-0 sm:max-w-[640px]">
        <SheetHeader className="border-b border-border p-5">
          <SheetTitle className="flex items-center gap-2 pr-6 text-base">
            {isWs ? <BrainCircuit className="size-4 text-brand-indigo-600" />
              : isCluster ? <Boxes className="size-4 text-brand-indigo-600" />
              : <Server className="size-4 text-brand-indigo-600" />} 프로비저닝 진행
            <span className="ml-auto">
              {status && <StatusBadge status={status} />}
            </span>
          </SheetTitle>
          <div className="flex items-center gap-2">
            <SheetDescription className="font-mono">
              {req && <>{req.vmName ?? req.workspaceName ?? req.clusterName ?? req.id} · {req.projectName}</>}
            </SheetDescription>
            {attempt > 1 && (
              <Badge variant="secondary" className="text-[10px]">{attempt}차 시도{status === "provisioning" ? " 중" : ""}</Badge>
            )}
          </div>
          {/* 진행률 바 */}
          <div className="mt-2 space-y-1">
            <div className="flex items-center justify-between text-[11px] text-muted-foreground">
              <span>{finishedSteps} / {steps.length} 단계</span>
              <span className="font-mono tabular-nums">{pct}%</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-secondary">
              <div className={cn("h-full rounded-full transition-all duration-500", failed ? "bg-rose-400" : "bg-brand-mint-500")} style={{ width: `${pct}%` }} />
            </div>
          </div>
        </SheetHeader>

        {/* 단계 */}
        <div className="space-y-1 p-3">
          {status === "approved" && (
            <div className="mx-2 mb-2 flex items-center gap-2 rounded-lg bg-secondary/50 px-3 py-2 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> 승인됨 — 프로비저닝 워크플로 시작 대기 중
            </div>
          )}
          {/* 시작(재시도) 직후 — 이번 회차 단계가 등록되기 전 */}
          {status === "provisioning" && steps.length === 0 && (
            <div className="mx-2 mb-2 flex items-center gap-2 rounded-lg bg-secondary/50 px-3 py-2 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {!data ? "시작하는 중…" : attempt > 1 ? `${attempt}차 시도를 시작하는 중…` : "프로비저닝을 시작하는 중…"}
            </div>
          )}
          {steps.map((s) => {
            const expanded = isOpen(s);
            const dur = stepDurationMs(s, t);
            const muted = s.status === "waiting" || s.status === "skipped";
            return (
              <div key={s.seq} className={cn("rounded-lg px-2 py-2.5", s.status === "running" && "bg-secondary/40", s.status === "failed" && "bg-rose-50/60")}>
                <button
                  type="button"
                  onClick={() => setToggled((t) => ({ status, map: { ...(t.status === status ? t.map : {}), [s.seq]: !expanded } }))}
                  className="flex w-full items-start gap-3 text-left"
                >
                  <span className="mt-0.5 shrink-0"><StepIcon status={s.status} /></span>
                  <span className="min-w-0 flex-1">
                    <span className={cn("block text-sm font-semibold", muted && "font-medium text-muted-foreground", s.status === "failed" && "text-rose-700")}>
                      {s.message}
                    </span>
                    <span className="block font-mono text-[11px] text-muted-foreground/70">{s.code}</span>
                  </span>
                  <span className="mt-0.5 flex shrink-0 items-center gap-2 font-mono text-xs text-muted-foreground tabular-nums">
                    {/* 1초 안에 끝난 단계 — 상태 확인(1초) 사이에 끝나 0.0s 인 것도, 0.5s 처럼 기록된 것도 */}
                    {s.status === "skipped" ? "건너뜀" : dur !== undefined && (s.status !== "running" && dur < 1000 ? "1초 미만" : fmtSec(dur))}
                    <ChevronDown className={cn("size-4 transition-transform", expanded && "rotate-180")} />
                  </span>
                </button>
                {expanded && (
                  <div className="ml-8 mt-2 space-y-1.5 rounded-lg border border-border bg-background/60 px-3 py-2.5">
                    {s.status === "skipped" && (
                      <p className="text-xs text-muted-foreground">가속기가 없는 스펙이라 이 단계를 건너뜁니다.</p>
                    )}
                    {s.details.map((d) => (
                      <div key={d.code} className="flex items-center gap-2 text-xs">
                        <span className="flex size-4 shrink-0 items-center justify-center"><SubIcon status={d.status} /></span>
                        <span className={cn("flex-1", d.status === "waiting" || d.status === "skipped" ? "text-muted-foreground" : "text-foreground", d.status === "failed" && "text-rose-700")}>
                          {d.label}
                        </span>
                        <span className="font-mono text-muted-foreground">{d.status === "waiting" ? "—" : d.value}</span>
                      </div>
                    ))}
                    {s.error && <div className="mt-1 rounded-md bg-rose-50 px-2 py-1 text-xs text-rose-700">{s.error}</div>}
                  </div>
                )}
              </div>
            );
          })}
        </div>

        <div className="px-5 pb-5">
          {failed && (
            <div className="rounded-xl border border-rose-200 bg-rose-50/60 p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-rose-800">
                <AlertCircle className="size-4" /> 프로비저닝 실패
              </div>
              <p className="mt-1 text-xs text-muted-foreground">{data?.errorMessage ?? "실패한 단계를 확인하세요."}</p>
              {canRetry && (
                <Button size="sm" className="mt-3 h-7 gap-1.5" onClick={handleRetry} disabled={retrying.isPending}>
                  <RotateCcw className="size-3.5" /> {retrying.isPending ? "요청 중…" : "재시도"}
                </Button>
              )}
            </div>
          )}

          {done && (
            <div className="rounded-xl border border-brand-mint-200 bg-brand-mint-50/60 p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-brand-mint-800">
                <CheckCircle2 className="size-4" /> 프로비저닝 완료
              </div>
              <div className="mt-2 space-y-1 text-xs">
                {isWs ? (
                  <div className="flex justify-between"><span className="text-muted-foreground">네임스페이스</span><span className="font-mono">{data?.result?.namespace ?? req?.workspaceName ?? "—"}</span></div>
                ) : isCluster ? (
                  <>
                    <div className="flex justify-between"><span className="text-muted-foreground">클러스터</span><span className="font-mono">{data?.result?.clusterName ?? req?.clusterName ?? "—"}</span></div>
                    <div className="flex justify-between"><span className="text-muted-foreground">네임스페이스</span><span className="font-mono">{data?.result?.namespace ?? req?.namespace ?? "—"}</span></div>
                    {data?.result?.apiAddress && <div className="flex justify-between"><span className="text-muted-foreground">API 주소</span><span className="font-mono">{data.result.apiAddress}</span></div>}
                  </>
                ) : (
                  <>
                    {/* 받은 IP — 직접 입력했으면 고정 IP, 아니면 DHCP 자동 할당(신청 상세와 같은 표시) */}
                    {data?.result?.fixedIp && (
                      <div className="flex items-center justify-between">
                        <span className="text-muted-foreground">IP</span>
                        <span className="inline-flex items-center gap-1.5">
                          <Badge variant="secondary" className="text-[10px]">{req?.provisionSpec?.fixedIp ? "고정 IP" : "DHCP"}</Badge>
                          <span className="font-mono">{data.result.fixedIp}</span>
                        </span>
                      </div>
                    )}
                    {data?.result?.floatingIp && <div className="flex justify-between"><span className="text-muted-foreground">Floating IP</span><span className="font-mono">{data.result.floatingIp}</span></div>}
                    <div className="flex justify-between"><span className="text-muted-foreground">키페어</span><span className="font-mono">{req?.cloudInit === false ? "미적용 (cloud-init 없음)" : req?.keypairName ?? "사용 안 함"}</span></div>
                  </>
                )}
                {totalMs !== undefined && <div className="flex justify-between"><span className="text-muted-foreground">총 소요 시간</span><span className="font-mono">{fmtSec(totalMs)}</span></div>}
              </div>
              <Link href={isWs ? "/ai-development/profiles" : isCluster ? "/paas/clusters" : "/iaas/instances"} onClick={onClose} className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90">
                {isWs ? <><BrainCircuit className="size-3.5" /> AI 워크스페이스 보기</>
                  : isCluster ? <><Boxes className="size-3.5" /> 클러스터 보기</>
                  : <><Server className="size-3.5" /> 생성된 가상머신 보기</>}
              </Link>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

// 신청 유형 필터 — 관리자·사용자 뷰 공용
function TypeSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="w-36">
        <SelectValue placeholder="유형" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="all">전체 유형</SelectItem>
        <SelectItem value="vm">VM</SelectItem>
        <SelectItem value="cluster">K8s 클러스터</SelectItem>
        <SelectItem value="ai-workspace">AI 워크스페이스</SelectItem>
      </SelectContent>
    </Select>
  );
}

// 목록 검색 — 신청자·프로젝트·ID (관리자·사용자 뷰 공용)
function matchesQuery(r: ResourceRequest, q: string) {
  return !q || r.requestedByName.includes(q) || r.projectName.includes(q) || r.id.includes(q);
}
function SearchBox({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="relative">
      <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
      <Input
        placeholder="신청자·프로젝트·ID 검색…"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-60 pl-9"
      />
    </div>
  );
}

// ─────────── 관리자 뷰 ───────────
function AdminView() {
  const [wizardOpen, setWizardOpen] = React.useState(false); // 관리자도 신청(조직이 할당된 모든 프로젝트)
  const [statusFilter, setStatusFilter] = React.useState("all");
  const [typeFilter, setTypeFilter] = React.useState("all");
  const [orgFilter, setOrgFilter] = React.useState("all");
  const [q, setQ] = React.useState("");

  const filters: Record<string, string> = {};
  if (statusFilter !== "all") filters.status = statusFilter;
  if (typeFilter !== "all") filters.type = typeFilter;
  if (orgFilter !== "all") filters.orgId = orgFilter;

  // 전체 신청 = MNT-CORE 실제 티켓 (목업 폴백 없음)
  const { data, isLoading, error } = useTickets(filters);
  const { data: orgs = [] } = useOrgs();
  const reviewMutation = useReviewTicket();
  const provisionMutation = useProvisionTicket();
  const provisionWsMutation = useProvisionAiWorkspaceTicket();
  const provisionClusterMutation = useProvisionClusterTicket();
  const qc = useQueryClient();
  const user = useAuth((s) => s.user);

  const [detail, setDetail] = React.useState<ResourceRequest | null>(null);
  const [detailOpen, setDetailOpen] = React.useState(false);
  const [detailView, setDetailView] = React.useState<DetailView | undefined>(undefined);
  const [openSeq, setOpenSeq] = React.useState(0); // 연 횟수 — 상세(스테퍼)는 닫아도 마운트된 채라, 열 때마다 새로 그린다
  const [rejecting, setRejecting] = React.useState(false);
  const [rejectReason, setRejectReason] = React.useState("");
  // AI 워크스페이스 생성 시작 — 신청(또는 지난 회차) 한도로 채워 두고 조정할 수 있게 한다
  const [wsLimits, setWsLimits] = React.useState<WorkspaceLimitsInput | null>(null);
  const [sheetReq, setSheetReq] = React.useState<ResourceRequest | null>(null);
  const [sheetOpen, setSheetOpen] = React.useState(false);

  // 승인·반려·프로비저닝·진행 보기 버튼 모두 상세 모달을 연다 (반려는 사유 입력 펼침, 진행 보기는 진행 화면부터)
  function openDetail(r: ResourceRequest, mode?: "reject" | "progress") {
    setDetail(r);
    setDetailView(mode === "progress" ? "progress" : undefined);
    setRejecting(mode === "reject");
    setRejectReason("");
    setWsLimits(r.type === "ai-workspace" ? toLimitsInput(limitsOf(r)) : null);
    setOpenSeq((n) => n + 1);
    setDetailOpen(true);
  }
  function openSheet(r: ResourceRequest) {
    setSheetReq(r);
    setSheetOpen(true);
  }

  const items = (data?.items ?? []).filter((r) => matchesQuery(r, q));
  // 열어 둔 신청 — 목록이 다시 오면(진행 중 상태가 바뀌면 갱신) 그 값으로 그린다
  const shown = detail && (data?.items.find((x) => x.id === detail.id) ?? detail);

  const pending = items.filter((r) => r.status === "pending").length;
  const inProgress = items.filter((r) => ["approved", "provisioning", "completed"].includes(r.status)).length;
  const failed = items.filter((r) => r.status === "failed").length;
  const rejected = items.filter((r) => r.status === "rejected").length;

  // 처리는 열어 둔 신청의 지금 값(목록에서 다시 받은 상태)으로 — 진행 중에 실패로 바뀐 뒤 반려·재시도해도 상태가 맞게
  // expected — 상세 창이 보고 있는 지금 상태(VM·클러스터 스테퍼가 진행 기록으로 맞춰 넘긴다). 없으면 열어 둔 신청의 상태
  async function review(action: "approve" | "reject", reason = rejectReason, expected?: RequestStatus) {
    if (!shown) return;
    const r = shown;
    try {
      await reviewMutation.mutateAsync({
        id: r.id,
        action,
        reason: action === "reject" ? reason.trim() : undefined,
        expectedStatus: expected ?? r.status,
      });
      setDetailOpen(false);
      if (action === "approve") {
        toast.success("승인되었습니다", { description: `신청 ID: ${r.id}` });
      } else {
        toast.success("반려 처리됨", { description: `신청 ID: ${r.id}` });
      }
    } catch (e) {
      toast.error("처리하지 못했습니다", { description: e instanceof Error ? e.message : undefined });
    }
  }

  // 승인(VM) → 생성 중: 승인 설정으로 Temporal 워크플로 시작 후 같은 상세 창이 진행 화면으로
  // 승인(AI 워크스페이스) → 생성 중: 확정한 한도로 시작(진행 시트) / 승인(클러스터) → 생성 중: 망·서브넷·노드 구성으로 시작
  // (실패 → 재시도도 같은 경로)
  async function provision(clusterSpec?: ClusterProvisionValue, vmSpec?: ProvisionSpec, expected?: RequestStatus) {
    if (!shown) return;
    const r = shown;
    const cur = expected ?? r.status;
    const isWs = r.type === "ai-workspace";
    const isCluster = r.type === "cluster";
    if (isWs ? !wsLimits || !limitsOk(wsLimits) : isCluster ? !clusterSpec : !vmSpec) return;
    try {
      const started = isCluster
        ? await provisionClusterMutation.mutateAsync({ id: r.id, expectedStatus: cur, provisionSpec: clusterSpec! })
        : isWs
        ? await provisionWsMutation.mutateAsync({
            id: r.id,
            processedBy: user?.username ?? "",
            expectedStatus: cur,
            ...limitsBody(wsLimits!),
          })
        : await provisionMutation.mutateAsync({
            id: r.id,
            expectedStatus: cur,
            provisionSpec: toProvisionBody(vmSpec!),
          });
      toastStarted(qc, r.id,
        cur === "failed" ? "재시도를 시작했습니다" : isWs ? "워크스페이스 생성을 시작했습니다"
          : isCluster ? "클러스터 생성을 시작했습니다" : "프로비저닝을 시작했습니다");
      if (isWs) {
        setDetailOpen(false);
        openSheet(started);
      } else {
        setDetail(started); // 같은 창이 진행 화면으로 바뀐다
      }
    } catch (e) {
      toast.error(cur === "failed" ? "재시도하지 못했습니다" : "프로비저닝을 시작하지 못했습니다", {
        description: e instanceof Error ? <span className="whitespace-pre-line">{e.message}</span> : undefined, // 서버 사유의 줄바꿈 유지
      });
    }
  }

  // 그대로 재시도 — 지난 회차의 승인 설정 그대로(VM: 망·IP·보안그룹·배치·키페어, 클러스터: 망·서브넷·노드 구성·키페어)
  function retrySame(r: ResourceRequest, expected: RequestStatus) {
    if (r.type === "cluster") {
      const cp = clusterSpecOf(r);
      if (cp) provision(toClusterProvisionBody(cp), undefined, expected);
    } else if (r.provisionSpec) {
      provision(undefined, r.provisionSpec, expected);
    }
  }

  // 대기 → 승인·반려. AI 워크스페이스: 승인 → 생성 시작(한도 조정)·반려, 실패 → 한도 확인 후 재시도·반려
  // (VM·클러스터는 RequestDetailStepper)
  const isPending = detail?.status === "pending";
  const canProvisionWs = detail?.type === "ai-workspace" && (detail.status === "approved" || detail.status === "failed");
  const canProcess = isPending || canProvisionWs;

  return (
    <div className="space-y-4">
      {/* 통계 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatBlock label="대기중" value={pending} color="warning" />
        <StatBlock label="진행·완료" value={inProgress} color="mint" />
        <StatBlock label="실패" value={failed} color="rose" />
        <StatBlock label="반려됨" value={rejected} color="indigo" />
      </div>

      {/* 필터 */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <SearchBox value={q} onChange={setQ} />
          <Select value={orgFilter} onValueChange={setOrgFilter}>
            <SelectTrigger className="w-36">
              <SelectValue placeholder="조직" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 조직</SelectItem>
              {orgs.map((o) => <SelectItem key={o.id} value={o.id}>{o.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className="w-36">
              <SelectValue placeholder="상태" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 상태</SelectItem>
              {(Object.keys(STATUS_LABEL) as RequestStatus[]).map((s) => <SelectItem key={s} value={s}>{STATUS_LABEL[s]}</SelectItem>)}
            </SelectContent>
          </Select>
          <TypeSelect value={typeFilter} onChange={setTypeFilter} />
          <span className="text-xs text-muted-foreground">{items.length}건</span>
          <div className="ml-auto">
            <Button size="sm" onClick={() => setWizardOpen(true)}>
              <Plus className="size-4" /> 자원 신청
            </Button>
          </div>
        </div>
      </Card>

      {/* 테이블 */}
      {isLoading ? (
        <Skeleton className="h-64" />
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-0 whitespace-nowrap">신청 ID</TableHead>
                <TableHead className="w-0 whitespace-nowrap">프로젝트 · 신청자</TableHead>
                <TableHead>신청 제목</TableHead>
                <TableHead className="w-0 whitespace-nowrap">자원 이름</TableHead>
                <TableHead className="w-0 whitespace-nowrap">스펙</TableHead>
                <TableHead className="w-0 whitespace-nowrap">신청 일시</TableHead>
                <TableHead className="w-0 whitespace-nowrap"><StatusHelpHead /></TableHead>
                <TableHead className="w-0 whitespace-nowrap text-right">액션</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => {
                const actionable = r.status === "pending";
                const isWsRow = r.type === "ai-workspace";
                const provisionable = r.status === "approved";
                const inProvision = IN_PROVISION.includes(r.status);
                return (
                  <TableRow key={r.id} className="cursor-pointer" onClick={() => openDetail(r)}>
                    <CopyCell value={r.id} label="신청 ID" className="whitespace-nowrap font-semibold" after={<TypeBadge type={r.type} />} />
                    <TableCell className="whitespace-nowrap"><ProjectRequesterCell r={r} /></TableCell>
                    <TableCell className="max-w-0"><TitleCell r={r} /></TableCell>
                    <CopyCell value={nameOf(r)} label="자원 이름" className="whitespace-nowrap" />
                    <TableCell className="whitespace-nowrap font-mono text-xs"><SpecCell r={r} /></TableCell>
                    <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">{fmtDt(r.requestedAt)}</TableCell>
                    <TableCell className="whitespace-nowrap"><StatusBadge status={r.status} /></TableCell>
                    <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                      {actionable ? (
                        <div className="flex justify-end gap-2">
                          {/* 신청 상세의 승인 버튼과 같은 기본(채움) 버튼 */}
                          <Button size="sm" className="h-7 gap-1" onClick={() => openDetail(r)}>
                            <CheckCircle2 className="size-3.5" /> 승인
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 gap-1 border-rose-200 text-rose-600 hover:bg-rose-50"
                            onClick={() => openDetail(r, "reject")}
                          >
                            <XCircle className="size-3.5" /> 반려
                          </Button>
                        </div>
                      ) : provisionable ? (
                        <div className="flex justify-end gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 gap-1 border-brand-indigo-200 text-brand-indigo-700 hover:bg-brand-indigo-50"
                            onClick={() => openDetail(r)}
                          >
                            {isWsRow ? <><BrainCircuit className="size-3.5" /> 생성 시작</>
                              : r.type === "cluster" ? <><Boxes className="size-3.5" /> 프로비저닝</>
                              : <><Server className="size-3.5" /> 프로비저닝</>}
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 gap-1 border-rose-200 text-rose-600 hover:bg-rose-50"
                            onClick={() => openDetail(r, "reject")}
                          >
                            <XCircle className="size-3.5" /> 반려
                          </Button>
                        </div>
                      ) : inProvision ? (
                        <Button size="sm" variant="outline" className="h-7 gap-1" onClick={() => (isWsRow ? openSheet(r) : openDetail(r, "progress"))}>
                          {r.status === "completed" ? <CheckCircle2 className="size-3.5" />
                            : r.status === "failed" ? <AlertCircle className="size-3.5 text-rose-600" />
                            : <Loader2 className="size-3.5 animate-spin" />}
                          진행 보기
                        </Button>
                      ) : r.status === "rejected" ? (
                        <span className="text-xs text-muted-foreground">반려됨</span>
                      ) : null}
                    </TableCell>
                  </TableRow>
                );
              })}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={8} className={cn("py-12 text-center text-sm", error ? "text-rose-600" : "text-muted-foreground")}>
                    {error ? `신청 목록을 불러오지 못했습니다 (${error.message}).` : "조건에 맞는 신청이 없습니다."}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      )}

      {/* 프로비저닝 진행 사이드 시트(AI 워크스페이스) */}
      <ProvisioningSheet key={sheetReq?.id} req={sheetReq} open={sheetOpen} onClose={() => setSheetOpen(false)} canRetry />

      {/* 신청 상세 — VM·클러스터: 스테퍼(RequestDetailStepper — 신청 내용 · 프로비저닝 설정 · 진행을 한 창에서)
          / 대기(AI 워크스페이스): 승인·반려 / 승인·실패(AI 워크스페이스): 한도 확인·조정 후 생성 시작(재시도)·반려 */}
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        {shown && isStepperDetail(shown) ? (
          <RequestDetailStepper
            key={`${shown.id}:${openSeq}`} // 열 때마다 처음부터 — 같은 신청이라도 '반려'·'진행 보기' 버튼으로 열면 그 화면부터
            r={shown}
            admin
            open={detailOpen}
            initialView={detailView}
            rejectFirst={rejecting}
            reviewing={reviewMutation.isPending}
            provisioning={provisionMutation.isPending || provisionClusterMutation.isPending}
            onApprove={(st) => review("approve", undefined, st)}
            onReject={(reason, st) => review("reject", reason, st)}
            onProvisionVm={(spec, st) => provision(undefined, spec, st)}
            onProvisionCluster={(spec, st) => provision(spec, undefined, st)}
            onRetry={(st) => retrySame(shown, st)}
            onClose={() => setDetailOpen(false)}
          />
        ) : (
        <DialogContent className={cn(DETAIL_CONTENT, DETAIL_W.read)} onOpenAutoFocus={(e) => e.preventDefault()}>
          <DetailHeader badge={detail && <StatusBadge status={detail.status} />} description={
            isPending ? "신청 내용을 확인하고 승인하거나 반려하세요."
              : canProvisionWs && detail.status === "failed" ? "워크스페이스 한도를 확인·수정해 재시도하거나 반려하세요."
              : canProvisionWs ? "워크스페이스 한도(vCPU·메모리·가속기·저장)를 확인·조정한 뒤 생성을 시작하거나 반려하세요."
              : "신청 내용입니다."} />
          <div className={DETAIL_BODY}>
            {detail && <RequestDetailBody r={detail} />}
            {canProvisionWs && !rejecting && wsLimits && (
              <Section input icon={<BrainCircuit className="size-4 text-brand-indigo-600" />} title="워크스페이스 한도 (생성 시 적용)">
                <WorkspaceLimitFields value={wsLimits} onChange={setWsLimits} />
                {!limitsOk(wsLimits) && (
                  <p className="text-xs text-rose-500">vCPU·메모리·저장은 1 이상, 가속기는 0 이상의 정수로 입력하세요.</p>
                )}
              </Section>
            )}
            {canProcess && rejecting && <RejectInput value={rejectReason} onChange={setRejectReason} />}
          </div>
          {canProcess && (
            <DetailFooter>
              {rejecting ? (
                <>
                  <Button variant="outline" onClick={() => setRejecting(false)}>취소</Button>
                  <Button variant="destructive" onClick={() => review("reject")} disabled={reviewMutation.isPending || !rejectReason.trim()}>
                    <XCircle className="size-3.5" /> 반려 확정
                  </Button>
                </>
              ) : (
                <>
                  <Button
                    variant="outline"
                    className="border-rose-200 text-rose-600 hover:bg-rose-50"
                    onClick={() => setRejecting(true)}
                  >
                    <XCircle className="size-3.5" /> 반려
                  </Button>
                  {isPending ? (
                    <Button onClick={() => review("approve")} disabled={reviewMutation.isPending}>
                      <CheckCircle2 className="size-3.5" /> 승인
                    </Button>
                  ) : (
                    <Button onClick={() => provision()} disabled={!wsLimits || !limitsOk(wsLimits) || provisionWsMutation.isPending}>
                      {detail.status === "failed" ? <RotateCcw className="size-3.5" /> : <BrainCircuit className="size-3.5" />}
                      {provisionWsMutation.isPending ? "시작 중…" : detail.status === "failed" ? "재시도" : "생성 시작"}
                    </Button>
                  )}
                </>
              )}
            </DetailFooter>
          )}
          {detail && IN_PROVISION.includes(detail.status) && detail.status !== "failed" && (
            <DetailFooter>
              <Button variant="outline" onClick={() => { setDetailOpen(false); openSheet(detail); }}>
                {detail.type === "cluster" ? <Boxes className="size-3.5" /> : detail.type === "ai-workspace" ? <BrainCircuit className="size-3.5" />
                  : <Server className="size-3.5" />} 진행 보기
              </Button>
            </DetailFooter>
          )}
        </DialogContent>
        )}
      </Dialog>

      <ResourceRequestWizard open={wizardOpen} onClose={() => setWizardOpen(false)} />
    </div>
  );
}

// ─────────── 사용자 뷰 ───────────
function UserView({ username }: { username: string }) {
  const [wizardOpen, setWizardOpen] = React.useState(false);
  const [statusFilter, setStatusFilter] = React.useState("all");
  const [typeFilter, setTypeFilter] = React.useState("all");
  const [q, setQ] = React.useState("");
  const [detail, setDetail] = React.useState<ResourceRequest | null>(null);
  const [detailOpen, setDetailOpen] = React.useState(false);
  const [openSeq, setOpenSeq] = React.useState(0); // 연 횟수 — 상세(스테퍼)는 닫아도 마운트된 채라, 열 때마다 새로 그린다

  // 내 신청 = MNT-CORE 실제 티켓 (목업 폴백 없음). 신청자는 서버가 토큰으로 정한다
  const filters: Record<string, string> = {};
  if (statusFilter !== "all") filters.status = statusFilter;
  if (typeFilter !== "all") filters.type = typeFilter;
  const { data, isLoading, error } = useTickets(filters, !!username);

  const items = (data?.items ?? []).filter((r) => matchesQuery(r, q));
  // 열어 둔 신청 — 목록이 다시 오면(진행 중 상태가 바뀌면 갱신) 그 값으로 그린다
  const shown = detail && (data?.items.find((x) => x.id === detail.id) ?? detail);
  const pending = items.filter((r) => r.status === "pending").length;
  const inProgress = items.filter((r) => r.status === "approved" || r.status === "provisioning").length;
  const done = items.filter((r) => r.status === "completed").length;

  return (
    <div className="space-y-4">
      {/* 안내 배너 */}
      <Card className="flex items-start gap-3 border-brand-indigo-200 bg-brand-indigo-50/40 p-4">
        <AlertCircle className="mt-0.5 size-4 shrink-0 text-brand-indigo-600" />
        <p className="text-sm text-brand-indigo-800">
          자원 신청 후 관리자 승인 시 프로비저닝이 시작됩니다.
          키페어는 신청 전에 미리 <a href="/iaas/keypairs" className="underline">키페어 관리</a>에서 생성해두세요.
        </p>
      </Card>

      {/* 통계 */}
      <div className="grid grid-cols-3 gap-3">
        <StatBlock label="대기중" value={pending} color="warning" />
        <StatBlock label="진행중" value={inProgress} color="indigo" />
        <StatBlock label="완료" value={done} color="mint" />
      </div>

      {/* 필터 + 신청 버튼 */}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <SearchBox value={q} onChange={setQ} />
          <Select value={statusFilter} onValueChange={setStatusFilter}>
            <SelectTrigger className="w-36">
              <SelectValue placeholder="상태" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 상태</SelectItem>
              {(Object.keys(STATUS_LABEL) as RequestStatus[]).map((s) => <SelectItem key={s} value={s}>{STATUS_LABEL[s]}</SelectItem>)}
            </SelectContent>
          </Select>
          <TypeSelect value={typeFilter} onChange={setTypeFilter} />
          <span className="text-xs text-muted-foreground">{items.length}건</span>
          <div className="ml-auto">
            <Button size="sm" onClick={() => setWizardOpen(true)}>
              <Plus className="size-4" /> 자원 신청
            </Button>
          </div>
        </div>
      </Card>

      {/* 내 신청 목록 */}
      {isLoading ? (
        <Skeleton className="h-64" />
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-0 whitespace-nowrap">신청 ID</TableHead>
                <TableHead className="w-0 whitespace-nowrap">프로젝트 · 신청자</TableHead>
                <TableHead>신청 제목</TableHead>
                <TableHead className="w-0 whitespace-nowrap">자원 이름</TableHead>
                <TableHead className="w-0 whitespace-nowrap">스펙</TableHead>
                <TableHead className="w-0 whitespace-nowrap">신청 일시</TableHead>
                <TableHead className="w-0 whitespace-nowrap"><StatusHelpHead /></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.id} className="cursor-pointer" onClick={() => { setDetail(r); setOpenSeq((n) => n + 1); setDetailOpen(true); }}>
                  <CopyCell value={r.id} label="신청 ID" className="whitespace-nowrap font-semibold" after={<TypeBadge type={r.type} />} />
                  <TableCell className="whitespace-nowrap"><ProjectRequesterCell r={r} /></TableCell>
                  <TableCell className="max-w-0"><TitleCell r={r} /></TableCell>
                  <CopyCell value={nameOf(r)} label="자원 이름" className="whitespace-nowrap" />
                  <TableCell className="whitespace-nowrap font-mono text-xs"><SpecCell r={r} /></TableCell>
                  <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">{fmtDt(r.requestedAt)}</TableCell>
                  <TableCell className="whitespace-nowrap"><StatusBadge status={r.status} /></TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className={cn("py-12 text-center text-sm", error ? "text-rose-600" : "text-muted-foreground")}>
                    {error
                      ? `신청 목록을 불러오지 못했습니다 (${error.message}).`
                      : "신청 내역이 없습니다. 위의 \"자원 신청\" 버튼으로 신청하세요."}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
      )}

      {/* 내 신청 상세 (읽기 전용) — VM·클러스터: 스테퍼(지금 단계 안내 · 진행은 04 단계, 재시도·예약 취소 없음),
          AI 워크스페이스는 기존 상세 */}
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        {shown && isStepperDetail(shown) ? (
          <RequestDetailStepper key={`${shown.id}:${openSeq}`} r={shown} admin={false} open={detailOpen} onClose={() => setDetailOpen(false)} />
        ) : (
        <DialogContent className={cn(DETAIL_CONTENT, DETAIL_W.read)} aria-describedby={undefined} onOpenAutoFocus={(e) => e.preventDefault()}>
          <DetailHeader badge={detail && <StatusBadge status={detail.status} />} />
          <div className={DETAIL_BODY}>{detail && <RequestDetailBody r={detail} />}</div>
        </DialogContent>
        )}
      </Dialog>

      <ResourceRequestWizard open={wizardOpen} onClose={() => setWizardOpen(false)} />
    </div>
  );
}

// ─────────── 공통 StatBlock ───────────
function StatBlock({
  label, value, color,
}: {
  label: string; value: number; color: "warning" | "mint" | "rose" | "indigo";
}) {
  const map = {
    warning: "bg-amber-50 text-amber-700",
    mint: "bg-brand-mint-50 text-brand-mint-700",
    rose: "bg-rose-50 text-rose-700",
    indigo: "bg-brand-indigo-50 text-brand-indigo-700",
  };
  return (
    <Card className="p-4">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={`mt-1 inline-flex items-center rounded-md px-2 py-0.5 text-2xl font-bold tabular-nums ${map[color]}`}>
        {value}
      </div>
    </Card>
  );
}

// ─────────── 페이지 진입점 ───────────
export default function RequestsPage() {
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";

  return (
    <div className="space-y-4">
      <PageHeader
        title={isAdmin ? "자원 신청 관리" : "자원 요청 관리"}
        description={
          isAdmin
            ? "사용자 VM·K8s 클러스터·AI 워크스페이스 자원 신청을 검토하고 승인·반려합니다."
            : "VM·K8s 클러스터·AI 워크스페이스 자원을 신청하고 처리 현황을 확인합니다."
        }
      />
      {isAdmin ? <AdminView /> : <UserView username={user?.username ?? ""} />}
    </div>
  );
}
