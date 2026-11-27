"""역할별 권한 — API 38개 전부. 사용자(gy.yang)는 접근 권한이 있는 클러스터(c-mine)의 상세·kubeconfig·내부(K8s)
조회·쓰기와 템플릿 조회만, 클러스터 삭제·노드그룹 변경과 템플릿 생성·수정·삭제는 관리자만."""
import httpx
import pytest
from conftest import ADMIN, MINE, OTHER, USER, client_as
from fastapi.testclient import TestClient

from app import auth, main
from app.openstack_client import magnum
from app.routers import templates

A, D = "허용", "거부"
P = "/api/paas"

CASES = [
    # 클러스터
    ("GET", "/clusters", None, A),
    ("GET", f"/clusters/{MINE}", None, A), ("GET", f"/clusters/{OTHER}", None, D),
    ("DELETE", f"/clusters/{MINE}", None, D),                    # 삭제는 접근 권한이 있어도 관리자만
    # 노드그룹 변경(추가·크기조정·삭제)도 관리자만 — 클러스터 인프라 영역
    ("POST", f"/clusters/{MINE}/nodegroups", {"name": "ng-1"}, D),
    ("POST", f"/clusters/{MINE}/nodegroups/ng-1/resize", {"nodeCount": 2}, D),
    ("DELETE", f"/clusters/{MINE}/nodegroups/ng-1", None, D),
    ("GET", f"/clusters/{MINE}/kubeconfig", None, A), ("GET", f"/clusters/{OTHER}/kubeconfig", None, D),
    ("GET", "/cluster-name-check?name=abc", None, A),            # 신청 화면 — 이름 사용 여부만
    # 템플릿 — 조회는 신청 화면용
    ("GET", "/clustertemplates", None, A), ("GET", "/clustertemplates/t1", None, A),
    ("POST", "/clustertemplates", {"name": "t"}, D),
    ("PATCH", "/clustertemplates/t1", {"name": "t"}, D), ("DELETE", "/clustertemplates/t1", None, D),
]
# 클러스터 내부(K8s) — 접근 권한이 있는 클러스터만
K8S = [
    ("GET", "/namespaces", None), ("GET", "/workloads", None), ("GET", "/pods", None), ("GET", "/services", None),
    ("GET", "/storage", None), ("GET", "/configs", None),
    ("GET", "/accelerators", None),                              # 워크로드 생성 — 클러스터 보유 가속기
    ("POST", "/workloads", {"name": "w", "namespace": "default", "image": "nginx"}),
    ("POST", "/workloads/apply", {"yaml": "kind: Deployment"}),
    ("POST", "/workloads/apply/preview", {"yaml": "kind: Deployment"}),
    ("GET", "/workloads/Deployment/default/w/yaml", None), ("PUT", "/workloads/Deployment/default/w/yaml", {"yaml": ""}),
    ("GET", "/workloads/Deployment/default/w/pods", None),
    ("GET", "/pods/default/p/logs", None),
    ("POST", "/workloads/Deployment/default/w/scale", {"replicas": 1}),
    ("POST", "/workloads/Deployment/default/w/restart", None),
    ("DELETE", "/workloads/Deployment/default/w", None),
    ("POST", "/configmaps", {"name": "c", "namespace": "default"}),
    ("PUT", "/configmaps/default/c", {"data": {}}), ("DELETE", "/configmaps/default/c", None),
    ("GET", "/secrets/default/s", None), ("POST", "/secrets", {"name": "s", "namespace": "default"}),
    ("PUT", "/secrets/default/s", {"data": {}}), ("DELETE", "/secrets/default/s", None),
]
CASES += [(m, f"/clusters/{MINE}{p}", b, A) for m, p, b in K8S] + [(m, f"/clusters/{OTHER}{p}", b, D) for m, p, b in K8S]


def _call(client, method, path, body):
    return client.request(method, P + path, json=body)


@pytest.mark.parametrize("method,path,body,expected", CASES, ids=[f"{m} {p} {b or ''}" for m, p, b, _ in CASES])
def test_사용자_권한(method, path, body, expected):
    r = _call(client_as(USER), method, path, body)
    if expected == D:
        assert r.status_code == 403 and r.json()["code"] == "FORBIDDEN", r.text
    else:
        assert r.status_code not in (401, 403), r.text  # 권한 통과 — 이후 Magnum 단계는 막혀 502 등


@pytest.mark.parametrize("method,path,body,expected", CASES, ids=[f"{m} {p} {b or ''}" for m, p, b, _ in CASES])
def test_관리자는_모두_통과(method, path, body, expected):
    r = _call(client_as(ADMIN), method, path, body)
    assert r.status_code not in (401, 403), r.text


