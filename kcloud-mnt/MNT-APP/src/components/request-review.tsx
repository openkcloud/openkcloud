"use client";
/**
 * 신청 내용(가상머신 · 서비스 클러스터) — 신청 마법사의 '검토·제출' 단계와 신청 상세 스테퍼가 같은 카드로 본다.
 *  신청 내용 카드 3장(보기 = 상세 창, 수정 = 마법사의 그 단계로 — 상세에는 없음) + 프로젝트 쿼터 막대 + 진행 순서(FlowLine).
 *  마법사는 고른 값으로, 상세는 신청 때 저장한 값(+ 이미지 · 스펙 · 템플릿 라이브 조회)으로 같은 데이터(VmReviewData · ClusterReviewData)를 만든다.
 *  프로젝트 쿼터는 둘 다 지금 사용량 기준(라이브).
 */
import * as React from "react";
import { AlertTriangle, Boxes, FileText, FolderKanban, Pencil, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  AccelBadge, ChangedTag, NetBadge, QuotaBar, RefTag, accelTotal, findFlavor, serverIcon, useAccelModel,
} from "@/components/cluster-node-fields";
import { InfoRow } from "@/components/request-detail";
import { useProjectFlavors, useProjectImages, useProjectQuotas, type FlavorSummary, type ProjectImage } from "@/lib/iaas-api";
import { useClusterTemplate, type ClusterTemplate } from "@/lib/paas-api";
import type { ClusterProvisionSpec, ResourceRequest, Tenant } from "@/lib/types";
import { cn } from "@/lib/utils";

export type Rows = [string, React.ReactNode][];

// 사용자에게 노출하는 네트워크 선택지(희망 망) — 내부 OpenStack 리소스명은 숨김
// 백엔드에서 neutronNetwork 값으로 실제 Neutron 네트워크에 매핑
export const USER_NETWORKS = [
  {
    id: "internal",
    label: "내부망",
    sublabel: "업무 · 개발 전용",
    description: "클러스터 내부에서만 통신하는 폐쇄망입니다. 외부 인터넷 접근이 불필요한 VM에 사용하세요.",
    neutronNetwork: "k8s-private-net",
    fipAvailable: false,
  },
  {
    id: "external",
    label: "인터넷망",
    sublabel: "외부 서비스 전용",
    description: "라우터를 통해 외부 인터넷과 통신이 가능한 네트워크입니다. 외부 API 호출·서비스 배포 시 선택하세요.",
    neutronNetwork: "k8s-private-net + 외부 라우팅",
    fipAvailable: true,
  },
];

// 이미지 형식(QCOW2·RAW …) · OS(os_distro os_version) · 카드 둘째 줄(형식 / OS / 최소 디스크)
export const imageFormat = (i: ProjectImage) => (i.diskFormat || "기타").toUpperCase();
export const imageOs = (i: ProjectImage) => [i.os, i.osVersion].filter(Boolean).join(" ");
export const imageLine = (i: ProjectImage) => [imageFormat(i), imageOs(i) || "OS 정보 없음", `최소 디스크 ${i.minVolumeGb} GB`].join(" / ");

// 프로젝트 쿼터 — 남은 양(쿼터 -1 은 무제한 = null), Disk 는 Cinder 원래 단위(GB)
export const quotaLeft = (quota: number, used: number) => (quota < 0 ? null : quota - used);
export const showLeft = (v: number | null, unit = "") => (v === null ? "무제한" : `${v.toLocaleString()}${unit}`);
export const diskQuota = (t: Tenant) => ({ limit: t.quotaDiskGb ?? t.quotaDiskTb * 1024, used: t.usedDiskGb ?? t.usedDiskTb * 1024 });

/** 상세 창의 남은 vCPU · RAM · Disk(신청 전) — 쿼터를 못 불러왔으면 '—'. */
function quotaLeftRows(t: Tenant | undefined): Rows {
  const d = t && diskQuota(t);
  return [
    ["남은 vCPU", t ? showLeft(quotaLeft(t.quotaVcpu, t.usedVcpu)) : "—"],
    ["남은 RAM", t ? showLeft(quotaLeft(t.quotaRamGb, t.usedRamGb), " GB") : "—"],
    ["남은 Disk", d ? showLeft(quotaLeft(d.limit, d.used), " GB") : "—"],
  ];
}

// ── 가속기 ────────────────────────────────────────────────────────

