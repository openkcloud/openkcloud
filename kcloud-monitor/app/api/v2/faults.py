"""장애 정보 분석 라우터.

알람 평가기의 firing 알람과 고정 PromQL 신호를 장애로 분류하고, 장애 하나의 증거(메트릭, 로그, 영향 자원)와
원인 분석을 조회한다. 조회 전용이다. 서비스 로직은 app/services/faults.py.
"""
from datetime import datetime, timedelta, timezone
from typing import Optional

from fastapi import APIRouter, HTTPException, Query

from app.schemas.faults import FaultDetailResponse, FaultListResponse
from app.services import faults

router = APIRouter(prefix="/faults")

_TYPES = "|".join(faults.FAULT_TYPES)
_CLUSTER = Query(None, description="클러스터 이름으로 좁힘, 예 `l40s`")
_TYPE = Query(None, pattern=f"^({_TYPES})$", description="장애 유형으로 좁힘")
_SEVERITY = Query(None, pattern="^(critical|warning|info)$", description="심각도로 좁힘 (critical | warning | info)")


def _filter(items: list[dict], cluster: Optional[str], fault_type: Optional[str], severity: Optional[str],
            status: Optional[str] = None) -> list[dict]:
    return [f for f in items
            if (cluster is None or f["target"].get("cluster") == cluster)
            and (fault_type is None or f["fault_type"] == fault_type)
            and (severity is None or f["severity"] == severity)
            and (status is None or f["status"] == status)]


def _status(warnings: list[str]) -> str:
    return "partial" if warnings else "success"


@router.get("/active", summary="진행 중 장애 목록", response_model=FaultListResponse)
async def list_active(cluster: Optional[str] = _CLUSTER, fault_type: Optional[str] = _TYPE,
                      severity: Optional[str] = _SEVERITY):
    """지금 걸려 있는 장애 목록 조회

    입력 예시

    * `GET /api/v2/faults/active`
    * `GET /api/v2/faults/active?cluster=l40s&severity=critical`

    입력 옵션

    * `cluster`: 클러스터 이름 (선택)
    * `fault_type`: 장애 유형 (선택)
    * `severity`: `critical` | `warning` | `info` (선택)

    응답

    * 장애마다 ID, 유형, 심각도, 발생 시각, 대상, 한 줄 설명, 탐지 근거
    * 정렬은 critical 먼저, 그 안에서 발생 시각 순

    탐지 경로

    * `alert`: 알람 정책이 firing 인 것 중 장애로 보는 지표 (가속기 상태 이상, 온도, 수집 중단, XID 로그, OOM 로그,
      재시작 반복 로그, 하드웨어와 디스크 오류 로그). 사용률, 메모리 포화, SLO 알람은 장애가 아니라 빠짐
    * `signal`: 알람 정책 없이도 보는 고정 신호. `xid`(최근 1시간 XID 코드 변화), `oom_killed`(최근 1시간 OOMKilled 종료),
      `restart_loop`(1시간 재시작 3회 이상), `npu_dead`(Furiosa alive 0), `npu_unhealthy`(Rebellions HEALTH 가 0 이 아님),
      `node_not_ready`, `vm_error`(Nova 상태 ERROR)

    장애 유형

    * `accelerator_error`, `npu_fault`, `workload_oom`, `workload_crash_loop`, `node_not_ready`, `vm_down`, `hardware_fault`

    경고

    * `ALERT_STORE_NOT_CONFIGURED`: 알람 저장소(DB)가 없어 고정 신호만 조회
    * `ALERT_POLICY_MISSING`: 정책이 지워진 알람 이력이 있어 분류하지 못하고 제외
    * `PROMETHEUS_UNAVAILABLE`: Prometheus 조회 실패로 고정 신호를 보지 못함
    """
    items, warnings = await faults.active_faults()
    data = _filter(items, cluster, fault_type, severity)
    return FaultListResponse(status=_status(warnings), data=data, total=len(data), warnings=warnings)


