"""프로젝트 플레이버(GET /projects/{id}/flavors) — public + 그 프로젝트에 접근이 허용된 비공개만."""
from conftest import MINE, USER, client_as

from app.routers import flavors

P = "/api/iaas"
FLAVORS = [{"id": "f-pub", "name": "m1.large", "os-flavor-access:is_public": True},
           {"id": "f-mine", "name": "m1.mine", "os-flavor-access:is_public": False},
           {"id": "f-other", "name": "m1.other", "os-flavor-access:is_public": False}]
ACCESS = {"f-mine": [{"flavor_id": "f-mine", "tenant_id": MINE}], "f-other": [{"flavor_id": "f-other", "tenant_id": "p-other"}]}


def _resp(data):
    return type("R", (), {"json": lambda self: data})()


def test_공개와_허용된_비공개만(monkeypatch):
    calls = []

    def nova(method, path, **k):
        calls.append(path)
        if path.startswith("/flavors/detail"):
            return _resp({"flavors": FLAVORS})
        fid, tail = path.split("/")[2], path.split("/")[3]
        return _resp({"flavor_access": ACCESS[fid]} if tail == "os-flavor-access" else {"extra_specs": {}})

    monkeypatch.setattr(flavors, "_nova", nova)
    monkeypatch.setattr(flavors, "_cyborg", lambda *a, **k: _resp({"device_profiles": []}))
    r = client_as(USER).get(f"{P}/projects/{MINE}/flavors")
    assert r.status_code == 200, r.text
    assert [f["name"] for f in r.json()["items"]] == ["m1.large", "m1.mine"]
    assert "/flavors/detail?is_public=None" in calls  # 비공개까지 받아야 거를 수 있다
    assert "/flavors/f-pub/os-flavor-access" not in calls  # 공개 플레이버는 접근 목록을 보지 않는다(Nova 가 거절)
