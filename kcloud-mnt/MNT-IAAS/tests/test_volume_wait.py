"""부팅 볼륨 준비 대기 작업(iaas.wait_volume) — 시간 초과면 지우지 않고 상태를 돌려주고, error 면 지우고 실패."""
import pytest
from temporalio.exceptions import ApplicationError

from app.routers import servers
from app.workflows import activities


class _Resp:
    def __init__(self, status: str):
        self.status = status

    def json(self):
        return {"volume": {"status": self.status}}


@pytest.fixture
def cinder(monkeypatch):
    """볼륨 상태를 정하는 가짜 Cinder — 지우기 시도는 deleted 에 기록."""
    state = {"status": "downloading", "deleted": []}
    monkeypatch.setattr(servers, "_cinder_req", lambda *a, **k: _Resp(state["status"]))
    monkeypatch.setattr(servers, "_discard_volume", lambda vid: state["deleted"].append(vid) or " — 삭제함")
    return state


def test_준비되면_None(cinder):
    cinder["status"] = "available"
    assert activities.wait_volume("p-1", "vol-1", 0) is None


def test_시간_초과면_지우지_않고_상태(cinder):
    assert activities.wait_volume("p-1", "vol-1", 0) == "downloading"  # 마감 지남 — 한 번 보고 바로 돌려준다
    assert cinder["deleted"] == []


def test_error_면_지우고_실패(cinder):
    cinder["status"] = "error"
    with pytest.raises(ApplicationError, match="Cinder 볼륨 생성 오류"):
        activities.wait_volume("p-1", "vol-1", 0)
    assert cinder["deleted"] == ["vol-1"]
