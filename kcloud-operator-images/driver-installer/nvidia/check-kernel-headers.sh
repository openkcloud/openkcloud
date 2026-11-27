#!/usr/bin/env bash
# ============================================================
# check-kernel-headers.sh: 커널 헤더 존재 확인 (initContainer용)
# 상세: DKMS 빌드 전 linux-headers 설치 여부를 확인하고 없으면 설치
# 생성일: 2026-04-13 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

KVER=$(ns "uname -r")
echo "[initContainer] 커널 버전: ${KVER}"

if ns "test -d /usr/src/linux-headers-${KVER}"; then
  echo "[initContainer] 커널 헤더 OK: /usr/src/linux-headers-${KVER}"
  exit 0
fi

echo "[initContainer] linux-headers-${KVER} 없음 — 설치 시도..."
if ns "DEBIAN_FRONTEND=noninteractive apt-get install -y linux-headers-${KVER}"; then
  echo "[initContainer] linux-headers-${KVER} 설치 완료"
else
  echo "[initContainer] linux-headers-${KVER} 실패 — linux-headers-generic 시도..." >&2
  ns "DEBIAN_FRONTEND=noninteractive apt-get install -y linux-headers-generic" || {
    echo "[initContainer] FAIL: 커널 헤더 설치 불가 — DKMS 빌드 불가능" >&2
    exit 1
  }
fi

echo "[initContainer] 커널 헤더 준비 완료"
exit 0
