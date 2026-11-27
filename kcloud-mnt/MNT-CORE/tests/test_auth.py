"""토큰 검증 · 사용자 확인 · 세션(로그아웃·관리자 강제·동시 로그인) · 역할별 권한."""
import time
import uuid

import jwt
from conftest import ISSUER, KEY, OTHER_KEY, PROJECT, add_member, bearer, make_token, sql

from app import auth
from app.config import settings
from app.routers import tickets


# ── 토큰 ──────────────────────────────────────────────────────────

def test_토큰_없음(client):
    r = client.get("/api/core/menus")
    assert r.status_code == 401 and r.json()["code"] == "TOKEN_REQUIRED"
    assert r.headers["www-authenticate"] == "Bearer"


def test_서명_발급자_대상이_다르면_거부(client):
    for t in (make_token(key=OTHER_KEY), make_token(iss="http://evil/realms/okc"), make_token(aud="account")):
        r = client.get("/api/core/menus", headers=bearer(t))
        assert r.status_code == 401 and r.json()["code"] == "TOKEN_INVALID"


def test_만료된_토큰(client):
    r = client.get("/api/core/menus", headers=bearer(make_token(exp_in=-10)))
    assert r.status_code == 401 and r.json()["code"] == "TOKEN_EXPIRED"


def test_발급_시각이_조금_미래여도_통과(client):
    # Keycloak 시계가 몇 초 빠르면 막 받은 토큰의 iat 가 이 서버 시각보다 뒤다
    t = make_token(iat=int(time.time()) + 5)
    assert client.get("/api/core/menus", headers=bearer(t)).status_code == 200


# ── 사용자 ────────────────────────────────────────────────────────

def test_포털에_없는_사용자(client):
    r = client.get("/api/core/menus", headers=bearer(make_token("kc-test")))
    assert r.status_code == 403
    assert r.json() == {"detail": "포털에 등록되지 않은 계정입니다. 관리자에게 문의하세요.", "code": "USER_NOT_REGISTERED"}


def test_비활성_역할없음_사용자(client):
    sql("INSERT INTO iam_user (username, name, status) VALUES ('test-off', '꺼짐', 'disabled'), ('test-norole', '역할없음', 'active')")
    assert client.get("/api/core/menus", headers=bearer(make_token("test-off"))).json()["code"] == "USER_INACTIVE"
    assert client.get("/api/core/menus", headers=bearer(make_token("test-norole"))).json()["code"] == "NO_ROLE"


def test_sub_저장_후_다른_sub_거부(client):
    assert client.get("/api/core/menus", headers=bearer(make_token(sub="kc-1"))).status_code == 200
    assert sql("SELECT keycloak_sub FROM iam_user WHERE username = 'ek.lee'").scalar() == "kc-1"
    r = client.get("/api/core/menus", headers=bearer(make_token(sub="kc-2")))
    assert r.status_code == 403 and r.json()["code"] == "USER_MISMATCH"


def test_내_정보(client):
    add_member("ek.lee", role="viewer")
    sql("""INSERT INTO paas_project_cluster (project_id, cluster_id, namespace)
           VALUES (:p, 'c-mine', NULL), (:p, 'c-mine', 'ns-a'), ('other-project', 'c-other', NULL)""", p=PROJECT)
    me = client.get("/api/core/auth/me", headers=bearer(make_token())).json()
    assert (me["username"], me["role"], me["projectIds"], me["clusterIds"]) == ("ek.lee", "user", [PROJECT], ["c-mine"])
    assert client.get("/api/core/auth/me", headers=bearer(make_token("admin"))).json()["role"] == "admin"


# ── 역할별 권한 ───────────────────────────────────────────────────

def test_메뉴는_토큰_역할로(client):
    r = client.get("/api/core/menus?role=admin", headers=bearer(make_token()))  # role 파라미터는 무시
    assert r.status_code == 200 and r.json()["role"] == "user"


def test_메뉴_관리는_관리자만(client):
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    assert client.get("/api/core/menus/all", headers=user).status_code == 403
    assert client.post("/api/core/menus", json={"code": "test.menu", "label": "x"}, headers=user).status_code == 403
    assert client.patch("/api/core/menus/1", json={"label": "x"}, headers=user).status_code == 403
    assert client.delete("/api/core/menus/1", headers=user).status_code == 403
    assert client.post("/api/core/menus/reorder", json=[], headers=user).status_code == 403
    assert client.get("/api/core/menus/all", headers=admin).status_code == 200
    mid = client.post("/api/core/menus", json={"code": "test.menu", "label": "x"}, headers=admin).json()["id"]
    assert client.delete(f"/api/core/menus/{mid}", headers=admin).status_code == 200


