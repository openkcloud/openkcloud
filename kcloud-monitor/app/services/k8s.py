"""
관리 클러스터 Kubernetes API 클라이언트 (수집 설정 변경 전용).

Prometheus Operator 의 수집 설정 3종(ServiceMonitor, PodMonitor, ScrapeConfig)만 읽고 고친다.
접속 정보는 .env 의 K8S_API_URL + K8S_TOKEN, 없으면 Pod 안에서 주어지는 서비스 계정을 쓴다.
"""
import logging
import os
from pathlib import Path
from typing import Optional

import httpx

from app.config import settings

logger = logging.getLogger(__name__)

_SA_DIR = Path("/var/run/secrets/kubernetes.io/serviceaccount")

# 수집 대상 ID 의 첫 조각 → (API 경로, 복수형 이름)
RESOURCES = {
    "serviceMonitor": ("apis/monitoring.coreos.com/v1", "servicemonitors"),
    "podMonitor": ("apis/monitoring.coreos.com/v1", "podmonitors"),
    "scrapeConfig": ("apis/monitoring.coreos.com/v1alpha1", "scrapeconfigs"),
}


class K8sError(Exception):
    """Kubernetes API 호출 실패. status 는 HTTP 상태 코드(연결 실패면 None)."""

    def __init__(self, message: str, status: Optional[int] = None) -> None:
        super().__init__(message)
        self.status = status


class K8sClient:
    def _connection(self) -> Optional[tuple[str, str, object]]:
        """(API 주소, 토큰, TLS 검증 설정). 접속 정보가 없으면 None."""
        if settings.K8S_API_URL and settings.K8S_TOKEN:
            verify: object = settings.K8S_CA_FILE or settings.K8S_VERIFY_TLS
            return settings.K8S_API_URL.rstrip("/"), settings.K8S_TOKEN, verify
        host, port = os.environ.get("KUBERNETES_SERVICE_HOST"), os.environ.get("KUBERNETES_SERVICE_PORT", "443")
        token_file = _SA_DIR / "token"
        if host and token_file.exists():
            return f"https://{host}:{port}", token_file.read_text().strip(), str(_SA_DIR / "ca.crt")
        return None

    @property
    def configured(self) -> bool:
        return self._connection() is not None

    async def _request(self, method: str, kind: str, namespace: str, name: str, **kwargs) -> dict:
        conn = self._connection()
        if conn is None:
            raise K8sError("Kubernetes 접속 정보 없음")
        base, token, verify = conn
        group, plural = RESOURCES[kind]
        url = f"{base}/{group}/namespaces/{namespace}/{plural}/{name}"
        headers = {"Authorization": f"Bearer {token}", **kwargs.pop("headers", {})}
        try:
            async with httpx.AsyncClient(timeout=10.0, verify=verify) as client:
                r = await client.request(method, url, headers=headers, **kwargs)
        except httpx.HTTPError as exc:
            # 예외 문구에 내부 주소가 들어가므로 로그에만 남기고 응답에는 싣지 않음
            logger.warning("Kubernetes API 연결 실패: %s", exc)
            raise K8sError("Kubernetes API 연결 실패") from exc
        if r.status_code >= 400:
            logger.warning("Kubernetes API %s %s/%s/%s: %s", r.status_code, kind, namespace, name, r.text[:300])
            raise K8sError(f"Kubernetes API 응답 {r.status_code}", r.status_code)
        return r.json()

    async def get(self, kind: str, namespace: str, name: str) -> dict:
        return await self._request("GET", kind, namespace, name)

    async def merge_patch(self, kind: str, namespace: str, name: str, body: dict) -> dict:
        """JSON merge patch. body 에 metadata.resourceVersion 을 넣으면 그 사이 바뀐 경우 409."""
        return await self._request(
            "PATCH", kind, namespace, name, json=body, headers={"Content-Type": "application/merge-patch+json"}
        )


k8s_client = K8sClient()
