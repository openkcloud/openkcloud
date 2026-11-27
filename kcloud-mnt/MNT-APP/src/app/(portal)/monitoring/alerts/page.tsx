"use client";
import * as React from "react";
import { AlertTriangle, Bell, Plus, Trash2, MoreVertical, Mail, MessageSquare, Webhook, Pencil, Loader2, Activity, X } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { cn, relativeTime } from "@/lib/utils";
import {
  useActiveAlerts, useAlertChannels, useAlertPolicies, useAlertMetrics,
  useUpdatePolicy, useDeletePolicy, useCreateChannel, useUpdateChannel, useDeleteChannel,
  useCreatePolicy, usePreviewPolicy, useNodes, useLogLabelValues, useNodeAccelerators, useSloServices,
  PolicyApiError,
  type AlertActive, type AlertChannel, type AlertPolicy, type AlertMetric, type AlertSeverity,
} from "@/lib/obs-api";

const OPS = [">", ">=", "<", "<=", "=="];
const SEVERITIES = ["critical", "warning", "info"] as const;
const CHANNEL_TYPES = ["email", "slack", "webhook"] as const;

// ── 정책 생성 폼 상수/헬퍼 ──
const CREATE_OPS = [">=", ">", "<=", "<"]; // 목업 기준 4종
const DURATIONS = ["0m", "2m", "3m", "5m", "10m"];
const WINDOWS = [1, 5, 10, 30];
const SELECTOR_FIELDS = ["cluster", "node", "acc_id", "model", "container", "unit"] as const;
type SelField = (typeof SELECTOR_FIELDS)[number];
const SEL_LABEL: Record<SelField, string> = {
  cluster: "클러스터", node: "노드", acc_id: "가속기 카드", model: "LLM 모델", container: "컨테이너", unit: "systemd 유닛",
};
const KIND_SCOPE: Record<string, SelField[]> = {
  resource: ["cluster", "node", "acc_id"], log: ["cluster", "container", "unit"], slo: ["cluster", "model"],
};
const KIND_SC_LABEL: Record<string, string> = { resource: "카드별", log: "로그", slo: "모델별" };
const SCOPE_UNIT: Record<string, string> = { resource: "가속기 카드", log: "로그", slo: "LLM 모델" };

/** 조건들의 kind 로 감시대상 허용 필드 결정(조건 1개=kind별, 2개↑=클러스터+로그 포함 시 컨테이너·유닛). */
function allowedFields(conds: { metric: string }[], metrics: AlertMetric[]): SelField[] {
  const kindOf = (m: string) => metrics.find((x) => x.key === m)?.kind ?? "resource";
  if (conds.length === 1) return KIND_SCOPE[kindOf(conds[0].metric)] ?? ["cluster"];
  const a: SelField[] = ["cluster"];
  if (conds.some((c) => kindOf(c.metric) === "log")) a.push("container", "unit");
  return a;
}
/** 등록된 채널 → 활성 버튼 종류. email / webhook+format=slack → slack / 그 외 webhook. */
function channelKind(c: AlertChannel): "email" | "slack" | "webhook" {
  if (c.type === "email") return "email";
  if (c.type === "webhook" && (c.config as { format?: string } | undefined)?.format === "slack") return "slack";
  return "webhook";
}
/** 선택된 감시대상 → "k=v,k=v" (허용 필드·빈 값 제외, 허용문자만). 아무것도 없으면 "". */
function buildSelector(tg: Partial<Record<SelField, string>>, allowed: SelField[]): string {
  return SELECTOR_FIELDS
    .filter((k) => allowed.includes(k) && tg[k] && /^[A-Za-z0-9_./-]+$/.test(tg[k]!))
    .map((k) => `${k}=${tg[k]}`)
    .join(",");
}

function sevClass(s: string) {
  return {
    critical: "bg-rose-500/15 text-rose-600 dark:text-rose-300",
    warning: "bg-amber-500/15 text-amber-600 dark:text-amber-300",
    info: "bg-sky-500/15 text-sky-600 dark:text-sky-300",
  }[s] ?? "bg-slate-500/15 text-slate-500";
}
function SevBadge({ s }: { s: string }) {
  return <span className={cn("rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase", sevClass(s))}>{s}</span>;
}
function ChannelIcon({ type }: { type: string }) {
  const I = type === "email" ? Mail : type === "slack" ? MessageSquare : Webhook;
  return <I className="size-3.5 text-muted-foreground" />;
}
/** 조건 렌더 — conditions[] 배열을 조합(최상위 레거시 metric/op/threshold 는 참조 금지).
 *  각 조건 "metric op threshold" (+window_min 있으면 "/Nm"), 복합이면 AND 로 연결. */
