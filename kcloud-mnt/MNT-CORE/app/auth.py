"""Keycloak(OIDC) 토큰 검증 · 현재 사용자 · 로그인 세션.

- 토큰: `Authorization: Bearer <access token>` — 서명(JWKS)·iss·aud·exp 를 확인한다.
- 사용자: 토큰 preferred_username ↔ iam_user.username. 역할·상태·소속은 포털 DB 가 기준이다.
  첫 요청 때 토큰 sub 를 iam_user.keycloak_sub 에 저장하고, 이후 다르면 거부한다(같은 아이디로 만든 다른 Keycloak 계정).
- 세션: 토큰 sid(Keycloak 세션 — 같은 브라우저의 탭들은 한 세션) 단위로 iam_user_session 에 기록한다.
  끝난 세션(로그아웃·관리자 강제 종료·다른 곳 로그인)의 토큰은 만료 전이라도 거부한다.
  SESSION_SINGLE_LOGIN 이면 새 세션이 올 때 같은 사용자의 다른 세션을 끝내고 Keycloak 세션도 지운다.

오류는 AuthError → main.py 핸들러가 {"detail": 문구, "code": 코드, ...} 로 응답한다(detail 은 기존 화면이 그대로 표시).
"""
from __future__ import annotations

import json
import logging
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass

import jwt
from fastapi import Depends, Request
from sqlalchemy import text
from sqlalchemy.orm import Session

from .config import settings
from .db import get_db

log = logging.getLogger(__name__)

# 세션 상태 확인·로그아웃은 만료된 토큰도 받는다(서명이 맞고 이 시간 안이면) — 쉬던 탭이 상태를 물을 수 있게
_EXPIRED_OK_SECONDS = 12 * 3600
# Keycloak 요청(공개키·서비스 계정 토큰·세션 삭제)의 연결·응답 대기(초)
_KC_TIMEOUT = 5

SESSION_END_MESSAGES = {  # cmn_code(IAM_SESSION_END_REASON)
    "logout": "로그아웃 되었습니다.",
    "admin": "관리자에 의해 로그아웃되었습니다.",
    "duplicate": "다른곳에서 로그인되었습니다.",
    "idle": "오랫동안 사용하지 않아 로그아웃 되었습니다.",
}
USER_END_REASONS = ("logout", "idle")  # 화면이 직접 끝내는 원인(직접 로그아웃·미사용 시간 초과)


class AuthError(Exception):
    """인증·권한 오류. status·code·detail(화면 문구)과 추가 필드(extra)를 응답에 담는다."""

    def __init__(self, status: int, code: str, detail: str, **extra):
        super().__init__(detail)
        self.status, self.code, self.detail, self.extra = status, code, detail, extra


@dataclass(frozen=True)
class CurrentUser:
    id: int
    username: str
    name: str
    email: str | None
    org: str | None
    roles: tuple[str, ...]
    sid: str
    token: str  # 원문 — CORE 가 MNT-IAAS 를 부를 때 그대로 넘긴다

    @property
    def is_admin(self) -> bool:
        return "admin" in self.roles

    @property
    def role(self) -> str:
        return "admin" if self.is_admin else "user"


# ── 토큰 ──────────────────────────────────────────────────────────

_jwks: jwt.PyJWKClient | None = None


def _jwks_client() -> jwt.PyJWKClient:
    global _jwks
    if _jwks is None:
        if not (settings.keycloak_issuer and settings.keycloak_jwks_url):
            raise AuthError(503, "AUTH_NOT_CONFIGURED", "인증 설정(KEYCLOAK_ISSUER·KEYCLOAK_JWKS_URL)이 없습니다.")
        _jwks = jwt.PyJWKClient(settings.keycloak_jwks_url, cache_keys=True, lifespan=300, timeout=_KC_TIMEOUT)
    return _jwks


def bearer_token(request: Request) -> str:
    scheme, _, token = request.headers.get("authorization", "").partition(" ")
    if scheme.lower() != "bearer" or not token.strip():
        raise AuthError(401, "TOKEN_REQUIRED", "로그인이 필요합니다.")
    return token.strip()


def decode_token(token: str, audience: str, *, allow_expired: bool = False,
                 require: tuple[str, ...] = ("iat", "iss", "aud", "sub")) -> dict:
    """서명·발급자·대상·만료 확인 후 클레임. allow_expired 면 만료 후 일정 시간까지 받는다."""
    try:
        key = _jwks_client().get_signing_key_from_jwt(token).key
        return jwt.decode(
            token, key, algorithms=["RS256"], audience=audience, issuer=settings.keycloak_issuer,
            leeway=_EXPIRED_OK_SECONDS if allow_expired else 0,
            # iat 는 있는지만 본다 — Keycloak 시계가 조금 빠르면 막 발급된 토큰이 '미래 발급'으로 거부된다
            options={"require": list(require), "verify_iat": False},
        )
    except jwt.ExpiredSignatureError:
        raise AuthError(401, "TOKEN_EXPIRED", "로그인이 만료되었습니다. 다시 로그인하세요.")
    except jwt.PyJWKClientConnectionError:
        raise AuthError(503, "AUTH_UNAVAILABLE", "인증 서버(Keycloak)에 연결할 수 없습니다.")
    except jwt.PyJWTError:
        raise AuthError(401, "TOKEN_INVALID", "유효하지 않은 로그인 정보입니다. 다시 로그인하세요.")