/** 가속기 — 종류별 개수(노드당), device-profile(종류를 모를 때 '가속기'), 모델명. list 도 dp 도 없으면 가속기 없음. */
export interface AccelInfo { list: { kind: string; count: number }[]; dp: string | null; model: string | null }

export function flavorAccel(f: FlavorSummary, modelOf: (dp: string | null | undefined) => string | null): AccelInfo {
  return { list: f.accelerator ? f.accelerators ?? [] : [], dp: f.accelerator ?? null, model: modelOf(f.accelerator) };
}

// 신청에 저장한 가속기 — 예전 신청은 acceleratorKind·acceleratorCount 만 있다
function requestAccel(r: ResourceRequest, modelOf: (dp: string | null | undefined) => string | null): AccelInfo {
  const list = r.accelerators?.length ? r.accelerators : r.acceleratorKind ? [{ kind: r.acceleratorKind, count: r.acceleratorCount ?? 1 }] : [];
  return { list, dp: r.deviceProfile ?? null, model: modelOf(r.deviceProfile) };
}

const hasAccel = (a: AccelInfo) => a.list.length > 0 || !!a.dp;

/** 예) "NPU(RNGD) × 2" — n 은 노드 수(accelTotal 과 같은 형식). */
export function accelText(a: AccelInfo, n: number) {
  return a.list.length ? a.list.map((x) => `${x.kind}${a.model ? `(${a.model})` : ""} × ${x.count * n}`).join(" + ")
    : a.dp ? `가속기 × ${n}` : "없음";
}

/** 총 자원의 가속기 줄 — 종류별 개수(n = VM·Worker 수). */
function AccelBadges({ a }: { a: AccelInfo }) {
  if (!hasAccel(a)) return "없음";
  return (
    <span className="inline-flex flex-wrap justify-end gap-1">
      {a.list.length ? a.list.map((x) => <AccelBadge key={x.kind || "acc"} kind={x.kind} model={a.model} count={x.count} title={a.dp ?? undefined} />)
        : <AccelBadge title={a.dp ?? undefined} />}
    </span>
  );
}

/** 새로 만드는 값 태그(네임스페이스). */
function NewTag() {
  return <span className="whitespace-nowrap rounded bg-brand-mint-100 px-[5px] py-px text-[10px] font-semibold text-brand-mint-700">새로 생성</span>;
}

// ── 신청 내용 카드 조각 ───────────────────────────────────────────

const infoIcon = <FileText className="size-4 text-brand-indigo-600" />;

function ReviewRow({ label, value, mono, sub }: { label: string; value?: React.ReactNode; mono?: boolean; sub?: string }) {
  return (
    <InfoRow label={label} sub={sub}>
      <span className={cn("max-w-full", mono && value && "font-mono")}>{value || "—"}</span>
    </InfoRow>
  );
}

/** 신청 카드 — 제목 옆 '보기'(상세 창), 오른쪽 '수정'(onEdit 이 있을 때 — 마법사의 그 단계로).
 *  위아래 12px, 행 간격 8px, 행(InfoRow) 최소 높이 24 → 20px. */
function ReviewCard({ icon, title, onView, onEdit, children }: {
  icon: React.ReactNode; title: string; onView: () => void; onEdit?: () => void; children: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2 rounded-xl border border-border px-[18px] py-3 [&>div]:min-h-5">
      <div className="flex items-center gap-1.5 text-sm font-bold">
        {icon}{title}
        <button type="button" onClick={onView}
          className="ml-1 cursor-pointer rounded border border-border px-1.5 py-px text-[11px] font-medium transition-colors hover:bg-secondary">보기</button>
        {onEdit && (
          <button type="button" onClick={onEdit}
            className="ml-auto inline-flex cursor-pointer items-center gap-1 text-xs font-medium text-primary hover:underline">
            <Pencil className="size-3" />수정
          </button>
        )}
      </div>
      {children}
    </div>
  );
}

/** 용도 첫 줄 — 카드에는 한 줄만(전체는 보기). */
function FirstLine({ text }: { text: string }) {
  const first = text.trim().split("\n")[0];
  return first ? <span title={text.trim()} className="block truncate">{first}</span> : "—";
}

