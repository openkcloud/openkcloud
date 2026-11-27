"""
KCloud Monitor v2 — Loki HTTP 클라이언트.

데이터소스: Loki HTTP API (.env LOKI_URL).
PrometheusClient와 동형 구조 — ping/query_range/labels/label_values/volume.
"""
import logging
from typing import Optional

import httpx
from fastapi import HTTPException

from app.config import settings

logger = logging.getLogger(__name__)

_EMPTY_STREAMS = {"status": "success", "data": {"resultType": "streams", "result": []}}


class LokiClient:
    def __init__(self) -> None:
        self.base_url: str = settings.LOKI_URL or ""

    @property
    def configured(self) -> bool:
        return bool(self.base_url)

    async def query_range(
        self,
        query: str,
        start: str,
        end: str,
        limit: int = 100,
        direction: str = "backward",
    ) -> dict:
        if not self.configured:
            return _EMPTY_STREAMS
        url = f"{self.base_url}/loki/api/v1/query_range"
        params = {
            "query": query,
            "start": start,
            "end": end,
            "limit": str(limit),
            "direction": direction,
        }
        try:
            async with httpx.AsyncClient(timeout=30.0) as client:
                r = await client.get(url, params=params)
                r.raise_for_status()
                return r.json()
        except httpx.HTTPStatusError as exc:
            # 빈 결과로 바꾸면 "쿼리 오류"와 "로그 없음"이 구분되지 않으므로 그대로 올려보냄
            logger.warning("Loki query_range HTTP error: %s — query=%r", exc, query)
            if exc.response.status_code == 400:
                raise HTTPException(status_code=400, detail=f"LogQL 오류: {exc.response.text.strip()}")
            raise HTTPException(status_code=502, detail=f"Loki 응답 오류 ({exc.response.status_code})")
        except httpx.RequestError as exc:
            logger.warning("Loki query_range request error: %s — query=%r", exc, query)
            raise HTTPException(status_code=504, detail=f"Loki 연결 실패: {type(exc).__name__}")

    async def query_instant(self, query: str) -> Optional[list[dict]]:
        """현재 시각 기준 집계 쿼리 결과(vector). 알람 평가용이라 실패 시 예외 대신 None."""
        if not self.configured:
            return None
        try:
            async with httpx.AsyncClient(timeout=15.0) as client:
                r = await client.get(f"{self.base_url}/loki/api/v1/query", params={"query": query})
                r.raise_for_status()
                return r.json().get("data", {}).get("result", [])
        except (httpx.HTTPStatusError, httpx.RequestError) as exc:
            logger.warning("Loki query error: %s — query=%r", exc, query)
            return None

    async def labels(
        self, start: Optional[str] = None, end: Optional[str] = None,
    ) -> Optional[list[str]]:
        """라벨 키 목록. 실패 시 None (빈 목록과 구분)."""
        if not self.configured:
            return None
        return await self._get_list("labels", start, end)

    async def label_values(
        self, label_name: str, start: Optional[str] = None, end: Optional[str] = None,
    ) -> Optional[list[str]]:
        """라벨 값 목록. 실패 시 None (빈 목록과 구분)."""
        if not self.configured:
            return None
        return await self._get_list(f"label/{label_name}/values", start, end)

    async def _get_list(self, path: str, start: Optional[str], end: Optional[str]) -> Optional[list[str]]:
        """labels, label_values 공통 GET. 실패 시 None."""
        params: dict[str, str] = {}
        if start:
            params["start"] = start
        if end:
            params["end"] = end
        try:
            async with httpx.AsyncClient(timeout=10.0) as client:
                r = await client.get(f"{self.base_url}/loki/api/v1/{path}", params=params)
                r.raise_for_status()
                return r.json().get("data", [])
        except (httpx.HTTPStatusError, httpx.RequestError) as exc:
            logger.warning("Loki %s error: %s", path, exc)
            return None

    async def volume(
        self, query: str, start: str, end: str, limit: int = 100,
    ) -> dict:
        """Loki index/volume (Loki 2.9+). 실패·미지원 시 빈 dict."""
        if not self.configured:
            return {}
        url = f"{self.base_url}/loki/api/v1/index/volume"
        params = {"query": query, "start": start, "end": end, "limit": str(limit)}
        try:
            async with httpx.AsyncClient(timeout=15.0) as client:
                r = await client.get(url, params=params)
                r.raise_for_status()
                return r.json()
        except (httpx.HTTPStatusError, httpx.RequestError) as exc:
            logger.warning("Loki volume error: %s", exc)
            return {}


loki_client = LokiClient()
