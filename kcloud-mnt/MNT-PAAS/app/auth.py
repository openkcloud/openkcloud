"""MNT-PAAS 권한 — 요청 사용자 확인은 MNT-CORE(/api/core/auth/me)에 맡긴다(MNT-IAAS 와 같은 방식).

- 토큰 검증·세션·역할·접근 클러스터는 CORE 가 판정한다(포털 DB 기준). 같은 토큰의 결과는 몇 초 캐시한다.
- 관리자: 전체. 사용자: 접근 권한이 있는 클러스터(paas_project_cluster — 소속 프로젝트에 매핑된 클러스터)만.
  템플릿 조회는 로그인만 확인(신청 화면), 템플릿 생성·수정·삭제와 클러스터 삭제는 관리자만.

오류는 AuthError → main.py 핸들러가 {"detail": 문구, "code": 코드, ...} 로 응답한다(CORE 와 같은 형식).
"""
from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass

import httpx
from fastapi import Depends, Request

from .config import settings

_CACHE_SECONDS = 5  # 세션 종료(로그아웃·강제 종료)가 PAAS 에 반영되기까지 최대 지연


class AuthError(Exception):
    def __init__(self, status: int, code: str, detail: str, **extra):
        super().__init__(detail)
        self.status, self.code, self.detail, self.extra = status, code, detail, extra


@dataclass(frozen=True)
class Caller:
    username: str
    role: str
    cluster_ids: frozenset[str]

    @property
    def is_admin(self) -> bool:
        return self.role == "admin"

    def can(self, cluster_id: str | None) -> bool:
        return self.is_admin or (cluster_id or "") in self.cluster_ids


_cache: dict[str, tuple[float, Caller]] = {}
_client: httpx.AsyncClient | None = None


async def caller(request: Request) -> Caller:
    """FastAPI 의존성 — 요청 토큰의 포털 사용자(역할·접근 클러스터). 한 요청 안에서는 한 번만 계산된다."""
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
    c = Caller(username=me["username"], role=me["role"], cluster_ids=frozenset(me.get("clusterIds") or []))
    now = time.monotonic()
    for k in [k for k, (exp, _) in _cache.items() if exp <= now]:  # 만료된 것 정리
        del _cache[k]
    _cache[key] = (now + _CACHE_SECONDS, c)
    return c


async def require_admin(c: Caller = Depends(caller)) -> Caller:
    if not c.is_admin:
        raise AuthError(403, "FORBIDDEN", "관리자만 할 수 있습니다.")
    return c


async def cluster_member(cluster_id: str, c: Caller = Depends(caller)) -> Caller:
    """경로의 cluster_id 에 대한 접근 권한 확인 — 관리자 또는 접근 권한이 있는 클러스터가 아니면 403."""
    if not c.can(cluster_id):
        raise AuthError(403, "FORBIDDEN", "접근 권한이 없는 클러스터입니다.")
    return c
