"""서비스 클러스터 내부 K8s 리소스 조회·관리 — 선택된 클러스터의 API 를 직접 질의.

경로 규칙: /clusters/{id}/... (프론트의 전역 클러스터 선택기와 1:1).
쓰기(CRUD)는 네임스페이스 리소스(사용자 self-service)만 — 클러스터·PV·StorageClass 는 관리자 영역.
권한: 관리자 또는 그 클러스터에 접근 권한이 있는 사용자(main.py 가 라우터 전체에 auth.cluster_member 를 건다).
"""
from __future__ import annotations

import base64
from datetime import datetime, timezone
from urllib.parse import quote

import httpx
import yaml
from fastapi import APIRouter, Body, HTTPException, Query

from ..k8s_client import k8s_get, k8s_get_text, k8s_request

router = APIRouter(tags=["k8s"])


def _err(e: Exception) -> HTTPException:  # noqa: ANN001
    # K8s 가 준 상태코드·메시지를 그대로 전달(409 충돌·404·422 검증 등 프론트가 구분)
    if isinstance(e, httpx.HTTPStatusError):
        try:
            msg = e.response.json().get("message", e.response.text)
        except Exception:  # noqa: BLE001
            msg = e.response.text
        return HTTPException(status_code=e.response.status_code, detail=msg)
    return HTTPException(status_code=502, detail=f"k8s api error: {type(e).__name__}: {e}")


def _ns_path(base_all: str, base_ns: str, namespace: str | None) -> str:
    return base_ns.format(ns=namespace) if namespace else base_all


# 가속기 리소스 키(limits) — 벤더 디바이스 플러그인
_ACCEL_KEYS = ("nvidia.com/gpu", "furiosa.ai/npu", "furiosa.ai/rngd", "rebellions.ai/atom", "samsung.com/pim")


def _accel_of(containers: list[dict]) -> dict | None:
    for c in containers:
        limits = (c.get("resources") or {}).get("limits") or {}
        for k in _ACCEL_KEYS:
            if k in limits:
                return {"kind": k, "count": int(limits[k])}
    return None


def _requests_of(containers: list[dict]) -> dict:
    cpu = mem = ""
    for c in containers:
        req = (c.get("resources") or {}).get("requests") or {}
        cpu = cpu or req.get("cpu", "")
        mem = mem or req.get("memory", "")
    return {"cpu": cpu, "memory": mem}


@router.get("/clusters/{cluster_id}/namespaces")
def namespaces(cluster_id: str):
    try:
        d = k8s_get(cluster_id, "/api/v1/namespaces")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    return {"items": [n["metadata"]["name"] for n in d.get("items", [])]}


# 노드 라벨에서 모델명을 주는 키(벤더 feature-discovery)
_ACCEL_PRODUCT_LABELS = ("nvidia.com/gpu.product",)


@router.get("/clusters/{cluster_id}/accelerators")
def accelerators(cluster_id: str):
    """클러스터가 실제 보유한 가속기만 — 노드 allocatable 합산 + 노드 라벨의 모델명.

    워크로드 생성 시 '이 클러스터에 있는 가속기'만 고르게 하기 위함.
    반환: [{kind(확장 리소스 키), count(합계), products(모델 라벨 distinct)}]
    """
    try:
        d = k8s_get(cluster_id, "/api/v1/nodes")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    agg: dict[str, int] = {}
    products: dict[str, set[str]] = {}
    for n in d.get("items", []):
        alloc = (n.get("status") or {}).get("allocatable") or {}
        labels = (n.get("metadata") or {}).get("labels") or {}
        node_products = [labels[k] for k in _ACCEL_PRODUCT_LABELS if labels.get(k)]
        for key in _ACCEL_KEYS:
            try:
                cnt = int(alloc.get(key, 0) or 0)
            except (TypeError, ValueError):
                cnt = 0
            if cnt <= 0:
                continue
            agg[key] = agg.get(key, 0) + cnt
            # nvidia 처럼 노드 라벨로만 모델을 아는 경우 그 노드의 product 라벨을 연결
            for p in node_products:
                products.setdefault(key, set()).add(p)
    items = [
        {"kind": k, "count": agg[k], "products": sorted(products.get(k, set()))}
        for k in sorted(agg)
    ]
    return {"items": items}


