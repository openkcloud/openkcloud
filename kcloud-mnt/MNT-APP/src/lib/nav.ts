import {
  LayoutDashboard,
  Server,
  Boxes,
  BrainCircuit,
  ShoppingBag,
  Activity,
  ShieldCheck,
  Settings,
  Layers,
  GitBranch,
  ScrollText,
  Bell,
  Sparkles,
  Database,
  Network,
  KeyRound,
  HardDrive,
  Globe,
  ClipboardList,
  ClipboardCheck,
  Disc,
  Cpu,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "@/lib/auth";

export interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
  roles?: Role[];
  children?: NavItem[];
  badge?: string;
  /** 이 경로들에서도 활성으로 표시 (한 메뉴 아래 묶인 서브탭 경로) */
  match?: string[];
}

export interface NavSection {
  label: string;
  items: NavItem[];
}

// ─────────────────────────────────────────────────────────────
// 섹션 정의 (재사용 가능한 단위) — 메뉴 구조는 feature/mockup-v2(v2.1)에 정렬
// ─────────────────────────────────────────────────────────────

const SECTION_OVERVIEW: NavSection = {
  label: "개요",
  items: [
    {
      label: "통합 대시보드",
      href: "/dashboard",
      icon: LayoutDashboard,
    },
  ],
};

const SECTION_OBSERVABILITY: NavSection = {
  label: "옵저버빌리티",
  items: [
    { label: "인프라 모니터링", href: "/monitoring", icon: Activity },
    { label: "AI 모델 분석", href: "/monitoring/insights", icon: Sparkles },
    { label: "분산 트레이스", href: "/monitoring/traces", icon: GitBranch },
    { label: "로그·이벤트", href: "/monitoring/logs", icon: ScrollText },
    { label: "알람", href: "/monitoring/alerts", icon: Bell },
  ],
};

// 클라우드 자원 — 통합 섹션. 관리자=IaaS/PaaS 전체, 사용자=부분집합(항목별 roles).
// 역할 차이는 "메뉴 위치 이동"이 아니라 "노출(roles)"로만 표현한다.
//   · 관리자 전용 자원(인프라 공급 성격): 라우터·이미지/플레이버
//   · 생성 권한 차이(사용자는 조회·선택)는 각 화면 안의 액션 수준에서 처리
const SECTION_CLOUD: NavSection = {
  label: "클라우드 자원",
  items: [
    { label: "자원 요청 관리", href: "/ticket/requests", icon: ClipboardList, roles: ["user"] as Role[] },
    { label: "자원 신청 관리", href: "/ticket/requests", icon: ClipboardCheck, roles: ["admin"] as Role[] },
    {
      label: "IaaS (OpenStack)",
      href: "/iaas",
      icon: Server,
      children: [
        { label: "프로젝트", href: "/iaas/projects", icon: Server },
        { label: "가상서버", href: "/iaas/instances", icon: Server },
        // 네트워크·라우터·보안그룹은 /iaas/networks 안의 탭으로 통합(라우터·SG는 네트워크 하위가 아닌 프로젝트 자원이라 한 영역에서 관리)
        { label: "네트워크", href: "/iaas/networks", icon: Network, match: ["/iaas/routers", "/iaas/security-groups"] },
        { label: "키페어", href: "/iaas/keypairs", icon: KeyRound },
        { label: "볼륨·스토리지", href: "/iaas/volumes", icon: HardDrive },
        { label: "이미지", href: "/iaas/images", icon: Disc, roles: ["admin"] as Role[] },
        { label: "플레이버", href: "/iaas/flavors", icon: Cpu, roles: ["admin"] as Role[] },
        { label: "디바이스 프로파일", href: "/iaas/device-profiles", icon: Cpu, roles: ["admin"] as Role[] },
        { label: "가속기 장치 현황", href: "/iaas/accelerator-devices", icon: Cpu, roles: ["admin"] as Role[] },
      ],
    },
    {
      label: "PaaS (Kubernetes)",
      href: "/paas",
      icon: Boxes,
      children: [
        { label: "클러스터", href: "/paas/clusters", icon: Boxes },
        { label: "워크로드", href: "/paas/workloads", icon: Boxes },
        { label: "서비스·인그레스", href: "/paas/services", icon: Globe },
        { label: "스토리지", href: "/paas/storage", icon: Database },
        { label: "설정 관리", href: "/paas/config", icon: Boxes },
        { label: "클러스터 템플릿", href: "/paas/templates", icon: Boxes, roles: ["admin"] as Role[] },
      ],
    },
  ],
};

