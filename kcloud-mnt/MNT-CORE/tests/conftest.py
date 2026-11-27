"""시험 환경 — 일회용 PostgreSQL(tests/run_tests.sh 가 띄움) + 직접 만든 RSA 키로 서명한 토큰.

공용 개발 DB 를 건드리지 않도록 TEST_DATABASE_URL 이 시험용(localhost:55432/okc_test)이 아니면 실행하지 않는다.
Keycloak 은 부르지 않는다 — 서명 키(JWKS) 조회와 Keycloak 세션 삭제를 가짜로 바꾼다.
"""
import os
import time
import uuid

import pytest

_URL = os.environ.get("TEST_DATABASE_URL", "")
if "@localhost:55432/okc_test" not in _URL:
    pytest.exit("TEST_DATABASE_URL 이 시험용 DB(localhost:55432/okc_test)가 아닙니다 — tests/run_tests.sh 로 실행하세요",
                returncode=2)

ISSUER = "http://kc.test/realms/okc"
os.environ.update(
    DATABASE_URL=_URL, KEYCLOAK_ISSUER=ISSUER, KEYCLOAK_JWKS_URL="http://kc.test/jwks",
    KEYCLOAK_AUDIENCE="mnt-core-client", KEYCLOAK_APP_CLIENT_ID="mnt-app-client",
    SESSION_SINGLE_LOGIN="true", TEMPORAL_API_BASE="", IAAS_API_BASE="", CORS_ORIGINS="",
)

import jwt  # noqa: E402 — 환경변수를 먼저 정한 뒤 앱을 불러온다
from cryptography.hazmat.primitives.asymmetric import rsa  # noqa: E402
from fastapi.testclient import TestClient  # noqa: E402
from sqlalchemy import text  # noqa: E402

from app import auth  # noqa: E402
from app.db import engine  # noqa: E402
from app.main import app  # noqa: E402

KEY = rsa.generate_private_key(public_exponent=65537, key_size=2048)
OTHER_KEY = rsa.generate_private_key(public_exponent=65537, key_size=2048)
PROJECT = "4ee114f1b9f1423a91bf40dec2fd05fb"  # migration_v0.2 의 okc_test_prj(조직 할당됨)


def make_token(username="ek.lee", sid=None, *, sub=None, aud="mnt-core-client", iss=ISSUER, exp_in=300,
               key=KEY, **extra) -> str:
    now = int(time.time())
    claims = {"iss": iss, "aud": aud, "sub": sub or f"sub-{username}", "preferred_username": username,
              "sid": sid or uuid.uuid4().hex, "iat": now, "exp": now + exp_in, "typ": "Bearer", **extra}
    return jwt.encode(claims, key, algorithm="RS256")


def bearer(token: str) -> dict:
    return {"Authorization": f"Bearer {token}"}


class _StubJwks:
    def get_signing_key_from_jwt(self, _token):
        return type("Key", (), {"key": KEY.public_key()})()


@pytest.fixture(autouse=True)
def keycloak_ended(monkeypatch):
    """서명 키 조회를 가짜로 바꾸고, Keycloak 세션 삭제 호출(sid)을 모은다."""
    monkeypatch.setattr(auth, "_jwks_client", lambda: _StubJwks())
    calls: list[str] = []
    monkeypatch.setattr(auth, "end_keycloak_session", calls.append)
    return calls


@pytest.fixture(autouse=True)
def _clean_db():
    yield
    with engine.begin() as c:
        c.execute(text("DELETE FROM iam_user_session"))
        c.execute(text("DELETE FROM iam_ssh_key"))
        c.execute(text("DELETE FROM tkt_request WHERE id LIKE 'req-990101-%'"))
        c.execute(text("DELETE FROM iam_project_member"))
        c.execute(text("DELETE FROM paas_project_cluster"))
        c.execute(text("DELETE FROM iam_user WHERE username LIKE 'test-%'"))
        c.execute(text("UPDATE iam_user SET keycloak_sub = NULL"))


@pytest.fixture
def client():
    return TestClient(app)


def sql(stmt: str, **params):
    with engine.begin() as c:
        return c.execute(text(stmt), params)


def add_member(username: str, project_id: str = PROJECT, role: str = "member"):
    sql("""INSERT INTO iam_project_member (project_id, user_id, role)
           SELECT :p, id, :r FROM iam_user WHERE username = :u""", p=project_id, r=role, u=username)
