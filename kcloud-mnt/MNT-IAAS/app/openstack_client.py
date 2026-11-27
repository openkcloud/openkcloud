"""openstacksdk 연결 — 설정(env)으로 OpenStack Connection을 만든다.

지금은 서비스 계정(예: extadmin) 하나로 대표 호출(관리자 스코프).
연결은 토큰 재사용을 위해 프로세스 1회 생성 후 캐시한다.
"""
import functools

import openstack
from keystoneauth1 import session as ks_session
from openstack.config import loader as _loader  # noqa: F401 (사이드이펙트 없음, 버전 호환 확인용)

from .config import settings

# 시간 제한을 따로 주지 않은 요청(토큰 발급·버전 확인)의 최대 대기(초). 없으면 Keystone 이 요청을 받고 응답하지 않을 때
# 토큰 잠금을 쥔 채 끝없이 기다려, 같은 연결을 쓰는 요청(IAAS 워커 슬롯 포함)이 모두 멈춘다. API 호출은 각자 timeout 을 준다
DEFAULT_TIMEOUT = 5
# 재시도 횟수를 따로 주지 않은 요청(토큰 발급)은 연결 오류 시 1회 재시도. 토큰은 약 1시간마다 다시 받아 Keystone 연결이
# 그동안 쉬므로 중간 장비가 끊어 놓았을 수 있다. 토큰 발급은 자원을 만들지 않아 다시 보내도 안전.
# 생성·변경은 이 재시도가 붙지 않게 os_request 로 보낸다(새 연결, 재시도 없음)
_SESSION = functools.partial(ks_session.Session, connect_retries=1)

_conn: "openstack.connection.Connection | None" = None
_proj_conns: "dict[str, openstack.connection.Connection]" = {}


def get_conn() -> "openstack.connection.Connection":
    global _conn
    if _conn is None:
        _conn = openstack.connection.Connection(
            auth_url=settings.os_auth_url,
            username=settings.os_username,
            password=settings.os_password,
            project_name=settings.os_project_name,
            user_domain_name=settings.os_user_domain_name,
            project_domain_name=settings.os_project_domain_name,
            region_name=settings.os_region_name or None,
            identity_api_version="3",
            auth_type="v3password",  # v3 전용 — 버전 확인 요청 없이 바로 토큰을 받는다
            verify=not settings.os_insecure,
            api_timeout=DEFAULT_TIMEOUT,
        )
        _conn.config.set_session_constructor(_SESSION)
    return _conn


def get_conn_for_project(project_id: str) -> "openstack.connection.Connection":
    """특정 프로젝트로 스코프된 연결(캐시). Cinder·Nova 처럼 '토큰 스코프 프로젝트'에만
    생성되는 서비스에서, 서비스 계정(extadmin)을 그 프로젝트로 스코프해 자원을 만든다.
    전제: extadmin 이 해당 도메인에 inherited role 보유(모든 프로젝트 스코프 가능)."""
    conn = _proj_conns.get(project_id)
    if conn is None:
        conn = openstack.connection.Connection(
            auth_url=settings.os_auth_url,
            username=settings.os_username,
            password=settings.os_password,
            project_id=project_id,  # project_name 대신 id 로 스코프
            user_domain_name=settings.os_user_domain_name,
            region_name=settings.os_region_name or None,
            identity_api_version="3",
            auth_type="v3password",
            verify=not settings.os_insecure,
            api_timeout=DEFAULT_TIMEOUT,
        )
        conn.config.set_session_constructor(_SESSION)
        _proj_conns[project_id] = conn
    return conn


def os_request(conn, url: str, method: str, **kwargs):
    """OpenStack API 요청 — 조회(GET)는 연결을 재사용하고 연결 오류 시 1회 재시도,
    생성·변경은 매번 새 연결로 보낸다(인증 토큰은 conn 과 공유).
    오래 쉬던 연결은 서버·NAT 가 이미 끊었을 수 있는데, 그 연결로 보낸 생성 요청은
    'Remote end closed connection without response' 로 실패하고, 다시 보내면 중복 생성 위험이 있어 재시도할 수 없다.
    새 연결로 보내면 이 경우가 생기지 않는다(추가 비용은 TLS 연결 1회)."""
    if method == "GET":
        return conn.session.request(url, method, connect_retries=1, **kwargs)
    fresh = ks_session.Session(auth=conn.session.auth, verify=conn.session.verify, timeout=conn.session.timeout)
    try:
        return fresh.request(url, method, connect_retries=0, **kwargs)
    finally:
        fresh.session.close()