function ruleText(p: AlertPolicy) {
  const conds = p.conditions ?? [];
  if (!conds.length) return "—";
  return conds
    .map((c) => `${c.metric} ${c.op} ${c.threshold ?? "—"}${c.window_min ? ` /${c.window_min}m` : ""}`)
    .join(" AND ");
}
function targetText(selector: string) {
  return selector?.trim() ? selector : "전체";
}
function activeTargetText(t: AlertActive["target"]) {
  const parts = Object.entries(t ?? {}).filter(([, v]) => v).map(([k, v]) => `${k}=${v}`);
  return parts.length ? parts.join(", ") : "전체";
}

export default function AlertsPage() {
  const active = useActiveAlerts();
  const policies = useAlertPolicies();
  const channels = useAlertChannels();

  const alerts = active.data?.alerts ?? [];
  const polList = policies.data?.policies ?? [];
  const chList = channels.data?.channels ?? [];

  const updatePolicy = useUpdatePolicy();
  const deletePolicy = useDeletePolicy();
  const confirm = useConfirm();

  const [editPolicy, setEditPolicy] = React.useState<AlertPolicy | null>(null);
  const [channelDialog, setChannelDialog] = React.useState<{ mode: "create" | "edit"; data?: AlertChannel } | null>(null);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [previewPolicy, setPreviewPolicy] = React.useState<AlertPolicy | null>(null);

  const counts = alerts.reduce(
    (a, x) => { const s = x.severity as "critical" | "warning" | "info"; if (s in a) a[s] += 1; return a; },
    { critical: 0, warning: 0, info: 0 } as Record<string, number>,
  );

  const onTogglePolicy = (p: AlertPolicy, enabled: boolean) =>
    updatePolicy.mutate({ id: p.id, body: { enabled } }, {
      onSuccess: () => toast.success(`${p.name} ${enabled ? "활성화" : "비활성화"}`),
      onError: (e) => toast.error(`변경 실패: ${e.message}`),
    });
  const onDeletePolicy = async (p: AlertPolicy) => {
    if (!(await confirm({ title: "정책 삭제", destructive: true, confirmLabel: "삭제", description: `'${p.name}' 정책을 삭제할까요?` }))) return;
    deletePolicy.mutate(p.id, {
      onSuccess: () => toast.success(`${p.name} 삭제됨`),
      onError: (e) => toast.error(`삭제 실패: ${e.message}`),
    });
  };

  return (
    <Tabs defaultValue="active" className="space-y-4">
      <TabsList>
        <TabsTrigger value="active">
          활성 알람 ({active.isLoading ? "…" : alerts.length})
        </TabsTrigger>
        <TabsTrigger value="policies">알람 정책 ({policies.isLoading ? "…" : polList.length})</TabsTrigger>
        <TabsTrigger value="channels">채널 ({channels.isLoading ? "…" : chList.length})</TabsTrigger>
      </TabsList>

      {/* ── 활성 알람 ── */}
      <TabsContent value="active">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              <span className="flex items-center gap-2"><AlertTriangle className="size-4 text-amber-500" /> 활성 알람</span>
              <span className="flex items-center gap-2">
                <Badge className={sevClass("critical")}>심각 {counts.critical}</Badge>
                <Badge className={sevClass("warning")}>경고 {counts.warning}</Badge>
                <Badge className={sevClass("info")}>정보 {counts.info}</Badge>
                <Badge variant="ghost" className="font-mono text-[10px]">/alerts/active</Badge>
              </span>
            </CardTitle>
            <CardDescription>현재 발생 중인 알람(정책 평가 결과). 주기 새로고침으로 갱신됩니다.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {active.isError && <div className="py-6 text-center text-sm text-rose-500">활성 알람 조회 실패</div>}
            {!active.isError && alerts.length === 0 && (
              <div className="py-8 text-center text-sm text-muted-foreground">
                {active.isLoading ? <Loader2 className="mx-auto size-4 animate-spin" /> : "발생 중인 알람이 없습니다."}
              </div>
            )}
            {alerts.map((a) => (
              <div key={a.fingerprint} className="flex items-start justify-between gap-3 rounded-md border border-border p-3">
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <SevBadge s={a.severity} />
                    <span className="text-sm font-medium">{a.message}</span>
                    {a.acked_at && <Badge variant="ghost" className="text-[10px]">확인됨</Badge>}
                  </div>
                  <div className="flex flex-wrap gap-x-4 gap-y-0.5 font-mono text-[11px] text-muted-foreground">
                    <span>정책 {a.policy_id}</span>
                    <span>대상 {activeTargetText(a.target)}</span>
                    <span>값 {a.value}</span>
                    <span>발생 {relativeTime(a.started_at)}</span>
                  </div>
                </div>
                <Badge variant="outline" className="shrink-0 text-[10px]">{a.state}</Badge>
              </div>
            ))}
          </CardContent>
        </Card>
      </TabsContent>

      {/* ── 알람 정책 ── */}
      <TabsContent value="policies">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              <span className="flex items-center gap-2"><Bell className="size-4" /> 알람 정책</span>
              <span className="flex items-center gap-2">
                <Badge variant="ghost" className="font-mono text-[10px]">/alerts/policies</Badge>
                <Button size="sm" onClick={() => setCreateOpen(true)}>
                  <Plus className="size-3.5" /> 정책 생성
                </Button>
              </span>
            </CardTitle>
            <CardDescription>리소스(Prometheus)·로그(Loki)·SLO(vLLM) 기반 기본 정책. 활성 토글·수정·삭제 가능.</CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-12">활성</TableHead>
                  <TableHead>정책</TableHead>
                  <TableHead>조건</TableHead>
                  <TableHead>대상</TableHead>
                  <TableHead>지속</TableHead>
                  <TableHead>심각도</TableHead>
                  <TableHead>채널</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {policies.isLoading && (
                  <TableRow><TableCell colSpan={8} className="py-8 text-center text-muted-foreground"><Loader2 className="mx-auto size-4 animate-spin" /></TableCell></TableRow>
                )}
                {!policies.isLoading && polList.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <Switch checked={p.enabled} onCheckedChange={(v) => onTogglePolicy(p, v)} />
                    </TableCell>
                    <TableCell>
                      <div className="font-medium">{p.name}</div>
                      <div className="font-mono text-[10px] text-muted-foreground">{p.id} · {p.description}</div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{ruleText(p)}</TableCell>
                    <TableCell className="font-mono text-xs">{targetText(p.selector)}</TableCell>
                    <TableCell className="font-mono text-xs">{p.duration}</TableCell>
                    <TableCell><SevBadge s={p.severity} /></TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        {Object.keys(p.channels ?? {}).length === 0
                          ? <Badge variant="outline" className="text-[10px] text-muted-foreground">미지정</Badge>
                          : Object.keys(p.channels).map((t) => <ChannelIcon key={t} type={t} />)}
                      </div>
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon" className="size-7"><MoreVertical className="size-4" /></Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setPreviewPolicy(p)}><Activity className="size-3.5" /> 미리 평가</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => setEditPolicy(p)}><Pencil className="size-3.5" /> 수정</DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem className="text-rose-600" onClick={() => onDeletePolicy(p)}><Trash2 className="size-3.5" /> 삭제</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </TabsContent>

      {/* ── 채널 ── */}
      <TabsContent value="channels">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              <span className="flex items-center gap-2"><Mail className="size-4" /> 알림 채널</span>
              <span className="flex items-center gap-2">
                <Badge variant="ghost" className="font-mono text-[10px]">/alerts/channels</Badge>
                <Button size="sm" onClick={() => setChannelDialog({ mode: "create" })}><Plus className="size-3.5" /> 채널 추가</Button>
              </span>
            </CardTitle>
            <CardDescription>알람은 선택한 채널 종류(email·slack·webhook)로 등록된 채널 전체에 동시 발송됩니다.</CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>이름</TableHead>
                  <TableHead>종류</TableHead>
                  <TableHead>수신 설정</TableHead>
                  <TableHead>해제 알림</TableHead>
                  <TableHead>활성</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {channels.isLoading && (
                  <TableRow><TableCell colSpan={6} className="py-8 text-center text-muted-foreground"><Loader2 className="mx-auto size-4 animate-spin" /></TableCell></TableRow>
                )}
                {!channels.isLoading && chList.map((c) => (
                  <ChannelRow key={c.id} c={c} onEdit={() => setChannelDialog({ mode: "edit", data: c })} />
                ))}
                {!channels.isLoading && chList.length === 0 && (
                  <TableRow><TableCell colSpan={6} className="py-8 text-center text-muted-foreground">등록된 채널이 없습니다.</TableCell></TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </TabsContent>

      {editPolicy && <PolicyEditDialog policy={editPolicy} onClose={() => setEditPolicy(null)} />}
      {channelDialog && <ChannelDialog mode={channelDialog.mode} data={channelDialog.data} onClose={() => setChannelDialog(null)} />}
      {createOpen && <CreatePolicyDialog onClose={() => setCreateOpen(false)} />}
      {previewPolicy && <PolicyPreviewDialog policy={previewPolicy} onClose={() => setPreviewPolicy(null)} />}
    </Tabs>
  );
}

// ── 채널 행 (활성 토글·수정·삭제) ──
function ChannelRow({ c, onEdit }: { c: AlertChannel; onEdit: () => void }) {
  const update = useUpdateChannel();
  const del = useDeleteChannel();
  const confirm = useConfirm();
  const summary = c.type === "email"
    ? (c.config?.to as string[] | undefined)?.join(", ")
    : (c.config?.webhook_url as string) ?? (c.config?.url as string) ?? "";
  const onDelete = async () => {
    if (!(await confirm({ title: "채널 삭제", destructive: true, confirmLabel: "삭제", description: `'${c.name}' 채널을 삭제할까요?` }))) return;
    del.mutate(c.id, { onSuccess: () => toast.success(`${c.name} 삭제됨`), onError: (e) => toast.error(`삭제 실패: ${e.message}`) });
  };
  return (
    <TableRow>
      <TableCell className="font-medium">{c.name}</TableCell>
      <TableCell><span className="flex items-center gap-1.5 text-xs"><ChannelIcon type={c.type} /> {c.type}</span></TableCell>
      <TableCell className="max-w-[320px] truncate font-mono text-[11px] text-muted-foreground">{summary}</TableCell>
      <TableCell>{c.notify_resolved ? <Badge variant="outline" className="text-[10px]">켜짐</Badge> : <span className="text-[11px] text-muted-foreground">꺼짐</span>}</TableCell>
      <TableCell>
        <Switch
          checked={c.enabled}
          onCheckedChange={(v) => update.mutate({ id: c.id, body: { enabled: v } }, {
            onSuccess: () => toast.success(`${c.name} ${v ? "활성화" : "비활성화"}`),
            onError: (e) => toast.error(`변경 실패: ${e.message}`),
          })}
        />
      </TableCell>
      <TableCell>
        <DropdownMenu>
          <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" className="size-7"><MoreVertical className="size-4" /></Button></DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={onEdit}><Pencil className="size-3.5" /> 수정</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-rose-600" onClick={onDelete}><Trash2 className="size-3.5" /> 삭제</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}

// ── 정책 수정 다이얼로그 (PATCH: threshold/op/severity/window_min/enabled) ──
function PolicyEditDialog({ policy, onClose }: { policy: AlertPolicy; onClose: () => void }) {
  const update = useUpdatePolicy();
  const [op, setOp] = React.useState(policy.op);
  const [threshold, setThreshold] = React.useState(String(policy.threshold));
  const [severity, setSeverity] = React.useState(policy.severity);
  const [windowMin, setWindowMin] = React.useState(String(policy.window_min));
  const isLog = policy.kind === "log";

  const onSave = () => {
    const body: Partial<AlertPolicy> = { op, threshold: Number(threshold), severity };
    if (isLog) body.window_min = Number(windowMin);
    update.mutate({ id: policy.id, body }, {
      onSuccess: () => { toast.success(`${policy.name} 수정됨`); onClose(); },
      onError: (e) => toast.error(`수정 실패: ${e.message}`),
    });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{policy.name} 수정</DialogTitle>
          <DialogDescription className="font-mono text-xs">{policy.id} · {policy.metric}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>연산자</Label>
              <Select value={op} onValueChange={setOp}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>{OPS.map((o) => <SelectItem key={o} value={o}>{o}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>임계값</Label>
              <Input type="number" value={threshold} onChange={(e) => setThreshold(e.target.value)} />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>심각도</Label>
              <Select value={severity} onValueChange={setSeverity}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>{SEVERITIES.map((s) => <SelectItem key={s} value={s}>{s}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            {isLog && (
              <div className="space-y-1.5">
                <Label>집계 창(분)</Label>
                <Input type="number" value={windowMin} onChange={(e) => setWindowMin(e.target.value)} />
              </div>
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={onSave} disabled={update.isPending}>
            {update.isPending && <Loader2 className="size-3.5 animate-spin" />} 저장
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── 채널 생성/수정 다이얼로그 ──
function ChannelDialog({ mode, data, onClose }: { mode: "create" | "edit"; data?: AlertChannel; onClose: () => void }) {
  const create = useCreateChannel();
  const update = useUpdateChannel();
  const [name, setName] = React.useState(data?.name ?? "");
  const [type, setType] = React.useState<string>(data?.type ?? "email");
  const [notifyResolved, setNotifyResolved] = React.useState(data?.notify_resolved ?? true);
  const [to, setTo] = React.useState(((data?.config?.to as string[] | undefined) ?? []).join(", "));
  const [url, setUrl] = React.useState((data?.config?.webhook_url as string) ?? (data?.config?.url as string) ?? "");
  const pending = create.isPending || update.isPending;

  const buildConfig = () =>
    type === "email"
      ? { to: to.split(",").map((s) => s.trim()).filter(Boolean) }
      : type === "slack"
        ? { webhook_url: url }
        : { url };

  const onSave = () => {
    const body = { name, type, config: buildConfig(), notify_resolved: notifyResolved };
    const done = { onSuccess: () => { toast.success(`채널 ${mode === "create" ? "추가" : "수정"}됨`); onClose(); }, onError: (e: Error) => toast.error(`실패: ${e.message}`) };
    if (mode === "create") create.mutate(body, done);
    else update.mutate({ id: data!.id, body }, done);
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "채널 추가" : "채널 수정"}</DialogTitle>
          <DialogDescription>알람을 받을 채널을 설정합니다.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="space-y-1.5">
            <Label>이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="예: 운영팀 메일" />
          </div>
          <div className="space-y-1.5">
            <Label>종류</Label>
            <Select value={type} onValueChange={setType} disabled={mode === "edit"}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>{CHANNEL_TYPES.map((t) => <SelectItem key={t} value={t}>{t}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          {type === "email" ? (
            <div className="space-y-1.5">
              <Label>수신 이메일 (쉼표 구분)</Label>
              <Input value={to} onChange={(e) => setTo(e.target.value)} placeholder="ops@example.com, oncall@example.com" />
            </div>
          ) : (
            <div className="space-y-1.5">
              <Label>{type === "slack" ? "Slack Webhook URL" : "Webhook URL"}</Label>
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" />
            </div>
          )}
          <div className="flex items-center justify-between rounded-md border border-border p-3">
            <div>
              <Label>해제 알림</Label>
              <p className="text-[11px] text-muted-foreground">알람이 해소될 때도 알림을 보냅니다.</p>
            </div>
            <Switch checked={notifyResolved} onCheckedChange={setNotifyResolved} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={onSave} disabled={pending || !name}>
            {pending && <Loader2 className="size-3.5 animate-spin" />} 저장
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── 정책 생성 다이얼로그 ──
type CondRow = { metric: string; op: string; th: string; w: number };
const ALL_SENTINEL = "__all__";

function CreatePolicyDialog({ onClose }: { onClose: () => void }) {
  const metricsQ = useAlertMetrics();
  const metrics = React.useMemo(() => metricsQ.data?.metrics ?? [], [metricsQ.data]);
  const channelsQ = useAlertChannels();
  const enabledKinds = React.useMemo(
    () => new Set((channelsQ.data?.channels ?? []).map(channelKind)),
    [channelsQ.data],
  );
  const create = useCreatePolicy();

  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const [conds, setConds] = React.useState<CondRow[]>([]);
  const [tg, setTg] = React.useState<Partial<Record<SelField, string>>>({});
  const [duration, setDuration] = React.useState("5m");
  const [severity, setSeverity] = React.useState<AlertSeverity>("warning");
  const [channels, setChannels] = React.useState<string[]>([]);
  const [errs, setErrs] = React.useState<{ name?: string; selector?: string; channels?: string; th: Record<number, string> }>({ th: {} });
  const [banner, setBanner] = React.useState<{ msg: string; retry?: boolean } | null>(null);

  const kindOf = (m: string) => metrics.find((x) => x.key === m)?.kind ?? "resource";
  const unitOf = (m: string) => metrics.find((x) => x.key === m)?.unit ?? "";

  // 지표 로드되면 첫 조건 초기화
  React.useEffect(() => {
    if (metrics.length && conds.length === 0) setConds([{ metric: metrics[0].key, op: ">=", th: "", w: 5 }]);
  }, [metrics, conds.length]);

  const allowed = React.useMemo(() => allowedFields(conds, metrics), [conds, metrics]);

  // 감시대상 옵션 소스
  const clustersQ = useLogLabelValues("cluster");
  const containersQ = useLogLabelValues(allowed.includes("container") ? "container" : null);
  const unitsQ = useLogLabelValues(allowed.includes("unit") ? "unit" : null);
  const nodesQ = useNodes(tg.cluster ?? "", allowed.includes("node") && !!tg.cluster);
  const accsQ = useNodeAccelerators(allowed.includes("acc_id") ? (tg.cluster ?? "") : "", allowed.includes("acc_id") ? (tg.node ?? "") : "");
  const sloQ = useSloServices(allowed.includes("model") ? (tg.cluster ?? "") : "");

  const optsFor = (f: SelField): string[] => {
    switch (f) {
      case "cluster": return clustersQ.data?.values ?? [];
      case "node": return (nodesQ.data?.nodes ?? []).map((n) => n.nodename).filter(Boolean) as string[];
      case "acc_id": return (accsQ.data?.accelerators ?? []).map((a) => a.acc_id).filter(Boolean);
      case "model": return (sloQ.data?.items ?? []).map((s) => s.model).filter(Boolean);
      case "container": return containersQ.data?.values ?? [];
      case "unit": return unitsQ.data?.values ?? [];
    }
  };
  const fieldDisabled = (f: SelField) =>
    !allowed.includes(f) ||
    (["node", "acc_id", "model"].includes(f) && !tg.cluster) ||
    (f === "acc_id" && !tg.node);

  function patchCond(i: number, patch: Partial<CondRow>) {
    setConds((cs) => cs.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  }
  function setTgField(f: SelField, raw: string) {
    const v = raw === ALL_SENTINEL ? "" : raw;
    setTg((t) => {
      const nt: Partial<Record<SelField, string>> = { ...t, [f]: v || undefined };
      if (f === "cluster") { delete nt.node; delete nt.acc_id; delete nt.model; }
      if (f === "node") delete nt.acc_id;
      return nt;
    });
    setErrs((e) => ({ ...e, selector: undefined }));
  }

  function submit() {
    const ne: typeof errs = { th: {} };
    if (!name.trim()) ne.name = "정책 이름을 입력해 주세요";
    conds.forEach((c, i) => { if (c.th === "" || isNaN(Number(c.th))) ne.th[i] = "기준 숫자를 입력해 주세요"; });
    if (ne.name || Object.keys(ne.th).length) {
      setErrs(ne);
      setBanner({ msg: "표시된 칸을 고친 뒤 다시 눌러 주세요. 서버로 보내지 않았습니다." });
      return;
    }
    setErrs({ th: {} });
    setBanner(null);
    const body = {
      name: name.trim(),
      description: desc.trim(),
      conditions: conds.map((c) => ({
        metric: c.metric,
        op: c.op,
        threshold: Number(c.th),
        ...(kindOf(c.metric) === "resource" ? {} : { window_min: c.w }),
      })),
      selector: buildSelector(tg, allowed),
      duration,
      severity,
      channels,
      enabled: true,
    };
    create.mutate(body, {
      onSuccess: () => { toast.success("정책을 생성했습니다"); onClose(); },
      onError: (e) => {
        if (e instanceof PolicyApiError && e.errors.length) {
          const me: typeof errs = { th: {} };
          let retry = false;
          for (const fe of e.errors) {
            const thm = /^th(\d+)$/.exec(fe.field);
            if (fe.field === "name") me.name = fe.message;
            else if (fe.field === "selector") { me.selector = fe.message; retry = true; }
            else if (fe.field === "channels") me.channels = fe.message;
            else if (thm) me.th[Number(thm[1])] = fe.message;
          }
          setErrs(me);
          setBanner({ msg: `서버 검수에서 ${e.errors.length}개 칸이 걸렸습니다. 정책은 저장되지 않았습니다.`, retry });
        } else {
          setBanner({ msg: (e as Error).message || "생성 실패" });
        }
      },
    });
  }

  const scopeUnit = conds.length > 1 ? "클러스터 (조합)" : (SCOPE_UNIT[kindOf(conds[0]?.metric ?? "")] ?? "—");

  return (
    <Dialog open onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent className="max-h-[88vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Bell className="size-4" /> 알람 정책 생성</DialogTitle>
          <DialogDescription>판정 단위: {scopeUnit}</DialogDescription>
        </DialogHeader>

        {banner && (
          <div className="flex items-center justify-between gap-2 rounded-md bg-rose-500/10 px-3 py-2 text-xs text-rose-600">
            <span><AlertTriangle className="mr-1 inline size-3.5" /> 저장하지 못했습니다. {banner.msg}</span>
            {banner.retry && <Button size="sm" variant="outline" onClick={submit}>다시 시도</Button>}
          </div>
        )}

        <div className="space-y-1">
          <Label>정책 이름 *</Label>
          <Input value={name} onChange={(e) => { setName(e.target.value); setErrs((x) => ({ ...x, name: undefined })); }} className={cn(errs.name && "border-rose-500")} placeholder="예: GPU 과부하로 서비스 지연" />
          {errs.name && <p className="text-xs text-rose-600">{errs.name}</p>}
        </div>
        <div className="space-y-1">
          <Label>설명 (선택)</Label>
          <Input value={desc} onChange={(e) => setDesc(e.target.value)} />
        </div>

        {/* 조건 */}
        <div className="space-y-2">
          <Label>조건 · 모두 만족하면 울림</Label>
          {conds.map((c, i) => (
            <div key={i} className="space-y-1">
              {i > 0 && <div className="text-center text-[11px] text-muted-foreground">그리고</div>}
              <div className="flex items-center gap-2">
                <Select value={c.metric} onValueChange={(v) => patchCond(i, { metric: v })}>
                  <SelectTrigger className="flex-1"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {metrics.map((m) => (
                      <SelectItem key={m.key} value={m.key}>{m.label} · {KIND_SC_LABEL[m.kind] ?? m.kind}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select value={c.op} onValueChange={(v) => patchCond(i, { op: v })}>
                  <SelectTrigger className="w-20"><SelectValue /></SelectTrigger>
                  <SelectContent>{CREATE_OPS.map((o) => <SelectItem key={o} value={o}>{o}</SelectItem>)}</SelectContent>
                </Select>
                <Input type="number" value={c.th} placeholder={`기준 (${unitOf(c.metric)})`} className={cn("w-28", errs.th[i] && "border-rose-500")}
                  onChange={(e) => { patchCond(i, { th: e.target.value }); setErrs((x) => { const t = { ...x.th }; delete t[i]; return { ...x, th: t }; }); }} />
                {kindOf(c.metric) === "resource" ? (
                  <span className="w-20 shrink-0 text-center text-[11px] text-muted-foreground">지금 값</span>
                ) : (
                  <Select value={String(c.w)} onValueChange={(v) => patchCond(i, { w: Number(v) })}>
                    <SelectTrigger className="w-20"><SelectValue /></SelectTrigger>
                    <SelectContent>{WINDOWS.map((w) => <SelectItem key={w} value={String(w)}>{w}분</SelectItem>)}</SelectContent>
                  </Select>
                )}
                <Button variant="ghost" size="icon-sm" className={cn(conds.length < 2 && "invisible")} onClick={() => setConds((cs) => cs.filter((_, j) => j !== i))}>
                  <X className="size-3.5" />
                </Button>
              </div>
              {errs.th[i] && <p className="text-xs text-rose-600">{errs.th[i]}</p>}
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={() => setConds((cs) => [...cs, { metric: metrics[0]?.key ?? "", op: ">=", th: "", w: 5 }])}>
            <Plus className="size-3.5" /> 조건 추가
          </Button>
        </div>

        {/* 감시 대상 */}
        <div className="space-y-1">
          <Label>감시 대상 · 안 고르면 전체</Label>
          <div className="grid grid-cols-3 gap-2">
            {SELECTOR_FIELDS.map((f) => {
              const dis = fieldDisabled(f);
              const loading = f === "model" && allowed.includes("model") && !!tg.cluster && sloQ.isFetching;
              return (
                <div key={f} className={cn("space-y-0.5", dis && "opacity-50")}>
                  <span className="text-[11px] text-muted-foreground">{SEL_LABEL[f]}{loading && <Loader2 className="ml-1 inline size-3 animate-spin" />}</span>
                  <Select value={dis ? ALL_SENTINEL : (tg[f] ?? ALL_SENTINEL)} onValueChange={(v) => setTgField(f, v)} disabled={dis}>
                    <SelectTrigger className={cn(errs.selector && "border-rose-500")}><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value={ALL_SENTINEL}>{allowed.includes(f) ? "전체" : "사용 안 함"}</SelectItem>
                      {optsFor(f).map((o) => <SelectItem key={o} value={o} className="font-mono text-xs">{o}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </div>
              );
            })}
          </div>
          {allowed.includes("model") && !!tg.cluster && !sloQ.isFetching && (sloQ.data?.warnings?.includes("NO_DATA") || (sloQ.data?.items?.length ?? 0) === 0) && (
            <p className="text-[11px] text-muted-foreground">이 클러스터에 최근 LLM 요청 데이터가 없습니다.</p>
          )}
          {errs.selector && <p className="text-xs text-rose-600">{errs.selector}</p>}
        </div>

        {/* 지속/심각도 */}
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1">
            <Label>지속 시간</Label>
            <Select value={duration} onValueChange={setDuration}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>{DURATIONS.map((d) => <SelectItem key={d} value={d}>{d}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label>심각도</Label>
            <div className="flex gap-1.5">
              {(["info", "warning", "critical"] as const).map((s) => (
                <Button key={s} size="sm" variant={severity === s ? "default" : "outline"} className="flex-1" onClick={() => setSeverity(s)}>{s}</Button>
              ))}
            </div>
          </div>
        </div>

        {/* 채널 */}
        <div className="space-y-1">
          <Label>알림 채널 · 등록된 종류만 선택 가능</Label>
          <div className="flex gap-1.5">
            {CHANNEL_TYPES.map((t) => {
              const on = channels.includes(t);
              const usable = enabledKinds.has(t);
              return (
                <Button key={t} size="sm" variant={on ? "default" : "outline"} disabled={!usable} className="flex-1"
                  title={usable ? undefined : "등록된 채널이 없는 종류"}
                  onClick={() => { setChannels((cs) => on ? cs.filter((x) => x !== t) : [...cs, t]); setErrs((x) => ({ ...x, channels: undefined })); }}>
                  {t}
                </Button>
              );
            })}
          </div>
          {errs.channels && <p className="text-xs text-rose-600">{errs.channels}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={create.isPending}>취소</Button>
          <Button onClick={submit} disabled={create.isPending || !metrics.length}>
            {create.isPending && <Loader2 className="size-3.5 animate-spin" />} 정책 생성
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── 정책 미리 평가 다이얼로그 ──
function PolicyPreviewDialog({ policy, onClose }: { policy: AlertPolicy; onClose: () => void }) {
  const preview = usePreviewPolicy();
  const ranRef = React.useRef(false);
  React.useEffect(() => {
    if (ranRef.current) return;
    ranRef.current = true;
    preview.mutate(policy.id);
  }, [policy.id, preview]);

  const r = preview.data as Record<string, unknown> | undefined;
  const matched = r ? (r.matched ?? r.firing ?? r.satisfied) : undefined;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Activity className="size-4" /> {policy.name} — 미리 평가</DialogTitle>
          <DialogDescription className="font-mono text-xs">{policy.id}</DialogDescription>
        </DialogHeader>
        {preview.isPending ? (
          <div className="py-8 text-center text-sm text-muted-foreground"><Loader2 className="mx-auto mb-1 size-4 animate-spin" /> 현재 수집값으로 평가 중…</div>
        ) : preview.isError ? (
          <div className="py-6 text-center text-sm text-rose-600">평가 실패: {(preview.error as Error).message}</div>
        ) : (
          <div className="space-y-2">
            {typeof matched === "boolean" && (
              <Badge className={matched ? sevClass("critical") : "bg-slate-500/15 text-slate-500"}>
                {matched ? "현재 조건 충족 (울림)" : "현재 미충족"}
              </Badge>
            )}
            <pre className="max-h-[50vh] overflow-auto rounded bg-muted p-3 text-[11px]">{JSON.stringify(r, null, 2)}</pre>
          </div>
        )}
        <DialogFooter><Button variant="outline" onClick={onClose}>닫기</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
