"use client";
/**
 * MNT-IAAS 연동 — IaaS 자원(OpenStack)은 전부 MNT-IAAS 모듈을 통해 가져온다.
 *
 * 주소(NEXT_PUBLIC_IAAS_API):
 *   - 미설정        → http://localhost:8001 (로컬 dev, MNT-IAAS 직접 — CORS 허용됨)
 *   - ""(빈값)      → same-origin (게이트웨이가 /api/iaas 라우팅하는 운영 구성)
 *   - "http://..."  → 고정 주소
 *
 * 실패 시 MSW 목업(/api/iaas/*)으로 폴백 — 백엔드 없이도 목업 동작 유지. 단 401·403(로그인·권한)은 폴백하지 않는다.
 * 모든 호출에 토큰(authFetch)을 붙인다.
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { IaasNetwork, ProvNetwork, SecurityGroup, Tenant } from "@/lib/types";
import { usePollingInterval } from "@/lib/polling";
import { ApiError, authFetch, readError } from "@/lib/http";
import { coreFetch } from "@/lib/menu-api";

const RAW = process.env.NEXT_PUBLIC_IAAS_API;
export const IAAS_BASE = RAW === undefined ? "http://localhost:8001" : RAW;

/** VM 웹 콘솔(noVNC) URL 발급 — 버튼 클릭 시 호출(호출마다 토큰 URL 새로). */
export async function getServerConsoleUrl(serverId: string): Promise<string> {
  const res = await authFetch(`${IAAS_BASE}/api/iaas/servers/${serverId}/console`, {
    headers: { "Content-Type": "application/json" },
  });
  if (!res.ok) throw await readError(res, `콘솔 URL 발급 실패 (${res.status})`);
  const d = (await res.json()) as { url?: string };
  if (!d.url) throw new Error("콘솔 URL이 비어있습니다");
  return d.url;
}

async function fetchJson<T>(url: string): Promise<T> {
  const res = await authFetch(url, { headers: { "Content-Type": "application/json" } });
  if (!res.ok) throw await readError(res);
  return (await res.json()) as T;
}

/** 로그인·권한 오류는 목업으로 가리지 않는다. */
function isAuthError(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 401 || e.status === 403);
}

async function sendJson<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await authFetch(url, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw await readError(res);
  if (res.status === 204) return undefined as T; // No Content (삭제)
  return (await res.json()) as T;
}

const postJson = <T>(url: string, body: unknown) => sendJson<T>("POST", url, body);

export interface CreateProjectInput {
  name: string;
  description?: string;
  quotaVcpu?: number;
  quotaRamGb?: number;
  quotaDiskGb?: number;
  quotaAcc?: number; // 비활성(-1) — OpenStack 미지원, 포털 DB 관리 예정
}

/** 프로젝트 생성 — MNT-IAAS가 Keystone 생성 + Nova/Cinder 쿼터 설정 → 포털 원장(iam_project)에 등록.
 *  등록은 조직 미지정(org_id NULL). 생성 자체는 OpenStack 에 완료되므로 등록 실패는 경고만 남기고 통과. */
export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: CreateProjectInput) => {
      const t = await postJson<Tenant>(`${IAAS_BASE}/api/iaas/projects`, input);
      try {
        await coreFetch(`/tickets/projects`, { method: "POST", body: JSON.stringify({ projectId: t.id }) });
      } catch (e) {
        console.warn("포털 프로젝트 등록 실패 — 자원신청 목록 반영이 지연될 수 있습니다:", e);
      }
      return t;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-projects"] }),
  });
}

export interface UpdateProjectInput {
  name?: string;
  description?: string;
  enabled?: boolean;
  quotaVcpu?: number;
  quotaRamGb?: number;
  quotaDiskGb?: number;
}

/** 프로젝트 수정 — Keystone 메타(이름·설명·활성) + Nova/Cinder 쿼터. */
export function useUpdateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: UpdateProjectInput & { id: string }) =>
      sendJson<Tenant>("PATCH", `${IAAS_BASE}/api/iaas/projects/${id}`, input),
    onSuccess: (_d, { id }) => {
      qc.invalidateQueries({ queryKey: ["iaas-projects"] });
      qc.invalidateQueries({ queryKey: ["iaas-project", id] });
    },
  });
}

/** 프로젝트 삭제 — Keystone 프로젝트 삭제(자원 사전 정리 전제). */
export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/projects/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-projects"] }),
  });
}

/** 프로젝트 목록(경량, 쿼터 미포함) — MNT-IAAS 우선, 실패 시 MSW 목업 폴백.
 *  쿼터는 N+1 방지를 위해 목록에 넣지 않고, 상세(useProject)에서 on-demand 조회한다. */
export function useProjects() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-projects"],
    queryFn: async (): Promise<{ items: Tenant[]; live: boolean }> => {
      try {
        const d = await fetchJson<{ items: Tenant[] }>(`${IAAS_BASE}/api/iaas/projects`);
        return { items: d.items, live: true };
      } catch (e) {
        if (isAuthError(e)) throw e;
        const d = await fetchJson<{ items: Tenant[] }>(`/api/iaas/projects`);
        return { items: d.items, live: false };
      }
    },
    staleTime: 30 * 1000,
    retry: 0,
    refetchInterval: pollMs || false,
  });
}

