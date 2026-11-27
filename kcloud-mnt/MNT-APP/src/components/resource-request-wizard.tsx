"use client";
/**
 * 자원 신청 모달(시안 Stepper D) — 1180×736 스테퍼. 첫 화면에서 유형(가상머신 · 서비스 클러스터 · AI 워크스페이스)을 고르고
 * 4단계(기본 정보 → 프로젝트·템플릿 → 구성 → 검토·제출)로 작성한다. AI 워크스페이스는 스테퍼 대상이 아니라 기존 신청 화면으로.
 *  - 왼쪽 스테퍼: 단계별 입력 요약, 지금 단계에 빠진 필수 항목('선택 필요 - 항목', 누르면 그 위치로). 끝난 단계는 눌러서 돌아간다.
 *  - 01 · 02 는 '다음 단계'를 눌렀을 때만 빨간 표시, 03 은 늘. '다음 단계'는 막혀도 눌리고 막힌 이유를 옆에 보인다.
 *  - 오른쪽 요약: 입력 정보 · 총 자원 · 프로젝트 쿼터(사용 → 신청 후). 접으면 본문이 넓어진다(보는 사람마다 기억).
 *  - 닫기: 입력이 있으면 확인 창. 제출하면 알림 + 창 안 완료 화면.
 * 입력 상태 · 검사 · 제출 값은 서버 확인 규칙과 같다 — 화면은 식별자만 보내고 서버가 라이브 조회로 다시 확인해 저장한다.
 * 티켓 관련 조회 · 제출은 목업으로 폴백하지 않는다 — 실패하면 오류를 보여준다.
 */
import * as React from "react";
import {
  AlertTriangle, Boxes, BrainCircuit, CheckCircle2, ChevronLeft, Copy, Globe, HardDrive, Network, Wifi,
} from "lucide-react";
import { useRouter, usePathname } from "next/navigation";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth";
import { useNovaKeypairs, useProjectFlavors, useProjectImages, useProjectQuotas, type FlavorSummary, type ProjectImage } from "@/lib/iaas-api";
import { useClusterTemplates, type ClusterTemplate } from "@/lib/paas-api";
import {
  projectsLoadingText, useClusterNameCheck, useCreateClusterTicket, useCreateTicket, useTicketProjects, useVmNameCheck,
  type TicketProject,
} from "@/lib/ticket-api";
import { useSshKeys, type SshKey } from "@/lib/sshkey-api";
import { WorkspaceRequestForm } from "@/components/workspace-requests";
import {
  CLUSTER_FLOW, ClusterCards, FlowLine, QuotaBars, USER_NETWORKS, VM_FLOW, VmCards, diskQuota, flavorAccel, imageFormat, imageOs,
  nodeView, quotaLeft, showLeft, templateDefaultRows,
} from "@/components/request-review";
import {
  AccelTags, ClusterNodeCards, EMPTY_NODE_CONFIG, KeypairTable, Pager, Radio, SearchInput, Segment, SpecPicker, TotalAccel,
  accelChoices, accelGroup, effectiveNodes, keyIcon, keyType, nodeConfigMissing, nodeTotals, nodesValid, paginate, serverIcon,
  useAccelModel, workerFlavorBlock, type ClusterNodeConfig,
} from "@/components/cluster-node-fields";
import {
  FormCard, LeftStepper, STEPPER_CONTENT, StatusPill, StepFooter, StepMain, StepperGrid, StepperHeader, FloatProgress, SumSection,
  useCardScroll, useCloseGuard, type StepItem,
} from "@/components/request-stepper";
import { cn, formatDateTime } from "@/lib/utils";

// 부팅 볼륨 최대 크기 (GB)
const MAX_VOLUME_GB = 500;

// VM 이름 = 서버 이름 = hostname. Nova hostname 규칙(^[a-zA-Z0-9]+[a-zA-Z0-9-]*[a-zA-Z0-9]+$, 2~63자)에서
// 영문은 소문자만 허용. 생성 시 hostname 에도 같은 값을 지정한다(microversion 2.90+).
const VM_NAME_RE = /^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$/;
// 클러스터 이름 — MNT-PAAS·MNT-CORE 와 같은 규칙(영문 소문자로 시작, 소문자·숫자·하이픈, 하이픈으로 끝나지 않음, 40자 이내)
const CLUSTER_NAME_RE = /^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$/;
const PURPOSE_MAX = 300;
const TITLE_MAX = 60; // 신청 제목 — 목록·알림에 한 줄로

// 신청 유형 — 첫 화면의 선택 카드
const REQUEST_TYPES = [
  { key: "vm", badge: "VM", title: "가상머신 신청", desc: "단일 가상머신(VM)을 생성합니다.", flow: "기본 정보 → 프로젝트·템플릿 → 가상머신 구성 → 검토·제출" },
  { key: "cluster", badge: "K8s", title: "서비스 클러스터 신청", desc: "쿠버네티스 서비스 클러스터를 생성합니다.", flow: "기본 정보 → 프로젝트·템플릿 → 노드 구성 → 검토·제출" },
  { key: "ai-workspace", badge: "AI", title: "AI 워크스페이스 신청", desc: "프로젝트 전용 AI 워크스페이스를 생성합니다.", flow: "지금 신청 화면(한 장)으로 이어집니다" },
] as const;

type RequestTypeKey = typeof REQUEST_TYPES[number]["key"];

/** 작성 폼 공통 — 입력 여부(닫기 확인) · 닫기 · 유형 바꾸기 · 제출 뒤 닫기. */
interface FormProps {
  onDirty: (dirty: boolean) => void;
  onClose: () => void;      // 확인 창을 거치는 닫기
  onChangeType: () => void; // 머리의 '유형 바꾸기'
  onDone: () => void;       // 바로 닫기(제출 완료 화면에서)
  onSubmitted: () => void;  // 제출 완료 — 이후 Esc · 바깥 클릭은 유형 선택이 아니라 닫기
}

/**
 * 자원 신청 모달 — 유형을 고르기 전에는 유형 선택 화면, 고르면 그 유형의 스테퍼. 다시 열면 늘 유형 선택부터.
 * 각 폼은 유형이 바뀌거나 닫히면 언마운트되어 입력값이 초기화된다.
 */
export function ResourceRequestWizard({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [type, setType] = React.useState<RequestTypeKey | null>(null);
  const [pick, setPick] = React.useState<RequestTypeKey | null>(null);
  const [dirty, setDirty] = React.useState(false);
  const [askType, setAskType] = React.useState(false);
  const [sent, setSent] = React.useState(false); // 제출 완료 화면

  function close() {
    onClose();
    setType(null);
    setPick(null);
    setDirty(false);
    setAskType(false);
    setSent(false);
  }
  // 유형을 고른 뒤 아무것도 입력하지 않았으면 Esc · 바깥 클릭은 유형 선택으로 돌아간다(제출 뒤에는 닫기)
  const guard = useCloseGuard(dirty, close, { onDismiss: type && !sent ? toTypeSelect : undefined });
  function toTypeSelect() {
    setAskType(false);
    setDirty(false);
    setType(null);
  }
  const form = {
    onDirty: setDirty, onClose: guard.requestClose, onChangeType: () => (dirty ? setAskType(true) : toTypeSelect()), onDone: close,
    onSubmitted: () => setSent(true),
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && guard.requestClose()}>
      {type === "ai-workspace" ? (
        // AI 워크스페이스 = 프로젝트 1:1 (Kubeflow Profile) — 승인 후 관리자가 생성 시작. 스테퍼 대상이 아니라 지금 신청 화면 그대로
        // (Esc · 바깥 클릭 · 닫기 확인은 스테퍼 화면과 같다)
        <DialogContent className="max-h-[90vh] max-w-3xl overflow-y-auto" aria-describedby={undefined} {...guard.contentProps}>
          <DialogHeader><DialogTitle>AI 워크스페이스 자원 신청</DialogTitle></DialogHeader>
          <button type="button" onClick={toTypeSelect}
            className="inline-flex cursor-pointer items-center gap-1 self-start text-xs text-muted-foreground transition-colors hover:text-foreground">
            <ChevronLeft className="size-3.5" /> 유형 다시 선택
          </button>
          <WorkspaceRequestForm onDone={close} onDirty={setDirty} />
          {guard.confirm}
        </DialogContent>
      ) : (
        <DialogContent hideClose className={STEPPER_CONTENT} {...guard.contentProps} onOpenAutoFocus={(e) => e.preventDefault()}>
          {type === null && <TypeSelect pick={pick} onPick={setPick} onNext={() => pick && setType(pick)} onClose={guard.requestClose} />}
          {type === "vm" && <VmRequestForm {...form} />}
          {type === "cluster" && <ClusterRequestForm {...form} />}
          {guard.confirm}
          <Dialog open={askType} onOpenChange={setAskType}>
            <DialogContent hideClose className="max-w-[400px] gap-0 p-0">
              <div className="flex flex-col gap-1.5 px-5 pt-[18px] pb-4">
                <DialogTitle className="text-base font-extrabold">유형 바꾸기</DialogTitle>
                <DialogDescription className="text-[13px] text-slate-700">유형을 바꾸면 입력한 내용이 초기화됩니다.</DialogDescription>
              </div>
              <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
                <Button variant="outline" onClick={() => setAskType(false)}>취소</Button>
                <Button onClick={toTypeSelect}>초기화하고 바꾸기</Button>
              </div>
            </DialogContent>
          </Dialog>
        </DialogContent>
      )}
    </Dialog>
  );
}

/** 머리 오른쪽 — '유형 · 가상머신 | 유형 바꾸기'. */
function TypeSwitch({ label, onChange }: { label: string; onChange: () => void }) {
  return (
    <>
      <span className="text-muted-foreground">유형 · <b className="text-foreground">{label}</b></span>
      <button type="button" onClick={onChange} className="cursor-pointer font-semibold text-brand-indigo-400 hover:underline">유형 바꾸기</button>
    </>
  );
}

// ── 유형 선택 ─────────────────────────────────────────────────────

