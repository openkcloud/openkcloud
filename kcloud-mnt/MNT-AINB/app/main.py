"""
MNT-AINB — AI 노트북(Kubeflow) 중개 백엔드 (기존 cmp-backend 를 포털 모듈로 귀속)

실행(로컬): uvicorn app.main:app --reload --port 8300 --env-file config.env
  - 외부 경로는 API_PREFIX(/api/ainb) — 게이트웨이가 접두어 그대로 넘기고 이 모듈이 직접 서빙한다
    (내부 라우트는 /api/v1/* 그대로 두고 아래 _ApiPrefix 가 접두어를 바꿔 붙인다).
  - 노트북 IDE 프록시는 /notebook/{ns}/{name}/... (게이트웨이에 경로 하나 더 필요).
  - TEMPORAL_API_BASE 가 있으면 Temporal 워커(AI 워크스페이스 프로비저닝 작업, 큐 okc-ainb)도 함께 시작.

목업 프론트(src/lib/notebook-api.ts) 주석에 명세된 인터페이스 구현:
  GET    /api/v1/notebooks                # 목록 (KFNotebook 호환 형태)
  POST   /api/v1/notebooks                # Notebook CRD 생성
  GET    /api/v1/notebooks/{name}         # 상세
  GET    /api/v1/notebooks/{name}/connect # NotebookResource + connectUrl 발급
  POST   /api/v1/notebooks/{name}/start
  POST   /api/v1/notebooks/{name}/stop
  POST   /api/v1/notebooks/{name}/restart
  DELETE /api/v1/notebooks/{name}

Kubeflow Notebook Controller(standalone) + minikube 대상.
iframe 접속은 노트북별 kubectl port-forward 프로세스로 제공 (MVP 방식 —
운영 전환 시 Istio VirtualService + oauth2-proxy로 교체).
"""
import json
import subprocess
import threading
import time
import zlib
from datetime import datetime, timezone
from typing import Optional

from fastapi import FastAPI, HTTPException, Request, WebSocket
from fastapi.middleware.cors import CORSMiddleware
from kubernetes import client, config
from pydantic import BaseModel

GROUP, VERSION, PLURAL = "kubeflow.org", "v1", "notebooks"
STOP_ANNOTATION = "kubeflow-resource-stopped"

# ══════════════════════════════════════════════════════════════════
# 환경 설정 — config.env 로 주입 (uvicorn --env-file config.env)
#   미설정 시 기본값 = 표준 클러스터(테스트베드) 기준.
#   로컬 WSL 개발환경 전용 우회는 값을 채워서 활성화한다.
#   예시: config.env.example 참고
# ══════════════════════════════════════════════════════════════════
import os as _os

DEFAULT_NS = _os.environ.get("DEFAULT_NAMESPACE", "ai-research")
DEFAULT_IMAGE = _os.environ.get(
    "DEFAULT_NOTEBOOK_IMAGE", "kubeflownotebookswg/jupyter-scipy:v1.9.2")
# 포털이 접근하는 이 백엔드의 외부 주소 (connectUrl 등 발급용)
PUBLIC_BASE_URL = _os.environ.get("PUBLIC_BASE_URL", "http://localhost:8000")
# 포털 origin (CORS)
CORS_ORIGINS = _os.environ.get(
    "CORS_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000").split(",")
# [로컬 전용] Pod을 특정 노드에 고정 — WSL 크로스노드 제약 우회. 비우면 스케줄러 자율
DEV_PIN_NODE = _os.environ.get("DEV_PIN_NODE", "")
# [로컬 전용] Pod 외부통신 프록시 — WSL egress 제약 우회. 비우면 직접 통신
POD_HTTP_PROXY = _os.environ.get("POD_HTTP_PROXY", "")
POD_NO_PROXY = _os.environ.get(
    "POD_NO_PROXY",
    "localhost,127.0.0.1,10.0.0.0/8,192.168.0.0/24,.svc,.cluster.local")
# LLM 실측 대상 (namespace/service)
LLM_SERVICE = _os.environ.get("LLM_SERVICE", "llm-serving/vllm-qwen")
LLM_NODE_DESC = _os.environ.get("LLM_NODE_DESC", "")
LLM_MODEL = _os.environ.get("LLM_MODEL", "Qwen/Qwen2.5-0.5B-Instruct")
# GPU Pod에 지정할 RuntimeClass (k3s+nvidia-container-runtime 환경 필수).
# NVIDIA GPU Operator를 쓰는 클러스터에서는 빈 값으로 두면 된다.
GPU_RUNTIME_CLASS = _os.environ.get("GPU_RUNTIME_CLASS", "nvidia")
# 가속기의 쿠버네티스 자원 이름. NVIDIA GPU는 nvidia.com/gpu, NPU는 장비 드라이버가
# 등록하는 이름(예: rebellions.ai/ATOM)으로 바꾼다. 쿼터 키는 requests.<이 값>이 된다.
ACCEL_RESOURCE = _os.environ.get("ACCEL_RESOURCE", "nvidia.com/gpu")
BENCH_IMAGE_ENV = _os.environ.get(
    "BENCH_IMAGE", "kubeflownotebookswg/jupyter-pytorch-cuda-full:v1.9.2")


def _pin_selector() -> dict:
    """DEV_PIN_NODE 설정 시에만 nodeSelector 반환 (정상 클러스터에선 빈 dict)."""
    return ({"kubernetes.io/hostname": DEV_PIN_NODE} if DEV_PIN_NODE else {})

# ══════════════════════════════════════════════════════════════════
# 서비스 클러스터 연결 — 클러스터마다 kubeconfig 하나
#   단일 모드(CLUSTER_SOURCE 비움, 기존 방식): 파드 안이면 ServiceAccount, 밖(개발 PC)이면 kubeconfig 로
#     클러스터 하나에 붙는다. 포털 파드가 다른(서비스) 클러스터를 조작할 때는 KUBECONFIG 를 준다.
#   PaaS 모드(CLUSTER_SOURCE=paas): MNT-PAAS 가 클러스터마다 발급하는 kubeconfig(GET /clusters/{id}/kubeconfig)를
#     사용자가 AI 노트북에 로그인하거나 AI 워크스페이스를 신청할 때 그 사용자의 토큰으로 받아 보관하고 쓴다.
#     보관은 관리 클러스터의 Secret(CLUSTER_STORE_SECRET) — 재시작 뒤에도, 사용자 없이 도는 작업
#     (워크스페이스 생성·IDE 연결 유지)에도 쓰인다. 파드 밖(개발 PC)에서는 CLUSTER_STORE_DIR 의 파일에 둔다.
#   요청마다 '지금 어느 클러스터 일인지'를 _cluster_var 에 두고, crd·core 등은 호출하는 순간 그 클러스터로 간다.
#   클러스터는 ?clusterId= 로 고르거나, 워크스페이스(네임스페이스)가 있는 클러스터로 정해진다.
# ══════════════════════════════════════════════════════════════════
import base64
import contextvars
from contextlib import contextmanager

CLUSTER_SOURCE =_os.environ.get("CLUSTER_SOURCE", "").strip().lower()   # "" | "paas"
MULTI = CLUSTER_SOURCE == "paas"
PAAS_API_BASE = _os.environ.get("PAAS_API_BASE", "").rstrip("/")
PAAS_API_PREFIX = _os.environ.get("PAAS_API_PREFIX", "/api/paas").rstrip("/")
CLUSTER_STORE_SECRET = _os.environ.get("CLUSTER_STORE_SECRET", "mnt-ainb-clusters")
CLUSTER_STORE_DIR = _os.environ.get("CLUSTER_STORE_DIR", _os.path.expanduser("~/.mnt-ainb-clusters"))
CLUSTER_REFRESH_DAYS = int(_os.environ.get("CLUSTER_REFRESH_DAYS", "7"))   # 이보다 오래된 접속 정보는 로그인 때 새로 받는다
DEFAULT_CLUSTER_ID = _os.environ.get("DEFAULT_CLUSTER_ID", "")
_IN_POD = bool(_os.environ.get("KUBERNETES_SERVICE_HOST"))
# 단일 모드에서 파드 안의 ServiceAccount 로 붙는 경우(노트북이 같은 클러스터에서 돈다)
IN_CLUSTER = _IN_POD and not _os.environ.get("KUBECONFIG") and not MULTI
_STORE_ANN = "openkcloud.io/clusters"
_CID_RE = __import__("re").compile(r"^[-._a-zA-Z0-9]{1,200}$")   # Secret 키로 쓸 수 있는 클러스터 ID
_kc_tmp: Optional[str] = None


def _kc_dir() -> str:
    """kubectl 에 넘길 kubeconfig 파일을 두는 곳(이 프로세스 전용, 0700)."""
    global _kc_tmp
    if _kc_tmp is None:
        import tempfile
        _kc_tmp = tempfile.mkdtemp(prefix="mnt-ainb-kc-")
    return _kc_tmp


class _Cluster:
    """서비스 클러스터 하나 — 쿠버네티스 클라이언트와 kubectl 용 kubeconfig 파일."""

    def __init__(self, cid: str, name: str = "", kubeconfig: Optional[str] = None, fetched_at: str = ""):
        self.id, self.name, self.fetched_at = cid, name or cid, fetched_at
        self.cfg = client.Configuration()
        self.kubeconfig_path: Optional[str] = None
        if kubeconfig is not None:          # PaaS 모드 — MNT-PAAS 가 발급한 kubeconfig
            self.kubeconfig_path = _os.path.join(_kc_dir(), f"{zlib.crc32(cid.encode()):08x}.yaml")
            fd = _os.open(self.kubeconfig_path, _os.O_WRONLY | _os.O_CREAT | _os.O_TRUNC, 0o600)
            with _os.fdopen(fd, "w") as f:
                f.write(kubeconfig)
            config.load_kube_config(config_file=self.kubeconfig_path, client_configuration=self.cfg)
            self.in_cluster = False
        elif IN_CLUSTER:
            config.load_incluster_config(client_configuration=self.cfg)
            self.in_cluster = True
        else:
            config.load_kube_config(client_configuration=self.cfg)
            self.kubeconfig_path = _os.environ.get("KUBECONFIG") or None
            self.in_cluster = False
        # ClusterIP·파드 IP 로 바로 닿는지 — 같은 클러스터 안(파드)이거나 클러스터 노드에서 도는 개발 PC.
        # 다른 클러스터를 kubeconfig 로 조작할 때는 닿지 않으므로 port-forward 로 간다
        direct = self.in_cluster or (kubeconfig is None and not _IN_POD)
        self.direct_net = _os.environ.get("CLUSTER_DIRECT_NET", "1" if direct else "0") == "1"
        self.api = client.ApiClient(self.cfg)
        self.crd = client.CustomObjectsApi(self.api)
        self.core = client.CoreV1Api(self.api)

    def env(self) -> dict:
        """kubectl 이 이 클러스터로 가도록 하는 환경변수."""
        e = dict(_os.environ)
        if self.kubeconfig_path:
            e["KUBECONFIG"] = self.kubeconfig_path
        return e


_clusters: dict[str, _Cluster] = {}
_clusters_lock = threading.Lock()
_cluster_var: contextvars.ContextVar[str] = contextvars.ContextVar("ainb_cluster", default="")


def _default_cid() -> str:
    if DEFAULT_CLUSTER_ID in _clusters:
        return DEFAULT_CLUSTER_ID
    return next(iter(_clusters), "")


def _cid() -> str:
    """지금 요청(작업)의 클러스터 ID."""
    return _cluster_var.get() or _default_cid()


def _cur() -> _Cluster:
    c = _clusters.get(_cid())
    if c is None:
        raise HTTPException(503, "서비스 클러스터의 접속 정보가 아직 없습니다 — 포털에 다시 로그인하면 받아 옵니다"
                            if MULTI else "쿠버네티스 연결 설정이 없습니다")
    return c


def _set_cluster(cid: str) -> None:
    """이 요청(작업)의 이후 호출을 그 클러스터로 보낸다 — 요청마다 컨텍스트가 따로라 다른 요청에는 번지지 않는다."""
    _cluster_var.set(cid or "")


@contextmanager
def _use_cluster(cid: Optional[str]):
    tok = _cluster_var.set(cid or "")
    try:
        yield
    finally:
        _cluster_var.reset(tok)


def _listing_cids() -> list[str]:
    """목록을 합칠 클러스터 — ?clusterId= 로 하나를 골랐으면 그 클러스터만, 아니면 전부."""
    chosen = _cluster_var.get()
    return [chosen] if chosen else list(_clusters)


def _search_cids() -> list[str]:
    """이름으로 찾을 때 볼 클러스터 순서 — 지금 클러스터부터."""
    cur = _cid()
    return ([cur] if cur in _clusters else []) + [c for c in list(_clusters) if c != cur]


def _tag(item: dict) -> dict:
    """목록 항목에 어느 클러스터 것인지 붙인다(PaaS 모드만)."""
    if MULTI:
        c = _clusters.get(_cid())
        item["clusterId"] = _cid()
        item["clusterName"] = c.name if c else _cid()
    return item


class _OnCluster:
    """crd·core 자리 — 호출하는 순간 '지금 클러스터'의 클라이언트로 보낸다."""

    def __init__(self, attr: str):
        self._attr = attr

    def __getattr__(self, name):
        return getattr(getattr(_cur(), self._attr), name)


crd = _OnCluster("crd")
core = _OnCluster("core")


def _apps() -> client.AppsV1Api:
    return client.AppsV1Api(_cur().api)


def _kenv() -> dict:
    """kubectl 을 부를 때 넘길 env — 지금 클러스터로 가게 한다."""
    return _cur().env()


def _exec_api() -> client.CoreV1Api:
    # kubernetes.stream()은 호출하는 동안 ApiClient.request를 websocket으로
    # 바꿔치기한다. 공유 core 클라이언트로 exec하면 같은 순간 들어온 일반
    # API 호출이 websocket 경로로 새어 들어가 간헐 500이 난다(레이스).
    # exec 계열은 매번 전용 클라이언트를 쓴다.
    return client.CoreV1Api(client.ApiClient(_cur().cfg))


# ── 접속 정보 보관 — 관리 클러스터 Secret(파드) 또는 파일(개발 PC) ─────────
def _mgmt_core() -> client.CoreV1Api:
    cfg = client.Configuration()
    config.load_incluster_config(client_configuration=cfg)
    return client.CoreV1Api(client.ApiClient(cfg))


def _mgmt_ns() -> str:
    try:
        with open("/var/run/secrets/kubernetes.io/serviceaccount/namespace") as f:
            return f.read().strip()
    except OSError:
        return "default"


def _store_read() -> dict:
    """보관된 접속 정보 — {클러스터 ID: {"name", "fetchedAt", "kubeconfig"}}."""
    out: dict = {}
    if _IN_POD:
        try:
            s = _mgmt_core().read_namespaced_secret(CLUSTER_STORE_SECRET, _mgmt_ns())
        except client.exceptions.ApiException as e:
            if e.status == 404:
                return out
            raise
        meta = json.loads((s.metadata.annotations or {}).get(_STORE_ANN, "{}"))
        for key, b64 in (s.data or {}).items():
            if key.endswith(".yaml"):
                cid = key[:-5]
                out[cid] = {**meta.get(cid, {}), "kubeconfig": base64.b64decode(b64).decode()}
        return out
    if _os.path.isdir(CLUSTER_STORE_DIR):
        for fn in _os.listdir(CLUSTER_STORE_DIR):
            if fn.endswith(".json"):
                with open(_os.path.join(CLUSTER_STORE_DIR, fn), encoding="utf-8") as f:
                    out[fn[:-5]] = json.load(f)
    return out


def _store_write(cid: str, entry: Optional[dict]) -> None:
    """한 클러스터의 접속 정보를 저장(entry)하거나 지운다(None). 동시에 저장하다 충돌하면 다시 읽어 재시도한다."""
    if not _CID_RE.match(cid):
        raise ValueError(f"보관할 수 없는 클러스터 ID: {cid!r}")
    if _IN_POD:
        api, ns = _mgmt_core(), _mgmt_ns()
        for _ in range(5):
            try:
                s, exists = api.read_namespaced_secret(CLUSTER_STORE_SECRET, ns), True
            except client.exceptions.ApiException as e:
                if e.status != 404:
                    raise
                s, exists = client.V1Secret(metadata=client.V1ObjectMeta(name=CLUSTER_STORE_SECRET),
                                            type="Opaque"), False
            ann = dict(s.metadata.annotations or {})
            meta, data = json.loads(ann.get(_STORE_ANN, "{}")), dict(s.data or {})
            if entry is None:
                meta.pop(cid, None)
                data.pop(f"{cid}.yaml", None)
            else:
                meta[cid] = {"name": entry["name"], "fetchedAt": entry["fetchedAt"]}
                data[f"{cid}.yaml"] = base64.b64encode(entry["kubeconfig"].encode()).decode()
            ann[_STORE_ANN] = json.dumps(meta, ensure_ascii=False)
            s.metadata.annotations, s.data = ann, data
            try:
                if exists:
                    api.replace_namespaced_secret(CLUSTER_STORE_SECRET, ns, s)
                else:
                    api.create_namespaced_secret(ns, s)
                return
            except client.exceptions.ApiException as e:
                if e.status != 409:      # 409 = 그사이 다른 저장이 있었다 → 다시 읽어 합친다
                    raise
        raise RuntimeError("접속 정보 저장이 계속 충돌합니다")
    path = _os.path.join(CLUSTER_STORE_DIR, f"{cid}.json")
    if entry is None:
        try:
            _os.remove(path)
        except FileNotFoundError:
            pass
        return
    _os.makedirs(CLUSTER_STORE_DIR, mode=0o700, exist_ok=True)
    fd = _os.open(path, _os.O_WRONLY | _os.O_CREAT | _os.O_TRUNC, 0o600)
    with _os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(entry, f, ensure_ascii=False)


def _add_cluster(cid: str, name: str, kubeconfig: str, fetched_at: str, persist: bool = True) -> _Cluster:
    c = _Cluster(cid, name, kubeconfig, fetched_at)   # kubeconfig 가 잘못됐으면 여기서 실패 — 보관하지 않는다
    with _clusters_lock:
        _clusters[cid] = c
    if persist:
        try:
            _store_write(cid, {"name": name, "fetchedAt": fetched_at, "kubeconfig": kubeconfig})
        except Exception as e:  # noqa: BLE001 — 보관에 실패해도 이번 실행에는 쓴다(재시작하면 다시 받아야 한다)
            print(f"[mnt-ainb] 서비스 클러스터 {cid} 접속 정보 보관 실패: {e}")
    return c


def _store_load() -> None:
    for cid, e in _store_read().items():
        try:
            _add_cluster(cid, e.get("name") or cid, e["kubeconfig"], e.get("fetchedAt", ""), persist=False)
        except Exception as ex:  # noqa: BLE001
            print(f"[mnt-ainb] 보관된 서비스 클러스터 {cid} 접속 정보를 읽지 못했습니다: {ex}")


# ── MNT-PAAS 에서 접속 정보 받기 — 요청자의 토큰으로 ─────────────────
def _paas(path: str, authz: str):
    """MNT-PAAS GET — 요청자 토큰(Authorization)을 그대로 넘긴다. 권한·존재 판정은 MNT-PAAS 가 한다."""
    if not PAAS_API_BASE:
        raise HTTPException(503, "MNT-PAAS 주소(PAAS_API_BASE)가 설정되지 않았습니다")
    try:
        r = httpx.get(f"{PAAS_API_BASE}{PAAS_API_PREFIX}{path}", headers={"Authorization": authz}, timeout=30)
    except httpx.HTTPError as e:
        raise HTTPException(502, f"MNT-PAAS 연결 실패: {type(e).__name__}")
    if r.status_code < 400:
        return r
    try:
        detail = r.json().get("detail") or r.text[:200]
    except ValueError:
        detail = r.text[:200]
    if r.status_code == 401:
        raise HTTPException(401, "로그인이 필요합니다")
    if r.status_code == 403:
        raise HTTPException(403, "이 서비스 클러스터에 접근할 권한이 없습니다")
    if r.status_code == 404:
        raise HTTPException(404, "서비스 클러스터를 찾을 수 없습니다")
    if r.status_code == 409:
        raise HTTPException(409, f"서비스 클러스터가 아직 준비되지 않았습니다: {detail}")
    raise HTTPException(502, f"MNT-PAAS 응답 오류(HTTP {r.status_code}): {detail}")


def _fetch_cluster(cid: str, authz: str, name: str = "") -> _Cluster:
    from urllib.parse import quote as _q
    kubeconfig = _paas(f"/clusters/{_q(cid, safe='')}/kubeconfig", authz).text
    if not name:
        try:
            name = _paas(f"/clusters/{_q(cid, safe='')}", authz).json().get("name") or cid
        except HTTPException:
            name = cid
    return _add_cluster(cid, name, kubeconfig, datetime.now().strftime("%Y-%m-%d %H:%M"))


def _ensure_cluster(cid: str, authz: Optional[str] = None) -> _Cluster:
    """그 클러스터의 접속 정보 — 없으면 요청자의 토큰(authz)으로 MNT-PAAS 에서 받아 온다."""
    c = _clusters.get(cid)
    if c is not None:
        return c
    if not MULTI:
        raise HTTPException(404, f"서비스 클러스터 {cid}를 찾을 수 없습니다")
    if not authz:
        raise HTTPException(503, "이 서비스 클러스터의 접속 정보가 아직 없습니다 — 포털에 다시 로그인하면 받아 옵니다")
    return _fetch_cluster(cid, authz)


def _usable(status: str) -> bool:
    """API 주소가 있어 접속 정보를 받을 수 있는 클러스터 상태(Magnum)."""
    return (status.endswith("_COMPLETE") and not status.startswith("DELETE")) or status.startswith("UPDATE_")


def _stale(c: _Cluster) -> bool:
    try:
        return (datetime.now() - datetime.strptime(c.fetched_at, "%Y-%m-%d %H:%M")).days >= CLUSTER_REFRESH_DAYS
    except ValueError:
        return True


def _sync_clusters(authz: str, prune: bool = False, force: bool = False) -> list[dict]:
    """이 사용자가 쓸 수 있는 서비스 클러스터의 접속 정보를 받아 둔다 — 없거나 오래된 것만(force 면 전부).
    prune(관리자 토큰 — 전체 목록이 보임)이면 MNT-PAAS 에 더는 없는 클러스터의 보관분을 지운다."""
    items = _paas("/clusters", authz).json().get("items", [])
    out = []
    for it in items:
        cid = it.get("id")
        if not cid:
            continue
        name, st = it.get("name") or cid, it.get("status") or ""
        known = _clusters.get(cid)
        row = {"id": cid, "name": name, "status": st, "connected": known is not None, "error": None}
        if _usable(st) and (force or known is None or _stale(known)):
            try:
                _fetch_cluster(cid, authz, name)
                row["connected"] = True
            except HTTPException as e:
                row["error"] = e.detail
            except Exception as e:  # noqa: BLE001
                row["error"] = f"{type(e).__name__}: {e}"
        out.append(row)
    if prune:
        alive = {it.get("id") for it in items}
        for cid in [c for c in list(_clusters) if c not in alive]:
            with _clusters_lock:
                _clusters.pop(cid, None)
            try:
                _store_write(cid, None)
            except Exception:  # noqa: BLE001
                pass
    _refresh_ws_index(force=True)
    return out


def _sync_quiet(authz: str, prune: bool) -> None:
    try:
        _sync_clusters(authz, prune=prune)
    except Exception as e:  # noqa: BLE001 — 로그인 뒤 백그라운드 — 실패해도 다음 로그인에 다시 시도
        print(f"[mnt-ainb] 서비스 클러스터 접속 정보 받기 실패: {getattr(e, 'detail', e)}")


# ── 워크스페이스(네임스페이스) → 클러스터 ────────────────────────────
_ws_index: dict[str, str] = {}
_ws_index_at = 0.0
_ws_index_lock = threading.Lock()


def _refresh_ws_index(force: bool = False) -> None:
    """워크스페이스가 어느 클러스터에 있는지 — 클러스터마다 Profile 목록으로 만든다(30초 보관)."""
    global _ws_index_at
    if not MULTI or (not force and time.time() - _ws_index_at < 30):
        return
    with _ws_index_lock:
        idx: dict[str, str] = {}
        for cid, c in list(_clusters.items()):
            try:
                for p in c.crd.list_cluster_custom_object(GROUP, VERSION, P_PLURAL, _request_timeout=10)["items"]:
                    idx.setdefault(p["metadata"]["name"], cid)
            except Exception:  # noqa: BLE001 — 닿지 않는 클러스터는 건너뛴다
                pass
        _ws_index.clear()
        _ws_index.update(idx)
        _ws_index_at = time.time()


def _ws_index_loop() -> None:
    while True:
        time.sleep(30)
        try:
            _refresh_ws_index()
        except Exception:  # noqa: BLE001
            pass


def _route_ns(ns: Optional[str]) -> None:
    """이 요청의 이후 호출을 워크스페이스(ns)가 있는 클러스터로 — ?clusterId= 등으로 이미 정해졌으면 그대로."""
    if not MULTI or not ns or _cluster_var.get():
        return
    cid = _ws_index.get(ns)
    if cid is None:
        _refresh_ws_index(force=True)
        cid = _ws_index.get(ns)
    if cid:
        _set_cluster(cid)


if MULTI:
    try:
        _store_load()
    except Exception as _e:  # noqa: BLE001
        print(f"[mnt-ainb] 보관된 서비스 클러스터 접속 정보를 읽지 못했습니다: {_e}")
else:
    _clusters["default"] = _Cluster("default")

app = FastAPI(title="openKcloud CMP Backend", version="0.1.0")


@app.on_event("startup")
def _widen_thread_pool() -> None:
    """동시 처리 한도 확대.

    FastAPI는 동기(def) 핸들러를 스레드풀에서 실행하며 기본 한도가 40이다.
    이 백엔드는 핸들러 대부분이 동기이고 kubectl port-forward 스폰 대기,
    파드 /metrics 조회처럼 수 초씩 스레드를 붙잡는 구간이 있어,
    화면 여러 개를 동시에 열어두면 40개가 모두 점유되고 이후 요청이
    무한정 대기한다(2026-08-07 시연 중 프로파일 생성 무한대기의 원인).
    """
    try:
        import anyio.to_thread
        anyio.to_thread.current_default_thread_limiter().total_tokens = 200
    except Exception:
        pass


# ── 접속 로그 (시각·IP·경로) — "언제 누가 들어왔나" 추적용 ──────────
# 폴링성 GET은 제외하고 의미 있는 요청만 남긴다. 로그인은 계정명까지 기록.
_ACCESS_LOG = _os.environ.get("ACCESS_LOG_FILE", "")   # 비우면 남기지 않는다(포털은 게이트웨이 로그 사용)
_NOISY_PREFIXES = ("/api/v1/metrics", "/api/v1/profiles", "/api/v1/notebooks",
                   "/api/v1/serving", "/api/v1/pipelines", "/api/v1/experiments",
                   "/api/v1/resources", "/api/v1/models", "/api/v1/benchmarks")