/** '보기' 상세 창 — 제목 · 본문(넘치면 스크롤) · 닫기. 신청 창 위에 겹쳐 뜬다. */
function ViewDialog({ title, open, onClose, children }: { title: string; open: boolean; onClose: () => void; children: React.ReactNode }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent aria-describedby={undefined} className="flex max-h-[80vh] max-w-[520px] flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b border-border px-6 py-4">
          <DialogTitle className="text-[17px] font-bold">{title}</DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4 [scrollbar-width:thin]">{children}</div>
        <DialogFooter className="border-t border-border bg-[#fafbfc] px-6 py-3">
          <Button variant="outline" onClick={onClose}>닫기</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 신청 정보 상세 — 신청 제목 · 용도(줄바꿈 그대로). */
function RequestInfoView({ title, purpose }: { title: string; purpose: string }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-2">
        <span className="text-[13px] font-bold">신청 제목</span>
        <div className="rounded-lg bg-secondary/70 px-3.5 py-2.5 text-[15px] font-semibold break-words">{title.trim() || "—"}</div>
      </div>
      <div className="grid gap-2 border-t border-border pt-4">
        <span className="text-[13px] font-bold">용도</span>
        <div className="rounded-lg bg-secondary/70 px-3.5 py-2.5 text-sm whitespace-pre-line break-words">{purpose.trim() || "—"}</div>
      </div>
    </div>
  );
}

