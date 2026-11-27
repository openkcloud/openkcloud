import { redirect } from "next/navigation";

// 옛 로그인 주소(즐겨찾기·링크 호환) — 로그인은 Keycloak 으로 바뀌어 /auth/login 으로 넘긴다
export default function LegacyLoginPage() {
  redirect("/auth/login");
}