def test_모든_API가_시험표에_있음():
    """API 를 새로 추가하면 이 표에도 권한 규칙을 넣게 한다(경로 템플릿 {x} 를 한 마디로 맞춰 본다)."""
    import re
    called = {(m, P + p.split("?")[0]) for m, p, *_ in CASES}
    missing = []
    for path, ops in main.app.openapi()["paths"].items():
        if path in ("/healthz", P + "/"):  # 상태 확인·서비스 이름 — 로그인 없이
            continue
        rx = re.compile("^" + re.sub(r"\{[^}]+\}", "[^/]+", path) + "$")
        missing += [(m.upper(), path) for m in ops if not any(cm == m.upper() and rx.match(cp) for cm, cp in called)]
    assert not missing, f"권한 시험표에 없는 API: {missing}"


# ── 목록 거르기 ──────────────────────────────────────────────────

def test_클러스터_목록은_접근_권한이_있는_것만(monkeypatch):
    seen = []

    def fake(method, path, json=None):
        seen.append(path)
        if path == "/clusters":
            return {"clusters": [{"uuid": MINE}, {"uuid": OTHER}]}
        return {"uuid": path.rsplit("/", 1)[-1], "name": "n"}

    monkeypatch.setattr(magnum, "request", fake)
    assert [c["id"] for c in client_as(USER).get(P + "/clusters").json()["items"]] == [MINE]
    assert f"/clusters/{OTHER}" not in seen  # 권한 없는 클러스터는 상세도 조회하지 않는다
    assert [c["id"] for c in client_as(ADMIN).get(P + "/clusters").json()["items"]] == [MINE, OTHER]


# ── 템플릿 소유 프로젝트 ─────────────────────────────────────────

def test_템플릿은_고른_프로젝트_소유로_생성(monkeypatch):
    # Magnum 은 요청 토큰의 프로젝트를 템플릿 소유로 둔다 — 고른 프로젝트 범위의 클라이언트로 만든다
    sent = []

    class Scoped:
        def __init__(self, project_id):
            self.project_id = project_id

        def request(self, method, path, json=None):
            sent.append((self.project_id, method, path, json))
            return {"uuid": "t-new", "project_id": self.project_id}
    monkeypatch.setattr(templates, "for_project", Scoped)
    r = client_as(ADMIN).post(P + "/clustertemplates", json={"name": "t", "image_id": "i", "project_id": "p1"})
    assert r.status_code == 201, r.text
    assert sent == [("p1", "POST", "/clustertemplates", {"name": "t", "image_id": "i", "coe": "kubernetes"})]
    r = client_as(ADMIN).post(P + "/clustertemplates", json={"name": "t", "image_id": "i"})
    assert (r.status_code, r.json()["detail"]) == (400, "프로젝트를 선택하세요")
    # 목록 — 관리자 계정이라 Magnum 이 모든 프로젝트 템플릿을 준다. 소유 프로젝트를 함께 돌려준다(신청 화면이 거른다)
    monkeypatch.setattr(magnum, "request", lambda method, path, json=None: {"clustertemplates": [{"uuid": "t1", "project_id": "p1"}]})
    assert client_as(USER).get(P + "/clustertemplates").json()["items"][0]["project_id"] == "p1"


# ── 사용자 확인(MNT-CORE /auth/me) ───────────────────────────────

def _core(monkeypatch, handler):
    calls = []

    def h(request):
        calls.append(request.headers.get("authorization"))
        return handler(request)

    monkeypatch.setattr(auth, "_client", httpx.AsyncClient(transport=httpx.MockTransport(h)))
    return calls


def test_CORE_판정과_캐시(monkeypatch):
    calls = _core(monkeypatch, lambda r: httpx.Response(200, json={"username": "gy.yang", "role": "user",
                                                                   "projectIds": ["p-mine"], "clusterIds": [MINE]}))
    c = TestClient(main.app)
    for _ in range(2):
        assert c.get(P + f"/clusters/{OTHER}", headers={"Authorization": "Bearer t1"}).status_code == 403
    assert calls == ["Bearer t1"]  # 같은 토큰은 캐시(5초)


def test_CORE_거부는_그대로_전달(monkeypatch):
    _core(monkeypatch, lambda r: httpx.Response(401, json={"detail": "다른곳에서 로그인되었습니다.",
                                                           "code": "SESSION_ENDED", "reason": "duplicate"}))
    r = TestClient(main.app).get(P + "/clustertemplates", headers={"Authorization": "Bearer t2"})
    assert r.status_code == 401
    assert r.json() == {"detail": "다른곳에서 로그인되었습니다.", "code": "SESSION_ENDED", "reason": "duplicate"}
    r = TestClient(main.app).delete(P + f"/clusters/{MINE}")
    assert r.status_code == 401 and r.json()["code"] == "TOKEN_REQUIRED"
    assert TestClient(main.app).get("/healthz").status_code == 200  # 상태 확인은 로그인 없이