def test_멤버_관리는_관리자만(client):
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    uid = sql("SELECT id FROM iam_user WHERE username = 'jw.lee'").scalar()
    body = {"userId": uid, "role": "member"}
    r = client.post(f"/api/core/projects/{PROJECT}/members", json=body, headers=user)
    assert r.status_code == 403 and r.json()["code"] == "FORBIDDEN"
    assert client.post(f"/api/core/projects/{PROJECT}/members", json=body, headers=admin).status_code == 201
    assert client.delete(f"/api/core/projects/{PROJECT}/members/{uid}", headers=user).status_code == 403
    for path in ("/api/core/users", "/api/core/orgs"):
        assert client.get(path, headers=user).status_code == 403
        assert client.get(path, headers=admin).status_code == 200


def test_멤버_목록은_소속일_때만(client):
    user = bearer(make_token())
    assert client.get(f"/api/core/projects/{PROJECT}/members", headers=user).status_code == 403
    add_member("ek.lee")
    assert client.get(f"/api/core/projects/{PROJECT}/members", headers=user).status_code == 200


def test_키페어_소유자는_토큰_사용자(client):
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test"
    r = client.post("/api/core/ssh-keys", json={"owner": "admin", "name": "k1", "publicKey": key}, headers=user)
    assert r.status_code == 201
    assert [k["name"] for k in client.get("/api/core/ssh-keys?owner=admin", headers=user).json()["items"]] == ["k1"]
    assert client.get("/api/core/ssh-keys", headers=admin).json()["items"] == []
    assert client.delete(f"/api/core/ssh-keys/{r.json()['id']}?owner=ek.lee", headers=admin).status_code == 404


def test_티켓_목록_사용자는_본인것만(client):
    sql("""INSERT INTO tkt_request (id, project_id, requested_by, request_spec)
           SELECT CASE username WHEN 'admin' THEN 'req-990101-001' ELSE 'req-990101-002' END, :p, id, '{}'::jsonb
             FROM iam_user WHERE username IN ('ek.lee', 'admin')""", p=PROJECT)  # 990101 = 시험용 날짜
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    mine = client.get("/api/core/tickets?requestedBy=admin", headers=user).json()["items"]
    assert [t["requestedBy"] for t in mine] == ["ek.lee"]
    assert {t["requestedBy"] for t in client.get("/api/core/tickets", headers=admin).json()["items"]} == {"ek.lee", "admin"}
    assert client.get("/api/core/tickets/req-990101-001/steps", headers=user).status_code == 404
    assert client.get("/api/core/tickets/req-990101-002/steps", headers=user).status_code == 200


def test_목록의_신청자_조직은_신청자_본인_조직(client):
    sql("""INSERT INTO tkt_request (id, project_id, requested_by, request_spec)
           SELECT 'req-990101-003', :p, id, '{}'::jsonb FROM iam_user WHERE username = 'admin'""", p=PROJECT)
    t = client.get("/api/core/tickets?requestedBy=admin", headers=bearer(make_token("admin"))).json()["items"][0]
    assert (t["requesterOrgName"], t["orgName"]) == ("ETRI", "오케스트로")  # 신청자(admin) 조직 ≠ 프로젝트 조직


def test_처리_기록에_처리자_조직(client):
    # 승인 = admin(ETRI), 프로비저닝 = ek.lee(오케스트로) — 처리자마다 본인 조직
    sql("""INSERT INTO tkt_request (id, project_id, requested_by, request_spec, approved_by, approved_at,
                                    provisioning_by, provisioning_at)
           SELECT 'req-990101-004', :p, a.id, '{}'::jsonb, a.id, now(), e.id, now()
             FROM iam_user a, iam_user e WHERE a.username = 'admin' AND e.username = 'ek.lee'""", p=PROJECT)
    t = client.get("/api/core/tickets?requestedBy=admin", headers=bearer(make_token("admin"))).json()["items"][0]
    assert (t["approvedByOrgName"], t["provisioningByOrgName"], t["finishedByOrgName"]) == ("ETRI", "오케스트로", None)


def test_승인은_관리자만(client):
    r = client.patch("/api/core/tickets/req-x", json={"action": "approve", "processedBy": "admin"},
                     headers=bearer(make_token()))
    assert r.status_code == 403 and r.json()["detail"] == "승인·반려는 관리자만 할 수 있습니다"