/** 단일 프로젝트 상세(+쿼터) — 신청 화면 등에서 특정 프로젝트 남은 쿼터 표시용. */
export function useProject(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<Tenant> =>
      fetchJson<Tenant>(`${IAAS_BASE}/api/iaas/projects/${projectId}`),
    staleTime: 30 * 1000,
    retry: 0,
  });
}

/** 남은 쿼터 = 한도 - 사용량 (음수 방지). 신청 화면에서 "남은 양" 표시 공통 유틸. */
export function remaining(quota: number, used: number): number {
  return Math.max(0, (quota ?? 0) - (used ?? 0));
}

// ────── 네트워크(Neutron, 읽기 전용) ──────
export interface NetworkDetail {
  id: string;
  name: string;
  tenantId: string;
  type: "tenant" | "external" | "shared";
  vlan: number | null;
  status: string;
  subnets: { id: string; name: string; cidr: string; gateway: string; dhcp: boolean; dns: string[]; allocationPool: string;
    internet?: boolean }[]; // internet: 게이트웨이 IP 를 가진 라우터에 외부 게이트웨이가 있음(인터넷망)
  ports: { id: string; name: string; status: string; mac: string; ip: string; deviceOwner: string; vm: string | null }[];
  securityGroups: { id: string; name: string; rules: number; desc: string }[];
  routers: { id: string; name: string; status: string; gatewayIp: string; routes: number; ha: boolean }[];
  floatingIps: { id: string; ip: string; portId: string | null; mappedTo: string; status: string }[];
  quotas: {
    ports: { used: number; max: number };
    floatingIps: { used: number; max: number };
    subnets: { used: number; max: number };
  };
}

/** 네트워크 목록(요약) — MNT-IAAS(Neutron) 우선, 실패 시 MSW 목업 폴백. */
export function useNetworks() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-networks"],
    queryFn: async (): Promise<{ items: IaasNetwork[]; live: boolean }> => {
      try {
        const d = await fetchJson<{ items: IaasNetwork[] }>(`${IAAS_BASE}/api/iaas/networks`);
        return { items: d.items, live: true };
      } catch (e) {
        if (isAuthError(e)) throw e;
        const d = await fetchJson<{ items: IaasNetwork[] }>(`/api/iaas/networks`);
        return { items: d.items, live: false };
      }
    },
    staleTime: 30 * 1000,
    retry: 0,
    refetchInterval: pollMs || false,
  });
}

export interface CreateNetworkInput {
  name: string;
  projectId: string;
  cidr: string;
  subnetName?: string;
  gatewayIp?: string;
  enableDhcp?: boolean;
  dnsNameservers?: string[];
  shared?: boolean;
}

/** 네트워크+서브넷 생성 — MNT-IAAS(Neutron). 성공 시 목록 갱신. */
export function useCreateNetwork() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateNetworkInput) =>
      postJson<NetworkDetail>(`${IAAS_BASE}/api/iaas/networks`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-networks"] }),
  });
}

/** 단일 네트워크 상세(+서브넷·포트·SG·라우터·FIP·쿼터) — 상세 패널 열 때 on-demand. */
export function useNetwork(networkId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-network", networkId],
    enabled: !!networkId,
    queryFn: async (): Promise<NetworkDetail> =>
      fetchJson<NetworkDetail>(`${IAAS_BASE}/api/iaas/networks/${networkId}`),
    staleTime: 30 * 1000,
    retry: 0,
  });
}

// ────── Floating IP (확보/연결/해제/반납) ──────
export interface FipTarget { portId: string; ip: string; networkName: string; vm: string | null }

