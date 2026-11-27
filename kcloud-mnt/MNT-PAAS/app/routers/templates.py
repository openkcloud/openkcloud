"""클러스터 템플릿(Magnum ClusterTemplate) — 조회 + 관리자 CRUD.

클러스터 생성의 설계도(이미지·플레이버·네트워크·드라이버·라벨 등). Horizon 의
Cluster Templates 화면과 동일 범위. 조회는 로그인 사용자(클러스터 신청 화면), 생성·수정·삭제는 관리자.
템플릿은 고른 프로젝트(project_id) 소유로 만든다 — Horizon 처럼 그 프로젝트 + 공유 자원으로 구성.
조회·수정·삭제는 관리자 계정이라 Magnum 이 모든 프로젝트 템플릿에 대해 처리한다(*_all_projects 정책).
"""
from __future__ import annotations

import re

import httpx
from fastapi import APIRouter, Body, Depends, HTTPException

from ..auth import require_admin
from ..openstack_client import for_project, magnum

router = APIRouter(tags=["cluster-templates"])


def _err(e: Exception) -> HTTPException:  # noqa: ANN001
    if isinstance(e, httpx.HTTPStatusError):
        try:
            d = e.response.json()
            msg = (d.get("errors") or [{}])[0].get("detail") or d.get("title") or e.response.text
        except Exception:  # noqa: BLE001
            msg = e.response.text
        return HTTPException(status_code=e.response.status_code, detail=msg)
    return HTTPException(status_code=502, detail=f"magnum api error: {type(e).__name__}: {e}")


# 목록·상세에 쓰는 필드(Horizon 표시 항목)
_FIELDS = [
    "name", "coe", "image_id", "flavor_id", "master_flavor_id", "keypair_id",
    "external_network_id", "fixed_network", "fixed_subnet", "dns_nameserver",
    "network_driver", "volume_driver", "docker_storage_driver", "docker_volume_size",
    "cluster_distro", "server_type", "public", "hidden", "tls_disabled",
    "floating_ip_enabled", "master_lb_enabled", "registry_enabled", "labels",
    "project_id",  # 소유 프로젝트 — 신청 화면은 공개 또는 신청 프로젝트 소유만 보인다
]
# 생성·수정에서 사용자가 지정 가능한 필드(그 외는 Magnum 기본값)
_WRITABLE = [
    "name", "coe", "image_id", "flavor_id", "master_flavor_id", "keypair_id",
    "external_network_id", "fixed_network", "fixed_subnet", "dns_nameserver",
    "network_driver", "volume_driver", "docker_storage_driver", "docker_volume_size",
    "server_type", "public", "hidden", "tls_disabled",
    "floating_ip_enabled", "master_lb_enabled", "labels",
]


_kube_versions: dict[str, str] = {}  # 이미지 ID·이름 → K8s 버전(이미지 속성은 거의 안 바뀐다 — 프로세스 수명 캐시)
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def _kube_version(image_id: str | None) -> str:
    """템플릿 이미지의 Glance 속성 kube_version — 값 형식이 섞여 있어('1.36.2', 'v1.34.8') 앞의 v 를 뗀다.
    템플릿 image_id 에는 이미지 이름이 들어 있기도 하다(Magnum 은 이름도 받는다). 조회 실패면 빈 값(다음 조회 때 다시 본다)."""
    if not image_id:
        return ""
    if image_id not in _kube_versions:
        try:
            if _UUID.match(image_id):
                img = magnum.service_get("glance", "image", "/v2", f"/images/{image_id}")
            else:
                found = magnum.service_get("glance", "image", "/v2", "/images", {"name": image_id}).get("images", [])
                img = found[0] if found else {}
        except Exception:  # noqa: BLE001 — 버전은 부가 정보
            return ""
        _kube_versions[image_id] = str(img.get("kube_version") or "").lstrip("v")
    return _kube_versions[image_id]


def _summary(t: dict) -> dict:
    out = {"id": t["uuid"], "createdAt": t.get("created_at") or "", "updatedAt": t.get("updated_at") or "",
           "kubeVersion": _kube_version(t.get("image_id"))}
    for f in _FIELDS:
        out[f] = t.get(f)
    return out


@router.get("/clustertemplates")
def list_templates():
    try:
        items = magnum.get("/clustertemplates").get("clustertemplates", [])
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    return {"items": [_summary(t) for t in items]}


@router.get("/clustertemplates/{template_id}")
def get_template(template_id: str):
    try:
        return _summary(magnum.get(f"/clustertemplates/{template_id}"))
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.post("/clustertemplates", status_code=201, dependencies=[Depends(require_admin)])
def create_template(body: dict = Body(...)):
    """템플릿 생성. 빈 값은 빼서 Magnum 기본값에 맡긴다. coe 기본=kubernetes.
    소유 프로젝트(project_id, 필수) — Magnum 은 요청 토큰의 프로젝트를 소유로 두므로 그 프로젝트 범위로 보낸다."""
    project_id = body.get("project_id")
    if not project_id:
        raise HTTPException(status_code=400, detail="프로젝트를 선택하세요")
    payload = {k: v for k, v in body.items() if k in _WRITABLE and v not in (None, "", {})}
    payload.setdefault("coe", "kubernetes")
    try:
        return for_project(project_id).request("POST", "/clustertemplates", json=payload)
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.patch("/clustertemplates/{template_id}", dependencies=[Depends(require_admin)])
def update_template(template_id: str, body: dict = Body(...)):
    """수정 — Magnum 은 JSON Patch(RFC 6902) 를 받는다.
    - 일반 필드: replace. 빈 값(None·""·{}·[])은 제외('operations needs value' 방지).
    - labels(dict): Magnum 이 /labels 에 dict 통째 replace 를 안 받으므로(값 타입은 str/int)
      키 단위('/labels/<키>')로 보낸다. 화면은 라벨 편집창의 최종 상태 전체를 보내므로
      제출된 키는 add, 기존에 있는데 제출에서 빠진 키는 remove — 항목 삭제가 반영된다(ETRI 검토의견)."""
    patch: list[dict] = []
    for k, v in body.items():
        if k not in _WRITABLE:
            continue
        if k == "labels":
            if isinstance(v, dict):
                try:
                    cur = magnum.request("GET", f"/clustertemplates/{template_id}").get("labels") or {}
                except Exception as e:  # noqa: BLE001
                    raise _err(e)
                patch += [{"op": "add", "path": f"/labels/{lk}", "value": str(lv)} for lk, lv in v.items()]
                patch += [{"op": "remove", "path": f"/labels/{lk}"} for lk in cur if lk not in v]
            continue
        if v not in (None, "", {}, []):
            patch.append({"op": "replace", "path": f"/{k}", "value": v})
    if not patch:
        raise HTTPException(status_code=400, detail="변경할 필드가 없습니다.")
    try:
        return magnum.request("PATCH", f"/clustertemplates/{template_id}", json=patch)
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.delete("/clustertemplates/{template_id}", dependencies=[Depends(require_admin)])
def delete_template(template_id: str):
    try:
        return magnum.request("DELETE", f"/clustertemplates/{template_id}")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
