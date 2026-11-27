"use client";
/** 프로젝트 멤버 관리 — MNT-CORE 연동(포털 DB). OpenStack 멤버와 별개. */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { coreFetch } from "@/lib/menu-api";

export interface PortalUser { id: string; username: string; name: string; org: string; status: string }
export interface ProjectMember {
  userId: string; username: string; name: string; org: string; email: string | null; role: string; createdAt: string;
}

/** 포털 사용자 목록 — 멤버 추가 picker용. */
/** 포털 사용자 목록 — 관리자 전용 API(멤버 추가 후보). enabled 로 사용자 화면에서는 부르지 않는다. */
export function usePortalUsers(enabled = true) {
  return useQuery({
    queryKey: ["core-users"],
    enabled,
    queryFn: async (): Promise<PortalUser[]> => (await coreFetch<{ items: PortalUser[] }>(`/users`)).items,
    staleTime: 5 * 60 * 1000, retry: 0,
  });
}

/** 내가 멤버인 프로젝트 ID 집합 — 사용자 화면 스코프(프로젝트·VM·네트워크·볼륨 필터). 사용자는 서버가 토큰으로 정한다.
 *  username 은 캐시 구분·호출 여부(관리자는 undefined → 호출 안 함)용. */
export function useMyProjectIds(username: string | undefined) {
  return useQuery({
    queryKey: ["core-my-projects", username],
    enabled: !!username,
    queryFn: async (): Promise<Set<string>> =>
      new Set((await coreFetch<{ projectIds: string[] }>(`/my-projects`)).projectIds),
    staleTime: 60 * 1000, retry: 0,
  });
}

export function useProjectMembers(projectId: string | undefined) {
  return useQuery({
    queryKey: ["core-members", projectId],
    enabled: !!projectId,
    queryFn: async (): Promise<ProjectMember[]> =>
      (await coreFetch<{ items: ProjectMember[] }>(`/projects/${projectId}/members`)).items,
    staleTime: 30 * 1000, retry: 0,
  });
}

export function useAddProjectMember(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { userId: number; role: string }) =>
      coreFetch(`/projects/${projectId}/members`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["core-members", projectId] });
      qc.invalidateQueries({ queryKey: ["core-project-member-counts"] }); // 프로젝트 목록의 멤버 수
    },
  });
}

export function useUpdateProjectMemberRole(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      coreFetch(`/projects/${projectId}/members/${userId}`, { method: "PATCH", body: JSON.stringify({ role }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["core-members", projectId] }),
  });
}

export function useRemoveProjectMember(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      coreFetch<void>(`/projects/${projectId}/members/${userId}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["core-members", projectId] });
      qc.invalidateQueries({ queryKey: ["core-project-member-counts"] }); // 프로젝트 목록의 멤버 수
    },
  });
}

// ── PaaS 서비스 클러스터 접근 권한(프로젝트↔클러스터 N:N) ──
export interface ClusterAccess { projectId: string; namespace: string | null; createdAt: string | null }

/** 내가 접근 가능한 클러스터 ID 집합 — 사용자 화면 가시성. 관리자는 undefined → 호출 안 함(전체 노출). */
export function useMyClusterIds(username: string | undefined) {
  return useQuery({
    queryKey: ["core-my-clusters", username],
    enabled: !!username,
    queryFn: async (): Promise<Set<string>> =>
      new Set((await coreFetch<{ clusterIds: string[] }>(`/my-cluster-ids`)).clusterIds),
    staleTime: 60 * 1000, retry: 0,
  });
}

/** 클러스터에 접근 가능한 프로젝트 목록(관리자 상세 모달). */
export function useClusterAccess(clusterId: string | undefined) {
  return useQuery({
    queryKey: ["core-cluster-access", clusterId],
    enabled: !!clusterId,
    queryFn: async (): Promise<ClusterAccess[]> =>
      (await coreFetch<{ items: ClusterAccess[] }>(`/clusters/${clusterId}/access`)).items,
    staleTime: 30 * 1000, retry: 0,
  });
}

export function useGrantClusterAccess(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { projectId: string; namespace?: string | null }) =>
      coreFetch(`/clusters/${clusterId}/access`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["core-cluster-access", clusterId] });
      qc.invalidateQueries({ queryKey: ["core-my-clusters"] });
    },
  });
}

export function useRevokeClusterAccess(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ projectId, namespace }: { projectId: string; namespace?: string | null }) => {
      const q = new URLSearchParams({ projectId });
      if (namespace) q.set("namespace", namespace);
      return coreFetch<void>(`/clusters/${clusterId}/access?${q.toString()}`, { method: "DELETE" });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["core-cluster-access", clusterId] });
      qc.invalidateQueries({ queryKey: ["core-my-clusters"] });
    },
  });
}

/** 고아 매핑 정리(관리자) — 실재 클러스터 ID 목록을 보내 없는 cluster_id 행을 purge.
 *  클러스터 삭제 완료가 비동기라, 관리자 클러스터 화면 로드 시 sweep 으로 정리한다(자가 치유). */
export function useReconcileClusterAccess() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (existingClusterIds: string[]) =>
      coreFetch<{ purged: number }>(`/clusters/access/reconcile`, {
        method: "POST", body: JSON.stringify({ existingClusterIds }),
      }),
    onSuccess: (r) => {
      if (r.purged > 0) {
        qc.invalidateQueries({ queryKey: ["core-my-clusters"] });
        qc.invalidateQueries({ queryKey: ["core-cluster-access"] });
      }
    },
  });
}

/** 프로젝트별 포털 멤버 수(iam_project_member) — 프로젝트 목록 '멤버' 컬럼(OpenStack 기준 아님). */
export function useProjectMemberCounts() {
  return useQuery({
    queryKey: ["core-project-member-counts"],
    queryFn: async (): Promise<Record<string, number>> =>
      (await coreFetch<{ counts: Record<string, number> }>(`/projects/member-counts`)).counts,
    staleTime: 60 * 1000, retry: 0,
  });
}
