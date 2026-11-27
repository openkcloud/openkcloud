"""
KCloud Recovery Policies — 장애 후속 조치 정책 설정, 관리.

장애 유형별로 어떤 절차를 권고할지 적어 둔 정책을 저장하고, 장애에 맞는 정책을 고른다.
정책은 권고 전용(recommendation-only)이다. 이 서비스는 외부 시스템을 바꾸지 않고, 절차의 실행은 관리자가 직접 한다.
DB 가 없으면 초기 정책(SEED_POLICIES)을 읽기 전용으로 쓴다.
ponytail: 절차(steps)는 정책 안에 둔다. 여러 정책이 같은 절차를 돌려 쓰게 되면 절차 템플릿을 따로 뺀다.
"""
from __future__ import annotations

import json
import logging
import uuid
from typing import Optional

from app.config import settings
from app.services.faults import FAULT_TYPES

logger = logging.getLogger(__name__)

MODE = "recommendation-only"
ACTION_TYPES: dict[str, str] = {
    "manual_review": "확인만 하는 단계",
    "manual_action": "관리자가 직접 실행하는 조치. 위험도(risk_level) 필수",
    "record_feedback": "조치 결과 기록",
}
RISK_LEVELS = ("low", "medium", "high")

# 참고 조회식의 자리표시자. 복구 정보를 만들 때 장애 대상 값으로 채운다
PLACEHOLDERS = ("fault_id", "cluster", "node", "namespace", "pod", "container", "accelerator_id", "vm_id")
_FAULT = "GET /api/v2/faults/{fault_id}"
_NODE_LOGS = "GET /api/v2/logs/clusters/{cluster}/nodes/{node}/logs"
_POD_LOGS = "GET /api/v2/logs/clusters/{cluster}/pods/{namespace}/{pod}/logs"
_POD_TRACE = "GET /api/v2/resource-map/pods/{pod}?cluster={cluster}&namespace={namespace}"
_ACCEL = "GET /api/v2/clusters/{cluster}/accelerators/{accelerator_id}"


def _step(name: str, action_type: str, guidance: str, queries: tuple[str, ...] = (), risk: Optional[str] = None) -> dict:
    return {"name": name, "action_type": action_type, "guidance": guidance,
            "reference_queries": list(queries), "risk_level": risk}


_RECORD = _step("결과 기록", "record_feedback", "조치 내용과 재발 여부를 운영 기록에 남긴다. 10분 뒤 장애가 해제됐는지 확인한다",
                ("GET /api/v2/faults/active",))

