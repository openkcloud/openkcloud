#!/usr/bin/env bash
# ============================================================
# prebuilt-ko_test.sh: entrypoint.sh 의 select_prebuilt_dir 가 variant·커널 일치 디렉터리만 고르는지 시험
# 상세: 가짜 /opt/furiosa-ko/<variant>/<kernel>/ 트리에 빈 .ko 와 vermagic 파일을 두고
#       함수 정의만 source(ENTRYPOINT_LIB_ONLY=1)해 호출한다. 일치 variant+kernel 이면
#       디렉터리 경로를 출력하고 exit 0, variant 나 kernel 이 다르면 exit 비0 을 확인한다.
#       컨테이너 실행 없이 도는 순수 bash 시험이다.
# 생성일: 2026-09-23
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

# 가짜 prebuilt 트리: rngd 커널 하나만 동봉된 상태를 흉내낸다.
mkdir -p "$tmp/ko/rngd/6.8.0-138-generic"
: > "$tmp/ko/rngd/6.8.0-138-generic/furiosa_rngd.ko"
echo "6.8.0-138-generic SMP preempt mod_unload modversions" > "$tmp/ko/rngd/6.8.0-138-generic/vermagic"

# 함수 정의만 로드(부작용 없음). PREBUILT_ROOT 로 가짜 트리를 가리킨다.
# source 는 현재 셸에서 돌므로 일반 변수 할당으로 넘긴다(명령 prefix 는 특수 빌트인에서
# 유지가 셸마다 달라 set -u 에서 unbound 가 날 수 있다).
PREBUILT_ROOT="$tmp/ko"
ENTRYPOINT_LIB_ONLY=1
export PREBUILT_ROOT ENTRYPOINT_LIB_ONLY
# shellcheck source=entrypoint.sh
source "$here/entrypoint.sh"

# 1) 일치 variant+kernel: 디렉터리 경로를 출력하고 exit 0.
d="$(select_prebuilt_dir rngd 6.8.0-138-generic)"
[ -f "$d/furiosa_rngd.ko" ] || { echo "FAIL match: $d"; exit 1; }

# 2) 다른 variant: 아무것도 출력하지 않고 exit 비0.
if select_prebuilt_dir warboy 6.8.0-138-generic >/dev/null 2>&1; then echo "FAIL variant"; exit 1; fi

# 3) 다른 kernel: 아무것도 출력하지 않고 exit 비0.
if select_prebuilt_dir rngd 5.15.0-171-generic >/dev/null 2>&1; then echo "FAIL kernel"; exit 1; fi

# --- 버전 결합 순수 헬퍼: 이미지 태그의 패키지 버전 접두 == 정책 DRIVER_VERSION 여부 ---

# 4) 태그에서 <variant>-k 앞 패키지 버전 접두 추출.
v="$(furiosa_pkg_version_from_tag 2026.3.1-rngd-k6.8.0-138-generic-v1 rngd)"
[ "$v" = "2026.3.1" ] || { echo "FAIL prefix: $v"; exit 1; }

# 5) 태그 접두 == DRIVER_VERSION 이면 0.
furiosa_prebuilt_version_ok 2026.3.1-rngd-k6.8.0-138-generic-v1 rngd 2026.3.1 >/dev/null 2>&1 \
  || { echo "FAIL bind ok"; exit 1; }

# 6) 정책 2026.3.0 인데 이미지 2026.3.1 이면 비0 + 두 값 로그(조용한 모듈 덮어쓰기 차단).
if out="$(furiosa_prebuilt_version_ok 2026.3.1-rngd-k6.8.0-138-generic-v1 rngd 2026.3.0 2>&1)"; then
  echo "FAIL bind mismatch should refuse: $out"; exit 1
fi
[[ "$out" == *2026.3.1* && "$out" == *2026.3.0* ]] || { echo "FAIL bind log missing values: $out"; exit 1; }

# 7) DRIVER_VERSION 미지정(정책 버전 없음)이면 결합 대상 아님 → 0.
furiosa_prebuilt_version_ok 2026.3.1-rngd-k6.8.0-138-generic-v1 rngd "" >/dev/null 2>&1 \
  || { echo "FAIL bind unset"; exit 1; }

echo "PASS 7/7"
