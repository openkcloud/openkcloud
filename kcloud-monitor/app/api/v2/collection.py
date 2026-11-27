"""수집 관리 라우터

수집 대상별 주기와 저장량 조회, 수집 정책 저장과 미리보기, 적용과 되돌리기.
실제 수집 설정은 apply 를 부를 때만 바뀌고, 서버의 허용 목록(COLLECTION_MANAGED_TARGETS)에 있는 대상만 가능하다.
DATABASE_URL 이 없으면 정책 경로는 503 COLLECTION_STORE_NOT_CONFIGURED.
"""
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Query

from app.auth import verify_token_or_api_key
from app.schemas.collection import (
    CollectionHistoryResponse,
    CollectionMetricListResponse,
    CollectionPolicyCreate,
    CollectionPolicyListResponse,
    CollectionPolicyPatch,
    CollectionPolicyResponse,
    CollectionPreviewResponse,
    CollectionSummary,
    CollectionTargetListResponse,
)
from app.services import collection as collection_service
from app.services.collection import store
from app.services.k8s import K8sError, k8s_client

router = APIRouter(prefix="/monitoring/collection")


@router.get("/targets", summary="수집 대상 목록", response_model=CollectionTargetListResponse)
async def list_collection_targets(
    mode: Optional[str] = Query(None, pattern="^(pull|push)$", description="수집 방식 필터 (pull | push)"),
    adjustable: Optional[bool] = Query(None, description="API 로 바꿀 수 있는 대상만(true) 또는 잠긴 대상만(false)"),
    search: Optional[str] = Query(None, description="ID 나 job 에 들어 있는 글자 (대소문자 무시)"),
):
    """수집 대상별 수집 주기와 저장량 조회

    입력 예시

    * `GET /api/v2/monitoring/collection/targets?mode=pull`

    입력 옵션

    * `mode`: 수집 방식 `pull` | `push` (선택)
    * `adjustable`: 바꿀 수 있는 대상만 `true`, 잠긴 대상만 `false` (선택)
    * `search`: ID 나 job 에 들어 있는 글자 (선택)

    응답

    * 묶음별 `id`, `job`, `cluster`, `source`, 수집 방식 `mode`
    * `targets`, `targets_up`: 수집 지점 개수와 수집 성공 개수
    * `interval_seconds`: 수집 주기(초)
    * `interval_estimated`: 주기가 추정값인지 여부
    * `timeout_seconds`, `max_scrape_duration_seconds`: 수집 제한 시간과 최근 소요 시간(초)
    * `samples_per_scrape`, `samples_per_second`: 수집 1회 저장량과 초당 저장량
    * `share_percent`: 전체 초당 저장량에서 차지하는 비율(%)
    * `adjustable`, `lock_reason`: 변경 가능 여부와 잠긴 이유
    * `summary`: 필터와 무관한 전체 묶음 개수와 초당 저장량 합계
    * `total`: 필터 적용 후 묶음 개수
    * 초당 저장량이 많은 순으로 정렬

    수집 방식

    * `pull`: Prometheus 가 직접 가져오는 대상. 주기는 설정값 그대로
    * `push`: 원격 수집기(Alloy)가 보내 주는 대상. 주기는 최근 10분 수집 횟수로 추정

    잠긴 이유

    * `push`: 원격 수집기 설정 파일로만 변경 가능
    * `not_managed`: 서버의 허용 목록에 없는 대상

    참고

    * 수집 대상 목록 조회 실패 시 `UPSTREAM_ERROR` 경고와 함께 `mode` 가 null
    * 수집 대상이 하나도 없으면 `NO_DATA` 경고
    """
    items, warnings = await collection_service.list_targets()
    summary = CollectionSummary(**collection_service.summarize(items))
    if mode:
        items = [i for i in items if i["mode"] == mode]
    if adjustable is not None:
        items = [i for i in items if i["adjustable"] == adjustable]
    if search:
        needle = search.lower()
        items = [i for i in items if needle in i["id"].lower() or needle in i["job"].lower()]
    return CollectionTargetListResponse(
        status="partial" if warnings else "success",
        data=items,
        total=len(items),
        summary=summary,
        warnings=warnings,
    )


