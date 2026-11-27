"use client";
/**
 * VM 프로비저닝 설정(관리자, 시안 Stepper D) — 승인(프로비저닝) · 실패(설정 바꿔 재시도). 신청 상세 스테퍼의 STEP 03 화면.
 *  고정 진행 줄(네트워크 · 보안그룹 · 배치 노드 + 키페어) + '사용자 희망' 줄 + 한 열 카드:
 *  네트워크(표 · IP 자동/직접 입력 + 중복 확인 · Floating IP + 풀) · 보안그룹(체크 표) · 키페어(신청 값 / OpenStack 키페어 직접 선택,
 *  비울 수도 있음) · 배치 노드(후보 노드 · 용량 막대 · 프로젝트 쿼터).
 * 재시도는 지난 회차 승인 설정으로 채운다. 망·IP·쿼터·키페어는 프로비저닝 요청 때 서버가 다시 확인한다(실패하면 사유가 오류로 온다).
 */
import * as React from "react";
import { AlertCircle, Check, Cpu, Network, RotateCcw, Server, ShieldCheck, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  AccelBadge, CapacityBar, ChangedTag, KeypairTable, NetBadge, Pager, QuotaBar, Radio, RefTag, SearchInput, Segment, SharedTag,
  keyIcon, keyType, paginate, useAccelModel, type KeyRow,
} from "@/components/cluster-node-fields";
import { QuotaBars } from "@/components/request-review";
import { ReasonBanner } from "@/components/request-detail";
import {
  FloatProgress, FormCard, RejectBar, StepFooter, StepMain, StepperGrid, SumSection, progressStep, useCardScroll, type ProgressItem, type StepItem,
} from "@/components/request-stepper";
import {
  useIpCheck, useNovaKeypairs, usePlacementPreview, useProjectNetworks, useProjectSecurityGroups, type PlacementPreview,
} from "@/lib/iaas-api";
import type { ProvNetwork, ProvisionSpec, ResourceRequest } from "@/lib/types";
import { cn } from "@/lib/utils";

function ipInCidr(ip: string, cidr: string) {
  const toNum = (s: string) => {
    const parts = s.split(".");
    if (parts.length !== 4 || parts.some((x) => !/^\d{1,3}$/.test(x) || Number(x) > 255)) return null;
    return parts.reduce((a, x) => a * 256 + Number(x), 0);
  };
  const [base, bitsStr] = cidr.split("/");
  const n = toNum(ip);
  const b = toNum(base);
  const bits = Number(bitsStr);
  if (n === null || b === null) return false;
  const size = 2 ** (32 - bits);
  const start = b - (b % size);
  return n > start && n < start + size - 1;
}

type Card = "net" | "sg" | "key" | "place";
const REQ_KEY = "@request"; // 키페어 표의 신청 키 행(포털 SSH 키 — OpenStack 키페어 이름과 겹치지 않는 값)

/**
 * VM 프로비저닝 설정 화면 — 왼쪽 스테퍼(left) · 오른쪽 요약 머리(summaryTop) · 총 자원(totals)은 신청 상세가 넘긴다.
 * 반려는 바닥 줄이 사유 입력으로 바뀐다(사유 필수). 입력 상태는 반려 입력 중에도 그대로 둔다.
 */