const SECTION_MARKETPLACE: NavSection = {
  label: "서비스 마켓플레이스",
  items: [
    {
      label: "서비스 마켓플레이스",
      href: "/catalog",
      icon: ShoppingBag,
    },
  ],
};

// AI 노트북 — 사용 순서대로: 워크스페이스 → 개발환경 → 학습 → 모델 배포 → 성능 분석.
// 학습은 파이프라인·실험(HPO)을 서브탭으로 묶는다.
const AI = "/ai-development";
const SECTION_AI_DEV: NavSection = {
  label: "AI 개발환경",
  items: [
    {
      label: "AI 노트북",
      href: AI,
      icon: BrainCircuit,
      children: [
        { label: "워크스페이스", href: `${AI}/profiles`, icon: BrainCircuit },
        { label: "개발환경", href: `${AI}/notebooks`, icon: BrainCircuit },
        { label: "학습", href: `${AI}/pipelines`, icon: BrainCircuit, match: [`${AI}/experiments`] },
        { label: "모델 배포", href: `${AI}/serving`, icon: BrainCircuit },
        { label: "성능 분석", href: `${AI}/model-insights`, icon: BrainCircuit },
      ],
    },
  ],
};

const SECTION_AI_INFRA: NavSection = {
  label: "AI 인프라",
  items: [
    { label: "리소스 현황", href: "/resource-map", icon: Database },
    { label: "인프라 관리", href: "/infrastructure/servers", icon: Server },
    { label: "기술인벤토리 관리", href: "/topology", icon: Layers },
  ],
};

const SECTION_OPS: NavSection = {
  label: "운영",
  items: [
    {
      label: "IAM·접근제어",
      href: "/iam",
      icon: ShieldCheck,
      children: [
        { label: "사용자", href: "/iam/users", icon: ShieldCheck },
        { label: "역할·권한", href: "/iam/roles", icon: ShieldCheck },
        { label: "API Key", href: "/iam/api-keys", icon: ShieldCheck },
        { label: "감사 로그", href: "/iam/audit", icon: ShieldCheck },
      ],
    },
    {
      label: "시스템 설정",
      href: "/settings",
      icon: Settings,
      children: [
        { label: "메뉴 관리", href: "/settings/menus", icon: Settings, roles: ["admin"] as Role[] },
      ],
    },
  ],
};

// ─────────────────────────────────────────────────────────────
// 역할별 섹션 순서 (feature/mockup-v2 v2.1에 정렬)
// ─────────────────────────────────────────────────────────────

// 사용자: 개요 → 클라우드 자원 → 마켓 → AI 개발환경 → 옵저버빌리티 → AI 인프라
const NAV_SECTIONS_USER: NavSection[] = [
  SECTION_OVERVIEW,
  SECTION_CLOUD,
  SECTION_MARKETPLACE,
  SECTION_AI_DEV,
  SECTION_OBSERVABILITY,
  SECTION_AI_INFRA,
];

// 관리자: 개요 → 옵저버빌리티 → 클라우드 자원(전체) → 마켓 → AI 개발환경 → AI 인프라 → 운영
const NAV_SECTIONS_ADMIN: NavSection[] = [
  SECTION_OVERVIEW,
  SECTION_OBSERVABILITY,
  SECTION_CLOUD,
  SECTION_MARKETPLACE,
  SECTION_AI_DEV,
  SECTION_AI_INFRA,
  SECTION_OPS,
];

// 기본 (역할 미지정 시): 관리자 뷰
export const NAV_SECTIONS: NavSection[] = NAV_SECTIONS_ADMIN;

// ─────────────────────────────────────────────────────────────
// 역할별 사이드바 조회
// ─────────────────────────────────────────────────────────────

function keep(item: NavItem, role: Role): boolean {
  if (item.roles && !item.roles.includes(role)) return false;
  return true;
}

export function getNavByRole(role: Role): NavSection[] {
  const sections = role === "admin" ? NAV_SECTIONS_ADMIN : NAV_SECTIONS_USER;
  return sections
    .map((s) => ({
      ...s,
      items: s.items
        .filter((i) => keep(i, role))
        .map((i) => (i.children ? { ...i, children: i.children.filter((c) => keep(c, role)) } : i)),
    }))
    .filter((s) => s.items.length > 0);
}

// backward-compat (기존 filterNavByRole 사용처 존재 시)
export function filterNavByRole(_sections: NavSection[], role: Role): NavSection[] {
  return getNavByRole(role);
}
