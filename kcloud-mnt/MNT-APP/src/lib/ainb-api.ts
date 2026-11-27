"use client";
/**
 * MNT-AINB 연동 — AI 노트북(Kubeflow) 자원은 전부 MNT-AINB 모듈을 통해 가져온다.
 *
 * 주소(NEXT_PUBLIC_AINB_API):
 *   - 미설정·""(빈값) → same-origin 상대경로 (/api/ainb) — 게이트웨이 운영 기본값
 *   - "http://..."    → 고정 주소 (로컬 dev 예: http://localhost:8300 — MNT-AINB CORS 에 포털 주소 등록 필요)
 *
 * 목업 폴백 없음 — MSW 에 /api/ainb 핸들러가 없어 요청은 그대로 백엔드로 간다.
 * IDE 프록시(/notebook/*)가 세션 쿠키(okc_session)를 쓰므로 모든 호출에 credentials: "include".
 */
import { useQuery } from "@tanstack/react-query";
import { accessToken } from "@/lib/keycloak";

const RAW = process.env.NEXT_PUBLIC_AINB_API;
export const AINB_BASE = RAW ?? "";

/**
 * MNT-AINB 호출 — path 는 /api/ainb 뒤 경로(예: "/notebooks").
 * timeoutMs 를 주면 응답이 없을 때 그 시간에 끊는다. 백엔드가 바쁘면(동시 처리 한도 소진) 요청이
 * 무한정 줄을 서고 화면은 아무 안내 없이 계속 도는 것처럼 보이므로, 생성·삭제처럼 사용자가 결과를
 * 기다리는 호출에 쓴다.
 */
export async function ainbFetch(path: string, init: RequestInit = {}, timeoutMs?: number): Promise<Response> {
  try {
    return await fetch(`${AINB_BASE}/api/ainb${path}`, {
      credentials: "include",
      ...init,
      ...(timeoutMs ? { signal: AbortSignal.timeout(timeoutMs) } : {}),
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "TimeoutError") {
      throw new Error(
        `서버가 응답하지 않습니다 (${(timeoutMs ?? 0) / 1000}초 초과). ` +
          `잠시 후 다시 시도하거나, 열어둔 다른 탭을 닫고 시도해 주세요.`,
      );
    }
    if (e instanceof TypeError) throw new Error("AI 노트북 서버(MNT-AINB)에 연결할 수 없습니다");
    throw e;
  }
}

/** GET → JSON (react-query 조회용). 실패하면 상태 코드를 메시지로 던진다. */
export async function ainbJson<T>(path: string): Promise<T> {
  const res = await ainbFetch(path);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

// ────── 목록 조회 — 상태가 계속 바뀌므로 주기적으로 다시 받는다 (화면이 응답 형태로 캐스팅해 쓴다) ──────
export function useNotebooks() {
  return useQuery({ queryKey: ["kf", "notebooks"], queryFn: () => ainbJson("/notebooks"), refetchInterval: 5000 });
}
/** 워크스페이스(Kubeflow Profile) — projectId·projectName 으로 OpenStack 프로젝트와 1:1 연결 */
export function useProfiles() {
  return useQuery({ queryKey: ["kf", "profiles"], queryFn: () => ainbJson("/profiles"), refetchInterval: 5000 });
}
export function usePipelines() {
  return useQuery({ queryKey: ["kf", "pipelines"], queryFn: () => ainbJson("/pipelines"), refetchInterval: 10000 });
}
export function useExperiments() {
  return useQuery({ queryKey: ["kf", "experiments"], queryFn: () => ainbJson("/experiments"), refetchInterval: 5000 });
}
export function useServing() {
  return useQuery({ queryKey: ["kf", "serving"], queryFn: () => ainbJson("/serving"), refetchInterval: 5000 });
}

/** AI 노트북이 쓰는 서비스 클러스터 — mode "paas" 면 여러 클러스터(워크스페이스 신청 때 클러스터를 고른다), "single" 이면 하나 */
export interface AinbClusters {
  mode: "single" | "paas";
  items: { id: string; name: string; fetchedAt: string }[];
}
export function useAinbClusters() {
  return useQuery({ queryKey: ["kf", "clusters"], queryFn: () => ainbJson<AinbClusters>("/clusters"), staleTime: 60_000 });
}

/**
 * 포털 로그인(Keycloak) 성공 뒤 MNT-AINB 세션(HttpOnly 쿠키 okc_session)을 발급받는다.
 * Keycloak 액세스 토큰을 Bearer 로 넘기면 MNT-AINB 가 MNT-CORE /auth/me 로 신원·역할을 확인한다.
 * IDE 프록시가 이 쿠키로 인증하므로, 실패하면 IDE 접속만 제한된다 — 포털 로그인은 막지 않고 기록만 남긴다.
 */
export async function ainbLogin(): Promise<void> {
  try {
    const token = await accessToken();
    if (!token) return;
    const res = await ainbFetch("/auth/login", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) console.warn(`[MNT-AINB] 로그인 실패 (${res.status}) — AI 노트북 개발환경(IDE) 접속이 제한됩니다`);
  } catch (e) {
    console.warn("[MNT-AINB] 로그인 요청 실패 — AI 노트북 개발환경(IDE) 접속이 제한됩니다", e);
  }
}

/** 포털 로그아웃 시 MNT-AINB 세션도 지운다 (결과는 기다리지 않는다). */
export function ainbLogout(): void {
  ainbFetch("/auth/logout", { method: "POST" }).catch(() => {});
}

/** MNT-AINB 세션이 살아 있는지 — IDE 프록시 접속 전에 확인한다. */
export async function ainbSessionOk(): Promise<boolean> {
  try {
    return (await ainbFetch("/auth/me")).ok;
  } catch {
    return false;
  }
}
