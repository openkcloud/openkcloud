"""시험 환경 — Magnum·클러스터 K8s·MNT-CORE 를 부르지 않는다.

- Magnum 호출(모든 MagnumClient.request·service_get·service_request — 프로젝트 스코프 클라이언트 포함)을 막는다.
  클러스터 K8s 접속도 Magnum 인증서 발급을 거치므로 함께 막힌다.
  권한을 통과한 요청은 Magnum·K8s 단계에서 502 등으로 끝나고 실제 자원은 바뀌지 않는다.
- 요청 사용자(caller)는 fixture 로 사용자(gy.yang, c-mine 접근 권한)·관리자를 고른다.
"""
import os

os.environ.update(OS_AUTH_URL="", CORE_API_BASE="http://core.test", TEMPORAL_API_BASE="", CORS_ORIGINS="")

import pytest  # noqa: E402 — 환경변수를 먼저 정한 뒤 앱을 불러온다
from fastapi.testclient import TestClient  # noqa: E402

from app import auth, k8s_client, main, openstack_client  # noqa: E402
from app.openstack_client import MagnumClient  # noqa: E402

MINE, OTHER = "c-mine", "c-other"
USER = auth.Caller("gy.yang", "user", frozenset({MINE}))
ADMIN = auth.Caller("admin", "admin", frozenset())


def _blocked(*_a, **_k):
    raise RuntimeError("시험 중 Magnum 호출 차단")


@pytest.fixture(autouse=True)
def no_magnum(monkeypatch):
    monkeypatch.setattr(MagnumClient, "request", _blocked)
    monkeypatch.setattr(MagnumClient, "service_get", _blocked)
    monkeypatch.setattr(MagnumClient, "service_request", _blocked)
    k8s_client._clients.clear()
    openstack_client._project_clients.clear()
    auth._cache.clear()
    yield
    main.app.dependency_overrides.clear()


def client_as(who: auth.Caller) -> TestClient:
    main.app.dependency_overrides[auth.caller] = lambda: who
    return TestClient(main.app, raise_server_exceptions=False)  # 권한 통과 뒤 막힌 Magnum 호출 예외는 500 으로
