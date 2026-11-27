"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { ensureAuth, useAuth } from "@/lib/auth";
import { AuthLoading, SessionBlur, SessionEnded } from "@/components/session-ended";

/* Full-screen 라우트 그룹 — 사이드바·헤더 없이 풀스크린 사용.
 * IDE / 라이브 토폴로지 등 몰입형 화면용. 인증 가드는 유지. */
export default function FullscreenLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const status = useAuth((s) => s.status);
  const user = useAuth((s) => s.user);

  React.useEffect(() => {
    void ensureAuth();
  }, []);

  React.useEffect(() => {
    if (status === "unauthenticated") {
      router.replace(`/auth/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`);
    }
  }, [status, router]);

  const ended = status === "ended";
  if (ended && !user) return <SessionEnded />; // 보던 화면이 없으면(새로고침 직후 등) 빈 배경 위에 안내
  if (!ended && status !== "authenticated") return <AuthLoading dark />;

  return <SessionBlur ended={ended}>{children}</SessionBlur>;
}
