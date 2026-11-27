"""인증서 발급 스코프 — 클러스터 조회는 기본 클라이언트(설정 프로젝트), CA 조회·CSR 서명은 클러스터 소유 프로젝트로
스코프한 클라이언트(다른 프로젝트 클러스터는 admin 스코프로 인증서가 안 됨 — 2026-10-19 시험)."""
import httpx

from app import k8s_client, openstack_client
from app.openstack_client import MagnumClient, magnum


class _Owner:
    """소유 프로젝트 클라이언트 대역 — 호출만 기록."""

    def __init__(self):
        self.calls = []

    def get(self, path):
        self.calls.append(("GET", path))
        return {"pem": "CA"}

    def request(self, method, path, json=None):
        self.calls.append((method, path))
        return {"pem": "CERT"}


def test_인증서는_소유_프로젝트_스코프로(monkeypatch):
    owner, scoped = _Owner(), []
    monkeypatch.setattr(magnum, "get", lambda path: {"api_address": "https://192.0.2.1:6443", "project_id": "p-owner"})
    monkeypatch.setattr(k8s_client, "for_project", lambda pid: scoped.append(pid) or owner)

    api, ca, cert, key = k8s_client.issue_client_cert("c1")

    assert scoped == ["p-owner"]
    assert owner.calls == [("GET", "/certificates/c1"), ("POST", "/certificates")]  # 기본 클라이언트는 막혀 있음(conftest)
    assert (api, ca, cert) == ("https://192.0.2.1:6443", "CA", "CERT") and "PRIVATE KEY" in key


def test_프로젝트_클라이언트는_프로젝트별_캐시():
    a = openstack_client.for_project("p1")
    assert openstack_client.for_project("p1") is a
    assert openstack_client.for_project("p2") is not a


def _login_scope(cli, monkeypatch):
    sent = {}

    def post(url, json):
        sent.update(json)
        return httpx.Response(201, headers={"X-Subject-Token": "t"}, request=httpx.Request("POST", url),
                              json={"token": {"expires_at": "2099-01-01T00:00:00Z", "catalog": []}})

    monkeypatch.setattr(cli._client, "post", post)
    cli._login()
    return sent["auth"]["scope"]


def test_로그인_스코프(monkeypatch):
    assert _login_scope(MagnumClient("p1"), monkeypatch) == {"project": {"id": "p1"}}
    assert set(_login_scope(MagnumClient(), monkeypatch)["project"]) == {"name", "domain"}  # 기본은 설정의 프로젝트 이름