@router.get("/metrics", summary="job별 메트릭 시계열 순위", response_model=CollectionMetricListResponse)
async def list_collection_metrics(
    job: str = Query(..., min_length=1, description="조회할 job 라벨 값 (예: kepler)"),
    cluster: Optional[str] = Query(None, description="cluster 라벨 값. 같은 job 이 여러 클러스터에 있을 때 지정"),
    limit: int = Query(20, ge=1, le=200, description="받을 메트릭 개수 (1 ~ 200, 기본 20)"),
):
    """job 하나에서 시계열이 많은 메트릭 순위 조회

    입력 예시

    * `GET /api/v2/monitoring/collection/metrics?job=kepler&limit=10`

    입력 옵션

    * `job`: job 라벨 값 (필수)
    * `cluster`: cluster 라벨 값 (선택)
    * `limit`: 받을 메트릭 개수 `1` ~ `200` (기본 `20`)

    응답

    * 메트릭별 이름 `metric`, 시계열 개수 `series`
    * `protected`: 이 API 가 조회에 쓰는 메트릭이라 수집에서 뺄 수 없는지 여부
    * `share_percent`: 같은 job 전체 시계열에서 차지하는 비율(%)
    * `total_series`: 이 job 의 전체 시계열 개수
    * 시계열이 많은 순으로 정렬

    참고

    * 해당 job 의 메트릭이 없거나 조회 실패 시 `NO_DATA` 경고
    """
    items, total_series = await collection_service.top_metrics(job, cluster, limit)
    warnings = [] if items else ["NO_DATA"]
    return CollectionMetricListResponse(
        status="partial" if warnings else "success",
        job=job,
        cluster=cluster,
        data=items,
        total_series=total_series,
        warnings=warnings,
    )


# ---------------------------------------------------------------------------
# 수집 정책
# ---------------------------------------------------------------------------

def _require_store() -> None:
    if not store.ready:
        raise HTTPException(status_code=503, detail="COLLECTION_STORE_NOT_CONFIGURED: DATABASE_URL 을 설정하세요")


async def _policy_or_404(policy_id: str) -> dict:
    row = await store.get_policy(policy_id)
    if not row:
        raise HTTPException(status_code=404, detail="POLICY_NOT_FOUND")
    return row


def _require_draft(policy: dict) -> None:
    if policy["state"] != "draft":
        raise HTTPException(status_code=409, detail="POLICY_APPLIED: 적용 중인 정책은 먼저 되돌려야 함")


def _require_k8s() -> None:
    if not k8s_client.configured:
        raise HTTPException(status_code=503, detail="K8S_NOT_CONFIGURED: K8S_API_URL 과 K8S_TOKEN 을 설정하세요")


async def _check(policy: dict, policy_id: Optional[str] = None) -> dict:
    """저장 전 검사. 잘못된 칸이 있으면 모두 모아 422."""
    target, _, warnings = await collection_service.find_target(policy["target_id"])
    if target is None and "UPSTREAM_ERROR" in warnings:
        raise HTTPException(status_code=503, detail="UPSTREAM_ERROR: 수집 대상 목록을 조회하지 못해 검사 불가")
    errors = collection_service.check_policy(policy, target)
    other = await store.get_by_target(policy["target_id"])
    if other and other["id"] != policy_id:
        errors.append({"field": "target_id", "message": f"이 대상에는 이미 정책이 있음: {other['id']}"})
    if errors:
        raise HTTPException(status_code=422, detail={"errors": errors})
    return policy


@router.get("/policies", summary="수집 정책 목록", response_model=CollectionPolicyListResponse)
async def list_collection_policies():
    """저장된 수집 정책 목록 조회

    입력 예시

    * `GET /api/v2/monitoring/collection/policies`

    응답

    * 정책별 `id`, `name`, `description`, `target_id`
    * `interval_seconds`: 바꿀 수집 주기(초)
    * `drop_metrics`: 수집에서 뺄 메트릭 이름 목록
    * `state`: 정책 상태 `draft` | `pending` | `applied`
    * `applied_at`, `confirm_deadline`: 적용 시각과 확정 기한
    * `created_at`, `updated_at`: 생성 시각과 마지막 수정 시각
    * `total`: 정책 개수
    * 생성 시각 오래된 순으로 정렬

    오류

    * 503: 저장소(DB) 미설정
    """
    _require_store()
    rows = await store.list_policies()
    return CollectionPolicyListResponse(data=rows, total=len(rows))