function TypeSelect({ pick, onPick, onNext, onClose }: {
  pick: RequestTypeKey | null; onPick: (t: RequestTypeKey) => void; onNext: () => void; onClose: () => void;
}) {
  const [tried, setTried] = React.useState(false);
  const steps: StepItem[] = [
    { no: "01", name: "기본 정보", desc: "유형을 고르면 시작", state: "todo" },
    { no: "02", name: "프로젝트·템플릿", state: "todo" },
    { no: "03", name: "구성", state: "todo" },
    { no: "04", name: "검토·제출", state: "todo" },
  ];
  return (
    <StepperHeader kind="NEW" title="자원 신청" onClose={onClose}>
      <StepperGrid left={<LeftStepper steps={steps} note="유형을 고르면 단계가 열립니다. 작성 중에도 머리의 '유형 바꾸기'로 바꿀 수 있습니다." />}>
        <StepMain title="어떤 자원을 신청할까요?" desc="신청할 자원 유형을 고르세요."
          footer={
            <StepFooter status={tried && !pick ? "유형을 선택해야 넘어갈 수 있습니다" : undefined} tone="bad">
              <Button variant="outline" onClick={onClose}>취소</Button>
              <Button onClick={() => (pick ? onNext() : setTried(true))} className={cn(!pick && tried && "opacity-60")}>다음 단계</Button>
            </StepFooter>
          }>
          <div className="grid flex-none grid-cols-3 gap-3.5">
            {REQUEST_TYPES.map((t) => {
              const on = pick === t.key;
              return (
                <button key={t.key} type="button" onClick={() => onPick(t.key)} onDoubleClick={() => { onPick(t.key); onNext(); }}
                  className={cn("flex cursor-pointer flex-col gap-2 rounded-xl border-[1.5px] p-[18px] text-left transition-colors",
                    on ? "border-primary! bg-[#f5f7ff]" : "border-border bg-card hover:bg-secondary/40")}>
                  <span className="flex items-center gap-2">
                    <span className="rounded-[5px] bg-brand-indigo-50 px-1.5 py-1 font-mono text-[11px] leading-none font-semibold text-primary">{t.badge}</span>
                    <span className="flex-1" />
                    <Radio on={on} />
                  </span>
                  <span className="text-base font-extrabold">{t.title}</span>
                  <span className="text-xs text-muted-foreground">{t.desc}</span>
                  <span className="mt-auto border-t border-dashed border-border pt-2 text-[11px] text-muted-foreground">{t.flow}</span>
                </button>
              );
            })}
          </div>
          <div className="flex flex-none gap-2.5 rounded-[10px] border border-brand-indigo-200! bg-[#f5f7ff] px-3.5 py-3 text-xs text-brand-indigo-900">
            <BrainCircuit className="size-4 flex-none text-primary" />
            가상머신·서비스 클러스터는 이 창에서 4단계로 작성합니다. AI 워크스페이스는 지금 신청 화면으로 이어집니다.
          </div>
        </StepMain>
      </StepperGrid>
    </StepperHeader>
  );
}

// ── 공통 조각 ─────────────────────────────────────────────────────

// 신청 키페어 목록 — 사용자 = 본인 SSH 키(포털 DB), 관리자 = OpenStack(Nova) 키페어(식별자가 이름이라 id 에 이름).
// 제출 때 사용자는 keypairId, 관리자는 novaKeypair 로 보낸다.
function useRequestKeypairs() {
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";
  const mine = useSshKeys(isAdmin ? undefined : user?.username);
  const nova = useNovaKeypairs(isAdmin);
  if (!isAdmin) return { isAdmin, data: mine.data, isLoading: mine.isLoading, error: mine.error };
  const data = nova.data?.map((k): SshKey => ({
    id: k.name, name: k.name, publicKey: k.publicKey, fingerprint: k.fingerprint, createdAt: "", vmCount: 0,
  }));
  return { isAdmin, data, isLoading: nova.isLoading, error: nova.error };
}

/** 이름 입력 후 0.4초 멈추면 확인 — 형식 검사를 통과한 이름만. */
function useDebounced(value: string) {
  const [v, setV] = React.useState("");
  React.useEffect(() => {
    const t = setTimeout(() => setV(value), 400);
    return () => clearTimeout(t);
  }, [value]);
  return v;
}

/** 단계 이름 · 왼쪽 스테퍼 · '다음 단계' 막힘 — 두 폼이 같은 규칙으로 쓴다(시안: 01 · 02 는 눌렀을 때만 빨간 표시, 03 은 늘). */
function useWizardSteps(count: number) {
  const [step, setStep] = React.useState(0);
  const [tried, setTried] = React.useState<boolean[]>(() => Array(count).fill(false));
  const show = (i: number) => i === 2 || tried[i];
  return { step, setStep, tried, setTried: (i: number) => setTried((t) => t.map((x, k) => (k === i ? true : x))), show };
}

function TitleField({ title, onTitle, placeholder, missing, cardRef }: {
  title: string; onTitle: (v: string) => void; placeholder: string; missing: boolean; cardRef: (el: HTMLDivElement | null) => void;
}) {
  return (
    <FieldBlock ref={cardRef} label="신청 제목" required missing={missing} ask="제목을 입력하세요">
      <Input id="req-title" placeholder={placeholder} value={title} maxLength={TITLE_MAX} onChange={(e) => onTitle(e.target.value)}
        className={cn("h-9 bg-card", missing && "border-[#f1aca5]!")} />
      <div className="flex justify-between text-xs text-muted-foreground">
        <span>신청 목록과 관리자 알림에 이 제목이 표시됩니다</span>
        <span className="font-mono">{title.length} / {TITLE_MAX}</span>
      </div>
    </FieldBlock>
  );
}

function PurposeField({ purpose, onPurpose, placeholder }: { purpose: string; onPurpose: (v: string) => void; placeholder: string }) {
  return (
    <FieldBlock label="용도" hint="옵션 · 관리자가 승인할 때 참고합니다">
      <textarea id="req-purpose" rows={5} placeholder={placeholder} value={purpose} maxLength={PURPOSE_MAX} onChange={(e) => onPurpose(e.target.value)}
        className="w-full rounded-md border border-input bg-card px-3 py-2 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring" />
      <p className="text-right font-mono text-xs text-muted-foreground">{purpose.length} / {PURPOSE_MAX}</p>
    </FieldBlock>
  );
}

/**
 * 입력 묶음(카드 테두리 없는 넓은 영역) — 제목 + 안내 + 오른쪽(검색 등). missing 이면 빨간 테두리로 감싸고 제목 줄만 빨갛게(내용은 흰 바탕),
 * 제목 줄 오른쪽에 빨간 안내 ask(입력 카드와 같은 모양).
 */
const FieldBlock = React.forwardRef<HTMLDivElement, {
  label: string; required?: boolean; hint?: React.ReactNode; aside?: React.ReactNode; missing?: boolean; ask?: string; children: React.ReactNode;
}>(function FieldBlock({ label, required, hint, aside, missing, ask, children }, ref) {
  return (
    <div ref={ref} className={cn("flex flex-none flex-col rounded-xl",
      missing && "-mx-3.5 overflow-hidden border-[1.5px] border-[#f1aca5]! bg-card")}>
      <div className={cn("flex min-h-7 items-center gap-2", missing && "border-b border-[#f6d0cb]! bg-[#fff5f4] px-3.5 py-2.5")}>
        <span className={cn("flex-none text-[15px] font-bold", missing && "text-[#b42318]")}>
          {label}{required && <span className="ml-0.5 text-[#d92d20]">*</span>}
        </span>
        {hint && <span className="min-w-0 text-xs text-muted-foreground">{hint}</span>}
        <span className="flex-1" />
        {missing && ask && <span className="flex-none text-[13px] font-semibold text-[#d92d20]">{ask}</span>}
        {aside}
      </div>
      <div className={cn("flex flex-col gap-2.5 pt-2.5", missing && "px-3.5 pb-3")}>{children}</div>
    </div>
  );
});

/** 이름 카드(가상서버명 · 클러스터명) — 입력 + 형식 · 중복 확인 문구. */
function NameField({ cardRef, icon, title, value, onChange, placeholder, maxLength, status, children }: {
  cardRef: (el: HTMLDivElement | null) => void; icon: React.ReactNode; title: string; value: string; onChange: (v: string) => void;
  placeholder: string; maxLength: number; status?: "done" | "missing"; children: React.ReactNode;
}) {
  return (
    <FormCard ref={cardRef} icon={icon} title={title} required status={status} ask={value ? "이름을 확인하세요" : "이름을 입력하세요"}>
      <Input value={value} onChange={(e) => onChange(e.target.value.trim())} placeholder={placeholder} maxLength={maxLength}
        className={cn("h-9 bg-card font-mono text-[13px]", status === "done" && "border-brand-mint-500!")} />
      {children}
    </FormCard>
  );
}

/** 이름 확인 문구 — 형식 → 확인 중 → 실패(다시 확인) → 사용 가능 / 사용 불가. */
function NameCheckLine({ value, formatOk, hint, formatError, ready, checking, failed, error, recheck, available, reason }: {
  value: string; formatOk: boolean; hint: string; formatError: string; ready: boolean; checking: boolean; failed: boolean;
  error?: string; recheck: () => void; available?: boolean; reason?: string | null;
}) {
  if (!value) return <p className="text-xs text-muted-foreground">{hint}</p>;
  if (!formatOk) return <p className="text-xs text-rose-500">{formatError}</p>;
  if (!ready || checking) return <p className="text-xs text-muted-foreground">이름 중복 확인 중…</p>;
  if (failed) {
    return (
      <p className="flex items-center gap-2 text-xs text-rose-500">
        이름 중복 확인에 실패했습니다{error ? ` (${error})` : ""}.
        <button type="button" className="font-medium underline" onClick={recheck}>다시 확인</button>
      </p>
    );
  }
  return available ? <p className="text-xs text-brand-mint-700">사용 가능한 이름입니다.</p>
    : <p className="text-xs text-rose-500">{reason ?? "사용할 수 없는 이름입니다."}</p>;
}

function LoadError({ what, message }: { what: string; message: string }) {
  return (
    <div className="flex gap-2.5 rounded-lg border border-rose-200 bg-rose-50 p-3">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-rose-600" />
      <p className="text-xs text-rose-800">{what}을(를) 불러오지 못했습니다 ({message}). 잠시 후 다시 시도하세요.</p>
    </div>
  );
}

/** 프로젝트를 고르기 전 자리 표시. */
function NeedProject({ what }: { what: string }) {
  return <div className="rounded-[10px] border border-dashed p-6 text-center text-[13px] text-muted-foreground">프로젝트를 먼저 고르면 {what}을(를) 불러옵니다</div>;
}

/** '선택 전'(필수인데 아직 고르지 않음) — 요약 값. */
const NOT_YET = <span className="text-brand-indigo-300">선택 전</span>;
const mono = (v?: string | null) => (v ? <span className="font-mono">{v}</span> : undefined);

/** 바닥 '다음 단계' — 막혔으면 눌러도 넘어가지 않고 이유를 보인다(blocked 면 옅게). */
function NextButton({ blocked, onClick, children = "다음 단계", pending }: {
  blocked: boolean; onClick: () => void; children?: React.ReactNode; pending?: boolean;
}) {
  return (
    <Button onClick={onClick} disabled={pending} className={cn("min-w-[84px]", blocked && "bg-brand-indigo-300 hover:bg-brand-indigo-300")}>{children}</Button>
  );
}