@router.get("/clusters/{cluster_id}/workloads")
def workloads(cluster_id: str, namespace: str | None = Query(None)):
    """Deployment·StatefulSet·DaemonSet·Job·CronJob 통합 목록."""
    kinds = [
        ("Deployment", "/apis/apps/v1/deployments", "/apis/apps/v1/namespaces/{ns}/deployments"),
        ("StatefulSet", "/apis/apps/v1/statefulsets", "/apis/apps/v1/namespaces/{ns}/statefulsets"),
        ("DaemonSet", "/apis/apps/v1/daemonsets", "/apis/apps/v1/namespaces/{ns}/daemonsets"),
        ("Job", "/apis/batch/v1/jobs", "/apis/batch/v1/namespaces/{ns}/jobs"),
        ("CronJob", "/apis/batch/v1/cronjobs", "/apis/batch/v1/namespaces/{ns}/cronjobs"),
    ]
    out: list[dict] = []
    try:
        for kind, all_p, ns_p in kinds:
            for it in k8s_get(cluster_id, _ns_path(all_p, ns_p, namespace)).get("items", []):
                meta, spec, status = it.get("metadata", {}), it.get("spec", {}), it.get("status", {})
                if kind == "CronJob":
                    tpl = spec.get("jobTemplate", {}).get("spec", {}).get("template", {}).get("spec", {})
                else:
                    tpl = spec.get("template", {}).get("spec", {})
                containers = tpl.get("containers", []) or []

                if kind == "Deployment" or kind == "StatefulSet":
                    ready, total = status.get("readyReplicas", 0), spec.get("replicas", 0)
                    state = "Running" if ready and ready == total else ("Pending" if total else "Stopped")
                elif kind == "DaemonSet":
                    ready, total = status.get("numberReady", 0), status.get("desiredNumberScheduled", 0)
                    state = "Running" if ready and ready == total else "Pending"
                elif kind == "Job":
                    ready, total = status.get("succeeded", 0) or status.get("active", 0), spec.get("completions", 1) or 1
                    state = "Succeeded" if status.get("succeeded") else ("Failed" if status.get("failed") else "Running")
                else:  # CronJob
                    ready, total = len(status.get("active", []) or []), 0
                    state = "Suspended" if spec.get("suspend") else "Running"

                out.append({
                    "name": meta.get("name", ""),
                    "namespace": meta.get("namespace", ""),
                    "kind": kind,
                    "image": containers[0].get("image", "") if containers else "",
                    "ready": ready,
                    "total": total,
                    "requests": _requests_of(containers),
                    "accelerator": _accel_of(containers),
                    "schedule": spec.get("schedule", "") if kind == "CronJob" else "",
                    "status": state,
                    "labels": meta.get("labels") or {},
                    "createdAt": meta.get("creationTimestamp", ""),
                })
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    return {"items": out}


@router.get("/clusters/{cluster_id}/pods")
def pods(cluster_id: str, namespace: str | None = Query(None)):
    """파드 목록 — 워크로드 화면에서 Pod 유형을 고르면 호출. 소유 워크로드(owner, 없으면 독립 파드)를 함께 준다."""
    try:
        items = k8s_get(cluster_id, _ns_path("/api/v1/pods", "/api/v1/namespaces/{ns}/pods", namespace)).get("items", [])
        # ReplicaSet 소유 파드는 원래 주인(Deployment)으로 보여준다 — 관리 단위는 RS 가 아니라 Deployment
        rs_owner: dict[tuple, dict] = {}
        for rs in k8s_get(cluster_id, _ns_path("/apis/apps/v1/replicasets",
                                               "/apis/apps/v1/namespaces/{ns}/replicasets", namespace)).get("items", []):
            m = rs.get("metadata", {})
            o = next((o for o in (m.get("ownerReferences") or []) if o.get("controller")), None)
            if o:
                rs_owner[(m.get("namespace"), m.get("name"))] = {"kind": o["kind"], "name": o["name"]}
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    out = []
    for p in items:
        meta, spec, st = p.get("metadata", {}), p.get("spec", {}), p.get("status", {})
        cs = st.get("containerStatuses", []) or []
        o = next((o for o in (meta.get("ownerReferences") or []) if o.get("controller")), None)
        owner = {"kind": o["kind"], "name": o["name"]} if o else None
        if owner and owner["kind"] == "ReplicaSet":
            owner = rs_owner.get((meta.get("namespace"), owner["name"])) or owner
        # 상태 — kubectl 처럼 대기 사유(CrashLoopBackOff·ImagePullBackOff 등)가 있으면 phase 대신 그것을 보여준다
        state = st.get("reason") or st.get("phase", "")
        state = next((c["state"]["waiting"]["reason"] for c in cs
                      if (c.get("state") or {}).get("waiting", {}).get("reason")), state)
        containers = spec.get("containers", []) or []
        out.append({
            "name": meta.get("name", ""),
            "namespace": meta.get("namespace", ""),
            "kind": "Pod",
            "image": containers[0].get("image", "") if containers else "",
            "ready": sum(1 for c in cs if c.get("ready")),
            "total": len(containers),
            "requests": _requests_of(containers),
            "accelerator": _accel_of(containers),
            "restarts": sum(c.get("restartCount", 0) for c in cs),
            "node": spec.get("nodeName", ""),
            "containers": [c.get("name", "") for c in containers],   # 로그 보기 — 컨테이너 선택용
            "owner": owner,
            "status": state,
            "labels": meta.get("labels") or {},
            "createdAt": meta.get("creationTimestamp", ""),
        })
    return {"items": out}


