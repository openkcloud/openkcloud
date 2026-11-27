/* Keycloak(OIDC Authorization Code + PKCE) — 브라우저 로그인. 토큰은 메모리(keycloak-js)에만 둔다.
 *
 * 주소(NEXT_PUBLIC_KEYCLOAK_URL) — API 주소(NEXT_PUBLIC_CORE_API 등)와 같은 규칙:
 *   - 미설정        → http://keycloak.dev.okestro:8080 (로컬 dev, 개발 Keycloak 직접)
 *   - ""(빈값)      → 같은 출처 /auth (게이트웨이: portal.kcloud.etri/auth/realms/… → Keycloak)
 *   - "http://..."  → 고정 주소
 * PKCE 는 보안 컨텍스트(HTTPS 또는 localhost)에서만 동작한다 — 다른 PC 에서 http://IP:3000 으로는 로그인 불가.
 */
import Keycloak from "keycloak-js";

const RAW = process.env.NEXT_PUBLIC_KEYCLOAK_URL;
export const KEYCLOAK_REALM = "openkcloud_realm";
export const KEYCLOAK_CLIENT_ID = "mnt-app-client";

function keycloakUrl(): string {
  if (RAW === undefined) return "http://keycloak.dev.okestro:8080";
  if (RAW === "") return `${window.location.origin}/auth`;
  return RAW;
}

let kc: Keycloak | null = null;
let initPromise: Promise<boolean> | null = null;

export function getKeycloak(): Keycloak {
  kc ??= new Keycloak({ url: keycloakUrl(), realm: KEYCLOAK_REALM, clientId: KEYCLOAK_CLIENT_ID });
  return kc;
}

/** 페이지당 1회 — 로그인 세션 확인(조용히). Keycloak 에서 돌아온 경우(#code=…)는 토큰 교환까지 한다. */
export function initKeycloak(): Promise<boolean> {
  initPromise ??= getKeycloak().init({
    onLoad: "check-sso",
    pkceMethod: "S256",
    checkLoginIframe: false, // 세션 변화는 MNT-CORE /auth/session 으로 확인(관리자 강제 종료까지 잡으려고)
    silentCheckSsoRedirectUri: `${window.location.origin}/silent-check-sso.html`,
    silentCheckSsoFallback: true, // 제3자 쿠키가 막히면(로컬 dev) Keycloak 을 잠깐 다녀오는 방식으로
  });
  return initPromise;
}

/** API 호출용 토큰 — 30초 안에 만료되면 먼저 갱신한다. 로그인 전·갱신 실패면 undefined(호출부가 401 처리). */
export async function accessToken(): Promise<string | undefined> {
  const k = getKeycloak();
  if (!k.authenticated) return undefined;
  try {
    await k.updateToken(30);
  } catch {
    return k.token; // 갱신 실패 — 서버가 만료·종료 원인을 알려 준다
  }
  return k.token;
}

/** Keycloak 로그인 화면으로 — 로그인 후 /auth/login 으로 돌아와 next 로 간다. */
export function loginRedirect(next?: string): Promise<void> {
  const q = next ? `?next=${encodeURIComponent(next)}` : "";
  return getKeycloak().login({ redirectUri: `${window.location.origin}/auth/login/${q}` });
}

// 토큰을 지운 뒤에도 Keycloak 로그아웃에 쓰는 ID 토큰(메모리만) — 없으면 Keycloak 이 "로그아웃 하시겠습니까?" 확인 화면을 띄운다
let idTokenHint: string | undefined;

/** 메모리의 토큰을 지운다(세션 종료). Keycloak 로그아웃용 ID 토큰만 남긴다. */
export function clearTokens() {
  const k = getKeycloak();
  idTokenHint = k.idToken ?? idTokenHint;
  k.clearToken();
}

/** 이 브라우저의 Keycloak 로그인 세션·쿠키를 끝내고 redirectUri 로 — 확인 화면 없이 바로 돌아온다. */
export function logoutRedirect(redirectUri: string): Promise<void> {
  const k = getKeycloak();
  k.idToken ??= idTokenHint;
  return k.logout({ redirectUri });
}

/** 이 브라우저의 Keycloak 세션을 페이지 이동 없이 끝낸다(갱신 토큰 로그아웃 — Keycloak 에 LOGOUT 기록). 끝났으면 true.
 *  갱신 토큰이 없으면(Keycloak 이 갱신을 거절해 keycloak-js 가 지움) 끝낼 세션이 없는 것으로 본다.
 *  Keycloak 쿠키는 지우지 못한다 — 남은 쿠키는 다음 로그인 때 Keycloak 이 세션이 없음을 보고 지운다. */
export async function revokeSession(): Promise<boolean> {
  const refreshToken = getKeycloak().refreshToken;
  if (!refreshToken) return true;
  try {
    const res = await fetch(`${keycloakUrl()}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/logout`, {
      method: "POST",
      body: new URLSearchParams({ client_id: KEYCLOAK_CLIENT_ID, refresh_token: refreshToken }),
    });
    return res.ok;
  } catch {
    return false;
  }
}