/** 제출 완료 화면 — 신청 ID 복사 · 안내 · 닫기 / 신청 목록에서 보기. */
function SubmittedView({ name, id, onClose, onList }: { name: string; id: string; onClose: () => void; onList: () => void }) {
  return (
    <StepMain title="신청이 접수되었습니다"
      footer={<StepFooter><Button variant="outline" onClick={onClose}>닫기</Button><Button onClick={onList}>신청 목록에서 보기 ›</Button></StepFooter>}>
      <div className="flex flex-col items-center gap-2.5 py-10 text-center">
        <CheckCircle2 className="size-14 fill-brand-mint-500 text-white" />
        <div className="text-xl font-extrabold [overflow-wrap:anywhere]">{name} 신청 완료</div>
        <button type="button" title="클릭해서 신청 ID 복사"
          onClick={() => { navigator.clipboard.writeText(id); toast.success("신청 ID 복사됨", { description: id }); }}
          className="inline-flex cursor-pointer items-center gap-1.5 rounded-md px-1.5 py-0.5 font-mono text-[13px] text-muted-foreground hover:bg-brand-indigo-50 hover:text-primary">
          {id}<Copy className="size-3" />
        </button>
        <p className="text-[13px] text-muted-foreground">관리자 승인과 프로비저닝 진행 상황은 신청 목록에서 확인할 수 있습니다.</p>
      </div>
    </StepMain>
  );
}

/** 신청 목록으로 — 지금 신청 목록이면 닫기만. */
function useToList(onDone: () => void) {
  const router = useRouter();
  const path = usePathname();
  return () => {
    onDone();
    if (!path?.startsWith("/ticket/requests")) router.push("/ticket/requests");
  };
}

// ── 가상머신 신청 ─────────────────────────────────────────────────
// 프로젝트 = 포털 DB(멤버십) + OpenStack 이름, 스펙·템플릿 = OpenStack 라이브, 키페어 = 포털 DB(본인 키) · 관리자는 OpenStack

const VM_STEPS = ["기본 정보", "프로젝트·템플릿", "가상머신 구성", "검토·제출"];
type VmCard = "title" | "project" | "image" | "name" | "net" | "spec" | "key";