@router.get("/clusters/{cluster_id}/services")
def services(cluster_id: str, namespace: str | None = Query(None)):
    """Service + Ingress."""
    try:
        svc = k8s_get(cluster_id, _ns_path("/api/v1/services", "/api/v1/namespaces/{ns}/services", namespace))
        ing = k8s_get(cluster_id, _ns_path(
            "/apis/networking.k8s.io/v1/ingresses", "/apis/networking.k8s.io/v1/namespaces/{ns}/ingresses", namespace))
    except Exception as e:  # noqa: BLE001
        raise _err(e)

    services_out = []
    for it in svc.get("items", []):
        meta, spec, status = it.get("metadata", {}), it.get("spec", {}), it.get("status", {})
        lb = (status.get("loadBalancer") or {}).get("ingress") or []
        services_out.append({
            "name": meta.get("name", ""),
            "namespace": meta.get("namespace", ""),
            "type": spec.get("type", "ClusterIP"),
            "clusterIp": spec.get("clusterIP", ""),
            "externalIp": (lb[0].get("ip") or lb[0].get("hostname")) if lb else "",
            "ports": [
                {"port": p.get("port"), "targetPort": str(p.get("targetPort", "")), "protocol": p.get("protocol", "TCP")}
                for p in spec.get("ports", []) or []
            ],
            "selector": spec.get("selector") or {},
            "createdAt": meta.get("creationTimestamp", ""),
        })

    ingresses_out = []
    for it in ing.get("items", []):
        meta, spec = it.get("metadata", {}), it.get("spec", {})
        rules = spec.get("rules", []) or []
        ingresses_out.append({
            "name": meta.get("name", ""),
            "namespace": meta.get("namespace", ""),
            "className": spec.get("ingressClassName", ""),
            "hosts": [r.get("host", "*") for r in rules],
            "tls": bool(spec.get("tls")),
            "ruleCount": len(rules),
            "createdAt": meta.get("creationTimestamp", ""),
        })
    return {"services": services_out, "ingresses": ingresses_out}


