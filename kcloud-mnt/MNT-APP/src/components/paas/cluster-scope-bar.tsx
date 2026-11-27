"use client";
/**
 * 서비스 클러스터 스코프 바 — 워크로드·서비스·스토리지·설정 공용 툴바 조각.
 * K8s 리소스는 "어느 클러스터를 볼지"가 선행되므로, 선택을 전역(persist)으로 공유한다.
 * 레이아웃·폰트는 IaaS 목록 툴바 규약(Card p-4, Select w-44, ml-auto RefreshControls)과 동일.
 */
import * as React from "react";
import type { QueryKey } from "@tanstack/react-query";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useClusters, usePaasCluster, useNamespaces } from "@/lib/paas-api";
import { useAuth } from "@/lib/auth";
import { useMyProjectIds } from "@/lib/member-api";

export function useClusterScope() {
  const clusterId = usePaasCluster((s) => s.clusterId);
  const setClusterId = usePaasCluster((s) => s.setClusterId);
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const myUsername = useAuth((s) => s.user?.username);
  const { data: myProjectIds } = useMyProjectIds(isAdmin ? undefined : myUsername);
  const clusters = useClusters();
  // 선택 가능한 클러스터 = 준비됨(COMPLETE) + 내 프로젝트 멤버십(관리자는 전체)
  const ready = React.useMemo(
    () => (clusters.data ?? []).filter(
      (c) => c.status.endsWith("_COMPLETE") && !!c.apiAddress && (isAdmin || myProjectIds?.has(c.projectId)),
    ),
    [clusters.data, isAdmin, myProjectIds],
  );
  // 선택이 접근 가능 목록에 없으면 보정: 접근 가능한 첫 클러스터로, 하나도 없으면 "" 로 비운다.
  // (persist 된 이전 clusterId 가 남아 접근 권한 없는 클러스터의 리소스가 보이던 문제 차단)
  React.useEffect(() => {
    if (!ready.some((c) => c.id === clusterId)) {
      const next = ready[0]?.id ?? "";
      if (next !== clusterId) setClusterId(next);
    }
  }, [ready, clusterId, setClusterId]);
  return { clusterId, setClusterId, clusters: ready, isLoading: clusters.isLoading };
}

/** 툴바 한 줄: 클러스터 → 네임스페이스 → children(검색 등) → (우측) 새로고침 + trailing(생성 등) */
export function ClusterScopeBar({
  queryKeys, namespace, onNamespaceChange, children, trailing,
}: {
  queryKeys: QueryKey[];
  namespace?: string;
  onNamespaceChange?: (ns: string) => void;
  children?: React.ReactNode;
  trailing?: React.ReactNode;
}) {
  const { clusterId, setClusterId, clusters, isLoading } = useClusterScope();
  const namespaces = useNamespaces(clusterId);
  const noClusters = !isLoading && clusters.length === 0;

  return (
    <div className="flex flex-wrap items-center gap-3">
      <Select value={clusters.some((c) => c.id === clusterId) ? clusterId : undefined} onValueChange={setClusterId} disabled={noClusters}>
        <SelectTrigger className="w-52">
          <SelectValue
            placeholder={isLoading ? "클러스터 불러오는 중…" : noClusters ? "사용 가능한 클러스터 없음" : "서비스 클러스터 선택"}
          />
        </SelectTrigger>
        <SelectContent>
          {clusters.map((c) => (
            <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      {onNamespaceChange && (
        <Select value={namespace ?? "all"} onValueChange={onNamespaceChange}>
          <SelectTrigger className="w-44">
            <SelectValue placeholder="네임스페이스" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">전체 네임스페이스</SelectItem>
            {(namespaces.data ?? []).map((ns) => (
              <SelectItem key={ns} value={ns} className="font-mono text-xs">{ns}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      {children}
      <div className="ml-auto flex items-center gap-2">
        <RefreshControls queryKeys={queryKeys} />
        {trailing}
      </div>
    </div>
  );
}
