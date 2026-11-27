#!/usr/bin/env bash
# ============================================================
# preflight.sh: NVIDIA 드라이버 설치 전 점검 진입점(operator 의 설치 전 점검 Job 이 실행)
# 상세: entrypoint.sh 함수만 불러(ENTRYPOINT_LIB_ONLY=1) run_preflight 를 실행하고 그 반환을 종료
#       코드로 낸다 — 0 통과, 3 hold 충돌, 그 밖 판정 불가. 패키지를 설치·제거·hold 하지 않는다.
#       이 파일이 없는 옛 이미지는 컨테이너가 시작되지 않고 operator 는 판정 불가로 다룬다.
# 생성일: 2026-10-27 | 수정일: 2026-10-27
# ============================================================
# shellcheck disable=SC2034  # 불러온 entrypoint.sh 의 ENTRYPOINT_LIB_ONLY 가드가 읽는다(이 파일에선 미참조)
ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
. "${ENTRYPOINT_PATH:-/usr/local/bin/entrypoint.sh}"
set +eE
run_preflight
exit $?