/** 공인 IP 연결 대상(프로젝트 compute 포트) 조회. */
export function useFipTargets(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-fip-targets", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<FipTarget[]> =>
      (await fetchJson<{ items: FipTarget[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/fip-targets`)).items,
    staleTime: 15 * 1000,
    retry: 0,
  });
}

/** FIP 변경 후 네트워크 상세·목록 캐시 무효화 공통 훅 팩토리. */
function useFipMutation<V>(fn: (v: V) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-network"] });
      qc.invalidateQueries({ queryKey: ["iaas-networks"] });
    },
  });
}

/** 공인 IP 확보(allocate). */
export function useAllocateFip() {
  return useFipMutation((projectId: string) =>
    sendJson("POST", `${IAAS_BASE}/api/iaas/floatingips`, { projectId }));
}

/** 공인 IP 반납(release). */
export function useReleaseFip() {
  return useFipMutation((fipId: string) =>
    sendJson("DELETE", `${IAAS_BASE}/api/iaas/floatingips/${fipId}`));
}

/** 공인 IP 연결/해제 — portId 지정=연결, null=해제. */
export function useUpdateFip() {
  return useFipMutation(({ fipId, portId }: { fipId: string; portId: string | null }) =>
    sendJson("PUT", `${IAAS_BASE}/api/iaas/floatingips/${fipId}`, { portId }));
}

// ────── 서브넷 추가 / 네트워크 삭제 ──────
export interface AddSubnetInput {
  name?: string;
  cidr: string;
  gatewayIp?: string;
  enableDhcp?: boolean;
  dnsNameservers?: string[];
}

/** 기존 네트워크에 서브넷 추가. */
export function useAddSubnet(networkId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddSubnetInput) =>
      postJson<NetworkDetail>(`${IAAS_BASE}/api/iaas/networks/${networkId}/subnets`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-network", networkId] });
      qc.invalidateQueries({ queryKey: ["iaas-networks"] });
    },
  });
}

/** 네트워크 삭제. */
export function useDeleteNetwork() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/networks/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-networks"] }),
  });
}

// ────── 보안 그룹 규칙 편집 ──────
export interface SgRule {
  id: string;
  direction: string;
  ethertype: string;
  protocol: string | null;
  portMin: number | null;
  portMax: number | null;
  remoteCidr: string | null;
  remoteGroup: string | null;
}
export interface SecurityGroupDetail { id: string; name: string; desc: string; rules: SgRule[] }
export interface AddSgRuleInput {
  direction: string;
  ethertype?: string;
  protocol?: string | null;
  portMin?: number | null;
  portMax?: number | null;
  remoteCidr?: string | null;
}

/** 보안 그룹 + 규칙 상세. */
export function useSecurityGroup(sgId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-sg", sgId],
    enabled: !!sgId,
    queryFn: async (): Promise<SecurityGroupDetail> =>
      fetchJson<SecurityGroupDetail>(`${IAAS_BASE}/api/iaas/security-groups/${sgId}`),
    staleTime: 15 * 1000,
    retry: 0,
  });
}

/** 보안 그룹 규칙 추가. */
export function useAddSgRule(sgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddSgRuleInput) =>
      postJson<SecurityGroupDetail>(`${IAAS_BASE}/api/iaas/security-groups/${sgId}/rules`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-sg", sgId] });
      qc.invalidateQueries({ queryKey: ["iaas-network"] });
    },
  });
}

/** 보안 그룹 규칙 삭제. */
export function useDeleteSgRule(sgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (ruleId: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/security-group-rules/${ruleId}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-sg", sgId] });
      qc.invalidateQueries({ queryKey: ["iaas-network"] });
    },
  });
}

// ────── 보안 그룹 목록/생성/삭제 ──────
export interface SgSummary { id: string; name: string; tenantId: string; desc: string; rules: number }

export function useSecurityGroups() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-sgs"],
    queryFn: async (): Promise<SgSummary[]> =>
      (await fetchJson<{ items: SgSummary[] }>(`${IAAS_BASE}/api/iaas/security-groups`)).items,
    staleTime: 30 * 1000,
    retry: 0,
    refetchInterval: pollMs || false,
  });
}

export function useCreateSecurityGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; projectId: string; description?: string }) =>
      postJson<SecurityGroupDetail>(`${IAAS_BASE}/api/iaas/security-groups`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-sgs"] }),
  });
}

export function useDeleteSecurityGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/security-groups/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-sgs"] }),
  });
}

// ────── 라우터 (L3) ──────
export interface RouterSummary {
  id: string; name: string; tenantId: string; status: string; adminStateUp: boolean;
  externalNetwork: string; externalIp: string; interfaces: number; ha: boolean; routes: number;
}
export interface RouterInterface { portId: string; subnetId: string; subnetName: string; cidr: string; ip: string }
export interface RouterDetail {
  id: string; name: string; tenantId: string; status: string; adminStateUp: boolean;
  externalNetwork: string; externalIp: string; ha: boolean;
  interfaces: RouterInterface[];
  routes: { destination: string; nexthop: string }[];
}
export interface ProjectSubnet { id: string; name: string; cidr: string; networkName: string }

export function useRouters() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-routers"],
    queryFn: async (): Promise<RouterSummary[]> =>
      (await fetchJson<{ items: RouterSummary[] }>(`${IAAS_BASE}/api/iaas/routers`)).items,
    staleTime: 30 * 1000,
    retry: 0,
    refetchInterval: pollMs || false,
  });
}

export function useRouter(routerId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-router", routerId],
    enabled: !!routerId,
    queryFn: async (): Promise<RouterDetail> =>
      fetchJson<RouterDetail>(`${IAAS_BASE}/api/iaas/routers/${routerId}`),
    staleTime: 15 * 1000,
    retry: 0,
  });
}

export function useProjectSubnets(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project-subnets", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<ProjectSubnet[]> =>
      (await fetchJson<{ items: ProjectSubnet[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/subnets`)).items,
    staleTime: 15 * 1000,
    retry: 0,
  });
}

export function useCreateRouter() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; projectId: string; externalNetworkId?: string }) =>
      postJson<RouterDetail>(`${IAAS_BASE}/api/iaas/routers`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-routers"] }),
  });
}

export function useDeleteRouter() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/routers/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-routers"] }),
  });
}

/** 라우터 인터페이스 연결/해제 — add=true 연결, false 해제. */
export function useRouterInterface(routerId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ subnetId, add }: { subnetId: string; add: boolean }) =>
      sendJson<RouterDetail>("PUT", `${IAAS_BASE}/api/iaas/routers/${routerId}/${add ? "add" : "remove"}-interface`, { subnetId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-router", routerId] });
      qc.invalidateQueries({ queryKey: ["iaas-routers"] });
    },
  });
}

// ────── 볼륨·스토리지 (Cinder, 백엔드 Ceph) ──────
export interface VolumeAttachment { serverId: string | null; device: string | null }
export interface VolumeSummary {
  id: string; name: string; tenantId: string; size: number; type: string;
  status: string; bootable: boolean; attachments: VolumeAttachment[]; description: string; createdAt: string;
}
export interface VolumeSnapshot { id: string; name: string; size: number; status: string; createdAt: string }
export interface VolumeDetail extends VolumeSummary { snapshots: VolumeSnapshot[] }
export interface StoragePool { name: string; backend: string; protocol: string; totalGb: number; freeGb: number }
export interface VolumeTypeOpt { id: string; name: string }

