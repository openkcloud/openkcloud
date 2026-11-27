"use client";
/**
 * MNT-PAAS 연동 — 서비스 클러스터(Magnum K8s)는 MNT-PAAS 모듈을 통해 가져온다.
 *
 * 주소(NEXT_PUBLIC_PAAS_API):
 *   - 미설정        → http://localhost:8002 (로컬 dev, MNT-PAAS 직접)
 *   - ""(빈값)      → same-origin 상대경로 (/api/paas) — 게이트웨이 운영
 *   - "http://..."  → 고정 주소
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { usePollingInterval } from "@/lib/polling";
import { authFetch } from "@/lib/http";
import { downloadText } from "@/lib/ssh-keygen";

const RAW = process.env.NEXT_PUBLIC_PAAS_API;
export const PAAS_BASE = RAW === undefined ? "http://localhost:8002" : RAW;

// 모든 요청에 토큰을 붙인다 — MNT-PAAS 가 CORE 로 사용자·접근 클러스터를 확인한다
async function sendJson<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${PAAS_BASE}${path}`, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = `${res.status}`;
    try { msg = (await res.json()).detail ?? msg; } catch { /* keep */ }
    throw new Error(String(msg));
  }
  return (res.status === 204 ? undefined : await res.json()) as T;
}

async function fetchJson<T>(url: string): Promise<T> {
  const res = await authFetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`PAAS ${res.status}`);
  return res.json() as Promise<T>;
}

// ── 서비스 클러스터 (Magnum) ──
export interface ClusterSummary {
  id: string;
  name: string;
  status: string; // CREATE_COMPLETE / CREATE_IN_PROGRESS / DELETE_IN_PROGRESS / ..._FAILED
  healthStatus: string; // HEALTHY / UNHEALTHY / ""
  k8sVersion: string;
  projectId: string;
  nodeCount: number;
  masterCount: number;
  flavorId: string; // 워커 flavor — IaaS useFlavors 와 조인해 가속기 표시
  masterFlavorId: string;
  apiAddress: string;
  createdAt: string;
}
export interface ClusterNodegroup {
  id: string;
  name: string;
  role: string; // master | worker
  flavorId: string;
  imageId: string;
  nodeCount: number;
  status: string;
  isDefault: boolean; // 기본(default-master/worker) — 삭제 불가
}
export interface ClusterDetail extends ClusterSummary {
  healthReason: Record<string, string>;
  statusReason: string;
  labels: Record<string, string>;
  fixedNetworkId: string;
  floatingIpEnabled: boolean;
  updatedAt: string;
  nodegroups: ClusterNodegroup[];
}

// 진행 중(…_IN_PROGRESS)이면 4초, 평상시엔 사용자 설정 주기 (IaaS 서버 목록과 동일 패턴)
const isClusterBusy = (s: string) => s.endsWith("_IN_PROGRESS");

export function useClusters(enabled = true) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-clusters"],
    enabled,
    queryFn: async (): Promise<ClusterSummary[]> =>
      (await fetchJson<{ items: ClusterSummary[] }>(`${PAAS_BASE}/api/paas/clusters`)).items,
    staleTime: 20 * 1000, retry: 0,
    refetchInterval: (q) =>
      ((q.state.data as ClusterSummary[] | undefined)?.some((c) => isClusterBusy(c.status)) ? 4000 : (pollMs || false)),
  });
}

export function useCluster(id: string | null) {
  return useQuery({
    queryKey: ["paas-cluster", id],
    enabled: !!id,
    queryFn: () => fetchJson<ClusterDetail>(`${PAAS_BASE}/api/paas/clusters/${id}`),
    staleTime: 10 * 1000, retry: 0,
    refetchInterval: (q) => {
      const d = q.state.data as ClusterDetail | undefined;
      return d && isClusterBusy(d.status) ? 4000 : false;
    },
  });
}