# 초기 정책 (DB 첫 기동 때 시드, DB 가 없으면 이 값을 읽기 전용으로 씀). 포탈이나 API 로 고쳐 쓴다
SEED_POLICIES: list[dict] = [
    dict(policy_id="policy-accel-driver-error", name="GPU 오류 후속 조치", fault_type="accelerator_error",
         vendor_scope=["nvidia"], description="XID, 상태 이상, 과열, 수집 중단이 난 GPU 의 확인과 격리 절차",
         steps=[
             _step("증거 확인", "manual_review", "XID 코드와 추정 원인, 장애 구간의 온도와 전력을 확인한다", (_FAULT, _NODE_LOGS)),
             _step("영향 워크로드 확인", "manual_review", "그 카드를 쓰는 Pod 와 워크로드를 확인하고 담당자에게 알린다", (_FAULT,)),
             _step("노드 스케줄 중지", "manual_action",
                   "하드웨어 원인 후보(XID 48, 63, 64, 74, 79, 92, 94, 95)면 노드에 새 Pod 가 배치되지 않게 한다 (kubectl cordon)",
                   risk="medium"),
             _step("GPU 초기화 또는 교체 요청", "manual_action",
                   "워크로드를 옮긴 뒤 드라이버를 다시 올리거나 노드를 재부팅한다. 같은 코드가 재발하면 카드 교체를 요청한다",
                   risk="high"),
             _RECORD]),
    dict(policy_id="policy-npu-fault", name="NPU 오류 후속 조치", fault_type="npu_fault",
         vendor_scope=["furiosa", "rebellions"], description="상태 이상, 과열, 수집 중단이 난 NPU 의 확인 절차",
         steps=[
             _step("증거 확인", "manual_review", "장애 구간의 온도, 전력, 사용률과 드라이버 로그를 확인한다", (_FAULT, _NODE_LOGS)),
             _step("장치 상태 확인", "manual_review",
                   "가속기 상세 API 로 상태, 온도, 쓰로틀을 보고, 노드에서 벤더 도구(furiosa-smi, rbln-stat)로 카드가 보이는지 확인한다",
                   (_ACCEL,)),
             _step("서빙 재시작", "manual_action", "카드는 정상인데 서빙만 멈췄으면 그 카드를 쓰는 서빙 프로세스를 다시 시작한다",
                   risk="medium"),
             _step("노드 재부팅 또는 교체 요청", "manual_action", "카드가 보이지 않으면 워크로드를 옮기고 노드를 재부팅한다. 재발하면 교체를 요청한다",
                   risk="high"),
             _RECORD]),
    dict(policy_id="policy-workload-oom", name="메모리 부족 종료 후속 조치", fault_type="workload_oom",
         description="OOM 으로 종료된 워크로드의 원인 확인과 한도 조정 절차",
         steps=[
             _step("종료 원인 확인", "manual_review", "커널 OOM 인지 CUDA OOM 인지, 메모리 사용량이 한도에 닿았는지 확인한다",
                   (_FAULT, _POD_LOGS)),
             _step("배치 위치 확인", "manual_review", "같은 노드나 같은 GPU 를 쓰는 다른 워크로드가 있는지 확인한다", (_POD_TRACE,)),
             _step("메모리 한도 또는 배치 크기 조정", "manual_action",
                   "컨테이너 메모리 한도(limits.memory)를 올리거나, CUDA OOM 이면 배치 크기와 동시 요청 수를 줄인다", risk="low"),
             _RECORD]),
    dict(policy_id="policy-workload-crash-loop", name="재시작 반복 후속 조치", fault_type="workload_crash_loop",
         description="시작 후 반복해서 종료되는 컨테이너의 원인 확인 절차",
         steps=[
             _step("종료 로그 확인", "manual_review", "직전 실행의 마지막 로그와 종료 사유를 확인한다 (kubectl logs --previous)",
                   (_FAULT, _POD_LOGS)),
             _step("설정과 의존 자원 확인", "manual_review", "이미지 태그, 환경 변수, 볼륨, 가속기 할당이 맞는지 확인한다", (_POD_TRACE,)),
             _step("수정 후 재배포", "manual_action", "원인을 고친 뒤 워크로드를 다시 배포한다. 원인을 모르면 복제 수를 0 으로 내려 반복을 멈춘다",
                   risk="medium"),
             _RECORD]),
    dict(policy_id="policy-node-notready", name="노드 NotReady 후속 조치", fault_type="node_not_ready",
         description="NotReady 노드의 원인 확인과 워크로드 이동 절차",
         steps=[
             _step("노드 상태 확인", "manual_review", "자원 압박 조건(메모리, 디스크, PID)과 kubelet 로그를 확인한다", (_FAULT, _NODE_LOGS)),
             _step("영향 워크로드 확인", "manual_review", "노드에 있던 Pod 와 워크로드, 그 노드가 올라간 VM 과 물리서버를 확인한다", (_FAULT,)),
             _step("kubelet 재시작", "manual_action", "노드에 접속되면 kubelet 과 컨테이너 런타임을 다시 시작한다", risk="medium"),
             _step("워크로드 이동", "manual_action", "복구가 길어지면 노드를 비워(kubectl drain) 워크로드를 다른 노드로 옮긴다",
                   risk="high"),
             _RECORD]),
    dict(policy_id="policy-vm-down", name="VM 비정상 후속 조치", fault_type="vm_down",
         description="ERROR 상태 VM 의 원인 확인과 복구 절차",
         steps=[
             _step("VM 과 하이퍼바이저 확인", "manual_review", "VM 상태와 올라간 물리서버, 같은 서버의 다른 VM 상태를 확인한다",
                   (_FAULT, "GET /api/v2/openstack/vms/{vm_id}")),
             _step("VM 재시작", "manual_action", "하이퍼바이저가 정상이면 VM 을 강제 재시작한다 (openstack server reboot --hard)",
                   risk="medium"),
             _step("VM 대피", "manual_action", "하이퍼바이저 장애면 VM 을 다른 서버로 옮긴다 (openstack server evacuate)", risk="high"),
             _RECORD]),
    dict(policy_id="policy-hw-fault", name="서버 하드웨어 오류 후속 조치", fault_type="hardware_fault",
         description="커널 하드웨어 오류, 디스크 오류 로그가 난 서버의 확인 절차",
         steps=[
             _step("오류 로그 확인", "manual_review", "하드웨어 오류(MCE)인지 디스크 I/O 오류인지, 반복되는지 확인한다", (_FAULT, _NODE_LOGS)),
             _step("센서와 이벤트 로그 확인", "manual_review", "BMC 센서(전원, 팬, 온도)와 이벤트 로그(SEL)를 확인한다",
                   ("GET /api/v2/clusters/{cluster}/nodes/{node}/hardware/sensors",)),
             _step("서버 점검 요청", "manual_action", "오류가 반복되면 워크로드를 옮기고 서버 점검이나 부품 교체를 요청한다", risk="high"),
             _RECORD]),
]


