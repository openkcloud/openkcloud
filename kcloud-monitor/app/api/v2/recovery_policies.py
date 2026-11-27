"""장애 후속 조치 정책 라우터.

장애 유형별 조치 절차(정책)를 조회, 생성, 수정, 삭제한다. 정책은 권고 전용이고 이 API 는 조치를 실행하지 않는다.
서비스 로직은 app/services/recovery_policies.py.
"""
from typing import Optional

from fastapi import APIRouter, HTTPException, Query, Response

from app.schemas.recovery_policies import (
    FaultType,
    RecoveryPolicyBody,
    RecoveryPolicyListResponse,
    RecoveryPolicyResponse,
)
from app.services import recovery_policies as service
from app.services.recovery_policies import store

router = APIRouter(prefix="/recovery/policies")

_NOT_CONFIGURED = "RECOVERY_STORE_NOT_CONFIGURED"


def _require_store() -> None:
    if not store.ready:
        raise HTTPException(status_code=503, detail=f"{_NOT_CONFIGURED}: DATABASE_URL 을 설정하세요")


def _checked(body: RecoveryPolicyBody) -> dict:
    data = body.model_dump()
    errors = service.check_policy(data)
    if errors:
        raise HTTPException(status_code=422, detail={"errors": errors})
    return data


def _out(row: dict) -> dict:
    return {**row, "created_at": row["created_at"].isoformat() if row.get("created_at") else None,
            "updated_at": row["updated_at"].isoformat() if row.get("updated_at") else None}


@router.get("", summary="후속 조치 정책 목록", response_model=RecoveryPolicyListResponse)
async def list_policies(
    fault_type: Optional[FaultType] = Query(None, description="장애 유형으로 좁힘"),
    enabled: Optional[bool] = Query(None, description="사용 여부로 좁힘"),
):
    """후속 조치 정책 목록 조회

    입력 예시

    * `GET /api/v2/recovery/policies`
    * `GET /api/v2/recovery/policies?fault_type=workload_oom&enabled=true`

    입력 옵션

    * `fault_type`: 장애 유형 (선택)
    * `enabled`: 사용 여부 (선택)

    응답

    * 정책마다 적용 조건(장애 유형, 벤더, XID 코드), 사용 여부, 조치 절차
    * 절차 단계마다 종류(`manual_review` | `manual_action` | `record_feedback`), 안내문, 참고 조회식, 위험도
    * `mode` 는 항상 `recommendation-only`. 조치는 관리자가 직접 실행

    경고

    * `RECOVERY_STORE_NOT_CONFIGURED`: 저장소(DB)가 없어 초기 정책 7건을 읽기 전용으로 반환
    """
    rows = await service.load_policies()
    data = [_out(p) for p in rows
            if (fault_type is None or p["fault_type"] == fault_type) and (enabled is None or p["enabled"] == enabled)]
    warnings = [] if store.ready else [_NOT_CONFIGURED]
    return RecoveryPolicyListResponse(status="partial" if warnings else "success", data=data, total=len(data),
                                      warnings=warnings)


@router.post("", summary="후속 조치 정책 생성", response_model=RecoveryPolicyResponse, status_code=201)
async def create_policy(body: RecoveryPolicyBody):
    """후속 조치 정책 생성

    입력 예시

    * `POST /api/v2/recovery/policies`
    * `{"name": "XID 79 전용", "fault_type": "accelerator_error", "vendor_scope": ["nvidia"], "error_codes": [79],
      "steps": [{"name": "노드 스케줄 중지", "action_type": "manual_action", "guidance": "kubectl cordon", "risk_level": "medium"}]}`

    입력 옵션

    * `name`, `fault_type`, `steps`: 필수
    * `vendor_scope`, `error_codes`: 적용 범위를 좁힘 (선택, 비우면 전체). 한 장애에 여러 정책이 맞으면
      XID 코드를 지정한 정책, 벤더를 지정한 정책 순으로 우선
    * `steps[].risk_level`: `manual_action` 단계는 필수
    * `steps[].reference_queries`: 자리표시자는 `{fault_id}`, `{cluster}`, `{node}`, `{namespace}`, `{pod}`, `{container}`,
      `{accelerator_id}`, `{vm_id}` 만 허용

    오류

    * 422: 형식 오류 또는 규칙 위반. `detail.errors` 에 `RISK_LEVEL_REQUIRED`, `UNKNOWN_PLACEHOLDER` 등.
      `mode` 에 `recommendation-only` 가 아닌 값을 주면 거절
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    return RecoveryPolicyResponse(data=_out(await store.create(_checked(body))))


@router.get("/{policy_id}", summary="후속 조치 정책 상세", response_model=RecoveryPolicyResponse)
async def get_policy(policy_id: str):
    """후속 조치 정책 1건 조회

    입력 예시

    * `GET /api/v2/recovery/policies/policy-workload-oom`

    오류

    * 404: 없는 정책
    """
    row = next((p for p in await service.load_policies() if p["policy_id"] == policy_id), None)
    if row is None:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return RecoveryPolicyResponse(data=_out(row))


@router.put("/{policy_id}", summary="후속 조치 정책 수정", response_model=RecoveryPolicyResponse)
async def replace_policy(policy_id: str, body: RecoveryPolicyBody):
    """후속 조치 정책 전체 교체. 사용 여부만 바꿀 때도 본문 전체를 보낸다

    입력 예시

    * `PUT /api/v2/recovery/policies/policy-workload-oom`

    오류

    * 404: 없는 정책
    * 422: 형식 오류 또는 규칙 위반
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    row = await store.replace(policy_id, _checked(body))
    if row is None:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return RecoveryPolicyResponse(data=_out(row))


@router.delete("/{policy_id}", summary="후속 조치 정책 삭제", status_code=204)
async def delete_policy(policy_id: str):
    """후속 조치 정책 삭제. 그 유형에 맞는 정책이 남지 않으면 복구 정보가 만들어지지 않는다

    오류

    * 404: 없는 정책
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    if not await store.delete(policy_id):
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return Response(status_code=204)