@app.middleware("http")
async def _access_log_mw(request: Request, call_next):
    response = await call_next(request)
    try:
        path = request.url.path
        interesting = (
            request.method != "GET"
            or not path.startswith(_NOISY_PREFIXES)
        )
        if _ACCESS_LOG and interesting and path != "/favicon.ico":
            ip = (request.headers.get("cf-connecting-ip")
                  or request.headers.get("x-forwarded-for", "").split(",")[0].strip()
                  or (request.client.host if request.client else "-"))
            ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
            with open(_ACCESS_LOG, "a") as f:
                f.write(f"{ts}  {ip}  {request.method} {path}"
                        f" -> {response.status_code}\n")
    except Exception:
        pass
    return response


app.add_middleware(
    CORSMiddleware,
    allow_origins=CORS_ORIGINS,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# 포털 게이트웨이 경로 — /api/ainb/<x> 로 들어온 요청을 내부 라우트 /api/v1/<x> 로 바꾼다.
API_PREFIX = _os.environ.get("API_PREFIX", "/api/ainb").rstrip("/")


class _ApiPrefix:
    def __init__(self, app, prefix: str):
        self.app, self.prefix = app, prefix

    async def __call__(self, scope, receive, send):
        path = scope.get("path", "")
        if (scope["type"] in ("http", "websocket") and self.prefix
                and (path == self.prefix or path.startswith(self.prefix + "/"))):
            new = "/api/v1" + path[len(self.prefix):]
            scope = dict(scope, path=new, raw_path=new.encode())
        await self.app(scope, receive, send)


app.add_middleware(_ApiPrefix, prefix=API_PREFIX)


class _ClusterRoute:
    """요청의 클러스터를 정한다(PaaS 모드) — ?clusterId=, 아니면 ?namespace=·IDE 경로(/notebook/{ns}/)의 워크스페이스가 있는 클러스터.
    여기서는 보관된 색인만 본다 — 못 찾으면 엔드포인트가 _route_ns·_get_nb 로 다시 찾는다."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if not MULTI or scope["type"] not in ("http", "websocket"):
            await self.app(scope, receive, send)
            return
        from urllib.parse import parse_qs
        qs = parse_qs((scope.get("query_string") or b"").decode(errors="replace"))
        cid = (qs.get("clusterId") or [""])[0]
        if not cid:
            ns = (qs.get("namespace") or [""])[0]
            path = scope.get("path", "")
            if not ns and path.startswith("/notebook/"):
                parts = path.split("/")
                ns = parts[2] if len(parts) > 2 else ""
            cid = _ws_index.get(ns, "") if ns else ""
        tok = _cluster_var.set(cid)
        try:
            await self.app(scope, receive, send)
        finally:
            _cluster_var.reset(tok)


app.add_middleware(_ClusterRoute)


@app.on_event("startup")
def _start_ws_index() -> None:
    if MULTI:
        threading.Thread(target=_ws_index_loop, daemon=True, name="ws-index").start()

# ══════════════════════════════════════════════════════════════════
# Auth — CMP 세션 (HMAC 서명 토큰, HttpOnly 쿠키)
#   포털 로그인 시 발급 → IDE 프록시(/notebook/*)가 검증
#   (운영 전환 시 IAM/oauth2-proxy로 대체되는 자리)
# ══════════════════════════════════════════════════════════════════
import hashlib
import hmac as _hmac
import json as _json_auth
import os
import time as _time_auth

# 세션 서명 키 — 운영(컨테이너)에서는 CMP_SESSION_SECRET(Secret)로 주입해
# 백엔드를 여러 대 띄우거나 재시작해도 로그인이 유지되게 한다
_SECRET_FILE = os.environ.get("CMP_SECRET_FILE", os.path.expanduser("~/.cmp-secret"))
if os.environ.get("CMP_SESSION_SECRET"):
    _SECRET = os.environ["CMP_SESSION_SECRET"].encode()
elif os.path.exists(_SECRET_FILE):
    _SECRET = open(_SECRET_FILE, "rb").read()
else:
    _SECRET = os.urandom(32)
    with open(_SECRET_FILE, "wb") as f:
        f.write(_SECRET)

# 계정 확인은 포털 공통 로그인(MNT-CORE /api/core/auth/login)에 맡긴다 — 이 모듈은 계정을 갖지 않는다.
# 확인되면 IDE 프록시(/notebook/*)용 세션 쿠키만 발급한다(Keycloak·oauth2-proxy 연동 전까지의 방식).
CORE_API_BASE = os.environ.get("CORE_API_BASE", "http://localhost:8000").rstrip("/")
SESSION_COOKIE = "okc_session"
SESSION_TTL = 8 * 3600


def _b64e(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).decode().rstrip("=")


def _b64d(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def _issue_token(username: str, role: str) -> str:
    payload = _json_auth.dumps({"sub": username, "role": role,
                                "exp": int(_time_auth.time()) + SESSION_TTL})
    body = _b64e(payload.encode())
    sig = _b64e(_hmac.new(_SECRET, body.encode(), hashlib.sha256).digest())
    return f"{body}.{sig}"


def _verify_token(token: Optional[str]) -> Optional[dict]:
    if not token or "." not in token:
        return None
    body, sig = token.rsplit(".", 1)
    good = _b64e(_hmac.new(_SECRET, body.encode(), hashlib.sha256).digest())
    if not _hmac.compare_digest(sig, good):
        return None
    try:
        payload = _json_auth.loads(_b64d(body))
    except Exception:
        return None
    if payload.get("exp", 0) < _time_auth.time():
        return None
    return payload


class LoginBody(BaseModel):
    username: str
    password: str


# ── port-forward 관리 (노트북별 고정 포트) ─────────────────────────
_forwards: dict[str, subprocess.Popen] = {}


def _fw_key(ns: str, name: str) -> str:
    """port-forward 구분 키 — PaaS 모드는 클러스터마다 같은 이름이 있을 수 있어 클러스터 ID 를 붙인다."""
    return f"{_cid()}/{ns}/{name}" if MULTI else f"{ns}/{name}"


def _port_for(ns: str, name: str) -> int:
    return 20000 + zlib.crc32(_fw_key(ns, name).encode()) % 10000


def _port_open(port: int) -> bool:
    import socket
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex(("127.0.0.1", port)) == 0


_fw_verified: dict[str, float] = {}   # key → 터널 정상 확인 시각(monotonic)


def _tunnel_alive(port: int) -> bool:
    """포워딩 터널이 실제로 응답하는지 확인.

    kubectl은 터널(원격 노드 구간)이 죽어도 로컬 포트는 계속 열어두므로
    _port_open만으로는 죽은 통로를 못 걸러낸다 — 원격(WSL) 노드에서
    실제로 겪은 문제. 어떤 상태코드든 HTTP 응답이 오면 터널은 산 것."""
    try:
        httpx.head(f"http://127.0.0.1:{port}/", timeout=2.5)
        return True
    except Exception:
        return False


def _ensure_forward(ns: str, name: str) -> int:
    """port-forward 프로세스를 보장하고, 터널 응답까지 확인 후 포트를 돌려준다.
    Pod이 아직 Ready 전이면 스폰이 곧바로 죽을 수 있어 재시도한다."""
    import time
    port = _port_for(ns, name)
    key = _fw_key(ns, name)
    for _ in range(3):
        proc = _forwards.get(key)
        if proc and proc.poll() is None and _port_open(port):
            # 매 요청마다 찔러보면 느려지므로 5초에 한 번만 실검사
            if time.monotonic() - _fw_verified.get(key, 0) < 5:
                return port
            if _tunnel_alive(port):
                _fw_verified[key] = time.monotonic()
                return port
            proc.terminate()          # 죽은 터널 — 재생성
            _forwards.pop(key, None)
            proc = None
        if proc and proc.poll() is None:
            time.sleep(1.0)   # 스폰 직후 리스닝 대기
            if _port_open(port):
                return port
            proc.terminate()
        _forwards[key] = subprocess.Popen(
            ["kubectl", "-n", ns, "port-forward", "--address", "0.0.0.0",
             f"svc/{name}", f"{port}:80"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=_kenv(),
        )
        time.sleep(1.5)
    if not _port_open(port):
        raise HTTPException(503, f"notebook {ns}/{name} 접속 준비 중 — 잠시 후 다시 시도하세요")
    return port


def _stop_forward(ns: str, name: str) -> None:
    _fw_verified.pop(_fw_key(ns, name), None)
    proc = _forwards.pop(_fw_key(ns, name), None)
    if proc and proc.poll() is None:
        proc.terminate()


# ── 상태 판별 (+사용자 안내 사유) ──────────────────────────────────
def _friendly_reason(pod) -> str:
    """Pending/Starting Pod의 이유를 사용자 언어로 변환."""
    try:
        evs = core.list_namespaced_event(
            pod.metadata.namespace,
            field_selector=f"involvedObject.name={pod.metadata.name}").items
        evs.sort(key=lambda e: e.last_timestamp or e.event_time or e.metadata.creation_timestamp,
                 reverse=True)
        for e in evs[:5]:
            msg = e.message or ""
            if f"Insufficient {ACCEL_RESOURCE}" in msg:
                return "GPU 부족 — 모든 GPU가 사용 중입니다. 다른 노트북을 정지해 자리를 만들거나 GPU 없이 생성하세요"
            if "Insufficient cpu" in msg or "Insufficient memory" in msg:
                return "CPU/메모리 부족 — 요청 자원을 줄이거나 다른 워크로드를 정리하세요"
            if "exceeded quota" in msg:
                return "워크스페이스 할당량 초과 — Profile 쿼터를 확인하세요"
            if e.reason == "Pulling" or "Pulling image" in msg:
                return "컨테이너 이미지 다운로드 중 — 처음 쓰는 이미지는 수 분 걸릴 수 있습니다"
            if e.reason in ("Failed", "ErrImagePull", "ImagePullBackOff"):
                return "이미지를 가져오지 못했습니다 — 이미지 이름/레지스트리 확인 필요"
        if pod.status.phase == "Pending":
            return "스케줄 대기 중"
    except Exception:
        pass
    return ""


def _status(nb: dict) -> tuple[str, str]:
    """(상태, 사유) 반환."""
    if nb["metadata"].get("annotations", {}).get(STOP_ANNOTATION):
        return "Stopped", ""
    ns, name = nb["metadata"]["namespace"], nb["metadata"]["name"]
    try:
        pod = core.read_namespaced_pod(f"{name}-0", ns)
        phase = pod.status.phase
        if phase == "Running" and all(cs.ready for cs in pod.status.container_statuses or []):
            return "Running", ""
        if phase in ("Pending", "Running"):
            return "Starting", _friendly_reason(pod)
        if phase == "Failed":
            return "Error", _friendly_reason(pod)
        return "Starting", _friendly_reason(pod)
    except client.exceptions.ApiException:
        return "Starting", "컨테이너 생성 준비 중"


def _accel(resources: dict) -> Optional[dict]:
    for key, count in (resources.get("limits") or resources.get("requests") or {}).items():
        if key.endswith(("/gpu", "/npu", "/atom", "/pim")) or key.startswith(
            ("nvidia.com", "rebellions.ai", "samsung.com")
        ):
            return {"kind": key, "count": int(count)}
    return None


ACCEL_LABEL = {"nvidia.com": "GPU", "rebellions.ai": "NPU", "samsung.com": "PIM"}


def _to_kf_notebook(nb: dict) -> dict:
    """프론트 목록 화면의 KFNotebook 타입에 맞춘 평탄 모델."""
    meta, spec = nb["metadata"], nb["spec"]["template"]["spec"]
    c = spec["containers"][0]
    req = (c.get("resources") or {}).get("requests", {})
    accel = _accel(c.get("resources") or {})
    kind = ACCEL_LABEL.get(accel["kind"].split("/")[0], "GPU") if accel else None
    storage_gb = 0
    for v in spec.get("volumes", []):
        if v.get("persistentVolumeClaim"):
            try:
                pvc = core.read_namespaced_persistent_volume_claim(
                    v["persistentVolumeClaim"]["claimName"], meta["namespace"])
                storage_gb = int(pvc.spec.resources.requests["storage"].rstrip("Gi"))
            except Exception:
                pass
    mem = req.get("memory", "0Gi")
    return {
        "id": meta["name"],
        "name": meta["name"],
        "ownerId": meta.get("annotations", {}).get("openkcloud.io/owner", "u-000"),
        "namespace": meta["namespace"],
        "image": c["image"],
        "cpu": str(req.get("cpu", "0")),
        "memGb": int(str(mem).rstrip("GiM")) if str(mem).rstrip("GiM").isdigit() else 0,
        **({"acceleratorKind": kind} if kind else {}),
        "acceleratorCount": accel["count"] if accel else 0,
        "storageGb": storage_gb,
        "status": (_st := _status(nb))[0],
        "statusReason": _st[1],
        "url": f"/ai-development/notebooks/{meta['name']}",
        "createdAt": meta["creationTimestamp"][:10],
        "lastActivity": meta["creationTimestamp"][:10],
    }


# ── API 모델 ───────────────────────────────────────────────────────
class CreateNotebook(BaseModel):
    name: str
    namespace: str = DEFAULT_NS
    image: str = DEFAULT_IMAGE
    cpu: str = "1"
    memoryGi: int = 2
    acceleratorKind: Optional[str] = None   # 예: nvidia.com/gpu
    acceleratorCount: int = 0
    storageGb: int = 0


# ── 엔드포인트 ─────────────────────────────────────────────────────
@app.get("/api/v1/notebooks")
def list_notebooks(namespace: Optional[str] = None):
    if namespace:
        _route_ns(namespace)
        items = crd.list_namespaced_custom_object(GROUP, VERSION, namespace, PLURAL)["items"]
        return {"items": [_tag(_to_kf_notebook(nb)) for nb in items]}
    out = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                items = crd.list_cluster_custom_object(GROUP, VERSION, PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue                 # 닿지 않는 클러스터는 건너뛴다
            out += [_tag(_to_kf_notebook(nb)) for nb in items]
    return {"items": out}


def _get_nb(name: str, namespace: str) -> dict:
    """지정 네임스페이스에서 찾고, 없으면 이름으로 전체 네임스페이스 탐색.
    (프론트가 namespace를 넘기지 않는 경로 대응 — id=name 전역 유일 가정)
    PaaS 모드는 알려진 클러스터를 차례로 보고, 찾은 클러스터를 이 요청의 이후 호출 대상으로 정한다."""
    _route_ns(namespace)
    for cid in _search_cids():
        _set_cluster(cid)
        try:
            return crd.get_namespaced_custom_object(GROUP, VERSION, namespace, PLURAL, name)
        except client.exceptions.ApiException as e:
            if e.status != 404:
                if not MULTI:
                    raise
                continue
        try:
            for nb in crd.list_cluster_custom_object(GROUP, VERSION, PLURAL)["items"]:
                if nb["metadata"]["name"] == name:
                    return nb
        except client.exceptions.ApiException:
            if not MULTI:
                raise
    raise HTTPException(404, f"notebook {name} not found in any namespace")


@app.get("/api/v1/notebooks/{name}")
def get_notebook(name: str, namespace: str = DEFAULT_NS):
    return _to_kf_notebook(_get_nb(name, namespace))


@app.post("/api/v1/notebooks", status_code=201)
def create_notebook(body: CreateNotebook):
    _route_ns(body.namespace)            # 워크스페이스가 있는 클러스터에 만든다
    resources: dict = {
        "requests": {"cpu": body.cpu, "memory": f"{body.memoryGi}Gi"},
        "limits": {},
    }
    if body.acceleratorKind and body.acceleratorCount > 0:
        resources["requests"][body.acceleratorKind] = str(body.acceleratorCount)
        resources["limits"][body.acceleratorKind] = str(body.acceleratorCount)

    volumes, mounts = [], []
    if body.storageGb > 0:
        pvc_name = f"{body.name}-vol"
        # 같은 이름의 볼륨이 남아 있으면 재사용 (삭제 시 보존한 데이터 복구)
        try:
            core.read_namespaced_persistent_volume_claim(pvc_name, body.namespace)
            volumes = [{"name": "workspace",
                        "persistentVolumeClaim": {"claimName": pvc_name}}]
            mounts = [{"name": "workspace", "mountPath": "/home/jovyan"}]
        except client.exceptions.ApiException:
            pass
    if body.storageGb > 0 and not volumes:
        core.create_namespaced_persistent_volume_claim(
            body.namespace,
            client.V1PersistentVolumeClaim(
                metadata=client.V1ObjectMeta(name=pvc_name),
                spec=client.V1PersistentVolumeClaimSpec(
                    access_modes=["ReadWriteOnce"],
                    resources=client.V1ResourceRequirements(
                        requests={"storage": f"{body.storageGb}Gi"}),
                ),
            ),
        )
        volumes = [{"name": "workspace",
                    "persistentVolumeClaim": {"claimName": pvc_name}}]
        mounts = [{"name": "workspace", "mountPath": "/home/jovyan"}]

    # POD_HTTP_PROXY 설정 시(로컬 WSL egress 우회) 프록시 env 주입, 아니면 없음
    proxy_env = ([
        {"name": "HTTP_PROXY", "value": POD_HTTP_PROXY},
        {"name": "HTTPS_PROXY", "value": POD_HTTP_PROXY},
        {"name": "http_proxy", "value": POD_HTTP_PROXY},
        {"name": "https_proxy", "value": POD_HTTP_PROXY},
        {"name": "NO_PROXY", "value": POD_NO_PROXY},
        {"name": "no_proxy", "value": POD_NO_PROXY},
    ] if POD_HTTP_PROXY else [])
    nb = {
        "apiVersion": f"{GROUP}/{VERSION}",
        "kind": "Notebook",
        "metadata": {"name": body.name, "namespace": body.namespace},
        "spec": {"template": {"spec": {
            # 학습 Job·서빙과 동일하게 DEV_PIN_NODE 적용 — 없으면 스케줄러가
            # 다른 노드로 보내는데, WSL 크로스노드 스트리밍이 불안정해
            # 노트북 접속이 됐다 안 됐다 한다
            **({"nodeSelector": _pin_selector()} if DEV_PIN_NODE else {}),
            # GPU 요청 시 nvidia 컨테이너 런타임을 지정해야 실제 디바이스가
            # 컨테이너에 노출된다 (k3s + nvidia-container-runtime)
            **({"runtimeClassName": GPU_RUNTIME_CLASS}
               if body.acceleratorCount > 0 and GPU_RUNTIME_CLASS else {}),
            "containers": [{
                "name": "notebook",
                "image": body.image,
                **({"env": proxy_env} if proxy_env else {}),
                "resources": resources,
                **({"volumeMounts": mounts} if mounts else {}),
            }],
            **({"volumes": volumes} if volumes else {}),
        }}},
    }
    crd.create_namespaced_custom_object(GROUP, VERSION, body.namespace, PLURAL, nb)
    return {"id": body.name, "namespace": body.namespace, "status": "Starting"}


def _base_url(request: Optional[Request]) -> str:
    """요청이 들어온 호스트를 기준으로 접속 주소를 만든다.
    (localhost로 들어오면 localhost, LAN IP로 들어오면 그 IP — WSL 환경에서
     호스트 자신은 자기 LAN IP로 WSL 서비스에 도달하지 못하므로 필수)"""
    if request is not None:
        fwd_host = request.headers.get("x-forwarded-host")
        host = fwd_host or request.headers.get("host") or ""
        if host:
            hostname = host.split(":")[0]
            # 외부 터널(단일 입구, https) 경유면 포트를 붙이지 않는다 —
            # 밖에서는 8000이 따로 열려 있지 않고 입구가 경로로 라우팅한다
            proto = (request.headers.get("x-forwarded-proto")
                     or request.url.scheme)
            if proto == "https" or hostname.endswith(".trycloudflare.com"):
                return f"https://{hostname}"
            # 인그레스·리버스 프록시 경유(클러스터 배포) — 브라우저가 연 주소 그대로
            if fwd_host:
                return f"{proto}://{fwd_host}"
            # 개발 환경 — 포털이 백엔드 포트로 직접 부른다
            from urllib.parse import urlparse as _urlparse
            port = _urlparse(PUBLIC_BASE_URL).port
            return f"{proto}://{hostname}:{port}" if port else f"{proto}://{hostname}"
    return PUBLIC_BASE_URL


@app.get("/api/v1/notebooks/{name}/connect")
def connect(name: str, request: Request, namespace: str = DEFAULT_NS):
    nb = _get_nb(name, namespace)
    namespace = nb["metadata"]["namespace"]
    c = nb["spec"]["template"]["spec"]["containers"][0]
    req = (c.get("resources") or {}).get("requests", {})
    pvc = next((v["persistentVolumeClaim"]["claimName"]
                for v in nb["spec"]["template"]["spec"].get("volumes", [])
                if v.get("persistentVolumeClaim")), None)
    _ensure_forward(namespace, name)
    # 모든 IDE 접속은 백엔드(8000) 리버스 프록시 경유 — 브라우저는 8000만 알면 됨.
    # 호스트명은 포털 API 폴링이 실제 성공 중인 'localhost'와 동일하게 맞춘다.
    base = _base_url(request)
    if "codeserver" in c["image"]:
        connect_url = f"{base}/notebook/{namespace}/{name}/"
    else:
        connect_url = f"{base}/notebook/{namespace}/{name}/lab"
    return {
        "namespace": namespace,
        "name": name,
        "status": _status(nb)[0],
        "image": c["image"],
        "resources": {
            "cpu": str(req.get("cpu", "0")),
            "memory": req.get("memory", "0Gi"),
            **({"accelerator": _accel(c.get("resources") or {})}
               if _accel(c.get("resources") or {}) else {}),
        },
        "workspaceVolume": {"pvcName": pvc or "-",
                            "sizeGb": _pvc_size_gb(namespace, pvc),
                            "mountPath": "/home/jovyan"},
        "connectUrl": connect_url,
        "connectMode": "live",
    }


@app.post("/api/v1/notebooks/{name}/stop")
def stop(name: str, namespace: str = DEFAULT_NS):
    namespace = _get_nb(name, namespace)["metadata"]["namespace"]
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    crd.patch_namespaced_custom_object(
        GROUP, VERSION, namespace, PLURAL, name,
        {"metadata": {"annotations": {STOP_ANNOTATION: ts}}})
    _stop_forward(namespace, name)
    return {"status": "Stopped"}


@app.post("/api/v1/notebooks/{name}/start")
def start(name: str, namespace: str = DEFAULT_NS):
    namespace = _get_nb(name, namespace)["metadata"]["namespace"]
    crd.patch_namespaced_custom_object(
        GROUP, VERSION, namespace, PLURAL, name,
        {"metadata": {"annotations": {STOP_ANNOTATION: None}}})
    return {"status": "Starting"}


@app.post("/api/v1/notebooks/{name}/restart")
def restart(name: str, namespace: str = DEFAULT_NS):
    namespace = _get_nb(name, namespace)["metadata"]["namespace"]
    try:
        core.delete_namespaced_pod(f"{name}-0", namespace)
    except client.exceptions.ApiException:
        pass
    return {"status": "Starting"}


@app.delete("/api/v1/notebooks/{name}")
def delete(name: str, namespace: str = DEFAULT_NS, keepVolume: bool = False):
    """keepVolume=true면 작업 데이터(PVC)를 남긴다 — 같은 이름으로 재생성 시 재사용."""
    namespace = _get_nb(name, namespace)["metadata"]["namespace"]
    crd.delete_namespaced_custom_object(GROUP, VERSION, namespace, PLURAL, name)
    _stop_forward(namespace, name)
    if not keepVolume:
        try:
            core.delete_namespaced_persistent_volume_claim(f"{name}-vol", namespace)
        except client.exceptions.ApiException:
            pass
    return {"status": "Deleted", "volumeKept": keepVolume}


# ══════════════════════════════════════════════════════════════════
# Profiles — Kubeflow Profile Controller 연동
#   Profile CR 생성 → 컨트롤러가 Namespace·ResourceQuota(kf-resource-quota)·
#   RoleBinding·ServiceAccount 자동 프로비저닝
# ══════════════════════════════════════════════════════════════════
P_PLURAL = "profiles"


def _qty_to_num(s: str) -> float:
    """K8s Quantity → 숫자(Gi 단위 메모리/스토리지는 GB, cpu는 코어)."""
    s = str(s)
    units = {"Ki": 1 / 1024 / 1024, "Mi": 1 / 1024, "Gi": 1, "Ti": 1024,
             "m": 0.001, "k": 1000}
    for u, f in units.items():
        if s.endswith(u):
            return float(s[: -len(u)]) * f
    return float(s)


def _quota_pair(hard: dict, used: dict, keys: list[str]) -> dict:
    h = sum(_qty_to_num(hard[k]) for k in keys if k in hard)
    u = sum(_qty_to_num(used.get(k, "0")) for k in keys if k in hard)
    return {"used": round(u, 1), "limit": round(h, 1)}


ACCEL_QUOTA_KEYS = [
    f"requests.{ACCEL_RESOURCE}", ACCEL_RESOURCE,
    "requests.rebellions.ai/atom", "requests.samsung.com/pim",
]


def _parse_cpu(v: str) -> float:
    """쿠버네티스 CPU 표기를 코어 수로.

    m(밀리)·u(마이크로)·n(나노) 단위가 섞여 온다. (예: '4980609n' → 0.005)
    """
    v = str(v or "0").strip()
    for suffix, div in (("n", 1e9), ("u", 1e6), ("m", 1e3)):
        if v.endswith(suffix):
            try:
                return float(v[:-1]) / div
            except ValueError:
                return 0.0
    try:
        return float(v)
    except ValueError:
        return 0.0


def _parse_mem(v: str) -> float:
    """쿠버네티스 메모리 표기를 GB로 (예: '6372Mi' → 6.22)."""
    v = str(v or "0").strip()
    units = {"Ki": 1 / 1024 / 1024, "Mi": 1 / 1024, "Gi": 1.0, "Ti": 1024.0}
    for u, f in units.items():
        if v.endswith(u):
            try:
                return float(v[:-2]) * f
            except ValueError:
                return 0.0
    try:
        return float(v) / (1024 ** 3)
    except ValueError:
        return 0.0


def _ns_actual_usage(ns: str) -> dict:
    """지금 실제로 쓰고 있는 양 (예약량이 아니라 실사용).

    예약은 '최대 이만큼 쓰겠다'는 약속이라 실제 사용과 크게 다를 수 있다.
    (예: 12GB를 예약하고 6GB만 쓰는 경우)
    """
    try:
        data = crd.list_namespaced_custom_object(
            "metrics.k8s.io", "v1beta1", ns, "pods")
    except Exception:
        return {}
    cpu = mem = 0.0
    for p in data.get("items", []):
        for c in p.get("containers", []):
            u = c.get("usage") or {}
            cpu += _parse_cpu(u.get("cpu", "0"))
            mem += _parse_mem(u.get("memory", "0"))
    return {"cpu": round(cpu, 2), "mem": round(mem, 1)}


def _ns_deployment_count(ns: str) -> int:
    """그 워크스페이스에서 도는 서비스 수.

    KServe InferenceService와 일반 Deployment를 모두 센다. 노트북이 없는데
    자원 사용량만 잡히는 경우(예: 추론 서버만 있는 네임스페이스)를 설명해준다.
    """
    n = 0
    try:
        n += len([s for s in crd.list_namespaced_custom_object(
            KSERVE_GROUP, KSERVE_VERSION, ns, I_PLURAL)["items"]])
    except Exception:
        pass
    try:
        apps = _apps()
        for d in apps.list_namespaced_deployment(ns).items:
            # KServe가 만든 predictor Deployment는 위에서 이미 셌다
            if (d.metadata.labels or {}).get("component") == "predictor":
                continue
            n += 1
    except Exception:
        pass
    return n


def _ns_workflow_count(ns: str) -> int:
    """그 워크스페이스에서 도는 파이프라인 작업 수."""
    try:
        return len([w for w in crd.list_namespaced_custom_object(
            "argoproj.io", "v1alpha1", ns, "workflows")["items"]
            if (w.get("status", {}).get("phase") or "") == "Running"])
    except Exception:
        return 0


def _to_profile(p: dict) -> dict:
    name = p["metadata"]["name"]
    owner = p.get("spec", {}).get("owner", {}).get("name", "-")
    hard, used = {}, {}
    # 쿠버네티스 클라이언트가 오류 응답을 처리하다 자체 예외를 내는 경우가 있어
    # (ApiException이 아닌 AttributeError) 예외 종류를 넓게 잡는다.
    # 한 워크스페이스 조회가 실패해도 목록 전체가 깨지면 안 된다.
    try:
        rq = core.read_namespaced_resource_quota("kf-resource-quota", name)
        hard = {k: str(v) for k, v in (rq.status.hard or {}).items()}
        used = {k: str(v) for k, v in (rq.status.used or {}).items()}
    except Exception:
        pass
    try:
        nb_count = len(crd.list_namespaced_custom_object(
            GROUP, VERSION, name, PLURAL)["items"])
    except Exception:
        nb_count = 0
    plugins = ["PodDefaults"]
    if any(k in hard for k in ACCEL_QUOTA_KEYS if "nvidia" in k):
        plugins.append("GPU")
    if any(k in hard for k in ACCEL_QUOTA_KEYS if "rebellions" in k):
        plugins.append("NPU")
    meta = p.get("metadata", {})
    return {
        "name": name,
        # 포털 프로젝트(OpenStack 프로젝트 UUID) — AI 워크스페이스 = 프로젝트 1:1. 라벨 없는 옛 워크스페이스는 None
        "projectId": (meta.get("labels") or {}).get(PROJECT_LABEL),
        "projectName": (meta.get("annotations") or {}).get(PROJECT_NAME_ANNOTATION),
        "owner": owner.split("@")[0],
        "ownerEmail": owner,
        "members": 1,
        "cpuQuota": _quota_pair(hard, used, ["cpu", "requests.cpu"]),
        "memQuota": _quota_pair(hard, used, ["memory", "requests.memory"]),
        # 예약량과 별개로 지금 실제 쓰는 양
        "actual": _ns_actual_usage(name),
        "accQuota": _quota_pair(hard, used, ACCEL_QUOTA_KEYS),
        "storageQuota": _quota_pair(hard, used, ["requests.storage"]),
        "plugins": plugins,
        "notebooks": nb_count,
        # 노트북 말고도 자원을 쓰는 것들 — 이 값이 0이면 "쓰는 게 없는데
        # 왜 사용량이 잡히지?" 하고 오해하게 된다
        "pipelines": _ns_workflow_count(name),
        "deployments": _ns_deployment_count(name),
        "status": "Active" if hard else "Provisioning",
    }


class CreateProfile(BaseModel):
    name: str
    ownerEmail: str
    cpu: int = 8
    memoryGi: int = 16
    acceleratorKey: str = ACCEL_RESOURCE
    acceleratorCount: int = 0
    storageGi: int = 50
    projectId: Optional[str] = None     # 포털 프로젝트 UUID (자원신청 프로비저닝이 채운다)
    projectName: Optional[str] = None
    clusterId: Optional[str] = None     # PaaS 모드 — 만들 서비스 클러스터(비우면 지금 클러스터)


@app.get("/api/v1/profiles")
def list_profiles():
    out = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                items = crd.list_cluster_custom_object(GROUP, VERSION, P_PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue
            out += [_tag(_to_profile(p)) for p in items]
    return {"items": out}


def _find_profile(name: str) -> Optional[dict]:
    """Profile 을 찾는다 — PaaS 모드는 클러스터를 차례로 보고, 찾은 클러스터를 이 요청의 이후 호출 대상으로 정한다."""
    _route_ns(name)
    for cid in _search_cids():
        _set_cluster(cid)
        try:
            return crd.get_cluster_custom_object(GROUP, VERSION, P_PLURAL, name)
        except client.exceptions.ApiException as e:
            if e.status != 404 and not MULTI:
                raise
    return None


@app.get("/api/v1/profiles/{name}")
def get_profile(name: str):
    p = _find_profile(name)
    if p is None:
        raise HTTPException(404, f"profile {name} not found")
    return _tag(_to_profile(p))


def _adopt_namespace(name: str, project_id: Optional[str], owner: str) -> None:
    """같은 이름의 네임스페이스가 이미 있으면(클러스터 신청이 미리 만든 프로젝트 네임스페이스) 워크스페이스로 넘겨받는다.
    같은 프로젝트 라벨일 때만 — Kubeflow Profile 컨트롤러는 네임스페이스의 owner 주석이 Profile 소유자와 같아야 넘겨받는다."""
    try:
        ns = core.read_namespace(name)
    except client.exceptions.ApiException as e:
        if e.status == 404:
            return
        raise
    if not project_id or (ns.metadata.labels or {}).get(PROJECT_LABEL) != project_id:
        raise HTTPException(409, f"'{name}' 네임스페이스가 이미 있습니다(다른 용도)")
    core.patch_namespace(name, {"metadata": {"annotations": {"owner": owner}}})


@app.post("/api/v1/profiles", status_code=201)
def create_profile(body: CreateProfile):
    if MULTI and body.clusterId:
        _ensure_cluster(body.clusterId)
        _set_cluster(body.clusterId)
    hard = {
        "cpu": str(body.cpu),
        "memory": f"{body.memoryGi}Gi",
        "requests.storage": f"{body.storageGi}Gi",
    }
    if body.acceleratorCount > 0:
        hard[f"requests.{body.acceleratorKey}"] = str(body.acceleratorCount)
    metadata: dict = {"name": body.name}
    if body.projectId:
        metadata["labels"] = {PROJECT_LABEL: body.projectId}
        metadata["annotations"] = {PROJECT_NAME_ANNOTATION: body.projectName or ""}
    profile = {
        "apiVersion": f"{GROUP}/{VERSION}",
        "kind": "Profile",
        "metadata": metadata,
        "spec": {
            "owner": {"kind": "User", "name": body.ownerEmail},
            "resourceQuotaSpec": {"hard": hard},
        },
    }
    _adopt_namespace(body.name, body.projectId, body.ownerEmail)
    crd.create_cluster_custom_object(GROUP, VERSION, P_PLURAL, profile)
    _ws_index[body.name] = _cid()
    return _tag({"name": body.name, "status": "Provisioning"})


class UpdateProfile(BaseModel):
    ownerEmail: Optional[str] = None
    cpu: Optional[int] = None
    memoryGi: Optional[int] = None
    acceleratorKey: str = ACCEL_RESOURCE
    acceleratorCount: Optional[int] = None
    storageGi: Optional[int] = None


@app.patch("/api/v1/profiles/{name}")
def update_profile(name: str, body: UpdateProfile):
    """쿼터·소유자 변경 — 컨트롤러가 ResourceQuota를 즉시 반영한다."""
    try:
        cur = _find_profile(name)
    except client.exceptions.ApiException as e:
        raise HTTPException(500, f"profile {name} 조회 실패: {e.reason}")
    if cur is None:
        raise HTTPException(404, f"profile {name} not found")

    hard = dict((cur.get("spec", {}).get("resourceQuotaSpec", {})
                 .get("hard", {})) or {})
    if body.cpu is not None:
        hard["cpu"] = str(body.cpu)
    if body.memoryGi is not None:
        hard["memory"] = f"{body.memoryGi}Gi"
    if body.storageGi is not None:
        hard["requests.storage"] = f"{body.storageGi}Gi"
    if body.acceleratorCount is not None:
        key = f"requests.{body.acceleratorKey}"
        if body.acceleratorCount > 0:
            hard[key] = str(body.acceleratorCount)
        else:
            hard.pop(key, None)

    spec: dict = {"resourceQuotaSpec": {"hard": hard}}
    if body.ownerEmail:
        spec["owner"] = {"kind": "User", "name": body.ownerEmail}

    crd.patch_cluster_custom_object(GROUP, VERSION, P_PLURAL, name,
                                    {"spec": spec})

    # 쿼터 키를 지운 경우 patch로는 제거되지 않으므로 ResourceQuota를 직접 맞춘다
    try:
        rq = core.read_namespaced_resource_quota("kf-resource-quota", name)
        rq.spec.hard = hard
        core.replace_namespaced_resource_quota("kf-resource-quota", name, rq)
    except client.exceptions.ApiException:
        pass

    return {"name": name, "status": "Updated", "hard": hard}


@app.delete("/api/v1/profiles/{name}")
def delete_profile(name: str):
    """⚠ Profile 삭제는 네임스페이스와 그 안의 노트북·PVC까지 함께 삭제한다.
    (클러스터 신청이 미리 만들어 넘겨받은 네임스페이스는 Profile 만 지워지고 네임스페이스는 남는다)"""
    if _find_profile(name) is None:
        raise HTTPException(404, f"profile {name} not found")
    try:
        crd.delete_cluster_custom_object(GROUP, VERSION, P_PLURAL, name)
    except client.exceptions.ApiException as e:
        if e.status == 404:
            raise HTTPException(404, f"profile {name} not found")
        raise
    return {"status": "Deleting"}


# ══════════════════════════════════════════════════════════════════
# AI 워크스페이스 = 프로젝트 1:1 (CR-AID-12)
#   신청·승인은 포털 자원신청(MNT-CORE 티켓, type ai-workspace)이 맡고, 승인 후 프로비저닝
#   워크플로가 이 모듈의 Temporal 작업(app/workflows/activities.py)으로 Profile 을 만든다.
#   Profile 에 프로젝트 ID 라벨을 달아 포털 프로젝트와 잇는다.
# ══════════════════════════════════════════════════════════════════
import re as _re_ws

PROJECT_LABEL = "openkcloud.io/project-id"
PROJECT_NAME_ANNOTATION = "openkcloud.io/project-name"
_WS_NAME = _re_ws.compile(r"^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$")


def _profile_exists(name: str) -> bool:
    try:
        crd.get_cluster_custom_object(GROUP, VERSION, P_PLURAL, name)
        return True
    except client.exceptions.ApiException as e:
        if e.status == 404:
            try:   # Profile은 없어도 같은 이름의 네임스페이스가 있으면 만들 수 없다
                core.read_namespace(name)
                return True
            except client.exceptions.ApiException:
                return False
        raise


def _ns_adoptable(name: str, project_id: Optional[str]) -> bool:
    """Profile 없이 같은 프로젝트 라벨의 네임스페이스만 있으면 True — 클러스터 신청이 미리 만든 프로젝트 네임스페이스."""
    if not project_id:
        return False
    try:
        crd.get_cluster_custom_object(GROUP, VERSION, P_PLURAL, name)
        return False
    except client.exceptions.ApiException as e:
        if e.status != 404:
            raise
    try:
        ns = core.read_namespace(name)
    except client.exceptions.ApiException as e:
        if e.status == 404:
            return False
        raise
    return (ns.metadata.labels or {}).get(PROJECT_LABEL) == project_id


@app.get("/api/v1/clusters")
def list_known_clusters():
    """이 모듈이 접속 정보를 가진 서비스 클러스터 — 화면이 모드(single·paas)를 보고 클러스터 선택 칸을 띄운다."""
    return {"mode": "paas" if MULTI else "single",
            "items": [{"id": c.id, "name": c.name, "fetchedAt": c.fetched_at} for c in list(_clusters.values())]}


@app.post("/api/v1/clusters/sync")
def sync_clusters(request: Request, force: bool = False):
    """요청자 토큰으로 쓸 수 있는 서비스 클러스터의 접속 정보를 지금 받아 둔다(PaaS 모드) — 로그인 때도 백그라운드로 한다."""
    if not MULTI:
        return list_known_clusters()
    authz = request.headers.get("authorization", "")
    if not authz.lower().startswith("bearer "):
        raise HTTPException(401, "로그인 토큰(Authorization: Bearer)이 필요합니다")
    return {"mode": "paas", "items": _sync_clusters(authz, force=force)}


@app.get("/api/v1/workspace-name-check")
def workspace_name_check(name: str, request: Request, projectId: Optional[str] = None,
                         clusterId: Optional[str] = None):
    """워크스페이스(=Profile·네임스페이스) 이름 사용 가능 여부 — 포털 자원신청 접수 때 확인용.
    projectId 를 주면 그 프로젝트의 워크스페이스(프로젝트 라벨)가 이미 있는지도 본다(프로젝트당 1개 — PaaS 모드는 모든 클러스터).
    PaaS 모드는 clusterId(만들 클러스터)를 받는다 — 그 클러스터 접속 정보가 없으면 요청자 토큰(Authorization)으로 받아 온다.
    같은 프로젝트 라벨의 네임스페이스만 있으면(클러스터 신청이 미리 만든 것) 넘겨받아 쓰므로 사용 가능으로 본다."""
    if not _WS_NAME.match(name):
        return {"available": False, "reason": "영문 소문자·숫자·하이픈(-) 63자 이내여야 합니다."}
    if MULTI:
        if not clusterId:
            if len(_clusters) != 1:
                return {"available": False, "reason": "AI 워크스페이스를 만들 서비스 클러스터를 선택하세요."}
            clusterId = next(iter(_clusters))
        try:
            _ensure_cluster(clusterId, request.headers.get("authorization"))
        except HTTPException as e:
            return {"available": False, "reason": e.detail}
    if projectId:
        for cid in (list(_clusters) if MULTI else [_cid()]):
            with _use_cluster(cid):
                try:
                    own = crd.list_cluster_custom_object(GROUP, VERSION, P_PLURAL,
                                                         label_selector=f"{PROJECT_LABEL}={projectId}")["items"]
                except Exception:
                    if not MULTI:
                        raise
                    continue
            if own:
                where = f", 클러스터 {_clusters[cid].name}" if MULTI else ""
                return {"available": False,
                        "reason": f"이 프로젝트의 워크스페이스({own[0]['metadata']['name']}{where})가 이미 있습니다."}
    if MULTI:
        _set_cluster(clusterId)
    if _profile_exists(name) and not _ns_adoptable(name, projectId):
        return {"available": False, "reason": f"'{name}' 워크스페이스가 이미 있습니다."}
    return {"available": True, "reason": None}


# ══════════════════════════════════════════════════════════════════
# Pipelines — Kubeflow Pipelines(KFP) v2beta1 REST 프록시
#   백엔드(=k3s 노드 호스트)가 ml-pipeline 서비스 ClusterIP로 직접 접근
# ══════════════════════════════════════════════════════════════════
import httpx

_kfp_bases: dict[str, str] = {}   # 클러스터 ID → KFP API 주소


def _kfp() -> str:
    """지금 클러스터의 KFP API 주소 — 클러스터 안 IP 로 닿으면 ClusterIP, 아니면(다른 클러스터를 조작) port-forward."""
    c = _cur()
    if not c.direct_net:
        return f"http://127.0.0.1:{_ensure_svc_forward('kubeflow', 'ml-pipeline', 8888)}"
    base = _kfp_bases.get(c.id)
    if not base:
        svc = core.read_namespaced_service("ml-pipeline", "kubeflow")
        base = _kfp_bases[c.id] = f"http://{svc.spec.cluster_ip}:8888"
    return base


def _kfp_get(path: str, params: dict | None = None) -> dict:
    r = httpx.get(f"{_kfp()}{path}", params=params, timeout=15)
    r.raise_for_status()
    return r.json()


def _run_pipeline_id(r: dict) -> str:
    return (r.get("pipeline_version_reference") or {}).get("pipeline_id", "")


import re as _re

_steps_cache: dict[str, int] = {}


def _strip_md(text: str) -> str:
    """마크다운 링크 [text](url) → text 로 정리."""
    return _re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text or "").strip()


def _pipeline_steps(pipeline_id: str) -> int:
    """최신 버전의 pipeline_spec에서 root DAG task 수를 센다."""
    if pipeline_id in _steps_cache:
        return _steps_cache[pipeline_id]
    try:
        versions = _kfp_get(
            f"/apis/v2beta1/pipelines/{pipeline_id}/versions", {"page_size": 1}
        ).get("pipeline_versions", [])
        spec = versions[0].get("pipeline_spec", {}) if versions else {}
        n = len(spec.get("root", {}).get("dag", {}).get("tasks", {}))
    except Exception:
        n = 0
    _steps_cache[pipeline_id] = n
    return n


@app.get("/api/v1/pipelines")
def list_pipelines():
    data = _kfp_get("/apis/v2beta1/pipelines", {"page_size": 50})
    all_runs = _kfp_get("/apis/v2beta1/runs", {"page_size": 200}).get("runs", [])
    items = []
    for p in data.get("pipelines", []):
        runs = [r for r in all_runs if _run_pipeline_id(r) == p["pipeline_id"]]
        done = [r for r in runs if r.get("state") in ("SUCCEEDED", "FAILED")]
        ok = [r for r in done if r.get("state") == "SUCCEEDED"]
        items.append({
            "id": p["pipeline_id"],
            "name": p["display_name"],
            "description": _strip_md(p.get("description", "")),
            "ownerId": "u-000",
            "steps": _pipeline_steps(p["pipeline_id"]),
            "runs": len(runs),
            "successRate": round(len(ok) / len(done), 2) if done else 0,
            "lastRun": (runs[0].get("created_at", "")[:10] if runs else ""),
            "status": "Active",
        })
    return {"items": items}


@app.delete("/api/v1/pipelines/{pipeline_id}")
def delete_pipeline(pipeline_id: str, force: bool = False):
    """파이프라인 삭제.

    KFP는 버전이 남아 있거나 실행 기록·예약이 걸려 있으면 삭제를 거부한다.
    force=True면 예약 → 실행 기록 → 버전 순으로 정리한 뒤 삭제한다.
    """
    try:
        info = _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}")
    except httpx.HTTPStatusError as e:
        if e.response.status_code == 404:
            raise HTTPException(404, "파이프라인을 찾을 수 없습니다")
        raise HTTPException(502, f"파이프라인 조회 실패: {e}")

    runs = [r for r in _kfp_get("/apis/v2beta1/runs", {"page_size": 200})
            .get("runs", []) if _run_pipeline_id(r) == pipeline_id]
    scheds = [s for s in _kfp_get("/apis/v2beta1/recurringruns",
                                  {"page_size": 100}).get("recurringRuns", [])
              if (s.get("pipeline_version_reference") or {})
              .get("pipeline_id") == pipeline_id]

    if not force and (runs or scheds):
        raise HTTPException(409, {
            "message": "실행 기록 또는 예약이 남아 있어 삭제할 수 없습니다",
            "name": info.get("display_name", ""),
            "runs": len(runs),
            "schedules": len(scheds),
            "hint": "함께 삭제하려면 force=true로 다시 요청하세요",
        })

    removed = {"schedules": 0, "runs": 0, "versions": 0}
    if force:
        for s in scheds:
            if httpx.delete(f"{_kfp()}/apis/v2beta1/recurringruns/"
                            f"{s['recurring_run_id']}", timeout=20).is_success:
                removed["schedules"] += 1
        for r in runs:
            rid = r["run_id"]
            # 실행 중이면 먼저 중단해야 삭제된다
            if r.get("state") in ("RUNNING", "PENDING"):
                httpx.post(f"{_kfp()}/apis/v2beta1/runs/{rid}:terminate",
                           timeout=20)
            if httpx.delete(f"{_kfp()}/apis/v2beta1/runs/{rid}",
                            timeout=20).is_success:
                removed["runs"] += 1

    # 버전은 항상 먼저 지워야 파이프라인 본체가 삭제된다
    for v in _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}/versions",
                      {"page_size": 100}).get("pipeline_versions", []):
        if httpx.delete(f"{_kfp()}/apis/v2beta1/pipelines/{pipeline_id}"
                        f"/versions/{v['pipeline_version_id']}",
                        timeout=20).is_success:
            removed["versions"] += 1

    r = httpx.delete(f"{_kfp()}/apis/v2beta1/pipelines/{pipeline_id}",
                     timeout=20)
    if not r.is_success:
        raise HTTPException(r.status_code, f"삭제 실패: {r.text[:200]}")
    _steps_cache.pop(pipeline_id, None)
    return {"status": "Deleted", "name": info.get("display_name", ""),
            "removed": removed}


class UploadPipeline(BaseModel):
    name: str
    description: str = ""
    yaml: str


@app.post("/api/v1/pipelines/upload", status_code=201)
def upload_pipeline(body: UploadPipeline):
    r = httpx.post(
        f"{_kfp()}/apis/v2beta1/pipelines/upload",
        params={"name": body.name, "description": body.description},
        files={"uploadfile": (f"{body.name}.yaml", body.yaml.encode(),
                              "application/x-yaml")},
        timeout=30,
    )
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text)
    return r.json()


class CreateRun(BaseModel):
    name: str
    params: dict = {}


@app.post("/api/v1/pipelines/{pipeline_id}/runs", status_code=201)
def create_run(pipeline_id: str, body: CreateRun):
    versions = _kfp_get(
        f"/apis/v2beta1/pipelines/{pipeline_id}/versions", {"page_size": 1}
    ).get("pipeline_versions", [])
    if not versions:
        raise HTTPException(404, "pipeline has no versions")
    payload = {
        "display_name": body.name,
        "pipeline_version_reference": {
            "pipeline_id": pipeline_id,
            "pipeline_version_id": versions[0]["pipeline_version_id"],
        },
        "runtime_config": {"parameters": body.params},
    }
    r = httpx.post(f"{_kfp()}/apis/v2beta1/runs", json=payload, timeout=30)
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text)
    return r.json()


@app.get("/api/v1/pipelines/{pipeline_id}/yaml")
def download_pipeline_yaml(pipeline_id: str):
    """파이프라인 정의(YAML) 내려받기 — 최신 버전의 pipeline_spec."""
    import yaml as _yaml
    versions = _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}/versions",
                        {"page_size": 1, "sort_by": "created_at desc"}
                        ).get("pipeline_versions", [])
    if not versions:
        raise HTTPException(404, "파이프라인 정의를 찾을 수 없습니다")
    v = versions[0]
    spec = v.get("pipeline_spec")
    if not spec:
        raise HTTPException(404, "pipeline_spec이 비어 있습니다")
    name = (v.get("display_name") or pipeline_id).replace(" ", "_")
    body = _yaml.safe_dump(spec, allow_unicode=True, sort_keys=False)
    return Response(
        content=body, media_type="application/x-yaml",
        headers={"Content-Disposition": f'attachment; filename="{name}.yaml"'})


@app.get("/api/v1/pipelines/{pipeline_id}/dag")
def pipeline_dag(pipeline_id: str):
    """파이프라인 정의에서 단계와 의존 관계를 추출 — 화면의 DAG 그림용."""
    versions = _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}/versions",
                        {"page_size": 1, "sort_by": "created_at desc"}
                        ).get("pipeline_versions", [])
    if not versions:
        return {"nodes": [], "note": "정의를 찾을 수 없습니다"}
    spec = versions[0].get("pipeline_spec") or {}
    tasks = ((spec.get("root") or {}).get("dag") or {}).get("tasks") or {}
    if not tasks:
        return {"nodes": []}

    deployment = (spec.get("deploymentSpec") or {}).get("executors") or {}
    comps = spec.get("components") or {}

    nodes = []
    for key, t in tasks.items():
        name = (t.get("taskInfo") or {}).get("name", key)
        deps = t.get("dependentTasks") or []
        # 실행 이미지 추출 (있으면 단계 종류를 추정하는 데 사용)
        image = ""
        cref = (t.get("componentRef") or {}).get("name")
        if cref:
            label = (comps.get(cref) or {}).get("executorLabel")
            if label:
                image = ((deployment.get(label) or {}).get("container") or {}).get("image", "")
        low = f"{name} {image}".lower()
        if any(k in low for k in ("train", "finetune", "fit")):
            kind = "ml"
        elif any(k in low for k in ("deploy", "serve", "register", "kserve")):
            kind = "deploy"
        elif any(k in low for k in ("data", "prep", "load", "token", "clean")):
            kind = "data"
        else:
            kind = "compute"
        nodes.append({"id": key, "name": name, "dependsOn": deps,
                      "image": image, "kind": kind})

    # 의존 관계를 따라 실행 순서(레벨) 계산
    level: dict[str, int] = {}
    for _ in range(len(nodes) + 1):
        for n in nodes:
            deps = [d for d in n["dependsOn"] if d in
                    {x["id"] for x in nodes} | {x["name"] for x in nodes}]
            level[n["id"]] = (0 if not deps
                              else max(level.get(d, 0) for d in deps) + 1)
    for n in nodes:
        n["level"] = level.get(n["id"], 0)
    nodes.sort(key=lambda x: (x["level"], x["name"]))
    return {"nodes": nodes, "levels": (max(level.values()) + 1) if level else 0}


# ── 스케줄 (KFP RecurringRun) ──────────────────────────────────────
class CreateSchedule(BaseModel):
    cron: str = "0 2 * * *"          # 분 시 일 월 요일 (KFP는 6자리 지원)
    name: Optional[str] = None
    maxConcurrency: int = 1
    enabled: bool = True


def _to_cron6(cron: str) -> str:
    """KFP는 초 단위 6자리 cron을 쓴다 — 5자리 입력이면 앞에 0을 붙인다."""
    parts = cron.split()
    return cron if len(parts) == 6 else "0 " + cron


@app.get("/api/v1/pipelines/{pipeline_id}/schedules")
def list_schedules(pipeline_id: str):
    data = _kfp_get("/apis/v2beta1/recurringruns", {"page_size": 100})
    items = []
    for r in data.get("recurringRuns", []):
        ref = r.get("pipeline_version_reference") or {}
        if ref.get("pipeline_id") != pipeline_id:
            continue
        trig = ((r.get("trigger") or {}).get("cron_schedule") or {})
        items.append({
            "id": r["recurring_run_id"],
            "name": r.get("display_name", ""),
            "cron": trig.get("cron", ""),
            "status": r.get("status", ""),          # ENABLED / DISABLED
            "createdAt": (r.get("created_at") or "")[:16].replace("T", " "),
        })
    return {"items": items}


@app.post("/api/v1/pipelines/{pipeline_id}/schedules", status_code=201)
def create_schedule(pipeline_id: str, body: CreateSchedule):
    versions = _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}/versions",
                        {"page_size": 1}).get("pipeline_versions", [])
    if not versions:
        raise HTTPException(404, "파이프라인 버전을 찾을 수 없습니다")
    payload = {
        "display_name": body.name or f"schedule-{_time_auth.strftime('%H%M%S', _time_auth.localtime())}",
        "pipeline_version_reference": {
            "pipeline_id": pipeline_id,
            "pipeline_version_id": versions[0]["pipeline_version_id"],
        },
        "trigger": {"cron_schedule": {"cron": _to_cron6(body.cron)}},
        "max_concurrency": str(body.maxConcurrency),
        "mode": "ENABLE" if body.enabled else "DISABLE",
        "no_catchup": True,
    }
    r = httpx.post(f"{_kfp()}/apis/v2beta1/recurringruns", json=payload, timeout=30)
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text[:300])
    return r.json()


@app.post("/api/v1/schedules/{schedule_id}/toggle")
def toggle_schedule(schedule_id: str, enable: bool = True):
    action = "enable" if enable else "disable"
    r = httpx.post(f"{_kfp()}/apis/v2beta1/recurringruns/{schedule_id}:{action}",
                   timeout=20)
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text[:200])
    return {"status": "ENABLED" if enable else "DISABLED"}


@app.delete("/api/v1/schedules/{schedule_id}")
def delete_schedule(schedule_id: str):
    r = httpx.delete(f"{_kfp()}/apis/v2beta1/recurringruns/{schedule_id}", timeout=20)
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text[:200])
    return {"status": "Deleted"}


@app.get("/api/v1/pipelines/{pipeline_id}/artifacts")
def list_run_artifacts(pipeline_id: str, limit: int = 20):
    """실행 결과 아티팩트 목록 — KFP가 MinIO(mlpipeline 버킷)에 남긴 산출물."""
    runs = [r for r in _kfp_get("/apis/v2beta1/runs", {"page_size": 200}).get("runs", [])
            if _run_pipeline_id(r) == pipeline_id]
    if not runs:
        return {"items": []}
    # KFP는 아티팩트를 artifacts/<워크플로우이름>/... 로 저장한다.
    # 워크플로우 이름은 파이프라인 이름으로 시작하므로 그것으로 매칭한다.
    try:
        pipe = _kfp_get(f"/apis/v2beta1/pipelines/{pipeline_id}")
        pname = pipe.get("display_name", "")
    except Exception:
        pname = ""
    try:
        cli = _minio_client()
    except Exception:
        return {"items": [], "note": "저장소 연결 불가"}
    items = []
    bucket = "mlpipeline"
    run_ids = {r["run_id"] for r in runs}

    # (1) KFP v2 실제 산출물 — v2/artifacts/<파이프라인>/<실행ID>/<단계>/<실행>/<이름>
    #     학습된 모델·데이터셋처럼 우리가 만든 결과물이 여기에 있다.
    try:
        if pname and cli.bucket_exists(bucket):
            for obj in cli.list_objects(bucket, prefix=f"v2/artifacts/{pname}/",
                                        recursive=True):
                parts = obj.object_name.split("/")
                if len(parts) < 7:
                    continue
                run_id, step, name = parts[3], parts[4], parts[-1]
                if run_ids and run_id not in run_ids:
                    continue
                items.append({
                    "name": name,
                    "step": step,
                    "run": run_id[:8],
                    "path": f"{bucket}/{obj.object_name}",
                    "sizeMb": round((obj.size or 0) / 1024 / 1024, 3),
                    "updatedAt": (obj.last_modified.strftime("%Y-%m-%d %H:%M")
                                  if obj.last_modified else ""),
                })
    except Exception:
        pass

    # (2) 단계별 실행 로그 (Argo가 남긴 main.log 등)
    try:
        if cli.bucket_exists(bucket) and len(items) < limit:
            for obj in cli.list_objects(bucket, prefix="artifacts/", recursive=True):
                wf = obj.object_name.split("/")[1] if "/" in obj.object_name else ""
                if pname and not wf.startswith(pname):
                    continue
                # KFP 내부 드라이버 로그는 사용자에게 의미가 없어 제외
                if ("-system-dag-driver-" in obj.object_name
                        or "-system-container-driver-" in obj.object_name):
                    continue
                items.append({
                    "name": obj.object_name.split("/")[-1],
                    "step": "실행 로그",
                    "run": wf,
                    "path": f"{bucket}/{obj.object_name}",
                    "sizeMb": round((obj.size or 0) / 1024 / 1024, 3),
                    "updatedAt": (obj.last_modified.strftime("%Y-%m-%d %H:%M")
                                  if obj.last_modified else ""),
                })
                if len(items) >= limit:
                    break
    except Exception:
        pass
    # 학습 결과물을 먼저, 실행 로그는 뒤로
    items.sort(key=lambda x: (x["step"] == "실행 로그",
                              [-ord(c) for c in x["updatedAt"]]))
    return {"items": items[:limit]}


@app.get("/api/v1/artifacts/download")
def download_artifact(path: str):
    """아티팩트 파일 다운로드 — path 형식: <bucket>/<object key>"""
    if "/" not in path:
        raise HTTPException(400, "잘못된 경로")
    bucket, key = path.split("/", 1)
    cli = _minio_client()
    try:
        resp = cli.get_object(bucket, key)
        data = resp.read()
    except Exception:
        raise HTTPException(404, "파일을 찾을 수 없습니다")
    finally:
        try:
            resp.close(); resp.release_conn()
        except Exception:
            pass
    fname = key.split("/")[-1] or "artifact"
    return Response(content=data, media_type="application/octet-stream",
                    headers={"Content-Disposition": f'attachment; filename="{fname}"'})


@app.get("/api/v1/notebooks/{name}/download")
def download_workspace_file(name: str, path: str, namespace: str = DEFAULT_NS):
    """노트북 작업공간의 파일 하나를 내려받는다."""
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    if ".." in path:
        raise HTTPException(400, "잘못된 경로")
    target = f"/home/jovyan/{path.lstrip('/')}"
    proc = subprocess.run(
        ["kubectl", "-n", ns, "exec", f"{name}-0", "--", "sh", "-c",
         f"cat '{target}'"],
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=300, env=_kenv())
    if proc.returncode != 0 or not proc.stdout:
        raise HTTPException(404, "파일을 찾을 수 없습니다")
    fname = path.split("/")[-1]
    return Response(content=proc.stdout, media_type="application/octet-stream",
                    headers={"Content-Disposition": f'attachment; filename="{fname}"'})


@app.get("/api/v1/pipelines/{pipeline_id}/runs")
def list_runs(pipeline_id: str):
    runs = [r for r in _kfp_get("/apis/v2beta1/runs", {"page_size": 200}).get("runs", [])
            if _run_pipeline_id(r) == pipeline_id]
    return {"items": [{
        "id": r["run_id"],
        "name": r.get("display_name", ""),
        "state": r.get("state", "UNKNOWN"),
        "createdAt": r.get("created_at", ""),
        "finishedAt": r.get("finished_at", ""),
    } for r in runs]}


# ══════════════════════════════════════════════════════════════════
# Experiments — Katib(HPO) v1beta1 연동
# ══════════════════════════════════════════════════════════════════
KATIB_VERSION = "v1beta1"
E_PLURAL = "experiments"


def _to_experiment(e: dict) -> dict:
    st = e.get("status", {})
    spec = e.get("spec", {})
    obj = spec.get("objective", {})
    optimal = (st.get("currentOptimalTrial") or {})
    metrics = (optimal.get("observation") or {}).get("metrics") or []
    best = None
    for m in metrics:
        if m.get("name") == obj.get("objectiveMetricName"):
            best = float(m.get("max" if obj.get("type") == "maximize" else "min",
                                m.get("latest", 0)) or 0)
    conds = st.get("conditions", [])
    status = "Running"
    if any(c["type"] == "Succeeded" and c["status"] == "True" for c in conds):
        status = "Succeeded"
    elif any(c["type"] == "Failed" and c["status"] == "True" for c in conds):
        status = "Failed"
    return {
        "id": e["metadata"]["name"],
        "name": e["metadata"]["name"],
        "namespace": e["metadata"]["namespace"],
        "pipelineId": "",
        "ownerId": "u-000",
        "objective": f"{obj.get('type', '')} {obj.get('objectiveMetricName', '')}".strip(),
        "goal": obj.get("goal", 0),
        "bestMetric": best if best is not None else 0,
        # HPO의 진짜 결과물 — 최고 점수를 낸 설정값. 이 값을 학습 코드에 넣는다.
        "bestParams": [{"name": a.get("name", ""), "value": a.get("value", "")}
                       for a in (optimal.get("parameterAssignments") or [])],
        "bestTrial": optimal.get("bestTrialName", ""),
        # 어떤 범위에서 찾았는지 (결과 해석에 필요)
        "searchSpace": [{
            "name": p.get("name", ""),
            "type": p.get("parameterType", ""),
            "range": (p.get("feasibleSpace") or {}).get("list")
                     or [(p.get("feasibleSpace") or {}).get("min", ""),
                         (p.get("feasibleSpace") or {}).get("max", "")],
        } for p in spec.get("parameters", [])],
        "algorithm": (spec.get("algorithm") or {}).get("algorithmName", ""),
        "maxTrials": spec.get("maxTrialCount", 0),
        "parallelTrials": spec.get("parallelTrialCount", 0),
        # 실제 학습을 하는 실험인지 (계산식으로 점수만 내는 검증용과 구분)
        "realTraining": ACCEL_RESOURCE in json.dumps(
            spec.get("trialTemplate", {}).get("trialSpec", {})),
        "trials": st.get("trials", 0),
        "trialsSucceeded": st.get("trialsSucceeded", 0),
        "status": status,
        "createdAt": e["metadata"]["creationTimestamp"][:10],
    }


@app.get("/api/v1/experiments")
def list_experiments():
    out = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                items = crd.list_cluster_custom_object(GROUP, KATIB_VERSION, E_PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue
            out += [_tag(_to_experiment(e)) for e in items]
    return {"items": out}


class CreateExperiment(BaseModel):
    name: str
    namespace: str = DEFAULT_NS
    metric: str = "accuracy"
    objectiveType: str = "maximize"      # maximize | minimize
    goal: float = 0.95
    algorithm: str = "random"            # random | grid | bayesianoptimization
    maxTrials: int = 6
    parallelTrials: int = 2
    lrMin: float = 0.0001
    lrMax: float = 0.01
    batchSizes: list[str] = ["16", "32", "64"]
    # demo = 계산식으로 점수만 내는 경량 검증용
    # dm-code = 실제 DM코드 데이터로 ResNet18을 학습해 정확도를 잰다
    trialType: str = "demo"
    epochs: int = 3
    datasetUri: str = "s3://datasets/dm-code/dm_dataset.tgz"


def _trial_spec(body: "CreateExperiment", real: bool, cm_name: str) -> dict:
    """시도 1회를 실행할 Job 명세.

    real=True면 노트북과 같은 이미지에서 GPU로 실제 학습을 돌린다.
    (모델 가중치 내려받기 때문에 사외 통신 필요 → 프록시 주입)
    """
    if not real:
        container = {
            "name": "training",
            "image": "python:3.11-slim",
            "command": ["python3", "/scripts/train.py",
                        "${trialParameters.learningRate}",
                        "${trialParameters.batchSize}"],
            "volumeMounts": [{"name": "script", "mountPath": "/scripts"}],
            "resources": {
                "requests": {"cpu": "100m", "memory": "128Mi"},
                "limits": {"cpu": "500m", "memory": "256Mi"},
            },
        }
        pod = {
            **({"nodeSelector": _pin_selector()} if DEV_PIN_NODE else {}),
            "containers": [container],
            "volumes": [{"name": "script", "configMap": {"name": cm_name}}],
            "restartPolicy": "Never",
        }
    else:
        env = [
            {"name": "S3_ENDPOINT", "value": MODEL_STORE_ENDPOINT},
            {"name": "S3_KEY", "value": MODEL_STORE_KEY},
            {"name": "S3_SECRET", "value": MODEL_STORE_SECRET},
        ]
        if POD_HTTP_PROXY:
            env += [
                {"name": "HTTP_PROXY", "value": POD_HTTP_PROXY},
                {"name": "HTTPS_PROXY", "value": POD_HTTP_PROXY},
                {"name": "NO_PROXY",
                 "value": "localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,"
                          "192.168.0.0/16,.svc,.cluster.local,.kubeflow"},
            ]
        container = {
            "name": "training",
            "image": "kubeflownotebookswg/jupyter-pytorch-cuda-full:v1.9.2",
            "command": ["sh", "-c",
                        "pip install -q minio >/dev/null 2>&1; "
                        "python3 /scripts/train.py "
                        "${trialParameters.learningRate} "
                        "${trialParameters.batchSize} "
                        f"{body.epochs} {body.datasetUri}"],
            "env": env,
            "volumeMounts": [{"name": "script", "mountPath": "/scripts"}],
            "resources": {
                "requests": {"cpu": "2", "memory": "6Gi"},
                "limits": {"cpu": "4", "memory": "10Gi",
                           ACCEL_RESOURCE: "1"},
            },
        }
        container["resources"]["requests"][ACCEL_RESOURCE] = "1"
        pod = {
            # 학습 데이터를 MinIO에서 받아야 하므로 클러스터 내부 통신이 되는
            # 노드에 고정한다. (로컬 WSL은 노드 간 DNS·서비스 통신이 막혀 있어
            # 다른 노드에 뜨면 데이터를 못 받는다)
            **({"nodeSelector": _pin_selector()} if DEV_PIN_NODE else {}),
            **({"runtimeClassName": GPU_RUNTIME_CLASS} if GPU_RUNTIME_CLASS else {}),
            "containers": [container],
            "volumes": [{"name": "script", "configMap": {"name": cm_name}}],
            "restartPolicy": "Never",
        }
    return {"apiVersion": "batch/v1", "kind": "Job",
            "spec": {"template": {"spec": pod}}}


@app.post("/api/v1/experiments", status_code=201)
def create_experiment(body: CreateExperiment):
    """데모 HPO 실험 — 학습 대신 파라미터로부터 유사-정확도를 계산하는 경량 Job.
    실제 학습 파이프라인 연동 시 trialSpec의 컨테이너만 교체하면 된다.
    주의: Katib 메트릭 수집기가 command를 셸 문자열로 재조합하므로
    인라인 코드는 깨진다 → 스크립트를 ConfigMap 파일로 마운트(공식 예제 패턴)."""
    _route_ns(body.namespace)            # 워크스페이스가 있는 클러스터에 만든다
    train_py = (
        "import math, sys, time\n"
        "lr = float(sys.argv[1]); bs = int(sys.argv[2])\n"
        "time.sleep(15)  # 메트릭 수집 사이드카가 프로세스를 붙잡을 시간 확보\n"
        "acc = 0.97 - abs(math.log10(lr) + 3) * 0.05 - abs(bs - 32) / 32 * 0.03\n"
        "print('accuracy=%.4f' % acc, flush=True)\n"
        "time.sleep(5)\n"
    )
    real = body.trialType == "dm-code"
    if real:
        # 실제 DM코드 데이터로 학습해 정확도를 재는 스크립트
        script_path = _os.path.join(_os.path.dirname(_os.path.dirname(_os.path.abspath(__file__))),
                                    "pipelines", "hpo_dm_train.py")
        try:
            with open(script_path, encoding="utf-8") as f:
                train_py = f.read()
        except OSError:
            raise HTTPException(500, "학습 스크립트를 찾을 수 없습니다 "
                                     "(pipelines/hpo_dm_train.py)")

    cm_name = "cmp-hpo-dm-script" if real else "cmp-hpo-demo-script"
    cm = client.V1ConfigMap(metadata=client.V1ObjectMeta(name=cm_name),
                            data={"train.py": train_py})
    try:
        core.create_namespaced_config_map(body.namespace, cm)
    except client.exceptions.ApiException as e:
        if e.status == 409:
            core.replace_namespaced_config_map(cm_name, body.namespace, cm)
        else:
            raise
    exp = {
        "apiVersion": f"{GROUP}/{KATIB_VERSION}",
        "kind": "Experiment",
        "metadata": {"name": body.name, "namespace": body.namespace},
        "spec": {
            "objective": {
                "type": body.objectiveType,
                "goal": body.goal,
                "objectiveMetricName": body.metric,
            },
            "algorithm": {"algorithmName": body.algorithm},
            "parallelTrialCount": min(body.parallelTrials, body.maxTrials),
            "maxTrialCount": body.maxTrials,
            # Katib은 maxFailedTrialCount > maxTrialCount 면 생성을 거부한다
            "maxFailedTrialCount": min(3, body.maxTrials),
            "parameters": [
                {"name": "lr", "parameterType": "double",
                 "feasibleSpace": {"min": str(body.lrMin), "max": str(body.lrMax)}},
                {"name": "batch", "parameterType": "categorical",
                 "feasibleSpace": {"list": body.batchSizes}},
            ],
            "trialTemplate": {
                "primaryContainerName": "training",
                "trialParameters": [
                    {"name": "learningRate", "reference": "lr"},
                    {"name": "batchSize", "reference": "batch"},
                ],
                "trialSpec": _trial_spec(body, real, cm_name),
            },
        },
    }
    crd.create_namespaced_custom_object(GROUP, KATIB_VERSION, body.namespace,
                                        E_PLURAL, exp)

    # [로컬 전용] Suggestion Deployment 노드 고정 — DEV_PIN_NODE 설정 시에만
    if DEV_PIN_NODE:
        def _pin_suggestion(ns: str, exp_name: str, algo: str, cid: str):
            import time
            with _use_cluster(cid):      # 새 스레드에는 요청의 클러스터가 넘어가지 않는다
                apps = _apps()
            sel = {"spec": {"template": {"spec": {"nodeSelector": _pin_selector()}}}}
            for _ in range(30):
                try:
                    apps.patch_namespaced_deployment(f"{exp_name}-{algo}", ns, sel)
                    return
                except client.exceptions.ApiException:
                    time.sleep(2)

        import threading
        threading.Thread(target=_pin_suggestion,
                         args=(body.namespace, body.name, body.algorithm, _cid()),
                         daemon=True).start()
    return {"name": body.name, "status": "Created"}


@app.delete("/api/v1/experiments/{name}")
def delete_experiment(name: str, namespace: str = DEFAULT_NS):
    _route_ns(namespace)
    try:
        crd.delete_namespaced_custom_object(GROUP, KATIB_VERSION, namespace,
                                            E_PLURAL, name)
    except client.exceptions.ApiException as e:
        if e.status == 404:
            for cid in _search_cids():   # PaaS 모드는 다른 클러스터도 본다
                _set_cluster(cid)
                try:
                    items = crd.list_cluster_custom_object(GROUP, KATIB_VERSION, E_PLURAL)["items"]
                except Exception:
                    if not MULTI:
                        raise
                    continue
                for x in items:
                    if x["metadata"]["name"] == name:
                        crd.delete_namespaced_custom_object(
                            GROUP, KATIB_VERSION, x["metadata"]["namespace"], E_PLURAL, name)
                        return {"status": "Deleting"}
            raise HTTPException(404, f"experiment {name} not found")
        raise
    return {"status": "Deleting"}


# ══════════════════════════════════════════════════════════════════
# Serving — KServe InferenceService (RawDeployment 모드)
# ══════════════════════════════════════════════════════════════════
from fastapi.responses import Response

KSERVE_GROUP, KSERVE_VERSION, I_PLURAL = "serving.kserve.io", "v1beta1", "inferenceservices"


def _to_isvc(s: dict) -> dict:
    meta, spec, st = s["metadata"], s.get("spec", {}), s.get("status", {})
    predictor = spec.get("predictor", {})
    model = predictor.get("model", {})
    ready = any(c.get("type") == "Ready" and c.get("status") == "True"
                for c in st.get("conditions", []))
    res = model.get("resources") or {}
    accel = _accel(res)
    limits = res.get("limits") or {}
    return {
        "id": meta["name"],
        "name": meta["name"],
        "namespace": meta["namespace"],
        "storageUri": model.get("storageUri", ""),
        # 자원 변경 화면을 채우기 위한 현재 값
        "cpu": str(limits.get("cpu", "")),
        "memoryGi": int(str(limits.get("memory", "0Gi")).replace("Gi", "") or 0),
        "gpu": int(limits.get(ACCEL_RESOURCE, 0) or 0),
        "modelName": (model.get("modelFormat") or {}).get("name", "custom"),
        "modelVersion": (model.get("storageUri") or "").split("/")[-1] or "v1",
        "ownerId": "u-000",
        **({"acceleratorKind": "GPU"} if accel else {}),
        **(_gpu_placement(meta["name"], meta["namespace"]) if accel else {}),
        "replicas": predictor.get("minReplicas", 1),
        # 아래 두 값은 목록 조회 시 실측으로 채운다 (_fill_serving_metrics)
        "rpsAvg": 0,
        "p95LatencyMs": 0,
        "requests": 0,
        **_isvc_status(meta["name"], meta["namespace"], ready),
        "endpoint": f"{PUBLIC_BASE_URL}{API_PREFIX or '/api/v1'}/serving/{meta['name']}/predict",
    }


def _find_isvc(name: str) -> Optional[dict]:
    """InferenceService 를 이름으로 찾는다 — PaaS 모드는 클러스터를 차례로 보고,
    찾은 클러스터를 이 요청의 이후 호출 대상으로 정한다."""
    for cid in _search_cids():
        _set_cluster(cid)
        try:
            items = crd.list_cluster_custom_object(KSERVE_GROUP, KSERVE_VERSION, I_PLURAL)["items"]
        except Exception:
            if not MULTI:
                raise
            continue
        for s in items:
            if s["metadata"]["name"] == name:
                return s
    return None


def _svc_base(ns: str, svc: str, port: int = 80) -> str:
    """서비스 주소 — 클러스터 안 IP 로 닿으면 ClusterIP, 아니면(다른 클러스터를 조작) port-forward."""
    if _cur().direct_net:
        return f"http://{core.read_namespaced_service(svc, ns).spec.cluster_ip}:{port}"
    return f"http://127.0.0.1:{_ensure_svc_forward(ns, svc, port)}"


_node_gpu_model_cache: dict[str, str] = {}


def _node_gpu_model(node: str) -> str:
    """노드에 달린 GPU 모델명 — 바뀌지 않는 값이라 한 번 조회 후 캐시(클러스터마다 따로)."""
    key = f"{_cid()}/{node}"
    if key in _node_gpu_model_cache:
        return _node_gpu_model_cache[key]
    try:
        pods = core.list_namespaced_pod(
            "kube-system", label_selector="app=gpu-metrics",
            field_selector=f"spec.nodeName={node}").items
        if pods and pods[0].status.phase == "Running":
            raw = k8s_stream(_exec_api().connect_get_namespaced_pod_exec,
                             pods[0].metadata.name, "kube-system",
                             command=["sh", "-c",
                                      "nvidia-smi --query-gpu=name --format=csv,noheader"],
                             stderr=True, stdin=False, stdout=True, tty=False,
                             _request_timeout=8)
            gpu_name = str(raw).strip().splitlines()[0].strip()
            if gpu_name and "command" not in gpu_name.lower():
                _node_gpu_model_cache[key] = gpu_name
                return gpu_name
    except Exception:
        pass
    return ""


def _gpu_placement(name: str, ns: str) -> dict:
    """GPU를 쓰는 서비스가 실제로 어느 노드의 어떤 GPU에서 도는지."""
    try:
        pods = core.list_namespaced_pod(
            ns, label_selector=f"serving.kserve.io/inferenceservice={name}")
        node = next((p.spec.node_name for p in pods.items
                     if p.status.phase == "Running" and p.spec.node_name), None)
        if node:
            return {"gpuNode": node, "gpuModel": _node_gpu_model(node)}
    except Exception:
        pass
    return {}


def _isvc_status(name: str, ns: str, ready: bool) -> dict:
    """Ready가 아니면 파드를 들여다보고 '기다리면 되는 상태'와
    '기다려도 안 되는 상태(반복 실패)'를 구분해 사유와 함께 알려준다.
    — Pending으로만 보여주면 사용자가 몇 시간을 헛기다린다(실사례)."""
    if ready:
        return {"status": "Ready", "statusReason": ""}
    try:
        pods = core.list_namespaced_pod(
            ns, label_selector=f"serving.kserve.io/inferenceservice={name}")
        for p in pods.items:
            for cs in (p.status.container_statuses or []):
                # 메모리 초과로 강제 종료된 경우 (현재 종료 상태이거나,
                # 재시작 대기 중이라 직전 종료 사유에 남아 있는 경우 모두)
                for st in (cs.state.terminated, cs.last_state.terminated):
                    if st and st.reason == "OOMKilled":
                        return {"status": "Failed",
                                "statusReason":
                                    f"메모리가 부족해 강제 종료됐습니다 (재시작 {cs.restart_count}회). "
                                    "자원 변경에서 메모리를 늘리세요 — LLM은 보통 8GB 이상 필요합니다"}
                w = cs.state.waiting
                if w and w.reason in ("CrashLoopBackOff", "ImagePullBackOff",
                                      "ErrImagePull", "CreateContainerError"):
                    n = cs.restart_count
                    why = {"CrashLoopBackOff":
                               f"컨테이너가 시작 직후 죽기를 반복합니다 (재시작 {n}회). "
                               "설정 문제일 가능성이 큽니다 — 예: LLM(vLLM)을 GPU 없이 배포",
                           "ImagePullBackOff": "이미지를 내려받지 못합니다 (이름·태그 확인)",
                           "ErrImagePull": "이미지를 내려받지 못합니다 (이름·태그 확인)",
                           "CreateContainerError": "컨테이너 생성에 실패했습니다",
                           }[w.reason]
                    return {"status": "Failed", "statusReason": why}
            if p.status.phase == "Pending":
                return {"status": "Pending",
                        "statusReason": _friendly_reason(p)}
    except Exception:
        pass
    return {"status": "Pending", "statusReason": ""}


def _fill_serving_metrics(item: dict) -> dict:
    """서비스 카드의 처리량·지연을 실측으로 채운다.

    지표 출처가 모델 종류마다 다르다.
      - LLM(vllm)  : vLLM이 내보내는 vllm:* 지표
      - 그 외      : KServe 래퍼가 내보내는 request_* 지표
    측정 이력이 없으면 0으로 둔다(=아직 호출된 적 없음).
    """
    if item["status"] != "Ready":
        return item
    name, ns = item["name"], item["namespace"]
    try:
        if item["modelName"] == "vllm":
            # 목록 조회는 화면이 주기적으로 부르는 경로다. 여기서 연결통로를
            # 새로 여느라 수 초를 붙잡으면 목록 전체가 느려지므로, 이미 열려
            # 있을 때만 지표를 읽고 아니면 지표 없이 넘어간다.
            port = _ensure_svc_forward(ns, f"{name}-predictor", spawn=False)
            if not port:
                return item
            t = httpx.get(f"http://127.0.0.1:{port}/metrics", timeout=2.5).text
            n = _prom_val(t, "vllm:e2e_request_latency_seconds_count")
            total = _prom_val(t, "vllm:e2e_request_latency_seconds_sum")
            buckets = _prom_hist(t, "vllm:e2e_request_latency_seconds")
        else:
            base = _predictor_metrics_base(ns, name, spawn=False)
            if not base:
                return item
            t = httpx.get(f"{base}/metrics", timeout=6).text
            n = _prom_val(t, "request_predict_seconds_count")
            total = _prom_val(t, "request_predict_seconds_sum")
            buckets = _prom_hist(t, "request_predict_seconds")
        if n:
            avg = total / n                      # 요청 1건당 평균 처리 시간(초)
            item["requests"] = int(n)
            # 한 건에 avg초가 걸리므로 초당 처리 가능 건수는 1/avg
            item["rpsAvg"] = round(1 / avg, 1) if avg > 0 else 0
            p95 = _hist_quantile(buckets, 0.95) if buckets else None
            item["p95LatencyMs"] = round(p95 * 1000, 1) if p95 else round(avg * 1000, 1)
    except Exception:
        pass
    return item


def _predictor_metrics_base(ns: str, name: str, spawn: bool = True) -> Optional[str]:
    """KServe 모델 서버 지표 주소 — 클러스터 안 IP 로 닿으면 파드 IP:8080, 아니면 predictor 서비스 port-forward.
    spawn=False 면 통로가 이미 열려 있을 때만(목록처럼 자주 부르는 경로)."""
    if _cur().direct_net:
        pods = core.list_namespaced_pod(ns, label_selector=f"app=isvc.{name}-predictor")
        ip = next((p.status.pod_ip for p in pods.items
                   if p.status.phase == "Running" and p.status.pod_ip), None)
        return f"http://{ip}:8080" if ip else None
    port = _ensure_svc_forward(ns, f"{name}-predictor", 80, spawn=spawn)
    return f"http://127.0.0.1:{port}" if port else None


@app.get("/api/v1/serving")
def list_serving():
    out = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                items = crd.list_cluster_custom_object(KSERVE_GROUP, KSERVE_VERSION, I_PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue
            out += [_tag(_fill_serving_metrics(_to_isvc(s))) for s in items]
    return {"items": out}


# ── 모델 저장소 (MinIO / S3 호환) ──────────────────────────────────
MODEL_STORE_ENDPOINT = _os.environ.get("MODEL_STORE_ENDPOINT",
                                       "minio-service.kubeflow:9000")
MODEL_STORE_BUCKET = _os.environ.get("MODEL_STORE_BUCKET", "models")
MODEL_STORE_KEY = _os.environ.get("MODEL_STORE_KEY", "minio")
MODEL_STORE_SECRET = _os.environ.get("MODEL_STORE_SECRET", "minio123")
MODEL_STORE_SA = _os.environ.get("MODEL_STORE_SA", "kserve-minio-sa")
MODEL_STORE_CRED_SECRET = "minio-s3-creds"


def _ensure_model_store_sa(ns: str):
    """s3:// 모델을 읽을 ServiceAccount와 자격증명 Secret을 네임스페이스에 만든다.

    예전에는 몇몇 네임스페이스에만 손으로 만들어 두어서, 새 워크스페이스에서
    저장소 모델을 배포하면 storage-initializer가 권한 없이 실패했다.
    이미 있으면 그대로 둔다(멱등).
    """
    try:
        core.read_namespaced_secret(MODEL_STORE_CRED_SECRET, ns)
    except client.exceptions.ApiException as e:
        if e.status != 404:
            raise
        core.create_namespaced_secret(ns, client.V1Secret(
            metadata=client.V1ObjectMeta(
                name=MODEL_STORE_CRED_SECRET,
                annotations={
                    "serving.kserve.io/s3-endpoint": MODEL_STORE_ENDPOINT,
                    "serving.kserve.io/s3-usehttps": "0",
                    "serving.kserve.io/s3-region": "us-east-1",
                    "serving.kserve.io/s3-useanoncredential": "false",
                }),
            string_data={"AWS_ACCESS_KEY_ID": MODEL_STORE_KEY,
                         "AWS_SECRET_ACCESS_KEY": MODEL_STORE_SECRET},
            type="Opaque"))
    try:
        core.read_namespaced_service_account(MODEL_STORE_SA, ns)
    except client.exceptions.ApiException as e:
        if e.status != 404:
            raise
        core.create_namespaced_service_account(ns, client.V1ServiceAccount(
            metadata=client.V1ObjectMeta(name=MODEL_STORE_SA),
            secrets=[client.V1ObjectReference(name=MODEL_STORE_CRED_SECRET)]))

def _minio_client():
    """백엔드에서 MinIO에 접근 — 같은 클러스터 안(파드)이면 서비스 주소로, 아니면 port-forward 경유(클러스터마다 따로)."""
    from minio import Minio
    if _cur().in_cluster:
        return Minio(MODEL_STORE_ENDPOINT, access_key=MODEL_STORE_KEY,
                     secret_key=MODEL_STORE_SECRET, secure=False)
    host, _, port = MODEL_STORE_ENDPOINT.partition(":")      # 예: minio-service.kubeflow:9000
    svc, _, rest = host.partition(".")
    try:
        local = _ensure_svc_forward(rest.split(".")[0] or "kubeflow", svc, int(port or 9000))
    except HTTPException:
        raise HTTPException(503, "모델 저장소(MinIO) 연결 실패")
    return Minio(f"127.0.0.1:{local}", access_key=MODEL_STORE_KEY,
                 secret_key=MODEL_STORE_SECRET, secure=False)


# 파일 구조로 서빙 형식을 알아낸다 — 배포 가능한 것만 목록에 올리기 위함
_SERVABLE_HINTS = [
    ("model-store/", ".mar", "pytorch"),
    ("", "model.joblib", "sklearn"),
    ("", "model.pkl", "sklearn"),
    ("", "model.bst", "xgboost"),
    ("", "saved_model.pb", "tensorflow"),
]


def _servable_format(key: str) -> Optional[str]:
    name = key.split("/")[-1]
    for folder, suffix, fmt in _SERVABLE_HINTS:
        if folder and f"/{folder}" in f"/{key}" and name.endswith(suffix):
            return fmt
        if not folder and name == suffix:
            return fmt
    return None


@app.get("/api/v1/models")
def list_models():
    """배포 가능한 모델 목록.

    최상위 폴더가 아니라 '서빙이 바로 읽을 수 있는 경로'를 찾아서 준다.
    (예: dm-code/serve 안에 model-store/*.mar 가 있으면 그 경로를 준다)
    """
    cli = _minio_client()
    if not cli.bucket_exists(MODEL_STORE_BUCKET):
        return {"items": [], "bucket": MODEL_STORE_BUCKET}

    found: dict[str, dict] = {}
    others: dict[str, dict] = {}
    for obj in cli.list_objects(MODEL_STORE_BUCKET, recursive=True):
        key = obj.object_name
        fmt = _servable_format(key)
        size = (obj.size or 0) / 1024 / 1024
        stamp = (obj.last_modified.strftime("%Y-%m-%d %H:%M")
                 if obj.last_modified else "")
        if fmt:
            # 모델 파일이 놓인 폴더가 배포 경로가 된다
            base = key.rsplit("/model-store/", 1)[0] if fmt == "pytorch" \
                else key.rsplit("/", 1)[0] if "/" in key else ""
            e = found.setdefault(base, {
                "name": base or "(루트)", "format": fmt, "files": 0,
                "sizeMb": 0.0, "servable": True,
                "uri": f"s3://{MODEL_STORE_BUCKET}/{base}" if base
                       else f"s3://{MODEL_STORE_BUCKET}",
            })
            e["files"] += 1
            e["sizeMb"] = round(e["sizeMb"] + size, 1)
            e["updatedAt"] = stamp
        else:
            top = key.split("/")[0]
            e = others.setdefault(top, {
                "name": top, "format": "", "files": 0, "sizeMb": 0.0,
                "servable": False,
                "uri": f"s3://{MODEL_STORE_BUCKET}/{top}",
            })
            e["files"] += 1
            e["sizeMb"] = round(e["sizeMb"] + size, 1)
            e["updatedAt"] = stamp

    # 배포 가능한 경로에 이미 포함된 폴더는 중복이라 제외
    servable_tops = {k.split("/")[0] for k in found}
    rest = [v for k, v in others.items() if k not in servable_tops]

    items = sorted(found.values(), key=lambda x: x["name"]) + \
        sorted(rest, key=lambda x: x["name"])
    return {"items": items, "bucket": MODEL_STORE_BUCKET}


class ImportModel(BaseModel):
    """노트북 작업공간의 폴더를 모델 저장소로 등록."""
    notebook: str
    path: str            # 노트북 내부 경로 (예: deploy/dm-classifier)
    modelName: str       # 저장소에 등록할 이름
    namespace: str = DEFAULT_NS


@app.post("/api/v1/models/import", status_code=201)
def import_model_from_notebook(body: ImportModel):
    """노트북 → 백엔드 → MinIO 로 모델 파일을 옮긴다."""
    import tarfile, tempfile, io as _io, os as _o
    nb = _get_nb(body.notebook, body.namespace)
    ns = nb["metadata"]["namespace"]
    src = f"/home/jovyan/{body.path.strip('/')}"

    with tempfile.TemporaryDirectory() as tmp:
        tar_path = _o.path.join(tmp, "m.tar")
        with open(tar_path, "wb") as f:
            p = subprocess.run(
                ["kubectl", "-n", ns, "exec", f"{body.notebook}-0", "--",
                 "sh", "-c", f"cd {src} && tar cf - ."],
                stdout=f, stderr=subprocess.DEVNULL, timeout=600, env=_kenv())
        if p.returncode != 0 or _o.path.getsize(tar_path) == 0:
            raise HTTPException(400, f"경로를 찾을 수 없습니다: {body.path}")

        cli = _minio_client()
        if not cli.bucket_exists(MODEL_STORE_BUCKET):
            cli.make_bucket(MODEL_STORE_BUCKET)

        uploaded, total = 0, 0
        with tarfile.open(tar_path) as tf:
            for m in tf.getmembers():
                if not m.isfile():
                    continue
                data = tf.extractfile(m).read()
                key = f"{body.modelName}/{m.name.lstrip('./')}"
                cli.put_object(MODEL_STORE_BUCKET, key, _io.BytesIO(data), len(data))
                uploaded += 1
                total += len(data)

    return {"model": body.modelName,
            "uri": f"s3://{MODEL_STORE_BUCKET}/{body.modelName}",
            "files": uploaded, "sizeMb": round(total / 1024 / 1024, 1)}


class CreateServing(BaseModel):
    name: str
    namespace: str = "llm-serving"
    modelFormat: str = "sklearn"     # sklearn | xgboost | tensorflow | pytorch | vllm
    storageUri: str = ""             # vllm은 modelId를 쓰므로 비워둘 수 있다
    cpu: str = "1"
    memoryGi: int = 1
    gpu: int = 0                     # 0이면 CPU로만 추론
    # LLM(vllm) 전용 — HuggingFace 모델 ID와 실행 옵션
    modelId: str = ""
    maxModelLen: int = 2048
    gpuMemoryUtilization: float = 0.35
    node: str = ""                   # 특정 장비에 올리고 싶을 때 (비우면 자동 배치)


@app.post("/api/v1/serving", status_code=201)
def create_serving(body: CreateServing):
    _route_ns(body.namespace)            # 워크스페이스가 있는 클러스터에 배포한다
    limits: dict = {"cpu": body.cpu, "memory": f"{body.memoryGi}Gi"}
    requests: dict = {"cpu": "100m", "memory": "256Mi"}
    if body.gpu > 0:
        # GPU는 쪼갤 수 없어 requests와 limits가 같아야 한다
        limits[ACCEL_RESOURCE] = str(body.gpu)
        requests[ACCEL_RESOURCE] = str(body.gpu)

    model: dict = {
        "modelFormat": {"name": body.modelFormat},
        "resources": {"requests": requests, "limits": limits},
    }
    if body.modelFormat == "vllm":
        # LLM은 파일을 내려받는 대신 모델 ID로 불러온다
        if not body.modelId:
            raise HTTPException(400, "LLM 배포에는 모델 ID가 필요합니다 "
                                     "(예: Qwen/Qwen2.5-0.5B-Instruct)")
        model["args"] = [
            f"--model={body.modelId}",
            f"--max-model-len={body.maxModelLen}",
            f"--gpu-memory-utilization={body.gpuMemoryUtilization}",
        ]
        # 핫스팟 프로파일링(on-demand torch profiler)을 쓰려면 배포 시점에
        # 켜 둬야 한다 — 빠지면 병목 분석의 "프로파일링 실행"이 실패한다
        model["env"] = [{"name": "VLLM_TORCH_PROFILER_DIR", "value": "/profiles"}]
        if POD_HTTP_PROXY:      # 모델을 받아오려면 사외 통신이 필요하다
            model["env"] += [
                {"name": "HTTP_PROXY", "value": POD_HTTP_PROXY},
                {"name": "HTTPS_PROXY", "value": POD_HTTP_PROXY},
                {"name": "NO_PROXY",
                 "value": "localhost,127.0.0.1,10.0.0.0/8,192.168.0.0/16,"
                          ".svc,.cluster.local,.kubeflow"},
            ]
    else:
        if not body.storageUri:
            raise HTTPException(400, "배포할 모델을 선택하세요")
        model["storageUri"] = body.storageUri
        if body.storageUri.startswith("s3://"):
            _ensure_model_store_sa(body.namespace)

    predictor: dict = {
        **({"nodeSelector": _pin_selector()} if DEV_PIN_NODE else {}),
        # s3:// 모델은 저장소 자격증명이 붙은 ServiceAccount로 읽는다
        **({"serviceAccountName": MODEL_STORE_SA}
           if body.storageUri.startswith("s3://") else {}),
        "model": model,
    }
    if body.gpu > 0:
        # 이 설정이 없으면 GPU가 배정돼도 컨테이너에서 CUDA를 못 본다
        if GPU_RUNTIME_CLASS:
            predictor["runtimeClassName"] = GPU_RUNTIME_CLASS
        # GPU 노드에 스케줄되어야 하므로 개발용 노드 고정은 풀어준다
        predictor.pop("nodeSelector", None)
    if body.node:
        predictor["nodeSelector"] = {"kubernetes.io/hostname": body.node}

    isvc = {
        "apiVersion": f"{KSERVE_GROUP}/{KSERVE_VERSION}",
        "kind": "InferenceService",
        "metadata": {"name": body.name, "namespace": body.namespace},
        "spec": {"predictor": predictor},
    }
    try:
        crd.create_namespaced_custom_object(KSERVE_GROUP, KSERVE_VERSION,
                                            body.namespace, I_PLURAL, isvc)
    except client.exceptions.ApiException as e:
        if e.status == 409:
            raise HTTPException(
                409, f"'{body.name}' 이름의 서비스가 이미 있습니다. "
                     f"다른 이름을 쓰거나 기존 서비스를 삭제하세요.")
        # 쿠버네티스가 돌려준 사유를 그대로 전달한다 (500만 뜨면 원인을 알 수 없다)
        detail = ""
        try:
            detail = json.loads(e.body or "{}").get("message", "")
        except Exception:
            detail = (e.body or "")[:200]
        raise HTTPException(e.status or 500, f"배포 실패: {detail or e.reason}")
    except Exception as e:
        raise HTTPException(500, f"배포 실패: {e}")
    return {"name": body.name, "status": "Creating"}


def _strip_deploy_gpu(ns: str, deploy_name: str) -> None:
    """Deployment에 남은 GPU 요청을 제거한다 (KServe가 안 지워주는 부분).

    파드가 재생성되면서 가속기를 실제로 반납한다.
    """
    import time as _t
    apps = _apps()
    for _ in range(6):          # KServe가 Deployment를 만들 때까지 잠시 대기
        try:
            d = apps.read_namespaced_deployment(deploy_name, ns)
        except client.exceptions.ApiException:
            _t.sleep(2)
            continue
        c = d.spec.template.spec.containers[0]
        changed = False
        for bucket in (c.resources.limits, c.resources.requests):
            if bucket and ACCEL_RESOURCE in bucket:
                bucket.pop(ACCEL_RESOURCE)
                changed = True
        if d.spec.template.spec.runtime_class_name:
            d.spec.template.spec.runtime_class_name = None
            changed = True
        if not changed:
            return
        try:
            apps.replace_namespaced_deployment(deploy_name, ns, d)
            return
        except client.exceptions.ApiException:
            _t.sleep(2)


class UpdateServing(BaseModel):
    cpu: Optional[str] = None
    memoryGi: Optional[int] = None
    gpu: Optional[int] = None
    storageUri: Optional[str] = None     # 모델 교체 (학습 파이프라인이 사용)
    modelFormat: Optional[str] = None


@app.patch("/api/v1/serving/{name}")
def update_serving(name: str, body: UpdateServing):
    """배포된 서비스의 자원(CPU·메모리·GPU)을 바꾼다.

    KServe가 파드를 새로 띄우고 준비되면 교체하므로 잠시 뒤 반영된다.
    """
    target = _find_isvc(name)
    if target is None:
        raise HTTPException(404, f"서비스 {name}을(를) 찾을 수 없습니다")

    ns = target["metadata"]["namespace"]
    predictor = target["spec"]["predictor"]
    res = predictor["model"].setdefault(
        "resources", {"requests": {}, "limits": {}})
    limits = res.setdefault("limits", {})
    requests = res.setdefault("requests", {})

    if body.cpu:
        limits["cpu"] = body.cpu
    if body.memoryGi:
        limits["memory"] = f"{body.memoryGi}Gi"
    if body.storageUri:
        predictor["model"]["storageUri"] = body.storageUri
        if body.storageUri.startswith("s3://"):
            _ensure_model_store_sa(ns)
            predictor["serviceAccountName"] = MODEL_STORE_SA
    if body.modelFormat:
        predictor["model"]["modelFormat"] = {"name": body.modelFormat}
    if body.gpu is not None:
        if body.gpu > 0:
            limits[ACCEL_RESOURCE] = str(body.gpu)
            requests[ACCEL_RESOURCE] = str(body.gpu)
            if GPU_RUNTIME_CLASS:
                predictor["runtimeClassName"] = GPU_RUNTIME_CLASS
            predictor.pop("nodeSelector", None)
        else:
            # 병합 패치에서는 키를 빼는 것만으로 지워지지 않는다.
            # 서버에서 삭제되게 하려면 null을 명시해야 한다.
            limits[ACCEL_RESOURCE] = None
            requests[ACCEL_RESOURCE] = None
            predictor["runtimeClassName"] = None
            if DEV_PIN_NODE:
                predictor["nodeSelector"] = _pin_selector()

    # 변경 사실을 주석으로 남겨 재배포를 확실히 트리거한다.
    # (같은 경로에 모델만 바뀌거나 GPU를 빼는 경우 KServe가 그냥 넘어간다)
    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    predictor.setdefault("annotations", {})[
        "openkcloud.io/updated-at"] = stamp

    crd.patch_namespaced_custom_object(
        KSERVE_GROUP, KSERVE_VERSION, ns, I_PLURAL, name,
        {"metadata": {"annotations": {"openkcloud.io/updated-at": stamp}},
         "spec": {"predictor": predictor}})

    # KServe는 자원을 '추가'만 반영하고 '제거'는 Deployment에 내리지 않는다.
    # GPU를 빼는 경우 파드가 계속 가속기를 물고 있으므로 직접 정리한다.
    if body.gpu == 0:
        _strip_deploy_gpu(ns, f"{name}-predictor")
    return {"name": name, "status": "Updating",
            "cpu": limits.get("cpu"), "memory": limits.get("memory"),
            "gpu": int(limits.get(ACCEL_RESOURCE) or 0)}


@app.delete("/api/v1/serving/{name}")
def delete_serving(name: str, namespace: str = "llm-serving"):
    s = _find_isvc(name)
    if s is None:
        raise HTTPException(404, f"inferenceservice {name} not found")
    crd.delete_namespaced_custom_object(
        KSERVE_GROUP, KSERVE_VERSION, s["metadata"]["namespace"], I_PLURAL, name)
    return {"status": "Deleting"}


@app.post("/api/v1/serving/{name}/predict")
async def predict(name: str, request: Request, namespace: str = "llm-serving"):
    """모델 API 호출 프록시 — KServe v1 protocol (:predict)."""
    s = _find_isvc(name)
    ns = s["metadata"]["namespace"] if s else namespace
    base = _svc_base(ns, f"{name}-predictor")
    model_name = await _resolve_model_name(name, base)
    url = f"{base}/v1/models/{model_name}:predict"
    body = await request.body()
    import time as _t
    t0 = _t.perf_counter()
    async with httpx.AsyncClient(timeout=30) as cli:
        r = await cli.post(url, content=body,
                           headers={"Content-Type": "application/json"})
    ms = round((_t.perf_counter() - t0) * 1000, 1)
    return Response(content=r.content, status_code=r.status_code,
                    headers={"X-Latency-Ms": str(ms),
                             "Content-Type": "application/json"})


# ══════════════════════════════════════════════════════════════════
# Model Benchmark — 배포 모델 부하 테스트 (RPS·p50/p95/p99·오류율 실측)
# ══════════════════════════════════════════════════════════════════
_model_bench_results: dict[str, dict] = {}

_SAMPLE_INPUTS = {
    "sklearn": {"instances": [[6.8, 2.8, 4.8, 1.4]]},
    "xgboost": {"instances": [[6.8, 2.8, 4.8, 1.4]]},
}

_sample_image_b64: Optional[str] = None
SAMPLE_DATASET = _os.environ.get("SAMPLE_DATASET",
                                 "datasets/dm-code/dm_dataset.tgz")


def _sample_image() -> Optional[str]:
    """이미지 모델 테스트용 샘플 1장 — 데이터셋에서 꺼내 캐시한다."""
    global _sample_image_b64
    if _sample_image_b64:
        return _sample_image_b64
    import base64 as _b64, io as _io2, tarfile as _tar
    try:
        bucket, key = SAMPLE_DATASET.split("/", 1)
        cli = _minio_client()
        raw = cli.get_object(bucket, key).read()
        with _tar.open(fileobj=_io2.BytesIO(raw)) as t:
            for m in t:
                if m.isfile() and m.name.lower().endswith((".jpg", ".png")):
                    f = t.extractfile(m)
                    if f:
                        _sample_image_b64 = _b64.b64encode(f.read()).decode()
                        return _sample_image_b64
    except Exception:
        return None
    return None


def _sample_payload(fmt: str) -> Optional[dict]:
    """모델 형식에 맞는 테스트 입력을 만든다."""
    if fmt in _SAMPLE_INPUTS:
        return _SAMPLE_INPUTS[fmt]
    if fmt in ("pytorch", "triton", "tensorflow"):
        img = _sample_image()
        if img:
            return {"instances": [{"image_b64": img}]}
    return None


@app.get("/api/v1/metrics/serving")
async def serving_metrics():
    """배포된 모델 서비스의 추론 지표 — KServe 래퍼가 내보내는 실측값.

    vLLM(LLM)과 달리 KServe 모델은 전처리·추론·후처리 구간이 따로 계측된다.
    """
    items = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                isvcs = crd.list_cluster_custom_object(KSERVE_GROUP, KSERVE_VERSION, I_PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue
            for s in isvcs:
                name, ns = s["metadata"]["name"], s["metadata"]["namespace"]
                model = (s["spec"]["predictor"].get("model") or {})
                # LLM은 vLLM 자체 지표(TTFT·TPOT)가 훨씬 유용해서 별도 카드에서 다룬다
                if (model.get("modelFormat") or {}).get("name") == "vllm":
                    continue
                ready = any(c.get("type") == "Ready" and c.get("status") == "True"
                            for c in (s.get("status", {}).get("conditions") or []))
                if not ready:
                    continue
                # 지표 조회에 실패해도 서비스를 목록에서 빼지 않는다. 빼버리면 화면에서
                # 카드가 나왔다 사라졌다 해서 사용자가 배포가 풀린 줄로 오해한다.
                # 배포되어 있다는 사실과, 아직 측정값이 없다는 사실을 함께 보여준다.
                t, measured = "", True
                try:
                    base = _predictor_metrics_base(ns, name)
                    if not base:
                        measured = False
                    else:
                        async with httpx.AsyncClient(timeout=8) as cli:
                            t = (await cli.get(f"{base}/metrics")).text
                except Exception:
                    measured = False

                def stage(metric: str) -> tuple[float, int]:
                    """(평균 ms, 호출 수)"""
                    n = _prom_val(t, f"{metric}_count")
                    total = _prom_val(t, f"{metric}_sum")
                    return (round(total / n * 1000, 2) if n else 0.0), int(n)

                pre_ms, n = stage("request_preprocess_seconds")
                inf_ms, _ = stage("request_predict_seconds")
                post_ms, _ = stage("request_postprocess_seconds")
                buckets = _prom_hist(t, "request_predict_seconds")
                p95 = _hist_quantile(buckets, 0.95) if buckets else None

                res = model.get("resources") or {}
                gpu = int((res.get("limits") or {}).get(ACCEL_RESOURCE, 0) or 0)
                items.append(_tag({
                    "name": name,
                    "namespace": ns,
                    "format": (model.get("modelFormat") or {}).get("name", ""),
                    "accelerator": f"GPU {gpu}장" if gpu else "CPU",
                    "requests": n,
                    "measured": measured,
                    "breakdown": [
                        {"stage": "입력 전처리", "ms": pre_ms, "key": "pre"},
                        {"stage": "모델 추론", "ms": inf_ms, "key": "infer"},
                        {"stage": "결과 후처리", "ms": post_ms, "key": "post"},
                    ],
                    "totalMs": round(pre_ms + inf_ms + post_ms, 2),
                    "p95Ms": round(p95 * 1000, 1) if p95 else None,
                }))
    return {"items": items}


_model_name_cache: dict[str, str] = {}


async def _resolve_model_name(svc_name: str, base: str) -> str:
    """서버에 실제 등록된 모델 이름을 알아낸다. base = 서비스 주소(_svc_base).

    TorchServe는 .mar 파일에 적힌 이름으로 모델을 등록하는데, 그 이름이
    서비스 이름과 다르면 예측 경로가 어긋나 404가 난다. (이미 만들어진
    .mar를 다른 이름의 서비스로 배포하는 경우가 그렇다)
    """
    key = f"{_cid()}/{svc_name}"         # 클러스터마다 같은 서비스 이름이 있을 수 있다
    if key in _model_name_cache:
        return _model_name_cache[key]
    try:
        async with httpx.AsyncClient(timeout=10) as cli:
            r = await cli.get(f"{base}/v1/models")
        models = r.json().get("models") or []
        # 서비스 이름과 같은 게 있으면 그것을, 없으면 등록된 첫 번째를 쓴다
        picked = svc_name if svc_name in models else (models[0] if models
                                                      else svc_name)
    except Exception:
        picked = svc_name
    _model_name_cache[key] = picked
    return picked


@app.post("/api/v1/serving/{name}/test")
async def test_serving(name: str):
    """서비스 호출 테스트 — 모델 형식에 맞는 샘플을 자동으로 넣어 호출한다."""
    target = _find_isvc(name)
    if target is None:
        raise HTTPException(404, f"서비스 {name}을(를) 찾을 수 없습니다")

    ns = target["metadata"]["namespace"]
    model = target["spec"]["predictor"].get("model", {})
    fmt = (model.get("modelFormat") or {}).get("name", "sklearn")
    payload = _sample_payload(fmt)
    if payload is None:
        raise HTTPException(
            400, f"'{fmt}' 형식은 자동 테스트 입력을 만들 수 없습니다. "
                 f"직접 호출해 확인하세요.")

    base = _svc_base(ns, f"{name}-predictor")
    model_name = await _resolve_model_name(name, base)
    url = f"{base}/v1/models/{model_name}:predict"
    import time as _t
    t0 = _t.perf_counter()
    try:
        async with httpx.AsyncClient(timeout=60) as cli:
            r = await cli.post(url, json=payload)
    except Exception as e:
        raise HTTPException(503, f"서비스에 연결하지 못했습니다: {e}")
    ms = round((_t.perf_counter() - t0) * 1000, 1)
    if r.status_code >= 400:
        raise HTTPException(r.status_code, f"모델 응답 오류: {r.text[:200]}")

    kind = "이미지 1장" if "image_b64" in str(payload)[:200] else "수치 입력"
    return {"latencyMs": ms, "input": kind, "format": fmt,
            "result": r.json()}


class ModelBenchRequest(BaseModel):
    requests: int = 50
    concurrency: int = 5


@app.post("/api/v1/benchmarks/model/{name}/run")
async def run_model_bench(name: str, body: ModelBenchRequest):
    import asyncio, time as _t
    ns, fmt = "llm-serving", "sklearn"
    s = _find_isvc(name)
    if s is not None:
        ns = s["metadata"]["namespace"]
        fmt = ((s["spec"]["predictor"].get("model") or {})
               .get("modelFormat") or {}).get("name", "sklearn")
    if fmt == "vllm":
        # LLM은 예측 API가 아니라 OpenAI 호환 대화 API를 쓴다.
        # 노드가 달라도 닿도록 통로(port-forward)를 경유한다.
        port = _llm_endpoint(name)
        url = f"http://127.0.0.1:{port}/v1/completions"
        payload = {
            "model": _llm_model_name(port),
            "prompt": "Explain GPU scheduling in one sentence.",
            "max_tokens": 32,
        }
    else:
        base = _svc_base(ns, f"{name}-predictor")
        model_name = await _resolve_model_name(name, base)
        url = f"{base}/v1/models/{model_name}:predict"
        payload = _sample_payload(fmt)
        if payload is None:
            raise HTTPException(
                400, f"'{fmt}' 형식은 부하 테스트 입력을 만들 수 없습니다")

    latencies: list[float] = []
    errors = 0
    sem = asyncio.Semaphore(body.concurrency)

    async def one(cli: httpx.AsyncClient):
        nonlocal errors
        async with sem:
            t0 = _t.perf_counter()
            try:
                r = await cli.post(url, json=payload, timeout=15)
                if r.status_code == 200:
                    latencies.append((_t.perf_counter() - t0) * 1000)
                else:
                    errors += 1
            except Exception:
                errors += 1

    t_start = _t.perf_counter()
    async with httpx.AsyncClient() as cli:
        await asyncio.gather(*[one(cli) for _ in range(body.requests)])
    wall = _t.perf_counter() - t_start

    if latencies:
        ls = sorted(latencies)
        pct = lambda p: round(ls[min(len(ls) - 1, int(len(ls) * p))], 1)
        result = {
            "name": name, "requests": body.requests,
            "concurrency": body.concurrency,
            "rps": round(len(ls) / wall, 1),
            "avgMs": round(sum(ls) / len(ls), 1),
            "p50Ms": pct(0.50), "p95Ms": pct(0.95), "p99Ms": pct(0.99),
            "errorRate": round(errors / body.requests, 3),
            "at": datetime.now(timezone.utc).strftime("%H:%M:%S"),
        }
    else:
        result = {"name": name, "requests": body.requests, "rps": 0,
                  "errorRate": 1.0, "at": datetime.now(timezone.utc).strftime("%H:%M:%S")}
    _model_bench_results[name] = result
    return result


@app.get("/api/v1/benchmarks/model")
def list_model_bench():
    return {"items": list(_model_bench_results.values())}


# ══════════════════════════════════════════════════════════════════
# Notebook 상세 — 작업공간 파일 목록 · Pod 리소스 사용량(metrics-server)
# ══════════════════════════════════════════════════════════════════
from kubernetes.stream import stream as _stream


@app.get("/api/v1/notebooks/{name}/files")
def notebook_files(name: str, namespace: str = DEFAULT_NS, path: str = ""):
    nb = _get_nb(name, namespace)
    namespace = nb["metadata"]["namespace"]
    home = "/home/jovyan"
    target = f"{home}/{path}".rstrip("/")
    if ".." in path:
        raise HTTPException(400, "invalid path")
    try:
        raw = str(_stream(_exec_api().connect_get_namespaced_pod_exec,
                          f"{name}-0", namespace,
                          command=["sh", "-c",
                                   f"ls -lA --time-style=+%Y-%m-%dT%H:%M {target} 2>/dev/null | tail -n +2"],
                          stderr=True, stdin=False, stdout=True, tty=False,
                          _request_timeout=10))
    except Exception as e:
        raise HTTPException(503, f"파일 목록 조회 실패: {e}")
    items = []
    for line in raw.splitlines():
        parts = line.split()
        if len(parts) < 7:
            continue
        perms, size, mtime, fname = parts[0], parts[4], parts[5], " ".join(parts[6:])
        items.append({
            "name": fname + ("/" if perms.startswith("d") else ""),
            "size": "—" if perms.startswith("d") else _human_size(int(size)),
            "modified": mtime,
            "isDir": perms.startswith("d"),
        })
    items.sort(key=lambda x: (not x["isDir"], x["name"].lower()))
    return {"items": items, "path": path or "/"}


def _pvc_size_gb(namespace: str, pvc: str | None) -> int:
    """PVC 스펙의 요청 용량(GB). 없거나 조회 실패 시 0."""
    if not pvc:
        return 0
    try:
        obj = core.read_namespaced_persistent_volume_claim(pvc, namespace)
        raw = (obj.spec.resources.requests or {}).get("storage", "0Gi")
        m = _re.match(r"(\d+)(Gi|G|Mi|M)?", str(raw))
        if not m:
            return 0
        n, unit = int(m.group(1)), m.group(2) or "Gi"
        return n if unit in ("Gi", "G") else max(1, n // 1024)
    except Exception:
        return 0


@app.get("/api/v1/notebooks/{name}/volume")
def notebook_volume(name: str, namespace: str = DEFAULT_NS):
    """볼륨 총 용량(PVC 스펙) + 실제 사용량(pod 안 du 실측)."""
    nb = _get_nb(name, namespace)
    namespace = nb["metadata"]["namespace"]
    pvc = next((v["persistentVolumeClaim"]["claimName"]
                for v in nb["spec"]["template"]["spec"].get("volumes", [])
                if v.get("persistentVolumeClaim")), None)
    size_gb = _pvc_size_gb(namespace, pvc)
    used_bytes = None
    try:
        raw = str(_stream(_exec_api().connect_get_namespaced_pod_exec,
                          f"{name}-0", namespace,
                          command=["sh", "-c",
                                   "du -s -B1 /home/jovyan 2>/dev/null | cut -f1"],
                          stderr=True, stdin=False, stdout=True, tty=False,
                          _request_timeout=20))
        m = _re.search(r"(\d+)", raw)
        if m:
            used_bytes = int(m.group(1))
    except Exception:
        pass  # 정지 상태 등 — 사용량 없이 총 용량만 반환
    return {
        "pvcName": pvc or "-",
        "sizeGb": size_gb,
        "usedBytes": used_bytes,
        "usedHuman": _human_size(used_bytes) if used_bytes is not None else None,
        "usedPercent": (round(used_bytes / (size_gb * 1024**3) * 100, 1)
                        if used_bytes is not None and size_gb else None),
    }


def _human_size(n: int) -> str:
    for unit in ["B", "KB", "MB", "GB"]:
        if n < 1024:
            return f"{n:.1f} {unit}" if unit != "B" else f"{n} B"
        n /= 1024
    return f"{n:.1f} TB"


# ── 볼륨 스냅샷 (MinIO 오브젝트 스토리지 저장) ─────────────────────
# k3s local-path는 CSI가 아니라 VolumeSnapshot 미지원 → 파일 아카이브 방식.
# 아카이브는 모델 저장소와 같은 MinIO에 보관하므로 백엔드가 어디서 돌든 동일하게 접근된다.
# CSI 스토리지(운영)에서는 snapshot.storage.k8s.io/VolumeSnapshot으로 교체 가능.
SNAPSHOT_BUCKET = _os.environ.get("SNAPSHOT_BUCKET", "snapshots")


def _snapshot_store():
    cli = _minio_client()
    if not cli.bucket_exists(SNAPSHOT_BUCKET):
        cli.make_bucket(SNAPSHOT_BUCKET)
    return cli


@app.get("/api/v1/notebooks/{name}/snapshots")
def list_snapshots(name: str, namespace: str = DEFAULT_NS):
    _route_ns(namespace)                 # 스냅샷은 노트북이 있는 클러스터의 저장소에 있다
    cli = _snapshot_store()
    items = []
    for obj in cli.list_objects(SNAPSHOT_BUCKET, prefix=f"{name}/", recursive=True):
        fname = obj.object_name.split("/")[-1]
        stamp = fname.replace(".tar.gz", "")
        items.append({
            "id": obj.object_name,
            "createdAt": stamp.replace("_", " "),
            "sizeMb": round((obj.size or 0) / 1024 / 1024, 1),
        })
    items.sort(key=lambda x: x["createdAt"], reverse=True)
    return {"items": items}


@app.post("/api/v1/notebooks/{name}/snapshots", status_code=201)
def create_snapshot(name: str, namespace: str = DEFAULT_NS):
    import tempfile
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    stamp = datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
    key = f"{name}/{stamp}.tar.gz"
    cmd = ("cd /home/jovyan && tar czf - --exclude=.cache --exclude=.npm "
           "--exclude=.conda --exclude=.local --exclude=.ipython . 2>/dev/null")

    with tempfile.NamedTemporaryFile(suffix=".tar.gz", delete=False) as tmp:
        tmp_path = tmp.name
    try:
        with open(tmp_path, "wb") as f:
            proc = subprocess.run(
                ["kubectl", "-n", ns, "exec", f"{name}-0", "--", "sh", "-c", cmd],
                stdout=f, stderr=subprocess.DEVNULL, timeout=900, env=_kenv())
        size = _os.path.getsize(tmp_path)
        if proc.returncode != 0 and size == 0:
            raise HTTPException(503, "스냅샷 실패 — 노트북이 실행 중인지 확인하세요")
        cli = _snapshot_store()
        cli.fput_object(SNAPSHOT_BUCKET, key, tmp_path)
    finally:
        if _os.path.exists(tmp_path):
            _os.remove(tmp_path)

    return {"id": key, "sizeMb": round(size / 1024 / 1024, 1),
            "createdAt": stamp.replace("_", " ")}


@app.post("/api/v1/notebooks/{name}/snapshots/{snap_id:path}/restore")
def restore_snapshot(name: str, snap_id: str, namespace: str = DEFAULT_NS):
    import tempfile
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    cli = _snapshot_store()
    with tempfile.NamedTemporaryFile(suffix=".tar.gz", delete=False) as tmp:
        tmp_path = tmp.name
    try:
        try:
            cli.fget_object(SNAPSHOT_BUCKET, snap_id, tmp_path)
        except Exception:
            raise HTTPException(404, "스냅샷을 찾을 수 없습니다")
        with open(tmp_path, "rb") as f:
            # -m: mtime 복원 생략(권한 경고 방지), --warning=none: 경고로 실패 처리 안 함
            proc = subprocess.run(
                ["kubectl", "-n", ns, "exec", "-i", f"{name}-0", "--", "sh", "-c",
                 "cd /home/jovyan && tar xzmf - --warning=none 2>/dev/null; echo DONE"],
                stdin=f, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                timeout=900, env=_kenv())
    finally:
        if _os.path.exists(tmp_path):
            _os.remove(tmp_path)
    if b"DONE" not in (proc.stdout or b""):
        raise HTTPException(503, "복원 실패 — 노트북이 실행 중인지 확인하세요")
    return {"status": "Restored", "id": snap_id}


@app.delete("/api/v1/notebooks/{name}/snapshots/{snap_id:path}")
def delete_snapshot(name: str, snap_id: str, namespace: Optional[str] = None):
    _route_ns(namespace)                 # 화면이 namespace 를 넘기면 그 워크스페이스의 클러스터 저장소에서
    cli = _snapshot_store()
    try:
        cli.remove_object(SNAPSHOT_BUCKET, snap_id)
    except Exception:
        raise HTTPException(404, "스냅샷을 찾을 수 없습니다")
    return {"status": "Deleted"}


# ── 리소스 변경 (CPU/MEM/GPU 재할당) ───────────────────────────────
class ResizeBody(BaseModel):
    cpu: str
    memoryGi: int
    acceleratorKind: Optional[str] = ACCEL_RESOURCE
    acceleratorCount: int = 0


@app.patch("/api/v1/notebooks/{name}/resources")
def resize_notebook(name: str, body: ResizeBody, namespace: str = DEFAULT_NS):
    """Notebook CRD의 resources 수정 → 컨트롤러가 Pod 재생성(작업 파일 유지)."""
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    resources: dict = {
        "requests": {"cpu": body.cpu, "memory": f"{body.memoryGi}Gi"},
        "limits": {},
    }
    if body.acceleratorKind and body.acceleratorCount > 0:
        resources["requests"][body.acceleratorKind] = str(body.acceleratorCount)
        resources["limits"][body.acceleratorKind] = str(body.acceleratorCount)
    # 주의: CRD 패치는 배열을 통째로 교체한다. 컨테이너를 일부만 적어 보내면
    # volumeMounts(작업 볼륨)·env 같은 나머지 설정이 사라진다.
    # → 기존 컨테이너를 그대로 복사하고 resources만 바꿔서 보낸다.
    cur = nb["spec"]["template"]["spec"]["containers"][0]
    container = dict(cur)
    container["resources"] = resources
    # 과거 버전이 볼륨 연결을 날린 노트북이 있어, 없으면 되살린다
    if not container.get("volumeMounts"):
        vols = nb["spec"]["template"]["spec"].get("volumes") or []
        ws = next((v["name"] for v in vols if v.get("persistentVolumeClaim")), None)
        if ws:
            container["volumeMounts"] = [{"name": ws,
                                          "mountPath": "/home/jovyan"}]
    pod_spec: dict = {"containers": [container]}
    # GPU 유무에 따라 nvidia 런타임 지정/해제
    if GPU_RUNTIME_CLASS:
        pod_spec["runtimeClassName"] = (
            GPU_RUNTIME_CLASS if body.acceleratorCount > 0 else None)
    crd.patch_namespaced_custom_object(
        GROUP, VERSION, ns, PLURAL, name,
        {"spec": {"template": {"spec": pod_spec}}})
    # 변경 반영을 위해 Pod 재생성
    try:
        core.delete_namespaced_pod(f"{name}-0", ns)
    except client.exceptions.ApiException:
        pass
    return {"status": "Resizing",
            "message": "자원 변경 적용 중 — 노트북이 재시작됩니다 (작업 파일은 유지)"}


@app.get("/api/v1/notebooks/{name}/events")
def notebook_events(name: str, namespace: str = DEFAULT_NS, limit: int = 20):
    """Pod 이벤트 — 스케줄·이미지 pull·시작 등 실제 기록."""
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    try:
        evs = core.list_namespaced_event(
            ns, field_selector=f"involvedObject.name={name}-0").items
    except client.exceptions.ApiException:
        return {"items": []}

    def _ts(e):
        return e.last_timestamp or e.event_time or e.metadata.creation_timestamp

    evs = [e for e in evs if _ts(e)]
    evs.sort(key=_ts, reverse=True)
    items = []
    for e in evs[:limit]:
        items.append({
            "time": _ts(e).strftime("%m-%d %H:%M:%S"),
            "reason": e.reason or "",
            "message": (e.message or "").strip()[:300],
            "type": e.type or "Normal",
            "count": e.count or 1,
        })
    return {"items": items}


@app.get("/api/v1/notebooks/{name}/env")
def notebook_env(name: str, namespace: str = DEFAULT_NS):
    """컨테이너 환경 변수 + Pod 배치 정보 (민감값은 가림)."""
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    SENSITIVE = ("SECRET", "TOKEN", "PASSWORD", "KEY", "CRED")
    env_items, pod_info = [], {}
    try:
        pod = core.read_namespaced_pod(f"{name}-0", ns)
        c = pod.spec.containers[0]
        for e in (c.env or []):
            if e.value is not None:
                val = e.value
                if any(s in (e.name or "").upper() for s in SENSITIVE):
                    val = "••••••"
                src = "직접 지정"
            else:
                val = "(참조값)"
                src = "Secret/ConfigMap 참조"
            env_items.append({"name": e.name, "value": val, "source": src})
        pod_info = {
            "podName": pod.metadata.name,
            "node": pod.spec.node_name or "-",
            "podIP": pod.status.pod_ip or "-",
            "image": c.image,
            "runtimeClass": pod.spec.runtime_class_name or "-",
            "serviceAccount": pod.spec.service_account_name or "-",
            "startedAt": (pod.status.start_time.strftime("%Y-%m-%d %H:%M")
                          if pod.status.start_time else "-"),
        }
    except client.exceptions.ApiException:
        pass
    env_items.sort(key=lambda x: x["name"])
    return {"items": env_items, "pod": pod_info}


@app.get("/api/v1/notebooks/{name}/gpu")
def notebook_gpu(name: str, namespace: str = DEFAULT_NS):
    """이 노트북이 올라간 노드의 GPU 실측 + 노트북의 할당량."""
    nb = _get_nb(name, namespace)
    ns = nb["metadata"]["namespace"]
    c = nb["spec"]["template"]["spec"]["containers"][0]
    accel = _accel(c.get("resources") or {})
    try:
        pod = core.read_namespaced_pod(f"{name}-0", ns)
        node = pod.spec.node_name
        phase = pod.status.phase
    except client.exceptions.ApiException:
        node, phase = None, "Unknown"

    gpu = None
    if node:
        for g in gpu_metrics()["items"]:
            if g.get("node") == node and g.get("gpu") != "unavailable":
                gpu = g
                break
    return {
        "node": node or "-",
        "podPhase": phase,
        "allocated": {"kind": accel["kind"], "count": accel["count"]} if accel else None,
        "nodeGpu": gpu,
    }


@app.get("/api/v1/notebooks/{name}/stats")
def notebook_stats(name: str, namespace: str = DEFAULT_NS):
    """metrics-server 기반 Pod CPU(mCore)·메모리(Mi) 순간값."""
    nb = _get_nb(name, namespace)
    namespace = nb["metadata"]["namespace"]
    try:
        m = crd.get_namespaced_custom_object(
            "metrics.k8s.io", "v1beta1", namespace, "pods", f"{name}-0")
    except client.exceptions.ApiException as e:
        raise HTTPException(503 if e.status != 404 else 404,
                            "메트릭 없음 — Pod이 실행 중인지 확인")
    c = m["containers"][0]["usage"]
    cpu_raw, mem_raw = c["cpu"], c["memory"]
    cpu_m = (int(cpu_raw[:-1]) / 1e6 if cpu_raw.endswith("n")
             else float(cpu_raw.rstrip("m") or 0))
    mem_mi = _qty_to_num(mem_raw) * 1024  # Gi→Mi 환산
    return {"cpuMilli": round(cpu_m, 1), "memMi": round(mem_mi, 1)}


# ══════════════════════════════════════════════════════════════════
# Pipeline Bottleneck — Argo Workflow 단계별 실행시간 실측
# ══════════════════════════════════════════════════════════════════
@app.get("/api/v1/pipelines/{pipeline_id}/run-history")
def pipeline_run_history(pipeline_id: str, limit: int = 10):
    """최근 실행별 총 소요시간 — '실행 시간 추이' 차트용 실측."""
    runs = [r for r in _kfp_get("/apis/v2beta1/runs", {"page_size": 200}).get("runs", [])
            if _run_pipeline_id(r) == pipeline_id]
    out = []
    for r in runs:
        s, f = r.get("created_at"), r.get("finished_at")
        if not s or not f or f.startswith("1970"):
            continue
        try:
            t0 = datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ")
            t1 = datetime.strptime(f, "%Y-%m-%dT%H:%M:%SZ")
        except ValueError:
            continue
        out.append({
            "run": r.get("display_name", "")[:24],
            "durSec": max(0, int((t1 - t0).total_seconds())),
            "state": r.get("state", ""),
            "at": s[5:16].replace("T", " "),
        })
    out.reverse()
    return {"items": out[-limit:]}


@app.get("/api/v1/metrics/pipeline-runs")
def pipeline_run_metrics(limit: int = 6):
    """최근 파이프라인 실행들의 단계별 소요시간 분해 — 병목 단계 식별.

    출처는 KFP의 실행 기록(MySQL 영구 저장)이다. Argo Workflow 객체는 실행이
    끝나면 클러스터에서 지워지므로 그것만 보면 최근 한두 건밖에 안 남고,
    파이프라인이 여러 개여도 하나만 화면에 뜬다.
    """
    runs = _kfp_get("/apis/v2beta1/runs", {"page_size": 200}).get("runs", [])
    runs.sort(key=lambda r: r.get("created_at", ""), reverse=True)

    # 파이프라인 id → 이름 (어느 모델의 실행인지 화면에 보여주기 위해)
    pnames: dict = {}
    try:
        for pl in _kfp_get("/apis/v2beta1/pipelines",
                           {"page_size": 100}).get("pipelines", []):
            pnames[pl.get("pipeline_id")] = pl.get("display_name") or ""
    except Exception:
        pass

    # 파이프라인이 만든 모델을 실제로 서비스하는 곳을 찾아 붙인다.
    # 같은 모델을 여러 서비스가 함께 쓸 수 있어(예: 두 서비스가 같은
    # s3://models/dm-code/serve 를 쓰는 경우) 목록으로 돌려준다.
    svc_by_path: dict = {}
    try:
        for i in crd.list_cluster_custom_object(
                KSERVE_GROUP, KSERVE_VERSION, I_PLURAL)["items"]:
            uri = ((i["spec"]["predictor"].get("model") or {})
                   .get("storageUri") or "")
            if not uri:
                continue
            svc_by_path.setdefault(uri, []).append(i["metadata"]["name"])
    except Exception:
        pass

    def _services_for(pipeline_name: str) -> list:
        """파이프라인 이름과 모델 경로를 이어 붙인다.
        예: 'dm-code-train' → 's3://models/dm-code/serve'를 쓰는 서비스들"""
        toks = [t for t in pipeline_name.split("-") if t not in ("train", "")]
        if not toks:
            return []
        stem = "-".join(toks)                       # dm-code-train → dm-code
        hits = []
        for uri, names in svc_by_path.items():
            if f"/{stem}/" in uri or uri.endswith(f"/{stem}"):
                hits.extend(names)
        return sorted(set(hits))

    # KFP가 내부적으로 만드는 보조 태스크는 사용자 단계가 아니다
    def _is_step(name: str, run_name: str) -> bool:
        if not name:
            return False
        if name in ("root", "executor", run_name):
            return False
        return not (name.endswith("-driver") or name.startswith("system-"))

    out = []
    for r in runs[:limit]:
        rid = r.get("run_id")
        try:
            det = _kfp_get(f"/apis/v2beta1/runs/{rid}")
        except Exception:
            continue
        tasks = (det.get("run_details") or {}).get("task_details") or []
        run_name = r.get("display_name") or ""
        # 실행 전체를 감싸는 최상위 노드가 태스크 목록에 섞여 있다. 이름이
        # 매번 달라(dm-code-train-h69gj 등) 이름으로는 못 거르므로, 실행
        # 전체와 시간대가 거의 같은 것을 최상위로 보고 제외한다.
        _spans = [t for t in tasks if t.get("start_time") and t.get("end_time")]
        _run_span = 0
        if _spans:
            _lo = min(t["start_time"] for t in _spans)
            _hi = max(t["end_time"] for t in _spans)
            _run_span = max(0, int(
                (datetime.strptime(_hi, "%Y-%m-%dT%H:%M:%SZ")
                 - datetime.strptime(_lo, "%Y-%m-%dT%H:%M:%SZ")).total_seconds()))

        # 한 단계는 준비(driver) 태스크와 실행 태스크로 나뉘어 기록된다.
        # 사용자가 아는 단계 이름(prep·train·…)으로 시간을 합친다.
        agg: dict = {}
        drivers: dict = {}
        for t in tasks:
            name = t.get("display_name") or ""
            s0, s1 = t.get("start_time"), t.get("end_time")
            if not s0:
                continue
            t0 = datetime.strptime(s0, "%Y-%m-%dT%H:%M:%SZ")
            t1 = (datetime.strptime(s1, "%Y-%m-%dT%H:%M:%SZ") if s1
                  else datetime.now(timezone.utc).replace(tzinfo=None))
            dur = max(0, int((t1 - t0).total_seconds()))
            if name.endswith("-driver"):
                base = name[:-7]
                drivers[base] = drivers.get(base, 0) + dur
                continue
            if not _is_step(name, run_name):
                continue
            if _run_span and dur >= _run_span * 0.9:
                continue          # 실행 전체를 감싸는 최상위 노드
            cur = agg.get(name)
            if cur is None:
                agg[name] = {"name": name, "durSec": dur,
                             "phase": t.get("state") or "", "startedAt": s0,
                             # 캐시가 적중하면 시작·종료가 같은 시각으로 기록된다
                             # (실제로 아무것도 실행하지 않았다는 뜻)
                             "cached": dur == 0}
            else:
                cur["durSec"] += dur
                cur["startedAt"] = min(cur["startedAt"], s0)
                if (t.get("state") or "") == "FAILED":
                    cur["phase"] = "FAILED"

        # 준비 시간도 그 단계가 쓴 시간이므로 합산한다
        for base, d in drivers.items():
            if base in agg:
                agg[base]["durSec"] += d
            elif base not in ("root",):
                agg[base] = {"name": base, "durSec": d, "phase": "",
                             "startedAt": "", "cached": True}

        steps = [x for x in agg.values() if x["startedAt"]]
        if not steps:
            continue
        steps.sort(key=lambda x: x["startedAt"])
        total = sum(x["durSec"] for x in steps) or 1
        for x in steps:
            x["pct"] = round(x["durSec"] / total * 100)
        bottleneck = max(steps, key=lambda x: x["durSec"])
        pid = (r.get("pipeline_version_reference") or {}).get("pipeline_id", "")
        out.append({
            "workflow": run_name or rid,
            "workflowId": rid,
            "pipeline": pnames.get(pid, ""),
            "services": _services_for(pnames.get(pid, "")),
            "cachedSteps": sum(1 for x in steps if x.get("cached")),
            "phase": {"SUCCEEDED": "Succeeded", "FAILED": "Failed"}.get(
                r.get("state") or "", r.get("state") or ""),
            "startedAt": r.get("created_at", ""),
            "totalSec": total,
            "steps": steps,
            "bottleneck": bottleneck["name"],
            "bottleneckPct": bottleneck["pct"],
        })
    return {"items": out}


# ══════════════════════════════════════════════════════════════════
# LLM Metrics — vLLM /metrics 실측 (TTFT·TPOT·E2E·KV cache·토큰 처리량)
# ══════════════════════════════════════════════════════════════════
_vllm_forward: Optional[subprocess.Popen] = None
VLLM_LOCAL_PORT = 18001


def _kill_stale_vllm_forward() -> None:
    """이전 백엔드 프로세스가 남긴 port-forward를 정리한다.

    백엔드를 재시작하면 앞선 kubectl port-forward가 고아로 살아남아
    포트를 점유한 채 이미 삭제된 파드를 가리키는 경우가 있다.
    """
    try:
        ps = subprocess.run(["ps", "-eo", "pid,args", "--no-headers"],
                            capture_output=True, text=True, timeout=10)
        for line in ps.stdout.splitlines():
            if "port-forward" in line and f"{VLLM_LOCAL_PORT}:8000" in line:
                pid = line.strip().split(None, 1)[0]
                if _vllm_forward and str(_vllm_forward.pid) == pid:
                    continue
                subprocess.run(["kill", pid], capture_output=True, timeout=10)
    except Exception:
        pass


def _vllm_forward_healthy() -> bool:
    try:
        return httpx.get(f"http://127.0.0.1:{VLLM_LOCAL_PORT}/health",
                         timeout=3).status_code < 500
    except Exception:
        return False


def _ensure_vllm_forward() -> int:
    global _vllm_forward
    import time as _t
    for attempt in range(3):
        if (_vllm_forward and _vllm_forward.poll() is None
                and _port_open(VLLM_LOCAL_PORT)):
            return VLLM_LOCAL_PORT
        if _vllm_forward and _vllm_forward.poll() is None:
            _t.sleep(1)
            if _port_open(VLLM_LOCAL_PORT):
                return VLLM_LOCAL_PORT
            _vllm_forward.terminate()
        # 우리 것이 아닌데 포트가 열려 있으면 — 남은 고아 forward
        if _port_open(VLLM_LOCAL_PORT):
            if _vllm_forward is None and _vllm_forward_healthy():
                return VLLM_LOCAL_PORT      # 살아있으면 그대로 재사용
            _kill_stale_vllm_forward()
            _t.sleep(1.5)
        # LLM_SERVICE 형식: <네임스페이스>/<서비스>[:<포트>] (기본 8000)
        llm_ns, llm_svc = LLM_SERVICE.split("/", 1)
        llm_port = "8000"
        if ":" in llm_svc:
            llm_svc, llm_port = llm_svc.rsplit(":", 1)
        _vllm_forward = subprocess.Popen(
            ["kubectl", "-n", llm_ns, "port-forward",
             f"svc/{llm_svc}", f"{VLLM_LOCAL_PORT}:{llm_port}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=_kenv())
        _t.sleep(1.5)
    raise HTTPException(503, "vLLM 연결 준비 중")


def _prom_val(text: str, metric: str) -> float:
    m = _re.search(rf'^{_re.escape(metric)}\{{[^}}]*\}}\s+([0-9.eE+-]+)',
                   text, _re.M)
    return float(m.group(1)) if m else 0.0


@app.get("/api/v1/metrics/llm")
def llm_metrics():
    port = _ensure_vllm_forward()
    try:
        r = httpx.get(f"http://127.0.0.1:{port}/metrics", timeout=8)
        r.raise_for_status()
    except Exception as e:
        raise HTTPException(503, f"vLLM metrics 조회 실패: {e}")
    t = r.text
    ttft_s, ttft_n = _prom_val(t, "vllm:time_to_first_token_seconds_sum"), \
        _prom_val(t, "vllm:time_to_first_token_seconds_count")
    tpot_s, tpot_n = _prom_val(t, "vllm:time_per_output_token_seconds_sum"), \
        _prom_val(t, "vllm:time_per_output_token_seconds_count")
    e2e_s, e2e_n = _prom_val(t, "vllm:e2e_request_latency_seconds_sum"), \
        _prom_val(t, "vllm:e2e_request_latency_seconds_count")
    return {
        "model": LLM_MODEL,
        "engine": "vLLM v0.6.3",
        "node": LLM_NODE_DESC or LLM_SERVICE,
        "requests": int(e2e_n),
        "ttftMs": round(ttft_s / ttft_n * 1000, 1) if ttft_n else None,
        "tpotMs": round(tpot_s / tpot_n * 1000, 2) if tpot_n else None,
        "e2eMs": round(e2e_s / e2e_n * 1000, 1) if e2e_n else None,
        "tokensPerSec": round(1 / (tpot_s / tpot_n), 1) if tpot_s else None,
        "promptTokens": int(_prom_val(t, "vllm:prompt_tokens_total")),
        "generationTokens": int(_prom_val(t, "vllm:generation_tokens_total")),
        "kvCacheUsagePct": round(_prom_val(t, "vllm:gpu_cache_usage_perc") * 100, 2),
        "runningReqs": int(_prom_val(t, "vllm:num_requests_running")),
    }


@app.get("/api/v1/metrics/llms")
def llm_metrics_all():
    """배포된 LLM 전부의 추론 지표.

    LLM이 여러 개일 수 있으므로 설정된 하나만 보던 방식을 대체한다.
    """
    items = []
    for cid in _listing_cids():          # PaaS 모드는 클러스터마다 모아 합친다
        with _use_cluster(cid):
            try:
                isvcs = crd.list_cluster_custom_object(KSERVE_GROUP, KSERVE_VERSION, I_PLURAL)["items"]
            except Exception:
                if not MULTI:
                    raise
                continue
            items += [_tag(e) for e in (_llm_entry(s) for s in isvcs) if e]
    return {"items": items}


def _llm_entry(s: dict) -> Optional[dict]:
    """vLLM InferenceService 하나의 추론 지표(지금 클러스터) — LLM 이 아니면 None."""
    model = s["spec"]["predictor"].get("model") or {}
    if (model.get("modelFormat") or {}).get("name") != "vllm":
        return None
    name, ns = s["metadata"]["name"], s["metadata"]["namespace"]
    ready = any(c.get("type") == "Ready" and c.get("status") == "True"
                for c in (s.get("status", {}).get("conditions") or []))
    # 어떤 모델을 서비스하는지는 실행 인자에 들어 있다
    model_id = next((a.split("=", 1)[1] for a in (model.get("args") or [])
                     if a.startswith("--model=")), "")
    entry = {"name": name, "namespace": ns, "modelId": model_id,
             "ready": ready, "requests": 0, "ttftMs": None,
             "tpotMs": None, "e2eMs": None, "tokensPerSec": None,
             "kvCacheUsagePct": 0.0, "runningReqs": 0}
    if ready:
        try:
            port = _ensure_svc_forward(ns, f"{name}-predictor")
            t = httpx.get(f"http://127.0.0.1:{port}/metrics",
                          timeout=8).text
            ttft_s = _prom_val(t, "vllm:time_to_first_token_seconds_sum")
            ttft_n = _prom_val(t, "vllm:time_to_first_token_seconds_count")
            tpot_s = _prom_val(t, "vllm:time_per_output_token_seconds_sum")
            tpot_n = _prom_val(t, "vllm:time_per_output_token_seconds_count")
            e2e_s = _prom_val(t, "vllm:e2e_request_latency_seconds_sum")
            e2e_n = _prom_val(t, "vllm:e2e_request_latency_seconds_count")
            entry.update(
                requests=int(e2e_n),
                ttftMs=round(ttft_s / ttft_n * 1000, 1) if ttft_n else None,
                tpotMs=round(tpot_s / tpot_n * 1000, 2) if tpot_n else None,
                e2eMs=round(e2e_s / e2e_n * 1000, 1) if e2e_n else None,
                tokensPerSec=(round(1 / (tpot_s / tpot_n), 1)
                              if tpot_s and tpot_n else None),
                kvCacheUsagePct=round(
                    _prom_val(t, "vllm:gpu_cache_usage_perc") * 100, 2),
                runningReqs=int(_prom_val(t, "vllm:num_requests_running")),
            )
        except Exception:
            pass
    return entry


def _prom_hist(text: str, metric: str) -> list[tuple[float, float]]:
    """히스토그램 bucket 파싱 → [(le, 누적개수)] 정렬 리스트."""
    out = []
    for m in _re.finditer(rf'^{_re.escape(metric)}_bucket\{{([^}}]*)\}}\s+([0-9.eE+-]+)',
                          text, _re.M):
        labels, val = m.group(1), float(m.group(2))
        le = _re.search(r'le="([^"]+)"', labels)
        if not le:
            continue
        try:
            bound = float("inf") if le.group(1) == "+Inf" else float(le.group(1))
        except ValueError:
            continue
        out.append((bound, val))
    return sorted(out)


def _hist_quantile(buckets: list[tuple[float, float]], q: float) -> Optional[float]:
    """히스토그램에서 분위수 추정 (선형 보간)."""
    if not buckets:
        return None
    total = buckets[-1][1]
    if total <= 0:
        return None
    target = total * q
    prev_b, prev_c = 0.0, 0.0
    for bound, cum in buckets:
        if cum >= target:
            if bound == float("inf"):
                return prev_b
            span = cum - prev_c
            if span <= 0:
                return bound
            return prev_b + (bound - prev_b) * ((target - prev_c) / span)
        prev_b, prev_c = bound, cum
    return buckets[-1][0]


# 추론 지표 시계열 (백엔드 메모리, 최근 120개 = 약 20분)
_llm_series: dict[str, list[dict]] = {}


def _collect_llm_detail(service: Optional[str] = None) -> dict:
    port = _llm_endpoint(service)
    r = httpx.get(f"http://127.0.0.1:{port}/metrics", timeout=8)
    r.raise_for_status()
    t = r.text

    def avg(sum_m, cnt_m):
        s, c = _prom_val(t, sum_m), _prom_val(t, cnt_m)
        return (s / c * 1000) if c else None

    e2e_b = _prom_hist(t, "vllm:e2e_request_latency_seconds")
    ttft_b = _prom_hist(t, "vllm:time_to_first_token_seconds")
    tpot_b = _prom_hist(t, "vllm:time_per_output_token_seconds")
    gen_b = _prom_hist(t, "vllm:request_generation_tokens")

    def q(buckets, p):
        v = _hist_quantile(buckets, p)
        return round(v * 1000, 1) if v is not None else None

    ttft = avg("vllm:time_to_first_token_seconds_sum",
               "vllm:time_to_first_token_seconds_count")
    tpot = avg("vllm:time_per_output_token_seconds_sum",
               "vllm:time_per_output_token_seconds_count")
    e2e = avg("vllm:e2e_request_latency_seconds_sum",
              "vllm:e2e_request_latency_seconds_count")
    gen_tokens_avg = (_prom_val(t, "vllm:request_generation_tokens_sum") /
                      max(_prom_val(t, "vllm:request_generation_tokens_count"), 1))

    # 단계 분해: 첫 토큰까지(프리필+대기) vs 나머지 토큰 생성(디코드)
    decode_ms = (tpot * max(gen_tokens_avg - 1, 0)) if (tpot and gen_tokens_avg) else None
    other_ms = (round(e2e - (ttft or 0) - (decode_ms or 0), 1)
                if e2e is not None else None)

    return {
        "model": LLM_MODEL,
        "requests": int(_prom_val(t, "vllm:e2e_request_latency_seconds_count")),
        "latency": {
            "ttftMs": round(ttft, 1) if ttft else None,
            "tpotMs": round(tpot, 2) if tpot else None,
            "e2eMs": round(e2e, 1) if e2e else None,
            "p50Ms": q(e2e_b, 0.50), "p95Ms": q(e2e_b, 0.95), "p99Ms": q(e2e_b, 0.99),
            "ttftP95Ms": q(ttft_b, 0.95), "tpotP95Ms": q(tpot_b, 0.95),
        },
        # ETRI 검토의견 대응: 응답 지연을 구간별로 분해해서 제시
        "breakdown": [
            {"stage": "첫 토큰까지 (대기+프리필)", "ms": round(ttft, 1) if ttft else 0,
             "key": "ttft"},
            {"stage": "나머지 토큰 생성 (디코드)", "ms": round(decode_ms, 1) if decode_ms else 0,
             "key": "decode"},
            {"stage": "기타 (전송·후처리)", "ms": max(other_ms or 0, 0), "key": "other"},
        ],
        # vLLM 표준 지표 — ETRI가 검증 요청한 KV cache 항목
        # (prefix hit rate는 측정 표본이 없으면 -1을 반환하므로 None 처리)
        "cache": {
            "gpuUsagePct": round(_prom_val(t, "vllm:gpu_cache_usage_perc") * 100, 2),
            "prefixHitPct": (lambda v: round(v * 100, 2) if v >= 0 else None)(
                _prom_val(t, "vllm:gpu_prefix_cache_hit_rate")),
            "cpuUsagePct": round(_prom_val(t, "vllm:cpu_cache_usage_perc") * 100, 2),
        },
        "queue": {
            "running": int(_prom_val(t, "vllm:num_requests_running")),
            "waiting": int(_prom_val(t, "vllm:num_requests_waiting")),
            "swapped": int(_prom_val(t, "vllm:num_requests_swapped")),
            "preemptions": int(_prom_val(t, "vllm:num_preemptions_total")),
        },
        "throughput": {
            "genTokPerSec": round(_prom_val(t, "vllm:avg_generation_throughput_toks_per_s"), 1),
            "promptTokPerSec": round(_prom_val(t, "vllm:avg_prompt_throughput_toks_per_s"), 1),
            "avgOutputTokens": round(gen_tokens_avg, 1),
            "p95OutputTokens": _hist_quantile(gen_b, 0.95),
        },
    }


@app.get("/api/v1/metrics/llm/detail")
def llm_detail(service: Optional[str] = None):
    """추론 지연 분해·분위수·KV cache·대기열 — 병목 분석 화면용 실측."""
    try:
        d = _collect_llm_detail(service)
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(503, f"vLLM 지표 조회 실패: {e}")

    # 시계열 누적 (트렌드 차트용)
    stamp = datetime.now().strftime("%H:%M:%S")
    lat = d["latency"]
    key = service or "(기본)"
    series = _llm_series.setdefault(key, [])
    if lat["p50Ms"] is not None:
        series.append({"t": stamp, "p50": lat["p50Ms"],
                       "p95": lat["p95Ms"], "p99": lat["p99Ms"]})
        del series[:-120]
    d["series"] = series
    return d


# ── 연산(op) 단위 실측 프로파일링 ────────────────────────────────
# vLLM 내장 torch profiler(VLLM_TORCH_PROFILER_DIR)를 필요할 때만 켜서
# 실제 추론 1회를 추적하고, 연산별 소요시간을 집계한다.
# 상시 수집하지 않는 이유: 프로파일러가 켜져 있으면 추론이 느려진다.

# 트레이스를 파드 안에서 파싱해 JSON 한 줄로 출력하는 스크립트
_PROFILE_PARSE = r'''
import gzip, json, glob, os, collections, sys
fs = sorted(glob.glob("/profiles/*.pt.trace.json.gz"), key=os.path.getmtime)
if not fs:
    print(json.dumps({"error": "트레이스 파일이 생성되지 않았습니다"})); sys.exit()
d = json.load(gzip.open(fs[-1]))
ops = [e for e in d.get("traceEvents", [])
       if e.get("cat") == "cpu_op" and "dur" in e and "ts" in e]
# 스레드별로 중첩을 풀어 self time(자기 자신이 쓴 시간) 계산
by_tid = collections.defaultdict(list)
for e in ops:
    by_tid[(e.get("pid"), e.get("tid"))].append(e)
calls = collections.Counter()
for e in ops:
    calls[e["name"]] += 1
# self time = 자기 구간에서 자식 연산이 쓴 시간을 뺀 값
self_us = collections.Counter()
for evs in by_tid.values():
    evs.sort(key=lambda e: (e["ts"], -e["dur"]))
    stack = []
    for e in evs:
        while stack and stack[-1]["end"] <= e["ts"]:
            done = stack.pop()
            self_us[done["name"]] += done["self"]
        if stack:
            stack[-1]["self"] -= e["dur"]
        stack.append({"end": e["ts"] + e["dur"], "self": e["dur"], "name": e["name"]})
    while stack:
        done = stack.pop()
        self_us[done["name"]] += done["self"]
os.remove(fs[-1])
print(json.dumps({
    "file": os.path.basename(fs[-1]),
    "events": len(ops),
    "ops": [{"op": n, "us": round(v, 1), "calls": calls[n]}
            for n, v in self_us.most_common(40) if v > 0],
}))
'''

# 연산 이름 → 사람이 읽는 구성요소 이름
_OP_LABELS: list[tuple[str, str, str]] = [
    ("unified_flash_attention", "Attention (FlashAttention)", "어텐션 커널"),
    ("attention", "Attention", "어텐션 연산"),
    ("aten::mm", "선형계층 행렬곱 (GEMM)", "가중치 곱셈"),
    ("aten::addmm", "선형계층 행렬곱 (GEMM)", "가중치 곱셈"),
    ("aten::matmul", "선형계층 행렬곱 (GEMM)", "가중치 곱셈"),
    ("aten::linear", "선형계층 (Linear)", "FFN·projection"),
    ("log_softmax", "샘플링 (다음 토큰 선택)", "확률 계산"),
    ("softmax", "샘플링 (다음 토큰 선택)", "확률 계산"),
    ("topk", "샘플링 (다음 토큰 선택)", "후보 추리기"),
    ("aten::copy_", "메모리 복사 (GPU↔CPU)", "텐서 이동"),
    ("_to_copy", "메모리 복사 (GPU↔CPU)", "텐서 이동"),
    ("aten::to", "메모리 복사 (GPU↔CPU)", "텐서 이동"),
    ("rms_norm", "정규화 (RMSNorm)", "레이어 정규화"),
    ("layer_norm", "정규화 (LayerNorm)", "레이어 정규화"),
    ("rotary", "위치 인코딩 (RoPE)", "토큰 위치 반영"),
    ("silu", "활성함수 (SiLU/SwiGLU)", "비선형 변환"),
]


def _label_op(name: str) -> tuple[str, str]:
    low = name.lower()
    for key, label, hint in _OP_LABELS:
        if key.lower() in low:
            return label, hint
    return name, "기타 연산"


# 프로파일링 상태 — 서비스(LLM)별로 마지막 결과를 따로 보관한다
_profile_states: dict[str, dict] = {}
_profile_lock = threading.Lock()   # 프로파일러는 한 번에 하나만 (추론 부담 제한)
# 1분 주기 샘플링 (CR-AID-06) — 성능 분석 화면을 누군가 보고 있을 때만 돈다
PROFILE_SAMPLE_SEC = int(_os.environ.get("PROFILE_SAMPLE_SEC", "60"))
PROFILE_AUTO = _os.environ.get("PROFILE_AUTO", "1") != "0"
_profile_watch: dict[str, float] = {}   # 서비스 → 화면이 마지막으로 조회한 시각
_PROFILE_WATCH_TTL = 90                 # 이 시간 동안 조회가 없으면 자동 샘플링 중지


def _pstate(service: Optional[str]) -> dict:
    return _profile_states.setdefault(service or "", {
        "running": False, "at": None, "result": None, "error": None, "mode": None})


def _profile_sampler():
    """화면이 열려 있는 서비스만 PROFILE_SAMPLE_SEC마다 추론 1회를 짧게 추적한다."""
    while True:
        time.sleep(5)
        if not PROFILE_AUTO:
            continue
        now = time.time()
        for svc, seen in list(_profile_watch.items()):
            if now - seen > _PROFILE_WATCH_TTL:
                _profile_watch.pop(svc, None)
                continue
            st = _pstate(svc)
            last = st.get("_ts") or 0
            if st["running"] or now - last < PROFILE_SAMPLE_SEC:
                continue
            try:
                llm_profile_run(samples=1, max_tokens=32, service=svc or None,
                                mode="auto")
            except Exception:
                pass   # 실패 사유는 상태의 error에 남는다. 다음 주기에 다시 시도


@app.on_event("startup")
def _start_profile_sampler():
    threading.Thread(target=_profile_sampler, daemon=True,
                     name="profile-sampler").start()


def _vllm_pod(service: Optional[str] = None) -> tuple[str, str]:
    if service:
        s = _find_isvc(service)
        if s is not None:
            ns = s["metadata"]["namespace"]
            out = subprocess.run(
                ["kubectl", "-n", ns, "get", "pods",
                 "-l", f"app=isvc.{service}-predictor",
                 "--field-selector=status.phase=Running",
                 "-o", "jsonpath={.items[0].metadata.name}"],
                capture_output=True, text=True, timeout=20, env=_kenv())
            if out.stdout.strip():
                return ns, out.stdout.strip()
        raise HTTPException(404, f"{service} 파드를 찾을 수 없습니다")
    ns, name = LLM_SERVICE.split("/", 1)
    name = name.split(":", 1)[0]  # "svc:80" 형태 허용
    # KServe predictor는 app=isvc.<name> 라벨 — 구형 Deployment(app=<name>)도 시도
    for label in (f"app={name}", f"app=isvc.{name}"):
        out = subprocess.run(
            ["kubectl", "-n", ns, "get", "pods", "-l", label,
             "--field-selector=status.phase=Running",
             "-o", "jsonpath={.items[0].metadata.name}"],
            capture_output=True, text=True, timeout=20, env=_kenv())
        if out.stdout.strip():
            return ns, out.stdout.strip()
    raise HTTPException(503, "vLLM 파드를 찾을 수 없습니다")


LLM_SLO_P99_MS = float(_os.environ.get("LLM_SLO_P99_MS", "1000"))


@app.get("/api/v1/metrics/llm/insights")
def llm_insights(service: Optional[str] = None):
    """실측 지표에서 규칙 기반으로 병목 소견을 만든다 (임의 수치 없음)."""
    d = _collect_llm_detail(service)
    lat, q, cache = d["latency"], d["queue"], d["cache"]
    out: list[dict] = []

    p99 = lat.get("p99Ms")
    if p99 is not None:
        over = (p99 - LLM_SLO_P99_MS) / LLM_SLO_P99_MS * 100
        if over > 0:
            out.append({
                "severity": "critical",
                "title": f"{d['model']} 추론 지연 P99이 목표를 "
                         f"{over:.0f}% 초과하고 있습니다",
                "detail": f"누적 {d['requests']}건의 실제 요청에서 계산한 P99가 "
                          f"{p99:.0f}ms로, 목표치 {LLM_SLO_P99_MS:.0f}ms를 넘습니다.",
                "evidence": [
                    {"label": "실측 P99", "value": f"{p99:.0f}ms",
                     "detail": f"목표 {LLM_SLO_P99_MS:.0f}ms"},
                    {"label": "실측 P50", "value": f"{lat['p50Ms']:.0f}ms"
                     if lat["p50Ms"] is not None else "—", "detail": "중앙값"},
                    {"label": "대상 요청", "value": f"{d['requests']}건",
                     "detail": "측정에 사용된 누적 요청"},
                ],
                "actions": [
                    "핫스팟 프로파일링을 실행해 어떤 연산이 시간을 쓰는지 확인",
                    "출력 토큰 수(max_tokens)를 줄이거나 스트리밍 응답 적용",
                    "동시 요청이 많다면 레플리카를 늘려 대기 시간 감소",
                ],
            })
        else:
            out.append({
                "severity": "ok",
                "title": "추론 지연이 목표 범위 안에 있습니다",
                "detail": f"P99 {p99:.0f}ms / 목표 {LLM_SLO_P99_MS:.0f}ms "
                          f"(여유 {-over:.0f}%).",
                "evidence": [
                    {"label": "실측 P99", "value": f"{p99:.0f}ms",
                     "detail": f"목표 {LLM_SLO_P99_MS:.0f}ms"},
                ],
                "actions": [],
            })

    if q["preemptions"] > 0:
        out.append({
            "severity": "warning",
            "title": "KV cache 부족으로 요청이 중단(선점)된 적이 있습니다",
            "detail": f"선점 {q['preemptions']}회 — 캐시 메모리가 모자라 처리 중이던 "
                      f"요청을 되돌린 횟수입니다. 지연이 튀는 직접적인 원인입니다.",
            "evidence": [
                {"label": "선점 횟수", "value": f"{q['preemptions']}회",
                 "detail": "정상은 0"},
                {"label": "KV cache 사용률",
                 "value": f"{cache['gpuUsagePct']}%", "detail": "GPU 캐시 점유"},
            ],
            "actions": [
                "--gpu-memory-utilization 값을 올려 KV cache 공간 확대",
                "--max-model-len(최대 문맥 길이)을 낮춰 요청당 캐시 사용량 감소",
            ],
        })

    if cache["gpuUsagePct"] >= 85:
        out.append({
            "severity": "warning",
            "title": "KV cache 사용률이 포화에 가깝습니다",
            "detail": f"현재 {cache['gpuUsagePct']}% — 90%를 넘으면 선점이 "
                      f"발생하기 시작합니다.",
            "evidence": [{"label": "KV cache 사용률",
                          "value": f"{cache['gpuUsagePct']}%",
                          "detail": "임계 85%"}],
            "actions": ["레플리카 추가 또는 KV cache 공간 확대 검토"],
        })

    if q["waiting"] > 0:
        out.append({
            "severity": "warning",
            "title": "요청이 대기열에서 순서를 기다리고 있습니다",
            "detail": f"처리 중 {q['running']}건, 대기 {q['waiting']}건 — "
                      f"처리 능력보다 요청이 많이 들어오고 있습니다.",
            "evidence": [
                {"label": "대기", "value": f"{q['waiting']}건", "detail": "정상은 0"},
                {"label": "처리 중", "value": f"{q['running']}건",
                 "detail": "동시 처리량"},
            ],
            "actions": ["레플리카를 늘려 동시 처리량 확보"],
        })

    # 어디가 오래 걸리는지 (TTFT vs 디코드)
    br = {b["key"]: b["ms"] for b in d["breakdown"]}
    tot = sum(br.values())
    if tot > 0 and lat["ttftMs"] is not None:
        ttft_share = br.get("ttft", 0) / tot * 100
        if ttft_share >= 50:
            out.append({
                "severity": "info",
                "title": "지연의 대부분이 '첫 토큰까지'에서 발생합니다",
                "detail": f"전체 {tot:.0f}ms 중 첫 토큰까지가 "
                          f"{br['ttft']:.0f}ms({ttft_share:.0f}%) — 대기·프롬프트 "
                          f"처리 구간이 병목입니다.",
                "evidence": [
                    {"label": "TTFT", "value": f"{lat['ttftMs']:.0f}ms",
                     "detail": "첫 토큰까지"},
                    {"label": "비중", "value": f"{ttft_share:.0f}%",
                     "detail": "전체 지연 대비"},
                ],
                "actions": ["프롬프트 길이 축소 또는 prefix 캐시 활용"],
            })
        else:
            out.append({
                "severity": "info",
                "title": "지연의 대부분이 '토큰 생성' 구간에서 발생합니다",
                "detail": f"전체 {tot:.0f}ms 중 디코드가 "
                          f"{br.get('decode', 0):.0f}ms({100 - ttft_share:.0f}%) — "
                          f"출력 토큰 수에 비례해 늘어나는 구간입니다.",
                "evidence": [
                    {"label": "TPOT", "value": f"{lat['tpotMs']:.2f}ms"
                     if lat["tpotMs"] is not None else "—",
                     "detail": "토큰 1개당"},
                    {"label": "평균 출력",
                     "value": f"{d['throughput']['avgOutputTokens']} 토큰",
                     "detail": "요청당"},
                ],
                "actions": ["max_tokens 조정 또는 스트리밍 응답으로 체감 지연 개선"],
            })

    return {"sloP99Ms": LLM_SLO_P99_MS, "model": d["model"],
            "at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
            "items": out}


@app.get("/api/v1/metrics/llm/profile")
def llm_profile_state(service: Optional[str] = None):
    """마지막 프로파일링 결과. 조회하는 동안(화면이 열려 있는 동안) 1분 주기 샘플링이 돈다."""
    _profile_watch[service or ""] = time.time()
    st = _pstate(service)
    return {
        "running": st["running"],
        "at": st["at"],
        "error": st["error"],
        "result": st["result"],
        "mode": st["mode"],
        "auto": PROFILE_AUTO,
        "intervalSec": PROFILE_SAMPLE_SEC,
        "kernelLevel": False,
        "kernelNote": ("이 개발 장비는 WSL2 환경이라 NVIDIA CUPTI가 초기화되지 않아 "
                       "GPU 커널 단위 시간은 수집되지 않습니다. "
                       "연산(op) 단위 실측은 정상 수집됩니다."),
    }


@app.post("/api/v1/metrics/llm/profile")
def llm_profile_run(samples: int = 3, max_tokens: int = 48,
                    service: Optional[str] = None, mode: str = "manual"):
    """추론을 실제로 실행하면서 연산별 소요시간을 측정한다.
    수동(버튼) 3회는 약 30~60초, 자동 샘플링(1회)은 그보다 짧다."""
    st = _pstate(service)
    if not _profile_lock.acquire(blocking=False):
        raise HTTPException(409, "이미 프로파일링이 진행 중입니다")
    st.update(running=True, error=None)
    try:
        port = _llm_endpoint(service)
        base = f"http://127.0.0.1:{port}"
        r = httpx.post(f"{base}/start_profile", timeout=30)
        if r.status_code == 404:
            raise HTTPException(
                503, "vLLM에 프로파일러가 켜져 있지 않습니다 "
                     "(VLLM_TORCH_PROFILER_DIR 환경변수 필요)")
        r.raise_for_status()

        for _ in range(max(1, min(samples, 10))):
            httpx.post(f"{base}/v1/completions", timeout=120, json={
                "model": _llm_model_name(port),
                "prompt": "Explain GPU scheduling in one sentence.",
                "max_tokens": max_tokens,
            })
        httpx.post(f"{base}/stop_profile", timeout=300)

        ns, pod = _vllm_pod(service)
        parsed, last = None, ""
        for _try in range(6):          # 트레이스 파일 기록 완료까지 대기
            time.sleep(4)
            out = subprocess.run(
                ["kubectl", "-n", ns, "exec", "-i", pod, "--",
                 "python3", "-c", _PROFILE_PARSE],
                capture_output=True, text=True, timeout=180, env=_kenv())
            last = (out.stdout or out.stderr).strip()
            line = next((ln for ln in last.splitlines()
                         if ln.startswith("{")), "")
            if line:
                parsed = json.loads(line)
                if not parsed.get("error"):
                    break
        if not parsed or parsed.get("error"):
            raise HTTPException(
                503, f"프로파일 결과 분석 실패: "
                     f"{(parsed or {}).get('error') or last[:200]}")

        # 구성요소별로 합산
        agg: dict[str, dict] = {}
        for o in parsed["ops"]:
            label, hint = _label_op(o["op"])
            e = agg.setdefault(label, {"component": label, "reason": hint,
                                       "us": 0.0, "calls": 0, "ops": []})
            e["us"] += o["us"]
            e["calls"] += o["calls"]
            if o["op"] not in e["ops"]:
                e["ops"].append(o["op"])
        total = sum(e["us"] for e in agg.values()) or 1.0
        rows = sorted(agg.values(), key=lambda e: -e["us"])[:8]
        result = {
            "samples": samples,
            "maxTokens": max_tokens,
            "totalMs": round(total / 1000, 1),
            "events": parsed["events"],
            "hotspots": [{
                "component": e["component"],
                "ms": round(e["us"] / 1000, 1),
                "pct": round(e["us"] / total * 100, 1),
                "calls": e["calls"],
                "reason": e["reason"],
                "ops": e["ops"][:4],
            } for e in rows],
        }
        st.update(result=result, mode=mode,
                  at=datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
        return result
    except HTTPException as e:
        st["error"] = e.detail
        raise
    except Exception as e:
        st["error"] = str(e)
        raise HTTPException(503, f"프로파일링 실패: {e}")
    finally:
        st["_ts"] = time.time()   # 성공·실패 모두 다음 자동 샘플링 기준 시각
        st["running"] = False
        _profile_lock.release()


class LlmChat(BaseModel):
    prompt: str
    maxTokens: int = 128


_llm_served_names: dict[int, str] = {}


def _llm_model_name(port: int) -> str:
    """서버에 실제 등록된 모델 이름.

    KServe로 배포하면 서비스 이름으로 등록되고, 직접 띄우면 HuggingFace
    모델 ID로 등록된다. 어느 쪽이든 맞게 호출하려면 물어봐야 한다.
    """
    if port in _llm_served_names:
        return _llm_served_names[port]
    try:
        data = httpx.get(f"http://127.0.0.1:{port}/v1/models", timeout=10).json()
        ids = [m.get("id") for m in data.get("data", []) if m.get("id")]
        _llm_served_names[port] = ids[0] if ids else LLM_MODEL
    except Exception:
        _llm_served_names[port] = LLM_MODEL
    return _llm_served_names[port]


_svc_forwards: dict[str, tuple[subprocess.Popen, int]] = {}
_next_llm_port = 18100


_svc_forward_lock = threading.Lock()


def _ensure_svc_forward(ns: str, svc: str, target_port: int = 80,
                        spawn: bool = True) -> Optional[int]:
    """특정 서비스로 가는 통로를 연다 (LLM이 여러 개일 때 각각 필요).

    spawn=False면 이미 열려 있을 때만 포트를 돌려주고 아니면 None을 준다.
    목록 조회처럼 화면이 주기적으로 부르는 경로에서 쓴다 — 통로를 새로 여는
    데 최대 6초가 걸려 그동안 처리 스레드를 붙잡기 때문이다.
    """
    global _next_llm_port
    import time as _t
    # PaaS 모드는 클러스터마다 같은 서비스 이름이 있을 수 있어 클러스터 ID 를 붙인다
    key = f"{_cid()}/{ns}/{svc}:{target_port}" if MULTI else f"{ns}/{svc}:{target_port}"
    ent = _svc_forwards.get(key)
    if ent and ent[0].poll() is None and _port_open(ent[1]):
        return ent[1]
    if not spawn:
        return None
    # 동시에 여러 요청이 같은 통로를 열려 하면 포트만 축내고 서로 죽인다
    with _svc_forward_lock:
        ent = _svc_forwards.get(key)
        if ent and ent[0].poll() is None and _port_open(ent[1]):
            return ent[1]
        _next_llm_port += 1
        port = _next_llm_port
        proc = subprocess.Popen(
            ["kubectl", "-n", ns, "port-forward", f"svc/{svc}",
             f"{port}:{target_port}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=_kenv())
        for _ in range(12):
            _t.sleep(0.5)
            if _port_open(port):
                _svc_forwards[key] = (proc, port)
                return port
        proc.terminate()
    raise HTTPException(503, f"{svc} 연결에 실패했습니다")


def _llm_endpoint(service: Optional[str]) -> int:
    """service를 지정하면 그 LLM으로, 없으면 기본 LLM으로 연결한다."""
    if not service:
        return _ensure_vllm_forward()
    s = _find_isvc(service)              # PaaS 모드는 서비스가 있는 클러스터를 찾아 그쪽으로 연다
    if s is not None:
        return _ensure_svc_forward(s["metadata"]["namespace"],
                                   f"{service}-predictor")
    raise HTTPException(404, f"서비스 {service}을(를) 찾을 수 없습니다")


@app.post("/api/v1/metrics/llm/chat")
def llm_chat(body: LlmChat, service: Optional[str] = None):
    """포털에서 LLM 실호출 테스트 (트래픽 생성 겸용)."""
    port = _llm_endpoint(service)
    import time as _t
    t0 = _t.perf_counter()
    r = httpx.post(f"http://127.0.0.1:{port}/v1/chat/completions", json={
        "model": _llm_model_name(port),
        "messages": [{"role": "user", "content": body.prompt}],
        "max_tokens": body.maxTokens,
    }, timeout=60)
    if r.status_code != 200:
        raise HTTPException(r.status_code, r.text[:300])
    d = r.json()
    return {
        "content": d["choices"][0]["message"]["content"],
        "usage": d.get("usage", {}),
        "latencyMs": round((_t.perf_counter() - t0) * 1000, 1),
    }


# ══════════════════════════════════════════════════════════════════
# GPU Metrics — gpu-metrics DaemonSet(kube-system) 경유 nvidia-smi 실측
# ══════════════════════════════════════════════════════════════════
from kubernetes.stream import stream as k8s_stream

_SMI_Q = ("nvidia-smi --query-gpu=name,utilization.gpu,memory.used,"
          "memory.total,temperature.gpu,power.draw --format=csv,noheader")


@app.get("/api/v1/resources/gpu-availability")
def gpu_availability():
    """클러스터 GPU 총량·사용량·여유 — 노트북 생성 폼에서 선택 가능 수량 안내.

    중요: 하나의 워크로드는 노드 한 대 안에서만 돈다. 클러스터에 2장이
    남아 있어도 각각 다른 노드에 있으면 'GPU 2장'짜리는 영영 뜨지 못한다.
    그래서 노드별 여유(perNode)와 한 번에 요청 가능한 최대치(maxPerPod)를
    함께 준다.
    """
    per_node: dict[str, dict] = {}
    total = 0
    for n in core.list_node().items:
        alloc = (n.status.allocatable or {}).get(ACCEL_RESOURCE)
        cnt = int(alloc) if alloc else 0
        total += cnt
        if cnt:
            per_node[n.metadata.name] = {"node": n.metadata.name,
                                         "total": cnt, "used": 0, "free": cnt}
    used = 0
    holders = []
    for p in core.list_pod_for_all_namespaces(
            field_selector="status.phase=Running").items:
        for c in p.spec.containers:
            lim = ((c.resources.limits or {}).get(ACCEL_RESOURCE)
                   or (c.resources.requests or {}).get(ACCEL_RESOURCE))
            if lim:
                n = int(lim)
                used += n
                holders.append({"name": p.metadata.name,
                                "namespace": p.metadata.namespace,
                                "node": p.spec.node_name, "count": n})
                ent = per_node.get(p.spec.node_name or "")
                if ent:
                    ent["used"] += n
                    ent["free"] = max(0, ent["total"] - ent["used"])
    nodes = sorted(per_node.values(), key=lambda x: x["node"])
    return {"total": total, "used": used, "available": max(0, total - used),
            "perNode": nodes,
            # 한 워크로드가 요청할 수 있는 최대 장수
            "maxPerPod": max([n["free"] for n in nodes], default=0),
            # 장비 한 대가 가진 최대 장수 (전부 비었을 때의 상한)
            "maxPerNode": max([n["total"] for n in nodes], default=0),
            "holders": holders}


@app.get("/api/v1/metrics/gpu")
def gpu_metrics():
    pods = core.list_namespaced_pod("kube-system",
                                    label_selector="app=gpu-metrics").items
    out = []
    for p in pods:
        if p.status.phase != "Running":
            continue
        try:
            raw = k8s_stream(_exec_api().connect_get_namespaced_pod_exec,
                             p.metadata.name, "kube-system",
                             command=["sh", "-c", _SMI_Q],
                             stderr=True, stdin=False, stdout=True, tty=False,
                             _request_timeout=10)
            parts = [x.strip() for x in raw.strip().split(",")]
            if len(parts) >= 6:
                out.append({
                    "node": p.spec.node_name,
                    "gpu": parts[0],
                    "utilPct": int(parts[1].rstrip(" %")),
                    "memUsedMb": int(parts[2].split()[0]),
                    "memTotalMb": int(parts[3].split()[0]),
                    "tempC": int(parts[4]),
                    "powerW": float(parts[5].split()[0]),
                })
        except Exception:
            out.append({"node": p.spec.node_name, "gpu": "unavailable"})
    return {"items": sorted(out, key=lambda x: x["node"])}


# ══════════════════════════════════════════════════════════════════
# HW Benchmark — GPU 실측 (matmul TFLOPS · 메모리 대역폭), 노드별 Job
#   GPU를 스케줄상 점유하지 않도록 runtimeClass nvidia + VISIBLE_DEVICES=all
# ══════════════════════════════════════════════════════════════════
BENCH_NS = "default"
BENCH_IMAGE = BENCH_IMAGE_ENV

_BENCH_PY = """
import torch, json, time
assert torch.cuda.is_available(), "no cuda"
dev = torch.device("cuda")
name = torch.cuda.get_device_name(0)

def matmul_tflops(dtype, n=8192, iters=30):
    a = torch.randn(n, n, dtype=dtype, device=dev)
    b = torch.randn(n, n, dtype=dtype, device=dev)
    for _ in range(3):
        torch.matmul(a, b)
    torch.cuda.synchronize()
    t0 = time.perf_counter()
    for _ in range(iters):
        torch.matmul(a, b)
    torch.cuda.synchronize()
    sec = (time.perf_counter() - t0) / iters
    return round(2 * n ** 3 / sec / 1e12, 2)

def mem_bw_gbs(mb=1024, iters=50):
    x = torch.randn(mb * 1024 * 1024 // 4, device=dev)
    y = torch.empty_like(x)
    torch.cuda.synchronize()
    t0 = time.perf_counter()
    for _ in range(iters):
        y.copy_(x)
    torch.cuda.synchronize()
    sec = (time.perf_counter() - t0) / iters
    return round(2 * x.numel() * 4 / sec / 1e9, 1)

result = {
    "gpu": name,
    "fp32Tflops": matmul_tflops(torch.float32),
    "fp16Tflops": matmul_tflops(torch.float16),
    "memBwGbs": mem_bw_gbs(),
    "vramGb": round(torch.cuda.get_device_properties(0).total_memory / 1e9, 1),
}
print("RESULT " + json.dumps(result), flush=True)
"""


def _ensure_bench_cm():
    cm = client.V1ConfigMap(metadata=client.V1ObjectMeta(name="gpu-bench-script"),
                            data={"bench.py": _BENCH_PY})
    try:
        core.create_namespaced_config_map(BENCH_NS, cm)
    except client.exceptions.ApiException as e:
        if e.status == 409:
            core.replace_namespaced_config_map("gpu-bench-script", BENCH_NS, cm)
        else:
            raise


@app.post("/api/v1/benchmarks/run", status_code=201)
def run_benchmarks():
    """양쪽 GPU 노드에 벤치마크 Job 생성 (기존 Job은 교체)."""
    _ensure_bench_cm()
    batch = client.BatchV1Api(_cur().api)
    nodes = [n.metadata.name for n in core.list_node().items]
    started = []
    for node in nodes:
        job_name = f"gpu-bench-{node}"
        try:
            batch.delete_namespaced_job(job_name, BENCH_NS,
                                        propagation_policy="Background")
            import time as _t
            _t.sleep(2)
        except client.exceptions.ApiException:
            pass
        job = {
            "apiVersion": "batch/v1",
            "kind": "Job",
            "metadata": {"name": job_name,
                         "labels": {"app": "gpu-bench", "bench-node": node}},
            "spec": {"backoffLimit": 1, "ttlSecondsAfterFinished": 604800,
                     "template": {"metadata": {"labels": {"app": "gpu-bench",
                                                          "bench-node": node}},
                                  "spec": {
                "nodeSelector": {"kubernetes.io/hostname": node},
                "runtimeClassName": "nvidia",
                "restartPolicy": "Never",
                "containers": [{
                    "name": "bench",
                    "image": BENCH_IMAGE,
                    "command": ["python3", "/bench/bench.py"],
                    "env": [
                        {"name": "NVIDIA_VISIBLE_DEVICES", "value": "all"},
                        {"name": "NVIDIA_DRIVER_CAPABILITIES",
                         "value": "compute,utility"},
                    ],
                    "volumeMounts": [{"name": "script", "mountPath": "/bench"}],
                    "resources": {"requests": {"cpu": "1", "memory": "2Gi"},
                                  "limits": {"cpu": "4", "memory": "8Gi"}},
                }],
                "volumes": [{"name": "script",
                             "configMap": {"name": "gpu-bench-script"}}],
            }}},
        }
        batch.create_namespaced_job(BENCH_NS, job)
        started.append(node)
    return {"started": started}


@app.get("/api/v1/benchmarks")
def list_benchmarks():
    import json as _json
    pods = core.list_namespaced_pod(BENCH_NS, label_selector="app=gpu-bench").items
    items = []
    for p in pods:
        node = p.metadata.labels.get("bench-node", p.spec.node_name)
        entry = {"node": node, "status": p.status.phase, "gpu": "-",
                 "fp32Tflops": None, "fp16Tflops": None,
                 "memBwGbs": None, "vramGb": None}
        if p.status.phase == "Succeeded":
            try:
                log = str(core.read_namespaced_pod_log(p.metadata.name, BENCH_NS))
                m = _re.search(r'RESULT (\{.*?\})', log)
                if m:
                    entry.update(_json.loads(m.group(1)))
                    entry["status"] = "Done"
            except Exception:
                pass
        elif p.status.phase == "Failed":
            entry["status"] = "Failed"
        else:
            entry["status"] = "Running"
        items.append(entry)
    return {"items": sorted(items, key=lambda x: x["node"])}


# ══════════════════════════════════════════════════════════════════
# Notebook reverse proxy — 모든 IDE 트래픽을 백엔드(8000) 경유로 통일
#   /notebook/{ns}/{name}/... → 노트북별 port-forward 업스트림
#   Jupyter는 base_url(NB_PREFIX) 경로 유지, code-server는 prefix 제거
#   (실서비스에서는 Istio VirtualService가 담당하는 역할)
# ══════════════════════════════════════════════════════════════════
from fastapi.responses import Response, StreamingResponse
from starlette.websockets import WebSocketDisconnect
import websockets as ws_client

_HOP_HEADERS = {"connection", "keep-alive", "transfer-encoding", "upgrade",
                "proxy-authenticate", "proxy-authorization", "te", "trailers", "host"}


def _upstream_for(ns: str, name: str, path: str) -> str:
    nb = _get_nb(name, ns)               # 먼저 찾아야 노트북이 있는 클러스터로 통로를 연다(PaaS 모드)
    port = _ensure_forward(ns, name)
    image = nb["spec"]["template"]["spec"]["containers"][0]["image"]
    if "codeserver" in image:
        return f"http://127.0.0.1:{port}/{path}"
    return f"http://127.0.0.1:{port}/notebook/{ns}/{name}/{path}"


PORTAL_URL = _os.environ.get("PORTAL_URL", CORS_ORIGINS[0])

_AUTH_DENIED_HTML = """<!doctype html><html lang="ko"><head><meta charset="utf-8">
<title>로그인이 필요합니다</title></head>
<body style="margin:0;display:flex;height:100vh;align-items:center;justify-content:center;
font-family:'Segoe UI','Malgun Gothic',sans-serif;background:#f7fafb;color:#223336">
<div style="text-align:center;max-width:420px;padding:36px 32px;background:#fff;
border:1px solid #e3ecec;border-radius:14px;box-shadow:0 2px 12px rgba(0,0,0,.05)">
<div style="font-size:38px;line-height:1;margin-bottom:14px">🔒</div>
<h2 style="margin:0 0 8px;font-size:1.15rem">로그인이 필요합니다</h2>
<p style="margin:0 0 6px;font-size:.9rem;color:#4b5f63">
개발환경에 접속하려면 openKcloud 포털에 로그인해 주세요.</p>
<p style="margin:0 0 20px;font-size:.8rem;color:#829699">
로그인 세션이 만료되었거나 아직 로그인하지 않았습니다.</p>
<a href="{portal}" target="_blank" rel="noopener" style="display:inline-block;background:#0d9488;color:#fff;
text-decoration:none;padding:10px 22px;border-radius:8px;font-size:.9rem;font-weight:600">
새 창에서 로그인하기</a>
<p id="hint" style="margin:16px 0 0;font-size:.75rem;color:#829699">
로그인이 완료되면 이 화면이 <b>자동으로</b> 열립니다.</p>
</div>
<script>
// 로그인 완료를 감지해 자동으로 진입 (사용자가 새로고침할 필요 없음)
var tries = 0;
setInterval(function () {
  tries++;
  fetch('{me}', { credentials: 'include' })
    .then(function (r) { if (r.ok) location.reload(); })
    .catch(function () {});
  if (tries === 20) {
    document.getElementById('hint').innerHTML =
      '로그인했는데도 열리지 않으면 이 창을 새로고침(F5)해 주세요.';
  }
}, 3000);
</script>
</body></html>"""


@app.api_route("/notebook/{ns}/{name}/{path:path}",
               methods=["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"])
async def notebook_proxy(ns: str, name: str, path: str, request: Request):
    if not _verify_token(request.cookies.get(SESSION_COOKIE)):
        # 포털로 돌아갈 수 있도록 로그인 경로 안내 (iframe 안에서도 최상위로 이동)
        # 접속한 호스트 기준으로 포털 주소를 만든다
        # (localhost로 들어왔는데 LAN IP로 안내하면 세션이 갈라져 루프가 생김)
        # 포털과 같은 주소(게이트웨이 single-origin)면 PORTAL_URL 을 비워 같은 호스트의 /login 으로 보낸다
        host = (request.headers.get("host") or "").split(":")[0]
        portal = ("" if not PORTAL_URL
                  else f"{request.url.scheme}://{host}:{PORTAL_URL.rsplit(':', 1)[-1]}" if host
                  else PORTAL_URL)
        html = (_AUTH_DENIED_HTML.replace("{portal}", f"{portal}/login")
                .replace("{me}", f"{API_PREFIX or '/api/v1'}/auth/me"))   # 게이트웨이 밖에서 보이는 경로
        return Response(content=html, status_code=401, media_type="text/html")
    url = _upstream_for(ns, name, path)
    headers = {k: v for k, v in request.headers.items()
               if k.lower() not in _HOP_HEADERS}
    body = await request.body()
    async with httpx.AsyncClient(timeout=60, follow_redirects=False) as cli:
        up = await cli.request(request.method, url, params=request.query_params,
                               headers=headers, content=body)
    # CMP 포털(3000)이 iframe으로 감싸므로 프레임 차단 헤더는 제거한다
    # (Jupyter Server 기본값: CSP frame-ancestors 'self' → cross-origin iframe 차단)
    # set-cookie는 dict에 담으면 여러 개가 쉼표로 합쳐져 브라우저가
    # 첫 쿠키만 인식한다(_xsrf 유실 → "'_xsrf' argument missing from POST").
    # 개별 헤더로 다시 append한다.
    drop = _HOP_HEADERS | {"content-length", "content-encoding", "set-cookie",
                           "x-frame-options", "content-security-policy"}
    resp_headers = {k: v for k, v in up.headers.items() if k.lower() not in drop}
    # 업스트림 상대 리다이렉트(./?folder=...)는 그대로 두면 브라우저가 알아서 따라감
    resp = Response(content=up.content, status_code=up.status_code,
                    headers=resp_headers)
    for cookie in up.headers.get_list("set-cookie"):
        resp.headers.append("set-cookie", cookie)
    return resp


@app.websocket("/notebook/{ns}/{name}/{path:path}")
async def notebook_ws_proxy(ns: str, name: str, path: str, websocket: WebSocket):
    import asyncio
    if not _verify_token(websocket.cookies.get(SESSION_COOKIE)):
        await websocket.close(code=4401)
        return
    url = _upstream_for(ns, name, path).replace("http://", "ws://")
    if websocket.url.query:
        url += f"?{websocket.url.query}"
    subprotocols = websocket.headers.get("sec-websocket-protocol")
    kwargs = {"subprotocols": [s.strip() for s in subprotocols.split(",")]} if subprotocols else {}
    await websocket.accept(subprotocol=None)
    try:
        async with ws_client.connect(url, max_size=None, **kwargs) as upstream:
            async def client_to_up():
                while True:
                    msg = await websocket.receive()
                    if msg.get("type") == "websocket.disconnect":
                        break
                    if "text" in msg and msg["text"] is not None:
                        await upstream.send(msg["text"])
                    elif "bytes" in msg and msg["bytes"] is not None:
                        await upstream.send(msg["bytes"])

            async def up_to_client():
                async for m in upstream:
                    if isinstance(m, str):
                        await websocket.send_text(m)
                    else:
                        await websocket.send_bytes(m)

            done, pending = await asyncio.wait(
                [asyncio.create_task(client_to_up()),
                 asyncio.create_task(up_to_client())],
                return_when=asyncio.FIRST_COMPLETED)
            for t in pending:
                t.cancel()
    except (WebSocketDisconnect, Exception):
        pass
    finally:
        try:
            await websocket.close()
        except Exception:
            pass


# ── Auth endpoints ─────────────────────────────────────────────────
@app.post("/api/v1/auth/login")
def auth_login(request: Request):
    """Keycloak 액세스 토큰(Authorization: Bearer)으로 MNT-CORE /auth/me 확인 → IDE 프록시용 세션 쿠키 발급.
    (Keycloak 전환으로 패스워드 로그인 제거 — 신원·역할은 MNT-CORE 가 토큰으로 판정한다.)"""
    authz = request.headers.get("authorization", "")
    if not authz.lower().startswith("bearer "):
        raise HTTPException(401, "로그인 토큰(Authorization: Bearer)이 필요합니다")
    try:
        r = httpx.get(f"{CORE_API_BASE}/api/core/auth/me", timeout=10,
                      headers={"Authorization": authz})
    except httpx.HTTPError as e:
        raise HTTPException(502, f"포털 사용자 확인(MNT-CORE) 실패: {e}")
    if r.status_code != 200:
        raise HTTPException(401 if r.status_code < 500 else 502, "유효하지 않은 로그인 정보")
    user = r.json()  # {id, username, name, email, org, role, projectIds}
    token = _issue_token(user["username"].lower(), user["role"])
    try:
        ip = (request.headers.get("cf-connecting-ip")
              or request.headers.get("x-forwarded-for", "").split(",")[0].strip()
              or (request.client.host if request.client else "-"))
        if _ACCESS_LOG:
            with open(_ACCESS_LOG, "a") as f:
                f.write(f"{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}  {ip}  LOGIN"
                        f"  user={user['username'].lower()}\n")
    except Exception:
        pass
    if MULTI:   # 이 사용자가 쓸 수 있는 서비스 클러스터의 접속 정보를 받아 둔다 — 사용자 없이 도는 작업(워크스페이스 생성·IDE 연결 유지)용
        threading.Thread(target=_sync_quiet, args=(authz, user.get("role") == "admin"),
                         daemon=True, name="cluster-sync").start()
    resp = Response(content=json.dumps({"username": user["username"], "role": user["role"]}),
                    media_type="application/json")
    resp.set_cookie(SESSION_COOKIE, token, max_age=SESSION_TTL,
                    httponly=True, samesite="lax", path="/")
    return resp


@app.post("/api/v1/auth/logout")
def auth_logout():
    resp = Response(content='{"ok":true}', media_type="application/json")
    resp.delete_cookie(SESSION_COOKIE, path="/")
    return resp


@app.get("/api/v1/auth/me")
def auth_me(request: Request):
    payload = _verify_token(request.cookies.get(SESSION_COOKIE))
    if not payload:
        raise HTTPException(401, "unauthenticated")
    # 프로젝트 소속은 포털(MNT-CORE)이 관리한다
    return {"username": payload["sub"], "role": payload["role"]}


@app.get("/healthz")
def healthz():
    return {"ok": True}


# ── Temporal 워커 — AI 워크스페이스 프로비저닝 작업(큐 okc-ainb). 주소가 없으면 시작하지 않는다 ──
TEMPORAL_API_BASE = _os.environ.get("TEMPORAL_API_BASE", "")
_temporal_worker = None


@app.on_event("startup")
async def _start_temporal_worker():
    global _temporal_worker
    if TEMPORAL_API_BASE:
        import asyncio
        from .workflows.runtime import run_worker
        _temporal_worker = asyncio.create_task(run_worker(TEMPORAL_API_BASE))


@app.on_event("shutdown")
async def _stop_temporal_worker():
    if _temporal_worker:
        _temporal_worker.cancel()
