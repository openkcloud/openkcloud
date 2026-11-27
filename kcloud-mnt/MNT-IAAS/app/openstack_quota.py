"""프로젝트 쿼터·사용량 조회 — Nova limits + Cinder os-quota-sets 를 합친다.

관리자 토큰으로 **임의 프로젝트**를 조회해야 하므로:
  - Nova  : GET /v2.1/limits?tenant_id={pid}     (absolute 에 쿼터+사용량)
  - Cinder: GET /v3/{admin_pid}/os-quota-sets/{pid}?usage=true
            (/v3/{pid}/limits 는 토큰 스코프 프로젝트만 가능 → 400. quota-sets 는 admin 이 임의 조회)

환경 독립: os_endpoint_mode 로 분기(코드 동일, 설정만 교체).
  - override : 진입 URL(ingress)로 직접. 버전·프로젝트 경로를 우리가 구성.
  - catalog  : 카탈로그 자동발견(파드). cinder 엔드포인트는 이미 /v3/{auth_proj} 스코프라
               상대경로 /os-quota-sets/{pid} 만 붙인다.
"""
from fastapi import HTTPException

from .config import settings
from .openstack_client import get_conn, os_request


def get_project_quota(pid: str) -> dict:
    conn = get_conn()
    try:
        admin_pid = conn.session.get_project_id()
        if settings.os_endpoint_mode == "catalog":
            nova = os_request(
                conn, f"/limits?tenant_id={pid}", "GET",
                endpoint_filter={"service_type": "compute", "interface": settings.os_interface},
                timeout=15,
            ).json()["limits"]["absolute"]
            cset = os_request(
                conn, f"/os-quota-sets/{pid}?usage=true", "GET",
                endpoint_filter={"service_type": "block-storage", "interface": settings.os_interface},
                timeout=15,
            ).json()["quota_set"]
        else:  # override
            nova_base = settings.os_endpoint_template.format(service="nova")
            cinder_base = settings.os_endpoint_template.format(service="cinder")
            nr = os_request(conn, f"{nova_base}/v2.1/limits?tenant_id={pid}", "GET", timeout=15)
            nr.raise_for_status()
            nova = nr.json()["limits"]["absolute"]
            cr = os_request(
                conn, f"{cinder_base}/v3/{admin_pid}/os-quota-sets/{pid}?usage=true", "GET", timeout=15
            )
            cr.raise_for_status()
            cset = cr.json()["quota_set"]
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"쿼터 조회 실패(pid={pid}): {type(e).__name__}: {e}")

    def cs(key: str) -> tuple[int, int]:
        v = cset.get(key, {})
        return (v.get("limit", 0), v.get("in_use", 0)) if isinstance(v, dict) else (v, 0)

    disk_q, disk_u = cs("gigabytes")
    vol_q, vol_u = cs("volumes")
    return {
        "vcpu": {"quota": nova.get("maxTotalCores", 0), "used": nova.get("totalCoresUsed", 0)},
        "ramGb": {"quota": nova.get("maxTotalRAMSize", 0) // 1024, "used": nova.get("totalRAMUsed", 0) // 1024},
        "instances": {"quota": nova.get("maxTotalInstances", 0), "used": nova.get("totalInstancesUsed", 0)},
        "diskGb": {"quota": disk_q, "used": disk_u},
        "volumes": {"quota": vol_q, "used": vol_u},
    }


def set_project_quota(pid: str, *, vcpu: int, ram_gb: int, disk_gb: int) -> None:
    """프로젝트 Nova/Cinder 쿼터 설정(관리자). 가속기는 OpenStack에 쿼터 없음 → 미설정."""
    conn = get_conn()
    nova_body = {"quota_set": {"cores": vcpu, "ram": ram_gb * 1024}}
    cinder_body = {"quota_set": {"gigabytes": disk_gb}}
    try:
        admin_pid = conn.session.get_project_id()
        if settings.os_endpoint_mode == "catalog":
            os_request(
                conn, f"/os-quota-sets/{pid}", "PUT", json=nova_body,
                endpoint_filter={"service_type": "compute", "interface": settings.os_interface}, timeout=15,
            ).raise_for_status()
            os_request(
                conn, f"/os-quota-sets/{pid}", "PUT", json=cinder_body,
                endpoint_filter={"service_type": "block-storage", "interface": settings.os_interface}, timeout=15,
            ).raise_for_status()
        else:  # override
            nova_base = settings.os_endpoint_template.format(service="nova")
            cinder_base = settings.os_endpoint_template.format(service="cinder")
            os_request(
                conn, f"{nova_base}/v2.1/os-quota-sets/{pid}", "PUT", json=nova_body, timeout=15
            ).raise_for_status()
            os_request(
                conn, f"{cinder_base}/v3/{admin_pid}/os-quota-sets/{pid}", "PUT", json=cinder_body, timeout=15
            ).raise_for_status()
    except Exception as e:  # noqa: BLE001
        raise HTTPException(502, f"쿼터 설정 실패(pid={pid}): {type(e).__name__}: {e}")