/** 상세 창 본문 — 묶음 제목 + 행(왼쪽 라벨, 오른쪽 값). */
function DetailGroups({ groups }: { groups: { title: string; rows: Rows }[] }) {
  return (
    <div className="flex flex-col gap-5">
      {groups.map((g) => (
        <div key={g.title}>
          <div className="pb-1 text-[13px] font-bold">{g.title}</div>
          {g.rows.map(([k, v]) => (
            <div key={k} className="grid grid-cols-[140px_minmax(0,1fr)] items-baseline gap-3 border-b border-border py-2.5 text-[13px]">
              <span className="text-muted-foreground">{k}</span>
              <span className="min-w-0 font-mono font-medium break-all">{v || "—"}</span>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

/** 프로젝트 쿼터 묶음(사용 → 신청 후 / 한도) — note: 제목 덧말, warn: 막대 위 경고. */
export interface QuotaView { note?: string; warn?: string }

/** 신청분(vCPU · RAM · Disk) — diskGb 가 없으면 Disk 막대 없음(클러스터 노드는 이미지 부팅이라 Cinder 를 쓰지 않음). */
export interface QuotaAdd { vcpu: number; ramGb: number; diskGb?: number }

/** 프로젝트 쿼터 막대 값 — 사용 → 신청 후 / 한도, 넘는 항목(over) · 꽉 차는 항목(full). 가속기(NPU) 쿼터는 OpenStack 에 없다. */
function useQuotaBars(projectId: string, add: QuotaAdd, enabled = true) {
  const { data: tenants, isLoading, error } = useProjectQuotas(enabled && !!projectId);
  const t = tenants?.find((x) => x.id === projectId);
  const bars = t ? [
    { k: "vCPU", used: t.usedVcpu, add: add.vcpu, limit: t.quotaVcpu, unit: "vCPU" },
    { k: "RAM", used: t.usedRamGb, add: add.ramGb, limit: t.quotaRamGb, unit: "GB" },
    ...(add.diskGb === undefined ? [] : [{ k: "Disk", ...diskQuota(t), add: add.diskGb, unit: "GB" }]),
  ] : [];
  const after = (b: typeof bars[number]) => quotaLeft(b.limit, b.used + b.add);
  const over = bars.filter((b) => (after(b) ?? 1) < 0).map((b) => b.k);
  const full = bars.filter((b) => after(b) === 0).map((b) => b.k);
  return { t, bars, over, full, isLoading, error };
}

/** 쿼터 막대 묶음 — 스테퍼 요약 · 신청 단계 카드. warnAt: 경고 문구를 막대 위(top) 또는 아래(bottom). */
export function QuotaBars({ projectId, add, warnAt = "top", note }: { projectId: string; add: QuotaAdd; warnAt?: "top" | "bottom"; note?: string }) {
  const { t, bars, over, full, isLoading, error } = useQuotaBars(projectId, add);
  if (!projectId) return <p className="text-xs text-muted-foreground">프로젝트를 고르면 보입니다</p>;
  const warn = over.length > 0 ? <p className="flex items-center gap-1 text-xs font-semibold text-[#d92d20]"><XCircle className="size-3.5" />{over.join(", ")} 쿼터가 부족합니다</p>
    : full.length > 0 && <p className="flex items-center gap-1 text-xs font-semibold text-[#b54708]"><AlertTriangle className="size-3.5" />{full.join(", ")} 쿼터를 모두 사용하게 됩니다</p>;
  return (
    <div className="flex flex-col gap-2">
      {warnAt === "top" && warn}
      {note && <p className="text-xs text-[#b54708]">{note}</p>}
      {isLoading ? <p className="text-xs text-muted-foreground">쿼터 확인 중…</p>
        : error ? <p className="text-xs text-rose-500">쿼터를 불러오지 못했습니다 ({error.message})</p>
        : !t ? <p className="text-xs text-muted-foreground">이 프로젝트의 쿼터 정보가 없습니다</p>
        : bars.map((b) => <QuotaBar key={b.k} label={b.k} q={{ quota: b.limit, used: b.used }} add={b.add} unit={b.unit} />)}
      {warnAt === "bottom" && warn}
    </div>
  );
}

// ── 진행 순서 ──────────────────────────────────────────────────────

export const VM_FLOW = ["신청 접수", "관리자 승인", "VM 생성", "네트워크·스토리지 연결", "부팅 완료"];
export const CLUSTER_FLOW = ["신청 접수", "관리자 승인", "노드 VM 생성", "클러스터 구성", "네임스페이스·접근 권한"];

/** 진행 순서(가로 한 줄) — 앞 단계는 초록 점, 지금 단계(current)는 남색 점 + 굵게(failed 면 빨강), 남은 단계는 회색. */
export function FlowLine({ label, steps, current, failed, className }: {
  label: string; steps: string[]; current: number; failed?: boolean; className?: string;
}) {
  return (
    <span className={cn("flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-xs whitespace-nowrap text-muted-foreground", className)}>
      <span className="mr-1 font-semibold text-foreground">{label}</span>
      {steps.map((s, i) => (
        <React.Fragment key={s}>
          {i > 0 && <span aria-hidden className="text-[#c5ccd6]">—</span>}
          <span className={cn("inline-flex items-center gap-1.5", i === current && (failed ? "font-semibold text-[#d92d20]" : "font-semibold text-foreground"))}>
            <span className={cn("size-[7px] flex-none rounded-full",
              i < current ? "bg-brand-mint-500" : i === current ? (failed ? "bg-[#d92d20]" : "bg-primary") : "bg-[#b6c0d3]")} />{s}
          </span>
        </React.Fragment>
      ))}
    </span>
  );
}

// ── 가상머신 ──────────────────────────────────────────────────────

export interface VmReviewData {
  title: string; purpose: string;
  projectId: string; projectName?: string; orgName?: string | null;
  imageName?: string; image?: ProjectImage; // image — 형식 · OS · 최소 디스크(못 찾으면 '—')
  cloudInit: boolean;
  vmName: string;
  networkType?: string; floatingIp: boolean;
  flavorName?: string; vcpus?: number; ramGb?: number;
  diskGb: number;    // 부팅 볼륨(Cinder 쿼터에서 차감)
  diskLabel: string; // '80 GB' · 디스크 없는 스펙이면 '부팅 볼륨 100 GB'
  accel: AccelInfo;
  keypair?: string;
}

/** 가상머신 신청 내용 카드(신청 정보 · 프로젝트 · 템플릿 · 가상서버) + 보기 창. onEdit 이 있으면 '수정'(신청 단계 — 그 단계로). */
export function VmCards({ v, onEdit }: { v: VmReviewData; onEdit?: (card: "info" | "source" | "vm") => void }) {
  const [view, setView] = React.useState<"info" | "source" | "vm" | null>(null);
  const { data: tenants } = useProjectQuotas(!!v.projectId);
  const net = USER_NETWORKS.find((n) => n.id === v.networkType);
  const keyValue = !v.cloudInit ? "미적용 (cloud-init 없음)" : v.keypair || "사용 안 함";
  const ram = v.ramGb != null ? `${v.ramGb} GB` : "—";
  const edit = (c: "info" | "source" | "vm") => (onEdit ? () => onEdit(c) : undefined);
  return (
    <div className="flex min-w-0 flex-none flex-col gap-2">
      <ReviewCard icon={infoIcon} title="신청 정보" onView={() => setView("info")} onEdit={edit("info")}>
        <ReviewRow label="신청 제목" value={v.title.trim()} />
        <ReviewRow label="용도" value={<FirstLine text={v.purpose} />} />
      </ReviewCard>
      <ReviewCard icon={<FolderKanban className="size-4 text-brand-indigo-600" />} title="프로젝트 · 템플릿"
        onView={() => setView("source")} onEdit={edit("source")}>
        <ReviewRow label="프로젝트" value={v.projectName} mono />
        <ReviewRow label="템플릿" value={v.image?.name ?? v.imageName} mono />
        <ReviewRow label="이미지 형식" value={v.image && imageLine(v.image)} mono />
      </ReviewCard>
      <ReviewCard icon={serverIcon} title="가상서버" onView={() => setView("vm")} onEdit={edit("vm")}>
        <ReviewRow label="가상서버명" value={v.vmName} mono />
        <ReviewRow label="네트워크(희망)" value={net?.label ?? v.networkType} />
        <ReviewRow label="Floating IP" value={v.floatingIp ? "희망" : "희망 안 함"} />
        <ReviewRow label="스펙" value={v.flavorName} mono sub={`${v.vcpus ?? "—"} vCPU / ${ram} / ${v.diskLabel}`} />
        <ReviewRow label="가속기" value={<AccelBadges a={v.accel} />} />
        <ReviewRow label="키페어" value={keyValue} mono={!!v.keypair && v.cloudInit} />
      </ReviewCard>

      <ViewDialog title="신청 정보" open={view === "info"} onClose={() => setView(null)}>
        <RequestInfoView title={v.title} purpose={v.purpose} />
      </ViewDialog>
      <ViewDialog title="프로젝트 · 템플릿 상세" open={view === "source"} onClose={() => setView(null)}>
        <DetailGroups groups={[
          { title: "프로젝트", rows: [
            ["프로젝트", v.projectName], ["조직", v.orgName || "—"], ...quotaLeftRows(tenants?.find((t) => t.id === v.projectId)),
          ] },
          { title: "템플릿", rows: [
            ["이미지", v.image?.name ?? v.imageName], ["형식", v.image && imageFormat(v.image)],
            ["OS", v.image && (v.image.os || "OS 정보 없음")], ["OS 버전", v.image?.osVersion],
            ["최소 디스크", v.image && `${v.image.minVolumeGb} GB`], ["cloud-init", v.cloudInit ? "있음" : "없음 (키페어 미적용)"],
          ] },
        ]} />
      </ViewDialog>
      <ViewDialog title="가상서버 상세" open={view === "vm"} onClose={() => setView(null)}>
        <DetailGroups groups={[
          { title: "가상서버", rows: [["가상서버명", v.vmName], ["생성 개수", "1 대"]] },
          { title: "네트워크", rows: [
            ["네트워크(희망)", net ? `${net.label} (${net.sublabel})` : v.networkType],
            ["Floating IP", v.floatingIp ? "희망" : "희망 안 함"],
          ] },
          { title: "스펙", rows: [
            ["스펙", v.flavorName], ["vCPU", v.vcpus != null ? `${v.vcpus}` : "—"], ["RAM", ram], ["Disk", v.diskLabel],
            ["가속기", accelText(v.accel, 1)],
          ] },
          { title: "접속", rows: [["키페어", keyValue]] },
        ]} />
      </ViewDialog>
    </div>
  );
}

// ── 서비스 클러스터 ─────────────────────────────────────────────────

/** 노드 — 수 · 스펙 이름 · vCPU · RAM(GB) · Disk(GB). 스펙을 이 프로젝트에서 못 찾으면 Disk 등이 비어 있다. */
export interface NodeView { count: number; name?: string; vcpus?: number; ramGb?: number; diskGb?: number }

export const nodeView = (count: number, f: FlavorSummary): NodeView =>
  ({ count, name: f.name, vcpus: f.vcpus, ramGb: f.ramMb / 1024, diskGb: f.diskGb });

// 카드 스펙 줄 — 예) "4 vCPU / 8 GB RAM / 80 GB Disk"(아는 값만)
const nodeLine = (n: NodeView) => [n.vcpus != null && `${n.vcpus} vCPU`, n.ramGb != null && `${n.ramGb} GB RAM`,
  n.diskGb != null && `${n.diskGb} GB Disk`].filter(Boolean).join(" / ") || undefined;

export interface ClusterReviewData {
  title: string; purpose: string;
  projectId: string; projectName?: string;
  clusterName: string; templateName?: string; kubeVersion?: string;
  namespace?: string; // 함께 만들 프로젝트 네임스페이스
  master: NodeView; worker: NodeView; workerAccel: AccelInfo;
  keypair?: string; keyFromTemplate: boolean;
  templateRows: Rows; // 템플릿 기본값(templateDefaultRows)
}

/** 템플릿 기본값 — 스펙 이름(이름 또는 ID), Worker 가속기, 노드 이미지 이름, 라벨(대시보드 · GPU Operator — 없으면 '—'), 키페어. */
export function templateDefaultRows(t: ClusterTemplate | undefined, flavors: FlavorSummary[], images: { id: string; name: string }[],
  modelOf: (dp: string | null | undefined) => string | null): Rows {
  if (!t) return [];
  const w = findFlavor(flavors, t.flavor_id);
  const flavorName = (ref: string | null) => findFlavor(flavors, ref)?.name ?? ref ?? "—";
  const onOff = (key: string) => {
    const v = t.labels?.[key];
    return v === undefined ? "—" : v.toLowerCase() === "true" ? "사용" : "없음";
  };
  return [
    ["Control Plane", flavorName(t.master_flavor_id)], ["Worker", flavorName(t.flavor_id)],
    ["가속기", w ? accelTotal(w, 1, modelOf(w.accelerator)) : "확인 불가"],
    ["노드 이미지", images.find((i) => i.id === t.image_id || i.name === t.image_id)?.name ?? t.image_id ?? "—"],
    ["대시보드", onOff("kube_dashboard_enabled")], ["GPU Operator", onOff("nvidia_gpu_operator_enabled")],
    ["키페어", t.keypair_id || "없음"],
  ];
}

/** 서비스 클러스터 신청 내용 카드(신청 정보 · 클러스터 · 노드) + 보기 창(템플릿 기본값은 클러스터 보기).
 *  onEdit 이 있으면 '수정'(신청 단계 — 그 단계로). */
export function ClusterCards({ c, onEdit }: { c: ClusterReviewData; onEdit?: (card: "info" | "cluster" | "nodes") => void }) {
  const [view, setView] = React.useState<"info" | "cluster" | "nodes" | null>(null);
  const { master: m, worker: w } = c;
  const kube = c.kubeVersion ? `v${c.kubeVersion}` : "버전 정보 없음";
  const keyNode = (
    <span className="inline-flex items-center gap-1.5">
      {c.keyFromTemplate && c.keypair && <RefTag label="템플릿" />}
      <span className={c.keypair ? "font-mono" : "text-muted-foreground"}>{c.keypair ?? "사용 안 함"}</span>
    </span>
  );
  const edit = (k: "info" | "cluster" | "nodes") => (onEdit ? () => onEdit(k) : undefined);
  return (
    <div className="flex min-w-0 flex-none flex-col gap-2">
      <ReviewCard icon={infoIcon} title="신청 정보" onView={() => setView("info")} onEdit={edit("info")}>
        <ReviewRow label="신청 제목" value={c.title.trim()} />
        <ReviewRow label="용도" value={<FirstLine text={c.purpose} />} />
      </ReviewCard>
      <ReviewCard icon={<Boxes className="size-4 text-brand-indigo-600" />} title="클러스터" onView={() => setView("cluster")} onEdit={edit("cluster")}>
        <ReviewRow label="프로젝트" value={c.projectName} mono />
        <ReviewRow label="클러스터 이름" value={c.clusterName} mono />
        <ReviewRow label="템플릿" value={c.templateName} mono />
        <ReviewRow label="Kubernetes" value={kube} mono={!!c.kubeVersion} />
        <InfoRow label="네임스페이스">
          <span className="inline-flex items-center gap-1.5"><NewTag /><span className="font-mono">{c.namespace || "—"}</span></span>
        </InfoRow>
      </ReviewCard>
      <ReviewCard icon={serverIcon} title="노드" onView={() => setView("nodes")} onEdit={edit("nodes")}>
        <ReviewRow label="Control Plane" value={`${m.count} 대 × ${m.name ?? "—"}`} mono sub={nodeLine(m)} />
        <ReviewRow label="Worker" value={`${w.count} 대 × ${w.name ?? "—"}`} mono sub={nodeLine(w)} />
        <ReviewRow label="Worker 가속기" value={<AccelBadges a={c.workerAccel} />} />
        <InfoRow label="키페어">{keyNode}</InfoRow>
      </ReviewCard>

      <ViewDialog title="신청 정보" open={view === "info"} onClose={() => setView(null)}>
        <RequestInfoView title={c.title} purpose={c.purpose} />
      </ViewDialog>
      <ViewDialog title="클러스터 상세" open={view === "cluster"} onClose={() => setView(null)}>
        <DetailGroups groups={[
          { title: "클러스터", rows: [
            ["프로젝트", c.projectName], ["클러스터 이름", c.clusterName], ["템플릿", c.templateName], ["Kubernetes", kube],
            ["네임스페이스", c.namespace],
          ] },
          { title: "템플릿 기본값", rows: c.templateRows },
        ]} />
      </ViewDialog>
      <ViewDialog title="노드 상세" open={view === "nodes"} onClose={() => setView(null)}>
        <DetailGroups groups={[
          ...([["Control Plane", m, null], ["Worker", w, c.workerAccel]] as const).map(([role, n, a]) => ({
            title: role, rows: [
              ["노드 수", `${n.count} 대`], ["스펙", n.name],
              ["vCPU / RAM / Disk", `${n.vcpus ?? "—"} vCPU / ${n.ramGb ?? "—"} GB / ${n.diskGb ?? "—"} GB`],
              ["가속기", a ? accelText(a, 1) : "없음"],
            ] as Rows,
          })),
          { title: "접속", rows: [["키페어", keyNode]] },
        ]} />
      </ViewDialog>
    </div>
  );
}

// ── 신청 상세 데이터 ─────────────────────────────────────────────

/** 상태별 쿼터 — 대기 · 승인됨: 사용 → 신청 후. 반려 · 실패: 지금 기준으로 다시 신청 · 재시도하면(실패한 회차의 자원이 정리되지
 *  않았으면 '사용'에 이미 들어 있을 수 있어 경고). 진행 중 · 완료: 넣지 않는다(일부만 만들어졌거나 이미 '사용'에 들어 있음). */
export function quotaView(r: ResourceRequest): QuotaView | null {
  const left = !!r.result && Object.values(r.result).some(Boolean); // 실패 — 정리하지 못한 자원 ID 가 남음
  switch (r.status) {
    case "pending": case "approved": return {};
    case "rejected": return { note: "지금 기준" };
    case "failed": return { note: "지금 기준",
      warn: left ? "정리되지 않은 자원이 있어 이 신청분이 '사용'에 이미 들어 있을 수 있습니다" : undefined };
    default: return null;
  }
}

/** 가상머신에 넣는 키페어 — 관리자가 바꿨으면 그 값(null = 넣지 않음), 아니면 신청 값. cloud-init 이 없으면 없음(undefined). */
export const vmAllocKey = (r: ResourceRequest) =>
  (r.cloudInit === false ? null : r.provisionSpec && "novaKeypair" in r.provisionSpec ? r.provisionSpec.novaKeypair : r.keypairName) || undefined;

/** 신청 저장 값 → 가상머신 신청 내용 데이터 + 실제 할당 값(관리자가 고른 값, 완료면 받은 값 — IP · Floating IP 주소).
 *  이미지(형식 · OS · 최소 디스크)는 라이브 조회. 실제 할당 값은 프로비저닝 설정이 있고 반려가 아닐 때만. */
export function useVmReviewData(r: ResourceRequest): { v: VmReviewData; alloc: Rows | null } {
  const modelOf = useAccelModel();
  const { data: images = [] } = useProjectImages(r.projectId);
  const p = r.provisionSpec;
  const done = r.status === "completed" ? r.result : undefined;
  return {
    v: {
      title: r.title ?? "", purpose: r.purpose ?? "",
      projectId: r.projectId, projectName: r.projectName, orgName: r.orgName,
      imageName: r.imageName, image: images.find((i) => i.id === r.imageId), cloudInit: r.cloudInit !== false,
      vmName: r.vmName ?? "", networkType: r.networkType, floatingIp: !!r.floatingIp,
      flavorName: r.flavorName, vcpus: r.vcpu, ramGb: r.ramGb,
      diskGb: r.diskGb || r.volumeGb || 0, diskLabel: r.diskGb ? `${r.diskGb} GB` : r.volumeGb ? `부팅 볼륨 ${r.volumeGb} GB` : "—",
      accel: requestAccel(r, modelOf), keypair: r.keypairName ?? r.novaKeypair,
    },
    alloc: p && r.status !== "rejected" ? [
      ["네트워크", <span key="n" className="inline-flex items-center gap-1.5">
        <NetBadge type={p.networkKind === "external" ? "internet" : "internal"} />{p.networkName}</span>],
      ["보안그룹", p.securityGroups.join(", ")],
      ["IP", done?.fixedIp ? `${done.fixedIp} (${p.fixedIp ? "고정" : "DHCP"})` : p.fixedIp ? `고정 IP ${p.fixedIp}` : "DHCP 자동 할당"],
      ["Floating IP", p.networkKind === "external" ? "해당 없음"
        : done ? (done.floatingIp ? `${done.floatingIp}${p.floatingNetworkName ? ` (${p.floatingNetworkName})` : ""}` : "없음")
        : p.floatingNetworkName ? `연결 (${p.floatingNetworkName})` : "연결 안 함"],
      ["배치 노드", p.host ?? "스케줄러가 고름"],
      ["키페어", vmAllocKey(r)],
    ] : null,
  };
}

/** 신청 저장 값 → 서비스 클러스터 신청 내용 데이터 + 실제 할당 값(관리자가 고른 망 · 조정한 노드, 완료면 받은 값 — API 주소 ·
 *  클러스터 ID · 네임스페이스). 노드 Disk · 템플릿 기본값은 라이브 조회(스펙 · 템플릿 · 이미지). */
export function useClusterReviewData(r: ResourceRequest): { c: ClusterReviewData; alloc: Rows | null } {
  const modelOf = useAccelModel();
  const { data: flavors = [] } = useProjectFlavors(r.projectId);
  const { data: images = [] } = useProjectImages(r.projectId);
  const { data: template } = useClusterTemplate(r.templateId ?? null);
  const mf = flavors.find((f) => f.id === r.masterFlavorId);
  const wf = flavors.find((f) => f.id === r.flavorId);
  // 키페어 — 신청자 SSH 키 · OpenStack 키페어, 둘 다 없으면 템플릿 키페어
  const own = r.keypairId ? r.keypairName ?? r.keypairId : r.novaKeypair;
  const cp = r.provisionSpec as unknown as ClusterProvisionSpec | undefined; // 클러스터 신청은 provision_spec 형태가 다르다
  const done = r.status === "completed" ? r.result : undefined;
  // 신청 값과 다르면 '변경'
  const changedNode = (count: number, name: string, changed: boolean) => (
    <span className="inline-flex items-center gap-1.5">{changed && <ChangedTag />}{count} 대 × {name}</span>
  );
  return {
    c: {
      title: r.title ?? "", purpose: r.purpose ?? "",
      projectId: r.projectId, projectName: r.projectName,
      clusterName: r.clusterName ?? "", templateName: r.templateName, kubeVersion: r.kubeVersion ?? r.k8sVersion,
      namespace: r.result?.namespace ?? r.namespace,
      master: { count: r.masterCount ?? 1, name: r.masterFlavorName ?? mf?.name, vcpus: r.masterVcpu ?? mf?.vcpus,
        ramGb: r.masterRamGb ?? (mf && mf.ramMb / 1024), diskGb: mf?.diskGb },
      worker: { count: r.nodeCount ?? 1, name: r.flavorName ?? wf?.name, vcpus: r.vcpu ?? wf?.vcpus,
        ramGb: r.ramGb ?? (wf && wf.ramMb / 1024), diskGb: wf?.diskGb },
      workerAccel: requestAccel(r, modelOf),
      keypair: own ?? (template?.keypair_id || undefined), keyFromTemplate: !own && !!template?.keypair_id,
      templateRows: templateDefaultRows(template, flavors, images, modelOf),
    },
    alloc: cp && r.status !== "rejected" ? [
      ["네트워크", cp.networkName], ["서브넷", cp.subnetCidr],
      ["Control Plane", changedNode(cp.masterCount, cp.masterFlavorName,
        cp.masterCount !== r.masterCount || cp.masterFlavorId !== r.masterFlavorId)],
      ["Worker", changedNode(cp.nodeCount, cp.flavorName, cp.nodeCount !== r.nodeCount || cp.flavorId !== r.flavorId)],
      // 넣은 키페어 — 설정 때 정한 값(예전 설정은 신청 값), 없으면 템플릿 키페어(템플릿에도 없으면 '—')
      ["키페어", ("keypairName" in cp ? cp.keypairName : r.keypairName) ?? (template?.keypair_id ? "템플릿 키페어" : undefined)],
      ...(done ? [["API 주소", done.apiAddress], ["클러스터 ID", done.clusterId], ["네임스페이스", done.namespace ?? r.namespace]] as Rows : []),
    ] : null,
  };
}
