"""
KCloud Recovery — 복구 프로세스와 관련 정보 생성.

장애(app/services/faults.py)에 후속 조치 정책(app/services/recovery_policies.py)을 대입해 복구 정보를 만든다:
조치 절차(참고 조회식의 자리를 장애 대상 값으로 채움), 추정 원인, 영향 범위, 조치 뒤 확인 방법.
저장하지 않고 조회할 때마다 만든다. 조치는 실행하지 않는다 (권고 전용).
ponytail: 검토 상태(수락, 거절)와 조치 결과는 저장하지 않는다. 필요해지면 recommendation_id 를 키로 표를 둔다.
"""
from __future__ import annotations

import string
from datetime import datetime, timezone
from typing import Optional

from app.services import faults
from app.services.recovery_policies import MODE, load_policies, match_policy

PRIORITY = {"critical": "urgent", "warning": "normal", "info": "info"}
VERIFY_WINDOW = "10m"  # 장애 증거를 발생 뒤 10분까지 모으는 것과 같은 길이

# 조치 뒤 무엇을 확인하면 복구로 볼지
VERIFY: dict[str, str] = {
    "accelerator_error": "XID 코드가 더 바뀌지 않고 가속기 상태, 온도가 정상 범위인지 확인",
    "npu_fault": "카드가 다시 보이고 사용률, 온도가 수집되는지 확인",
    "workload_oom": "컨테이너가 다시 OOMKilled 로 끝나지 않고 메모리 사용량이 한도 아래에 머무는지 확인",
    "workload_crash_loop": "재시작 횟수가 더 늘지 않는지 확인",
    "node_not_ready": "노드가 Ready 로 돌아오고 Pod 가 다시 배치되는지 확인",
    "vm_down": "VM 상태가 ACTIVE 이고 그 위 노드가 Ready 인지 확인",
    "hardware_fault": "같은 오류 로그가 다시 나오지 않는지 확인",
}


def recommendation_id(fault_id: str) -> str:
    """장애 하나에 복구 정보 하나라 ID 를 장애 ID 에서 만든다.

    >>> recommendation_id("flt-3f9a1c2b7d10"), fault_id_of("rec-3f9a1c2b7d10")
    ('rec-3f9a1c2b7d10', 'flt-3f9a1c2b7d10')
    """
    return "rec-" + fault_id.removeprefix("flt-")


def fault_id_of(rec_id: str) -> str:
    return "flt-" + rec_id.removeprefix("rec-")


def fill(query: str, fault: dict) -> Optional[str]:
    """참고 조회식의 자리를 장애 대상 값으로 채운다. 채울 값이 없는 자리가 있으면 None (깨진 경로를 내지 않음).

    >>> f = {"fault_id": "flt-1", "target": {"cluster": "l40s", "node": "n1"}}
    >>> fill("GET /api/v2/logs/clusters/{cluster}/nodes/{node}/logs", f)
    'GET /api/v2/logs/clusters/l40s/nodes/n1/logs'
    >>> fill("GET /api/v2/faults/{fault_id}", f), fill("GET /api/v2/resource-map/pods/{pod}", f)
    ('GET /api/v2/faults/flt-1', None)
    """
    values = {**fault["target"], "fault_id": fault["fault_id"]}
    names = [name for _, name, _, _ in string.Formatter().parse(query) if name]
    if any(not values.get(n) for n in names):
        return None
    return query.format(**{n: values[n] for n in names})


def summarize(fault: dict, policy: dict) -> dict:
    """목록용 요약. 증거 수집 없이 장애와 정책만으로 만든다."""
    return {"recommendation_id": recommendation_id(fault["fault_id"]), "fault_id": fault["fault_id"],
            "fault_type": fault["fault_type"], "fault_status": fault["status"], "severity": fault["severity"],
            "priority": PRIORITY.get(fault["severity"], "normal"), "summary": fault["summary"],
            "target": fault["target"], "detected_at": fault["detected_at"],
            "policy_id": policy["policy_id"], "policy_name": policy["name"], "mode": MODE,
            "review_required": policy["review_required"], "step_count": len(policy["steps"])}


