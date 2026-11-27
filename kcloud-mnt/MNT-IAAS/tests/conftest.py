"""시험 환경 — OpenStack·MNT-CORE 를 부르지 않는다.

- OpenStack 연결(get_conn·get_conn_for_project)을 모두 막는다. 권한을 통과한 요청은 OpenStack 단계에서 502 로 끝나고
  실제 자원은 바뀌지 않는다.
- 자원의 소속 프로젝트 조회는 가짜: 아이디가 mine- 으로 시작하면 내 프로젝트(p-mine), 아니면 다른 프로젝트(p-other).
  네트워크 아이디가 ext- 로 시작하면 외부망.
- 요청 사용자(caller)는 fixture 로 사용자(gy.yang, p-mine 소속)·관리자를 고른다.
"""
import os

os.environ.update(OS_AUTH_URL="", CORE_API_BASE="http://core.test", TEMPORAL_API_BASE="", CORS_ORIGINS="")

import pytest  # noqa: E402 — 환경변수를 먼저 정한 뒤 앱을 불러온다
from fastapi.testclient import TestClient  # noqa: E402

from app import auth, main, openstack_client, openstack_quota  # noqa: E402
from app.routers import flavors, images, networks, projects, servers, volumes  # noqa: E402

MINE, OTHER = "p-mine", "p-other"
USER = auth.Caller("gy.yang", "user", frozenset({MINE}))
ADMIN = auth.Caller("admin", "admin", frozenset())


def _blocked(*_a, **_k):
    raise RuntimeError("시험 중 OpenStack 호출 차단")


def _project_of(resource_id: str) -> str:
    return MINE if resource_id.startswith("mine") else OTHER


def _network_info(network_id: str) -> tuple[str, str]:
    return _project_of(network_id), "external" if network_id.startswith("ext") else "tenant"


@pytest.fixture(autouse=True)
def no_openstack(monkeypatch):
    for m in (openstack_client, openstack_quota, flavors, images, networks, projects, servers, volumes):
        for name in ("get_conn", "get_conn_for_project"):
            if hasattr(m, name):
                monkeypatch.setattr(m, name, _blocked)
    monkeypatch.setattr(servers, "server_project", _project_of)
    monkeypatch.setattr(volumes, "volume_project", _project_of)
    monkeypatch.setattr(servers, "volume_project", _project_of)
    monkeypatch.setattr(networks, "network_info", _network_info)
    monkeypatch.setattr(servers, "network_info", _network_info)
    monkeypatch.setattr(networks, "_sg_project", _project_of)
    auth._cache.clear()
    yield
    main.app.dependency_overrides.clear()


def client_as(who: auth.Caller) -> TestClient:
    main.app.dependency_overrides[auth.caller] = lambda: who
    return TestClient(main.app, raise_server_exceptions=False)  # 권한 통과 뒤 막힌 OpenStack 호출 예외는 500 으로