export function VmSetupView({ r, left, summaryTop, totals, rejectFirst, reviewing, provisioning, onReject, onContent, onProvision }: {
  r: ResourceRequest;
  left: (cur?: Partial<StepItem>) => React.ReactNode; // STEP 03 에 필수 n/m · 선택 필요를 덮어써 그린다
  summaryTop: React.ReactNode;
  totals: [string, React.ReactNode][];
  rejectFirst?: boolean;   // 목록의 '반려' 버튼으로 열면 반려 사유 입력부터
  reviewing?: boolean;
  provisioning?: boolean;
  onReject?: (reason: string) => void;
  onContent?: () => void;  // 바닥 왼쪽 '신청 내용 보기(전체)'
  onProvision?: (spec: ProvisionSpec) => void; // host 포함(노드 지정 권한이 있을 때만)
}) {
  const isFailed = r.status === "failed";
  const [rejecting, setRejecting] = React.useState(!!rejectFirst);
  const [tried, setTried] = React.useState(false); // [프로비저닝]을 눌러 막혔는지
  const { scrollRef, cardRef, scrollTo } = useCardScroll<Card>();

  const { data: networks = [], isLoading: netLoading, error: netError } = useProjectNetworks(r.projectId);
  const { data: sgs = [], isLoading: sgLoading, error: sgError } = useProjectSecurityGroups(r.projectId);
  const externals = networks.filter((n) => n.kind === "external");

  // 재시도(실패한 신청)면 이전 회차의 승인 설정으로 채워 두고 고칠 수 있게 한다
  const prev = r.provisionSpec;
  const [networkId, setNetworkId] = React.useState(prev?.networkId ?? "");
  const [fipOn, setFipOn] = React.useState(prev ? !!prev.floatingNetworkId : !!r.floatingIp);
  const [fipNetId, setFipNetId] = React.useState(prev?.floatingNetworkId ?? "");
  const [ipManual, setIpManual] = React.useState(!!prev?.fixedIp);
  const [fixedIp, setFixedIp] = React.useState(prev?.fixedIp ?? "");
  const [sgNames, setSgNames] = React.useState<string[]>(prev?.securityGroups ?? ["default"]);
  const [place, setPlace] = React.useState<PlacementSummary>({ ok: false, pin: null, loading: true, candidates: 0, total: 0 });
  // 키페어 — 신청 값 또는 OpenStack 키페어 직접 선택(null = 넣지 않음). 재시도면 지난 회차에 바꾼 키페어부터(기록이 없으면 신청 값)
  const { data: novaKeys, isLoading: keysLoading, error: keysError } = useNovaKeypairs();
  const noCloudInit = r.cloudInit === false;
  const reqNova = r.keypairId ? undefined : r.novaKeypair ?? null; // 신청 키가 OpenStack 키페어면 그 이름(포털 SSH 키면 undefined)
  const [keySrc, setKeySrc] = React.useState<"req" | "custom">(prev && "novaKeypair" in prev ? "custom" : "req");
  const [keyPick, setKeyPick] = React.useState<string | null>(prev?.novaKeypair ?? null);

  const net = networks.find((n) => n.id === networkId);
  const isExternal = net?.kind === "external";
  const fipNet = externals.find((n) => n.id === fipNetId) ?? externals[0];
  const ipInRange = !!net && net.cidr.split(", ").some((c) => ipInCidr(fixedIp, c)); // 서브넷이 여러 개면 ", " 로 연결됨

  // 직접 입력 IP — '중복 확인' 버튼을 누른 IP 로 사용 여부 조회. 입력을 바꾸면 다시 확인해야 한다.
  const [checkedIp, setCheckedIp] = React.useState("");
  const ipCheckReady = ipManual && ipInRange && checkedIp === fixedIp;
  const {
    data: ipCheck, isFetching: ipChecking, isError: ipCheckFailed, error: ipCheckError, refetch: recheckIp,
  } = useIpCheck(networkId, checkedIp, ipCheckReady);
  const ipValid = !ipManual || (ipCheckReady && !ipChecking && ipCheck?.available === true);

  // 필수 — 네트워크(IP·Floating IP 풀까지), 보안그룹(1개 이상), 배치(쿼터 안 + 고른 노드가 후보)
  const fipOk = isExternal || !fipOn || !!fipNet;
  const netOk = !!net && ipValid && fipOk;
  const sgOk = sgNames.length > 0;
  const placeOk = place.ok && place.pin?.candidate !== false;
  // 키페어(옵션) — 넣을 OpenStack 키페어가 지금 없으면(지웠으면) 막는다. 신청 값이 포털 SSH 키면 서버가 확인한다
  const keyChanged = !noCloudInit && keySrc === "custom" && keyPick !== reqNova;
  const keyNova = keySrc === "custom" ? keyPick : reqNova;
  const keyMissing = !!keyNova && !!novaKeys && !novaKeys.some((k) => k.name === keyNova);
  const keyName = keySrc === "custom" ? keyPick : r.keypairName ?? null;
  const keyValue = noCloudInit ? "미적용 (cloud-init 없음)" : keyName ?? "사용 안 함";
  const spec: ProvisionSpec | null = netOk && sgOk && placeOk && !keyMissing ? {
    networkId: net.id,
    networkName: net.name,
    networkKind: net.kind,
    fixedIp: ipManual ? fixedIp : undefined, // 직접 입력 — 망 종류와 무관
    ...(!isExternal && fipOn && fipNet ? { floatingNetworkId: fipNet.id, floatingNetworkName: fipNet.name } : {}),
    securityGroups: sgNames,
    host: place.pin?.host,
    ...(keyChanged ? { novaKeypair: keyPick } : {}),
  } : null;
  // 막힌 이유와 첫 빈 카드 — [프로비저닝]을 눌러 막히면 바닥에 빨갛게 보이고 그 카드로 옮긴다(평소에는 진행 알림이 빠진 항목을 보여 준다)
  const verb = isFailed ? "재시도" : "프로비저닝";
  const blocked: [Card, string] | null = spec ? null
    : !net ? ["net", `네트워크를 골라야 ${verb}할 수 있습니다`]
    : !ipValid ? ["net", `IP 중복 확인이 끝나야 ${verb}할 수 있습니다`]
    : !fipOk ? ["net", `Floating IP 풀을 골라야 ${verb}할 수 있습니다`]
    : !sgOk ? ["sg", `보안그룹을 하나 이상 골라야 ${verb}할 수 있습니다`]
    : !placeOk ? ["place", place.loading ? "배치·가용량을 확인하는 중입니다" : `배치 노드 — ${place.problem ?? "확인이 필요합니다"}`]
    : ["key", `OpenStack 에 없는 키페어입니다 — 다른 키페어를 골라야 ${verb}할 수 있습니다`];
  const status = spec ? `VM 1대 ${verb} 준비됨${place.pin ? ` / ${place.pin.host}` : ""}`
    : tried ? blocked?.[1]
    : place.loading && netOk && sgOk && !keyMissing ? "배치·가용량을 확인하는 중…" : undefined;

  const netLine = !net ? undefined
    : [ipManual ? (ipValid ? `고정 IP ${fixedIp}` : "IP 중복 확인이 필요합니다") : "IP 자동 할당(DHCP)",
      !isExternal && fipOn && (fipNet ? `Floating IP ${fipNet.name}` : "Floating IP 풀이 없습니다")].filter(Boolean).join(" / ");

  // 키페어 행 — OpenStack(Nova) 키페어(이름으로 찾아 공개키를 넣는다). 신청 키는 포털 SSH 키 또는 OpenStack 키페어
  const keyRows: KeyRow[] = (novaKeys ?? []).map((k) => ({ id: k.name, name: k.name, fingerprint: k.fingerprint, type: k.type || keyType(k.publicKey) }));
  const reqKeyRow: KeyRow | undefined = !r.keypairName ? undefined
    : r.keypairId ? { id: REQ_KEY, name: r.keypairName, fingerprint: "신청자 SSH 키 — 포털에 등록한 공개키", type: "—" }
    : keyRows.find((k) => k.id === r.keypairName)
      ?? { id: r.keypairName, name: r.keypairName, fingerprint: novaKeys ? "OpenStack 에 없음 — 직접 선택으로 다른 키페어를 고르세요" : "—", type: "—" };

  const summary = (
    <>
      {summaryTop}
      <SumSection title="사용자 희망" rows={[
        ["네트워크(희망)", r.networkType === "external" ? "인터넷망" : "내부망"], ["Floating IP", r.floatingIp ? "희망" : "희망 안 함"],
        ["키페어", r.keypairName || undefined],
      ]} />
      <SumSection title="총 자원" rows={totals} />
      <SumSection title={<>프로젝트 쿼터 <span className="font-normal text-muted-foreground">({isFailed ? "지금 기준" : "사용 → 신청 후"})</span></>}>
        <QuotaBars projectId={r.projectId} add={{ vcpu: r.vcpu ?? 0, ramGb: r.ramGb ?? 0, diskGb: r.volumeGb || r.diskGb || 0 }} />
      </SumSection>
    </>
  );

  const progress: ProgressItem[] = [
    { key: "net", label: "네트워크", value: net?.name, ok: netOk },
    { key: "sg", label: "보안그룹", value: sgNames.join(", "), ok: sgOk },
    { key: "place", label: "배치 노드", value: place.loading ? undefined : place.pin?.host ?? (place.ok ? "스케줄러" : undefined), ok: placeOk },
    { key: "key", label: "키페어", value: noCloudInit ? "미적용" : keyName ?? "사용 안 함", ok: !keyMissing, optional: true },
  ];
  const jump = (k: string) => scrollTo(k as Card);

  return (
    <StepperGrid left={left(progressStep(progress, jump))} summary={summary} summaryTitle="신청 요약">
      <StepMain step={["03", "04"]} title={isFailed ? "프로비저닝 설정 · 재시도" : "프로비저닝 설정"} scrollRef={scrollRef}
        desc={isFailed ? "지난 회차 설정으로 채웠습니다. 확인·수정해 재시도하거나 반려하세요." : "네트워크·보안그룹을 고르고 배치·키페어를 확인한 뒤 프로비저닝하거나 반려하세요."}
        progress={<FloatProgress onJump={jump} items={progress} />}
        footer={rejecting ? (
          <RejectBar onCancel={() => setRejecting(false)} onConfirm={(reason) => onReject?.(reason)} busy={reviewing} />
        ) : (
          <StepFooter status={status} tone={spec ? "ok" : tried ? "bad" : "muted"}
            left={onContent && <Button variant="outline" onClick={onContent}>신청 내용 보기(전체)</Button>}>
            <Button variant="outline" className="border-rose-200 text-rose-600 hover:bg-rose-50" onClick={() => setRejecting(true)}>
              <XCircle className="size-3.5" /> 반려
            </Button>
            <Button onClick={() => { if (spec) onProvision?.(spec); else { setTried(true); if (blocked) scrollTo(blocked[0]); } }}
              disabled={provisioning} className={cn(!spec && "bg-brand-indigo-300 hover:bg-brand-indigo-300")}>
              {isFailed ? <RotateCcw className="size-3.5" /> : <Server className="size-3.5" />}
              {provisioning ? "시작 중…" : isFailed ? "재시도" : "프로비저닝"}
            </Button>
          </StepFooter>
        )}>
        <ReasonBanner r={r} />
        <div className="flex flex-none items-center gap-3 rounded-[10px] border border-border bg-secondary/40 px-4 py-2.5 text-[13px]">
          <span className="font-semibold">사용자 희망</span>
          <NetBadge type={r.networkType === "external" ? "internet" : "internal"} />
          <span className="text-muted-foreground">{r.floatingIp ? "공인 IP 희망" : "공인 IP 희망 안 함"}</span>
        </div>
        <FormCard ref={cardRef("net")} icon={<Network className="size-4 text-primary" />} title="네트워크" required
          status={netOk ? "done" : "missing"} value={net?.name} line={netLine}>
          <NetworkTable networks={networks} loading={netLoading} error={netError?.message} selected={networkId}
            onSelect={(id) => { setNetworkId(networkId === id ? "" : id); setIpManual(false); setFixedIp(""); setCheckedIp(""); }} />
          {net && (
            <>
              <div className="flex items-center gap-3">
                <span className="text-sm font-semibold">IP</span>
                <span className="flex-1 text-xs text-muted-foreground">{ipManual ? `${net.cidr} 범위의 호스트 IP` : "DHCP 로 자동 할당합니다"}</span>
                <Segment options={[["auto", "자동 할당"], ["manual", "직접 입력"]] as const} value={ipManual ? "manual" : "auto"}
                  onChange={(v) => setIpManual(v === "manual")} />
              </div>
              {ipManual && (
                <div className="grid gap-1.5">
                  <div className="flex items-center gap-2">
                    <Input value={fixedIp} onChange={(e) => setFixedIp(e.target.value.trim())}
                      placeholder={net.cidr.split("/")[0].replace(/\d+$/, "10")} className="h-9 w-48 bg-card font-mono text-[13px]" />
                    <Button type="button" variant="outline" className="h-9"
                      disabled={!ipInRange || (ipChecking && checkedIp === fixedIp)}
                      onClick={() => (checkedIp === fixedIp ? recheckIp() : setCheckedIp(fixedIp))}>
                      중복 확인
                    </Button>
                  </div>
                  {fixedIp && (
                    !ipInRange ? <p className="text-xs text-rose-500">{net.cidr} 범위의 호스트 IP를 입력하세요.</p>
                      : !ipCheckReady ? <p className="text-xs text-muted-foreground">중복 확인을 눌러 IP 사용 여부를 확인하세요.</p>
                      : ipChecking ? <p className="text-xs text-muted-foreground">IP 사용 여부 확인 중…</p>
                      : ipCheckFailed ? <p className="text-xs text-rose-500">IP 중복 확인에 실패했습니다{ipCheckError?.message ? ` (${ipCheckError.message})` : ""}. 중복 확인을 다시 눌러 주세요.</p>
                      : ipCheck?.available ? <p className="text-xs text-brand-mint-700">사용 가능한 IP입니다.</p>
                      : <p className="text-xs text-rose-500">{ipCheck?.reason ?? "사용할 수 없는 IP입니다."}</p>
                  )}
                </div>
              )}
              {/* Floating IP — 내부·공유망만(외부망은 받은 IP 가 곧 공인 IP) */}
              {!isExternal && (
                <>
                  <div className="flex items-center gap-3">
                    <span className="text-sm font-semibold">Floating IP</span>
                    <span className="flex-1 text-xs text-muted-foreground">밖에서 접속할 공인 IP(외부망 풀에서)</span>
                    <label className="flex cursor-pointer items-center gap-2 text-xs font-medium">
                      <input type="checkbox" checked={fipOn} onChange={(e) => setFipOn(e.target.checked)} className="size-3.5 accent-primary" />
                      연결
                    </label>
                  </div>
                  {fipOn && (externals.length ? (
                    <Select value={fipNet?.id ?? ""} onValueChange={setFipNetId}>
                      <SelectTrigger className="h-9 w-64 bg-card font-mono text-xs"><SelectValue placeholder="FIP 풀 (외부망)" /></SelectTrigger>
                      <SelectContent>
                        {externals.map((n) => <SelectItem key={n.id} value={n.id} className="font-mono text-xs">{n.name} / {n.cidr}</SelectItem>)}
                      </SelectContent>
                    </Select>
                  ) : <p className="text-xs text-rose-500">Floating IP 풀(외부망)이 없습니다. 연결을 끄거나 외부망을 준비하세요.</p>)}
                </>
              )}
            </>
          )}
        </FormCard>
        <FormCard ref={cardRef("sg")} icon={<ShieldCheck className="size-4 text-emerald-600" />} title="보안그룹" required
          status={sgOk ? "done" : "missing"} value={sgOk ? sgNames.join(", ") : undefined} ask="하나 이상 고르세요">
          <SecurityGroupTable groups={sgs} loading={sgLoading} error={sgError?.message} selected={sgNames}
            onToggle={(name) => setSgNames((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]))} />
        </FormCard>
        {/* 배치 노드 — 막힌 이유는 값(고른 노드)이 없으면 머리 오른쪽 안내로, 있으면 아래 줄로 */}
        <FormCard ref={cardRef("place")} icon={<Server className="size-4 text-rose-600" />} title="배치 노드" required
          status={place.loading ? undefined : placeOk ? "done" : "missing"}
          value={place.loading ? "확인 중…" : place.pin ? place.pin.host : place.ok ? "스케줄러가 후보 중에서 고름" : undefined}
          valueMuted={place.loading || (!place.pin && place.ok)}
          ask={place.problem}
          line={place.loading ? undefined : place.problem ? (place.pin || place.ok ? place.problem : undefined) : `후보 ${place.candidates}대 / 전체 ${place.total}대`}>
          <PlacementView r={r} onState={setPlace} />
        </FormCard>
        <FormCard ref={cardRef("key")} icon={keyIcon} title="키페어" optional
          tag={noCloudInit ? undefined : keyChanged ? <ChangedTag /> : <RefTag label="신청 값" />}
          status={keyMissing ? "missing" : undefined} value={keyValue} valueMuted={noCloudInit || keyName === null}
          line={noCloudInit ? "이 템플릿은 키페어를 넣을 수 없습니다"
            : keyMissing ? "OpenStack 에 없는 키페어입니다. 직접 선택에서 다른 키페어를 고르세요"
            : keyName === null ? "VM 에 SSH 키를 넣지 않습니다"
            : keyChanged ? <span className="font-mono text-[#9aa3ad] line-through">신청 {r.keypairName ?? "사용 안 함"}</span>
            : keySrc === "req" && r.keypairId ? "신청자 SSH 키" : "OpenStack 키페어"}
          action={!noCloudInit && (
            <Segment options={[["req", "신청 값"], ["custom", "직접 선택"]] as const} value={keySrc}
              onChange={(s) => { setKeySrc(s); if (s === "custom") setKeyPick(reqNova ?? null); }} />
          )}>
          {noCloudInit ? null : keySrc === "req" ? (
            <KeypairTable key="key-req" rows={reqKeyRow ? [reqKeyRow] : []} selected={reqKeyRow?.id ?? null} refId={reqKeyRow?.id}
              refLabel="신청" nameCol="minmax(0,200px)" empty="신청 키페어가 없습니다 — VM 에 SSH 키를 넣지 않습니다" />
          ) : keysLoading ? <Skeleton className="h-24" /> : keysError ? (
            <p className="text-xs text-rose-500">OpenStack 키페어를 불러오지 못했습니다 ({keysError.message}).</p>
          ) : (
            <KeypairTable key="key-custom" rows={keyRows} selected={keyPick} refId={reqNova} refLabel="신청" nameCol="minmax(0,200px)"
              paged onSelect={(id) => setKeyPick(keyPick === id ? null : id)} empty="OpenStack 키페어가 없습니다" />
          )}
        </FormCard>
      </StepMain>
    </StepperGrid>
  );
}

