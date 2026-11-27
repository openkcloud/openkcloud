"use client";
/**
 * 워크로드 — 선택한 서비스 클러스터의 Deployment·StatefulSet·DaemonSet·Job·CronJob 라이브 조회 + 쓰기.
 * 네임스페이스 리소스는 사용자 self-service: 생성(기본 Deployment)·스케일·재시작·삭제.
 * 파드(Pod)는 유형 필터에서 선택했을 때만 목록으로 — 소유 워크로드 표시, 로그·YAML·삭제. 생성은 YAML 로만.
 * 레이아웃·폰트는 IaaS 목록 화면 규약(툴바 Card + 테이블 Card)과 동일.
 */
import * as React from "react";
import { Search, Loader2, Layers, Plus, Scale, RotateCw, Trash2, ScrollText, FileCode, Maximize2, Minimize2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@/lib/utils";
import { Switch } from "@/components/ui/switch";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { useConfirm } from "@/components/ui/confirm";
import { ClusterScopeBar, useClusterScope } from "@/components/paas/cluster-scope-bar";
import { WorkloadDiff } from "@/components/paas/workload-diff";
import {
  useWorkloads, usePods, useCreateWorkload, useScaleWorkload, useRestartWorkload, useDeleteWorkload,
  useApplyWorkload, useUpdateWorkloadYaml, fetchWorkloadYaml, previewWorkloadApply, useWorkloadPods, fetchPodLogs,
  useNamespaces, useClusterAccelerators,
  type WorkloadRow, type PodListRow, type PodRow, type WorkloadPreview, type ClusterAccelerator,
} from "@/lib/paas-api";

const TEXTAREA_CLS = "w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

// "전체 유형"은 워크로드 5종만 — Pod 는 양이 많아 유형에서 직접 골랐을 때만 보여준다
const KINDS = ["Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Pod"] as const;
const SCALABLE = new Set(["Deployment", "StatefulSet"]);
const RESTARTABLE = new Set(["Deployment", "StatefulSet", "DaemonSet"]);

function stVariant(s: string): "success" | "warning" | "destructive" | "secondary" {
  if (s === "Running" || s === "Succeeded") return "success";
  if (s === "Pending") return "warning";
  if (s === "Failed" || s.endsWith("BackOff") || s.startsWith("Err")) return "destructive";
  return "secondary";
}

export default function WorkloadsPage() {
  const { clusterId } = useClusterScope();
  const [namespace, setNamespace] = React.useState("all");
  const [kind, setKind] = React.useState("all");
  const [q, setQ] = React.useState("");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [scaleTarget, setScaleTarget] = React.useState<WorkloadRow | null>(null);
  const [podsTarget, setPodsTarget] = React.useState<WorkloadRow | null>(null);
  const [yamlTarget, setYamlTarget] = React.useState<WorkloadRow | null>(null);
  const [logTarget, setLogTarget] = React.useState<PodListRow | null>(null);

  const isPod = kind === "Pod";   // Pod 는 별도 API — 소유 워크로드까지 함께 온다
  const workloads = useWorkloads(clusterId, namespace === "all" ? undefined : namespace, !isPod);
  const podList = usePods(clusterId, namespace === "all" ? undefined : namespace, isPod);
  const listQ = isPod ? podList : workloads;
  const restart = useRestartWorkload(clusterId);
  const del = useDeleteWorkload(clusterId);
  const confirm = useConfirm();
  const busy = restart.isPending || del.isPending;

  const items: (WorkloadRow | PodListRow)[] = isPod
    ? (podList.data ?? []).filter((w) => !q || w.name.includes(q) || w.image.includes(q))
    : (workloads.data ?? []).filter(
        (w) =>
          (kind === "all" || w.kind === kind) &&
          (!q || w.name.includes(q) || w.image.includes(q)),
      );

  async function doRestart(w: WorkloadRow) {
    if (!(await confirm({ title: "롤아웃 재시작", confirmLabel: "재시작", description: `${w.name} 을(를) 재시작할까요? 파드가 순차 교체됩니다.` }))) return;
    restart.mutate({ kind: w.kind, namespace: w.namespace, name: w.name },
      { onSuccess: () => toast.success(`${w.name} 재시작 요청됨`), onError: (e) => toast.error("재시작 실패", { description: String((e as Error).message) }) });
  }
  async function doDelete(w: WorkloadRow) {
    const podDesc = `${w.name} 파드를 삭제할까요? 소유 워크로드가 관리하는 파드는 자동으로 다시 생성됩니다.`;
    if (!(await confirm({
      title: w.kind === "Pod" ? "파드 삭제" : "워크로드 삭제", destructive: true, confirmLabel: "삭제",
      description: w.kind === "Pod" ? podDesc : `${w.name} 을(를) 삭제할까요? 되돌릴 수 없습니다.`,
    }))) return;
    del.mutate({ kind: w.kind, namespace: w.namespace, name: w.name },
      { onSuccess: () => toast.success(`${w.name} 삭제 요청됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <ClusterScopeBar
          queryKeys={[["paas-workloads"], ["paas-pod-list"], ["paas-namespaces"]]}
          namespace={namespace}
          onNamespaceChange={setNamespace}
          trailing={
            <Button size="sm" disabled={!clusterId} onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> 워크로드 생성
            </Button>
          }
        >
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="w-36"><SelectValue placeholder="유형" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 유형</SelectItem>
              {KINDS.map((k) => <SelectItem key={k} value={k}>{k}</SelectItem>)}
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="워크로드 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-52 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
        </ClusterScopeBar>
      </Card>

      <Card>
        {!clusterId ? (
          <EmptyBox text="먼저 서비스 클러스터를 선택하세요." />
        ) : listQ.isLoading ? (
          <Skeleton className="h-64" />
        ) : listQ.isError ? (
          <EmptyBox text={isPod ? "파드 조회 실패 — 클러스터 API 응답 오류." : "워크로드 조회 실패 — 클러스터 API 응답 오류."} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{isPod ? "파드" : "워크로드"}</TableHead>
                <TableHead>유형</TableHead>
                <TableHead>네임스페이스</TableHead>
                {isPod && <TableHead>소유 워크로드</TableHead>}
                <TableHead>이미지</TableHead>
                <TableHead>{isPod ? "Ready" : "레플리카"}</TableHead>
                <TableHead>요청 자원</TableHead>
                <TableHead>가속기</TableHead>
                <TableHead>상태</TableHead>
                <TableHead className="w-40" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((w) => (
                <Row
                  key={`${w.kind}/${w.namespace}/${w.name}`}
                  w={w} busy={busy}
                  onPods={() => setPodsTarget(w)}
                  onLogs={() => setLogTarget(w as PodListRow)}
                  onYaml={() => setYamlTarget(w)}
                  onScale={() => setScaleTarget(w)}
                  onRestart={() => doRestart(w)}
                  onDelete={() => doDelete(w)}
                />
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={isPod ? 10 : 9} className="py-10 text-center text-sm text-muted-foreground">
                    {isPod ? "조건에 맞는 파드가 없습니다." : "조건에 맞는 워크로드가 없습니다."}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </Card>

      {createOpen && <CreateDialog clusterId={clusterId} defaultNs={namespace === "all" ? "default" : namespace} onClose={() => setCreateOpen(false)} />}
      {scaleTarget && <ScaleDialog clusterId={clusterId} w={scaleTarget} onClose={() => setScaleTarget(null)} />}
      {podsTarget && <PodsDialog clusterId={clusterId} w={podsTarget} onClose={() => setPodsTarget(null)} />}
      {yamlTarget && <YamlDialog clusterId={clusterId} w={yamlTarget} onClose={() => setYamlTarget(null)} />}
      {logTarget && (
        <LogsDialog clusterId={clusterId} namespace={logTarget.namespace}
                    pod={{ name: logTarget.name, containers: logTarget.containers ?? [] }}
                    onClose={() => setLogTarget(null)} />
      )}
    </div>
  );
}

function Row({
  w, busy, onPods, onLogs, onYaml, onScale, onRestart, onDelete,
}: {
  w: WorkloadRow | PodListRow; busy: boolean; onPods: () => void; onLogs: () => void; onYaml: () => void; onScale: () => void; onRestart: () => void; onDelete: () => void;
}) {
  const isPod = w.kind === "Pod";
  const owner = isPod ? (w as PodListRow).owner : null;
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <Layers className="size-3.5 text-brand-indigo-600" />
          <span className="max-w-[220px] truncate font-mono text-sm font-semibold">{w.name}</span>
        </div>
      </TableCell>
      <TableCell><Badge variant="outline" className="text-[10px]">{w.kind}</Badge></TableCell>
      <TableCell className="font-mono text-xs">{w.namespace}</TableCell>
      {isPod && (
        <TableCell>
          {owner ? (
            <span className="font-mono text-xs">{owner.kind}/{owner.name}</span>
          ) : (
            <Badge variant="secondary" className="text-[10px]">독립 파드</Badge>
          )}
        </TableCell>
      )}
      <TableCell className="max-w-[240px] truncate font-mono text-[11px] text-muted-foreground">{w.image}</TableCell>
      <TableCell className="font-mono text-xs">
        {w.kind === "CronJob" ? (w.schedule || "—") : `${w.ready}/${w.total}`}
      </TableCell>
      <TableCell className="font-mono text-[11px] text-muted-foreground">
        {[w.requests.cpu, w.requests.memory].filter(Boolean).join(" · ") || "—"}
      </TableCell>
      <TableCell>
        {w.accelerator ? (
          <Badge variant="default" className="font-mono text-[10px]">{w.accelerator.kind} × {w.accelerator.count}</Badge>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell>
        <Badge variant={stVariant(w.status)} className="text-[10px]">
          {w.status === "Pending" && <Loader2 className="size-3 animate-spin" />}
          {w.status}
        </Badge>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-0.5">
          {isPod ? (
            <Button variant="ghost" size="icon-sm" title="로그" onClick={onLogs}><ScrollText className="size-3.5" /></Button>
          ) : (
            <Button variant="ghost" size="icon-sm" title="파드·로그" onClick={onPods}><ScrollText className="size-3.5" /></Button>
          )}
          <Button variant="ghost" size="icon-sm" title="YAML 보기·수정" onClick={onYaml}><FileCode className="size-3.5" /></Button>
          {SCALABLE.has(w.kind) && (
            <Button variant="ghost" size="icon-sm" title="레플리카 조정" disabled={busy} onClick={onScale}><Scale className="size-3.5" /></Button>
          )}
          {RESTARTABLE.has(w.kind) && (
            <Button variant="ghost" size="icon-sm" title="재시작" disabled={busy} onClick={onRestart}><RotateCw className="size-3.5" /></Button>
          )}
          <Button variant="ghost" size="icon-sm" title="삭제" disabled={busy} onClick={onDelete}><Trash2 className="size-3.5 text-rose-600" /></Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function ScaleDialog({ clusterId, w, onClose }: { clusterId: string; w: WorkloadRow; onClose: () => void }) {
  const scale = useScaleWorkload(clusterId);
  const [replicas, setReplicas] = React.useState(String(w.total));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>레플리카 조정</DialogTitle>
          <DialogDescription className="font-mono text-xs">{w.kind} · {w.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5 py-2">
          <Label>레플리카 수</Label>
          <Input type="number" min={0} value={replicas} onChange={(e) => setReplicas(e.target.value)} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button
            disabled={scale.isPending}
            onClick={() => scale.mutate(
              { kind: w.kind, namespace: w.namespace, name: w.name, replicas: Number(replicas) },
              { onSuccess: () => { toast.success(`${w.name} → ${replicas} 레플리카`); onClose(); }, onError: (e) => toast.error("조정 실패", { description: String((e as Error).message) }) },
            )}
          >
            {scale.isPending && <Loader2 className="size-3.5 animate-spin" />} 적용
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// 종류별 시작 매니페스트 — YAML 탭에서 템플릿을 고르면 편집창에 채워진다({NS} 는 선택 네임스페이스로 치환)
const WORKLOAD_TEMPLATES: Record<string, string> = {
  Deployment: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: {NS}
spec:
  replicas: 1
  selector:
    matchLabels: { app: my-app }
  template:
    metadata:
      labels: { app: my-app }
    spec:
      containers:
        - name: app
          image: nginx:1.27
          resources:
            requests: { cpu: 100m, memory: 128Mi }
`,
  StatefulSet: `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
  namespace: {NS}
spec:
  serviceName: my-sts
  replicas: 1
  selector:
    matchLabels: { app: my-sts }
  template:
    metadata:
      labels: { app: my-sts }
    spec:
      containers:
        - name: app
          image: nginx:1.27
`,
  DaemonSet: `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: my-ds
  namespace: {NS}
spec:
  selector:
    matchLabels: { app: my-ds }
  template:
    metadata:
      labels: { app: my-ds }
    spec:
      containers:
        - name: agent
          image: busybox:1.36
          command: ["sh", "-c", "sleep infinity"]
`,
  Job: `apiVersion: batch/v1
kind: Job
metadata:
  name: my-train-job
  namespace: {NS}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: trainer
          image: python:3.12
          command: ["python", "-c", "print('train')"]
`,
  CronJob: `apiVersion: batch/v1
kind: CronJob
metadata:
  name: my-cron
  namespace: {NS}
spec:
  schedule: "0 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: task
              image: busybox:1.36
              command: ["sh", "-c", "date"]
`,
  Pod: `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: {NS}
spec:
  restartPolicy: Never
  containers:
    - name: app
      image: busybox:1.36
      command: ["sh", "-c", "echo hello; sleep 3600"]
`,
};

// K8s 확장 리소스 키 → 모델명. 벤더 1종은 키로 확정, nvidia 는 노드 라벨(product)로 구분.
const ACCEL_KIND_MODEL: Record<string, string> = {
  "furiosa.ai/rngd": "RNGD",
  "furiosa.ai/npu": "Warboy",
  "rebellions.ai/atom": "ATOM",
  "samsung.com/pim": "Samsung PIM",
  "nvidia.com/gpu": "NVIDIA GPU",
};
function accelModelLabel(a: ClusterAccelerator): string {
  if (a.products.length) return a.products.map((p) => p.replace(/^NVIDIA-/i, "").replace(/_/g, " ")).join(", ");
  return ACCEL_KIND_MODEL[a.kind] ?? a.kind;
}

function CreateDialog({ clusterId, defaultNs, onClose }: { clusterId: string; defaultNs: string; onClose: () => void }) {
  const create = useCreateWorkload(clusterId);
  const apply = useApplyWorkload(clusterId);
  const namespaces = useNamespaces(clusterId);
  const accels = useClusterAccelerators(clusterId); // 이 클러스터가 실제 보유한 가속기만
  const [form, setForm] = React.useState({
    kind: "Deployment" as "Deployment" | "StatefulSet",
    namespace: defaultNs, name: "", image: "", replicas: "1",
    cpu: "", memory: "", acceleratorKind: "none", acceleratorCount: "1",
  });
  const tpl = (k: string) => WORKLOAD_TEMPLATES[k].replace("{NS}", defaultNs);
  const [yamlKind, setYamlKind] = React.useState("Deployment");
  const [yaml, setYaml] = React.useState(() => tpl("Deployment"));
  const [preview, setPreview] = React.useState<WorkloadPreview | null>(null);
  const [previewing, setPreviewing] = React.useState(false);
  const set = (k: string, v: string) => setForm((f) => ({ ...f, [k]: v }));
  const pending = create.isPending || apply.isPending;
  const selectedAccel = accels.data?.find((a) => a.kind === form.acceleratorKind);

  const pickTemplate = (k: string) => { setYamlKind(k); setYaml(tpl(k)); };

  const submitForm = () => {
    create.mutate({
      kind: form.kind,
      namespace: form.namespace, name: form.name, image: form.image,
      replicas: Number(form.replicas) || 1,
      cpu: form.cpu || undefined, memory: form.memory || undefined,
      acceleratorKind: form.acceleratorKind === "none" ? undefined : form.acceleratorKind,
      acceleratorCount: form.acceleratorKind === "none" ? 0 : Number(form.acceleratorCount),
    }, {
      onSuccess: () => { toast.success(`${form.name} 생성됨`); onClose(); },
      onError: (e) => toast.error("생성 실패", { description: String((e as Error).message) }),
    });
  };
  // "적용" → 바로 실행하지 않고 dryRun 미리보기를 받아 비교 뷰로 전환
  const requestPreview = async () => {
    setPreviewing(true);
    try {
      setPreview(await previewWorkloadApply(clusterId, yaml));
    } catch (e) {
      toast.error("미리보기 실패", { description: String((e as Error).message) });
    } finally {
      setPreviewing(false);
    }
  };
  // 비교 뷰의 "실행" → 실제 apply
  const runApply = () => {
    apply.mutate(yaml, {
      onSuccess: () => { toast.success("워크로드 적용됨"); onClose(); },
      onError: (e) => toast.error("적용 실패", { description: String((e as Error).message) }),
    });
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[88vh] w-[90vw] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>워크로드 생성</DialogTitle>
          <DialogDescription>폼(Deployment/StatefulSet) 또는 YAML(Pod·Job·CronJob 등 모든 종류).</DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="form">
          <TabsList>
            <TabsTrigger value="form">폼</TabsTrigger>
            <TabsTrigger value="yaml">YAML</TabsTrigger>
          </TabsList>

          <TabsContent value="form" className="space-y-3 pt-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label>종류</Label>
                <Select value={form.kind} onValueChange={(v) => set("kind", v)}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="Deployment">Deployment</SelectItem>
                    <SelectItem value="StatefulSet">StatefulSet</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label>네임스페이스</Label>
                <Select value={form.namespace} onValueChange={(v) => set("namespace", v)}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {(namespaces.data ?? [form.namespace]).map((ns) => (
                      <SelectItem key={ns} value={ns} className="font-mono text-xs">{ns}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label>이름</Label>
              <Input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="my-app" />
            </div>
            <div className="space-y-1.5">
              <Label>이미지</Label>
              <Input value={form.image} onChange={(e) => set("image", e.target.value)} placeholder="nginx:1.27" className="font-mono text-xs" />
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div className="space-y-1.5">
                <Label>레플리카</Label>
                <Input type="number" min={0} value={form.replicas} onChange={(e) => set("replicas", e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>CPU 요청</Label>
                <Input value={form.cpu} onChange={(e) => set("cpu", e.target.value)} placeholder="500m" className="font-mono text-xs" />
              </div>
              <div className="space-y-1.5">
                <Label>메모리 요청</Label>
                <Input value={form.memory} onChange={(e) => set("memory", e.target.value)} placeholder="512Mi" className="font-mono text-xs" />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label>가속기</Label>
                <Select value={form.acceleratorKind} onValueChange={(v) => set("acceleratorKind", v)}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">없음</SelectItem>
                    {(accels.data ?? []).map((a) => (
                      <SelectItem key={a.kind} value={a.kind}>
                        {accelModelLabel(a)}
                        <span className="ml-2 font-mono text-[10px] text-muted-foreground">{a.kind} · 최대 {a.count}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {accels.data && accels.data.length === 0 && (
                  <p className="text-[10px] text-muted-foreground">이 클러스터에 가속기가 없습니다.</p>
                )}
              </div>
              {form.acceleratorKind !== "none" && (
                <div className="space-y-1.5">
                  <Label>가속기 개수</Label>
                  <Input type="number" min={1} max={selectedAccel?.count} value={form.acceleratorCount} onChange={(e) => set("acceleratorCount", e.target.value)} />
                  {selectedAccel && <p className="text-[10px] text-muted-foreground">이 클러스터 가용 {selectedAccel.count}개</p>}
                </div>
              )}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>취소</Button>
              <Button disabled={pending || !form.name || !form.image} onClick={submitForm}>
                {create.isPending && <Loader2 className="size-3.5 animate-spin" />} 생성
              </Button>
            </DialogFooter>
          </TabsContent>

          <TabsContent value="yaml" className="space-y-2 pt-3">
            {preview ? (
              <>
                <Label>변경 미리보기 (실행 전)</Label>
                <WorkloadDiff before={preview.before} after={preview.after} action={preview.action} />
                <DialogFooter>
                  <Button variant="outline" onClick={() => setPreview(null)}>뒤로</Button>
                  <Button disabled={pending} onClick={runApply}>
                    {apply.isPending && <Loader2 className="size-3.5 animate-spin" />} 실행
                  </Button>
                </DialogFooter>
              </>
            ) : (
              <>
                <div className="flex items-center gap-2">
                  <Label className="shrink-0">템플릿</Label>
                  <Select value={yamlKind} onValueChange={pickTemplate}>
                    <SelectTrigger className="w-44"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {KINDS.map((k) => <SelectItem key={k} value={k}>{k}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </div>
                <textarea value={yaml} onChange={(e) => setYaml(e.target.value)} rows={20} className={TEXTAREA_CLS} spellCheck={false} />
                <DialogFooter>
                  <Button variant="outline" onClick={onClose}>취소</Button>
                  <Button disabled={pending || previewing || !yaml.trim()} onClick={requestPreview}>
                    {previewing && <Loader2 className="size-3.5 animate-spin" />} 적용
                  </Button>
                </DialogFooter>
              </>
            )}
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

// ── 파드 드릴다운 + 로그 ──
function PodsDialog({ clusterId, w, onClose }: { clusterId: string; w: WorkloadRow; onClose: () => void }) {
  const pods = useWorkloadPods(clusterId, w.kind, w.namespace, w.name);
  const [logPod, setLogPod] = React.useState<PodRow | null>(null);
  const del = useDeleteWorkload(clusterId);
  const confirm = useConfirm();
  async function doDeletePod(p: PodRow) {
    if (!(await confirm({
      title: "파드 삭제", destructive: true, confirmLabel: "삭제",
      description: `${p.name} 파드를 삭제할까요? 워크로드가 관리하는 파드는 자동으로 다시 생성됩니다.`,
    }))) return;
    del.mutate({ kind: "Pod", namespace: w.namespace, name: p.name },
      { onSuccess: () => toast.success(`${p.name} 삭제 요청됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] w-[92vw] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Layers className="size-4 text-brand-indigo-600" /> {w.name} · 파드</DialogTitle>
          <DialogDescription className="font-mono text-xs">{w.kind} · {w.namespace}</DialogDescription>
        </DialogHeader>
        {pods.isLoading ? <Skeleton className="h-40" /> : pods.isError ? (
          <div className="py-8 text-center text-sm text-rose-500">파드 조회 실패</div>
        ) : (
          <Card className="overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow className="[&>th]:whitespace-nowrap">
                  <TableHead>파드</TableHead><TableHead>상태</TableHead><TableHead>Ready</TableHead>
                  <TableHead>재시작</TableHead><TableHead>노드</TableHead><TableHead className="w-24" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {(pods.data ?? []).map((p) => (
                  <TableRow key={p.name}>
                    <TableCell className="max-w-[260px] truncate font-mono text-xs font-medium">{p.name}</TableCell>
                    <TableCell><Badge variant={stVariant(p.phase === "Running" ? "Running" : p.phase === "Pending" ? "Pending" : p.phase === "Failed" ? "Failed" : "x")} className="text-[10px]">{p.phase}</Badge></TableCell>
                    <TableCell className="font-mono text-xs">{p.ready}</TableCell>
                    <TableCell className="font-mono text-xs">{p.restarts}</TableCell>
                    <TableCell className="font-mono text-[11px] text-muted-foreground">{p.node || "—"}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-0.5">
                        <Button variant="ghost" size="icon-sm" title="로그" onClick={() => setLogPod(p)}><ScrollText className="size-3.5" /></Button>
                        <Button variant="ghost" size="icon-sm" title="파드 삭제" disabled={del.isPending} onClick={() => doDeletePod(p)}><Trash2 className="size-3.5 text-rose-600" /></Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
                {(pods.data ?? []).length === 0 && (
                  <TableRow><TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">파드가 없습니다.</TableCell></TableRow>
                )}
              </TableBody>
            </Table>
          </Card>
        )}
        {logPod && <LogsDialog clusterId={clusterId} namespace={w.namespace} pod={logPod} onClose={() => setLogPod(null)} />}
      </DialogContent>
    </Dialog>
  );
}

function LogsDialog({ clusterId, namespace, pod, onClose }: { clusterId: string; namespace: string; pod: { name: string; containers: string[] }; onClose: () => void }) {
  const [container, setContainer] = React.useState(pod.containers[0] ?? "");
  const [big, setBig] = React.useState(false);   // 확대 토글
  const [live, setLive] = React.useState(false); // 실시간(주기 재조회)
  const logsQ = useQuery({
    queryKey: ["paas-pod-logs", clusterId, namespace, pod.name, container],
    queryFn: () => fetchPodLogs(clusterId, namespace, pod.name, container || undefined).then((d) => d.logs || "(로그 없음)"),
    refetchInterval: live ? 3000 : false, // 켜면 3초마다 최근 500줄 다시 가져옴
    retry: 0,
  });
  const logs = logsQ.isLoading ? "불러오는 중…"
    : logsQ.isError ? `로그 조회 실패: ${(logsQ.error as Error).message}`
    : (logsQ.data ?? "");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className={cn("overflow-hidden", big ? "h-[94vh] w-[98vw] max-w-none" : "max-h-[88vh] w-[92vw] max-w-5xl")}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 font-mono text-sm">{pod.name}</DialogTitle>
          <DialogDescription className="text-[11px] text-muted-foreground">최근 500줄 · 긴 줄은 가로 스크롤 · 아래 모서리를 끌어 높이 조절</DialogDescription>
          {/* 확대/축소 토글 — 기본 닫기(X)는 우상단, 그 왼쪽에 둔다 */}
          <Button variant="ghost" size="icon-sm" className="absolute right-11 top-3.5" title={big ? "축소" : "확대"} onClick={() => setBig((v) => !v)}>
            {big ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
          </Button>
        </DialogHeader>
        {/* 컨트롤 바 — 컨테이너 선택 + 실시간 토글 */}
        <div className="flex items-center gap-2">
          {pod.containers.length > 1 && (
            <Select value={container} onValueChange={setContainer}>
              <SelectTrigger className="h-7 w-48"><SelectValue /></SelectTrigger>
              <SelectContent>{pod.containers.map((c) => <SelectItem key={c} value={c} className="font-mono text-xs">{c}</SelectItem>)}</SelectContent>
            </Select>
          )}
          <label className="ml-auto flex items-center gap-1.5 text-[11px] text-muted-foreground">
            {live && logsQ.isFetching && <Loader2 className="size-3 animate-spin" />}
            <span>실시간</span>
            <Switch checked={live} onCheckedChange={setLive} />
          </label>
        </div>
        {/* 줄바꿈 없이(whitespace-pre) 긴 줄은 가로 스크롤, 세로는 드래그로 크기 조절(resize-y) */}
        <pre className={cn(
          "resize-y overflow-auto rounded bg-slate-950 p-3 font-mono text-[11px] leading-relaxed text-slate-100 whitespace-pre",
          big ? "h-[82vh] max-h-[82vh]" : "h-[64vh] max-h-[68vh]",
        )}>{logs}</pre>
      </DialogContent>
    </Dialog>
  );
}

// ── YAML 보기·수정 ──
function YamlDialog({ clusterId, w, onClose }: { clusterId: string; w: WorkloadRow; onClose: () => void }) {
  const update = useUpdateWorkloadYaml(clusterId);
  const [text, setText] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [preview, setPreview] = React.useState<WorkloadPreview | null>(null);
  const [previewing, setPreviewing] = React.useState(false);
  React.useEffect(() => {
    let alive = true;
    fetchWorkloadYaml(clusterId, w.kind, w.namespace, w.name)
      .then((d) => { if (alive) { setText(d.yaml); setLoading(false); } })
      .catch((e) => { if (alive) { setText(`# 조회 실패: ${e.message}`); setLoading(false); } });
    return () => { alive = false; };
  }, [clusterId, w]);
  // "저장" → dryRun 미리보기를 받아 비교 뷰로 전환
  const requestPreview = async () => {
    setPreviewing(true);
    try {
      setPreview(await previewWorkloadApply(clusterId, text));
    } catch (e) {
      toast.error("미리보기 실패", { description: String((e as Error).message) });
    } finally {
      setPreviewing(false);
    }
  };
  const runSave = () => update.mutate(
    { kind: w.kind, namespace: w.namespace, name: w.name, yaml: text },
    { onSuccess: () => { toast.success(`${w.name} 저장됨`); onClose(); }, onError: (e) => toast.error("저장 실패", { description: String((e as Error).message) }) },
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[88vh] w-[90vw] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><FileCode className="size-4 text-brand-indigo-600" /> {w.name} · YAML</DialogTitle>
          <DialogDescription className="font-mono text-xs">{w.kind} · {w.namespace} — {preview ? "변경 미리보기 (실행 전)" : "수정 후 저장 시 server-side apply"}</DialogDescription>
        </DialogHeader>
        {preview ? (
          <>
            <WorkloadDiff before={preview.before} after={preview.after} action={preview.action} />
            <DialogFooter>
              <Button variant="outline" onClick={() => setPreview(null)}>뒤로</Button>
              <Button disabled={update.isPending} onClick={runSave}>
                {update.isPending && <Loader2 className="size-3.5 animate-spin" />} 실행
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            {loading ? <Skeleton className="h-80" /> : (
              <textarea value={text} onChange={(e) => setText(e.target.value)} rows={24} className={TEXTAREA_CLS} spellCheck={false} />
            )}
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>닫기</Button>
              <Button disabled={previewing || loading} onClick={requestPreview}>
                {previewing && <Loader2 className="size-3.5 animate-spin" />} 저장
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function EmptyBox({ text }: { text: string }) {
  return <div className="py-10 text-center text-sm text-muted-foreground">{text}</div>;
}
