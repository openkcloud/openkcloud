import { redirect } from "next/navigation";

// 포털 진입 — 로그인 확인은 /auth/login 한 곳에서(세션 있으면 /dashboard, 없으면 Keycloak 로그인 화면)
export default function RootPage() {
  redirect("/auth/login");
}
