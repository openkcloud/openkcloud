"use client";
/**
 * 자원신청 티켓(VM · AI 워크스페이스 · 서비스 클러스터) — MNT-CORE 연동(포털 DB). 신청자·처리자는 서버가 토큰으로 정한다(요청에 담지 않음).
 * 티켓 관련 호출은 MSW 목업으로 폴백하지 않는다 — 실패하면 오류를 보여준다(실제 티켓과 목업 데이터가 섞이지 않게).
 */
import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LOOKUP, lookupRetry, withTimeout } from "@/lib/http";
import { coreFetch } from "@/lib/menu-api";
import type { ClusterProvisionSpec, Org, ProvisionSpec, ProvisionStepsResp, ResourceRequest } from "@/lib/types";

/** 신청할 수 있는 프로젝트 — 내가 owner·member 인 프로젝트(이름은 OpenStack 라이브). */
export interface TicketProject { id: string; name: string; role: string; orgId: string | null; orgName: string | null }
export function useTicketProjects(username: string | undefined) {
  return useQuery({
    queryKey: ["tickets", "projects", username],
    enabled: !!username,
    queryFn: ({ signal }) => withTimeout(LOOKUP.timeoutMs, signal, async (s): Promise<TicketProject[]> =>
      (await coreFetch<{ items: TicketProject[] }>(`/tickets/projects`, { signal: s })).items),
    ...lookupRetry, // 한 번에 5초, 최대 3번 — 화면은 failureCount 로 "(n/3)" 표시
  });
}

/** 프로젝트 목록 조회 중 문구 — 몇 번째 시도인지(n/3). */
export function projectsLoadingText(failureCount: number) {
  return `프로젝트 목록 가져오는 중…(${Math.min(failureCount + 1, LOOKUP.attempts)}/${LOOKUP.attempts})`;
}

/** VM 이름 사용 가능 여부(같은 프로젝트의 기존 VM + 진행 중 티켓 — 완료·반려 제외). */
export function useVmNameCheck(projectId: string, name: string, enabled: boolean) {
  return useQuery({
    queryKey: ["tickets", "vm-name-check", projectId, name],
    enabled,
    queryFn: () =>
      coreFetch<{ available: boolean; reason: string | null }>(
        `/tickets/vm-name-check?${new URLSearchParams({ projectId, name })}`,
      ),
    retry: 1,
  });
}

/** 신청 목록 — filters: requestedBy(username)·status·type·orgId. requestedBy 가 없으면 전체(관리자).
 *  생성 중(provisioning)인 티켓이 있으면 상태가 바뀔 때까지 2초마다 다시 조회.
 *  (승인 → 생성 중은 관리자가 프로비저닝 버튼을 눌러야 바뀌므로 approved 는 다시 조회하지 않음) */
export function useTickets(filters: Record<string, string>, enabled = true) {
  return useQuery({
    queryKey: ["tickets", "list", filters],
    enabled,
    queryFn: () => coreFetch<{ items: ResourceRequest[]; total: number }>(`/tickets?${new URLSearchParams(filters)}`),
    retry: 0,
    refetchInterval: (query) =>
      query.state.data?.items.some((r) => r.status === "provisioning") ? 2000 : false,
  });
}

/** 조직 목록 — 신청 관리(관리자) 조직 필터. */
export function useOrgs() {
  return useQuery({
    queryKey: ["tickets", "orgs"],
    queryFn: async (): Promise<Org[]> => (await coreFetch<{ items: Org[] }>(`/orgs`)).items,
    retry: 0,
  });
}

export interface ReviewTicketInput {
  id: string;
  action: "approve" | "reject";
  reason?: string;         // 반려 사유 (반려 시 필수)
  expectedStatus: string;  // 화면이 보고 있던 상태 — 그사이 바뀌었으면 서버가 409
}