def normalize(p: dict) -> dict:
    """저장, 응답용 모양으로 맞춘다. 단계 번호는 순서대로 다시 매기고 mode 는 항상 권고 전용."""
    steps = [{"step": i, "name": s["name"], "action_type": s["action_type"], "guidance": s.get("guidance", ""),
              "reference_queries": list(s.get("reference_queries") or []), "risk_level": s.get("risk_level")}
             for i, s in enumerate(p.get("steps") or [], 1)]
    return {"policy_id": p["policy_id"], "name": p["name"], "fault_type": p["fault_type"],
            "vendor_scope": list(p.get("vendor_scope") or []), "error_codes": list(p.get("error_codes") or []),
            "enabled": p.get("enabled", True), "mode": MODE, "description": p.get("description", ""),
            "review_required": p.get("review_required", True), "steps": steps,
            "created_at": p.get("created_at"), "updated_at": p.get("updated_at")}


def check_policy(p: dict) -> list[str]:
    """정책 규칙 위반 목록. 비어 있으면 통과.

    >>> ok = {"fault_type": "workload_oom", "steps": [{"name": "확인", "action_type": "manual_review"}]}
    >>> check_policy(ok)
    []
    >>> check_policy({**ok, "mode": "auto"})
    ['MODE_NOT_ALLOWED']
    >>> check_policy({"fault_type": "x", "steps": [{"name": "조치", "action_type": "manual_action"},
    ...                                            {"name": "조회", "action_type": "manual_review",
    ...                                             "reference_queries": ["GET /api/v2/x/{unknown}"]}]})
    ['UNKNOWN_FAULT_TYPE', 'RISK_LEVEL_REQUIRED', 'UNKNOWN_PLACEHOLDER']
    >>> check_policy({"fault_type": "workload_oom", "steps": []})
    ['STEPS_REQUIRED']
    """
    import string

    errors = []
    if p.get("mode", MODE) != MODE:  # 자동 실행 모드는 두지 않는다
        errors.append("MODE_NOT_ALLOWED")
    if p.get("fault_type") not in FAULT_TYPES:
        errors.append("UNKNOWN_FAULT_TYPE")
    steps = p.get("steps") or []
    if not steps:
        errors.append("STEPS_REQUIRED")
    if any(s.get("action_type") not in ACTION_TYPES for s in steps):
        errors.append("UNKNOWN_ACTION_TYPE")
    if any(s.get("action_type") == "manual_action" and s.get("risk_level") not in RISK_LEVELS for s in steps):
        errors.append("RISK_LEVEL_REQUIRED")
    try:
        names = {name for s in steps for q in s.get("reference_queries") or []
                 for _, name, _, _ in string.Formatter().parse(q) if name}
    except ValueError:  # 짝이 맞지 않는 중괄호
        names = {""}
    if names - set(PLACEHOLDERS):
        errors.append("UNKNOWN_PLACEHOLDER")
    return errors


def match_policy(fault: dict, policies: list[dict]) -> Optional[dict]:
    """장애에 맞는 켜진 정책. 여럿이면 오류 코드 지정, 벤더 지정 순으로 좁은 쪽, 같으면 ID 순.

    >>> pols = [normalize(p) for p in [
    ...     dict(policy_id="a", name="", fault_type="accelerator_error", steps=[]),
    ...     dict(policy_id="b", name="", fault_type="accelerator_error", vendor_scope=["nvidia"], steps=[]),
    ...     dict(policy_id="c", name="", fault_type="accelerator_error", error_codes=[79], steps=[]),
    ...     dict(policy_id="d", name="", fault_type="accelerator_error", error_codes=[48], enabled=False, steps=[])]]
    >>> f = lambda **kw: {"fault_type": "accelerator_error", "target": {"vendor": kw.get("vendor")}, "xid_code": kw.get("xid")}
    >>> [match_policy(x, pols)["policy_id"] for x in (f(vendor="nvidia", xid=79), f(vendor="nvidia", xid=48), f())]
    ['c', 'b', 'a']
    >>> match_policy({"fault_type": "vm_down", "target": {}}, pols) is None
    True
    """
    vendor, code = fault["target"].get("vendor"), fault.get("xid_code")
    hits = [p for p in policies
            if p["enabled"] and p["fault_type"] == fault["fault_type"]
            and (not p["vendor_scope"] or vendor in p["vendor_scope"])
            and (not p["error_codes"] or code in p["error_codes"])]
    return min(hits, key=lambda p: (not p["error_codes"], not p["vendor_scope"], p["policy_id"]), default=None)