@router.get("/clusters/{cluster_id}/storage")
def storage(cluster_id: str, namespace: str | None = Query(None)):
    """PVC(네임스페이스) + PV·StorageClass(클러스터 전역, 조회용)."""
    try:
        pvc = k8s_get(cluster_id, _ns_path(
            "/api/v1/persistentvolumeclaims", "/api/v1/namespaces/{ns}/persistentvolumeclaims", namespace))
        pv = k8s_get(cluster_id, "/api/v1/persistentvolumes")
        sc = k8s_get(cluster_id, "/apis/storage.k8s.io/v1/storageclasses")
    except Exception as e:  # noqa: BLE001
        raise _err(e)

    pvcs = [{
        "name": m["metadata"].get("name", ""),
        "namespace": m["metadata"].get("namespace", ""),
        "status": m.get("status", {}).get("phase", ""),
        "volumeName": m.get("spec", {}).get("volumeName", ""),
        "capacity": (m.get("status", {}).get("capacity") or {}).get("storage", "")
            or (m.get("spec", {}).get("resources", {}).get("requests") or {}).get("storage", ""),
        "accessModes": m.get("spec", {}).get("accessModes", []) or [],
        "storageClass": m.get("spec", {}).get("storageClassName", ""),
        "createdAt": m["metadata"].get("creationTimestamp", ""),
    } for m in pvc.get("items", [])]

    pvs = [{
        "name": m["metadata"].get("name", ""),
        "capacity": (m.get("spec", {}).get("capacity") or {}).get("storage", ""),
        "accessModes": m.get("spec", {}).get("accessModes", []) or [],
        "reclaimPolicy": m.get("spec", {}).get("persistentVolumeReclaimPolicy", ""),
        "status": m.get("status", {}).get("phase", ""),
        "claim": (lambda c: f"{c.get('namespace','')}/{c.get('name','')}" if c else "")(m.get("spec", {}).get("claimRef")),
        "storageClass": m.get("spec", {}).get("storageClassName", ""),
        "csiDriver": (m.get("spec", {}).get("csi") or {}).get("driver", ""),
        "createdAt": m["metadata"].get("creationTimestamp", ""),
    } for m in pv.get("items", [])]

    scs = [{
        "name": m["metadata"].get("name", ""),
        "provisioner": m.get("provisioner", ""),
        "reclaimPolicy": m.get("reclaimPolicy", ""),
        "bindingMode": m.get("volumeBindingMode", ""),
        "isDefault": (m["metadata"].get("annotations") or {}).get(
            "storageclass.kubernetes.io/is-default-class") == "true",
        "parameters": m.get("parameters") or {},
    } for m in sc.get("items", [])]

    return {"pvcs": pvcs, "pvs": pvs, "storageClasses": scs}


@router.get("/clusters/{cluster_id}/configs")
def configs(cluster_id: str, namespace: str | None = Query(None)):
    """ConfigMap + Secret — 키 목록만(값은 반환하지 않음, 상세·쓰기는 다음 단계)."""
    try:
        cm = k8s_get(cluster_id, _ns_path("/api/v1/configmaps", "/api/v1/namespaces/{ns}/configmaps", namespace))
        sec = k8s_get(cluster_id, _ns_path("/api/v1/secrets", "/api/v1/namespaces/{ns}/secrets", namespace))
    except Exception as e:  # noqa: BLE001
        raise _err(e)

    cms = [{
        "name": m["metadata"].get("name", ""),
        "namespace": m["metadata"].get("namespace", ""),
        "keys": sorted((m.get("data") or {}).keys()),
        "createdAt": m["metadata"].get("creationTimestamp", ""),
    } for m in cm.get("items", [])]

    secrets = [{
        "name": m["metadata"].get("name", ""),
        "namespace": m["metadata"].get("namespace", ""),
        "type": m.get("type", ""),
        "keys": sorted((m.get("data") or {}).keys()),
        "createdAt": m["metadata"].get("creationTimestamp", ""),
    } for m in sec.get("items", [])]

    return {"configmaps": cms, "secrets": secrets}


# ════════════════════════════════════════════════════════════════════
# 쓰기 — 네임스페이스 리소스(사용자 self-service). 클러스터/PV/SC 는 제외.
# ════════════════════════════════════════════════════════════════════
_WORKLOAD_API = {
    "Deployment": ("apps/v1", "deployments"),
    "StatefulSet": ("apps/v1", "statefulsets"),
    "DaemonSet": ("apps/v1", "daemonsets"),
    "Job": ("batch/v1", "jobs"),
    "CronJob": ("batch/v1", "cronjobs"),
    "Pod": ("v1", "pods"),   # YAML 적용·조회·삭제만 — 폼 생성·스케일·재시작은 불가
}
_SCALABLE = {"Deployment", "StatefulSet"}
_RESTARTABLE = {"Deployment", "StatefulSet", "DaemonSet"}


def _wl_path(kind: str, ns: str, name: str | None = None) -> str:
    if kind not in _WORKLOAD_API:
        raise HTTPException(status_code=400, detail=f"지원하지 않는 워크로드 종류: {kind}")
    grp, plural = _WORKLOAD_API[kind]
    prefix = "/api/v1" if grp == "v1" else f"/apis/{grp}"   # Pod 는 core API
    base = f"{prefix}/namespaces/{ns}/{plural}"
    return f"{base}/{name}" if name else base


