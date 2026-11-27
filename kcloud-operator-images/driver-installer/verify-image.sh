#!/usr/bin/env bash
# ============================================================
# verify-image.sh: driver-ds image 의 driver version label 검증 (R5 차단)
# 상세: docker pull 후 image LABEL "ai.npu.driver.full" 가 기대값과 일치하는지 확인한다.
#       레이블이 없는 legacy image 는 경고만 내고 통과시킨다. 사용법은 usage() 참조.
# 생성일: 2026-04-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

usage() {
  cat <<EOF
사용: $0 <vendor> <tag> [expected-version]
  vendor   : nvidia | furiosa | tenstorrent
  tag      : image tag (예: 590.48.01-v17). expected 미지정 시 -vN 접미사 제거하여 사용.
  expected : 검증 기대 버전 (선택)
환경변수:
  REGISTRY : 기본 ghcr.io/openkcloud
종료 코드:
  0  - 일치 (또는 legacy graceful warning)
  1  - 불일치 (FAIL)
  2  - 인자 오류
  3  - docker / image 접근 실패
EOF
}

if [[ $# -lt 2 || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 2
fi

VENDOR="$1"
TAG="$2"
EXPECTED="${3:-${TAG%-v*}}"   # tag 끝의 -vN 접미사 제거. v 없으면 TAG 그대로.
REGISTRY="${REGISTRY:-ghcr.io/openkcloud}"
IMAGE="${REGISTRY}/${VENDOR}-driver-ds:${TAG}"

case "$VENDOR" in
  nvidia|furiosa|tenstorrent) ;;
  *)
    echo "[ERR ] unknown vendor: $VENDOR (expected: nvidia | furiosa | tenstorrent)" >&2
    exit 2
    ;;
esac

if ! command -v docker >/dev/null 2>&1; then
  echo "[ERR ] docker CLI 미설치" >&2
  exit 3
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "[ERR ] jq 미설치 (apt install jq)" >&2
  exit 3
fi

echo "[INFO] image=${IMAGE} expected=${EXPECTED}"

if ! docker pull "$IMAGE" >/dev/null 2>&1; then
  echo "[ERR ] docker pull 실패: ${IMAGE}" >&2
  exit 3
fi

LABEL=$(docker inspect "$IMAGE" 2>/dev/null \
  | jq -r '.[0].Config.Labels."ai.npu.driver.full" // empty')

if [[ -z "$LABEL" ]]; then
  echo "[WARN] image '${IMAGE}' 에 ai.npu.driver.full LABEL 없음 (legacy image 가능)"
  echo "[WARN] 새 빌드는 'docker build --build-arg <VENDOR>_DRIVER_VERSION=${EXPECTED} ...' 사용 권장"
  exit 0
fi

if [[ "$LABEL" != "$EXPECTED" ]]; then
  echo "[FAIL] image label '${LABEL}' ≠ expected '${EXPECTED}'" >&2
  exit 1
fi

echo "[OK  ] image ${IMAGE} 의 ai.npu.driver.full=${LABEL} (일치)"
exit 0