@router.post(
    "/policies", summary="수집 정책 생성", response_model=CollectionPolicyResponse, status_code=201
)
async def create_collection_policy(body: CollectionPolicyCreate):
    """수집 정책 생성

    입력 예시

    * `{"name": "Kepler 프로세스 지표 제외", "target_id": "serviceMonitor/monitoring/kepler/0", "interval_seconds": 30, "drop_metrics": ["kepler_process_cpu_watts"]}`

    입력 옵션

    * `name`: 정책 이름 (필수)
    * `description`: 정책 설명 (선택)
    * `target_id`: 수집 대상 ID, 수집 대상 목록의 `id` 값 (필수)
    * `interval_seconds`: 바꿀 수집 주기(초) `5` ~ `60` (선택)
    * `drop_metrics`: 수집에서 뺄 메트릭 이름 목록, 최대 `50` 개 (선택)
    * `interval_seconds` 와 `drop_metrics` 중 하나는 필수

    응답

    * 저장된 정책 (`id`, `name`, `description`, `target_id`, `interval_seconds`, `drop_metrics`, `created_at`, `updated_at`)
    * `state`: 항상 `draft` (저장만 된 상태)

    참고

    * 저장만 하고 실제 수집 설정은 바꾸지 않음
    * 수집 대상 하나에 정책은 하나만 가능

    오류

    * 422: 저장하지 않고 잘못된 칸을 모두 한 번에 반환
    * 응답 형식: `{"detail": {"errors": [{"field": "drop_metrics[0]", "message": "..."}]}}`
    * 수집 중이 아닌 대상, `push` 방식 대상, 이미 정책이 있는 대상
    * 범위를 벗어난 주기, 대상의 수집 제한 시간보다 짧은 주기
    * 패턴이 섞인 메트릭 이름, 이 API 가 조회에 쓰는 메트릭
    * 503: 저장소(DB) 미설정 또는 수집 대상 목록 조회 실패
    """
    _require_store()
    row = await store.create_policy(await _check(body.model_dump()))
    return CollectionPolicyResponse(data=row)


@router.get("/policies/{policy_id}", summary="수집 정책 상세", response_model=CollectionPolicyResponse)
async def get_collection_policy(policy_id: str):
    """수집 정책 1건 조회

    입력 예시

    * `GET /api/v2/monitoring/collection/policies/col-1a2b3c`

    응답

    * `id`, `name`, `description`, `target_id`
    * `interval_seconds`: 바꿀 수집 주기(초)
    * `drop_metrics`: 수집에서 뺄 메트릭 이름 목록
    * `state`: 정책 상태 `draft` | `pending` | `applied`
    * `applied_at`, `confirm_deadline`: 적용 시각과 확정 기한
    * `created_at`, `updated_at`: 생성 시각과 마지막 수정 시각

    상태

    * `draft`: 저장만 된 상태, 수집 설정은 그대로
    * `pending`: 적용했고 확정 대기 중, 기한이 지나면 자동으로 되돌림
    * `applied`: 확정 완료

    오류

    * 404: 없는 정책
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    return CollectionPolicyResponse(data=await _policy_or_404(policy_id))


@router.patch("/policies/{policy_id}", summary="수집 정책 수정", response_model=CollectionPolicyResponse)
async def patch_collection_policy(policy_id: str, body: CollectionPolicyPatch):
    """수집 정책 수정

    입력 예시

    * `PATCH /api/v2/monitoring/collection/policies/col-1a2b3c`
    * `{"interval_seconds": 15}`

    입력 옵션

    * `name`, `description`, `target_id`: 바꿀 것만 보냄
    * `interval_seconds`: 바꿀 수집 주기(초) `5` ~ `60`, `null` 을 보내면 주기 변경 취소
    * `drop_metrics`: 뺄 메트릭 목록을 통째로 교체, `[]` 를 보내면 비움

    응답

    * 수정된 정책

    오류

    * 422: 정책 생성과 같은 검사, 수정 후 전체 내용 기준으로 잘못된 칸을 모두 한 번에 반환
    * 404: 없는 정책
    * 409: 적용 중인 정책, 먼저 되돌려야 수정 가능
    * 503: 저장소(DB) 미설정 또는 수집 대상 목록 조회 실패
    """
    _require_store()
    cur = await _policy_or_404(policy_id)
    _require_draft(cur)
    patch = body.model_dump(exclude_unset=True)
    # null 로 지울 수 있는 칸은 주기뿐. 나머지 칸의 null 은 안 보낸 것으로 봄
    patch = {k: v for k, v in patch.items() if v is not None or k == "interval_seconds"}
    row = await store.update_policy(policy_id, await _check({**cur, **patch}, policy_id))
    return CollectionPolicyResponse(data=row)


@router.delete("/policies/{policy_id}", summary="수집 정책 삭제", status_code=204)
async def delete_collection_policy(policy_id: str):
    """수집 정책 삭제

    입력 예시

    * `DELETE /api/v2/monitoring/collection/policies/col-1a2b3c`

    응답

    * 성공 시 본문 없이 204

    오류

    * 404: 없는 정책
    * 409: 적용 중인 정책, 먼저 되돌려야 삭제 가능
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    _require_draft(await _policy_or_404(policy_id))
    await store.delete_policy(policy_id)


