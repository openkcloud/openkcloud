"""MNT-IAAS 권한 — 요청 사용자 확인은 MNT-CORE(/api/core/auth/me)에 맡긴다.

- 토큰 검증·세션·역할·소속 프로젝트는 CORE 가 판정한다(포털 DB 기준). 같은 토큰의 결과는 몇 초 캐시한다.
- 관리자: 전체. 사용자: 소속 프로젝트 자원만(조회와 허용된 쓰기). 공용 목록(플레이버 등)은 로그인만 확인.
- 라우터 함수는 프로비저닝 워커가 프로세스 안에서 직접 부른다(create_volume·delete_volume·allocate_fip·update_fip·
  release_fip·delete_server·list_server_arqs). 그래서 이 함수들의 인자는 바꾸지 않고 검사는 의존성(Depends)에서만 한다.

오류는 AuthError → main.py 핸들러가 {"detail": 문구, "code": 코드, ...} 로 응답한다(CORE 와 같은 형식).
"""
from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass

import httpx
from fastapi import Depends, Request

from .config import settings

_CACHE_SECONDS = 5  # 세션 종료(로그아웃·강제 종료)가 IAAS 에 반영되기까지 최대 지연


class AuthError(Exception):
    def __init__(self, status: int, code: str, detail: str, **extra):
        super().__init__(detail)
        self.status, self.code, self.detail, self.extra = status, code, detail, extra


@dataclass(frozen=True)
class Caller:
    username: str
    role: str
    project_ids: frozenset[str]

    @property
    def is_admin(self) -> bool:
        return self.role == "admin"

    def can(self, project_id: str | None) -> bool:
        return self.is_admin or (project_id or "") in self.project_ids


_cache: dict[str, tuple[float, Caller]] = {}
_client: httpx.AsyncClient | None = None


async def caller(request: Request) -> Caller:
    """FastAPI 의존성 — 요청 토큰의 포털 사용자(역할·소속 프로젝트). 한 요청 안에서는 한 번만 계산된다."""
    header = request.headers.get("authorization", "")
    if not header.lower().startswith("bearer ") or not header[7:].strip():
        raise AuthError(401, "TOKEN_REQUIRED", "로그인이 필요합니다.")
    key = hashlib.sha256(header.encode()).hexdigest()
    hit = _cache.get(key)
    if hit and hit[0] > time.monotonic():
        return hit[1]

    if not settings.core_api_base:
        raise AuthError(503, "AUTH_NOT_CONFIGURED", "MNT-CORE 주소(CORE_API_BASE)가 설정되지 않았습니다.")
    global _client
    if _client is None:
        _client = httpx.AsyncClient(timeout=10)
    try:
        r = await _client.get(settings.core_api_base.rstrip("/") + "/api/core/auth/me", headers={"Authorization": header})
    except httpx.HTTPError as e:
        raise AuthError(503, "AUTH_UNAVAILABLE", f"사용자 확인(MNT-CORE)에 실패했습니다: {type(e).__name__}")
    if r.status_code in (401, 403):  # CORE 판정(토큰 만료·세션 종료·미등록 등)을 그대로 전달
        body = r.json()
        raise AuthError(r.status_code, body.pop("code", "UNAUTHORIZED"), body.pop("detail", ""), **body)
    if r.status_code != 200:
        raise AuthError(503, "AUTH_UNAVAILABLE", f"사용자 확인(MNT-CORE) 응답 오류: HTTP {r.status_code}")
    me = r.json()
    c = Caller(username=me["username"], role=me["role"], project_ids=frozenset(me.get("projectIds") or []))
    now = time.monotonic()
    for k in [k for k, (exp, _) in _cache.items() if exp <= now]:  # 만료된 것 정리
        del _cache[k]
    _cache[key] = (now + _CACHE_SECONDS, c)
    return c


async def require_admin(c: Caller = Depends(caller)) -> Caller:
    if not c.is_admin:
        raise AuthError(403, "FORBIDDEN", "관리자만 할 수 있습니다.")
    return c


def ensure_project(c: Caller, project_id: str | None) -> None:
    """관리자 또는 그 프로젝트 멤버가 아니면 403."""
    if not c.can(project_id):
        raise AuthError(403, "FORBIDDEN", "소속 프로젝트의 자원이 아닙니다.")


async def project_member(project_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 project_id 에 대한 멤버 확인."""
    ensure_project(c, project_id)
    return c


def scoped(c: Caller, items: list[dict], key: str = "tenantId", also=None) -> dict:
    """목록을 사용자 범위로 거른다 — 관리자는 전체, 사용자는 소속 프로젝트(+ also(item) 이 참인 공용 항목)."""
    if not c.is_admin:
        items = [x for x in items if x.get(key) in c.project_ids or (also is not None and also(x))]
    return {"items": items, "count": len(items)}