/** kubeconfig 다운로드 — MNT-PAAS 가 토큰을 확인하므로 링크 대신 토큰을 붙여 받아 파일로 저장한다. */
export async function downloadKubeconfig(id: string, name: string): Promise<void> {
  const res = await authFetch(`${PAAS_BASE}/api/paas/clusters/${id}/kubeconfig`);
  if (!res.ok) {
    let msg = `${res.status}`;
    try { msg = (await res.json()).detail ?? msg; } catch { /* keep */ }
    throw new Error(String(msg));
  }
  downloadText(`${name}-kubeconfig.yaml`, await res.text());
}

// ── 노드그룹 관리(관리자) — Magnum 비동기(UPDATE_IN_PROGRESS 로 바뀐 뒤 반영) ──
export interface CreateNodegroupInput { name: string; nodeCount: number; flavorId?: string }

function invalidateCluster(qc: ReturnType<typeof useQueryClient>, clusterId: string) {
  qc.invalidateQueries({ queryKey: ["paas-cluster", clusterId] });
  qc.invalidateQueries({ queryKey: ["paas-clusters"] });
}
export function useCreateNodegroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateNodegroupInput) =>
      sendJson("POST", `/api/paas/clusters/${clusterId}/nodegroups`, body),
    onSuccess: () => invalidateCluster(qc, clusterId),
  });
}
export function useResizeNodegroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ nodegroup, nodeCount }: { nodegroup: string; nodeCount: number }) =>
      sendJson("POST", `/api/paas/clusters/${clusterId}/nodegroups/${nodegroup}/resize`, { nodeCount }),
    onSuccess: () => invalidateCluster(qc, clusterId),
  });
}
export function useDeleteNodegroup(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (nodegroup: string) =>
      sendJson<void>("DELETE", `/api/paas/clusters/${clusterId}/nodegroups/${nodegroup}`),
    onSuccess: () => invalidateCluster(qc, clusterId),
  });
}

