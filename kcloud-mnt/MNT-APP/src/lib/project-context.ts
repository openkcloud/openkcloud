/* AI 노트북 프로젝트 컨텍스트 — 워크스페이스(Kubeflow Profile·네임스페이스) = 프로젝트(OpenStack) 1:1.
 * 워크스페이스 목록은 MNT-AINB(/api/ainb/profiles — projectId·projectName 포함),
 * 내가 속한 프로젝트는 MNT-CORE(/api/core/my-projects — IaaS 화면과 같은 기준)에서 받는다.
 * 사용자는 소속 프로젝트에 연결된 워크스페이스만, 관리자는 전체(프로젝트 연결 전 워크스페이스 포함)를 본다.
 * 포털 헤더에는 전역 프로젝트 선택이 없어, AI 노트북 화면 위에서 고른 워크스페이스를 기억해 둔다.
 * 쿼터·사용량은 워크스페이스 ResourceQuota 실측값이다. */
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { useAuth } from "@/lib/auth";
import { useProfiles } from "@/lib/ainb-api";
import { useMyProjectIds } from "@/lib/member-api";
import type { Tenant } from "@/lib/types";

export const ALL = "all";

export interface AIProject {
  /** 워크스페이스 이름 = 네임스페이스 */
  name: string;
  /** 연결된 OpenStack 프로젝트 — 연결 전에 만든 워크스페이스는 null */
  projectId: string | null;
  projectName: string | null;
  description: string;
  tenant: Tenant;
}

type Quota = { used: number; limit: number } | undefined;
interface RealProfile {
  name: string;
  owner?: string;
  members?: number;
  projectId?: string | null;
  projectName?: string | null;
  cpuQuota?: Quota;
  memQuota?: Quota;
  accQuota?: Quota;
  storageQuota?: Quota;
}

function toProject(p: RealProfile): AIProject {
  const q = (x: Quota) => ({ used: x?.used ?? 0, limit: x?.limit ?? 0 });
  const cpu = q(p.cpuQuota), mem = q(p.memQuota), acc = q(p.accQuota), sto = q(p.storageQuota);
  const description = p.owner ? `소유자 ${p.owner}` : "";
  return {
    name: p.name,
    projectId: p.projectId ?? null,
    projectName: p.projectName ?? null,
    description,
    tenant: {
      id: p.projectId ?? p.name,
      name: p.name,
      orgId: "",
      description,
      quotaVcpu: cpu.limit,
      usedVcpu: cpu.used,
      quotaRamGb: mem.limit,
      usedRamGb: mem.used,
      // 스토리지 쿼터는 GB 단위로 오므로 TB로 환산
      quotaDiskTb: +(sto.limit / 1024).toFixed(2),
      usedDiskTb: +(sto.used / 1024).toFixed(2),
      quotaAcc: acc.limit,
      usedAcc: acc.used,
      members: p.members ?? 0,
      createdAt: "",
    },
  };
}

/** 내가 볼 수 있는 워크스페이스 — 사용자는 소속 프로젝트(포털 멤버십)에 연결된 것만, 관리자는 전체 */
export function useAIProjects(): { projects: AIProject[]; loading: boolean } {
  const user = useAuth((s) => s.user);
  const isAdmin = user?.role === "admin";
  const prof = useProfiles() as { data?: { items: RealProfile[] }; isLoading: boolean };
  const mine = useMyProjectIds(isAdmin ? undefined : user?.username);
  const all = (prof.data?.items ?? []).map(toProject);
  const projects = isAdmin ? all : all.filter((p) => !!p.projectId && !!mine.data?.has(p.projectId));
  return { projects, loading: prof.isLoading || (!isAdmin && mine.isLoading) };
}

interface ProjectCtxState {
  /** 사용자가 직접 고른 워크스페이스. 아직 안 골랐으면 null — 관리자는 전체, 사용자는 첫 워크스페이스로 시작한다 */
  current: string | null;
  setCurrent: (p: string) => void;
}

const useProjectCtxStore = create<ProjectCtxState>()(
  persist((set) => ({ current: null, setCurrent: (current) => set({ current }) }), { name: "openkcloud-ai-project" }),
);

export function useProjectContext() {
  const user = useAuth((s) => s.user);
  const stored = useProjectCtxStore((s) => s.current);
  const setCurrent = useProjectCtxStore((s) => s.setCurrent);
  const { projects, loading } = useAIProjects();
  const isAdmin = user?.role === "admin";
  const allowed = projects.map((p) => p.name);
  // 목록이 아직 안 왔으면 저장된 선택을 그대로 둔다(첫 렌더에서 엉뚱한 워크스페이스로 튀지 않게)
  const fallback = isAdmin ? ALL : (allowed[0] ?? ALL);
  let current = stored ?? fallback;
  if (!loading && (current === ALL ? !isAdmin : !allowed.includes(current))) current = fallback;
  const project = projects.find((p) => p.name === current);
  /** 내가 볼 수 있는 네임스페이스인지 (선택과 무관) */
  const canSee = (ns: string) => isAdmin || allowed.includes(ns);
  return {
    current,
    setCurrent,
    isAdmin,
    allowed,
    projects,
    project,
    loading,
    isAll: current === ALL,
    canSee,
    /** 네임스페이스가 현재 선택에 속하는지. 네임스페이스가 없는 자원(KFP 파이프라인 등록본)은 공용이라 모두에게 보인다.
     *  사용자는 소속 워크스페이스가 없어 '전체'로 남아도 남의 워크스페이스 자원은 보지 않는다 */
    match: (ns?: string) => !ns || (canSee(ns) && (current === ALL || ns === current)),
    /** 새 자원(노트북·실험·서비스)을 만들 워크스페이스 후보 — 지금 보고 있는 것 먼저 */
    choices: current === ALL ? allowed : [current, ...allowed.filter((n) => n !== current)],
    label: current === ALL ? "전체 프로젝트" : current,
  };
}