// ── 고르기 표 ─────────────────────────────────────────────────────

const NET_COLS = "grid grid-cols-[22px_minmax(0,1fr)_minmax(0,1.2fr)_168px] items-center gap-[14px] px-4";
const SG_COLS = "grid grid-cols-[22px_minmax(0,160px)_minmax(0,1fr)] items-center gap-[14px] px-4";

/** 네트워크 표 — 검색 · 4개씩. 유형: 외부망은 인터넷망, 그 밖은 서브넷마다 인터넷망(외부 게이트웨이 라우터)·내부망. 다시 누르면 해제. */
function NetworkTable({ networks, loading, error, selected, onSelect }: {
  networks: ProvNetwork[]; loading: boolean; error?: string; selected: string; onSelect: (id: string) => void;
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
            return (
              <div key={n.id} onClick={() => onSelect(n.id)}
                className={cn(NET_COLS, "h-[46px] cursor-pointer border-b border-border/60 last:border-b-0",
                  sel ? "bg-brand-indigo-50/60" : "hover:bg-secondary/40")}>
                <Radio on={sel} />
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate font-mono text-[13px] font-semibold">{n.name}</span>
                  {n.shared && <SharedTag />}
                </span>
                <span title={n.cidr} className="truncate font-mono text-[11px] text-muted-foreground">{n.cidr || "—"}</span>
                <span className="flex flex-wrap gap-1">
                  {n.kind === "external" ? <NetBadge type="internet" />
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

/** 보안그룹 표 — 검색 · 4개씩, 여러 개 고름(다시 누르면 해제). */
function SecurityGroupTable({ groups, loading, error, selected, onToggle }: {
  groups: { id: string; name: string; description?: string }[]; loading: boolean; error?: string; selected: string[];
  onToggle: (name: string) => void;
}) {
  const [query, setQuery] = React.useState("");
  const [page, setPage] = React.useState<number | null>(null);
  const q = query.trim().toLowerCase();
  const all = groups.filter((g) => g.name.toLowerCase().includes(q));
  const { rows, page: pageNow, pages } = paginate(all, page, (g) => selected.includes(g.name));
  return (
    <>
      <SearchInput value={query} onChange={(v) => { setQuery(v); setPage(0); }} placeholder="보안그룹 이름 검색" />
      <div className="flex flex-col overflow-hidden rounded-[10px] border border-border bg-card">
        <div className={cn(SG_COLS, "border-b border-border bg-secondary/50 py-[9px] text-xs text-muted-foreground")}>
          <span /><span>이름</span><span>설명</span>
        </div>
        <div className="h-[184px] border-b border-border/60">
          {loading ? <Skeleton className="m-3 h-20" /> : error ? (
            <div className="p-5 text-center text-[13px] text-rose-600">보안그룹을 불러오지 못했습니다 ({error}).</div>
          ) : rows.map((g) => {
            const on = selected.includes(g.name);
            return (
              <div key={g.id} onClick={() => onToggle(g.name)}
                className={cn(SG_COLS, "h-[46px] cursor-pointer border-b border-border/60 last:border-b-0",
                  on ? "bg-brand-indigo-50/60" : "hover:bg-secondary/40")}>
                <span className={cn("flex size-4 items-center justify-center rounded-[4px] border-[1.6px]",
                  on ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/40")}>
                  {on && <Check className="size-3" />}
                </span>
                <span className="truncate font-mono text-[13px] font-semibold">{g.name}</span>
                <span title={g.description} className="truncate text-xs text-muted-foreground">{g.description || "—"}</span>
              </div>
            );
          })}
          {!loading && !error && rows.length === 0 && <div className="p-5 text-center text-[13px] text-muted-foreground">{groups.length ? "조건에 맞는 보안그룹이 없습니다" : "보안그룹이 없습니다"}</div>}
        </div>
        <Pager total={all.length} page={pageNow} pages={pages} onPage={setPage} />
      </div>
    </>
  );
}

// ── 배치·가용량 미리보기 ─────────────────────────────────────────────

// 배치 판정 요약 — 왼쪽 카드·준비 판정용. ok: 미리보기를 읽었고 프로젝트 쿼터를 넘지 않음,
// pin: 노드 지정 권한이 있으면 고른 노드(프로비저닝 때 넘김, 권한이 없으면 null → 스케줄러가 고름), problem: 막힌 이유
type PlacementSummary = {
  ok: boolean; pin: PlacementPreview["nodes"][number] | null; loading: boolean; candidates: number; total: number; problem?: string;
};

// 배치·가용량 미리보기(관리자 판단용) — 후보 노드(Placement, Nova 스케줄러와 같은 질의) + 프로젝트 쿼터. MNT-IAAS 라이브 조회.
// 실제 노드는 스케줄러가 후보 중에서 정하므로 노드마다 탭으로 보여준다(처음엔 여유가 가장 큰 후보). 후보가 아닌 노드는 '불가'와 이유.
function PlacementView({ r, onState }: { r: ResourceRequest; onState: (s: PlacementSummary) => void }) {
  const { data, isLoading, error } = usePlacementPreview(r.flavorId, r.projectId);
  const modelOf = useAccelModel();
  const [host, setHost] = React.useState(r.provisionSpec?.host); // 재시도면 지난 회차에 고른 노드부터
  const addVcpu = r.vcpu ?? 0;
  const addRamGb = r.ramGb ?? 0;
  const bootGb = r.volumeGb || r.diskGb || 0; // 부팅 볼륨 = 플레이버 디스크(0이면 신청 입력값)
  const gb = (mb: number) => Math.round(mb / 1024);
  const cur = data?.nodes.find((c) => c.host === host) ?? data?.nodes[0]; // 다시 조회해 빠진 노드면 첫 노드
  const candidates = data?.nodes.filter((c) => c.candidate) ?? [];
  const pinned = data?.hostPinning && cur ? cur : null;
  // 프로젝트 쿼터 — 막대와 초과 판정이 같은 목록을 쓴다(무제한 -1 은 초과 없음)
  const quotaRows = data?.quota ? [
    { label: "vCPU", q: data.quota.vcpu, add: addVcpu, unit: "vCPU" },
    { label: "메모리", q: data.quota.ramGb, add: addRamGb, unit: "GB" },
    { label: "인스턴스", q: data.quota.instances, add: 1, unit: "개" },
    { label: "볼륨 용량", q: data.quota.diskGb, add: bootGb, unit: "GB" },
    { label: "볼륨 수", q: data.quota.volumes, add: 1, unit: "개" },
  ] : [];
  const quotaOver = quotaRows.filter((x) => x.q.quota >= 0 && x.q.used + x.add > x.q.quota).map((x) => x.label);
  const ok = !!data && quotaOver.length === 0;
  const accelShort = data?.accelerators.find((a) => a.free < a.count);
  const model = modelOf(r.deviceProfile);
  const problem = error ? "가용량을 불러오지 못했습니다"
    : !data ? undefined
    : quotaOver.length ? `프로젝트 쿼터 초과(${quotaOver.join(", ")})`
    : candidates.length === 0 ? (accelShort ? `${accelShort.kind} 여유가 있는 노드가 없습니다` : "vCPU·메모리 여유가 있는 노드가 없습니다")
    : pinned && !pinned.candidate ? "후보 노드를 고르세요"
    : undefined;
  React.useEffect(
    () => onState({ ok, pin: pinned, loading: isLoading, candidates: candidates.length, total: data?.nodes.length ?? 0, problem }),
    [ok, pinned, isLoading, candidates.length, data?.nodes.length, problem, onState],
  );
  const blocked = (c: PlacementPreview["nodes"][number]) => { // 후보가 아닌 이유 — 막대와 같은 기준
    const why = [
      c.disabled && "꺼 둔 노드",
      c.vcpu.used + addVcpu > c.vcpu.capacity && "vCPU 부족",
      c.ramMb.used + addRamGb * 1024 > c.ramMb.capacity && "메모리 부족",
      ...(data?.accelerators ?? []).map((a, i) =>
        c.accel[i].capacity - c.accel[i].used < a.count && (c.accel[i].capacity ? `${a.kind} 여유 부족` : `${a.kind} 장치 없음`)),
    ].filter(Boolean);
    return why.length ? why.join(", ") : "배치 조건 불충족";
  };

  if (isLoading) return <Skeleton className="h-24" />;
  // '가용량을 불러오지 못했습니다'는 카드 요약 줄(problem)이 이미 보여 준다 — 본문은 원인과 결과만
  if (error) return <p className="text-xs text-rose-500">{error.message} — 확인할 수 없어 프로비저닝할 수 없습니다.</p>;
  if (!data) return null;
  return (
    <div className="space-y-2.5">
      <div className="flex items-center justify-between text-xs">
        <span className="text-muted-foreground">후보 노드</span>
        <span className="font-mono">{candidates.length}대 <span className="text-muted-foreground">/ 전체 {data.nodes.length}대</span></span>
      </div>
      {!data.hostPinning && candidates.length > 0 && (
        <p className="flex items-center gap-1 text-[11px] text-amber-700">
          <AlertCircle className="size-3.5 shrink-0" />자원풀 권한이 부족하여 후보 노드중 하나에 스케줄링 됩니다.
        </p>
      )}
      {candidates.length === 0 && (
        <div className="flex items-start gap-2 rounded-md border border-rose-200 bg-rose-50 p-2.5 text-xs text-rose-700">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          {accelShort
            ? `요청한 가속기(${accelShort.kind}${model ? `(${model})` : ""} × ${accelShort.count}${r.deviceProfile ? ` / ${r.deviceProfile}` : ""}) 여유가 있는 노드가 없습니다 — 전체 ${accelShort.total}장 중 여유 ${accelShort.free}장. 반려하거나 회수 후 다시 시도하세요.`
            : "vCPU·메모리 여유가 있는 노드가 없습니다."}
        </div>
      )}
      {cur && (
        <Tabs value={cur.host} onValueChange={setHost}>
          <TabsList className="h-auto max-w-full flex-wrap justify-start">
            {data.nodes.map((c) => (
              <TabsTrigger
                key={c.host} value={c.host}
                className={cn(
                  "group px-2.5 font-mono text-xs data-[state=active]:bg-primary data-[state=active]:font-semibold data-[state=active]:text-primary-foreground",
                  !c.candidate && "text-muted-foreground/70",
                )}
              >
                {c.host}
                {!c.candidate && <span className="font-sans text-[10px] text-rose-500 group-data-[state=active]:text-primary-foreground/80">불가</span>}
              </TabsTrigger>
            ))}
          </TabsList>
          {data.nodes.map((c) => (
            <TabsContent key={c.host} value={c.host} className="mt-2.5 space-y-2.5">
              {!c.candidate && (
                <p className="flex items-center gap-1 text-[11px] text-rose-600">
                  <AlertCircle className="size-3.5 shrink-0" />배치 불가 — {blocked(c)}{data.hostPinning && " · 후보 노드를 골라야 프로비저닝할 수 있습니다"}
                </p>
              )}
              <CapacityBar label="vCPU" used={c.vcpu.used} add={addVcpu} cap={c.vcpu.capacity} unit="vCPU" />
              <CapacityBar label="메모리" used={gb(c.ramMb.used)} add={addRamGb} cap={gb(c.ramMb.capacity)} unit="GB" />
              {data.accelerators.map((a, i) => (
                <CapacityBar
                  key={`${a.resourceClass}-${i}`}
                  label={
                    <span className="flex min-w-0 items-center gap-1.5">
                      가속기
                      <AccelBadge kind={a.kind} model={model} count={a.count} />
                      <span className="truncate font-mono text-muted-foreground">
                        {r.deviceProfile}{a.traits.length > 0 && `(${a.traits.join(", ")})`}
                      </span>
                    </span>
                  }
                  used={c.accel[i].used} add={a.count} cap={c.accel[i].capacity} unit="장"
                />
              ))}
            </TabsContent>
          ))}
        </Tabs>
      )}
      {bootGb > 0 && (
        <div className="flex items-center justify-between text-[11px]">
          <span className="text-muted-foreground">부팅 볼륨</span>
          <span className="font-mono">{bootGb} GB · Cinder 풀 할당</span>
        </div>
      )}

      <div className="space-y-2 border-t border-border pt-2.5">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold text-muted-foreground"><Cpu className="size-3" />프로젝트 쿼터</div>
        {data.quotaError ? (
          <p className="text-xs text-rose-500">쿼터를 불러오지 못했습니다 ({data.quotaError}).</p>
        ) : (
          <>
            {quotaOver.length > 0 && (
              <p className="flex items-start gap-1 text-[11px] text-rose-600">
                <AlertCircle className="mt-px size-3.5 shrink-0" />
                프로젝트 쿼터({quotaOver.join(", ")})를 초과해 프로비저닝할 수 없습니다. 쿼터를 늘리거나 반려하세요.
              </p>
            )}
            {quotaRows.map((x) => <QuotaBar key={x.label} label={x.label} q={x.q} add={x.add} unit={x.unit} />)}
          </>
        )}
      </div>
    </div>
  );
}
