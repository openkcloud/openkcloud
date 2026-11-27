"use client";
/**
 * 서비스 클러스터 신청(관리자) — 프로비저닝 설정(승인 · 실패의 '설정 바꿔 재시도', 신청 상세 스테퍼의 STEP 03).
 *  고정 진행 줄(네트워크 · 서브넷 · Control Plane · Worker + 키페어, 오른쪽에 '신청 값 그대로' 스위치) + 한 열 카드. 노드 구성은
 *  카드마다 신청 값 / 직접 선택, 키페어는 신청자 키(사용자 = 포털 SSH 키, 관리자 = OpenStack 키페어) · 템플릿 키페어 중에서 고르고
 *  비울 수도 있다. 실패(재시도)는 지난 회차 값으로 채운다.
 * 스펙 표 · 키페어 표 · 페이지네이션 · 세그먼트는 신청 화면과 같은 조각(cluster-node-fields).
 * 망·서브넷(DHCP·Floating IP)·쿼터·이름·키페어는 프로비저닝 요청 때 서버가 다시 확인한다(실패하면 사유가 오류로 온다).
 * ClusterRequestSections — 기존 상세(AI 워크스페이스 외 옛 신청 상세 본문)가 쓰는 클러스터 신청 내용 카드.
 */
import * as React from "react";
import { AlertCircle, Boxes, FileText, HelpCircle, KeyRound, Network, RotateCcw, Server, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Switch } from "@/components/ui/switch";
import { InfoRow, ReasonBanner, Section } from "@/components/request-detail";
import {
  AccelBadge, AccelTags, ChangedTag, KeypairTable, MAX_WORKERS, NetBadge, Pager, Radio, RefTag, SearchInput, Segment, SharedTag,
  SpecPicker, WorkerAccel, accelTotal, keyIcon, keyType, masterFlavorBlock, nodeTotals, paginate, serverIcon, specLine, useAccelModel,
  workerFlavorBlock, type KeyRow,
} from "@/components/cluster-node-fields";
import { QuotaBars, type Rows } from "@/components/request-review";
import {
  FloatProgress, FormCard, RejectBar, StepFooter, StepMain, StepperGrid, SumSection, progressStep, useCardScroll, type ProgressItem, type StepItem,
} from "@/components/request-stepper";
import { useNetwork, useNovaKeypairs, useProjectFlavors, useProjectNetworks, type FlavorSummary, type NovaKeypair } from "@/lib/iaas-api";
import { useClusterTemplate, type ClusterTemplate } from "@/lib/paas-api";
import type { ProvisionClusterInput } from "@/lib/ticket-api";
import type { ClusterProvisionSpec, ResourceRequest } from "@/lib/types";
import { cn } from "@/lib/utils";

export type ClusterProvisionValue = ProvisionClusterInput["provisionSpec"];

const TPL_KEY = "@template"; // 키페어 목록의 템플릿 키페어 행(키페어 이름·ID 와 겹치지 않는 값)

// ── 공통 ──────────────────────────────────────────────────────────

function PurposeButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={(e) => { e.stopPropagation(); onClick(); }}
      className="inline-flex flex-none cursor-pointer items-center gap-1 rounded-[5px] border border-input px-1.5 py-px text-[11px] text-primary hover:bg-brand-indigo-50"
    >
      <FileText className="size-2.5" />보기
    </button>
  );
}