# ---------------------------------------------------------------------------
# 저장소
# ---------------------------------------------------------------------------

SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS recovery_policies (
    policy_id     TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    fault_type    TEXT NOT NULL,
    vendor_scope  JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_codes   JSONB NOT NULL DEFAULT '[]'::jsonb,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    description   TEXT NOT NULL DEFAULT '',
    review_required BOOLEAN NOT NULL DEFAULT TRUE,
    steps         JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
"""
_JSON = ("vendor_scope", "error_codes", "steps")
_COLS = ("policy_id", "name", "fault_type", "vendor_scope", "error_codes", "enabled", "description",
         "review_required", "steps")


def _row(r) -> dict:
    d = dict(r)
    for k in _JSON:
        if isinstance(d.get(k), str):
            d[k] = json.loads(d[k])
    return normalize(d)


def _args(p: dict) -> list:
    return [json.dumps(p[k], ensure_ascii=False) if k in _JSON else p[k] for k in _COLS]


class RecoveryPolicyStore:
    def __init__(self) -> None:
        self.pool = None

    @property
    def ready(self) -> bool:
        return self.pool is not None

    async def connect(self) -> None:
        import asyncpg  # 선택 의존성. DATABASE_URL 미설정 환경에서는 import 자체를 피함

        pool = await asyncpg.create_pool(settings.DATABASE_URL, min_size=1, max_size=2)
        try:
            async with pool.acquire() as con, con.transaction():
                await con.execute(SCHEMA_SQL)
                # 표가 비어 있을 때만 시드. 여러 워커가 함께 떠도 같은 ID 는 건너뜀
                if await con.fetchval("SELECT count(*) FROM recovery_policies") == 0:
                    for p in SEED_POLICIES:
                        await self._insert(con, normalize(p), " ON CONFLICT (policy_id) DO NOTHING")
                    logger.info("recovery policies seeded: %d", len(SEED_POLICIES))
        except Exception:
            await pool.close()
            raise
        self.pool = pool

    async def close(self) -> None:
        if self.pool:
            await self.pool.close()
            self.pool = None

    @staticmethod
    async def _insert(con, p: dict, suffix: str = "") -> None:
        marks = ",".join(f"${i}::jsonb" if k in _JSON else f"${i}" for i, k in enumerate(_COLS, 1))
        await con.execute(f"INSERT INTO recovery_policies ({','.join(_COLS)}) VALUES ({marks}){suffix}", *_args(p))

    async def list(self) -> list[dict]:
        return [_row(r) for r in await self.pool.fetch("SELECT * FROM recovery_policies ORDER BY fault_type, policy_id")]

    async def get(self, pid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM recovery_policies WHERE policy_id=$1", pid)
        return _row(r) if r else None

    async def create(self, body: dict) -> dict:
        p = normalize({**body, "policy_id": f"policy-{uuid.uuid4().hex[:6]}"})
        async with self.pool.acquire() as con:
            await self._insert(con, p)
        return await self.get(p["policy_id"])

    async def replace(self, pid: str, body: dict) -> Optional[dict]:
        p = normalize({**body, "policy_id": pid})
        sets = ",".join(f"{k}=${i}::jsonb" if k in _JSON else f"{k}=${i}" for i, k in enumerate(_COLS[1:], 2))
        status = await self.pool.execute(
            f"UPDATE recovery_policies SET {sets}, updated_at=now() WHERE policy_id=$1", *_args(p))
        return await self.get(pid) if status.endswith("1") else None

    async def delete(self, pid: str) -> bool:
        return (await self.pool.execute("DELETE FROM recovery_policies WHERE policy_id=$1", pid)).endswith("1")


store = RecoveryPolicyStore()


async def load_policies() -> list[dict]:
    """정책 목록. DB 가 없으면 초기 정책."""
    return await store.list() if store.ready else [normalize(p) for p in SEED_POLICIES]
