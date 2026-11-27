#!/usr/bin/env bash
# ============================================================
# healthcheck.sh: NVIDIA 드라이버 상태 확인 (livenessProbe용)
# 상세: nvidia-smi 응답 및 /proc/driver/nvidia/version 존재 여부 검사
# 생성일: 2026-04-13 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

# /proc/driver/nvidia/version 파일 확인
if ! ns "test -f /proc/driver/nvidia/version" 2>/dev/null; then
  echo "[healthcheck] FAIL: /proc/driver/nvidia/version 없음 — 커널 모듈 미로드" >&2
  exit 1
fi

# nvidia-smi 응답 확인
if ! ns "nvidia-smi -L >/dev/null 2>&1"; then
  echo "[healthcheck] FAIL: nvidia-smi 응답 없음 — GPU 접근 불가" >&2
  exit 1
fi

echo "[healthcheck] OK: NVIDIA 드라이버 정상"
exit 0