/** 클러스터 삭제(관리자). Magnum DELETE 는 비동기 — 목록은 DELETE_IN_PROGRESS 로 바뀐 뒤 소멸. */
export function useDeleteCluster() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `/api/paas/clusters/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-clusters"] }),
  });
}

// ── 전역 클러스터 선택 — 워크로드·서비스·스토리지·설정이 공유하는 스코프 ──
interface PaasClusterState {
  clusterId: string; // "" = 미선택
  setClusterId: (id: string) => void;
}
export const usePaasCluster = create<PaasClusterState>()(
  persist((set) => ({ clusterId: "", setClusterId: (id) => set({ clusterId: id }) }),
    { name: "okc-paas-cluster" }),
);

// ── 클러스터 내부 K8s 리소스 (읽기) — 선택된 클러스터의 API 질의 ──
const nsQ = (ns?: string) => (ns && ns !== "all" ? `?namespace=${encodeURIComponent(ns)}` : "");

export function useNamespaces(clusterId: string) {
  return useQuery({
    queryKey: ["paas-namespaces", clusterId],
    enabled: !!clusterId,
    queryFn: async () =>
      (await fetchJson<{ items: string[] }>(`${PAAS_BASE}/api/paas/clusters/${clusterId}/namespaces`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}

// 클러스터가 실제 보유한 가속기(노드 allocatable 합산 + 모델 라벨)
export interface ClusterAccelerator { kind: string; count: number; products: string[] }
export function useClusterAccelerators(clusterId: string) {
  return useQuery({
    queryKey: ["paas-accelerators", clusterId],
    enabled: !!clusterId,
    queryFn: async () =>
      (await fetchJson<{ items: ClusterAccelerator[] }>(
        `${PAAS_BASE}/api/paas/clusters/${clusterId}/accelerators`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}

export interface WorkloadRow {
  name: string;
  namespace: string;
  kind: "Deployment" | "StatefulSet" | "DaemonSet" | "Job" | "CronJob" | string;
  image: string;
  ready: number;
  total: number;
  requests: { cpu: string; memory: string };
  accelerator: { kind: string; count: number } | null;
  schedule?: string;   // CronJob 만 — 파드 목록(PodListRow)에는 없다
  status: string;
  labels: Record<string, string>;
  createdAt: string;
}
export function useWorkloads(clusterId: string, namespace?: string, enabled = true) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-workloads", clusterId, namespace ?? "all"],
    enabled: enabled && !!clusterId,
    queryFn: async () =>
      (await fetchJson<{ items: WorkloadRow[] }>(
        `${PAAS_BASE}/api/paas/clusters/${clusterId}/workloads${nsQ(namespace)}`)).items,
    retry: 0, refetchInterval: pollMs || false,
  });
}

/** 파드 목록(워크로드 화면에서 Pod 유형 선택 시) — 워크로드 행 + 소유자·재시작·노드·컨테이너 */
export interface PodListRow extends WorkloadRow {
  owner: { kind: string; name: string } | null;   // 소유 워크로드 — 없으면 독립 파드
  restarts: number;
  node: string;
  containers: string[];
}
export function usePods(clusterId: string, namespace?: string, enabled = true) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-pod-list", clusterId, namespace ?? "all"],
    enabled: enabled && !!clusterId,
    queryFn: async () =>
      (await fetchJson<{ items: PodListRow[] }>(
        `${PAAS_BASE}/api/paas/clusters/${clusterId}/pods${nsQ(namespace)}`)).items,
    retry: 0, refetchInterval: pollMs || false,
  });
}

export interface ServiceRow {
  name: string;
  namespace: string;
  type: string;
  clusterIp: string;
  externalIp: string;
  ports: { port: number; targetPort: string; protocol: string }[];
  selector: Record<string, string>;
  createdAt: string;
}
export interface IngressRow {
  name: string;
  namespace: string;
  className: string;
  hosts: string[];
  tls: boolean;
  ruleCount: number;
  createdAt: string;
}
export function useK8sServices(clusterId: string, namespace?: string) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-services", clusterId, namespace ?? "all"],
    enabled: !!clusterId,
    queryFn: () => fetchJson<{ services: ServiceRow[]; ingresses: IngressRow[] }>(
      `${PAAS_BASE}/api/paas/clusters/${clusterId}/services${nsQ(namespace)}`),
    retry: 0, refetchInterval: pollMs || false,
  });
}

export interface PvcRow {
  name: string; namespace: string; status: string; volumeName: string;
  capacity: string; accessModes: string[]; storageClass: string; createdAt: string;
}
export interface PvRow {
  name: string; capacity: string; accessModes: string[]; reclaimPolicy: string;
  status: string; claim: string; storageClass: string; csiDriver: string; createdAt: string;
}
export interface StorageClassRow {
  name: string; provisioner: string; reclaimPolicy: string; bindingMode: string;
  isDefault: boolean; parameters: Record<string, string>;
}
export function useK8sStorage(clusterId: string, namespace?: string) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-storage", clusterId, namespace ?? "all"],
    enabled: !!clusterId,
    queryFn: () => fetchJson<{ pvcs: PvcRow[]; pvs: PvRow[]; storageClasses: StorageClassRow[] }>(
      `${PAAS_BASE}/api/paas/clusters/${clusterId}/storage${nsQ(namespace)}`),
    retry: 0, refetchInterval: pollMs || false,
  });
}

export interface ConfigMapRow { name: string; namespace: string; keys: string[]; createdAt: string }
export interface SecretRow { name: string; namespace: string; type: string; keys: string[]; createdAt: string }
export function useK8sConfigs(clusterId: string, namespace?: string) {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-configs", clusterId, namespace ?? "all"],
    enabled: !!clusterId,
    queryFn: () => fetchJson<{ configmaps: ConfigMapRow[]; secrets: SecretRow[] }>(
      `${PAAS_BASE}/api/paas/clusters/${clusterId}/configs${nsQ(namespace)}`),
    retry: 0, refetchInterval: pollMs || false,
  });
}

// ── 클러스터 템플릿(Magnum ClusterTemplate) — 관리자 ──
export interface ClusterTemplate {
  id: string;
  name: string;
  coe: string;
  image_id: string | null;
  flavor_id: string | null;
  master_flavor_id: string | null;
  keypair_id: string | null;
  external_network_id: string | null;
  fixed_network: string | null;
  fixed_subnet: string | null;
  dns_nameserver: string | null;
  network_driver: string | null;
  volume_driver: string | null;
  docker_storage_driver: string | null;
  docker_volume_size: number | null;
  cluster_distro: string | null;
  server_type: string | null;
  public: boolean;
  hidden: boolean;
  tls_disabled: boolean;
  floating_ip_enabled: boolean;
  master_lb_enabled: boolean;
  registry_enabled: boolean;
  labels: Record<string, string> | null;
  project_id: string | null; // 소유 프로젝트 — 생성 때 고름(수정 불가). 신청 화면은 공개 또는 신청 프로젝트 소유만
  kubeVersion?: string; // 이미지의 K8s 버전(Glance 속성 kube_version, 앞의 v 없이)
  createdAt: string;
  updatedAt: string;
}
export function useClusterTemplates() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["paas-templates"],
    queryFn: async () => (await fetchJson<{ items: ClusterTemplate[] }>(`${PAAS_BASE}/api/paas/clustertemplates`)).items,
    retry: 0, refetchInterval: pollMs || false,
  });
}
export function useClusterTemplate(id: string | null) {
  return useQuery({
    queryKey: ["paas-template", id],
    enabled: !!id,
    queryFn: () => fetchJson<ClusterTemplate>(`${PAAS_BASE}/api/paas/clustertemplates/${id}`),
    retry: 0,
  });
}
export type ClusterTemplateInput = Partial<Omit<ClusterTemplate, "id" | "createdAt" | "updatedAt">>;
export function useCreateTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ClusterTemplateInput) => sendJson("POST", `/api/paas/clustertemplates`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-templates"] }),
  });
}
export function useUpdateTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: ClusterTemplateInput }) =>
      sendJson("PATCH", `/api/paas/clustertemplates/${id}`, body),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["paas-templates"] }); qc.invalidateQueries({ queryKey: ["paas-template"] }); },
  });
}
export function useDeleteTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `/api/paas/clustertemplates/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-templates"] }),
  });
}