def _parse_workload(body: dict) -> tuple[dict, str, str, str]:
    """apply·preview 공용 — YAML 파싱·검증 후 (obj, kind, ns, name). 네임스페이스는 obj 에 보정해 넣는다."""
    try:
        obj = yaml.safe_load(body["yaml"])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"YAML 파싱 실패: {e}")
    if not isinstance(obj, dict) or "kind" not in obj or not obj.get("metadata", {}).get("name"):
        raise HTTPException(status_code=400, detail="kind·metadata.name 이 있는 단일 리소스 YAML 이어야 합니다.")
    kind = obj["kind"]
    if kind not in _WORKLOAD_API:
        raise HTTPException(status_code=400, detail=f"워크로드 종류만 허용: {kind} 불가")
    ns = obj["metadata"].get("namespace") or body.get("namespace") or "default"
    obj["metadata"]["namespace"] = ns
    return obj, kind, ns, obj["metadata"]["name"]


def _clean_manifest(obj: dict) -> str:
    """편집·비교용 YAML — managedFields·status 제거."""
    obj.get("metadata", {}).pop("managedFields", None)
    obj.pop("status", None)
    return yaml.safe_dump(obj, allow_unicode=True, sort_keys=False)


# ── 워크로드 ──
@router.post("/clusters/{cluster_id}/workloads", status_code=201)
def create_workload(cluster_id: str, body: dict = Body(...)):
    """폼 기반 생성 — Deployment/StatefulSet(레플리카형). 이미지·자원·가속기.
    (Job/CronJob/DaemonSet·커스텀은 /workloads/apply 로 YAML 적용)"""
    kind = body.get("kind", "Deployment")
    if kind not in ("Deployment", "StatefulSet"):
        raise HTTPException(status_code=400, detail="폼 생성은 Deployment/StatefulSet 만. 그 외는 YAML 적용을 쓰세요.")
    ns = body["namespace"]
    name = body["name"]
    requests: dict = {}
    limits: dict = {}
    if body.get("cpu"):
        requests["cpu"] = str(body["cpu"])
    if body.get("memory"):
        requests["memory"] = str(body["memory"])
    acc_kind, acc_count = body.get("acceleratorKind"), int(body.get("acceleratorCount") or 0)
    if acc_kind and acc_count > 0:
        limits[acc_kind] = str(acc_count)  # 가속기는 requests=limits 가 원칙
        requests[acc_kind] = str(acc_count)
    spec: dict = {
        "replicas": int(body.get("replicas") or 1),
        "selector": {"matchLabels": {"app": name}},
        "template": {
            "metadata": {"labels": {"app": name}},
            "spec": {"containers": [{
                "name": name,
                "image": body["image"],
                **({"resources": {"requests": requests, "limits": limits or requests}} if requests else {}),
            }]},
        },
    }
    if kind == "StatefulSet":
        spec["serviceName"] = name  # StatefulSet 필수 — 헤드리스 서비스명(DNS)
    manifest = {
        "apiVersion": "apps/v1", "kind": kind,
        "metadata": {"name": name, "namespace": ns, "labels": {"app": name}},
        "spec": spec,
    }
    try:
        return k8s_request(cluster_id, "POST", _wl_path(kind, ns), json=manifest)
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.post("/clusters/{cluster_id}/workloads/apply", status_code=201)
def apply_workload(cluster_id: str, body: dict = Body(...)):
    """YAML 로 워크로드 생성/적용(server-side apply) — Job/CronJob/DaemonSet 등 모든 종류."""
    obj, kind, ns, name = _parse_workload(body)
    path = f"{_wl_path(kind, ns, name)}?fieldManager=okc-portal&force=true"
    try:
        return k8s_request(cluster_id, "PATCH", path,
                           content=yaml.safe_dump(obj).encode(), content_type="application/apply-patch+yaml")
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.post("/clusters/{cluster_id}/workloads/apply/preview")
def preview_workload_apply(cluster_id: str, body: dict = Body(...)):
    """적용 전 미리보기 — before(현재 객체)·after(dryRun 적용 결과)·action(create|update).

    after 는 서버가 기본값·정규화까지 채운 실제 적용 결과(dryRun=All, 클러스터 비영속)."""
    obj, kind, ns, name = _parse_workload(body)
    # before — 현재 객체(없으면 신규 생성)
    try:
        before = _clean_manifest(k8s_get(cluster_id, _wl_path(kind, ns, name)))
        action = "update"
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            before, action = "", "create"
        else:
            raise _err(e)
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    # after — dryRun=All 적용 결과(클러스터에 생성·변경하지 않음)
    path = f"{_wl_path(kind, ns, name)}?fieldManager=okc-portal&force=true&dryRun=All"
    try:
        result = k8s_request(cluster_id, "PATCH", path,
                             content=yaml.safe_dump(obj).encode(), content_type="application/apply-patch+yaml")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    return {"before": before, "after": _clean_manifest(result), "action": action}


