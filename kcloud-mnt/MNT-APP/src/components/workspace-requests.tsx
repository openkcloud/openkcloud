"use client";
/* AI 워크스페이스 신청 — 포털 자원신청 티켓(MNT-CORE, type "ai-workspace")으로 처리한다.
 * 사용자: 소속 프로젝트 하나를 골라 신청 (워크스페이스 = 프로젝트 1:1, 이름은 서버가 정한다)
 * 관리자: 신청 관리(/ticket/requests)에서 승인·반려 → 생성 시작(한도 조정 가능) → 진행 확인
 * 여기에는 신청 폼(자원 신청 창의 탭 · 워크스페이스 화면의 신청 창 공용), 워크스페이스 화면의 신청 현황,
 * 한도 입력칸(신청 · 관리자 생성 시작 공용)을 둔다. */
import * as React from "react";
import Link from "next/link";
import { toast } from "sonner";
import { ArrowUpRight, Inbox, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAuth } from "@/lib/auth";
import { useAinbClusters, useProfiles } from "@/lib/ainb-api";
import { useClusters } from "@/lib/paas-api";
import { projectsLoadingText, useCreateAiWorkspaceTicket, useTicketProjects, useTickets } from "@/lib/ticket-api";
import type { RequestStatus, ResourceRequest } from "@/lib/types";
import { formatDateTime } from "@/lib/utils";

/* ── 한도 (vCPU·메모리·가속기·저장) ─────────────────────────── */
export interface WorkspaceLimits { cpu?: number; memoryGi?: number; acceleratorCount?: number; storageGi?: number }
/** 한도 입력값 — 입력칸 그대로(문자열) 두고 보낼 때 숫자로 바꾼다 */
export interface WorkspaceLimitsInput { cpu: string; memoryGi: string; acceleratorCount: string; storageGi: string }

/** 티켓의 한도 — 관리자가 생성 시작 때 조정했으면 그 값(provisionSpec), 아니면 신청 값 */
export function limitsOf(r: ResourceRequest): WorkspaceLimits {
  const l = (r.provisionSpec as unknown as WorkspaceLimits | null | undefined) ?? r;
  return { cpu: l.cpu, memoryGi: l.memoryGi, acceleratorCount: l.acceleratorCount, storageGi: l.storageGi };
}

export function limitsText(l: WorkspaceLimits) {
  return `vCPU ${l.cpu ?? "—"} / 메모리 ${l.memoryGi ?? "—"} GB / 가속기 ${l.acceleratorCount ?? 0}장 / 저장 ${l.storageGi ?? "—"} GB`;
}

export function toLimitsInput(l: WorkspaceLimits): WorkspaceLimitsInput {
  return {
    cpu: String(l.cpu ?? ""),
    memoryGi: String(l.memoryGi ?? ""),
    acceleratorCount: String(l.acceleratorCount ?? 0),
    storageGi: String(l.storageGi ?? ""),
  };
}

/** vCPU·메모리·저장은 1 이상, 가속기는 0 이상의 정수 */
export function limitsOk(v: WorkspaceLimitsInput) {
  const int = (x: string) => x.trim() !== "" && Number.isInteger(Number(x));
  return [v.cpu, v.memoryGi, v.storageGi].every((x) => int(x) && Number(x) > 0)
    && int(v.acceleratorCount) && Number(v.acceleratorCount) >= 0;
}

export function limitsBody(v: WorkspaceLimitsInput) {
  return {
    cpu: Number(v.cpu),
    memoryGi: Number(v.memoryGi),
    acceleratorCount: Number(v.acceleratorCount),
    storageGi: Number(v.storageGi),
  };
}

/** 한도 입력칸 4개 — 신청 폼과 관리자 생성 시작(한도 조정)이 함께 쓴다 */
export function WorkspaceLimitFields({
  value, onChange,
}: { value: WorkspaceLimitsInput; onChange: (v: WorkspaceLimitsInput) => void }) {
  const field = (key: keyof WorkspaceLimitsInput, label: string, min: number) => (
    <div className="space-y-1.5">
      <Label htmlFor={`ws-${key}`}>{label}</Label>
      <Input id={`ws-${key}`} type="number" min={min} value={value[key]}
             onChange={(e) => onChange({ ...value, [key]: e.target.value })} />
    </div>
  );
  return (
    <div className="grid grid-cols-4 gap-2">
      {field("cpu", "vCPU", 1)}
      {field("memoryGi", "메모리 (GB)", 1)}
      {field("acceleratorCount", "가속기 (장)", 0)}
      {field("storageGi", "저장 (GB)", 1)}
    </div>
  );
}

