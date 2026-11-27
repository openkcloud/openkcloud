"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { PageHeader } from "@/components/page-header";
import { useAuth } from "@/lib/auth";

// 끝 슬래시(trailingSlash:true) 정규화
function normalizePath(p: string) {
  return p.length > 1 && p.endsWith("/") ? p.slice(0, -1) : p;
}

// 자체 PageHeader를 렌더링하는 페이지 — layout 헤더는 생략(중복 방지)
const SELF_HEADER_ROUTES = new Set<string>([]);

// 자체 헤더가 없는 페이지 — layout이 메뉴명으로 헤더 렌더링
const PAGE_META: Record<string, { title: string; description: string; userDescription?: string }> = {
  "/paas/clusters": {
    title: "클러스터",
    description: "서비스 클러스터(Magnum)의 상태·노드풀을 확인하고 kubeconfig 를 받습니다.",
    userDescription: "내 프로젝트의 서비스 클러스터를 확인하고 kubeconfig 를 받습니다. 새 클러스터는 자원 요청 메뉴에서 신청합니다.",
  },
  "/paas/workloads": { title: "워크로드", description: "선택한 서비스 클러스터의 워크로드를 확인합니다." },
  "/paas/services": { title: "서비스·인그레스", description: "선택한 서비스 클러스터의 Service·Ingress 를 확인합니다." },
  "/paas/storage": { title: "스토리지", description: "선택한 서비스 클러스터의 PVC·PV·StorageClass 를 확인합니다." },
  "/paas/config": { title: "설정 관리", description: "선택한 서비스 클러스터의 ConfigMap·Secret 을 확인합니다." },
  "/paas/templates": { title: "클러스터 템플릿", description: "클러스터 생성 설계도(Magnum ClusterTemplate)를 관리합니다. 관리자 전용." },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const isAdmin = useAuth((s) => s.user?.role === "admin");

  const here = normalizePath(pathname);
  const meta = SELF_HEADER_ROUTES.has(here) ? null : PAGE_META[here];

  return (
    <div className="space-y-6">
      {/* 동기화·등록 버튼 없음 — 목록은 라이브(폴링), 생성은 자원 요청 메뉴에서 신청 */}
      {meta && (
        <PageHeader
          title={meta.title}
          description={!isAdmin && meta.userDescription ? meta.userDescription : meta.description}
        />
      )}
      {children}
    </div>
  );
}