def test_관리자는_멤버가_아니어도_모든_조직_프로젝트에_신청(client, monkeypatch):
    monkeypatch.setattr(tickets, "_iaas", lambda path, token, **kw: {"items": [{"id": PROJECT, "name": "okc_test_prj"}]})
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    assert client.get("/api/core/tickets/projects", headers=user).json()["items"] == []
    assert [p["id"] for p in client.get("/api/core/tickets/projects", headers=admin).json()["items"]] == [PROJECT]
    # 멤버 확인을 통과하면 다음 확인(희망 네트워크)에서 400
    body = {"projectId": PROJECT, "vmName": "vm-a", "flavorId": "f", "imageId": "i", "networkType": "bad"}
    assert client.post("/api/core/tickets", json=body, headers=user).status_code == 403
    assert client.post("/api/core/tickets", json=body, headers=admin).status_code == 400
    # 조직이 할당되지 않은 프로젝트는 관리자도 불가
    assert client.post("/api/core/tickets", json={**body, "projectId": "no-org"}, headers=admin).status_code == 403


def test_AI_워크스페이스_신청자는_토큰_사용자(client, monkeypatch):
    monkeypatch.setattr(tickets, "_iaas", lambda path, token: {"items": [{"id": PROJECT, "name": "okc_test_prj"}]})
    monkeypatch.setattr(tickets, "_ainb", lambda path, token=None: {"available": True})
    user, admin = bearer(make_token()), bearer(make_token("admin"))
    body = {"requestedBy": "admin", "projectId": PROJECT}  # requestedBy 는 무시된다
    assert client.post("/api/core/tickets/ai-workspace", json=body, headers=user).status_code == 403  # 멤버 아님
    r = client.post("/api/core/tickets/ai-workspace", json={**body, "requestedBy": "ek.lee"}, headers=admin)
    try:
        assert r.status_code == 201 and r.json()["requestedBy"] == "admin"  # 관리자는 멤버가 아니어도 신청
    finally:
        sql("DELETE FROM tkt_request WHERE type = 'ai-workspace' AND project_id = :p", p=PROJECT)


def test_디바이스_프로파일이_없는_스펙은_신청_불가(client, monkeypatch):
    flavor = {"id": "f", "name": "m1.large.2rngd", "isPublic": True, "accelerator": "dp-2rngd", "deviceProfileMissing": True}
    fake = {"/projects": {"items": [{"id": PROJECT, "name": "okc_test_prj"}]},
            f"/projects/{PROJECT}/flavors": {"items": [flavor]}}
    monkeypatch.setattr(tickets, "_iaas", lambda path, token: fake[path])
    body = {"projectId": PROJECT, "vmName": "vm-a", "flavorId": "f", "imageId": "i", "networkType": "internal"}
    r = client.post("/api/core/tickets", json=body, headers=bearer(make_token("admin")))
    assert r.status_code == 400 and r.json()["detail"] == "연결된 디바이스 프로파일이 존재하지 않습니다"


def test_IAAS_호출에_사용자_토큰_전달(client, monkeypatch):
    add_member("ek.lee")
    seen = []
    monkeypatch.setattr(tickets, "_iaas", lambda path, token, **kw: seen.append(token) or {"items": [{"id": PROJECT, "name": "p"}]})
    t = make_token()
    assert client.get("/api/core/tickets/projects?username=admin", headers=bearer(t)).json()["items"][0]["id"] == PROJECT
    assert seen == [t]


def test_프로젝트_목록은_IAAS_를_5초_제한으로_조회(client, monkeypatch):
    seen = []
    monkeypatch.setattr(tickets, "_iaas", lambda path, token, **kw: seen.append((path, kw)) or {"items": []})
    assert client.get("/api/core/tickets/projects", headers=bearer(make_token("admin"))).status_code == 200
    assert seen == [("/projects", {"timeout": 5})]  # 화면이 5초씩 다시 시도한다 — 서버도 5초 넘게 기다리지 않게


# ── 세션 ──────────────────────────────────────────────────────────

def test_다른곳에서_로그인하면_이전_세션_종료(client, keycloak_ended):
    a, b = make_token(sid="sid-a"), make_token(sid="sid-b")
    assert client.get("/api/core/menus", headers=bearer(a)).status_code == 200
    assert client.get("/api/core/menus", headers=bearer(b)).status_code == 200
    assert keycloak_ended == ["sid-a"]
    r = client.get("/api/core/menus", headers=bearer(a))
    assert r.status_code == 401
    assert r.json() == {"detail": "다른곳에서 로그인되었습니다.", "code": "SESSION_ENDED", "reason": "duplicate"}


def test_동시_로그인_허용이면_둘_다_유지(client, keycloak_ended, monkeypatch):
    monkeypatch.setattr(settings, "session_single_login", False)
    a, b = make_token(sid="sid-a"), make_token(sid="sid-b")
    for t in (a, b, a):
        assert client.get("/api/core/menus", headers=bearer(t)).status_code == 200
    assert keycloak_ended == []


