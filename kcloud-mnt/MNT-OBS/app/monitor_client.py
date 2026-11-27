"""KCloud Monitor v2 API(이노그리드) 클라이언트.

admin/changeme 로 로그인해 JWT(access_token)를 받아 캐시하고, 만료(expires_in)나
401 발생 시 재로그인한다. MNT-IAAS 가 extadmin 으로 OpenStack 을 대표 호출하는 것과 동일 패턴.
"""
from __future__ import annotations

import threading
import time

import httpx

from .config import settings


class MonitorClient:
    def __init__(self) -> None:
        self._token: str | None = None
        self._exp: float = 0.0
        self._lock = threading.Lock()
        self._client = httpx.Client(base_url=settings.monitor_base_url, timeout=settings.monitor_timeout)

    def _login(self) -> None:
        r = self._client.post(
            "/auth/login",
            json={"username": settings.monitor_username, "password": settings.monitor_password},
        )
        r.raise_for_status()
        d = r.json()
        self._token = d["access_token"]
        # 만료 60초 전에 미리 갱신
        self._exp = time.time() + float(d.get("expires_in", 3600)) - 60

    def _ensure_token(self) -> None:
        if not self._token or time.time() >= self._exp:
            with self._lock:
                if not self._token or time.time() >= self._exp:
                    self._login()

    def get(self, path: str, params: dict | None = None) -> dict:
        self._ensure_token()
        headers = {"Authorization": f"Bearer {self._token}", "Accept": "application/json"}
        r = self._client.get(path, headers=headers, params=params)
        if r.status_code == 401:  # 토큰 조기 만료 등 → 1회 재로그인
            with self._lock:
                self._login()
            headers["Authorization"] = f"Bearer {self._token}"
            r = self._client.get(path, headers=headers, params=params)
        r.raise_for_status()
        return r.json()

    def get_raw(self, path: str, params: dict | None = None) -> httpx.Response:
        """원시 응답(바이트) — CSV/JSON 파일 내보내기 중계용."""
        self._ensure_token()
        headers = {"Authorization": f"Bearer {self._token}"}
        r = self._client.get(path, headers=headers, params=params)
        if r.status_code == 401:
            with self._lock:
                self._login()
            headers["Authorization"] = f"Bearer {self._token}"
            r = self._client.get(path, headers=headers, params=params)
        r.raise_for_status()
        return r

    def iter_sse(self, path: str, params: dict | None = None):
        """SSE 스트림을 청크 단위로 중계(제너레이터). 연결 유지 위해 read timeout 없음."""
        self._ensure_token()
        headers = {"Authorization": f"Bearer {self._token}", "Accept": "text/event-stream"}
        with self._client.stream("GET", path, headers=headers, params=params, timeout=None) as r:
            r.raise_for_status()
            for chunk in r.iter_raw():
                yield chunk

    def request(self, method: str, path: str, json: dict | None = None, params: dict | None = None) -> dict:
        """POST/PATCH/DELETE 중계 — 바디·응답 JSON 그대로. 본문 없으면 {} 반환."""
        self._ensure_token()
        headers = {"Authorization": f"Bearer {self._token}", "Accept": "application/json"}
        r = self._client.request(method, path, headers=headers, json=json, params=params)
        if r.status_code == 401:
            with self._lock:
                self._login()
            headers["Authorization"] = f"Bearer {self._token}"
            r = self._client.request(method, path, headers=headers, json=json, params=params)
        r.raise_for_status()
        return r.json() if r.content else {}


monitor = MonitorClient()