/* ── 신청 폼 (사용자) ──────────────────────────────────────── */
const LIMITS_DEFAULT: WorkspaceLimitsInput = { cpu: "4", memoryGi: "16", acceleratorCount: "1", storageGi: "50" };

/**
 * 자원 신청 창의 'AI 워크스페이스 신청' 탭과 워크스페이스 화면의 신청 창이 함께 쓴다.
 * onDirty: 입력 여부(프로젝트 · 사용 목적을 넣었거나 한도를 기본값에서 바꿈) — 자원 신청 창의 닫기 확인 · 유형 선택 복귀용
 */
export function WorkspaceRequestForm({ onDone, onDirty }: { onDone: () => void; onDirty?: (dirty: boolean) => void }) {
  const user = useAuth((s) => s.user);
  const create = useCreateAiWorkspaceTicket();
  const { data: projects = [], isLoading, error, failureCount } = useTicketProjects(user?.username);
  // 이미 워크스페이스가 있는 프로젝트는 고를 수 없다 (프로젝트당 1개 — 서버도 다시 확인)
  const { data: prof } = useProfiles() as { data?: { items: { name: string; projectId?: string | null }[] } };
  const taken = new Map((prof?.items ?? []).flatMap((p) => (p.projectId ? [[p.projectId, p.name] as const] : [])));
  // 처리 중인 내 신청이 있는 프로젝트도 고를 수 없다 (다른 구성원의 신청은 서버가 다시 확인)
  const { data: mine } = useTickets({ type: "ai-workspace", requestedBy: user?.username ?? "" }, !!user);
  const pending = new Set((mine?.items ?? [])
    .filter((r) => ["pending", "approved", "provisioning", "failed"].includes(r.status))
    .map((r) => r.projectId));
  const [projectId, setProjectId] = React.useState("");
  // 서비스 클러스터 — AI 노트북이 여러 클러스터를 쓰는 경우(PaaS 모드)에만 고른다.
  // 고른 프로젝트 소유 클러스터를 앞에 둔다(다른 프로젝트 소유라도 접근 권한이 있으면 고를 수 있고, 서버가 다시 확인한다)
  const { data: ainbClusters } = useAinbClusters();
  const multi = ainbClusters?.mode === "paas";
  const { data: clusters = [], isLoading: clLoading, error: clError } = useClusters(multi);
  const usable = clusters
    .filter((c) => c.status.endsWith("_COMPLETE") && !c.status.startsWith("DELETE"))
    .sort((a, b) => Number(b.projectId === projectId) - Number(a.projectId === projectId));
  const [clusterId, setClusterId] = React.useState("");
  const [purpose, setPurpose] = React.useState("");
  const [limits, setLimits] = React.useState<WorkspaceLimitsInput>(LIMITS_DEFAULT);
  const dirty = !!projectId || !!purpose.trim()
    || (Object.keys(LIMITS_DEFAULT) as (keyof WorkspaceLimitsInput)[]).some((k) => limits[k] !== LIMITS_DEFAULT[k]);
  React.useEffect(() => onDirty?.(dirty), [dirty, onDirty]);

  function submit() {
    create.mutate(
      {
        requestedBy: user?.username ?? "", projectId, purpose: purpose.trim(), ...limitsBody(limits),
        ...(multi && clusterId ? { clusterId } : {}),
      },
      {
        onSuccess: (r) => {
          toast.success("AI 워크스페이스 신청이 접수되었습니다", {
            description: `${r.projectName}${r.workspaceName ? ` · 워크스페이스 ${r.workspaceName}` : ""} · 승인 대기 중`,
          });
          onDone();
        },
        onError: (e) => toast.error("신청을 접수하지 못했습니다", { description: e.message }),
      },
    );
  }

  return (
    <div className="space-y-3 py-2">
      <div className="space-y-1.5">
        <Label>프로젝트</Label>
        {isLoading ? (
          <p className="text-xs text-muted-foreground">{projectsLoadingText(failureCount)}</p>
        ) : error ? (
          <p className="text-xs text-rose-600">프로젝트를 불러오지 못했습니다 ({error.message}).</p>
        ) : projects.length === 0 ? (
          <p className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
            신청할 수 있는 프로젝트가 없습니다(프로젝트 owner·member만 신청 가능). 조직 관리자에게 프로젝트 할당을 요청하세요.
          </p>
        ) : (
          <Select value={projectId} onValueChange={setProjectId}>
            <SelectTrigger><SelectValue placeholder="워크스페이스를 만들 프로젝트" /></SelectTrigger>
            <SelectContent>
              {projects.map((p) => (
                <SelectItem key={p.id} value={p.id} disabled={taken.has(p.id) || pending.has(p.id)}>
                  {p.name}{p.orgName ? ` (${p.orgName})` : ""}
                  {taken.has(p.id) ? ` — 워크스페이스 있음 (${taken.get(p.id)})` : pending.has(p.id) ? " — 신청 처리 중" : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <p className="text-[11px] text-muted-foreground">
          워크스페이스는 프로젝트당 1개입니다. 이름(네임스페이스)은 서버가 프로젝트 이름으로 정해 신청 내역에 표시합니다.
        </p>
      </div>
      {multi && (
        <div className="space-y-1.5">
          <Label>서비스 클러스터</Label>
          {clLoading ? (
            <p className="text-xs text-muted-foreground">클러스터 목록을 불러오는 중…</p>
          ) : clError ? (
            <p className="text-xs text-rose-600">클러스터 목록을 불러오지 못했습니다 ({clError.message}).</p>
          ) : usable.length === 0 ? (
            <p className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
              사용할 수 있는 서비스 클러스터가 없습니다. 먼저 서비스 클러스터를 신청하세요.
            </p>
          ) : (
            <Select value={clusterId} onValueChange={setClusterId}>
              <SelectTrigger><SelectValue placeholder="워크스페이스를 만들 서비스 클러스터" /></SelectTrigger>
              <SelectContent>
                {usable.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}{projectId && c.projectId === projectId ? " — 이 프로젝트의 클러스터" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <p className="text-[11px] text-muted-foreground">
            노트북이 실제로 만들어지는 클러스터입니다. 클러스터에 이 프로젝트의 네임스페이스가 있으면 그 네임스페이스를 워크스페이스로 씁니다.
          </p>
        </div>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="ws-purpose">사용 목적</Label>
        <Input id="ws-purpose" value={purpose} onChange={(e) => setPurpose(e.target.value)}
               placeholder="예: DM코드 판독 모델 학습·배포" />
      </div>
      <WorkspaceLimitFields value={limits} onChange={setLimits} />
      <p className="text-[11px] text-muted-foreground">
        적은 값은 워크스페이스가 쓸 수 있는 최대 한도입니다. 관리자가 생성을 시작할 때 조정할 수 있습니다.
      </p>
      <div className="flex justify-end">
        <Button onClick={submit} disabled={!projectId || (multi && !clusterId) || !limitsOk(limits) || create.isPending}>
          <Send className="size-4" /> {create.isPending ? "신청 중…" : "신청"}
        </Button>
      </div>
    </div>
  );
}

/** 워크스페이스 화면의 신청 창 */
export function WorkspaceRequestDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>AI 워크스페이스 신청</DialogTitle>
          <DialogDescription>
            관리자가 승인하고 생성을 시작하면 워크스페이스가 만들어집니다. 처리 현황은 자원 요청 관리에서도 볼 수 있습니다.
          </DialogDescription>
        </DialogHeader>
        <WorkspaceRequestForm onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  );
}

/* ── 신청 현황 (워크스페이스 화면) ──────────────────────────── */
const STATUS_LABEL: Record<RequestStatus, string> = {
  pending: "대기중",
  approved: "승인",
  provisioning: "생성중",
  completed: "완료",
  failed: "실패",
  rejected: "반려됨",
};
const STATUS_VARIANT: Record<RequestStatus, "default" | "success" | "destructive" | "outline"> = {
  pending: "outline",
  approved: "default",
  provisioning: "default",
  completed: "success",
  failed: "destructive",
  rejected: "destructive",
};

/** 관리자: 처리할 신청(대기·승인·생성 중·실패), 사용자: 내 신청 최근 5건. 승인·생성 시작은 신청 관리 화면에서 */
export function WorkspaceRequestsPanel({ isAdmin }: { isAdmin: boolean }) {
  const user = useAuth((s) => s.user);
  const filters: Record<string, string> = { type: "ai-workspace" };
  if (!isAdmin) filters.requestedBy = user?.username ?? "";
  const { data, isLoading, error } = useTickets(filters, !!user);
  const items = data?.items ?? [];
  const shown = isAdmin
    ? items.filter((r) => r.status !== "completed" && r.status !== "rejected")
    : items.slice(0, 5);
  const waiting = items.filter((r) => r.status === "pending").length;
  if (!isAdmin && !isLoading && !error && items.length === 0) return null;

  return (
    <Card className={isAdmin && waiting > 0 ? "border-amber-300" : undefined}>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-base">
              <Inbox className="size-4 text-brand-indigo-600" />
              {isAdmin ? "AI 워크스페이스 신청" : "내 AI 워크스페이스 신청"}
              {waiting > 0 && <Badge variant="warning">승인 대기 {waiting}건</Badge>}
            </CardTitle>
            <CardDescription>
              {isAdmin
                ? "처리할 신청입니다. 자원 신청 관리에서 승인한 뒤 '생성 시작'을 누르면 워크스페이스가 만들어집니다(한도 조정 가능)."
                : "승인 후 생성이 끝나면 워크스페이스가 아래 목록과 위 프로젝트 선택에 나타납니다."}
            </CardDescription>
          </div>
          <Button size="sm" variant="outline" asChild>
            <Link href="/ticket/requests">
              {isAdmin ? "자원 신청 관리에서 처리" : "자원 요청 관리에서 보기"} <ArrowUpRight className="size-3.5" />
            </Link>
          </Button>
        </div>
      </CardHeader>
      <CardContent className="p-0">
        {error && (
          <div className="px-5 py-4 text-xs text-rose-700">신청 목록을 불러오지 못했습니다: {error.message}</div>
        )}
        {isLoading && <div className="px-5 py-4 text-xs text-muted-foreground">불러오는 중…</div>}
        {!isLoading && !error && shown.length === 0 && (
          <div className="px-5 py-4 text-xs text-muted-foreground">처리할 신청이 없습니다.</div>
        )}
        {shown.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>신청 일시</TableHead>
                <TableHead>프로젝트 · 워크스페이스</TableHead>
                {isAdmin && <TableHead>신청자</TableHead>}
                <TableHead>한도</TableHead>
                <TableHead>상태</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="whitespace-nowrap font-mono text-[11px] text-muted-foreground">
                    {formatDateTime(r.requestedAt)}
                  </TableCell>
                  <TableCell>
                    <div className="font-mono text-xs font-semibold">{r.projectName}</div>
                    <div className="max-w-[260px] truncate text-[11px] text-muted-foreground">
                      {r.workspaceName ?? "—"}{r.purpose ? ` · ${r.purpose}` : ""}
                    </div>
                  </TableCell>
                  {isAdmin && <TableCell className="text-xs">{r.requestedByName}</TableCell>}
                  <TableCell className="whitespace-nowrap text-[11px]">{limitsText(limitsOf(r))}</TableCell>
                  <TableCell>
                    <Badge variant={STATUS_VARIANT[r.status]} className="text-[10px]">{STATUS_LABEL[r.status]}</Badge>
                    {r.status === "rejected" && (r.approvedRejectReason ?? r.provisioningRejectReason) && (
                      <div className="mt-0.5 text-[10px] text-muted-foreground">
                        {r.approvedRejectReason ?? r.provisioningRejectReason}
                      </div>
                    )}
                    {r.status === "failed" && r.errorMessage && (
                      <div className="mt-0.5 max-w-[220px] truncate text-[10px] text-rose-600" title={r.errorMessage}>
                        {r.errorMessage}
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
