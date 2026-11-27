"use client";
/**
 * MNT-CORE 메뉴 API 연동 — 사이드바(LNB)를 DB 원장(mnu_menu·mnu_role_menu)에서 받는다.
 *
 * 주소 결정(NEXT_PUBLIC_CORE_API):
 *   - 미설정          → http://localhost:8000 (로컬 dev, MNT-CORE 직접 호출 — CORS 허용됨)
 *   - ""(빈 문자열)   → same-origin 상대경로 (게이트웨이가 /api/core 라우팅하는 운영 구성)
 *   - "http://..."    → 고정 주소
 *
 * 실패 시 정적 nav.ts 로 폴백 — 백엔드가 없어도 목업은 기존대로 동작한다.
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  Route,
  KeyRound,
  HardDrive,
  Globe,
  ClipboardList,
  ClipboardCheck,
  Disc,
  Cpu,
  CircleDot,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "@/lib/auth";
import { authFetch, readError } from "@/lib/http";
import type { NavItem, NavSection } from "@/lib/nav";

const RAW = process.env.NEXT_PUBLIC_CORE_API;
export const CORE_BASE = RAW === undefined ? "http://localhost:8000" : RAW;

/** MNT-CORE 호출 공통(토큰 포함) — 실패 시 서버 detail(문자열)을 메시지로 한 ApiError, 연결 실패는 안내 문구로 던진다.
 *  204 는 undefined. */
export async function coreFetch<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    const headers = new Headers(init?.headers);
    if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    res = await authFetch(`${CORE_BASE}/api/core${path}`, { ...init, headers });
  } catch {
    throw new Error("서버(MNT-CORE)에 연결할 수 없습니다");
  }
  if (!res.ok) throw await readError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// DB(icon 컬럼, lucide 이름 문자열) → 컴포넌트 매핑
const ICON_MAP: Record<string, LucideIcon> = {
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
  Route,
  KeyRound,
  HardDrive,
  Globe,
  ClipboardList,
  ClipboardCheck,
  Disc,
  Cpu,
};

// DB에 없는 프론트 전용 활성판정 보강 — 한 메뉴가 다른 경로의 화면도 대표할 때
const MATCH_RULES: Record<string, string[]> = {
  "/ai-development/pipelines": ["/ai-development/experiments"], // 학습 = 파이프라인 + 실험(HPO)
};

interface ApiMenuNode {
  code: string;
  label: string;
  path: string | null;
  icon: string | null;
  menu_type: "section" | "menu" | "submenu";
  children?: ApiMenuNode[];
}

interface ApiMenusResponse {
  role: string;
  sections: ApiMenuNode[];
}

function toNavItem(node: ApiMenuNode): NavItem {
  return {
    label: node.label,
    href: node.path ?? "#",
    icon: (node.icon && ICON_MAP[node.icon]) || CircleDot,
    ...(node.path && MATCH_RULES[node.path] ? { match: MATCH_RULES[node.path] } : {}),
    ...(node.children?.length ? { children: node.children.map(toNavItem) } : {}),
  };
}

function toSections(data: ApiMenusResponse): NavSection[] {
  return data.sections
    .filter((s) => s.menu_type === "section")
    .map((s) => ({ label: s.label, items: (s.children ?? []).map(toNavItem) }));
}

/** 내 역할의 메뉴(역할은 서버가 토큰으로 판단) — 성공 시 DB 메뉴, 실패 시 undefined(호출측이 정적 nav 폴백).
 *  role 은 캐시 구분·로그인 대기용. */
export function useMenusFromCore(role: Role | undefined) {
  return useQuery({
    queryKey: ["core-menus", role],
    queryFn: async (): Promise<NavSection[]> => {
      const res = await authFetch(`${CORE_BASE}/api/core/menus`);
      if (!res.ok) throw new Error(`menus API ${res.status}`);
      return toSections((await res.json()) as ApiMenusResponse);
    },
    enabled: !!role,
    staleTime: 5 * 60 * 1000, // 메뉴는 자주 안 바뀜
    retry: 1,
  });
}

// ── 메뉴 관리(관리자, /settings) — mnu_menu 원장 CRUD ──
export interface AdminMenu {
  id: number;
  parent_id: number | null;
  menu_type: "section" | "menu" | "submenu";
  code: string;
  label: string;
  path: string | null;
  icon: string | null;
  sort_order: number;
  is_active: boolean;
  roles: string[]; // ["admin","user"]
}
export interface AdminMenuInput {
  parent_id?: number | null;
  menu_type?: string;
  code?: string;
  label?: string;
  path?: string | null;
  icon?: string | null;
  is_active?: boolean;
  sort_order?: number;
  roles?: string[];
}
const ADMIN_MENUS_KEY = ["core-menus-all"] as const;

export function useAllMenus() {
  return useQuery({
    queryKey: ADMIN_MENUS_KEY,
    queryFn: () => coreFetch<{ items: AdminMenu[] }>("/menus/all").then((d) => d.items),
    staleTime: 30 * 1000,
  });
}
function useMenuMutation<V>(fn: (v: V) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ADMIN_MENUS_KEY });
      qc.invalidateQueries({ queryKey: ["core-menus"] }); // 사이드바도 갱신
    },
  });
}
export function useCreateMenu() {
  return useMenuMutation((body: AdminMenuInput) =>
    coreFetch<{ id: number }>("/menus", { method: "POST", body: JSON.stringify(body) }));
}
export function useUpdateMenu() {
  return useMenuMutation(({ id, body }: { id: number; body: AdminMenuInput }) =>
    coreFetch<{ id: number }>(`/menus/${id}`, { method: "PATCH", body: JSON.stringify(body) }));
}
export function useDeleteMenu() {
  return useMenuMutation((id: number) => coreFetch(`/menus/${id}`, { method: "DELETE" }));
}
export function useReorderMenus() {
  return useMenuMutation((items: { id: number; sort_order: number }[]) =>
    coreFetch(`/menus/reorder`, { method: "POST", body: JSON.stringify(items) }));
}