/** 승인(대기 → 승인)·반려(대기·승인 → 반려) — 관리자. 승인 설정은 프로비저닝 단계에서 입력. */
export function useReviewTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ReviewTicketInput) =>
      coreFetch<ResourceRequest>(`/tickets/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(body) }),
    // 실패(예: 다른 관리자가 먼저 처리 → 409)해도 목록을 다시 받아 최신 상태를 보여준다
    onSettled: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

export interface CreateTicketInput {
  projectId: string;
  title?: string;          // 신청 제목(선택)
  vmName: string;
  purpose?: string;        // 용도(선택, 300자)
  networkType: string;
  floatingIp: boolean;
  flavorId: string;
  imageId: string;
  volumeGb?: number;
  keypairId?: string;
  novaKeypair?: string;    // 관리자만 — OpenStack(Nova) 키페어 이름
}

/** VM 자원신청 — 서버가 멤버·이름·스펙·이미지·볼륨·키페어를 다시 확인한 뒤 저장. */
export function useCreateTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateTicketInput) =>
      coreFetch<ResourceRequest>(`/tickets`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

export interface ProvisionTicketInput {
  id: string;
  expectedStatus: string;  // 화면이 보고 있던 상태 — 그사이 바뀌었으면 서버가 409
  provisionSpec: Pick<ProvisionSpec, "networkId" | "floatingNetworkId" | "fixedIp" | "securityGroups" | "host" | "novaKeypair">;
}

/** 프로비저닝 시작(승인 → 생성 중) · 재시도(실패 → 생성 중, 새 회차) — 서버가 승인 설정·VM 이름을 다시 확인한 뒤
 *  Temporal 워크플로를 시작. 시작되면 그 티켓의 새 진행 기록을 받을 때까지 요청 중으로 둔다(refreshSteps). */
export function useProvisionTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ProvisionTicketInput) =>
      coreFetch<ResourceRequest>(`/tickets/${encodeURIComponent(id)}/provision`, { method: "POST", body: JSON.stringify(body) }),
    onSuccess: (_data, { id }) => refreshSteps(qc, id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

export interface CreateAiWorkspaceTicketInput {
  requestedBy: string;
  projectId: string;       // 이 프로젝트의 AI 워크스페이스 (프로젝트당 1개)
  clusterId?: string;      // 만들 서비스 클러스터 — AI 노트북이 여러 클러스터를 쓸 때(PaaS 모드)만
  purpose: string;
  cpu: number;
  memoryGi: number;
  acceleratorCount: number;
  storageGi: number;
}

/** AI 워크스페이스 신청 — 서버가 멤버·프로젝트·중복·워크스페이스 이름(MNT-AINB)을 다시 확인한 뒤 저장. */
export function useCreateAiWorkspaceTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateAiWorkspaceTicketInput) =>
      coreFetch<ResourceRequest>(`/tickets/ai-workspace`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

export interface ProvisionAiWorkspaceInput {
  id: string;
  processedBy: string;     // 처리자 username (admin 역할)
  expectedStatus: string;  // 화면이 보고 있던 상태 — 그사이 바뀌었으면 서버가 409
  // 한도 조정 — 빼면 신청 값 그대로
  cpu?: number;
  memoryGi?: number;
  acceleratorCount?: number;
  storageGi?: number;
}

/** AI 워크스페이스 생성 시작(승인 → 생성 중) · 재시도(실패 → 생성 중, 새 회차) — Temporal 워크플로 시작.
 *  VM 과 같이 새 진행 기록을 받을 때까지 요청 중으로 둔다(refreshSteps). */
export function useProvisionAiWorkspaceTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ProvisionAiWorkspaceInput) =>
      coreFetch<ResourceRequest>(`/tickets/${encodeURIComponent(id)}/provision-ai-workspace`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, { id }) => refreshSteps(qc, id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

/** 클러스터 이름 사용 가능 여부(모든 프로젝트의 클러스터 + 진행 중 신청). */
export function useClusterNameCheck(name: string, enabled: boolean) {
  return useQuery({
    queryKey: ["tickets", "cluster-name-check", name],
    enabled,
    queryFn: () =>
      coreFetch<{ available: boolean; reason: string | null }>(
        `/tickets/cluster-name-check?${new URLSearchParams({ name })}`,
      ),
    retry: 1,
  });
}

export interface CreateClusterTicketInput {
  projectId: string;
  title?: string;          // 신청 제목(선택)
  clusterName: string;
  templateId: string;
  masterCount: number;
  masterFlavorId: string;
  nodeCount: number;
  flavorId: string;        // Worker 플레이버 — 가속기는 이 플레이버의 device-profile (망·서브넷은 관리자가 프로비저닝 때)
  keypairId?: string;      // 본인 SSH 키(iam_ssh_key.id) — 없으면 템플릿 키페어
  novaKeypair?: string;    // 관리자만 — OpenStack(Nova) 키페어 이름
  purpose: string;
}

/** 서비스 클러스터 신청 — 서버가 멤버·이름·템플릿·플레이버·노드 수를 다시 확인한 뒤 저장. */
export function useCreateClusterTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateClusterTicketInput) =>
      coreFetch<ResourceRequest>(`/tickets/cluster`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

export interface ProvisionClusterInput {
  id: string;
  expectedStatus: string;  // 화면이 보고 있던 상태 — 그사이 바뀌었으면 서버가 409
  // 망·서브넷은 필수, 노드 구성·키페어는 빼면 신청 값 그대로. 키페어는 OpenStack 키페어 이름(null = 템플릿 키페어)
  provisionSpec: Pick<ClusterProvisionSpec, "networkId" | "subnetId">
    & Partial<Pick<ClusterProvisionSpec, "masterCount" | "masterFlavorId" | "nodeCount" | "flavorId">> & { novaKeypair?: string | null };
}

/** 클러스터 프로비저닝 시작(승인 → 생성 중) · 재시도(실패 → 생성 중, 새 회차) — 서버가 망·서브넷(DHCP·Floating IP)·
 *  플레이버·쿼터·이름을 다시 확인한 뒤 Temporal 워크플로 시작. VM 과 같이 새 진행 기록을 받을 때까지 요청 중으로 둔다. */
export function useProvisionClusterTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ProvisionClusterInput) =>
      coreFetch<ResourceRequest>(`/tickets/${encodeURIComponent(id)}/provision-cluster`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, { id }) => refreshSteps(qc, id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["tickets", "list"] }),
  });
}

/** VM 프로비저닝 시작 대기(예약) 취소 — 워크플로에 취소 신호만 보낸다(되돌려졌는지·그사이 시작했는지는 진행 조회로 확인). */
export function useCancelProvisionQueue() {
  return useMutation({
    mutationFn: (id: string) =>
      coreFetch<{ accepted: boolean }>(`/tickets/${encodeURIComponent(id)}/provision/cancel`, { method: "POST" }),
  });
}

/** 프로비저닝 진행 1회 조회 — receivedAt = 응답을 받은 순간(performance.now), now(DB 시각)와 짝지어 지금의 서버 시각을 추정한다. */
async function fetchTicketSteps(id: string) {
  const r = await coreFetch<ProvisionStepsResp>(`/tickets/${encodeURIComponent(id)}/steps`);
  // now 를 주기 전 CORE 면 PC 시각으로 대신한다(배포 순서가 엇갈린 동안)
  return { ...r, now: r.now ?? new Date().toISOString(), receivedAt: performance.now() };
}

/** 시작(재시도) 직후 그 티켓의 새 진행 기록을 받아 둔다 — 받을 때까지 요청 중(신청 상세는 '시작 중…', 진행 창은 이전 화면 그대로),
 *  받은 뒤 진행 화면·시작 대기(예약) 모달로. 이미 나가 있는 조회는 이전 회차 응답일 수 있어 끊고, 방금 받은 기록이 있어도 새로 받는다. */
async function refreshSteps(qc: QueryClient, id: string) {
  await qc.cancelQueries({ queryKey: ["tickets", "steps", id], exact: true });
  await qc.prefetchQuery({ queryKey: ["tickets", "steps", id], queryFn: () => fetchTicketSteps(id), staleTime: 0 });
}

/** 프로비저닝 진행(최신 회차 단계·상세 단계) — 생성 중이면 1초마다 다시 조회. enabled=false 면 받아 둔 값만 쓴다(닫힌 창). */
export function useTicketSteps(id: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ["tickets", "steps", id],
    enabled: !!id && enabled,
    queryFn: () => fetchTicketSteps(id!),
    refetchInterval: (query) => (query.state.data?.status === "provisioning" ? 1000 : false),
  });
}
