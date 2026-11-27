"""서비스 클러스터 K8s API 클라이언트.

Magnum 인증서(CA 조회 + CSR 서명)로 클러스터별 클라이언트 인증서를 만들어
그 클러스터의 K8s REST API 를 직접 호출한다(kubernetes 패키지 불필요 — httpx).
인증서·클라이언트는 클러스터별 캐시(프로세스 수명).
"""
from __future__ import annotations

import tempfile
import ssl
import threading

import httpx

from .openstack_client import for_project, magnum

_lock = threading.Lock()
_clients: dict[str, httpx.Client] = {}


def issue_client_cert(cluster_id: str) -> tuple[str, str, str, str]:
    """(api_address, ca_pem, cert_pem, key_pem) — kubeconfig 조립·K8s 접속 공용."""
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.x509.oid import NameOID

    c = magnum.get(f"/clusters/{cluster_id}")
    api_address = c.get("api_address") or ""
    if not api_address:
        raise RuntimeError("클러스터 API 주소가 아직 없습니다(생성 중이거나 실패).")

    # 인증서는 클러스터 소유 프로젝트 스코프에서만 된다(조회는 admin 스코프로 됨)
    owner = for_project(c["project_id"])
    ca_pem = owner.get(f"/certificates/{cluster_id}")["pem"]

    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    csr = (
        x509.CertificateSigningRequestBuilder()
        .subject_name(x509.Name([
            x509.NameAttribute(NameOID.COMMON_NAME, "admin"),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, "system:masters"),
        ]))
        .sign(key, hashes.SHA256())
    )
    cert_pem = owner.request("POST", "/certificates", json={
        "cluster_uuid": cluster_id,
        "csr": csr.public_bytes(serialization.Encoding.PEM).decode(),
    })["pem"]
    key_pem = key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.TraditionalOpenSSL,
        serialization.NoEncryption(),
    ).decode()
    return api_address, ca_pem, cert_pem, key_pem


def _write_tmp(content: str, suffix: str) -> str:
    f = tempfile.NamedTemporaryFile("w", suffix=suffix, delete=False)
    f.write(content)
    f.close()
    return f.name


def _ssl_context(ca_pem: str, cert_pem: str, key_pem: str) -> ssl.SSLContext:
    """httpx 0.28+ 는 Client(cert=, verify=) 레거시 인자를 무시한다(→ 익명 접속·403).
    TLS 는 SSLContext 로 직접 구성: CA 로 서버 검증 + 클라이언트 인증서 로드."""
    ctx = ssl.create_default_context(cafile=_write_tmp(ca_pem, ".ca.crt"))
    ctx.load_cert_chain(
        certfile=_write_tmp(cert_pem, ".crt"),
        keyfile=_write_tmp(key_pem, ".key"),
    )
    return ctx


def k8s(cluster_id: str) -> httpx.Client:
    """클러스터별 K8s REST 클라이언트(캐시). 인증서 만료·회전 시 프로세스 재시작으로 갱신."""
    cli = _clients.get(cluster_id)
    if cli is not None:
        return cli
    with _lock:
        cli = _clients.get(cluster_id)
        if cli is not None:
            return cli
        api_address, ca_pem, cert_pem, key_pem = issue_client_cert(cluster_id)
        cli = httpx.Client(
            base_url=api_address,
            verify=_ssl_context(ca_pem, cert_pem, key_pem),
            timeout=20,
        )
        _clients[cluster_id] = cli
        return cli


def k8s_get(cluster_id: str, path: str) -> dict:
    r = k8s(cluster_id).get(path)
    r.raise_for_status()
    return r.json()


def k8s_get_text(cluster_id: str, path: str) -> str:
    """텍스트 응답(파드 로그 등) — JSON 이 아니라 평문."""
    r = k8s(cluster_id).get(path)
    r.raise_for_status()
    return r.text


def k8s_request(cluster_id: str, method: str, path: str, json: dict | None = None,
                content: bytes | None = None, content_type: str | None = None) -> dict:
    """쓰기 공용 — POST/PUT/PATCH/DELETE. content_type 지정 시 그 헤더로(패치의 strategic-merge 등)."""
    headers = {"Content-Type": content_type} if content_type else None
    r = k8s(cluster_id).request(method, path, json=json, content=content, headers=headers)
    r.raise_for_status()
    return r.json() if r.content else {}