// ── 쓰기(네임스페이스 리소스 CRUD) ──
const base = (clusterId: string) => `/api/paas/clusters/${clusterId}`;

/** 워크로드 생성(폼: Deployment/StatefulSet) */
export interface CreateWorkloadInput {
  kind?: "Deployment" | "StatefulSet";
  namespace: string; name: string; image: string; replicas: number;
  cpu?: string; memory?: string; acceleratorKind?: string; acceleratorCount?: number;
}
export function useCreateWorkload(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateWorkloadInput) => sendJson("POST", `${base(clusterId)}/workloads`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] }),
  });
}
export function useScaleWorkload(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, namespace, name, replicas }: { kind: string; namespace: string; name: string; replicas: number }) =>
      sendJson("POST", `${base(clusterId)}/workloads/${kind}/${namespace}/${name}/scale`, { replicas }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] }),
  });
}
export function useRestartWorkload(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, namespace, name }: { kind: string; namespace: string; name: string }) =>
      sendJson("POST", `${base(clusterId)}/workloads/${kind}/${namespace}/${name}/restart`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] }),
  });
}
export function useDeleteWorkload(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, namespace, name }: { kind: string; namespace: string; name: string }) =>
      sendJson<void>("DELETE", `${base(clusterId)}/workloads/${kind}/${namespace}/${name}`),
    onSuccess: () => {   // 파드 목록·드릴다운도 갱신(파드 삭제 가능)
      qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] });
      qc.invalidateQueries({ queryKey: ["paas-pod-list", clusterId] });
      qc.invalidateQueries({ queryKey: ["paas-pods", clusterId] });
    },
  });
}
/** YAML 로 워크로드 생성/적용(server-side apply) — Pod/Job/CronJob 등 모든 종류 */
export function useApplyWorkload(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (yaml: string) => sendJson("POST", `${base(clusterId)}/workloads/apply`, { yaml }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] });
      qc.invalidateQueries({ queryKey: ["paas-pod-list", clusterId] });
    },
  });
}
/** YAML 직접 수정 저장 */
export function useUpdateWorkloadYaml(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, namespace, name, yaml }: { kind: string; namespace: string; name: string; yaml: string }) =>
      sendJson("PUT", `${base(clusterId)}/workloads/${kind}/${namespace}/${name}/yaml`, { yaml }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-workloads", clusterId] }),
  });
}
/** 워크로드 YAML 조회(편집용) — 호출 시점에 가져온다 */
export function fetchWorkloadYaml(clusterId: string, kind: string, namespace: string, name: string) {
  return fetchJson<{ yaml: string }>(`${PAAS_BASE}${base(clusterId)}/workloads/${kind}/${namespace}/${name}/yaml`);
}
/** 적용 전 미리보기 — before(현재)·after(dryRun 적용 결과)·action. 클러스터에 아무것도 바꾸지 않는다 */
export interface WorkloadPreview { before: string; after: string; action: "create" | "update" }
export function previewWorkloadApply(clusterId: string, yaml: string) {
  return sendJson<WorkloadPreview>("POST", `${base(clusterId)}/workloads/apply/preview`, { yaml });
}

