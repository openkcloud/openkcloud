"""측정 데이터 내보내기 라우터

지원 포맷은 csv와 json. 그 외 포맷 요청은 400 UNSUPPORTED_FORMAT 반환.
"""
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, Query, Request
from fastapi.responses import Response

from app.api.v2.deps import TimeseriesParams
from app.api.v2.monitoring import time_range
from app.schemas.export import (
    EXPORT_METRIC_ALLOWLIST,
    MetricExportData,
    MetricExportResponse,
    MetricExportRow,
    PowerExportData,
    PowerExportResponse,
    PowerExportRow,
    ReportExportResponse,
)
from app.services.exporters import metric_export_rows, power_export_rows, rows_to_csv

router = APIRouter()

_SUPPORTED_FORMATS = ("csv", "json")


def _check_format(format: str) -> None:
    """csv|json 이외 포맷 요청 시 400. excel/parquet 등은 의존성 미도입으로 차단."""
    if format not in _SUPPORTED_FORMATS:
        raise HTTPException(
            status_code=400,
            detail=f"UNSUPPORTED_FORMAT: csv|json만 지원 (요청값: {format})",
        )


def _safe_filename_part(value: str) -> str:
    """Content-Disposition 파일명에 부적합한 문자(콜론 등) 치환 — ISO 8601 타임스탬프 대응."""
    return value.replace(":", "-").replace("/", "-")


@router.get("/export/power", summary="전력 데이터 내보내기")
async def export_power(
    request: Request,
    format: str = Query("csv", description="내려받을 형식 (`csv` | `json`, 기본 `csv`)"),
    params: TimeseriesParams = Depends(),
):
    """기간 내 전력 측정값을 CSV 또는 JSON 으로 내보내기

    입력 예시

    * `GET /api/v2/export/power`
    * `GET /api/v2/export/power?format=json&period=24h&step=5m`

    입력 옵션

    * `format`: `csv` | `json` (기본 `csv`)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 현재 시각에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * 행마다 `timestamp` (측정 시각, ISO 8601 UTC), `node` (노드 이름), `layer` (측정 구분), `watts` (전력 W)
    * `layer`: `server` (IPMI 서버 총전력) | `cpu` (Kepler CPU 전력) | `accelerator:벤더` (가속기 전력)
    * `format=csv`: `power_시작_종료.csv` 첨부 파일로 내려받기
    * `format=json`: `data.rows` 배열로 반환

    경고

    * `NO_DATA_SERVER_POWER`: 서버 전력 데이터가 없음
    * `NO_DATA_CPU_POWER`: CPU 전력 데이터가 없음
    * `NO_DATA_ACCELERATOR`: 가속기 전력 데이터가 없음

    오류

    * 400: `csv`, `json` 이외의 `format`
    """
    _check_format(format)

    start, end, step = time_range(params)

    rows, warnings = await power_export_rows(start, end, step)
    status = "partial" if warnings else "success"

    if format == "json":
        return PowerExportResponse(
            status=status,
            data=PowerExportData(rows=[PowerExportRow(**r) for r in rows]),
            warnings=warnings,
        )

    csv_body = rows_to_csv(rows, fieldnames=["timestamp", "node", "layer", "watts"])
    filename = f"power_{_safe_filename_part(start)}_{_safe_filename_part(end)}.csv"
    return Response(
        content=csv_body,
        media_type="text/csv; charset=utf-8",
        headers={"Content-Disposition": f'attachment; filename="{filename}"'},
    )


