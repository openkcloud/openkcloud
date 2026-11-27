"""복구 정보 라우터.

진행 중 장애에 후속 조치 정책을 대입해 조치 절차, 추정 원인, 영향 범위, 확인 방법을 만든다.
조회 전용이고 조치를 실행하지 않는다. 서비스 로직은 app/services/recovery.py.
"""
from typing import Optional

from fastapi import APIRouter, HTTPException, Query

from app.schemas.recovery import RecommendationListResponse, RecommendationResponse
from app.schemas.recovery_policies import FaultType
from app.services import recovery

router = APIRouter(prefix="/recovery/recommendations")


def _status(warnings: list[str]) -> str:
    return "partial" if warnings else "success"


@router.get("", summary="복구 정보 목록", response_model=RecommendationListResponse)
async def list_recommendations(
    cluster: Optional[str] = Query(None, description="클러스터 이름으로 좁힘, 예 `l40s`"),
    fault_type: Optional[FaultType] = Query(None, description="장애 유형으로 좁힘"),
    priority: Optional[str] = Query(None, pattern="^(urgent|normal|info)$",
                                    description="우선순위로 좁힘 (urgent | normal | info)"),
):
    """진행 중 장애마다 만든 복구 정보 요약 목록

    입력 예시

    * `GET /api/v2/recovery/recommendations`
    * `GET /api/v2/recovery/recommendations?priority=urgent&cluster=l40s`

    입력 옵션

    * `cluster`, `fault_type`, `priority`: 범위 필터 (선택)

    응답

    * 장애마다 한 건. 우선순위(`urgent` 먼저), 대상, 적용한 후속 조치 정책, 절차 단계 수
    * 정책은 `/recovery/policies` 에서 켜져 있고 장애 유형, 벤더, XID 코드가 맞는 것 중 가장 좁은 것을 고름
    * 조치 절차와 영향 범위는 상세에서 조회

    경고

    * `NO_POLICY_MATCHED`: 맞는 정책이 없어 빠진 장애가 있음. `/faults/active` 와 비교해 정책을 추가
    * `RECOVERY_STORE_NOT_CONFIGURED` 는 없음. 저장소가 없으면 초기 정책으로 만든다
    * 그 밖은 `/faults/active` 와 같음
    """
    items, warnings = await recovery.list_recommendations()
    data = [r for r in items
            if (cluster is None or r["target"].get("cluster") == cluster)
            and (fault_type is None or r["fault_type"] == fault_type)
            and (priority is None or r["priority"] == priority)]
    return RecommendationListResponse(status=_status(warnings), data=data, total=len(data), warnings=warnings)


@router.get("/{recommendation_id}", summary="복구 정보 상세", response_model=RecommendationResponse)
async def get_recommendation(recommendation_id: str):
    """장애 하나의 복구 절차와 관련 정보 생성

    입력 예시

    * `GET /api/v2/recovery/recommendations/rec-3f9a1c2b7d10`

    입력 옵션

    * `recommendation_id`: 복구 정보 ID (경로, 필수). 목록 응답의 `recommendation_id`

    응답

    * `probable_cause`, `contributing_factors`, `confidence`: 증거 기반 추정 원인
    * `recommended_actions`: 조치 절차. 단계마다 종류, 안내문, 위험도, 이 장애의 대상 값이 채워진 참고 API 경로
    * `affected_scope`: 영향 가속기, Pod, 워크로드와 물리서버까지의 경로. 범위를 정하지 못하면 `status` 가 `unknown`
    * `verification`: 조치 뒤 지켜볼 시간과 확인할 내용
    * `mode` 는 항상 `recommendation-only`. 이 API 는 조치를 실행하지 않고, 검토 상태와 조치 결과를 저장하지 않음

    경고

    * `EVIDENCE_PARTIAL`: 증거 일부를 수집하지 못해 원인 추정과 영향 범위가 불완전

    오류

    * 404: 없는 장애(`RECOMMENDATION_NOT_FOUND`) 또는 맞는 정책 없음(`NO_POLICY_MATCHED`)
    """
    rec, warnings = await recovery.get_recommendation(recommendation_id)
    if rec is None:
        code = "NO_POLICY_MATCHED" if warnings else "RECOMMENDATION_NOT_FOUND"
        raise HTTPException(status_code=404, detail=f"{code}: {recommendation_id}")
    return RecommendationResponse(status=_status(warnings), data=rec, warnings=warnings)