function VmRequestForm({ onDirty, onClose, onChangeType, onDone, onSubmitted }: FormProps) {
  const user = useAuth((s) => s.user);
  const createTicket = useCreateTicket();
  const { data: projects = [], isLoading: projLoading, error: projError, failureCount: projFails } = useTicketProjects(user?.username);
  const { step, setStep, tried, setTried, show } = useWizardSteps(4);
  const { scrollRef, cardRef, scrollTo } = useCardScroll<VmCard>();
  const [submitted, setSubmitted] = React.useState<{ id: string; at: string } | null>(null);
  const toList = useToList(onDone);

  const [vmName, setVmName] = React.useState("");
  const [title, setTitle] = React.useState("");
  const [purpose, setPurpose] = React.useState("");
  const [project, setProject] = React.useState<string>("");
  const [network, setNetwork] = React.useState<string>("");
  const [floatingIp, setFloatingIp] = React.useState(false);
  const [flavor, setFlavor] = React.useState<string>("");
  const [image, setImage] = React.useState<string>("");
  const [volumeGb, setVolumeGb] = React.useState<number>(0);
  const [keypair, setKeypair] = React.useState<string>(""); // 키페어 이름
  const modelOf = useAccelModel();
  const [accel, setAccel] = React.useState(""); // 스펙 표의 가속기 고르기 — "" = 가속기 없음

  // 스펙 = 선택한 프로젝트가 쓸 수 있는 플레이버(public + 이 프로젝트에 허용된 비공개)
  const { data: flavors = [], isLoading: flavorLoading, error: flavorError } = useProjectFlavors(project || undefined);
  // 템플릿 = 선택한 프로젝트에서 쓸 수 있는 이미지
  const { data: images = [], isLoading: imageLoading, error: imageError } = useProjectImages(project || undefined);

  // 이름 형식 검사 후 입력이 멈추면(0.4초) 중복 확인
  const debouncedName = useDebounced(vmName);
  const nameFormatOk = VM_NAME_RE.test(vmName);
  const nameCheckReady = nameFormatOk && debouncedName === vmName && !!project;
  const {
    data: nameCheck, isFetching: nameChecking, isError: nameCheckFailed, error: nameCheckError, refetch: recheckName,
  } = useVmNameCheck(project, debouncedName, nameCheckReady);
  const nameOk = nameCheckReady && !nameChecking && nameCheck?.available === true;

  const selectedNetworkObj = USER_NETWORKS.find((n) => n.id === network);
  const selectedProject = projects.find((p) => p.id === project);
  const selectedFlavor = flavors.find((f) => f.name === flavor);
  const selectedImage = images.find((i) => i.id === image);
  const needVolume = !!selectedFlavor && selectedFlavor.diskGb === 0;
  const minVolume = Math.max(1, selectedImage?.minVolumeGb ?? 1);

  // 키페어 — 사용자는 본인 키만(iam_ssh_key.owner = username), 관리자는 OpenStack(Nova) 키페어
  const { isAdmin, data: myKeypairs = [], isLoading: keyLoading, error: keyError } = useRequestKeypairs();

  // 고른 행을 다시 누르면 해제
  function handleNetworkSelect(id: string) {
    const next = network === id ? "" : id;
    setNetwork(next);
    if (!USER_NETWORKS.find((n) => n.id === next)?.fipAvailable) setFloatingIp(false);
  }

  // 프로젝트 변경 시 이후 단계 입력 초기화
  function handleProjectSelect(id: string) {
    setProject(id);
    setNetwork("");
    setFloatingIp(false);
    setFlavor("");
    setImage("");
    setVolumeGb(0);
    setKeypair("");
  }

  // 부팅 볼륨 크기는 템플릿을 바꿀 때마다 그 템플릿의 최소 디스크로 맞춘다. 고른 스펙 디스크가 새 템플릿보다 작으면 스펙을 비운다
  function handleImageSelect(img: ProjectImage | null) {
    if (!img) return setImage("");
    setImage(img.id);
    if (!img.cloudInit) setKeypair(""); // cloud-init 없으면 키페어 주입 불가
    setVolumeGb(Math.max(1, img.minVolumeGb));
    if (selectedFlavor && selectedFlavor.diskGb > 0 && selectedFlavor.diskGb < img.minVolumeGb) setFlavor("");
  }
  // 부팅 볼륨 = 플레이버 디스크 — 고른 템플릿의 최소 디스크보다 작은 스펙은 쓸 수 없다(디스크 0 스펙은 부팅 볼륨 크기를 입력)
  const tooSmall = (f: FlavorSummary) => selectedImage && f.diskGb > 0 && f.diskGb < selectedImage.minVolumeGb
    ? `템플릿 최소 디스크(${selectedImage.minVolumeGb} GB)보다 작은 스펙` : null;
  const noCloudInit = !!selectedImage && !selectedImage.cloudInit;
  const keyValue = noCloudInit ? "미적용 (cloud-init 없음)" : keypair || "사용 안 함";

  // 가속기(종류·모델·노드당 개수 묶음) — 스펙을 고르면 그 스펙의 가속기. 바꿔서 고른 스펙과 맞지 않으면 스펙 선택을 비운다
  const sameAccel = accelGroup(accelChoices(flavors, modelOf));
  function handleAccel(dp: string) {
    setAccel(dp);
    if (selectedFlavor && workerFlavorBlock(selectedFlavor, dp, sameAccel)) setFlavor("");
  }

  function handleFlavorSelect(f: FlavorSummary) {
    if (f.name === flavor) return setFlavor("");
    setFlavor(f.name);
    setAccel(f.accelerator ?? "");
    // 디스크 없는 스펙 — 부팅 볼륨을 템플릿 최소 디스크로 맞춘다
    if (f.diskGb === 0) setVolumeGb(Math.max(1, selectedImage?.minVolumeGb ?? 1));
  }

  const volumeValid = !needVolume || (volumeGb >= minVolume && volumeGb <= MAX_VOLUME_GB);
  // 부팅 볼륨 크기(Cinder 쿼터에서 차감) — 디스크 없는 스펙은 입력한 크기
  const bootGb = needVolume ? volumeGb : selectedFlavor?.diskGb ?? 0;
  const bootLabel = needVolume ? `부팅 볼륨 ${volumeGb} GB` : `${bootGb} GB`;

  const specOk = !!selectedFlavor && volumeValid && !tooSmall(selectedFlavor);
  const req3 = [nameOk, !!network, specOk];
  const valid = [!!title.trim() && purpose.length <= PURPOSE_MAX, !!project && !!image, req3.every(Boolean), true];
  const reasons = [
    "신청 제목을 입력해야 넘어갈 수 있습니다",
    !project ? "프로젝트를 선택해야 넘어갈 수 있습니다" : "템플릿을 선택해야 넘어갈 수 있습니다",
    !nameOk ? "가상서버명 확인이 끝나야 넘어갈 수 있습니다" : !network ? "네트워크를 선택해야 넘어갈 수 있습니다"
      : !selectedFlavor ? "스펙을 선택해야 넘어갈 수 있습니다" : "부팅 볼륨 크기를 입력해야 넘어갈 수 있습니다",
    "",
  ];
  // 막힌 이유와 같은 순서의 첫 빈 카드 — [다음 단계]로 막히면 그 카드로 옮긴다
  const blockedCard: (VmCard | undefined)[] = [
    !title.trim() ? "title" : undefined, !project ? "project" : "image", !nameOk ? "name" : !network ? "net" : "spec", undefined,
  ];
  const dirty = !submitted && !!(title.trim() || purpose.trim() || project || image || vmName || network || flavor || keypair);
  React.useEffect(() => onDirty(dirty), [dirty, onDirty]);

  // 다른 단계의 카드면 그 단계를 그린 다음 스크롤
  function jump(i: number, card: VmCard) {
    if (i !== step) setStep(i);
    setTimeout(() => scrollTo(card), i === step ? 0 : 80);
  }
  function next() {
    if (!valid[step]) {
      setTried(step);
      const card = blockedCard[step];
      if (card) scrollTo(card);
      return;
    }
    setStep(step + 1);
  }

  function submit() {
    const proj = selectedProject;
    const flv = selectedFlavor;
    const img = selectedImage;
    if (!proj || !flv || !img) return;
    // 이름·스펙·이미지 정보는 서버가 라이브 조회로 다시 확인해 저장한다 — 화면은 식별자만 보낸다
    createTicket.mutate(
      {
        projectId: proj.id,
        title: title.trim() || undefined,
        vmName,
        purpose: purpose.trim(),
        networkType: network,
        floatingIp,
        flavorId: flv.id, // Nova 생성 시 flavorRef
        imageId: img.id,
        volumeGb: needVolume ? volumeGb : undefined,
        keypairId: isAdmin ? undefined : myKeypairs.find((k) => k.name === keypair)?.id,
        novaKeypair: isAdmin && keypair ? keypair : undefined, // 관리자는 OpenStack 키페어 이름
      },
      {
        onSuccess: (t) => {
          toast.success("자원 요청이 접수되었습니다", { description: `${vmName} (${flv.name}) · 승인 대기 중` });
          setSubmitted({ id: t.id, at: t.requestedAt });
          onSubmitted();
        },
        onError: (e) => toast.error("요청 접수에 실패했습니다", { description: e.message }),
      },
    );
  }

  // 왼쪽 스테퍼 — 설명 = 입력 요약, 지금 단계이고 앞 단계 필수가 채워졌을 때만 빨간 '필수 n/m 완료' + '선택 필요'
  const done = (i: number) => (submitted ? "done" : i < step ? "done" : i === step ? "cur" : "todo");
  const s1 = valid[0] && step === 1;
  const s2 = valid[0] && valid[1] && step === 2;
  const n3 = req3.filter(Boolean).length;
  const steps: StepItem[] = [
    { no: "01", name: VM_STEPS[0], state: done(0), onClick: !submitted && step > 0 ? () => setStep(0) : undefined,
      desc: title.trim() || (step === 0 ? "필수 0/1 완료" : "신청 제목 · 용도"), descTone: !title.trim() && step === 0 ? "red" : undefined,
      missing: !submitted && !title.trim() && step === 0 ? [{ label: "신청 제목", onClick: () => jump(0, "title") }] : undefined },
    { no: "02", name: VM_STEPS[1], state: done(1), onClick: !submitted && step > 1 ? () => setStep(1) : undefined,
      desc: selectedProject && selectedImage ? `${selectedProject.name}\n${selectedImage.name}` : s1 ? `필수 ${[!!project, !!image].filter(Boolean).length}/2 완료` : "프로젝트와 템플릿 선택",
      descTone: s1 && !valid[1] ? "red" : undefined,
      missing: !submitted && s1 ? ([["프로젝트", !project, "project"], ["템플릿", !image, "image"]] as const).filter(([, m]) => m)
        .map(([label, , card]) => ({ label, onClick: () => jump(1, card) })) : undefined },
    { no: "03", name: VM_STEPS[2], state: done(2), onClick: !submitted && step > 2 ? () => setStep(2) : undefined,
      desc: `필수 ${n3}/3 완료`, descTone: s2 && n3 < 3 ? "red" : undefined,
      missing: !submitted && s2 ? ([["가상서버명", "name"], ["네트워크", "net"], ["스펙", "spec"]] as const).filter((_, i) => !req3[i])
        .map(([label, card]) => ({ label, onClick: () => jump(2, card) })) : undefined },
    { no: "04", name: VM_STEPS[3], state: done(3), desc: submitted ? `${formatDateTime(submitted.at).slice(11, 16)} 제출` : "제출 전 최종 확인" },
  ];

  const reviewData = selectedFlavor && {
    title, purpose, projectId: project, projectName: selectedProject?.name, orgName: selectedProject?.orgName,
    imageName: selectedImage?.name, image: selectedImage, cloudInit: !noCloudInit,
    vmName, networkType: network, floatingIp,
    flavorName: selectedFlavor.name, vcpus: selectedFlavor.vcpus, ramGb: selectedFlavor.ramMb / 1024, diskGb: bootGb, diskLabel: bootLabel,
    accel: flavorAccel(selectedFlavor, modelOf), keypair: keypair || undefined,
  };
  const netValue = selectedNetworkObj && (selectedNetworkObj.fipAvailable
    ? `${selectedNetworkObj.label} / Floating IP ${floatingIp ? "희망" : "희망 안 함"}` : selectedNetworkObj.label);

  const summary = (
    <>
      <SumSection title="신청 정보" step="01" onStep={!submitted ? () => setStep(0) : undefined} rows={[["신청 제목", title.trim() || undefined]]} />
      <SumSection title="프로젝트·템플릿" step="02" onStep={!submitted && step >= 1 ? () => setStep(1) : undefined} rows={[
        ["기관", selectedProject?.orgName || undefined], ["프로젝트", selectedProject?.name],
        ["템플릿", selectedImage?.name],
      ]} />
      <SumSection title="가상서버" step="03" onStep={!submitted && step >= 2 ? () => setStep(2) : undefined} rows={[
        ["이름", mono(vmName)], ["네트워크", selectedNetworkObj?.label], ["스펙", selectedFlavor ? mono(selectedFlavor.name) : NOT_YET],
        ["키페어", keypair ? mono(keypair) : noCloudInit ? "미적용" : undefined],
      ]} />
      <SumSection title="총 자원" rows={[
        ["VM", "1대"], ["vCPU", selectedFlavor && `${selectedFlavor.vcpus}`], ["RAM", selectedFlavor && `${selectedFlavor.ramMb / 1024} GB`],
        ["Disk", selectedFlavor && (needVolume ? `부팅 볼륨 ${volumeGb} GB` : `${selectedFlavor.diskGb} GB`)],
        ["가속기", selectedFlavor && <TotalAccel f={selectedFlavor} model={modelOf(selectedFlavor.accelerator)} n={1} />],
      ]} />
      <SumSection title={<>프로젝트 쿼터 <span className="font-normal text-muted-foreground">(사용 → 신청 후)</span></>}>
        <QuotaBars projectId={project} add={{ vcpu: selectedFlavor?.vcpus ?? 0, ramGb: selectedFlavor ? selectedFlavor.ramMb / 1024 : 0, diskGb: bootGb }} />
      </SumSection>
    </>
  );

  const footer = (
    // 막힌 이유 — [다음 단계]를 눌러 막혔을 때만(평소에는 떠 있는 진행 알림이 빠진 항목을 보여 준다)
    <StepFooter tone="bad" status={tried[step] && !valid[step] ? reasons[step] : undefined}
      left={<Button variant="outline" onClick={() => setStep(step - 1)} disabled={step === 0}>이전 단계</Button>}>
      {step === 3 ? (
        <Button onClick={submit} disabled={createTicket.isPending}>{createTicket.isPending ? "제출 중…" : "가상머신 신청 제출"}</Button>
      ) : (
        <NextButton blocked={!valid[step] && show(step)} onClick={next} />
      )}
    </StepFooter>
  );

  return (
    <StepperHeader kind="VM" title="가상머신 신청" onClose={onClose}
      badge={submitted && <StatusPill tone="done">제출 완료</StatusPill>}
      right={!submitted && <TypeSwitch label="가상머신" onChange={onChangeType} />}>
      <StepperGrid left={<LeftStepper steps={steps} />} summary={summary}>
        {submitted ? (
          <SubmittedView name={vmName} id={submitted.id} onClose={onDone} onList={toList} />
        ) : step === 0 ? (
          <StepMain step={["01", "04"]} title="기본 정보" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as VmCard)} items={[{ key: "title", label: "신청 제목", ok: !!title.trim() }]} />}>
            <TitleField title={title} onTitle={setTitle} placeholder="예: NPU 추론 성능 검증용 가상머신" missing={tried[0] && !title.trim()} cardRef={cardRef("title")} />
            <PurposeField purpose={purpose} onPurpose={setPurpose} placeholder="예: 모델 학습·추론 실험" />
          </StepMain>
        ) : step === 1 ? (
          <StepMain step={["02", "04"]} title="프로젝트·템플릿" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as VmCard)} items={[
              { key: "project", label: "프로젝트", value: selectedProject?.name, ok: !!project },
              { key: "image", label: "템플릿", value: selectedImage?.name, ok: !!image },
            ]} />}>
            <ProjectTable projects={projects} loading={projLoading} loadingText={projectsLoadingText(projFails)} error={projError?.message}
              selected={project} onSelect={handleProjectSelect} missing={tried[1] && !project} cardRef={cardRef("project")} />
            <FieldBlock ref={cardRef("image")} label="템플릿" required hint="다음 단계에서는 이 이미지의 최소 디스크 이상인 스펙만 보입니다"
              missing={tried[1] && !image} ask="템플릿을 고르세요">
              {!project ? <NeedProject what="템플릿" /> : (
                <>
                  {imageLoading && <p className="text-xs text-muted-foreground">템플릿을 불러오는 중…</p>}
                  {imageError && <LoadError what="템플릿" message={imageError.message} />}
                  {!imageLoading && !imageError && images.length === 0 && (
                    <p className="text-xs text-muted-foreground">이 프로젝트에서 사용할 수 있는 템플릿이 없습니다.</p>
                  )}
                  {images.length > 0 && <ImageTable images={images} selected={image} onSelect={handleImageSelect} />}
                </>
              )}
              {selectedImage && !selectedImage.cloudInit && (
                <div className="flex gap-2.5 rounded-lg border border-amber-200 bg-amber-50 p-3">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
                  <p className="text-xs text-amber-800">
                    <span className="font-semibold">키페어 미적용</span> — cloud-init이 없는 이미지는 키페어가 주입되지 않습니다. 이미지의 기본 계정으로 접속하세요.
                  </p>
                </div>
              )}
            </FieldBlock>
          </StepMain>
        ) : step === 2 ? (
          <StepMain step={["03", "04"]} title="가상머신 구성" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as VmCard)} items={[
              { key: "name", label: "가상서버명", value: nameOk ? vmName : undefined, ok: nameOk },
              { key: "net", label: "네트워크", value: selectedNetworkObj?.label, ok: !!network },
              { key: "spec", label: "스펙", value: selectedFlavor?.name, ok: specOk },
              { key: "key", label: "키페어", value: keyValue, ok: true, optional: true },
            ]} />}>
            <NameField cardRef={cardRef("name")} icon={serverIcon} title="가상서버명" value={vmName} onChange={setVmName} placeholder="예: research-trainer-04" maxLength={63}
              status={nameOk ? "done" : vmName && nameFormatOk && (!nameCheckReady || nameChecking) ? undefined : "missing"}>
              <NameCheckLine value={vmName} formatOk={nameFormatOk} ready={nameCheckReady} checking={nameChecking} failed={nameCheckFailed}
                error={nameCheckError?.message} recheck={() => recheckName()} available={nameCheck?.available} reason={nameCheck?.reason}
                hint="영문 소문자·숫자·하이픈(-) 2~63자, 하이픈으로 시작하거나 끝날 수 없습니다. 서버 이름과 hostname으로 함께 사용됩니다."
                formatError="영문 소문자·숫자·하이픈(-)만 2~63자로 입력하세요. 하이픈으로 시작하거나 끝날 수 없습니다." />
            </NameField>
            <FormCard ref={cardRef("net")} icon={<Network className="size-4 text-primary" />} title="네트워크" required
              status={network ? "done" : "missing"} value={netValue}>
              <div className="flex flex-col gap-2">
                {USER_NETWORKS.map((n) => {
                  const on = network === n.id;
                  return (
                    <button key={n.id} type="button" onClick={() => handleNetworkSelect(n.id)}
                      className={cn("flex cursor-pointer items-center gap-3 rounded-[10px] border-[1.5px] px-4 py-3 text-left transition-colors",
                        on ? "border-primary! bg-[#f5f7ff]" : "border-border bg-card hover:bg-secondary/40")}>
                      {n.id === "external" ? <Globe className="size-5 shrink-0 text-amber-600" /> : <Wifi className="size-5 shrink-0 text-sky-600" />}
                      <span className="flex-1 text-sm font-semibold">{n.label}</span>
                      {on && <CheckCircle2 className="size-4 shrink-0 fill-brand-mint-500 text-white" />}
                    </button>
                  );
                })}
                {/* Floating IP 희망 — 인터넷망 선택 시만 활성 */}
                <button type="button" onClick={() => selectedNetworkObj?.fipAvailable && setFloatingIp((v) => !v)}
                  disabled={!selectedNetworkObj?.fipAvailable}
                  className={cn("flex cursor-pointer items-center gap-3 rounded-[10px] border-[1.5px] px-4 py-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40",
                    floatingIp ? "border-primary! bg-[#f5f7ff]" : "border-border bg-card hover:bg-secondary/40")}>
                  <Globe className="size-5 shrink-0 text-amber-600" />
                  <span className="flex flex-1 flex-col">
                    <span className="text-sm font-semibold">외부 접속용 Floating IP 희망</span>
                    <span className="text-xs text-muted-foreground">Floating IP 를 할당합니다</span>
                  </span>
                  <Radio on={floatingIp} />
                </button>
                {network === "internal" && (
                  <div className="flex gap-2.5 rounded-lg border border-amber-200 bg-amber-50 p-3">
                    <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
                    <p className="text-xs whitespace-pre-line text-amber-800">
                      <span className="font-semibold">외부 SSH 접속 불가</span> — 내부망 가상머신은 외부에서 접근할 수 없습니다. 외부 접근이 필요하다면 다음 단계를 진행하세요.{"\n"}- 인터넷망 → Floating IP 희망
                    </p>
                  </div>
                )}
              </div>
            </FormCard>
            <FormCard ref={cardRef("spec")} icon={serverIcon} title="스펙" required status={specOk ? "done" : "missing"}
              value={selectedFlavor?.name} line={selectedFlavor && needVolume && !volumeValid ? "부팅 볼륨 크기를 확인하세요" : undefined}>
              {flavorLoading && <p className="text-xs text-muted-foreground">스펙을 불러오는 중…</p>}
              {flavorError && <LoadError what="스펙" message={flavorError.message} />}
              {!flavorLoading && !flavorError && (
                <SpecPicker flavors={flavors} role="w" fixed={false} selected={selectedFlavor?.id ?? ""}
                  onSelect={(id) => { const f = flavors.find((x) => x.id === id); if (f) handleFlavorSelect(f); }}
                  accel={accel} onAccel={handleAccel} refLabel="" refId="" sortSelect groupAccel zeroDisk="Disk 볼륨 지정"
                  extraBlock={tooSmall} />
              )}
              {/* 부팅 볼륨 — 디스크 없는 스펙 */}
              {needVolume && (
                <div className="space-y-2 rounded-[10px] border border-border bg-secondary/30 p-3.5">
                  <label htmlFor="volume" className="text-sm font-semibold">부팅 볼륨 크기</label>
                  <p className="text-xs text-muted-foreground">
                    선택한 스펙(<span className="font-mono">{selectedFlavor?.name}</span>)에 디스크가 없어 부팅 볼륨을 생성합니다.
                    {selectedImage ? ` ${minVolume} ~ ${MAX_VOLUME_GB} GB` : ` 최대 ${MAX_VOLUME_GB} GB`}
                  </p>
                  <div className="flex items-center gap-2">
                    <HardDrive className="size-5 shrink-0 text-indigo-600" />
                    <Input id="volume" type="number" min={minVolume} max={MAX_VOLUME_GB} value={volumeGb || ""}
                      onChange={(e) => setVolumeGb(Math.max(0, Number(e.target.value)))} placeholder={String(minVolume)} className="h-9 w-32 bg-card text-right" />
                    <span className="text-xs text-muted-foreground">GB</span>
                  </div>
                  {volumeGb > 0 && !volumeValid && (
                    <p className="text-xs text-rose-500">
                      {volumeGb > MAX_VOLUME_GB ? `최대 ${MAX_VOLUME_GB} GB까지 신청할 수 있습니다.` : `이미지 최소 디스크(${minVolume} GB) 이상이어야 합니다.`}
                    </p>
                  )}
                </div>
              )}
            </FormCard>
            <FormCard ref={cardRef("key")} icon={keyIcon} title="키페어" optional value={keyValue} valueMuted={noCloudInit || !keypair}
              line={noCloudInit ? "이 템플릿은 키페어를 넣을 수 없습니다" : keypair ? undefined : "VM 에 SSH 키를 넣지 않습니다"}>
              {noCloudInit ? (
                <div className="flex gap-2.5 rounded-lg border border-amber-200 bg-amber-50 p-3">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600" />
                  <p className="text-xs text-amber-800">
                    <span className="font-semibold">키페어 미적용</span> — cloud-init이 없는 이미지는 키페어가 주입되지 않습니다. 이미지의 기본 계정으로 접속하세요.
                  </p>
                </div>
              ) : keyLoading ? <p className="text-xs text-muted-foreground">키페어를 불러오는 중…</p>
                : keyError ? <LoadError what="키페어" message={keyError.message} /> : (
                <KeypairTable
                  rows={myKeypairs.map((k) => ({ id: k.name, name: k.name, fingerprint: k.fingerprint, type: keyType(k.publicKey) }))}
                  selected={keypair || null} refLabel="" paged
                  onSelect={(id) => setKeypair((cur) => (cur === id ? "" : id))}
                  empty={<>등록된 키페어가 없습니다. 키 없이 신청할 수 있고, <span className="font-semibold">키페어</span> 메뉴에서 등록하면 여기서 고를 수 있습니다.</>}
                />
              )}
            </FormCard>
          </StepMain>
        ) : (
          <StepMain step={["04", "04"]} title="검토·제출" desc={REVIEW_LEAD} footer={footer} scrollRef={scrollRef}>
            {reviewData && (
              <>
                <VmCards v={reviewData} onEdit={(k) => setStep(k === "info" ? 0 : k === "source" ? 1 : 2)} />
                <QuotaCard projectId={project} add={{ vcpu: reviewData.vcpus, ramGb: reviewData.ramGb, diskGb: bootGb }} />
                <FlowLine label="제출 후 진행 순서" steps={VM_FLOW} current={0} className="flex-none" />
              </>
            )}
          </StepMain>
        )}
      </StepperGrid>
    </StepperHeader>
  );
}

