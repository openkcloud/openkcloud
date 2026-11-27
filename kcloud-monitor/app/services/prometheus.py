"""
KCloud Monitor v2 — Prometheus HTTP 클라이언트.

데이터소스: Prometheus HTTP API (.env PROMETHEUS_URL 필수).
"""
import logging
import math
from datetime import datetime, timezone
from typing import Optional

import httpx

from app.config import settings

logger = logging.getLogger(__name__)


class PrometheusClient:
    def __init__(self) -> None:
        if not settings.PROMETHEUS_URL:
            raise RuntimeError("PROMETHEUS_URL 환경변수가 설정되지 않았습니다. .env에 PROMETHEUS_URL을 지정하세요.")
        self.base_url: str = settings.PROMETHEUS_URL

    async def ping(self) -> bool:
        """Prometheus 준비 상태 확인 — /-/ready (헬스체크용, 짧은 타임아웃).

        연결 가능하고 200이면 True, 그 외(미도달·비정상)면 False.
        """
        url = f"{self.base_url}/-/ready"
        try:
            async with httpx.AsyncClient(timeout=2.0) as client:
                r = await client.get(url)
                return r.status_code == 200
        except httpx.HTTPError as exc:
            logger.warning("Prometheus ping failed: %s — url=%s", exc, url)
            return False

    async def instant(self, query: str) -> list[dict]:
        """즉시 쿼리(instant query).

        GET {base_url}/api/v1/query?query={query}
        성공 시 data.result 목록 반환, 오류 시 빈 리스트 반환.
        """
        url = f"{self.base_url}/api/v1/query"
        try:
            async with httpx.AsyncClient(timeout=10.0) as client:
                r = await client.get(url, params={"query": query})
                r.raise_for_status()
                return r.json()["data"]["result"]
        except httpx.HTTPError as exc:
            logger.warning("Prometheus instant query error: %s — query=%r", exc, query)
            return []

    async def active_targets(self) -> Optional[list[dict]]:
        """Prometheus 가 직접 가져오는(pull) 수집 대상 목록.

        GET {base_url}/api/v1/targets?state=active
        성공 시 data.activeTargets, 오류 시 None (빈 목록과 구분).
        """
        url = f"{self.base_url}/api/v1/targets"
        try:
            async with httpx.AsyncClient(timeout=10.0) as client:
                r = await client.get(url, params={"state": "active"})
                r.raise_for_status()
                return r.json()["data"]["activeTargets"]
        except (httpx.HTTPError, KeyError, ValueError) as exc:
            logger.warning("Prometheus targets error: %s", exc)
            return None

    async def range_query(
        self,
        query: str,
        start: str,
        end: str,
        step: str = "60s",
        raise_errors: bool = False,
    ) -> list[dict]:
        """범위 쿼리(range query).

        GET {base_url}/api/v1/query_range?query={query}&start={start}&end={end}&step={step}
        성공 시 data.result 목록 반환, 오류 시 빈 리스트 반환.
        raise_errors=True 면 오류를 그대로 올림 (빈 결과와 조회 실패를 구분해야 하는 미터링 계측용).
        """
        url = f"{self.base_url}/api/v1/query_range"
        params = {"query": query, "start": start, "end": end, "step": step}
        try:
            async with httpx.AsyncClient(timeout=30.0) as client:
                r = await client.get(url, params=params)
                r.raise_for_status()
                return r.json()["data"]["result"]
        except httpx.HTTPError as exc:
            if raise_errors:
                raise
            logger.warning(
                "Prometheus range query error: %s — query=%r start=%s end=%s",
                exc, query, start, end,
            )
            return []


def esc(value: str) -> str:
    """PromQL 라벨 값 이스케이프 (역슬래시, 큰따옴표)."""
    return value.replace("\\", "\\\\").replace('"', '\\"')


def parse_float(raw) -> Optional[float]:
    try:
        return float(raw)
    except (TypeError, ValueError):
        return None


def parse_finite(raw) -> Optional[float]:
    """parse_float + NaN/Inf 는 JSON 비호환이라 값 없음(None)으로 취급."""
    value = parse_float(raw)
    if value is None or math.isnan(value) or math.isinf(value):
        return None
    return value


def sample_value(item: dict) -> Optional[float]:
    """instant 결과 한 항목의 value 파싱. 없으면 None."""
    try:
        return parse_float(item["value"][1])
    except (KeyError, IndexError, TypeError):
        return None


def first_value(results: list[dict]) -> Optional[float]:
    """instant 결과 첫 항목의 value."""
    return sample_value(results[0]) if results else None


def first_finite(results: list[dict]) -> Optional[float]:
    """first_value 와 같되 NaN/Inf 는 None."""
    return parse_finite(first_value(results))


def value_map(results: list[dict], *labels: str, finite: bool = False) -> dict:
    """instant 결과를 라벨 값(여러 개면 튜플) → value 사전으로. 없는 라벨은 "", 값 없는 항목은 제외.

    finite=True 면 NaN/Inf 도 값 없음으로 보고 제외.
    """
    out: dict = {}
    for item in results:
        metric = item.get("metric", {})
        key = tuple(metric.get(label, "") for label in labels)
        val = parse_finite(sample_value(item)) if finite else sample_value(item)
        if val is not None:
            out[key if len(labels) > 1 else key[0]] = val
    return out


def ts_iso(ts) -> Optional[str]:
    """Prometheus epoch(초) → ISO 8601 UTC 문자열."""
    if ts is None:
        return None
    return datetime.fromtimestamp(float(ts), tz=timezone.utc).isoformat()


prometheus_client = PrometheusClient()
