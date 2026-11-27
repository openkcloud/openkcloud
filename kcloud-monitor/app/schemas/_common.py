"""응답 모델 공통 필드의 Swagger 설명 문구.

status, observed_at, warnings 처럼 모든 응답 모델에 반복되는 필드의 description 을
한 곳에서 관리한다. 각 스키마 모듈은 ``Field(description=DESC_STATUS)`` 처럼 가져다 쓴다.
"""

from datetime import datetime, timezone

DESC_STATUS = (
    "처리 결과 (success | partial | error | not_implemented). "
    "partial: 일부 데이터 누락, 원인은 warnings 참고"
)
DESC_OBSERVED_AT = "응답 생성 시각 (ISO 8601, UTC)"
DESC_WARNINGS = (
    "일부 데이터가 비거나 근사치일 때의 원인 코드 목록. "
    "정상이면 빈 목록 (예: NO_DATA, UPSTREAM_ERROR)"
)

DESC_TOTAL = "필터 적용 후 전체 개수. limit, offset 과 무관"
DESC_LIMIT = "페이지 크기"
DESC_OFFSET = "건너뛴 개수"
DESC_HAS_NEXT = "다음 페이지 존재 여부"
DESC_NEXT_CURSOR = "다음 페이지 요청 시 cursor 파라미터로 넘길 값. 마지막 페이지면 null"


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()