const REVIEW_LEAD = "아래 내용을 확인하고 신청하세요. 이 내용 그대로 관리자에게 전달됩니다.";

/** 신청 단계의 프로젝트 쿼터 카드 — 사용 → 신청 후 / 한도, 경고는 막대 아래. */
function QuotaCard({ projectId, add }: { projectId: string; add: { vcpu: number; ramGb: number; diskGb?: number } }) {
  return (
    <div className="flex flex-none flex-col gap-2.5 rounded-[10px] border border-border px-3.5 py-3">
      <span className="text-sm font-bold">프로젝트 쿼터 <span className="font-normal text-muted-foreground">(사용 → 신청 후 / 한도)</span></span>
      <QuotaBars projectId={projectId} add={add} warnAt="bottom" />
    </div>
  );
}

// ── 프로젝트 · 템플릿 표 ──────────────────────────────────────────

type ProjSort = "name" | "vcpu" | "ram" | "disk";
const PROJ_COLS = "grid grid-cols-[22px_minmax(0,1fr)_84px_84px_96px] items-center gap-[14px] px-4";

/** 표 머리 칸 — 누르면 정렬(같은 칸이면 방향 바꿈). */
function SortHead<K extends string>({ k, label, sort, onSort }: { k: K; label: string; sort: { key: K; dir: 1 | -1 }; onSort: (k: K) => void }) {
  return (
    <button type="button" onClick={() => onSort(k)}
      className={cn("flex cursor-pointer items-center gap-1 text-left text-xs hover:text-foreground", sort.key === k ? "font-semibold text-primary" : "text-muted-foreground")}>
      {label}<span className="w-2 text-[10px]">{sort.key === k ? (sort.dir === 1 ? "↑" : "↓") : ""}</span>
    </button>
  );
}

/** 신청 프로젝트 표(가상머신·서비스 클러스터 공용) — 검색 + 표: 프로젝트(이름 · 조직 · 쿼터 부족) · 남은 vCPU · RAM · Disk(쿼터 − 사용량).
 *  열 제목을 누르면 정렬(기본 남은 vCPU 오름차순), 4줄씩. 남은 vCPU 나 RAM 이 0 이하면 '쿼터 부족' — 고를 수는 있다(승인 전에 쿼터를
 *  늘릴 수 있고, 프로비저닝 때 서버가 다시 확인). 가속기(NPU) 쿼터는 OpenStack 에 없어 표시하지 않는다. 고른 행을 다시 누르면 해제. */