// Cinder 전이(진행 중) 상태 — 이 상태가 하나라도 있으면 자동 폴링, 안정되면 중단.
const VOLUME_SETTLED = new Set(["available", "in-use", "error", "error_deleting", "error_extending", "error_restoring"]);
const isTransitional = (status: string) => !VOLUME_SETTLED.has(status);

export function useVolumes() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-volumes"],
    queryFn: async (): Promise<VolumeSummary[]> =>
      (await fetchJson<{ items: VolumeSummary[] }>(`${IAAS_BASE}/api/iaas/volumes`)).items,
    staleTime: 30 * 1000, retry: 0,
    // 진행 중(creating/deleting/extending 등)이면 3초로 빠르게, 평상시엔 사용자 설정 주기
    refetchInterval: (q) => ((q.state.data as VolumeSummary[] | undefined)?.some((v) => isTransitional(v.status)) ? 3000 : (pollMs || false)),
  });
}
export function useVolume(id: string | undefined) {
  return useQuery({
    queryKey: ["iaas-volume", id],
    enabled: !!id,
    queryFn: async (): Promise<VolumeDetail> => fetchJson<VolumeDetail>(`${IAAS_BASE}/api/iaas/volumes/${id}`),
    staleTime: 15 * 1000, retry: 0,
    // 볼륨 또는 스냅샷이 진행 중이면 폴링
    refetchInterval: (q) => {
      const d = q.state.data as VolumeDetail | undefined;
      if (!d) return false;
      const pending = isTransitional(d.status) || d.snapshots.some((s) => isTransitional(s.status));
      return pending ? 3000 : false;
    },
  });
}
export function useVolumeTypes() {
  return useQuery({
    queryKey: ["iaas-volume-types"],
    queryFn: async (): Promise<VolumeTypeOpt[]> =>
      (await fetchJson<{ items: VolumeTypeOpt[] }>(`${IAAS_BASE}/api/iaas/volume-types`)).items,
    staleTime: 5 * 60 * 1000, retry: 0,
  });
}
export function useStorageCapacity() {
  return useQuery({
    queryKey: ["iaas-storage-capacity"],
    queryFn: async (): Promise<StoragePool[]> =>
      (await fetchJson<{ items: StoragePool[] }>(`${IAAS_BASE}/api/iaas/storage-capacity`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}

function useVolumeMutation<V, R = unknown>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation<R, Error, V>({
    mutationFn: fn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-volumes"] });
      qc.invalidateQueries({ queryKey: ["iaas-volume"] });
      qc.invalidateQueries({ queryKey: ["iaas-storage-capacity"] });
    },
  });
}

export interface CreateVolumeInput {
  name: string; size: number; projectId: string; volumeType?: string; description?: string; imageRef?: string; snapshotId?: string;
}
export function useCreateVolume() {
  return useVolumeMutation((input: CreateVolumeInput) =>
    postJson<VolumeSummary>(`${IAAS_BASE}/api/iaas/volumes`, input));
}
export function useDeleteVolume() {
  return useVolumeMutation(({ id, cascade }: { id: string; cascade?: boolean }) =>
    sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/volumes/${id}${cascade ? "?cascade=true" : ""}`));
}
export function useExtendVolume() {
  return useVolumeMutation(({ id, newSize }: { id: string; newSize: number }) =>
    sendJson<VolumeDetail>("POST", `${IAAS_BASE}/api/iaas/volumes/${id}/extend`, { newSize }));
}
export function useCreateSnapshot() {
  return useVolumeMutation((input: { volumeId: string; name: string; force?: boolean }) =>
    postJson(`${IAAS_BASE}/api/iaas/snapshots`, input));
}
export function useDeleteSnapshot() {
  return useVolumeMutation((id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/snapshots/${id}`));
}

// ────── 이미지 (Glance) ──────
export interface ImageSummary {
  id: string; name: string; tenantId: string; os: string; diskFormat: string; containerFormat: string;
  sizeGb: number; visibility: string; status: string; protected: boolean; minDisk: number; minRam: number; createdAt: string;
  minVolumeGb: number; // 부팅 볼륨 최소 크기 = max(min_disk, 이미지 가상 크기)
  osVersion?: string;   // Glance 속성 os_version
  kubeVersion?: string; // Glance 속성 kube_version (클러스터 템플릿용)
}

export function useImages() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-images"],
    queryFn: async (): Promise<ImageSummary[]> =>
      (await fetchJson<{ items: ImageSummary[] }>(`${IAAS_BASE}/api/iaas/images`)).items,
    staleTime: 60 * 1000, retry: 0,
    refetchInterval: pollMs || false,
  });
}
/** 프로젝트별 쿼터·사용량 — 신청 화면 프로젝트 카드(남은 vCPU·RAM·Disk). 사용자는 소속 프로젝트만. 목업 폴백 없음. */
export function useProjectQuotas(enabled = true) {
  return useQuery({
    queryKey: ["iaas-projects", "detail"],
    enabled,
    queryFn: async (): Promise<Tenant[]> => (await fetchJson<{ items: Tenant[] }>(`${IAAS_BASE}/api/iaas/projects?detail=true`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}
/** 프로젝트가 쓸 수 있는 이미지(public + 프로젝트 소유 + 공유받은 것) — 자원신청 템플릿. 목업 폴백 없음.
 *  cloudInit=false 면 키페어 주입 불가(Glance 속성 okc_cloud_init=false). */
export type ProjectImage = ImageSummary & { cloudInit: boolean };
export function useProjectImages(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project-images", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<ProjectImage[]> =>
      (await fetchJson<{ items: ProjectImage[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/images`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}
/** 승인 설정 — 티켓 프로젝트 기준 네트워크(프로젝트 소유 + 공유 + 외부). 목업 폴백 없음. */
export function useProjectNetworks(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project-networks", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<ProvNetwork[]> =>
      (await fetchJson<{ items: ProvNetwork[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/networks`)).items,
    staleTime: 30 * 1000, retry: 0,
  });
}
/** 승인 판단 — 컴퓨트 노드별 후보 여부(Placement)·가속기 여유·프로젝트 쿼터(라이브). 목업 폴백 없음. */
export interface PlacementPreview {
  nodes: { // 모든 컴퓨트 노드 — 후보(신청 스펙이 들어감) 먼저, 각각 여유가 큰 순
    host: string; candidate: boolean; disabled: boolean; // disabled = 꺼 둔 노드
    vcpu: { used: number; capacity: number }; ramMb: { used: number; capacity: number };
    accel: { used: number; capacity: number }[]; // 노드의 가속기 사용/용량 — accelerators 와 같은 순서
  }[];
  accelerators: { kind: string; resourceClass: string; traits: string[]; count: number; free: number; total: number }[]; // 종류별 클러스터 전체, traits 는 금지면 '!' 접두
  quota: Record<"vcpu" | "ramGb" | "instances" | "diskGb" | "volumes", { quota: number; used: number }> | null; // quota -1 = 무제한
  quotaError: string | null;
  hostPinning: boolean; // VM 생성 때 노드를 지정할 수 있는지(Nova 정책) — 없으면 스케줄러가 후보 중에서 고름
}
export function usePlacementPreview(flavorId: string | undefined, projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-placement-preview", flavorId, projectId],
    enabled: !!flavorId && !!projectId,
    queryFn: () => fetchJson<PlacementPreview>(
      `${IAAS_BASE}/api/iaas/placement-preview?${new URLSearchParams({ flavorId: flavorId!, projectId: projectId! })}`),
    staleTime: 30 * 1000, retry: 0,
  });
}
/** 승인 설정 — 프로젝트 보안그룹(라이브). 목업 폴백 없음. */
export function useProjectSecurityGroups(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project-sgs", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<SecurityGroup[]> =>
      (await fetchJson<{ items: SecurityGroup[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/security-groups`)).items,
    staleTime: 30 * 1000, retry: 0,
  });
}
/** Static IP 사용 가능 여부 — 서브넷 범위·게이트웨이·사용 중 포트(Neutron). */
export function useIpCheck(networkId: string, ip: string, enabled: boolean) {
  return useQuery({
    queryKey: ["iaas-ip-check", networkId, ip],
    enabled,
    queryFn: () =>
      fetchJson<{ available: boolean; reason: string | null }>(
        `${IAAS_BASE}/api/iaas/networks/${networkId}/ip-check?${new URLSearchParams({ ip })}`,
      ),
    retry: 1,
  });
}
export function useSetImageVisibility() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, visibility }: { id: string; visibility: string }) =>
      sendJson<ImageSummary>("PATCH", `${IAAS_BASE}/api/iaas/images/${id}`, { visibility }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-images"] }),
  });
}
export function useDeleteImage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/images/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-images"] }),
  });
}
// 이미지 상세 — 편집 가능한 속성(메타데이터) 포함(os_distro 등)
export type ImageDetail = ImageSummary & { properties: Record<string, string> };
export function useImage(id: string | undefined) {
  return useQuery({
    queryKey: ["iaas-image", id],
    enabled: !!id,
    queryFn: () => fetchJson<ImageDetail>(`${IAAS_BASE}/api/iaas/images/${id}`),
    staleTime: 10 * 1000, retry: 0,
  });
}
/** 이미지 속성(메타데이터) 추가·수정·삭제 — os_distro 등. */
export function useUpdateImageProperties() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, set, remove }: { id: string; set?: Record<string, string>; remove?: string[] }) =>
      sendJson<ImageDetail>("PATCH", `${IAAS_BASE}/api/iaas/images/${id}/properties`, { set: set ?? {}, remove: remove ?? [] }),
    onSuccess: (_d, v) => {
      qc.invalidateQueries({ queryKey: ["iaas-image", v.id] });
      qc.invalidateQueries({ queryKey: ["iaas-images"] });
    },
  });
}
export interface RegisterImageInput {
  name: string; url: string; diskFormat: string; containerFormat: string;
  visibility: string; minDisk: number; minRam: number; properties: Record<string, string>;
}
/** URL 가져오기(web-download) 등록 — Glance 가 URL 에서 직접 내려받음. */
export function useRegisterImage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: RegisterImageInput) => postJson<ImageDetail>(`${IAAS_BASE}/api/iaas/images`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-images"] }),
  });
}

// ────── 플레이버 (Nova) — 전역 자원(프로젝트 없음) ──────
export interface FlavorSummary {
  id: string; name: string; vcpus: number; ramMb: number; diskGb: number; isPublic: boolean; accelerator: string | null;
  accelerators?: { kind: string; count: number }[]; // device-profile 의 가속기 종류(GPU/NPU)·개수
  deviceProfileMissing?: boolean; // 가리키는 device-profile 이 Cyborg 에 없음 — 신청 불가
}
export interface DeviceProfile {
  name: string;
  uuid: string;
  description?: string | null;
  accelerators?: { kind: string; count: number }[];
  groups?: Record<string, string>[]; // 원본 그룹(resources:*, trait:*)
}

/** 프로파일 groups 의 고유 트레잇(하드웨어 식별자 CUSTOM_NVIDIA_26B9 등) — 목록·셀렉트 표기용. */
export function profileTraits(groups?: Record<string, string>[]): string[] {
  const s = new Set<string>();
  for (const g of groups ?? [])
    for (const k of Object.keys(g)) if (k.startsWith("trait:")) s.add(k.slice(6));
  return Array.from(s);
}

/** IaaS 목록 공통 정렬 비교자 — 프로젝트명 → 자원명(한국어 로캘). 프로젝트 컬럼이 있는 화면 공용. */
export function byProjectThenName<T extends { tenantId?: string; name: string }>(projName: (id: string) => string) {
  return (a: T, b: T) =>
    projName(a.tenantId ?? "").localeCompare(projName(b.tenantId ?? ""), "ko") ||
    a.name.localeCompare(b.name, "ko");
}

/** 프로젝트가 쓸 수 있는 플레이버(public + 이 프로젝트에 허용된 비공개) — VM·클러스터 신청. 목업 폴백 없음. */
export function useProjectFlavors(projectId: string | undefined) {
  return useQuery({
    queryKey: ["iaas-project-flavors", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<FlavorSummary[]> =>
      (await fetchJson<{ items: FlavorSummary[] }>(`${IAAS_BASE}/api/iaas/projects/${projectId}/flavors`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}

export function useFlavors() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-flavors"],
    queryFn: async (): Promise<FlavorSummary[]> =>
      (await fetchJson<{ items: FlavorSummary[] }>(`${IAAS_BASE}/api/iaas/flavors`)).items,
    staleTime: 60 * 1000, retry: 0,
    refetchInterval: pollMs || false,
  });
}
export function useDeviceProfiles() {
  return useQuery({
    queryKey: ["iaas-device-profiles"],
    queryFn: async (): Promise<DeviceProfile[]> =>
      (await fetchJson<{ items: DeviceProfile[] }>(`${IAAS_BASE}/api/iaas/device-profiles`)).items,
    staleTime: 5 * 60 * 1000, retry: 0,
  });
}

// ── 가속기 모델명 매핑(포털 DB, MNT-CORE) — Cyborg 가 모델명을 안 줘서 product_id → 모델명 관리 ──
export interface AccelModel { productId: string; vendor: string | null; model: string }
export function useAccelModels() {
  return useQuery({
    queryKey: ["core-accel-models"],
    queryFn: async (): Promise<AccelModel[]> => (await coreFetch<{ items: AccelModel[] }>(`/accel-models`)).items,
    staleTime: 5 * 60 * 1000, retry: 0,
  });
}
export function useUpsertAccelModel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: { productId: string; vendor?: string | null; model: string }) =>
      coreFetch(`/accel-models`, { method: "PUT", body: JSON.stringify(b) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["core-accel-models"] }),
  });
}
export function useDeleteAccelModel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (productId: string) => coreFetch<void>(`/accel-models/${productId}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["core-accel-models"] }),
  });
}
/** 트레잇(CUSTOM_NVIDIA_26B9)에서 끝 product_id(26b9) 추출 */
export function productIdFromTrait(trait: string): string | null {
  const m = /_([0-9a-fA-F]{3,8})$/.exec(trait);
  return m ? m[1].toLowerCase() : null;
}
/** device-profile groups 의 첫 트레잇에서 product_id 추출 */
export function productIdFromProfile(dp: DeviceProfile): string | null {
  for (const g of dp.groups ?? [])
    for (const k of Object.keys(g))
      if (k.startsWith("trait:")) {
        const pid = productIdFromTrait(k.slice(6));
        if (pid) return pid;
      }
  return null;
}
/** product_id → 모델명 맵 */
export function accelModelByProductId(models: AccelModel[] | undefined): Record<string, string> {
  const m: Record<string, string> = {};
  for (const x of models ?? []) m[x.productId.toLowerCase()] = x.model;
  return m;
}
/** device-profile 이름 → 표시 라벨 리졸버. 매핑되면 모델명, 매핑 없으면 product_id, 트레잇 없으면 dp 이름 유지. */
export function accelLabelResolver(
  profiles: DeviceProfile[] | undefined,
  models: AccelModel[] | undefined,
): (dpName: string | null | undefined) => string | null {
  const byPid = accelModelByProductId(models);
  const byDp: Record<string, string> = {};
  for (const dp of profiles ?? []) {
    const pid = productIdFromProfile(dp);
    if (pid) byDp[dp.name] = byPid[pid] ?? pid;
  }
  return (dpName) => (dpName ? (byDp[dpName] ?? dpName) : null);
}
// 디바이스 프로파일 생성/삭제 (관리자) — Cyborg 는 불변이라 수정은 없다.
// 가속기 1종류 = 장치 조건. 수량은 Cyborg group 복제 수(각 group 개수는 1 고정).
export interface CreateDeviceProfileInput {
  name: string;
  description?: string;
  accelerators: { resourceClass: string; quantity: number; traits: { name: string; policy: "required" | "forbidden" }[] }[];
}
export function useCreateDeviceProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateDeviceProfileInput) =>
      postJson<DeviceProfile>(`${IAAS_BASE}/api/iaas/device-profiles`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-device-profiles"] });
      qc.invalidateQueries({ queryKey: ["iaas-flavors"] }); // deviceProfileMissing 재평가
    },
  });
}
export function useDeleteDeviceProfile() {
  const qc = useQueryClient();
  return useMutation({
    // force=true 면 참조 플레이버가 있어도 강제 삭제(기본은 백엔드가 409 로 막음)
    mutationFn: ({ uuid, force }: { uuid: string; force?: boolean }) =>
      sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/device-profiles/${uuid}${force ? "?force=true" : ""}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-device-profiles"] });
      qc.invalidateQueries({ queryKey: ["iaas-flavors"] });
    },
  });
}