def test_로그아웃_Keycloak_세션은_화면이_끝냄(client, keycloak_ended):
    # 관리 API 로 먼저 지우면 화면의 Keycloak 로그아웃이 세션을 못 찾아 LOGOUT_ERROR 로 남는다
    t = make_token(sid="sid-out")
    assert client.get("/api/core/menus", headers=bearer(t)).status_code == 200
    assert client.post("/api/core/auth/logout", headers=bearer(t)).status_code == 204
    assert keycloak_ended == []
    r = client.get("/api/core/menus", headers=bearer(t))
    assert r.status_code == 401
    assert (r.json()["reason"], r.json()["detail"]) == ("logout", "로그아웃 되었습니다.")


def test_미사용_시간_초과(client, keycloak_ended):
    t = make_token(sid="sid-idle")
    client.get("/api/core/menus", headers=bearer(t))
    assert client.post("/api/core/auth/logout", json={"reason": "idle"}, headers=bearer(t)).status_code == 204
    assert keycloak_ended == []  # 화면이 끝낸다
    r = client.get("/api/core/menus", headers=bearer(t))
    assert (r.json()["reason"], r.json()["detail"]) == ("idle", "오랫동안 사용하지 않아 로그아웃 되었습니다.")
    other = make_token(sid="sid-x")
    assert client.post("/api/core/auth/logout", json={"reason": "admin"}, headers=bearer(other)).status_code == 400


def test_새_세션이면_마지막_로그인_시각(client):
    sql("UPDATE iam_user SET last_login_at = NULL WHERE username = 'ek.lee'")
    client.get("/api/core/menus", headers=bearer(make_token(sid="sid-ll")))
    assert sql("SELECT last_login_at IS NOT NULL FROM iam_user WHERE username = 'ek.lee'").scalar()


def test_옛_DB_비밀번호_로그인은_없음(client):
    assert client.post("/api/core/auth/login", json={"username": "admin", "password": "admin"}).status_code in (404, 405)


def _logout_token(sid, sub="sub-ek.lee", **extra):
    claims = {"iss": ISSUER, "aud": "mnt-app-client", "sub": sub, "sid": sid, "iat": int(time.time()),
              "jti": uuid.uuid4().hex, "events": {auth._BACKCHANNEL_EVENT: {}}, **extra}
    return jwt.encode(claims, KEY, algorithm="RS256")


def test_관리자_강제_로그아웃_통보(client):
    t = make_token(sid="sid-adm")
    assert client.get("/api/core/menus", headers=bearer(t)).status_code == 200
    r = client.post("/api/core/auth/backchannel-logout", data={"logout_token": _logout_token("sid-adm")})
    assert r.status_code == 200
    r = client.get("/api/core/menus", headers=bearer(t))
    assert r.status_code == 401
    assert (r.json()["reason"], r.json()["detail"]) == ("admin", "관리자에 의해 로그아웃되었습니다.")


def test_아직_못본_세션의_강제_로그아웃도_반영(client):
    assert client.get("/api/core/menus", headers=bearer(make_token(sid="sid-1"))).status_code == 200  # sub 저장
    client.post("/api/core/auth/backchannel-logout", data={"logout_token": _logout_token("sid-2")})
    assert client.get("/api/core/menus", headers=bearer(make_token(sid="sid-2"))).json()["reason"] == "admin"


def test_잘못된_로그아웃_통보는_400(client):
    bad = [_logout_token("s", events={}), _logout_token("s", nonce="n"), make_token(sid="s")]  # 이벤트 없음·nonce·접근 토큰
    for t in bad:
        assert client.post("/api/core/auth/backchannel-logout", data={"logout_token": t}).status_code == 400


def test_세션_상태는_만료된_토큰으로도_확인(client):
    t = make_token(sid="sid-st")
    client.get("/api/core/menus", headers=bearer(t))
    old = make_token(sid="sid-st", exp_in=-600)
    assert client.get("/api/core/auth/session", headers=bearer(old)).json() == {"active": True}
    client.post("/api/core/auth/logout", headers=bearer(old))
    assert client.get("/api/core/auth/session", headers=bearer(old)).json()["reason"] == "logout"


def test_세션_상태_확인은_접근_로그에서_제외():
    import logging
    from app.main import SkipSessionPoll

    def rec(path):
        return logging.LogRecord("uvicorn.access", logging.INFO, "", 0, '%s - "%s %s HTTP/%s" %d', ("127.0.0.1:1", "GET", path, "1.1", 200), None)

    f = SkipSessionPoll()
    assert not f.filter(rec("/api/core/auth/session"))
    assert f.filter(rec("/api/core/auth/me")) and f.filter(rec("/api/core/menus"))