// ── 파드 드릴다운 ──
export interface PodRow {
  name: string; phase: string; ready: string; restarts: number;
  node: string; podIP: string; startedAt: string; containers: string[];
}
export function useWorkloadPods(clusterId: string, kind: string, namespace: string, name: string, enabled = true) {
  return useQuery({
    queryKey: ["paas-pods", clusterId, kind, namespace, name],
    enabled: enabled && !!clusterId,
    queryFn: async () => (await fetchJson<{ pods: PodRow[] }>(
      `${PAAS_BASE}${base(clusterId)}/workloads/${kind}/${namespace}/${name}/pods`)).pods,
    refetchInterval: 5000,
  });
}
export function fetchPodLogs(clusterId: string, namespace: string, pod: string, container?: string, tail = 500) {
  const q = new URLSearchParams({ tail: String(tail) });
  if (container) q.set("container", container);
  return fetchJson<{ logs: string }>(`${PAAS_BASE}${base(clusterId)}/pods/${namespace}/${pod}/logs?${q.toString()}`);
}

/** ConfigMap CRUD */
export function useCreateConfigMap(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { namespace: string; name: string; data: Record<string, string> }) =>
      sendJson("POST", `${base(clusterId)}/configmaps`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}
export function useUpdateConfigMap(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name, data }: { namespace: string; name: string; data: Record<string, string> }) =>
      sendJson("PUT", `${base(clusterId)}/configmaps/${namespace}/${name}`, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}
export function useDeleteConfigMap(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name }: { namespace: string; name: string }) =>
      sendJson<void>("DELETE", `${base(clusterId)}/configmaps/${namespace}/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}

/** Secret CRUD + 값 열람 */
export async function revealSecret(clusterId: string, namespace: string, name: string) {
  return fetchJson<{ name: string; namespace: string; type: string; data: Record<string, string> }>(
    `${PAAS_BASE}${base(clusterId)}/secrets/${namespace}/${name}`);
}
export function useCreateSecret(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { namespace: string; name: string; type?: string; data: Record<string, string> }) =>
      sendJson("POST", `${base(clusterId)}/secrets`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}
export function useUpdateSecret(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name, data }: { namespace: string; name: string; data: Record<string, string> }) =>
      sendJson("PUT", `${base(clusterId)}/secrets/${namespace}/${name}`, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}
export function useDeleteSecret(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ namespace, name }: { namespace: string; name: string }) =>
      sendJson<void>("DELETE", `${base(clusterId)}/secrets/${namespace}/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["paas-configs", clusterId] }),
  });
}
