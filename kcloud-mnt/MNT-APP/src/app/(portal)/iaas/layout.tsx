"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { Download } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth";

// 끝 슬래시(trailingSlash:true) 정규화
function normalizePath(p: string) {
  return p.length > 1 && p.endsWith("/") ? p.slice(0, -1) : p;
}

// 자체 PageHeader를 렌더링하는 페이지 — layout 헤더는 생략(중복 방지)
const SELF_HEADER_ROUTES = new Set([
  "/iaas/security-groups",
  "/iaas/keypairs",
  "/iaas/routers",
]);

// 자체 헤더가 없는 페이지 — layout이 메뉴명으로 헤더 렌더링
const PAGE_META: Record<string, { title: string; description: string; userDescription?: string }> = {
  "/iaas/projects": {
    title: "프로젝트",
    description: "프로젝트별 할당 자원과 멤버를 관리합니다.",
    userDescription: "내가 속한 프로젝트의 할당 자원과 멤버를 확인합니다.",
  },
  "/iaas/instances": { title: "가상서버", description: "실행 중인 가상서버의 목록과 상태를 확인합니다." },
  "/iaas/networks": { title: "네트워크", description: "네트워크 구성을 확인합니다." },
  "/iaas/volumes": { title: "볼륨·스토리지", description: "Cinder 블록 볼륨과 스토리지 할당을 관리합니다." },
  "/iaas/images": { title: "이미지", description: "Glance 이미지 카탈로그를 관리합니다." },
  "/iaas/flavors": { title: "플레이버", description: "Nova 플레이버(vCPU·RAM·Disk 규격)를 관리합니다." },
  "/iaas/providers": { title: "공급자 관리", description: "연동된 클라우드 공급자·리전을 관리합니다." },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";

  // 자체 헤더가 없는 페이지에 한해 layout이 메뉴명 헤더를 렌더링
  const here = normalizePath(pathname);
  const meta = SELF_HEADER_ROUTES.has(here) ? null : PAGE_META[here];

  return (
    <div className="space-y-6">
      {meta && (
        <PageHeader
          title={meta.title}
          description={!isAdmin && meta.userDescription ? meta.userDescription : meta.description}
          actions={
            isAdmin ? (
              <Button variant="outline" size="sm">
                <Download className="size-4" /> 엑셀 내보내기
              </Button>
            ) : null
          }
        />
      )}
      {children}
    </div>
  );
}