/** 용도 전체 — 부모 모달 위에 겹쳐 연다(줄바꿈 유지, 넘치면 본문만 스크롤). */
function PurposeDialog({ open, onOpenChange, clusterName, purpose }: {
  open: boolean; onOpenChange: (o: boolean) => void; clusterName?: string; purpose: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[80vh] max-w-[480px] flex-col gap-0 overflow-hidden rounded-xl p-0">
        <div className="flex items-baseline gap-2 border-b border-border/70 px-5 py-4 pr-12">
          <DialogTitle className="text-[17px] font-bold">용도</DialogTitle>
          <span className="truncate font-mono text-xs text-muted-foreground">{clusterName}</span>
        </div>
        <DialogDescription asChild className="min-h-0 flex-1 overflow-y-auto px-5 py-4 text-sm leading-[1.75] whitespace-pre-wrap text-foreground">
          <div>{purpose}</div>
        </DialogDescription>
        <div className="flex justify-end border-t border-border/70 bg-secondary/30 px-5 py-3">
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>닫기</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// 신청 키페어 표시 — 이름과 출처
function requestKeyLabel(r: ResourceRequest, template?: ClusterTemplate) {
  if (r.keypairId) return { name: r.keypairName ?? r.keypairId, from: "신청자 SSH 키" };
  if (r.novaKeypair) return { name: r.novaKeypair, from: "OpenStack 키페어" };
  if (template?.keypair_id) return { name: template.keypair_id, from: "템플릿 키페어" };
  return { name: template ? "사용 안 함" : "—", from: "", none: true };
}

// 예) "NPU(RNGD) × 1" — 노드 구성의 가속기 표시와 같은 형식(모델명을 모르면 "NPU × 1")
function reqAccel(r: ResourceRequest, model: string | null) {
  return r.accelerators?.length
    ? r.accelerators.map((a) => <AccelBadge key={a.kind} kind={a.kind} model={model} count={a.count} title={r.deviceProfile ?? undefined} />)
    : r.deviceProfile ? <AccelBadge title={r.deviceProfile} /> : null;
}

// ── 클러스터 신청 내용(기존 상세) ─────────────────────────────────

/** 클러스터 신청 내용 — 클러스터(템플릿·버전·네임스페이스·용도) · 노드(신청) 카드 + 총 자원.
 *  기존 상세(생성 중·완료·반려)가 쓴다. 용도 전체는 '보기' 로 겹쳐 연다. */
export function ClusterRequestSections({ r }: { r: ResourceRequest }) {
  const { data: flavors = [] } = useProjectFlavors(r.projectId);
  const { data: template } = useClusterTemplate(r.templateId ?? null);
  const [purposeOpen, setPurposeOpen] = React.useState(false);
  const master = flavors.find((f) => f.id === r.masterFlavorId);
  const worker = flavors.find((f) => f.id === r.flavorId);
  const key = requestKeyLabel(r, template);
  const modelOf = useAccelModel();
  const accel = reqAccel(r, modelOf(r.deviceProfile));
  const kube = r.kubeVersion ?? r.k8sVersion;
  const nodes = { masterCount: r.masterCount ?? 1, masterFlavorId: r.masterFlavorId ?? "", accel: r.deviceProfile ?? "",
    nodeCount: r.nodeCount ?? 1, flavorId: r.flavorId ?? "" };
  const totals = nodeTotals(flavors, nodes);
  // 스펙 줄 — 프로젝트 플레이버에서(디스크 포함), 없으면 신청에 남긴 vCPU·RAM
  const line = (f: FlavorSummary | undefined, vcpu?: number, ramGb?: number) => f ? specLine(f) : vcpu ? `${vcpu} vCPU / ${ramGb} GB RAM` : undefined;

  return (
    <>
      <div className="grid grid-cols-2 gap-[14px]">
        <Section icon={<Boxes className="size-4 text-primary" />} title="클러스터">
          <InfoRow label="템플릿"><span className="font-mono font-semibold [overflow-wrap:anywhere]">{r.templateName ?? "—"}</span></InfoRow>
          <InfoRow label="Kubernetes 버전"><span className="font-mono">{kube ? `v${kube}` : "—"}</span></InfoRow>
          <InfoRow label="네임스페이스"><span className="font-mono">{r.result?.namespace ?? r.namespace ?? "—"}</span></InfoRow>
          <InfoRow label={<span className="flex items-center gap-1.5">용도 {r.purpose && <PurposeButton onClick={() => setPurposeOpen(true)} />}</span>}>
            <span title={r.purpose} className="max-w-full truncate font-medium">{r.purpose || "—"}</span>
          </InfoRow>
        </Section>
        <Section icon={<Server className="size-4 text-rose-600" />} title="노드 (신청)">
          <InfoRow label="Control Plane" sub={line(master, r.masterVcpu, r.masterRamGb)}>
            <span className="font-mono font-semibold">{r.masterCount ?? 0}대 / {r.masterFlavorName ?? r.masterFlavorId ?? "—"}</span>
          </InfoRow>
          <InfoRow label="Worker" sub={line(worker, r.vcpu, r.ramGb)}>
            <span className="font-mono font-semibold">{r.nodeCount ?? 0}대 / {r.flavorName ?? "—"}</span>
          </InfoRow>
          <InfoRow label="Worker 가속기">
            {accel ? <span className="flex flex-wrap justify-end gap-1">{accel}</span> : <span className="text-muted-foreground">없음</span>}
          </InfoRow>
          <InfoRow label="키페어">
            {"none" in key ? <span className="text-muted-foreground">{key.name}</span> : <>
              <span className="flex items-center gap-1 font-mono font-semibold"><KeyRound className="size-[13px] text-amber-600" />{key.name}</span>
              <span className="text-[11px] text-muted-foreground">{key.from}</span>
            </>}
          </InfoRow>
        </Section>
      </div>
      <div className="flex items-center gap-3 rounded-[10px] border border-border/70 bg-secondary/50 px-4 py-[11px]">
        <span className="text-xs text-muted-foreground">총 자원</span>
        <span className="flex-1 text-right font-mono text-xs font-semibold">
          {totals
            ? <>{[`VM ${totals.vms}대`, `${totals.vcpu} vCPU`, `${totals.ramGb} GB RAM`, `${totals.diskGb} GB Disk`].join(" / ")}
              {worker?.accelerator && <> / <AccelTags f={worker} model={modelOf(worker.accelerator)} n={nodes.nodeCount} /></>}</>
            : "—"}
        </span>
      </div>
      <PurposeDialog open={purposeOpen} onOpenChange={setPurposeOpen} clusterName={r.clusterName} purpose={r.purpose ?? ""} />
    </>
  );
}

// ── 프로비저닝 설정(스테퍼 STEP 03) ─────────────────────────────

type Card = "net" | "subnet" | "cp" | "w" | "key";
type Src = "req" | "custom";
// 필수 스펙이 비었을 때 카드 안내 — 신청 값을 이 프로젝트에서 못 쓰면 / 직접 고를 차례면
const MISSING_LINE: Record<Src, string | undefined> = { req: "신청 스펙을 사용할 수 없습니다", custom: undefined };
type SetupState = {
  net: string; subnet: string; autoNet: string; // autoNet: 이 망을 고를 때 DHCP 서브넷이 하나뿐이면 그것으로(서브넷을 직접 고르기 전까지)
  cpSrc: Src; cp: string; cpn: number;
  wSrc: Src; w: string; accel: string; wn: number;
  keySrc: Src; key: string | null;              // null = 고르지 않음
};

const NET_COLS = "grid grid-cols-[22px_minmax(0,1fr)_minmax(0,1.2fr)_168px] items-center gap-[14px] px-4"; // 유형 — 아이콘 배지 두 개가 한 줄에
const SUB_COLS = "grid grid-cols-[22px_minmax(0,1fr)_minmax(0,1fr)_84px_56px] items-center gap-[14px] px-4";

interface SetupProps {
  r: ResourceRequest;
  left: (cur?: Partial<StepItem>) => React.ReactNode; // 왼쪽 일생 스테퍼(신청 상세가 그림) — STEP 03 에 필수 n/m · 선택 필요를 덮어써 그린다
  summaryTop: React.ReactNode;  // 오른쪽 요약 머리(신청 요약)
  wish: Rows;                   // 오른쪽 '사용자 희망'(신청 노드 · 키페어)
  rejectFirst?: boolean;       // 목록의 '반려' 버튼으로 열면 반려 사유 입력부터
  reviewing?: boolean;
  provisioning?: boolean;
  onReject?: (reason: string) => void;
  onContent?: () => void;       // 바닥 왼쪽 '신청 내용 보기(전체)'
  onProvision?: (spec: ClusterProvisionValue) => void;
}

/**
 * 클러스터 프로비저닝 설정 화면 — 데이터(스펙 · 키페어 · 템플릿)를 받은 뒤 그려 신청(재시도면 지난 회차) 값으로 채운다.
 * 고정 진행 줄(네트워크 · 서브넷 · Control Plane · Worker + 키페어, 오른쪽에 '신청 값 그대로' 스위치) + 한 열 카드.
 */
export function ClusterSetupView(p: SetupProps) {
  const flavorsQ = useProjectFlavors(p.r.projectId);
  const keysQ = useNovaKeypairs();
  const templateQ = useClusterTemplate(p.r.templateId ?? null);
  // 반려 입력 — 데이터를 불러오는 중 · 불러오지 못했을 때도 반려할 수 있게 여기서 갖는다
  const [rejecting, setRejecting] = React.useState(!!p.rejectFirst);
  const loading = flavorsQ.isLoading || keysQ.isLoading || templateQ.isLoading;
  const error = flavorsQ.error ?? keysQ.error;
  if (loading || error || !flavorsQ.data || !keysQ.data) {
    return (
      <StepperGrid left={p.left()} summary={p.summaryTop} summaryTitle="신청 요약">
        <StepMain step={["03", "04"]} title="프로비저닝 설정"
          footer={rejecting ? (
            <RejectBar onCancel={() => setRejecting(false)} onConfirm={(reason) => p.onReject?.(reason)} busy={p.reviewing} />
          ) : (
            <StepFooter left={p.onContent && <Button variant="outline" onClick={p.onContent}>신청 내용 보기(전체)</Button>}>
              <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" onClick={() => setRejecting(true)}>
                <XCircle className="size-3.5" /> 반려
              </Button>
              <Button disabled>{p.r.status === "failed" ? "재시도" : "프로비저닝"}</Button>
            </StepFooter>
          )}>
          {loading ? <Skeleton className="h-[420px] rounded-xl" /> : (
            <div className="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700">
              노드 구성을 불러오지 못했습니다{error ? ` (${error.message})` : ""}.
            </div>
          )}
        </StepMain>
      </StepperGrid>
    );
  }
  return <ClusterSetupBody {...p} flavors={flavorsQ.data} keys={keysQ.data} template={templateQ.data} rejecting={rejecting} setRejecting={setRejecting} />;
}

function ClusterSetupBody({ r, left, summaryTop, wish, reviewing, provisioning, onReject, onContent, onProvision, flavors, keys, template, rejecting, setRejecting }: SetupProps & {
  flavors: FlavorSummary[]; keys: NovaKeypair[]; template?: ClusterTemplate; rejecting: boolean; setRejecting: (on: boolean) => void;
}) {
  const isFailed = r.status === "failed";
  const [tried, setTried] = React.useState(false); // [프로비저닝]을 눌러 막혔는지
  const { scrollRef, cardRef, scrollTo } = useCardScroll<Card>();
  const networksQ = useProjectNetworks(r.projectId);
  const modelOf = useAccelModel();
  const tplKey = template?.keypair_id || null;
  const tplMissing = !!tplKey && !keys.some((k) => k.name === tplKey); // 키페어를 넘기지 않으면 Magnum 이 넣는 키 — 없으면 생성이 거절됨
  // 신청 값 — 키페어는 이름(관리자 = OpenStack 키페어, 사용자 = 포털 SSH 키 — 같은 이름의 OpenStack 키페어를 쓴다), 없으면 템플릿 키페어
  const REQ = {
    cp: r.masterFlavorId ?? "", cpn: r.masterCount ?? 1, w: r.flavorId ?? "", wn: r.nodeCount ?? 1, accel: r.deviceProfile ?? "",
    key: r.keypairName ?? (tplKey ? TPL_KEY : null),
  };
  const prev = isFailed ? (r.provisionSpec as unknown as ClusterProvisionSpec | undefined) : undefined;

  const [st, setSt] = React.useState<SetupState>(() => {
    const cp = prev?.masterFlavorId ?? REQ.cp;
    const w = prev?.flavorId ?? REQ.w;
    // 지난 회차 키페어 — 기록이 있을 때만(키페어를 바꿀 수 있게 되기 전 기록은 신청 값)
    const key = prev && "keypairName" in prev ? prev.keypairName ?? (tplKey ? TPL_KEY : null) : REQ.key;
    return {
      net: prev?.networkId ?? "", subnet: prev?.subnetId ?? "", autoNet: "",
      cpSrc: cp === REQ.cp ? "req" : "custom", cp, cpn: prev?.masterCount ?? REQ.cpn,
      wSrc: w === REQ.w ? "req" : "custom", w, accel: w === REQ.w ? REQ.accel : flavors.find((f) => f.id === w)?.accelerator ?? "",
      wn: prev?.nodeCount ?? REQ.wn,
      keySrc: key === REQ.key ? "req" : "custom", key,
    };
  });
  const set = (o: Partial<SetupState>) => setSt((s) => ({ ...s, ...o }));

  // 실제 값 — 출처가 신청 값이면 신청 값. 키페어를 비우면 템플릿 키페어가 들어간다(서버에 키페어를 보내지 않음과 같음)
  const cp = st.cpSrc === "req" ? REQ.cp : st.cp;
  const w = st.wSrc === "req" ? REQ.w : st.w;
  const accel = st.wSrc === "req" ? REQ.accel : st.accel;
  const key = st.keySrc === "req" ? REQ.key : st.key;
  const keyApplied = key ?? (tplKey ? TPL_KEY : null);
  const cpF = flavors.find((f) => f.id === cp && !masterFlavorBlock(f));
  const wF = flavors.find((f) => f.id === w && !workerFlavorBlock(f, accel));
  const reqCpF = flavors.find((f) => f.id === REQ.cp);
  const reqWF = flavors.find((f) => f.id === REQ.w);

  // 망·서브넷 — 노드는 프로젝트·공유망. 공유된 외부망은 흐리게만 보인다(노드를 바로 붙이면 API 로드밸런서 주소에 밖에서
  // 닿지 못함 — 2026-10-22 시험). 서브넷은 DHCP 가 켜지고 인터넷망(외부 게이트웨이 라우터)인 것만 — API 에 Floating IP 를 붙인다
  const nodeNets = (networksQ.data ?? []).filter((n) => n.kind !== "external" || n.shared);
  const net = nodeNets.find((n) => n.id === st.net);
  const netBlocked = net?.kind === "external"; // 재시도 때 지난 회차 값으로 들어올 수 있다
  const { data: netDetail, isLoading: subnetLoading } = useNetwork(st.net || undefined);
  const subnets = netDetail?.id === st.net ? netDetail.subnets : [];
  const usable = (s: { dhcp: boolean; internet?: boolean }) => s.dhcp && !!s.internet;
  const usableSubnets = subnets.filter(usable);
  const subnetId = st.subnet || (st.autoNet === st.net && usableSubnets.length === 1 ? usableSubnets[0].id : "");
  const subnet = subnets.find((s) => s.id === subnetId && usable(s));
  const netName = net?.name ?? (prev?.networkId === st.net ? prev?.networkName : undefined);
  const subnetCidr = subnet?.cidr ?? (prev?.subnetId === subnetId && subnetId ? prev?.subnetCidr : undefined);

  // 키페어 행 — OpenStack(Nova) 키페어(이름으로 찾아 그대로 넣는다 — 새로 만들지 않음) + 템플릿 키페어
  const keyRows: KeyRow[] = [
    ...keys.map((k) => ({ id: k.name, name: k.name, fingerprint: k.fingerprint, type: k.type || keyType(k.publicKey) })),
    ...(tplKey ? [{ id: TPL_KEY, name: tplKey, fingerprint: tplMissing ? "템플릿 키페어 — OpenStack 에 없음" : "템플릿 키페어", type: "ssh" }] : []),
  ];
  const keyName = (k: string | null) => k === null ? "사용 안 함" : k === TPL_KEY ? tplKey ?? "—" : k;
  // 비웠을 때 실제로 들어가는 것 — 템플릿에 키페어가 있으면 Magnum 이 그것을 넣는다
  const keyNone = tplKey ? `템플릿 키페어(${tplKey})가 적용됩니다` : "노드에 SSH 키를 넣지 않습니다";
  // 신청 키(특히 사용자 포털 SSH 키)·템플릿 키 이름의 OpenStack 키페어가 없으면 넣을 수 없다 — 직접 선택으로 바꿔야 한다
  const keyMissing = (k: string | null) => k !== null && (k === TPL_KEY ? tplMissing : !keys.some((x) => x.name === k));
  const reqKeyRow: KeyRow | undefined = REQ.key === null ? undefined : keyRows.find((k) => k.id === REQ.key)
    ?? { id: REQ.key, name: REQ.key, fingerprint: "OpenStack 에 없음 — 직접 선택으로 다른 키페어를 고르세요", type: "—" };

  // 변경·완료
  const keyChanged = keyApplied !== REQ.key;
  const cpChanged = cp !== REQ.cp || st.cpn !== REQ.cpn;
  const wChanged = w !== REQ.w || st.wn !== REQ.wn || accel !== REQ.accel;
  const changes = [cp !== REQ.cp, st.cpn !== REQ.cpn, w !== REQ.w, st.wn !== REQ.wn, accel !== REQ.accel, keyChanged].filter(Boolean).length;
  const netOk = !!st.net && !netBlocked;
  const ready = netOk && !!subnet && !!cpF && !!wF && !keyMissing(keyApplied);
  const keyBody = { novaKeypair: keyApplied === null || keyApplied === TPL_KEY ? null : keyApplied };
  const spec: ClusterProvisionValue | null = ready
    ? { networkId: st.net, subnetId, masterCount: st.cpn, masterFlavorId: cp, nodeCount: st.wn, flavorId: w, ...(keyChanged ? keyBody : {}) }
    : null;
  // 막힌 이유와 첫 빈 카드 — [프로비저닝]을 눌러 막히면 바닥에 빨갛게 보이고 그 카드로 옮긴다(평소에는 진행 알림이 빠진 항목을 보여 준다)
  const verb = isFailed ? "재시도" : "프로비저닝";
  const blocked: [Card, string] | null = ready ? null
    : !st.net ? ["net", `네트워크를 골라야 ${verb}할 수 있습니다`]
    : netBlocked ? ["net", `외부망은 쓸 수 없습니다 — 다른 네트워크를 골라야 ${verb}할 수 있습니다`]
    : !subnet ? ["subnet", `서브넷을 골라야 ${verb}할 수 있습니다`]
    : !cpF ? ["cp", `Control Plane 스펙을 골라야 ${verb}할 수 있습니다`]
    : !wF ? ["w", `Worker 스펙을 골라야 ${verb}할 수 있습니다`]
    : ["key", `OpenStack 에 없는 키페어입니다 — 다른 키페어를 골라야 ${verb}할 수 있습니다`];
  const status = ready ? `VM ${st.cpn + st.wn}대 ${verb} 준비됨${changes ? ` · 신청 대비 변경 ${changes}건` : ""}` : tried ? blocked?.[1] : undefined;

  // 신청 값 그대로 — 세 출처가 신청 값이고 수도 신청 값일 때. 끄면 지금 값을 직접 선택 값으로 채운다
  const asReq = st.cpSrc === "req" && st.wSrc === "req" && st.keySrc === "req" && st.cpn === REQ.cpn && st.wn === REQ.wn;
  const toCustom = (r2: "cp" | "w" | "key"): Partial<SetupState> =>
    r2 === "cp" ? { cpSrc: "custom", cp } : r2 === "w" ? { wSrc: "custom", w, accel } : { keySrc: "custom", key };
  const srcSeg = (r2: "cp" | "w" | "key") => (
    <Segment options={[["req", "신청 값"], ["custom", "직접 선택"]] as const} value={r2 === "cp" ? st.cpSrc : r2 === "w" ? st.wSrc : st.keySrc}
      onChange={(s) => set(s === "req" ? { [r2 === "cp" ? "cpSrc" : r2 === "w" ? "wSrc" : "keySrc"]: "req" } : toCustom(r2))} />
  );
  const spec2 = (role: "cp" | "w") => {
    const src = role === "w" ? st.wSrc : st.cpSrc;
    return (
      <SpecPicker
        key={`${role}-${src}`}
        flavors={flavors}
        role={role}
        fixed={src === "req"}
        fixedEmpty={`신청 스펙(${role === "w" ? r.flavorName ?? REQ.w : r.masterFlavorName ?? REQ.cp})을 이 프로젝트에서 쓸 수 없습니다`}
        selected={role === "w" ? w : cp}
        onSelect={(id) => set(role === "w" ? { w: id === st.w ? "" : id } : { cp: id === st.cp ? "" : id })}
        accel={accel}
        onAccel={(dp) => set({ accel: dp, w: (() => { const f = flavors.find((x) => x.id === st.w); return f && !workerFlavorBlock(f, dp) ? f.id : ""; })() })}
        refLabel="신청"
        refId={role === "w" ? REQ.w : REQ.cp}
        refAccel={REQ.accel}
        compactFixed
      />
    );
  };

  const now = nodeTotals(flavors, { masterCount: st.cpn, masterFlavorId: cpF?.id ?? "", accel, nodeCount: st.wn, flavorId: wF?.id ?? "" });
  const req = nodeTotals(flavors, { masterCount: REQ.cpn, masterFlavorId: REQ.cp, accel: REQ.accel, nodeCount: REQ.wn, flavorId: REQ.w });
  const was = (v?: string, reqV?: string) => (
    v ? <>{reqV && v !== reqV && <span className="mr-1.5 text-[11px] text-[#9aa3ad] line-through">{reqV}</span>}
      <span className={cn(reqV && v !== reqV && "font-semibold text-[#9a4a0b]")}>{v}</span></> : undefined
  );
  const accelNow = now && wF ? accelTotal(wF, st.wn, modelOf(wF.accelerator)) : undefined;
  const accelReq = req && reqWF ? accelTotal(reqWF, REQ.wn, modelOf(reqWF.accelerator)) : undefined;

  const summary = (
    <>
      {summaryTop}
      <SumSection title="사용자 희망" rows={wish} />
      <SumSection title="총 자원" rows={[
        ["노드", was(now ? `${now.vms}대` : undefined, req ? `${req.vms}대` : undefined)],
        ["vCPU", was(now ? `${now.vcpu}` : undefined, req ? `${req.vcpu}` : undefined)],
        ["RAM", was(now ? `${now.ramGb} GB` : undefined, req ? `${req.ramGb} GB` : undefined)],
        ["Disk", was(now ? `${now.diskGb} GB` : undefined, req ? `${req.diskGb} GB` : undefined)],
        ["가속기", was(accelNow, accelReq)],
      ]} />
      {/* 노드는 이미지 부팅(로컬 디스크) — Cinder(Disk) 쿼터를 쓰지 않아 Disk 막대는 없다 */}
      <SumSection title={<>프로젝트 쿼터 <span className="font-normal text-muted-foreground">({isFailed ? "지금 기준" : "사용 → 신청 후"})</span></>}>
        <QuotaBars projectId={r.projectId} add={{ vcpu: now?.vcpu ?? 0, ramGb: now?.ramGb ?? 0 }} />
      </SumSection>
    </>
  );

  const progress: ProgressItem[] = [
    { key: "net", label: "네트워크", value: netName, ok: netOk },
    { key: "subnet", label: "서브넷", value: subnetCidr, ok: netOk && !!subnet },
    { key: "cp", label: "Control Plane", value: cpF && `${st.cpn}대`, ok: !!cpF },
    { key: "w", label: "Worker", value: wF && `${st.wn}대`, ok: !!wF },
    { key: "key", label: "키페어", value: keyName(key), ok: !keyMissing(keyApplied), optional: true },
  ];
  const jump = (k: string) => scrollTo(k as Card);

  return (
    <StepperGrid left={left(progressStep(progress, jump))} summary={summary} summaryTitle="신청 요약">
      <StepMain step={["03", "04"]} title={isFailed ? "프로비저닝 설정 · 재시도" : "프로비저닝 설정"} scrollRef={scrollRef}
        desc={isFailed ? "지난 회차 설정으로 채웠습니다. 노드 구성을 확인·수정해 재시도하거나 반려하세요."
          : "노드를 붙일 네트워크·서브넷을 고르고 노드 구성을 확인·조정한 뒤 프로비저닝하거나 반려하세요."}
        extra={
          <label className="flex cursor-pointer items-center gap-1.5 font-semibold text-foreground">
            신청 값 그대로
            <Switch checked={asReq} className="data-[state=checked]:bg-brand-mint-500" aria-label="신청 값 그대로"
              onCheckedChange={(on) => set(on ? { cpSrc: "req", wSrc: "req", keySrc: "req", cpn: REQ.cpn, wn: REQ.wn }
                : { ...toCustom("cp"), ...toCustom("w"), ...toCustom("key") })} />
          </label>
        }
        progress={<FloatProgress onJump={jump} items={progress} />}
        footer={rejecting ? (
          <RejectBar onCancel={() => setRejecting(false)} onConfirm={(reason) => onReject?.(reason)} busy={reviewing} />
        ) : (
          <StepFooter status={status} tone={ready ? "ok" : "bad"}
            left={onContent && <Button variant="outline" onClick={onContent}>신청 내용 보기(전체)</Button>}>
            <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" onClick={() => setRejecting(true)}>
              <XCircle className="size-3.5" /> 반려
            </Button>
            <Button onClick={() => { if (spec) onProvision?.(spec); else { setTried(true); if (blocked) scrollTo(blocked[0]); } }}
              disabled={provisioning} className={cn(!spec && "bg-brand-indigo-300 hover:bg-brand-indigo-300")}>
              {isFailed ? <RotateCcw className="size-3.5" /> : <Boxes className="size-3.5" />}
              {provisioning ? "시작 중…" : isFailed ? "재시도" : "프로비저닝"}
            </Button>
          </StepFooter>
        )}>
        <ReasonBanner r={r} />
        <FormCard ref={cardRef("net")} icon={<Network className="size-4 text-primary" />} title="네트워크" required
          status={netOk ? "done" : "missing"} value={st.net ? netName ?? st.net : undefined}
          line={netBlocked ? "외부망은 쓸 수 없습니다. 다른 네트워크를 고르세요" : undefined}>
          <div className="flex gap-2 rounded-[10px] border border-[#f6dfa4]! bg-[#fff8e6] px-3.5 py-3">
            <AlertCircle className="mt-px size-3.5 flex-none text-[#b7791f]" />
            <p className="text-xs text-[#7a4f06]">노드가 OpenStack API·DNS 에 닿지 못하면 생성이 끝나지 않습니다.</p>
          </div>
          <NetworkPicker networks={nodeNets} loading={networksQ.isLoading} error={networksQ.error?.message} selected={st.net}
            onSelect={(id) => (id === st.net ? set({ net: "", subnet: "", autoNet: "" }) : set({ net: id, subnet: "", autoNet: id }))} />
        </FormCard>
        <FormCard ref={cardRef("subnet")} title="서브넷" required status={netOk && subnet ? "done" : "missing"} value={subnetCidr}
          line={netOk && !subnet ? "DHCP 가 켜진 인터넷망 서브넷을 고르세요" : undefined}>
          {!st.net ? (
            <div className="rounded-[10px] border-[1.5px] border-dashed border-border bg-card p-5 text-center text-[13px] text-muted-foreground">네트워크를 먼저 고르세요</div>
          ) : (
            <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
              <div className={cn(SUB_COLS, "border-b border-border bg-secondary/50 py-[9px] text-xs text-muted-foreground")}>
                <span /><span>CIDR</span><span>이름</span><span>유형</span><span>DHCP</span>
              </div>
              {subnetLoading ? <Skeleton className="m-3 h-10" /> : subnets.map((sn) => {
                const sel = subnetId === sn.id;
                const ok = usable(sn);
                const why = !sn.dhcp ? "DHCP 가 꺼진 서브넷에는 노드가 주소를 받지 못합니다"
                  : !sn.internet ? "외부 게이트웨이 라우터에 연결되지 않아 클러스터 API 에 Floating IP 를 붙일 수 없습니다" : null;
                return (
                  <div
                    key={sn.id}
                    onClick={ok ? () => set({ subnet: sel ? "" : sn.id, autoNet: "" }) : undefined}
                    className={cn(SUB_COLS, "min-h-[46px] border-b border-border/60 py-1.5 last:border-b-0",
                      !ok ? "cursor-not-allowed bg-[#fcfcfd]" : sel ? "cursor-pointer bg-brand-indigo-50/60" : "cursor-pointer hover:bg-secondary/40")}
                  >
                    <Radio on={sel} off={!ok} />
                    <span className="flex min-w-0 flex-col gap-0.5">
                      <span className={cn("font-mono text-[13px] font-semibold", !ok && "text-[#9aa3ad]")}>{sn.cidr}</span>
                      {why && <span className="text-[11px] text-[#e8823f]">{why}</span>}
                    </span>
                    <span className={cn("truncate text-xs", ok ? "text-muted-foreground" : "text-[#b3bac2]")}>{sn.name || "—"}</span>
                    <NetBadge type={sn.internet ? "internet" : "internal"} />
                    <span className={cn("w-fit rounded-full px-2 py-0.5 text-[11px] font-semibold",
                      sn.dhcp ? "bg-[#dff6ee] text-[#0f7a58]" : "bg-[#f1f3f6] text-[#7b858f]")}>{sn.dhcp ? "켜짐" : "꺼짐"}</span>
                  </div>
                );
              })}
              {!subnetLoading && subnets.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">서브넷이 없습니다</div>}
            </div>
          )}
        </FormCard>
        <FormCard ref={cardRef("cp")} icon={serverIcon} title="Control Plane" required range="1 또는 3대"
          tag={cpChanged ? <ChangedTag /> : <RefTag label="신청 값" />} status={cpF ? "done" : "missing"} ask="스펙을 고르세요"
          value={cpF && `${st.cpn}대 / ${cpF.name}`} line={!cpF ? MISSING_LINE[st.cpSrc]
            : cpChanged ? <span className="font-mono text-[#9aa3ad] line-through">신청 {REQ.cpn}대 / {reqCpF?.name ?? r.masterFlavorName ?? REQ.cp}</span> : undefined}
          action={srcSeg("cp")}>
          <div className="flex items-center gap-3">
            <Segment options={[["1", "1대"], ["3", "3대"]] as const} value={String(st.cpn) as "1" | "3"} onChange={(n) => set({ cpn: Number(n) })} />
          </div>
          {spec2("cp")}
        </FormCard>
        <FormCard ref={cardRef("w")} icon={serverIcon} title="Worker" required range={`1~${MAX_WORKERS}대`}
          tag={wChanged ? <ChangedTag /> : <RefTag label="신청 값" />} status={wF ? "done" : "missing"} ask="스펙을 고르세요"
          value={wF && `${st.wn}대 / ${wF.name}`} line={!wF ? MISSING_LINE[st.wSrc]
            : wChanged ? <span className="font-mono text-[#9aa3ad] line-through">신청 {REQ.wn}대 / {reqWF?.name ?? r.flavorName ?? REQ.w}</span> : undefined}
          action={srcSeg("w")}>
          <div className="flex items-center gap-3">
            <div className="flex items-center overflow-hidden rounded-[7px] border border-input bg-card">
              <button type="button" onClick={() => set({ wn: Math.max(1, st.wn - 1) })} className="h-7 w-7 cursor-pointer text-[15px] text-muted-foreground hover:bg-secondary">−</button>
              <span className="w-[30px] text-center font-mono text-[13px] font-semibold tabular-nums">{st.wn}</span>
              <button type="button" onClick={() => set({ wn: Math.min(MAX_WORKERS, st.wn + 1) })} className="h-7 w-7 cursor-pointer text-[15px] text-muted-foreground hover:bg-secondary">+</button>
            </div>
            <span className="text-xs text-muted-foreground">대</span>
            {wF && <WorkerAccel f={wF} model={modelOf(wF.accelerator)} />}
          </div>
          {spec2("w")}
        </FormCard>
        <FormCard ref={cardRef("key")} icon={keyIcon} title="키페어" optional
          tag={keyChanged ? <ChangedTag /> : <RefTag label="신청 값" />} status={keyMissing(keyApplied) ? "missing" : undefined}
          value={keyName(key)} valueMuted={key === null}
          line={keyMissing(keyApplied)
            ? keyApplied === TPL_KEY ? `템플릿 키페어(${tplKey})가 OpenStack 에 없습니다. 직접 선택에서 다른 키페어를 고르세요`
              : "OpenStack 에 없는 키페어입니다. 직접 선택으로 바꾸세요"
            : key === null ? keyNone
            : keyChanged ? <span className="font-mono text-[#9aa3ad] line-through">신청 {keyName(REQ.key)}</span> : undefined}
          action={srcSeg("key")}>
          {st.keySrc === "req" ? (
            <KeypairTable key="key-req" rows={reqKeyRow ? [reqKeyRow] : []} selected={REQ.key} refId={REQ.key} refLabel="신청" nameCol="minmax(0,200px)"
              empty="신청 키페어가 없습니다 — 템플릿에도 키페어가 없어 노드에 SSH 키를 넣지 않습니다" />
          ) : (
            <KeypairTable key="key-custom" rows={keyRows} selected={st.key} refId={REQ.key} refLabel="신청" nameCol="minmax(0,200px)" paged
              onSelect={(id) => set({ key: st.key === id ? null : id })}
              empty="OpenStack 키페어가 없습니다" />
          )}
        </FormCard>
      </StepMain>
    </StepperGrid>
  );
}

/** 외부망 행 물음표 — 마우스를 올리거나 클릭하면 고를 수 없는 이유를 보여 준다.
 *  Radix 툴팁은 트리거 클릭 시 닫히므로 preventDefault 로 막고 연다. */
function ExternalNetHelp() {
  const [open, setOpen] = React.useState(false);
  return (
    <Tooltip open={open} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <button type="button" aria-label="선택할 수 없는 이유" onClick={(e) => { e.preventDefault(); setOpen(true); }}
          className="shrink-0 cursor-help text-muted-foreground/50 hover:text-muted-foreground">
          <HelpCircle className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-[270px] px-3 py-2.5">
        노드를 외부망에 직접 연결하면 클러스터 외부에서 API 주소(OVN 로드밸런서)에 접근할 수 없어 클러스터 생성이 완료되지 않습니다.
      </TooltipContent>
    </Tooltip>
  );
}

/** 네트워크 표 — 검색 · 4개씩. 유형: 서브넷마다 인터넷망(게이트웨이 IP 를 가진 라우터에 외부 게이트웨이)·내부망 — 섞여 있으면
 *  둘 다. 외부망(router:external)은 '인터넷망'으로 표시하되 흐리게(고를 수 없음). 공유된 망은 이름 옆에 '공유' 태그. 다시 누르면 해제. */
function NetworkPicker({ networks, loading, error, selected, onSelect }: {
  networks: { id: string; name: string; kind: string; cidr: string; shared?: boolean; access?: ("internet" | "internal")[] }[];
  loading: boolean; error?: string; selected: string; onSelect: (id: string) => void;
}) {
  const [query, setQuery] = React.useState("");
  const [page, setPage] = React.useState<number | null>(null);
  const q = query.trim().toLowerCase();
  const all = networks.filter((n) => n.name.toLowerCase().includes(q));
  const { rows, page: pageNow, pages } = paginate(all, page, (n) => n.id === selected);
  return (
    <>
      <SearchInput value={query} onChange={(v) => { setQuery(v); setPage(0); }} placeholder="네트워크 이름 검색" />
      <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
        <div className={cn(NET_COLS, "border-b border-border bg-secondary/50 py-[9px] text-xs text-muted-foreground")}>
          <span /><span>이름</span><span>서브넷</span><span>유형</span>
        </div>
        <div className="h-[184px] border-b border-border/60">
          {loading ? <Skeleton className="m-3 h-20" /> : error ? (
            <div className="p-5 text-center text-[13px] text-rose-600">네트워크를 불러오지 못했습니다 ({error}).</div>
          ) : rows.map((n) => {
            const sel = selected === n.id;
            const blocked = n.kind === "external";
            return (
              <div key={n.id} onClick={blocked ? undefined : () => onSelect(n.id)}
                className={cn(NET_COLS, "h-[46px] border-b border-border/60 last:border-b-0",
                  blocked ? "cursor-not-allowed bg-[#fcfcfd]" : sel ? "cursor-pointer bg-brand-indigo-50/60" : "cursor-pointer hover:bg-secondary/40")}>
                <Radio on={sel} off={blocked} />
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className={cn("truncate font-mono text-[13px] font-semibold", blocked && "text-[#9aa3ad]")}>{n.name}</span>
                    {n.shared && <SharedTag />}
                  </span>
                  {blocked && (
                    <span className="flex min-w-0 items-center gap-1">
                      <span className="truncate text-[11px] text-[#e8823f]">노드용으로 선택할 수 없습니다</span>
                      <ExternalNetHelp />
                    </span>
                  )}
                </span>
                <span title={n.cidr} className="truncate font-mono text-[11px] text-muted-foreground">{n.cidr || "—"}</span>
                <span className="flex flex-wrap gap-1">
                  {blocked ? <NetBadge type="internet" />
                    : n.access?.length ? n.access.map((t) => <NetBadge key={t} type={t} />)
                    : <span className="text-[11px] text-muted-foreground">—</span>}
                </span>
              </div>
            );
          })}
          {!loading && !error && rows.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">조건에 맞는 네트워크가 없습니다</div>}
        </div>
        <Pager total={all.length} page={pageNow} pages={pages} onPage={setPage} />
      </div>
    </>
  );
}
