"use client";
/**
 * 사용자 SSH 공개키(키페어) — MNT-CORE 연동.
 * 사용자 스코프(소유자 = 토큰 사용자, 서버가 정함). 개인키는 서버로 보내지 않는다(공개키만 저장).
 * 훅의 owner 인자는 캐시 구분·호출 여부용(요청에 담지 않음).
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { coreFetch } from "@/lib/menu-api";

export interface SshKey {
  id: string;
  name: string;
  publicKey: string;
  fingerprint: string;
  createdAt: string;
  vmCount: number;
}

/** 내(owner) SSH 공개키 목록. */
export function useSshKeys(owner: string | undefined) {
  return useQuery({
    queryKey: ["core-ssh-keys", owner],
    enabled: !!owner,
    queryFn: async (): Promise<SshKey[]> =>
      (await coreFetch<{ items: SshKey[] }>(`/ssh-keys`)).items,
    staleTime: 30 * 1000,
    retry: 0,
  });
}

/** 공개키 등록(생성/가져오기 공통). */
export function useCreateSshKey(owner: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; publicKey: string }) =>
      coreFetch<SshKey>(`/ssh-keys`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["core-ssh-keys", owner] }),
  });
}

/** 내 키 삭제. */
export function useDeleteSshKey(owner: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      coreFetch<void>(`/ssh-keys/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["core-ssh-keys", owner] }),
  });
}