@router.get("/history", summary="장애 이력", response_model=FaultListResponse)
async def list_history(
    start: Optional[str] = Query(None, description="이 시각 이후 발생한 장애 (ISO 8601). 없으면 7일 전"),
    status: Optional[str] = Query(None, pattern="^(active|resolved|unknown)$",
                                  description="상태로 좁힘 (active | resolved | unknown)"),
    cluster: Optional[str] = _CLUSTER, fault_type: Optional[str] = _TYPE, severity: Optional[str] = _SEVERITY,
    limit: int = Query(100, ge=1, le=500, description="최대 개수 (1 ~ 500, 기본 100)"),
):
    """지난 장애 목록 조회. 알람 이력의 발생, 해제 기록으로 만든다

    입력 예시

    * `GET /api/v2/faults/history`
    * `GET /api/v2/faults/history?start=2026-11-01&status=resolved&fault_type=workload_oom`

    입력 옵션

    * `start`: 조회 시작 시각, ISO 8601 (선택, 기본 7일 전. 날짜만 주면 UTC 0시)
    * `status`: `active` | `resolved` | `unknown` (선택)
    * `cluster`, `fault_type`, `severity`: 범위 필터 (선택)
    * `limit`: 최대 개수 (기본 100)

    응답

    * 최신 순. 장애마다 발생 시각과 해제 시각
    * 고정 신호로만 잡힌 장애는 이력에 남지 않음. 이력이 필요한 신호는 알람 정책으로 등록

    오류

    * 422: `start` 형식 오류
    * 503: 알람 저장소(DB) 미설정 (`ALERT_STORE_NOT_CONFIGURED`)
    """
    try:
        since = datetime.fromisoformat(start) if start else datetime.now(timezone.utc) - timedelta(days=7)
    except ValueError:
        raise HTTPException(status_code=422, detail="start 는 ISO 8601 날짜나 시각 (예: 2026-11-01)")
    if since.tzinfo is None:
        since = since.replace(tzinfo=timezone.utc)
    items, warnings = await faults.alert_faults(since)
    if "ALERT_STORE_NOT_CONFIGURED" in warnings:
        raise HTTPException(status_code=503, detail="ALERT_STORE_NOT_CONFIGURED: DATABASE_URL 을 설정하세요")
    data = _filter(items, cluster, fault_type, severity, status)[:limit]
    return FaultListResponse(status=_status(warnings), data=data, total=len(data), warnings=warnings)


@router.get("/{fault_id}", summary="장애 상세와 원인 분석", response_model=FaultDetailResponse)
async def get_fault(fault_id: str):
    """장애 하나의 증거와 원인 분석 조회

    입력 예시

    * `GET /api/v2/faults/flt-3f9a1c2b7d10`

    입력 옵션

    * `fault_id`: 장애 ID (경로, 필수). 목록 응답의 `fault_id`

    응답

    * 장애 기본 정보 (목록과 같음)
    * `evidence.metrics_snapshot`: 발생 30분 전부터 10분 뒤까지의 메트릭. 가속기는 사용률, 온도, 전력, 메모리,
      워크로드는 메모리 사용량과 한도, 재시작 횟수, 노드는 Ready 와 자원 압박 조건
    * `evidence.logs_snapshot`: 같은 구간의 관련 로그 최대 20줄과 뽑아낸 필드 (XID 코드, OOM 프로세스 등)
    * `evidence.related_resources`: 대상에서 물리서버까지의 경로와 영향을 받는 Pod
    * `evidence.collection_status`: 증거 종류별 수집 결과
    * `analysis`: 추정 원인, 기여 요인, 확신 정도. 규칙으로 낸 추정이며 조치는 실행하지 않음

    경고

    * `EVIDENCE_PARTIAL`: 증거 일부를 수집하지 못함. `evidence.collection_status` 에 종류별 이유
    * 그 밖은 진행 중 장애 목록과 같음

    오류

    * 404: 없는 장애. 고정 신호 장애는 신호가 사라지면 조회할 수 없고, 알람 장애는 30일까지 조회
    """
    fault, warnings = await faults.get_fault(fault_id)
    if fault is None:
        raise HTTPException(status_code=404, detail=f"장애를 찾을 수 없음: {fault_id}")
    evidence = await faults.collect_evidence(fault)
    analysis = faults.analyze(fault, evidence)
    if analysis["unverified"]:
        warnings.append("EVIDENCE_PARTIAL")
    return FaultDetailResponse(status=_status(warnings), warnings=warnings,
                               data={**fault, "evidence": evidence, "analysis": analysis})