# ── 현재 사용자 ───────────────────────────────────────────────────

_USER_SQL = text("""
    SELECT u.id, u.username, u.name, u.email, u.org, u.status, u.keycloak_sub,
           array_remove(array_agg(r.code), NULL) AS roles
      FROM iam_user u
      LEFT JOIN iam_user_role ur ON ur.user_id = u.id
      LEFT JOIN iam_role r ON r.id = ur.role_id
     WHERE u.username = :un
     GROUP BY u.id
""")


def current_user(request: Request, db: Session = Depends(get_db)) -> CurrentUser:
    """FastAPI 의존성 — 토큰을 확인하고 포털 사용자로 바꾼다. 한 요청 안에서는 한 번만 계산된다."""
    token = bearer_token(request)
    claims = decode_token(token, settings.keycloak_audience)
    username = (claims.get("preferred_username") or "").lower()
    sid = claims.get("sid")
    if not username or not sid:
        raise AuthError(401, "TOKEN_INVALID", "유효하지 않은 로그인 정보입니다. 다시 로그인하세요.")

    row = db.execute(_USER_SQL, {"un": username}).mappings().first()
    if row is None:
        raise AuthError(403, "USER_NOT_REGISTERED", "포털에 등록되지 않은 계정입니다. 관리자에게 문의하세요.")
    if row["keycloak_sub"] and row["keycloak_sub"] != claims["sub"]:
        raise AuthError(403, "USER_MISMATCH", "계정 연결 정보가 일치하지 않습니다. 관리자에게 문의하세요.")
    if row["status"] != "active":
        raise AuthError(403, "USER_INACTIVE", "사용할 수 없는 계정입니다. 관리자에게 문의하세요.")
    if not row["roles"]:
        raise AuthError(403, "NO_ROLE", "권한(역할)이 지정되지 않은 계정입니다. 관리자에게 문의하세요.")

    ended = _use_session(db, sid, row["id"])
    if not row["keycloak_sub"]:
        db.execute(text("UPDATE iam_user SET keycloak_sub = :s WHERE id = :id AND keycloak_sub IS NULL"),
                   {"s": claims["sub"], "id": row["id"]})
    db.commit()
    for old in ended:  # 커밋 뒤 — Keycloak 호출이 실패해도 포털 쪽 종료는 이미 반영됨
        end_keycloak_session(old)

    return CurrentUser(id=row["id"], username=row["username"], name=row["name"], email=row["email"],
                       org=row["org"], roles=tuple(row["roles"]), sid=sid, token=token)


def require_admin(user: CurrentUser = Depends(current_user)) -> CurrentUser:
    if not user.is_admin:
        raise AuthError(403, "FORBIDDEN", "관리자만 할 수 있습니다.")
    return user


# ── 세션 ──────────────────────────────────────────────────────────

def _session_ended(reason: str) -> AuthError:
    return AuthError(401, "SESSION_ENDED", SESSION_END_MESSAGES.get(reason, SESSION_END_MESSAGES["logout"]), reason=reason)


def _use_session(db: Session, sid: str, user_id: int) -> list[str]:
    """세션이 끝났으면 거부. 처음 보는 세션이면 기록하고, 동시 로그인 제한이면 이 사용자의 다른 세션을 끝낸다.
    반환: 이번에 끝낸 다른 세션 sid 목록(커밋 후 Keycloak 에서도 지운다)."""
    row = db.execute(text("SELECT ended_at, end_reason FROM iam_user_session WHERE sid = :sid"),
                     {"sid": sid}).mappings().first()
    if row is not None:
        if row["ended_at"] is not None:
            raise _session_ended(row["end_reason"])
        return []
    inserted = db.execute(text("INSERT INTO iam_user_session (sid, user_id) VALUES (:sid, :u) ON CONFLICT (sid) DO NOTHING"),
                          {"sid": sid, "u": user_id}).rowcount
    if inserted:  # 새 로그인(세션) — 마지막 로그인 시각
        db.execute(text("UPDATE iam_user SET last_login_at = now() WHERE id = :u"), {"u": user_id})
    if not (inserted and settings.session_single_login):
        return []
    return list(db.execute(
        text("""UPDATE iam_user_session SET ended_at = now(), end_reason = 'duplicate'
                 WHERE user_id = :u AND sid <> :sid AND ended_at IS NULL RETURNING sid"""),
        {"u": user_id, "sid": sid},
    ).scalars())