// Nova keypair(클러스터 노드 SSH·인프라, 관리자). 사용자 포털 DB SSH키와 별개 자원.
export interface NovaKeypair { name: string; fingerprint: string; publicKey: string; type: string }
export function useNovaKeypairs(enabled = true) {
  return useQuery({
    queryKey: ["iaas-nova-keypairs"],
    enabled,
    queryFn: async (): Promise<NovaKeypair[]> =>
      (await fetchJson<{ items: NovaKeypair[] }>(`${IAAS_BASE}/api/iaas/keypairs`)).items,
    staleTime: 60 * 1000, retry: 0,
  });
}
export function useImportNovaKeypair() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; publicKey: string }) =>
      postJson<NovaKeypair>(`${IAAS_BASE}/api/iaas/keypairs`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-nova-keypairs"] }),
  });
}
export function useDeleteNovaKeypair() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/keypairs/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-nova-keypairs"] }),
  });
}

// 가속기 장치 현황(Cyborg 5-2) — 물리 장치 인벤토리 + 사용 상태(조회 전용)
export interface AcceleratorDevice {
  id: number | string | null;
  uuid: string;
  hostname: string | null;
  type: string | null;
  model: string | null;
  vendor: string | null;
  productId: string | null; // std_board_info.product_id (모델명 매핑 키)
  resourceClasses: string[];
  traits: string[];
  usage: "used" | "available" | "unknown";
  instances: { id: string; name: string | null }[];
}
export function useAcceleratorDevices() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-accel-devices"],
    queryFn: async (): Promise<AcceleratorDevice[]> =>
      (await fetchJson<{ items: AcceleratorDevice[] }>(`${IAAS_BASE}/api/iaas/accelerator-devices`)).items,
    staleTime: 10 * 1000, retry: 0,
    refetchInterval: pollMs || false,
  });
}
export interface CreateFlavorInput {
  name: string; vcpus: number; ramMb: number; diskGb: number; isPublic?: boolean; deviceProfile?: string;
}
export function useCreateFlavor() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateFlavorInput) => postJson<FlavorSummary>(`${IAAS_BASE}/api/iaas/flavors`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-flavors"] }),
  });
}
export function useDeleteFlavor() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/flavors/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-flavors"] }),
  });
}