@router.get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/yaml")
def workload_yaml(cluster_id: str, kind: str, namespace: str, name: str):
    """워크로드 매니페스트(YAML) 조회 — managedFields·status 는 제거해 편집용으로 정리."""
    try:
        obj = k8s_get(cluster_id, _wl_path(kind, namespace, name))
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    obj.get("metadata", {}).pop("managedFields", None)
    obj.pop("status", None)
    return {"yaml": yaml.safe_dump(obj, allow_unicode=True, sort_keys=False)}


@router.put("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/yaml")
def update_workload_yaml(cluster_id: str, kind: str, namespace: str, name: str, body: dict = Body(...)):
    """YAML 직접 수정 저장 — server-side apply."""
    try:
        obj = yaml.safe_load(body["yaml"])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"YAML 파싱 실패: {e}")
    obj.setdefault("metadata", {})["namespace"] = namespace
    path = f"{_wl_path(kind, namespace, name)}?fieldManager=okc-portal&force=true"
    try:
        return k8s_request(cluster_id, "PATCH", path,
                           content=yaml.safe_dump(obj).encode(), content_type="application/apply-patch+yaml")
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.get("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/pods")
def workload_pods(cluster_id: str, kind: str, namespace: str, name: str):
    """워크로드에 속한 파드 목록 — selector(matchLabels)로 조회. Job 류는 job-name."""
    try:
        wl = k8s_get(cluster_id, _wl_path(kind, namespace, name))
        sel = (wl.get("spec", {}).get("selector", {}) or {}).get("matchLabels") or {}
        if not sel and kind in ("Job", "CronJob"):
            sel = {"job-name": name}
        ls = ",".join(f"{k}={v}" for k, v in sel.items())
        pods = k8s_get(cluster_id, f"/api/v1/namespaces/{namespace}/pods?labelSelector={quote(ls)}")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    out = []
    for p in pods.get("items", []):
        meta, spec, st = p.get("metadata", {}), p.get("spec", {}), p.get("status", {})
        cs = st.get("containerStatuses", []) or []
        out.append({
            "name": meta.get("name", ""),
            "phase": st.get("phase", ""),
            "ready": f"{sum(1 for c in cs if c.get('ready'))}/{len(cs)}" if cs else "0/0",
            "restarts": sum(c.get("restartCount", 0) for c in cs),
            "node": spec.get("nodeName", ""),
            "podIP": st.get("podIP", ""),
            "startedAt": st.get("startTime", ""),
            "containers": [c.get("name", "") for c in (spec.get("containers", []) or [])],
        })
    return {"pods": out}


@router.get("/clusters/{cluster_id}/pods/{namespace}/{name}/logs")
def pod_logs(cluster_id: str, namespace: str, name: str,
             container: str | None = Query(None), tail: int = Query(500)):
    """파드 로그 — 평문. container 미지정 시 기본 컨테이너."""
    q = f"?tailLines={tail}" + (f"&container={quote(container)}" if container else "")
    try:
        return {"logs": k8s_get_text(cluster_id, f"/api/v1/namespaces/{namespace}/pods/{name}/log{q}")}
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.post("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/scale")
def scale_workload(cluster_id: str, kind: str, namespace: str, name: str, body: dict = Body(...)):
    """레플리카 조정 — scale 서브리소스 PATCH."""
    if kind not in _SCALABLE:
        raise HTTPException(status_code=400, detail=f"{kind} 은(는) 레플리카 조정을 지원하지 않습니다.")
    replicas = int(body["replicas"])
    try:
        return k8s_request(
            cluster_id, "PATCH", f"{_wl_path(kind, namespace, name)}/scale",
            content=f'{{"spec":{{"replicas":{replicas}}}}}'.encode(),
            content_type="application/merge-patch+json",
        )
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.post("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}/restart")
def restart_workload(cluster_id: str, kind: str, namespace: str, name: str):
    """롤아웃 재시작 — template 에 restartedAt 주석 PATCH(kubectl rollout restart 와 동일)."""
    if kind not in _RESTARTABLE:
        raise HTTPException(status_code=400, detail=f"{kind} 은(는) 재시작을 지원하지 않습니다.")
    now = datetime.now(timezone.utc).isoformat()
    patch = {"spec": {"template": {"metadata": {"annotations": {
        "kubectl.kubernetes.io/restartedAt": now}}}}}
    try:
        import json as _json
        return k8s_request(
            cluster_id, "PATCH", _wl_path(kind, namespace, name),
            content=_json.dumps(patch).encode(),
            content_type="application/strategic-merge-patch+json",
        )
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.delete("/clusters/{cluster_id}/workloads/{kind}/{namespace}/{name}")
def delete_workload(cluster_id: str, kind: str, namespace: str, name: str):
    try:
        return k8s_request(cluster_id, "DELETE", _wl_path(kind, namespace, name))
    except Exception as e:  # noqa: BLE001
        raise _err(e)


