#!/usr/bin/env bash
# ============================================================
# healthcheck-v17.sh: NVIDIA 드라이버 v17 healthcheck (container-local)
# 상세: nsenter 미사용. 컨테이너 안에서 nvidia-smi + /proc/driver/nvidia/version 검사.
#       /dev/nvidia* 가 컨테이너에 마운트되어 있다는 가정 (privileged DS).
# 생성일: 2026-04-27 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

# /proc/driver/nvidia/version 확인 — container 의 /proc 은 host kernel 과 공유
if [[ ! -f "/proc/driver/nvidia/version" ]]; then
  echo "[healthcheck-v17] FAIL: /proc/driver/nvidia/version 없음 — 커널 모듈 미로드" >&2
  exit 1
fi

# v17.3 — NVRM 파싱 (X.Y / X.Y.Z / X.Y.Z.W 모두 흡수, whitespace 변동 robust)
parse_nvrm() {
  local src="$1"
  local v=""
  v=$(grep -oP 'NVRM version:\s+NVIDIA UNIX[^/]*Kernel Module\s+\K[0-9]+(\.[0-9]+)+' "${src}" 2>/dev/null \
      | head -n1 || true)
  if [[ -z "${v}" ]]; then
    v=$(grep -oP 'NVRM version:[^0-9]*\K[0-9]+(\.[0-9]+)+' "${src}" 2>/dev/null \
        | head -n1 || true)
  fi
  echo "${v}"
}

# DRIVER_VERSION 이 있으면 메이저 매칭 검증
if [[ -n "${DRIVER_VERSION:-}" ]]; then
  expected_major="${DRIVER_VERSION%%.*}"
  loaded="$(parse_nvrm /proc/driver/nvidia/version)"
  if [[ -z "${loaded}" ]]; then
    echo "[healthcheck-v17] FAIL: NVRM version 파싱 실패 — /proc/driver/nvidia/version 출력:" >&2
    sed -n '1,5p' /proc/driver/nvidia/version 2>/dev/null | sed 's/^/  | /' >&2 || true
    exit 1
  fi
  loaded_major="${loaded%%.*}"
  if [[ "${loaded_major}" != "${expected_major}" ]]; then
    echo "[healthcheck-v17] FAIL: version mismatch loaded=${loaded} expected major=${expected_major}" >&2
    exit 1
  fi
fi

# nvidia-smi 응답 확인 (container 안에서 직접 — /dev/nvidia* 마운트 가정)
if command -v nvidia-smi >/dev/null 2>&1; then
  if ! nvidia-smi -L >/dev/null 2>&1; then
    echo "[healthcheck-v17] FAIL: nvidia-smi -L 응답 없음 — /dev/nvidia* 마운트 확인" >&2
    exit 1
  fi
else
  echo "[healthcheck-v17] WARN: nvidia-smi 미존재 — proc 검사만 통과" >&2
fi

echo "[healthcheck-v17] OK: NVIDIA 드라이버 정상 (container-local 검사)"
exit 0
