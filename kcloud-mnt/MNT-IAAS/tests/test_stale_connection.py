"""오래 쉬던 연결을 중간 장비가 끊어 놓았을 때 — 조회는 새 연결로 1회 다시, 생성·변경은 처음부터 새 연결,
토큰 재발급도 1회 다시 보내 실패하지 않는다.

가짜 OpenStack: 연결마다 첫 요청만 응답하고, 같은 연결로 온 다음 요청은 응답 없이 닫는다
(쉬는 동안 끊긴 연결을 다시 쓴 것과 같은 'Remote end closed connection without response')."""
import json
import threading
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from app import openstack_client, openstack_quota
from app.config import settings
from app.routers import flavors, images, keypairs, projects

# conftest 가 시험마다 OpenStack 연결을 막기 전에 진짜 함수를 잡아 둔다
_get_conn = openstack_client.get_conn


def _token() -> bytes:
    exp = (datetime.now(timezone.utc) + timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%S.000000Z")
    dom = {"id": "default", "name": "Default"}
    return json.dumps({"token": {"methods": ["password"], "expires_at": exp, "catalog": [],
                                 "user": {"id": "u", "name": "u", "domain": dom},
                                 "project": {"id": "p-admin", "name": "admin", "domain": dom}}}).encode()


class _Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"  # 연결을 열어 두고 다시 쓰게 한다

    def setup(self):
        super().setup()
        self.served = False  # 이 연결에서 응답했는지(처리기는 연결마다 하나)

    def _serve(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.served:  # 다시 쓴 연결 — 응답 없이 닫는다
            self.server.dropped.append((self.command, self.path))
            self.close_connection = True
            return
        self.served = True
        self.server.served.append((self.command, self.path))
        body, code = (_token(), 201) if self.path.endswith("/auth/tokens") else (b"{}", 200)
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if code == 201:
            self.send_header("X-Subject-Token", "tok")
        self.end_headers()
        self.wfile.write(body)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _serve

    def log_message(self, *_a):
        pass


@pytest.fixture
def openstack(monkeypatch):
    srv = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
    srv.served, srv.dropped = [], []
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{srv.server_address[1]}"
    monkeypatch.setattr(settings, "os_auth_url", f"{url}/v3")
    monkeypatch.setattr(settings, "os_endpoint_mode", "override")
    monkeypatch.setattr(settings, "os_endpoint_template", url + "/{service}")
    monkeypatch.setattr(openstack_client, "_conn", None)
    monkeypatch.setattr(openstack_client, "_proj_conns", {})
    for m in (flavors, images, keypairs, projects, openstack_quota):
        monkeypatch.setattr(m, "get_conn", _get_conn)
    yield srv
    srv.shutdown()
    srv.server_close()


def test_조회는_끊긴_연결이면_새_연결로_다시(openstack):
    _get_conn().session.get_token()  # 토큰 받은 연결이 쉬는 연결로 남는다
    assert keypairs._nova("GET", "/os-keypairs").ok
    assert openstack.dropped == [("GET", "/nova/v2.1/os-keypairs")]  # 끊긴 연결에서 한 번 실패한 뒤
    assert openstack.served[-1] == ("GET", "/nova/v2.1/os-keypairs")


@pytest.mark.parametrize("call", [
    lambda: flavors._os_req("nova", "compute", "/v2.1", "POST", "/flavors", json={}),
    lambda: images._glance_req("DELETE", "/images/i-1"),
    lambda: keypairs._nova("POST", "/os-keypairs", json={}),
    lambda: projects.delete_project("p-1"),
    lambda: openstack_quota.set_project_quota("p-1", vcpu=1, ram_gb=1, disk_gb=1),
], ids=["플레이버", "이미지", "키페어", "프로젝트", "쿼터"])
def test_생성_변경은_새_연결로(openstack, call):
    _get_conn().session.get_token()
    call()
    assert openstack.dropped == []  # 쉬는 연결을 쓰지 않았다 — 썼다면 실패하거나 같은 요청을 다시 보낸다(중복 위험)
    assert len(openstack.served) > 1


def test_토큰_재발급도_끊긴_연결이면_다시(openstack):
    conn = _get_conn()
    conn.session.get_token()
    conn.session.auth.invalidate()  # 만료 — 다음 요청 때 다시 받는다
    assert conn.session.get_token() == "tok"
    assert openstack.dropped == [("POST", "/v3/auth/tokens")]
    assert openstack.served == [("POST", "/v3/auth/tokens")] * 2