# ── ConfigMap ──
@router.post("/clusters/{cluster_id}/configmaps", status_code=201)
def create_configmap(cluster_id: str, body: dict = Body(...)):
    ns, name = body["namespace"], body["name"]
    manifest = {"apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": name, "namespace": ns},
                "data": body.get("data") or {}}
    try:
        return k8s_request(cluster_id, "POST", f"/api/v1/namespaces/{ns}/configmaps", json=manifest)
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.put("/clusters/{cluster_id}/configmaps/{namespace}/{name}")
def update_configmap(cluster_id: str, namespace: str, name: str, body: dict = Body(...)):
    """data 전체 교체(merge patch)."""
    try:
        import json as _json
        return k8s_request(
            cluster_id, "PATCH", f"/api/v1/namespaces/{namespace}/configmaps/{name}",
            content=_json.dumps({"data": body.get("data") or {}}).encode(),
            content_type="application/merge-patch+json",
        )
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.delete("/clusters/{cluster_id}/configmaps/{namespace}/{name}")
def delete_configmap(cluster_id: str, namespace: str, name: str):
    try:
        return k8s_request(cluster_id, "DELETE", f"/api/v1/namespaces/{namespace}/configmaps/{name}")
    except Exception as e:  # noqa: BLE001
        raise _err(e)


# ── Secret ──
@router.get("/clusters/{cluster_id}/secrets/{namespace}/{name}")
def reveal_secret(cluster_id: str, namespace: str, name: str):
    """Secret 값 열람 — base64 디코드해서 평문 반환(명시적 요청 시에만)."""
    try:
        d = k8s_get(cluster_id, f"/api/v1/namespaces/{namespace}/secrets/{name}")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
    decoded: dict = {}
    for k, v in (d.get("data") or {}).items():
        try:
            decoded[k] = base64.b64decode(v).decode("utf-8")
        except Exception:  # noqa: BLE001
            decoded[k] = "(바이너리 — 표시 불가)"
    return {"name": name, "namespace": namespace, "type": d.get("type", ""), "data": decoded}


@router.post("/clusters/{cluster_id}/secrets", status_code=201)
def create_secret(cluster_id: str, body: dict = Body(...)):
    """Opaque Secret 생성 — 평문 data 를 base64 로 인코딩해 stringData 대신 data 로."""
    ns, name = body["namespace"], body["name"]
    data = {k: base64.b64encode(str(v).encode()).decode() for k, v in (body.get("data") or {}).items()}
    manifest = {"apiVersion": "v1", "kind": "Secret",
                "metadata": {"name": name, "namespace": ns},
                "type": body.get("type") or "Opaque", "data": data}
    try:
        return k8s_request(cluster_id, "POST", f"/api/v1/namespaces/{ns}/secrets", json=manifest)
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.put("/clusters/{cluster_id}/secrets/{namespace}/{name}")
def update_secret(cluster_id: str, namespace: str, name: str, body: dict = Body(...)):
    data = {k: base64.b64encode(str(v).encode()).decode() for k, v in (body.get("data") or {}).items()}
    try:
        import json as _json
        return k8s_request(
            cluster_id, "PATCH", f"/api/v1/namespaces/{namespace}/secrets/{name}",
            content=_json.dumps({"data": data}).encode(),
            content_type="application/merge-patch+json",
        )
    except Exception as e:  # noqa: BLE001
        raise _err(e)


@router.delete("/clusters/{cluster_id}/secrets/{namespace}/{name}")
def delete_secret(cluster_id: str, namespace: str, name: str):
    try:
        return k8s_request(cluster_id, "DELETE", f"/api/v1/namespaces/{namespace}/secrets/{name}")
    except Exception as e:  # noqa: BLE001
        raise _err(e)
