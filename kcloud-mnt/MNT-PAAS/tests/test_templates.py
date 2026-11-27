"""클러스터 템플릿 수정 — 라벨 키 단위 JSON Patch(add + 빠진 키 remove, ETRI 검토의견: 항목 삭제 반영)."""
import pytest
from conftest import ADMIN, client_as

from app.routers import templates

P = "/api/paas"


@pytest.fixture
def magnum_calls(monkeypatch):
    """Magnum 대역 — GET 은 기존 라벨, PATCH 는 보낸 patch 를 기록."""
    state = {"labels": {"octavia_provider": "ovn", "kcloud_operator_enabled": "true"}, "patches": []}

    def fake(method, path, json=None):
        assert path == "/clustertemplates/t1", path
        if method == "GET":
            return {"uuid": "t1", "labels": dict(state["labels"])}
        if method == "PATCH":
            state["patches"].append(json)
            return {"uuid": "t1"}
        raise AssertionError(method)

    monkeypatch.setattr(templates, "magnum", type("M", (), {"request": staticmethod(fake)})())
    return state


def _patch_ops(state):
    assert len(state["patches"]) == 1, state["patches"]
    return sorted(state["patches"][0], key=lambda o: (o["op"], o["path"]))


def test_라벨_항목_삭제가_반영된다(magnum_calls):
    # 기존 octavia_provider·kcloud_operator_enabled 중 하나만 남기면, 빠진 키는 remove
    r = client_as(ADMIN).patch(P + "/clustertemplates/t1", json={"labels": {"octavia_provider": "ovn"}})
    assert r.status_code == 200, r.text
    assert _patch_ops(magnum_calls) == [
        {"op": "add", "path": "/labels/octavia_provider", "value": "ovn"},
        {"op": "remove", "path": "/labels/kcloud_operator_enabled"},
    ]


def test_라벨_전체_삭제(magnum_calls):
    r = client_as(ADMIN).patch(P + "/clustertemplates/t1", json={"labels": {}})
    assert r.status_code == 200, r.text
    assert _patch_ops(magnum_calls) == [
        {"op": "remove", "path": "/labels/kcloud_operator_enabled"},
        {"op": "remove", "path": "/labels/octavia_provider"},
    ]


def test_라벨_미제출이면_건드리지_않는다(magnum_calls):
    r = client_as(ADMIN).patch(P + "/clustertemplates/t1", json={"name": "t1-new"})
    assert r.status_code == 200, r.text
    assert _patch_ops(magnum_calls) == [{"op": "replace", "path": "/name", "value": "t1-new"}]


def test_변경할_것이_없으면_400(magnum_calls):
    magnum_calls["labels"] = {}
    r = client_as(ADMIN).patch(P + "/clustertemplates/t1", json={"labels": {}, "name": ""})
    assert r.status_code == 400 and "변경할 필드가 없습니다" in r.json()["detail"]
    assert magnum_calls["patches"] == []