// ────── 가상서버 (Nova, 운영: 조회·전원·삭제·볼륨 연결/분리) ──────
export interface ServerAddress { network: string; ip: string; type: string | null }
export interface ServerSummary {
  id: string; name: string; tenantId: string; status: string; vmState: string; taskState: string | null; powerState: number;
  flavorName: string; vcpus: number; ramMb: number; accelerator: string | null; host: string;
  keyName: string | null; imageId: string | null; addresses: ServerAddress[]; volumeCount: number; createdAt: string;
}
export interface ServerInterface { portId: string; netId: string; state: string | null; ips: string[] }
export interface ServerDetail extends ServerSummary {
  attachedVolumes: { volumeId: string; device: string | null }[];
  interfaces: ServerInterface[];
  securityGroups: string[];
  imageName?: string | null;    // 볼륨 부팅 VM — 부팅 볼륨의 원본 이미지 이름(volume_image_metadata)
  bootVolumeId?: string | null; // 볼륨 부팅 VM 의 부팅 볼륨
}

// 진행 중(전이) 상태면 폴링, 안정되면 중단
const SERVER_SETTLED = new Set(["ACTIVE", "SHUTOFF", "ERROR", "PAUSED", "SUSPENDED"]);
// status가 안정값이어도 taskState(powering-off/on·rebooting 등)가 남아 있으면 아직 전이 중 → 폴링 유지.
// (정지 직후 Nova는 잠시 status=ACTIVE + taskState=powering-off 로 머문다 → status만 보면 폴링이 멈춰 뱃지가 안 바뀜)
const isServerBusy = (s: { status: string; taskState?: string | null }) => !SERVER_SETTLED.has(s.status) || !!s.taskState;

