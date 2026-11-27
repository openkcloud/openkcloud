"use client";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { ProvisionStepsResp } from "@/lib/types";

async function get<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { "Content-Type": "application/json" } });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

async function post<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

export const api = {
  dashboard: {
    summary: () =>
      useQuery({ queryKey: ["dashboard", "summary"], queryFn: () => get("/api/dashboard/summary") }),
    activities: () =>
      useQuery({ queryKey: ["dashboard", "activities"], queryFn: () => get("/api/dashboard/activities") }),
    alerts: () =>
      useQuery({ queryKey: ["dashboard", "alerts"], queryFn: () => get("/api/dashboard/alerts") }),
  },
  accelerators: {
    list: (q?: Record<string, string>) => {
      const search = q ? "?" + new URLSearchParams(q).toString() : "";
      return useQuery({
        queryKey: ["accelerators", q],
        queryFn: () => get(`/api/accelerators${search}`),
      });
    },
    topology: () =>
      useQuery({ queryKey: ["accelerators", "topology"], queryFn: () => get("/api/accelerators/topology") }),
  },
  // 자원 신청(티켓) — VM·서비스 클러스터 공통
  ticket: {
    // 프로비저닝 진행 단계 — 진행 중이면 폴링
    steps: (id: string | undefined) =>
      useQuery({
        queryKey: ["ticket", "steps", id],
        queryFn: () => get<ProvisionStepsResp>(`/api/ticket/requests/${id}/steps`),
        enabled: !!id,
        refetchInterval: (q) => {
          const st = q.state.data?.status;
          return st === "approved" || st === "provisioning" ? 1000 : false;
        },
      }),
    retry: () => {
      const qc = useQueryClient();
      return useMutation({
        mutationFn: (id: string) => post(`/api/ticket/requests/${id}/retry`, {}),
        onSuccess: () => qc.invalidateQueries({ queryKey: ["ticket"] }),
      });
    },
    createRequest: () => {
      const qc = useQueryClient();
      return useMutation({
        mutationFn: (payload: Record<string, unknown>) => post(`/api/ticket/requests`, payload),
        onSuccess: () => qc.invalidateQueries({ queryKey: ["ticket", "requests"] }),
      });
    },
  },
  iaas: {
    pools: () => useQuery({ queryKey: ["iaas", "pools"], queryFn: () => get("/api/iaas/pools") }),
    projects: () => useQuery({ queryKey: ["iaas", "projects"], queryFn: () => get("/api/iaas/projects") }),
    instances: (q?: Record<string, string>) => {
      const search = q ? "?" + new URLSearchParams(q).toString() : "";
      return useQuery({
        queryKey: ["iaas", "instances", q],
        queryFn: () => get(`/api/iaas/instances${search}`),
      });
    },
    volumes: () => useQuery({ queryKey: ["iaas", "volumes"], queryFn: () => get("/api/iaas/volumes") }),
    images: () => useQuery({ queryKey: ["iaas", "images"], queryFn: () => get("/api/iaas/images") }),
    networks: () => useQuery({ queryKey: ["iaas", "networks"], queryFn: () => get("/api/iaas/networks") }),
    centers: () => useQuery({ queryKey: ["iaas", "centers"], queryFn: () => get("/api/iaas/centers") }),
    zones: () => useQuery({ queryKey: ["iaas", "zones"], queryFn: () => get("/api/iaas/zones") }),
  },
  paas: {
    clusters: () => useQuery({ queryKey: ["paas", "clusters"], queryFn: () => get("/api/paas/clusters") }),
    workloads: (q?: Record<string, string>) => {
      const search = q ? "?" + new URLSearchParams(q).toString() : "";
      return useQuery({
        queryKey: ["paas", "workloads", q],
        queryFn: () => get(`/api/paas/workloads${search}`),
      });
    },
    services: () => useQuery({ queryKey: ["paas", "services"], queryFn: () => get("/api/paas/services") }),
  },
  kubeflow: {
    notebooks: () => useQuery({ queryKey: ["kf", "notebooks"], queryFn: () => get("/api/kubeflow/notebooks") }),
    pipelines: () => useQuery({ queryKey: ["kf", "pipelines"], queryFn: () => get("/api/kubeflow/pipelines") }),
    experiments: () => useQuery({ queryKey: ["kf", "experiments"], queryFn: () => get("/api/kubeflow/experiments") }),
    serving: () => useQuery({ queryKey: ["kf", "serving"], queryFn: () => get("/api/kubeflow/serving") }),
  },
  resourceMap: {
    graph: () => useQuery({ queryKey: ["rm", "graph"], queryFn: () => get("/api/resource-map") }),
    partitions: () => useQuery({ queryKey: ["rm", "partitions"], queryFn: () => get("/api/partitions") }),
    mgmtClusters: () => useQuery({ queryKey: ["rm", "mgmt"], queryFn: () => get("/api/clusters/management") }),
    serviceClusters: () => useQuery({ queryKey: ["rm", "svc"], queryFn: () => get("/api/clusters/service") }),
    passthroughs: () => useQuery({ queryKey: ["rm", "pt"], queryFn: () => get("/api/passthroughs") }),
  },
  infra: {
    servers: () => useQuery({ queryKey: ["infra", "servers"], queryFn: () => get("/api/infrastructure/servers") }),
    server: (id: string) => useQuery({ queryKey: ["infra", "server", id], queryFn: () => get(`/api/infrastructure/servers/${id}`) }),
    vmDetail: (id: string) => useQuery({ queryKey: ["iaas", "instance", id], queryFn: () => get(`/api/iaas/instances/${id}`) }),
    acceleratorDetail: (id: string) => useQuery({ queryKey: ["acc", "detail", id], queryFn: () => get(`/api/accelerators/${id}/detail`) }),
    workloadDetail: (id: string) => useQuery({ queryKey: ["wl", "detail", id], queryFn: () => get(`/api/paas/workloads/${id}`) }),
    topology: () => useQuery({ queryKey: ["topology"], queryFn: () => get("/api/topology") }),
  },
};