def build_recommendation(fault: dict, policy: dict, evidence: dict, analysis: dict,
                         now: Optional[datetime] = None) -> dict:
    """장애, 정책, 증거, 분석으로 복구 정보를 조립. 순수 함수.

    >>> fault = {"fault_id": "flt-1", "fault_type": "workload_oom", "status": "active", "severity": "warning",
    ...          "summary": "ml/train-0 OOM", "detected_at": None,
    ...          "target": {"cluster": "l40s", "namespace": "ml", "pod": "train-0"}}
    >>> policy = {"policy_id": "p", "name": "OOM", "review_required": True, "steps": [
    ...     {"step": 1, "name": "확인", "action_type": "manual_review", "guidance": "g", "risk_level": None,
    ...      "reference_queries": ["GET /api/v2/faults/{fault_id}", "GET /x/{node}"]},
    ...     {"step": 2, "name": "조정", "action_type": "manual_action", "guidance": "g", "risk_level": "low",
    ...      "reference_queries": []}]}
    >>> ev = {"related_resources": {"scope": "pod", "path": [{"layer": "k8s_node", "name": "n1"}],
    ...       "affected_pod_count": 1, "affected_pods": [{"namespace": "ml", "pod": "train-0", "workload": "Job/train"}]},
    ...       "collection_status": {"resource_map": "ok"}}
    >>> an = {"probable_cause": "OOMKilled", "contributing_factors": [], "confidence": "high", "unverified": []}
    >>> r = build_recommendation(fault, policy, ev, an, datetime(2026, 11, 11, tzinfo=timezone.utc))
    >>> (r["recommendation_id"], r["priority"], r["mode"], r["probable_cause"])
    ('rec-1', 'normal', 'recommendation-only', 'OOMKilled')
    >>> [(a["step"], a["action_type"], a["risk_level"], a["reference_queries"]) for a in r["recommended_actions"]]
    [(1, 'manual_review', None, ['GET /api/v2/faults/flt-1']), (2, 'manual_action', 'low', [])]
    >>> (r["affected_scope"]["status"], r["affected_scope"]["pod_count"], r["affected_scope"]["workloads"])
    ('ok', 1, ['Job/train'])
    >>> r["verification"]["post_action_window"], r["verification"]["reference_queries"]
    ('10m', ['GET /api/v2/faults/flt-1', 'GET /api/v2/faults/active'])
    """
    related = evidence["related_resources"]
    pods = related.get("affected_pods")
    known = evidence["collection_status"].get("resource_map") == "ok"
    actions = [{**s, "reference_queries": [q for q in (fill(x, fault) for x in s["reference_queries"]) if q]}
               for s in policy["steps"]]
    return {
        **summarize(fault, policy),
        "probable_cause": analysis["probable_cause"],
        "contributing_factors": analysis["contributing_factors"],
        "confidence": analysis["confidence"],
        "recommended_actions": actions,
        # 범위를 정하지 못했으면 0 이 아니라 unknown
        "affected_scope": {
            "status": "ok" if known else "unknown",
            "scope": related.get("scope"),
            "accelerators": [fault["target"]["accelerator_id"]] if fault["target"].get("accelerator_id") else [],
            "pod_count": related.get("affected_pod_count") if known else None,
            "pods": pods if known else None,
            # Pod 로 셀 수 없는 범위(단독 VM)면 워크로드도 빈 목록이 아니라 null
            "workloads": sorted({p["workload"] for p in pods if p.get("workload")}) if known and pods is not None else None,
            "path": related.get("path", []),
        },
        "verification": {
            "post_action_window": VERIFY_WINDOW,
            "check": VERIFY[fault["fault_type"]],
            "reference_queries": [f"GET /api/v2/faults/{fault['fault_id']}", "GET /api/v2/faults/active"],
        },
        "generated_at": (now or datetime.now(timezone.utc)).isoformat(),
    }


async def list_recommendations() -> tuple[list[dict], list[str]]:
    """진행 중 장애마다 맞는 정책을 대입한 복구 정보 요약. 맞는 정책이 없는 장애는 빠지고 경고를 남긴다."""
    (items, warnings), policies = await faults.active_faults(), await load_policies()
    out = []
    for f in items:
        policy = match_policy(f, policies)
        if policy:
            out.append(summarize(f, policy))
        elif "NO_POLICY_MATCHED" not in warnings:
            warnings.append("NO_POLICY_MATCHED")
    out.sort(key=lambda r: list(PRIORITY.values()).index(r["priority"]))
    return out, warnings


async def get_recommendation(rec_id: str) -> tuple[Optional[dict], list[str]]:
    """복구 정보 1건. 장애가 없으면 (None, []), 맞는 정책이 없으면 (None, ['NO_POLICY_MATCHED'])."""
    fault, warnings = await faults.get_fault(fault_id_of(rec_id))
    if fault is None:
        return None, []
    policy = match_policy(fault, await load_policies())
    if policy is None:
        return None, ["NO_POLICY_MATCHED"]
    evidence = await faults.collect_evidence(fault)
    analysis = faults.analyze(fault, evidence)
    if analysis["unverified"]:
        warnings.append("EVIDENCE_PARTIAL")
    return build_recommendation(fault, policy, evidence, analysis), warnings
