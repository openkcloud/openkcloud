"use client";
import * as React from "react";
import { useRouter } from "next/navigation";
import { ensureAuth, useAuth } from "@/lib/auth";
import { loginRedirect } from "@/lib/keycloak";
import { AuthLoading, SessionEnded } from "@/components/session-ended";

/** 같은 사이트 경로만 — 외부 주소로 보내는 것(open redirect) 방지 */
function safeNext(raw: string | null): string {
  return raw && raw.startsWith("/") && !raw.startsWith("//") && !raw.startsWith("/auth/") ? raw : "/dashboard";
}

/** 로그인 중간 지점 — 세션이 있으면 next(없으면 /dashboard)로, 없으면 Keycloak 로그인 화면으로.
 *  Keycloak 에서 로그인한 뒤에도 여기로 돌아온다(아이디·비밀번호는 Keycloak 화면에서 입력). */
export default function AuthLoginPage() {
  const router = useRouter();
  const status = useAuth((s) => s.status);

  React.useEffect(() => {
    void ensureAuth();
  }, []);

  React.useEffect(() => {
    const next = safeNext(new URLSearchParams(window.location.search).get("next"));
    if (status === "authenticated") router.replace(next);
    else if (status === "unauthenticated") void loginRedirect(next === "/dashboard" ? undefined : next);
  }, [status, router]);

  if (status === "ended") return <SessionEnded />;
  return <AuthLoading />;
}