@router.post(
    "/policies/{policy_id}/preview", summary="수집 정책 미리보기", response_model=CollectionPreviewResponse
)
async def preview_collection_policy(policy_id: str):
    """수집 정책을 적용했을 때의 예상 저장량 계산

    입력 예시

    * `POST /api/v2/monitoring/collection/policies/col-1a2b3c/preview`

    응답

    * `current`: 지금 수집 주기, 수집 1회 저장량, 초당 저장량
    * `expected`: 적용 후 예상 수집 주기, 수집 1회 저장량, 초당 저장량
    * `dropped`: 빼려는 메트릭별 시계열 개수
    * `saved_samples_per_second`: 줄어드는 초당 저장량, 주기를 줄이는 정책이면 음수
    * `saved_target_percent`, `saved_total_percent`: 이 대상과 전체 저장량 대비 줄어드는 비율(%)
    * `applicable`, `lock_reason`: 실제 적용 가능 여부와 잠긴 이유

    참고

    * 계산만 하고 실제 수집 설정은 바꾸지 않음
    * 대상에 없는 메트릭이 있으면 `METRIC_NOT_FOUND` 경고, 그 메트릭의 `series` 는 `0`
    * 대상이 지금 수집 중이 아니면 `data` 가 null 이고 `NO_DATA` 경고

    오류

    * 404: 없는 정책
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    policy = await _policy_or_404(policy_id)
    data, warnings = await collection_service.preview(policy)
    if data is None and "NO_DATA" not in warnings:
        warnings = [*warnings, "NO_DATA"]
    return CollectionPreviewResponse(status="partial" if warnings else "success", data=data, warnings=warnings)


# ---------------------------------------------------------------------------
# 적용, 확정, 되돌리기, 이력
# ---------------------------------------------------------------------------

@router.post("/policies/{policy_id}/apply", summary="수집 정책 적용", response_model=CollectionPolicyResponse)
async def apply_collection_policy(policy_id: str, actor: str = Depends(verify_token_or_api_key)):
    """수집 정책을 실제 수집 설정에 반영

    입력 예시

    * `POST /api/v2/monitoring/collection/policies/col-1a2b3c/apply`

    응답

    * 적용된 정책, `state` 는 `pending`
    * `confirm_deadline`: 이 시각까지 확정하지 않으면 자동으로 되돌림

    동작

    * 수집 설정의 수집 주기를 정책 값으로 바꾸고, 원래 있던 필터 뒤에 메트릭 제외 규칙 추가
    * 바꾸기 전 값을 저장해 두고 되돌릴 때 그대로 복구
    * 적용 약 90초 뒤부터 수집 성공 개수가 적용 전보다 줄면 자동으로 되돌림
    * 실제 반영까지 1분 안팎 소요

    오류

    * 404: 없는 정책
    * 409 `POLICY_APPLIED`: 이미 적용 중인 정책
    * 409 `TARGET_LOCKED`: 서버의 허용 목록에 없는 수집 대상
    * 409 `TARGET_NOT_PATCHABLE`: 고칠 수 있는 수집 설정이 없는 대상
    * 422: 정책 생성과 같은 검사를 지금 기준으로 다시 수행
    * 502 `K8S_ERROR`: Kubernetes 호출 실패, 수집 설정은 바뀌지 않음
    * 503: 저장소(DB) 미설정, Kubernetes 접속 정보 미설정 또는 수집 대상 목록 조회 실패
    """
    _require_store()
    policy = await _policy_or_404(policy_id)
    _require_draft(policy)
    _require_k8s()
    await _check(policy, policy_id)
    target, _, _ = await collection_service.find_target(policy["target_id"])
    if target is None:
        raise HTTPException(status_code=503, detail="UPSTREAM_ERROR: 수집 대상 목록을 조회하지 못해 검사 불가")
    if not target["adjustable"]:
        raise HTTPException(status_code=409, detail=f"TARGET_LOCKED: {target['lock_reason']}")
    if collection_service.parse_target_id(policy["target_id"]) is None:
        raise HTTPException(status_code=409, detail="TARGET_NOT_PATCHABLE: 고칠 수 있는 수집 설정이 없는 대상")
    try:
        row = await collection_service.apply_policy(policy, target, actor)
    except K8sError as exc:
        raise HTTPException(status_code=502, detail=f"K8S_ERROR: {exc}") from exc
    return CollectionPolicyResponse(data=row)


@router.post("/policies/{policy_id}/confirm", summary="수집 정책 확정", response_model=CollectionPolicyResponse)
async def confirm_collection_policy(policy_id: str, actor: str = Depends(verify_token_or_api_key)):
    """적용한 수집 정책을 확정해 자동 되돌리기 중지

    입력 예시

    * `POST /api/v2/monitoring/collection/policies/col-1a2b3c/confirm`

    응답

    * 확정된 정책, `state` 는 `applied`, `confirm_deadline` 은 null

    참고

    * 확정 후에도 되돌리기 가능

    오류

    * 404: 없는 정책
    * 409 `POLICY_NOT_PENDING`: 확정 대기 중이 아닌 정책
    * 503: 저장소(DB) 미설정
    """
    _require_store()
    policy = await _policy_or_404(policy_id)
    if policy["state"] != "pending":
        raise HTTPException(status_code=409, detail="POLICY_NOT_PENDING: 확정 대기 중인 정책이 아님")
    return CollectionPolicyResponse(data=await collection_service.confirm_policy(policy, actor))


@router.post("/policies/{policy_id}/rollback", summary="수집 정책 되돌리기", response_model=CollectionPolicyResponse)
async def rollback_collection_policy(policy_id: str, actor: str = Depends(verify_token_or_api_key)):
    """수집 설정을 정책 적용 전 값으로 복구

    입력 예시

    * `POST /api/v2/monitoring/collection/policies/col-1a2b3c/rollback`

    응답

    * 되돌린 정책, `state` 는 `draft`

    참고

    * 수집 주기와 필터를 적용 직전 값 그대로 복구
    * 정책 내용은 남아 있어 다시 적용 가능

    오류

    * 404: 없는 정책
    * 409 `POLICY_NOT_APPLIED`: 적용한 적 없는 정책
    * 502 `K8S_ERROR`: Kubernetes 호출 실패, 상태는 그대로
    * 503: 저장소(DB) 미설정 또는 Kubernetes 접속 정보 미설정
    """
    _require_store()
    policy = await _policy_or_404(policy_id)
    if policy["state"] == "draft":
        raise HTTPException(status_code=409, detail="POLICY_NOT_APPLIED: 적용한 적 없는 정책")
    _require_k8s()
    try:
        row = await collection_service.rollback_policy(policy, "rollback", "", actor)
    except K8sError as exc:
        raise HTTPException(status_code=502, detail=f"K8S_ERROR: {exc}") from exc
    return CollectionPolicyResponse(data=row)


@router.get("/history", summary="수집 설정 변경 이력", response_model=CollectionHistoryResponse)
async def list_collection_history(
    policy_id: Optional[str] = Query(None, description="특정 정책의 이력만"),
    limit: int = Query(100, ge=1, le=1000, description="받을 이력 개수 (1 ~ 1000, 기본 100)"),
):
    """수집 설정을 바꾼 이력 조회

    입력 예시

    * `GET /api/v2/monitoring/collection/history?policy_id=col-1a2b3c`

    입력 옵션

    * `policy_id`: 특정 정책만 (선택)
    * `limit`: 받을 이력 개수 `1` ~ `1000` (기본 `100`)

    응답

    * 이력별 `id`, 기록 시각 `ts`, `policy_id`, `target_id`
    * `action`: 한 일 `apply` | `confirm` | `rollback` | `auto_rollback`
    * `reason`: 자동으로 되돌린 이유 `confirm_timeout` | `health_check_failed` | `config_gone`
    * `actor`: 요청한 사용자, 서버가 자동으로 한 일이면 `system`
    * `before`, `after`: 바꾸기 전과 후의 수집 주기와 필터 규칙
    * `total`: 반환한 이력 개수
    * 기록 시각 최신순으로 정렬

    오류

    * 503: 저장소(DB) 미설정
    """
    _require_store()
    rows = await store.list_history(policy_id, limit)
    return CollectionHistoryResponse(data=rows, total=len(rows))
