"""Keystone 이 요청을 받고 응답하지 않을 때 — 토큰 발급이 DEFAULT_TIMEOUT 안에 오류로 끝난다(끝없이 기다리지 않음).
생성·변경 요청이 쓰는 새 세션(os_request)도 같은 제한을 따른다."""
import socket
import threading

import pytest

from app import openstack_client
from app.config import settings

# conftest 가 시험마다 OpenStack 연결을 막기 전에 진짜 함수를 잡아 둔다
_get_conn, _get_conn_for_project = openstack_client.get_conn, openstack_client.get_conn_for_project


@pytest.fixture
def silent_keystone(monkeypatch):
    """연결은 받지만 응답하지 않는 가짜 Keystone."""
    srv = socket.socket()
    srv.bind(("127.0.0.1", 0))
    srv.listen(50)
    held = []

    def accept():
        while True:
            try:
                held.append(srv.accept()[0])
            except OSError:
                return

    threading.Thread(target=accept, daemon=True).start()
    url = f"http://127.0.0.1:{srv.getsockname()[1]}"
    monkeypatch.setattr(settings, "os_auth_url", f"{url}/v3")
    monkeypatch.setattr(openstack_client, "DEFAULT_TIMEOUT", 0.5)
    monkeypatch.setattr(openstack_client, "_conn", None)
    monkeypatch.setattr(openstack_client, "_proj_conns", {})
    yield url
    srv.close()
    for c in held:
        c.close()


def _ends(fn, within: float = 5) -> BaseException | None:
    """fn 을 따로 실행해 within 초 안에 끝나는지 — 끝나지 않으면 실패(시험이 멈추지 않게)."""
    out: list[BaseException | None] = []

    def run():
        try:
            fn()
            out.append(None)
        except BaseException as e:  # noqa: BLE001
            out.append(e)

    t = threading.Thread(target=run, daemon=True)
    t.start()
    t.join(within)
    assert not t.is_alive(), f"{within}초 안에 끝나지 않음 — 토큰 발급을 끝없이 기다림"
    return out[0]


def test_토큰_발급_응답이_없으면_오류로_끝남(silent_keystone):
    assert _ends(lambda: _get_conn().identity.get_token()) is not None


def test_생성_요청의_새_세션도_제한(silent_keystone):
    conn = _get_conn_for_project("p-1")
    e = _ends(lambda: openstack_client.os_request(conn, f"{silent_keystone}/servers", "POST", json={}, timeout=30))
    assert e is not None