export function useServers() {
  const pollMs = usePollingInterval((s) => s.intervalMs);
  return useQuery({
    queryKey: ["iaas-servers"],
    queryFn: async (): Promise<ServerSummary[]> =>
      (await fetchJson<{ items: ServerSummary[] }>(`${IAAS_BASE}/api/iaas/servers`)).items,
    staleTime: 20 * 1000, retry: 0,
    // 진행 중(BUILD/REBOOT 등)이면 4초로 빠르게, 평상시엔 사용자 설정 주기
    refetchInterval: (q) => ((q.state.data as ServerSummary[] | undefined)?.some((s) => isServerBusy(s)) ? 4000 : (pollMs || false)),
  });
}
export function useServer(id: string | undefined) {
  return useQuery({
    queryKey: ["iaas-server", id],
    enabled: !!id,
    queryFn: async (): Promise<ServerDetail> => fetchJson<ServerDetail>(`${IAAS_BASE}/api/iaas/servers/${id}`),
    staleTime: 10 * 1000, retry: 0,
    refetchInterval: (q) => { const d = q.state.data as ServerDetail | undefined; return d && isServerBusy(d) ? 4000 : false; },
  });
}

export interface ServerDiagnostics {
  state: string; uptime: number; numCpus: number; cpuTimeNs: number;
  memoryMb: number; memoryUsedMb: number;
  diskReadBytes: number; diskWriteBytes: number; nicRxBytes: number; nicTxBytes: number;
}
/** 하이퍼바이저 진단(현재 카운터) — active일 때만 3초 폴링. CPU%·속도는 컴포넌트가 델타로 계산. */
export function useServerDiagnostics(id: string | undefined, active: boolean) {
  return useQuery({
    queryKey: ["iaas-server-diag", id],
    enabled: !!id && active,
    queryFn: async (): Promise<ServerDiagnostics> =>
      fetchJson<ServerDiagnostics>(`${IAAS_BASE}/api/iaas/servers/${id}/diagnostics`),
    refetchInterval: 3000, staleTime: 0, retry: 0,
  });
}