@router.get("/export/metrics", summary="메트릭 데이터 내보내기")
async def export_metrics(
    request: Request,
    format: str = Query("csv", description="내려받을 형식 (`csv` | `json`, 기본 `csv`)"),
    metric: Optional[str] = Query(None, description="메트릭 이름, 예 `DCGM_FI_DEV_POWER_USAGE`, 허용된 이름만 가능 (필수)"),
    params: TimeseriesParams = Depends(),
):
    """허용된 메트릭의 기간 내 값을 CSV 또는 JSON 으로 내보내기

    입력 예시

    * `GET /api/v2/export/metrics?metric=DCGM_FI_DEV_POWER_USAGE`
    * `GET /api/v2/export/metrics?metric=DCGM_FI_DEV_POWER_USAGE&format=json&period=24h&step=5m`

    입력 옵션

    * `metric`: 메트릭 이름, 예 `DCGM_FI_DEV_POWER_USAGE`, 허용된 이름만 가능 (필수)
    * `format`: `csv` | `json` (기본 `csv`)
    * `period`: 조회 기간, 예 `30m`, `1h`, `7d` (기본 `1h`, 최대 10일)
    * `start`: 시작 시각, ISO 8601 형식 (선택, 기본 현재 시각에서 `period` 만큼 이전)
    * `end`: 종료 시각, ISO 8601 형식 (선택, 기본 현재 시각)
    * `step`: 데이터 점 간격, 예 `1m`, `5m`, `1h` (기본 `5m`)

    응답

    * 행마다 `timestamp` (측정 시각, ISO 8601 UTC), `labels` (원본 메트릭 라벨), `value` (값)
    * `format=csv`: `metrics_메트릭_시작_종료.csv` 첨부 파일로 내려받기, `labels` 는 `키=값;키=값` 문자열
    * `format=json`: `data.metric` 과 `data.rows` 배열로 반환

    경고

    * 데이터가 없는 경우 등 내보내기 경고가 `warnings` 에 담기고 `status` 는 `partial`

    오류

    * 400: 허용 목록에 없는 `metric` (`INVALID_METRIC`)
    * 400: `csv`, `json` 이외의 `format` (`UNSUPPORTED_FORMAT`)
    """
    _check_format(format)

    if metric is None or metric not in EXPORT_METRIC_ALLOWLIST:
        raise HTTPException(
            status_code=400,
            detail=f"INVALID_METRIC: 허용 목록 {list(EXPORT_METRIC_ALLOWLIST.keys())} 중에서 선택하세요.",
        )

    start, end, step = time_range(params)

    rows, warnings = await metric_export_rows(EXPORT_METRIC_ALLOWLIST[metric], start, end, step)
    status = "partial" if warnings else "success"

    if format == "json":
        return MetricExportResponse(
            status=status,
            data=MetricExportData(metric=metric, rows=[MetricExportRow(**r) for r in rows]),
            warnings=warnings,
        )

    csv_body = rows_to_csv(rows, fieldnames=["timestamp", "labels", "value"])
    filename = f"metrics_{metric}_{_safe_filename_part(start)}_{_safe_filename_part(end)}.csv"
    return Response(
        content=csv_body,
        media_type="text/csv; charset=utf-8",
        headers={"Content-Disposition": f'attachment; filename="{filename}"'},
    )


@router.get("/export/report", summary="운영 요약 리포트 생성", response_model=ReportExportResponse)
async def export_report(
    request: Request,
    report_type: str = Query("daily", pattern="^(daily|weekly|monthly)$", description="리포트 주기 (`daily` | `weekly` | `monthly`, 기본 `daily`)"),
):
    """일간, 주간, 월간 운영 요약 리포트 생성

    입력 예시

    * `GET /api/v2/export/report`
    * `GET /api/v2/export/report?report_type=weekly`

    입력 옵션

    * `report_type`: `daily` | `weekly` | `monthly` (기본 `daily`)

    응답

    * `report_type`: 요청한 리포트 주기
    * `status`: 항상 `partial`
    * `data`: 항상 `null`

    경고

    * `NOT_CONFIGURED`: 리포트 생성 기능을 아직 설정하지 않음
    """
    return ReportExportResponse(
        status="partial",
        data=None,
        report_type=report_type,
        warnings=["NOT_CONFIGURED"],
    )