def session_status(db: Session, token: str) -> dict:
    """세션이 아직 유효한지 — 만료된 토큰도 받는다(쉬던 탭의 상태 확인용). 처음 보는 세션은 유효로 본다."""
    sid = decode_token(token, settings.keycloak_audience, allow_expired=True).get("sid")
    row = db.execute(text("SELECT ended_at, end_reason FROM iam_user_session WHERE sid = :sid"),
                     {"sid": sid}).mappings().first()
    if row is None or row["ended_at"] is None:
        return {"active": True}
    return {"active": False, "reason": row["end_reason"], "detail": SESSION_END_MESSAGES.get(row["end_reason"])}


def end_session(db: Session, token: str, reason: str) -> None:
    """이 토큰의 세션을 끝낸다(화면의 로그아웃·미사용 시간 초과). 만료된 토큰도 받는다.
    Keycloak 세션은 화면이 이 호출 뒤에 끝낸다(Keycloak 로그아웃) — Keycloak 에 사용자 LOGOUT 이 남게.
    여기서 관리 API 로 먼저 지우면 화면의 Keycloak 로그아웃이 세션을 못 찾아 LOGOUT_ERROR 로 남는다."""
    if reason not in USER_END_REASONS:
        raise AuthError(400, "INVALID_REASON", f"reason 은 {USER_END_REASONS} 중 하나")
    sid = decode_token(token, settings.keycloak_audience, allow_expired=True).get("sid")
    db.execute(text("UPDATE iam_user_session SET ended_at = now(), end_reason = :r WHERE sid = :sid AND ended_at IS NULL"),
               {"sid": sid, "r": reason})
    db.commit()


_BACKCHANNEL_EVENT = "http://schemas.openid.net/event/backchannel-logout"


def end_session_by_logout_token(db: Session, logout_token: str) -> None:
    """Keycloak 백채널 로그아웃 통보(관리자 강제 종료 등) — 해당 sid 를 끝낸다. 이미 끝난 세션은 원인을 바꾸지 않는다.
    CORE 가 아직 못 본 세션이면 sub 로 사용자를 찾아 끝난 세션으로 기록한다(그 토큰이 나중에 와도 거부)."""
    try:
        claims = decode_token(logout_token, settings.keycloak_app_client_id, require=("iat", "iss", "aud"))
    except AuthError as e:
        raise AuthError(400, "LOGOUT_TOKEN_INVALID", e.detail)
    sid = claims.get("sid")
    if not sid or _BACKCHANNEL_EVENT not in (claims.get("events") or {}) or "nonce" in claims:
        raise AuthError(400, "LOGOUT_TOKEN_INVALID", "로그아웃 통보 형식이 올바르지 않습니다.")
    db.execute(
        text("""INSERT INTO iam_user_session (sid, user_id, ended_at, end_reason)
                SELECT :sid, id, now(), 'admin' FROM iam_user WHERE keycloak_sub = :sub
                ON CONFLICT (sid) DO UPDATE SET ended_at = now(), end_reason = 'admin'
                 WHERE iam_user_session.ended_at IS NULL"""),
        {"sid": sid, "sub": claims.get("sub")},
    )
    db.commit()


# ── Keycloak 관리 API (서비스 계정 mnt-core-client) ────────────────

_svc = {"token": "", "exp": 0.0}
_svc_lock = threading.Lock()


def _realm_url() -> str:
    return (settings.keycloak_internal_url or settings.keycloak_issuer).rstrip("/")


def _service_token() -> str:
    with _svc_lock:
        if _svc["token"] and _svc["exp"] - 30 > time.time():
            return _svc["token"]
        data = urllib.parse.urlencode({"grant_type": "client_credentials", "client_id": settings.keycloak_client_id,
                                       "client_secret": settings.keycloak_client_secret}).encode()
        with urllib.request.urlopen(_realm_url() + "/protocol/openid-connect/token", data, timeout=_KC_TIMEOUT) as r:
            t = json.load(r)
        _svc.update(token=t["access_token"], exp=time.time() + float(t.get("expires_in", 60)))
        return _svc["token"]


def end_keycloak_session(sid: str) -> None:
    """Keycloak 세션 삭제(동시 로그인 제한) — 그 브라우저가 조용히 다시 로그인하지 못하게. 실패는 기록만 한다."""
    server, _, realm = _realm_url().rpartition("/realms/")
    url = f"{server}/admin/realms/{realm}/sessions/{urllib.parse.quote(sid, safe='')}"
    try:
        req = urllib.request.Request(url, method="DELETE", headers={"Authorization": "Bearer " + _service_token()})
        urllib.request.urlopen(req, timeout=_KC_TIMEOUT).close()
    except urllib.error.HTTPError as e:
        if e.code != 404:  # 이미 없는 세션은 정상
            log.warning("Keycloak 세션 삭제 실패 sid=%s: HTTP %s", sid, e.code)
    except Exception as e:  # noqa: BLE001 — 포털 쪽 종료는 이미 반영됨
        log.warning("Keycloak 세션 삭제 실패 sid=%s: %s", sid, e)