function useServerMutation<V, R = unknown>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation<R, Error, V>({
    mutationFn: fn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["iaas-servers"] });
      qc.invalidateQueries({ queryKey: ["iaas-server"] });
    },
  });
}

/** 전원 제어 — action: start|stop|reboot. */
export function usePowerAction() {
  return useServerMutation(({ id, action, hard }: { id: string; action: "start" | "stop" | "reboot"; hard?: boolean }) =>
    postJson(`${IAAS_BASE}/api/iaas/servers/${id}/power`, { action, hard: !!hard }));
}
export function useDeleteServer() {
  return useServerMutation((id: string) => sendJson<void>("DELETE", `${IAAS_BASE}/api/iaas/servers/${id}`));
}
/** 인스턴스 스냅샷 — 현재 서버를 이미지로 저장(그 이미지로 새 VM 생성 가능). 생성 후 이미지 목록 갱신. */
export function useCreateServerSnapshot() {
  const qc = useQueryClient();
  return useMutation<{ ok: boolean; imageId: string | null; name: string }, Error, { id: string; name: string }>({
    mutationFn: ({ id, name }) => postJson(`${IAAS_BASE}/api/iaas/servers/${id}/snapshot`, { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["iaas-images"] }),
  });
}
export function useAttachVolume() {
  return useServerMutation(({ serverId, volumeId }: { serverId: string; volumeId: string }) =>
    postJson<ServerDetail>(`${IAAS_BASE}/api/iaas/servers/${serverId}/volumes`, { volumeId }));
}
export function useDetachVolume() {
  return useServerMutation(({ serverId, volumeId }: { serverId: string; volumeId: string }) =>
    sendJson<ServerDetail>("DELETE", `${IAAS_BASE}/api/iaas/servers/${serverId}/volumes/${volumeId}`));
}
/** 네트워크 인터페이스(NIC) 연결 — 포털 네트워크 선택. */
export function useAttachInterface() {
  return useServerMutation(({ serverId, networkId }: { serverId: string; networkId: string }) =>
    postJson<ServerDetail>(`${IAAS_BASE}/api/iaas/servers/${serverId}/interfaces`, { networkId }));
}
export function useDetachInterface() {
  return useServerMutation(({ serverId, portId }: { serverId: string; portId: string }) =>
    sendJson("DELETE", `${IAAS_BASE}/api/iaas/servers/${serverId}/interfaces/${portId}`));
}
/** 가상서버 보안그룹 추가 — Nova addSecurityGroup(이름 기준). Horizon '보안 그룹 편집'과 동일. */
export function useAddServerSecurityGroup() {
  return useServerMutation(({ serverId, name }: { serverId: string; name: string }) =>
    postJson(`${IAAS_BASE}/api/iaas/servers/${serverId}/security-groups`, { name }));
}
/** 가상서버 보안그룹 제거 — Nova removeSecurityGroup. */
export function useRemoveServerSecurityGroup() {
  return useServerMutation(({ serverId, name }: { serverId: string; name: string }) =>
    sendJson("DELETE", `${IAAS_BASE}/api/iaas/servers/${serverId}/security-groups/${encodeURIComponent(name)}`));
}
/** 스펙(플레이버) 변경 — resize 시작 → VERIFY_RESIZE 후 confirm/revert. */
export function useResizeServer() {
  return useServerMutation(({ id, flavorId }: { id: string; flavorId: string }) =>
    postJson(`${IAAS_BASE}/api/iaas/servers/${id}/resize`, { flavorId }));
}
export function useResizeConfirm() {
  return useServerMutation((id: string) => postJson(`${IAAS_BASE}/api/iaas/servers/${id}/resize-confirm`, {}));
}
export function useResizeRevert() {
  return useServerMutation((id: string) => postJson(`${IAAS_BASE}/api/iaas/servers/${id}/resize-revert`, {}));
}