function ProjectTable({ projects, loading, loadingText, error, selected, onSelect, missing, cardRef }: {
  projects: TicketProject[]; loading: boolean; loadingText: string; error?: string; selected: string;
  onSelect: (id: string) => void; missing: boolean; cardRef: (el: HTMLDivElement | null) => void;
}) {
  const [query, setQuery] = React.useState("");
  const [sort, setSort] = React.useState<{ key: ProjSort; dir: 1 | -1 }>({ key: "vcpu", dir: 1 });
  const [page, setPage] = React.useState<number | null>(null);
  const { data: quotas, isLoading: quotaLoading } = useProjectQuotas(projects.length > 0);
  const byId = new Map((quotas ?? []).map((t) => [t.id, t]));
  const left = (p: TicketProject) => {
    const t = byId.get(p.id);
    const d = t && diskQuota(t);
    return t ? { vcpu: quotaLeft(t.quotaVcpu, t.usedVcpu), ram: quotaLeft(t.quotaRamGb, t.usedRamGb), disk: d ? quotaLeft(d.limit, d.used) : null } : null;
  };
  const num = (v: number | null | undefined) => (v === null ? Infinity : v ?? -Infinity); // 무제한은 가장 큼, 모름은 가장 작음
  const q = query.trim().toLowerCase();
  const all = projects.filter((p) => !q || p.name.toLowerCase().includes(q) || (p.orgName ?? "").toLowerCase().includes(q))
    .sort((a, b) => sort.dir * (sort.key === "name" ? a.name.localeCompare(b.name) : num(left(a)?.[sort.key]) - num(left(b)?.[sort.key]) || a.name.localeCompare(b.name)));
  const { rows, page: pageNow, pages } = paginate(all, page, (p) => p.id === selected);
  const onSort = (k: ProjSort) => { setSort((s) => (s.key === k ? { key: k, dir: s.dir === 1 ? -1 : 1 } : { key: k, dir: 1 })); setPage(0); };
  return (
    <FieldBlock ref={cardRef} label="프로젝트" required hint={missing ? undefined : "프로젝트를 선택하세요"} missing={missing} ask="프로젝트를 고르세요">
      <SearchInput value={query} onChange={(v) => { setQuery(v); setPage(0); }} placeholder="프로젝트 검색" />
      {loading ? <p className="text-xs text-muted-foreground">{loadingText}</p> : error ? <LoadError what="프로젝트" message={error} /> : projects.length === 0 ? (
        <div className="rounded-[10px] border border-dashed bg-card p-5 text-center text-[13px] text-muted-foreground">
          신청할 수 있는 프로젝트가 없습니다(프로젝트 owner·member만 신청 가능).<br /><span className="text-xs">조직 관리자에게 프로젝트 할당을 요청하세요.</span>
        </div>
      ) : (
        <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
          <div className={cn(PROJ_COLS, "border-b border-border bg-secondary/50 py-[9px]")}>
            <span /><SortHead k="name" label="프로젝트" sort={sort} onSort={onSort} /><SortHead k="vcpu" label="남은 vCPU" sort={sort} onSort={onSort} />
            <SortHead k="ram" label="남은 RAM" sort={sort} onSort={onSort} /><SortHead k="disk" label="남은 Disk" sort={sort} onSort={onSort} />
          </div>
          <div className="h-[184px] border-b border-border/60">
            {rows.map((p) => {
              const sel = selected === p.id;
              const l = left(p);
              const short = !!l && ((l.vcpu !== null && l.vcpu <= 0) || (l.ram !== null && l.ram <= 0));
              const cell = (v: number | null | undefined, unit = "") => (
                <span className={cn("font-mono text-xs", short && "text-[#e8823f]")}>{l ? showLeft(v ?? null, unit) : quotaLoading ? "…" : "—"}</span>
              );
              return (
                <div key={p.id} onClick={() => onSelect(sel ? "" : p.id)}
                  className={cn(PROJ_COLS, "h-[46px] cursor-pointer border-b border-border/60 last:border-b-0", sel ? "bg-brand-indigo-50/60" : "hover:bg-secondary/40")}>
                  <Radio on={sel} />
                  <span className="flex min-w-0 items-center gap-2">
                    <span title={p.name} className="min-w-0 truncate font-mono text-[13px] font-semibold">{p.name}</span>
                    {p.orgName && <span className="flex-none text-[11px] text-muted-foreground">({p.orgName})</span>}
                    {short && <span className="flex-none rounded-full bg-amber-100 px-1.5 py-px text-[10px] font-semibold text-amber-800">쿼터 부족</span>}
                  </span>
                  {cell(l?.vcpu)}{cell(l?.ram, " GB")}{cell(l?.disk, " GB")}
                </div>
              );
            })}
            {rows.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">조건에 맞는 프로젝트가 없습니다</div>}
          </div>
          <Pager total={all.length} page={pageNow} pages={pages} onPage={setPage} />
        </div>
      )}
    </FieldBlock>
  );
}

// 버전 내림차순(1.36.2 → 1.34.8, ubuntu 22.04 → ubuntu), 정보 없음('')은 맨 뒤
function byVersionDesc(a: string, b: string) {
  if (!a || !b) return a ? -1 : b ? 1 : 0;
  return b.localeCompare(a, undefined, { numeric: true });
}

/** 버전 고르기(K8s 버전 · OS) — 전체 + 값(내림차순), 정보 없음('')은 unknownLabel. */
function VersionSelect({ values, value, onChange, allLabel, unknownLabel, label = (v) => v }: {
  values: string[]; value: string; onChange: (v: string) => void; allLabel: string; unknownLabel: string; label?: (v: string) => string;
}) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className="h-9 rounded-lg border border-input bg-card px-2 text-[13px]">
      <option value="all">{allLabel}</option>
      {Array.from(new Set(values)).sort(byVersionDesc).map((v) => <option key={v} value={v}>{v ? label(v) : unknownLabel}</option>)}
    </select>
  );
}

const IMG_COLS = "grid grid-cols-[22px_minmax(0,1fr)_60px_96px_76px] items-center gap-[14px] px-4";

/** 가상머신 템플릿(OS 이미지) 표 — 형식(QCOW2·RAW …) 세그먼트 + OS + 이름 검색, 표 이미지 · 형식 · OS · 최소 디스크, 4줄씩.
 *  고른 행을 다시 누르면 해제. cloud-init 없는 이미지는 고른 뒤 아래 안내로 알린다. */
