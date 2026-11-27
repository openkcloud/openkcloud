#!/usr/bin/env bash
# ============================================================
# prebuilt-coherent_test.sh: nvidia_prebuilt_coherent 가 동봉==정책만 판정하는지 시험
# 상세: 빌드 시점 가드가 이미지 안에서 모듈·사용자 공간 일치를 이미 보장해 함수는 두 버전
#       문자열만 비교한다 — docker·컨테이너 없이 순수 bash 로 돈다.
# 생성일: 2026-09-27 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"

# 함수 정의만 로드(부작용 없음). 가드가 실행부(marker 이관·설치·트랩) 앞에서 멈춘다.
# 복사본이 아니라 source 된 실제 본문을 부른다.
# shellcheck disable=SC2034  # source 된 entrypoint.sh 가 읽는다(이 파일에선 미참조)
ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint.sh"

# 호출은 $() 로 감싼다 — set -u 의 unbound 오류로 셸이 죽지 않고 FAIL 을 깔끔히 낸다.
# 1) 동봉 == 정책 → 0.
if ! out="$(nvidia_prebuilt_coherent 580.126.09 580.126.09 2>&1)"; then
  echo "FAIL 1: 동봉==정책 인데 0 이 아니다: $out"; exit 1
fi

# 2) 동봉 != 정책 → 1 + 로그에 두 값.
if out="$(nvidia_prebuilt_coherent 580.126.09 580.159.03 2>&1)"; then
  echo "FAIL 2: 동봉!=정책 인데 거부하지 않았다: $out"; exit 1
fi
[[ "$out" == *580.126.09* && "$out" == *580.159.03* ]] \
  || { echo "FAIL 2: 로그에 두 값이 없다: $out"; exit 1; }

# 3) 정책이 빈 문자열 → 0 (정책이 비면 그 비교를 건너뛴다).
if ! out="$(nvidia_prebuilt_coherent 580.126.09 "" 2>&1)"; then
  echo "FAIL 3: 정책이 비었는데 0 이 아니다: $out"; exit 1
fi

# 4) 동봉 모듈 버전을 읽지 못함(빈 문자열) → 1.
if out="$(nvidia_prebuilt_coherent "" 580.126.09 2>&1)"; then
  echo "FAIL 4: 동봉 버전이 비었는데 거부하지 않았다: $out"; exit 1
fi

echo "PASS: all cases"