function ImageTable({ images, selected, onSelect }: { images: ProjectImage[]; selected: string; onSelect: (img: ProjectImage | null) => void }) {
  const [fmt, setFmt] = React.useState("all");
  const [osSel, setOsSel] = React.useState("all");
  const [query, setQuery] = React.useState("");
  const [page, setPage] = React.useState<number | null>(null);
  const formats = Array.from(new Set(images.map(imageFormat)));
  const q = query.trim().toLowerCase();
  const all = images.filter((i) => (fmt === "all" || imageFormat(i) === fmt) && (osSel === "all" || imageOs(i) === osSel)
    && (!q || i.name.toLowerCase().includes(q)));
  const { rows, page: pageNow, pages } = paginate(all, page, (i) => i.id === selected);
  return (
    <>
      <div className="flex items-center gap-2">
        <Segment
          options={[["all", `전체 (${images.length} 개)`], ...formats.map((f) => [f, `${f} (${images.filter((i) => imageFormat(i) === f).length} 개)`] as const)]}
          value={fmt} onChange={(v) => { setFmt(v); setPage(0); }} />
        <VersionSelect values={images.map(imageOs)} value={osSel} onChange={(v) => { setOsSel(v); setPage(0); }} allLabel="전체 OS" unknownLabel="OS 정보 없음" />
        <div className="min-w-0 flex-1"><SearchInput value={query} onChange={(v) => { setQuery(v); setPage(0); }} placeholder="이미지 이름 검색" /></div>
      </div>
      <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
        <div className={cn(IMG_COLS, "border-b border-border bg-secondary/50 py-[9px] text-xs text-muted-foreground")}>
          <span /><span>이미지</span><span>형식</span><span>OS</span><span>최소 디스크</span>
        </div>
        <div className="h-[184px] border-b border-border/60">
          {rows.map((img) => {
            const sel = selected === img.id;
            return (
              <div key={img.id} onClick={() => onSelect(sel ? null : img)}
                className={cn(IMG_COLS, "h-[46px] cursor-pointer border-b border-border/60 last:border-b-0", sel ? "bg-brand-indigo-50/60" : "hover:bg-secondary/40")}>
                <Radio on={sel} />
                <span title={img.name} className="truncate font-mono text-[13px] font-semibold">{img.name}</span>
                <span className="font-mono text-xs text-muted-foreground">{imageFormat(img)}</span>
                <span title={imageOs(img)} className="truncate text-xs text-muted-foreground">{imageOs(img) || "OS 정보 없음"}</span>
                <span className="font-mono text-xs text-muted-foreground">{img.minVolumeGb} GB</span>
              </div>
            );
          })}
          {rows.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">조건에 맞는 이미지가 없습니다</div>}
        </div>
        <Pager total={all.length} page={pageNow} pages={pages} onPage={setPage} />
      </div>
    </>
  );
}

const ACCEL_TABS = [["all", "전체"], ["none", "가속기 없음"], ["NPU", "NPU"], ["GPU", "GPU"]] as const;
type AccelTab = typeof ACCEL_TABS[number][0];
const TPL_COLS = "grid grid-cols-[22px_minmax(0,1fr)_84px_minmax(0,120px)_minmax(0,120px)] items-center gap-[14px] px-4";

/** 서비스 클러스터 템플릿 표 — 가속기 탭 + K8s 버전 + 이름 검색, 표 템플릿(+ 가속기 · 'Worker 스펙 확인 필요') · Kubernetes · CP · WK, 4줄씩.
 *  가속기 종류는 Worker 스펙을 고른 프로젝트의 스펙 목록에서 찾아 정한다 — 못 찾으면(이 프로젝트에서 쓸 수 없음) '전체'에만 보이고
 *  'Worker 스펙 확인 필요'를 붙인다. K8s 버전은 템플릿 이미지의 Glance 속성 kube_version(MNT-PAAS 가 채운다). 고른 행을 다시 누르면 해제. */
function TemplateTable({ templates, flavors, flavorsLoading, modelOf, selected, onSelect }: {
  templates: ClusterTemplate[]; flavors: FlavorSummary[]; flavorsLoading: boolean; modelOf: (dp: string | null | undefined) => string | null;
  selected: string; onSelect: (id: string) => void;
}) {
  const [tab, setTab] = React.useState<AccelTab>("all");
  const [ver, setVer] = React.useState("all");
  const [query, setQuery] = React.useState("");
  const [page, setPage] = React.useState<number | null>(null);
  const workerOf = (t: ClusterTemplate) => flavors.find((f) => f.id === t.flavor_id || f.name === t.flavor_id);
  const kinds = (t: ClusterTemplate): string[] => {
    const w = workerOf(t);
    if (!w) return [];
    return w.accelerator ? (w.accelerators ?? []).map((a) => a.kind) : ["none"];
  };
  const inTab = (t: ClusterTemplate, k: AccelTab) => k === "all" || kinds(t).includes(k);
  const q = query.trim().toLowerCase();
  const all = templates.filter((t) => inTab(t, tab) && (ver === "all" || (t.kubeVersion || "") === ver) && (!q || t.name.toLowerCase().includes(q)));
  const { rows, page: pageNow, pages } = paginate(all, page, (t) => t.id === selected);
  return (
    <>
      <div className="flex items-center gap-2">
        <Segment options={ACCEL_TABS.map(([k, label]) => [k, `${label} (${templates.filter((t) => inTab(t, k)).length})`] as const)}
          value={tab} onChange={(v) => { setTab(v); setPage(0); }} />
        <VersionSelect values={templates.map((t) => t.kubeVersion || "")} value={ver} onChange={(v) => { setVer(v); setPage(0); }} allLabel="전체 버전"
          unknownLabel="버전 정보 없음" label={(v) => `v${v}`} />
        <div className="min-w-0 flex-1"><SearchInput value={query} onChange={(v) => { setQuery(v); setPage(0); }} placeholder="템플릿 이름 검색" /></div>
      </div>
      <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
        <div className={cn(TPL_COLS, "border-b border-border bg-secondary/50 py-[9px] text-xs text-muted-foreground")}>
          <span /><span>템플릿</span><span>Kubernetes</span><span>CP</span><span>WK</span>
        </div>
        <div className="h-[184px] border-b border-border/60">
          {rows.map((t) => {
            const sel = selected === t.id;
            const w = workerOf(t);
            // Worker 스펙을 이 프로젝트에서 못 찾음(삭제·이름 변경·접근 불가) 또는 가리키는 device-profile 이 없음 — 다음 단계에서 템플릿 값을 쓸 수 없다
            const workerCheck = !flavorsLoading && !!t.flavor_id && (!w || !!w.deviceProfileMissing);
            return (
              <div key={t.id} onClick={() => onSelect(sel ? "" : t.id)}
                className={cn(TPL_COLS, "h-[46px] cursor-pointer border-b border-border/60 last:border-b-0", sel ? "bg-brand-indigo-50/60" : "hover:bg-secondary/40")}>
                <Radio on={sel} />
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span title={t.name} className="truncate font-mono text-[13px] font-semibold">{t.name}</span>
                  <span className="flex min-w-0 gap-1">
                    {workerCheck ? (
                      <Badge variant="ghost" title={`Worker 스펙(${t.flavor_id})을 이 프로젝트에서 쓸 수 없습니다. 다음 단계에서 직접 고르거나 관리자에게 문의하세요`}
                        className="h-[18px] shrink-0 bg-amber-100 px-1.5 text-[10px] text-amber-800">Worker 스펙 확인 필요</Badge>
                    ) : w && <AccelTags f={w} model={modelOf(w.accelerator)} />}
                  </span>
                </span>
                <span className={cn("font-mono text-xs", t.kubeVersion ? "text-primary" : "text-muted-foreground")}>{t.kubeVersion ? `v${t.kubeVersion}` : "정보 없음"}</span>
                <span title={t.master_flavor_id ?? ""} className="truncate font-mono text-xs text-muted-foreground">{t.master_flavor_id ?? "—"}</span>
                <span title={t.flavor_id ?? ""} className="truncate font-mono text-xs text-muted-foreground">{t.flavor_id ?? "—"}</span>
              </div>
            );
          })}
          {rows.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">조건에 맞는 템플릿이 없습니다</div>}
        </div>
        <Pager total={all.length} page={pageNow} pages={pages} onPage={setPage} />
      </div>
    </>
  );
}

// 클러스터와 함께 만들 프로젝트 네임스페이스 — 미리 보기. MNT-CORE _namespace_name 과 같은 규칙(실제 값은 서버가 신청 때 정해 저장)
const NS_NAME_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;
function namespaceName(projectName: string, projectId: string) {
  const name = projectName.toLowerCase().replace(/[^a-z0-9-]/g, "-").replace(/-+/g, "-").slice(0, 63).replace(/^-+|-+$/g, "");
  const ok = NS_NAME_RE.test(name) && name !== "default" && !name.startsWith("kube-");
  return ok ? name : `ws-${projectId.replace(/-/g, "").slice(0, 8).toLowerCase()}`;
}

// ── 서비스 클러스터 신청 ───────────────────────────────────────────
// 사용자는 클러스터를 직접 만들지 않고 템플릿·노드 구성(가속기 포함)을 정해 신청한다(망은 묻지 않는다 — 내부망/인터넷망 선택은
// 헷갈릴 위험). 승인 뒤 관리자가 망·서브넷을 골라 프로비저닝하면 Magnum 클러스터 + 프로젝트 네임스페이스 + 접근 권한(프로젝트
// 멤버)이 만들어진다. 프로젝트·이름 확인 = 포털 DB·MNT-PAAS, 템플릿 = MNT-PAAS, 스펙 = 프로젝트 플레이버(MNT-IAAS).

const CLUSTER_STEPS = ["기본 정보", "프로젝트·템플릿", "노드 구성", "검토·제출"];
type ClusterCard = "title" | "project" | "template" | "name" | "cp" | "w" | "key";

function ClusterRequestForm({ onDirty, onClose, onChangeType, onDone, onSubmitted }: FormProps) {
  const user = useAuth((s) => s.user);
  const createTicket = useCreateClusterTicket();
  const { data: projects = [], isLoading: projLoading, error: projError, failureCount: projFails } = useTicketProjects(user?.username);
  const { data: allTemplates = [], isLoading: tplLoading, error: tplError } = useClusterTemplates();
  const { step, setStep, tried, setTried, show } = useWizardSteps(4);
  const { scrollRef, cardRef, scrollTo } = useCardScroll<ClusterCard>();
  const [submitted, setSubmitted] = React.useState<{ id: string; at: string } | null>(null);
  const toList = useToList(onDone);

  const [project, setProject] = React.useState("");
  const [clusterName, setClusterName] = React.useState("");
  const [title, setTitle] = React.useState("");
  const [purpose, setPurpose] = React.useState("");
  const [templateId, setTemplateId] = React.useState("");
  const [nodeCfg, setNodeCfg] = React.useState<ClusterNodeConfig>(EMPTY_NODE_CONFIG);

  const { data: flavors = [], isLoading: flavorLoading, error: flavorError } = useProjectFlavors(project || undefined);
  const { isAdmin, data: myKeypairs = [], isLoading: keyLoading, error: keyError } = useRequestKeypairs();
  // 신청할 수 있는 템플릿 — 숨김이 아니고 공개이거나 고른 프로젝트 소유(MNT-CORE 확인과 같음)
  const templates = allTemplates.filter((t) => !t.hidden && (t.public || t.project_id === project));
  const modelOf = useAccelModel();

  // 이름 형식 검사 후 입력이 멈추면(0.4초) 중복 확인 — 모든 프로젝트의 클러스터 + 진행 중 신청
  const debouncedName = useDebounced(clusterName);
  const nameFormatOk = CLUSTER_NAME_RE.test(clusterName);
  const nameCheckReady = nameFormatOk && debouncedName === clusterName;
  const {
    data: nameCheck, isFetching: nameChecking, isError: nameCheckFailed, error: nameCheckError, refetch: recheckName,
  } = useClusterNameCheck(debouncedName, nameCheckReady);
  const nameOk = nameCheckReady && !nameChecking && nameCheck?.available === true;

  const selectedProject = projects.find((p) => p.id === project);
  const selectedTemplate = templates.find((t) => t.id === templateId);
  // 실제로 신청할 노드 — 항목별로 템플릿 값 또는 직접 고른 값
  const nodes = effectiveNodes(flavors, nodeCfg, selectedTemplate ?? { master_flavor_id: null, flavor_id: null });
  const nodesMissing = nodeConfigMissing(nodes);
  // 직접 고른 키 — 템플릿 값이거나 고르지 않았으면 템플릿 키페어
  const chosenKey = nodeCfg.source.key === "custom" ? nodeCfg.keypairId || undefined : undefined;
  const keyName = chosenKey ? myKeypairs.find((k) => k.id === chosenKey)?.name ?? chosenKey : selectedTemplate?.keypair_id || undefined;
  const masterFlavor = flavors.find((f) => f.id === nodes.masterFlavorId);
  const workerFlavor = flavors.find((f) => f.id === nodes.flavorId);
  const totals = nodeTotals(flavors, nodes);
  // 신청 단계 — 템플릿 기본값의 노드 이미지 이름
  const { data: images = [] } = useProjectImages(step === 3 ? project : undefined);

  // 프로젝트를 바꾸면 쓸 수 있는 스펙이 달라지므로 템플릿·노드 선택을 비운다
  function handleProjectSelect(id: string) {
    setProject(id);
    setTemplateId("");
    setNodeCfg(EMPTY_NODE_CONFIG);
  }

  // 템플릿을 (새로) 고르면 노드 구성은 '템플릿 그대로 생성'(모든 항목 템플릿 값)에서 시작한다 — 같은 템플릿이면 고친 값 유지
  function handleTemplateSelect(id: string) {
    if (id === templateId) return;
    setTemplateId(id);
    setNodeCfg(id ? { ...EMPTY_NODE_CONFIG, source: { cp: "tpl", w: "tpl", key: "tpl" } } : EMPTY_NODE_CONFIG);
  }

  const req3 = [nameOk, !!nodes.masterFlavorId, !!nodes.flavorId];
  const valid = [!!title.trim() && purpose.length <= PURPOSE_MAX, !!project && !!templateId,
    nameOk && nodesValid(nodes) && nodesMissing.length === 0, true];
  const reasons = [
    "신청 제목을 입력해야 넘어갈 수 있습니다",
    !project ? "프로젝트를 선택해야 넘어갈 수 있습니다" : "템플릿을 선택해야 넘어갈 수 있습니다",
    !nameOk ? "클러스터명 확인이 끝나야 넘어갈 수 있습니다" : !nodes.masterFlavorId ? "Control Plane 스펙을 선택해야 넘어갈 수 있습니다"
      : "Worker 스펙을 선택해야 넘어갈 수 있습니다",
    "",
  ];
  // 막힌 이유와 같은 순서의 첫 빈 카드 — [다음 단계]로 막히면 그 카드로 옮긴다
  const blockedCard: (ClusterCard | undefined)[] = [
    !title.trim() ? "title" : undefined, !project ? "project" : "template", !nameOk ? "name" : !nodes.masterFlavorId ? "cp" : "w", undefined,
  ];
  const dirty = !submitted && !!(title.trim() || purpose.trim() || project || templateId || clusterName);
  React.useEffect(() => onDirty(dirty), [dirty, onDirty]);

  // 다른 단계의 카드면 그 단계를 그린 다음 스크롤
  function jump(i: number, card: ClusterCard) {
    if (i !== step) setStep(i);
    setTimeout(() => scrollTo(card), i === step ? 0 : 80);
  }
  function next() {
    if (!valid[step]) {
      setTried(step);
      const card = blockedCard[step];
      if (card) scrollTo(card);
      return;
    }
    setStep(step + 1);
  }

  function submit() {
    if (!selectedProject || !selectedTemplate || !valid[2]) return;
    // 이름·템플릿·스펙 정보는 서버가 라이브 조회로 다시 확인해 저장한다 — 화면은 식별자만 보낸다
    createTicket.mutate(
      {
        projectId: selectedProject.id,
        title: title.trim() || undefined,
        clusterName,
        templateId: selectedTemplate.id,
        masterCount: nodes.masterCount,
        masterFlavorId: nodes.masterFlavorId,
        nodeCount: nodes.nodeCount,
        flavorId: nodes.flavorId,
        // 직접 선택 — 사용자는 본인 키(id), 관리자는 OpenStack 키페어(이름). 없으면 템플릿 키페어
        keypairId: isAdmin ? undefined : chosenKey,
        novaKeypair: isAdmin ? chosenKey : undefined,
        purpose: purpose.trim(),
      },
      {
        onSuccess: (t) => {
          toast.success("서비스 클러스터 신청이 접수되었습니다", {
            description: `${clusterName} · Control Plane ${nodes.masterCount} + Worker ${nodes.nodeCount} · 승인 대기 중`,
          });
          setSubmitted({ id: t.id, at: t.requestedAt });
          onSubmitted();
        },
        onError: (e) => toast.error("요청 접수에 실패했습니다", { description: e.message }),
      },
    );
  }

  const done = (i: number) => (submitted ? "done" : i < step ? "done" : i === step ? "cur" : "todo");
  const s1 = valid[0] && step === 1;
  const s2 = valid[0] && valid[1] && step === 2;
  const n3 = req3.filter(Boolean).length;
  const steps: StepItem[] = [
    { no: "01", name: CLUSTER_STEPS[0], state: done(0), onClick: !submitted && step > 0 ? () => setStep(0) : undefined,
      desc: title.trim() || (step === 0 ? "필수 0/1 완료" : "신청 제목 · 용도"), descTone: !title.trim() && step === 0 ? "red" : undefined,
      missing: !submitted && !title.trim() && step === 0 ? [{ label: "신청 제목", onClick: () => jump(0, "title") }] : undefined },
    { no: "02", name: CLUSTER_STEPS[1], state: done(1), onClick: !submitted && step > 1 ? () => setStep(1) : undefined,
      desc: selectedProject && selectedTemplate ? `${selectedProject.name}\n${selectedTemplate.name}` : s1 ? `필수 ${[!!project, !!templateId].filter(Boolean).length}/2 완료` : "프로젝트와 템플릿 선택",
      descTone: s1 && !valid[1] ? "red" : undefined,
      missing: !submitted && s1 ? ([["프로젝트", !project, "project"], ["템플릿", !templateId, "template"]] as const).filter(([, m]) => m)
        .map(([label, , card]) => ({ label, onClick: () => jump(1, card) })) : undefined },
    { no: "03", name: CLUSTER_STEPS[2], state: done(2), onClick: !submitted && step > 2 ? () => setStep(2) : undefined,
      desc: `필수 ${n3}/3 완료`, descTone: s2 && n3 < 3 ? "red" : undefined,
      missing: !submitted && s2 ? ([["클러스터명", "name"], ["Control Plane", "cp"], ["Worker", "w"]] as const).filter((_, i) => !req3[i])
        .map(([label, card]) => ({ label, onClick: () => jump(2, card) })) : undefined },
    { no: "04", name: CLUSTER_STEPS[3], state: done(3), desc: submitted ? `${formatDateTime(submitted.at).slice(11, 16)} 제출` : "제출 전 최종 확인" },
  ];

  const namespace = selectedProject && namespaceName(selectedProject.name, selectedProject.id);
  const reviewData = selectedProject && selectedTemplate && masterFlavor && workerFlavor && {
    title, purpose, projectId: project, projectName: selectedProject.name,
    clusterName, templateName: selectedTemplate.name, kubeVersion: selectedTemplate.kubeVersion,
    namespace, master: nodeView(nodes.masterCount, masterFlavor), worker: nodeView(nodes.nodeCount, workerFlavor),
    workerAccel: flavorAccel(workerFlavor, modelOf), keypair: keyName, keyFromTemplate: !chosenKey && !!keyName,
    templateRows: templateDefaultRows(selectedTemplate, flavors, images, modelOf),
  };

  const summary = (
    <>
      <SumSection title="신청 정보" step="01" onStep={!submitted ? () => setStep(0) : undefined} rows={[["신청 제목", title.trim() || undefined]]} />
      <SumSection title="클러스터" step="02" onStep={!submitted && step >= 1 ? () => setStep(1) : undefined} rows={[
        ["기관", selectedProject?.orgName || undefined], ["프로젝트", selectedProject?.name],
        ["이름", mono(clusterName)], ["템플릿", selectedTemplate?.name],
        ["Kubernetes", selectedTemplate?.kubeVersion && mono(`v${selectedTemplate.kubeVersion}`)],
        ["네임스페이스", namespace && <>{namespace} <span className="text-[11px] text-muted-foreground">(새로 생성)</span></>],
      ]} />
      <SumSection title="노드" step="03" onStep={!submitted && step >= 2 ? () => setStep(2) : undefined} rows={[
        ["Control Plane", masterFlavor ? mono(`${masterFlavor.name} × ${nodes.masterCount}대`) : selectedTemplate ? NOT_YET : undefined],
        ["Worker", workerFlavor ? mono(`${workerFlavor.name} × ${nodes.nodeCount}대`) : selectedTemplate ? NOT_YET : undefined],
        ["키페어", mono(keyName)],
      ]} />
      <SumSection title="총 자원" rows={[
        ["노드", totals && `${totals.vms}대`], ["vCPU", totals && `${totals.vcpu}`], ["RAM", totals && `${totals.ramGb} GB`],
        ["Disk", totals && `${totals.diskGb} GB`],
        ["가속기", totals && workerFlavor && <TotalAccel f={workerFlavor} model={modelOf(workerFlavor.accelerator)} n={nodes.nodeCount} />],
      ]} />
      {/* 노드는 이미지 부팅(로컬 디스크) — Cinder(Disk) 쿼터를 쓰지 않아 Disk 막대는 없다 */}
      <SumSection title={<>프로젝트 쿼터 <span className="font-normal text-muted-foreground">(사용 → 신청 후)</span></>}>
        <QuotaBars projectId={project} add={{ vcpu: totals?.vcpu ?? 0, ramGb: totals?.ramGb ?? 0 }} />
      </SumSection>
    </>
  );

  const footer = (
    // 막힌 이유 — [다음 단계]를 눌러 막혔을 때만(평소에는 떠 있는 진행 알림이 빠진 항목을 보여 준다)
    <StepFooter tone="bad" status={tried[step] && !valid[step] ? reasons[step] : undefined}
      left={<Button variant="outline" onClick={() => setStep(step - 1)} disabled={step === 0}>이전 단계</Button>}>
      {step === 3 ? (
        <Button onClick={submit} disabled={createTicket.isPending}>{createTicket.isPending ? "제출 중…" : "클러스터 신청 제출"}</Button>
      ) : (
        <NextButton blocked={!valid[step] && show(step)} onClick={next} />
      )}
    </StepFooter>
  );

  return (
    <StepperHeader kind="K8s" title="서비스 클러스터 신청" onClose={onClose}
      badge={submitted && <StatusPill tone="done">제출 완료</StatusPill>}
      right={!submitted && <TypeSwitch label="서비스 클러스터" onChange={onChangeType} />}>
      <StepperGrid left={<LeftStepper steps={steps} />} summary={summary}>
        {submitted ? (
          <SubmittedView name={clusterName} id={submitted.id} onClose={onDone} onList={toList} />
        ) : step === 0 ? (
          <StepMain step={["01", "04"]} title="기본 정보" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as ClusterCard)} items={[{ key: "title", label: "신청 제목", ok: !!title.trim() }]} />}>
            <TitleField title={title} onTitle={setTitle} placeholder="예: LLM 평가용 클러스터 신설" missing={tried[0] && !title.trim()} cardRef={cardRef("title")} />
            <PurposeField purpose={purpose} onPurpose={setPurpose} placeholder="예: LLM 평가 서비스 배포" />
          </StepMain>
        ) : step === 1 ? (
          <StepMain step={["02", "04"]} title="프로젝트·템플릿" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as ClusterCard)} items={[
              { key: "project", label: "프로젝트", value: selectedProject?.name, ok: !!project },
              { key: "template", label: "템플릿", value: selectedTemplate?.name, ok: !!templateId },
            ]} />}>
            <ProjectTable projects={projects} loading={projLoading} loadingText={projectsLoadingText(projFails)} error={projError?.message}
              selected={project} onSelect={handleProjectSelect} missing={tried[1] && !project} cardRef={cardRef("project")} />
            <FieldBlock ref={cardRef("template")} label="템플릿" required
              hint="Kubernetes 버전과 기본 노드 스펙은 템플릿이 정합니다. 노드 스펙은 다음 단계에서 바꿀 수 있습니다" missing={tried[1] && !templateId}
              ask="템플릿을 고르세요">
              {!project ? <NeedProject what="템플릿" /> : (
                <>
                  {tplLoading && <p className="text-xs text-muted-foreground">템플릿을 불러오는 중…</p>}
                  {tplError && <LoadError what="클러스터 템플릿" message={tplError.message} />}
                  {!tplLoading && !tplError && templates.length === 0 && (
                    <p className="text-xs text-muted-foreground">신청할 수 있는 클러스터 템플릿이 없습니다. 관리자에게 문의하세요.</p>
                  )}
                  {templates.length > 0 && (
                    <TemplateTable templates={templates} flavors={flavors} flavorsLoading={flavorLoading} modelOf={modelOf} selected={templateId} onSelect={handleTemplateSelect} />
                  )}
                </>
              )}
            </FieldBlock>
          </StepMain>
        ) : step === 2 ? (
          <StepMain step={["03", "04"]} title="노드 구성" desc="필수 항목을 모두 입력해주세요." footer={footer} scrollRef={scrollRef}
            progress={<FloatProgress onJump={(k) => scrollTo(k as ClusterCard)} items={[
              { key: "name", label: "클러스터명", value: nameOk ? clusterName : undefined, ok: nameOk },
              { key: "cp", label: "Control Plane", value: masterFlavor && `${masterFlavor.name} × ${nodes.masterCount}`, ok: !!masterFlavor },
              { key: "w", label: "Worker", value: workerFlavor && `${workerFlavor.name} × ${nodes.nodeCount}`, ok: !!workerFlavor },
              { key: "key", label: "키페어", value: keyName || "사용 안 함", ok: true, optional: true },
            ]} />}>
            {(flavorLoading || keyLoading) && <p className="text-xs text-muted-foreground">스펙·키페어를 불러오는 중…</p>}
            {flavorError && <LoadError what="스펙" message={flavorError.message} />}
            {keyError && <LoadError what="키페어" message={keyError.message} />}
            {!flavorLoading && !keyLoading && !flavorError && !keyError && selectedTemplate && (
              <ClusterNodeCards flavors={flavors} keypairs={myKeypairs} template={selectedTemplate} value={nodeCfg} onChange={setNodeCfg}
                cardRef={(k) => cardRef(k)}
                nameCard={
                  <NameField cardRef={cardRef("name")} icon={<Boxes className="size-4 text-primary" />} title="클러스터명" value={clusterName}
                    onChange={setClusterName} placeholder="예: svc-llm-eval" maxLength={40}
                    status={nameOk ? "done" : clusterName && nameFormatOk && (!nameCheckReady || nameChecking) ? undefined : "missing"}>
                    <NameCheckLine value={clusterName} formatOk={nameFormatOk} ready={nameCheckReady} checking={nameChecking} failed={nameCheckFailed}
                      error={nameCheckError?.message} recheck={() => recheckName()} available={nameCheck?.available} reason={nameCheck?.reason}
                      hint="영문 소문자로 시작하고 소문자·숫자·하이픈(-)만, 40자 이내. 하이픈으로 끝날 수 없습니다."
                      formatError="영문 소문자로 시작하고 소문자·숫자·하이픈(-)만, 40자 이내로 입력하세요. 하이픈으로 끝날 수 없습니다." />
                  </NameField>
                } />
            )}
          </StepMain>
        ) : (
          <StepMain step={["04", "04"]} title="검토·제출" desc={REVIEW_LEAD} footer={footer} scrollRef={scrollRef}>
            {reviewData && (
              <>
                <ClusterCards c={reviewData} onEdit={(k) => setStep(k === "info" ? 0 : k === "cluster" ? 1 : 2)} />
                <QuotaCard projectId={project} add={{ vcpu: totals?.vcpu ?? 0, ramGb: totals?.ramGb ?? 0 }} />
                <FlowLine label="제출 후 진행 순서" steps={CLUSTER_FLOW} current={0} className="flex-none" />
              </>
            )}
          </StepMain>
        )}
      </StepperGrid>
    </StepperHeader>
  );
}
