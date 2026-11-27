#!/usr/bin/env bash
# ============================================================
# check-kernel-headers.sh: 커널 헤더 존재 확인 (initContainer용)
# 상세: DKMS 빌드에 필요한 커널 헤더가 없으면 자동 설치
#       tenstorrent tt-kmd DKMS 빌드 전에 실행됨
# 생성일: 2026-04-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

KVER=$(ns "uname -r")
echo "[initContainer] Checking kernel headers for ${KVER}..."

if ! ns "test -d /usr/src/linux-headers-${KVER}"; then
  echo "[initContainer] Installing linux-headers-${KVER}..."
  ns "DEBIAN_FRONTEND=noninteractive apt-get install -y linux-headers-${KVER}" || \
    ns "DEBIAN_FRONTEND=noninteractive apt-get install -y linux-headers-generic"
fi

echo "[initContainer] Kernel headers OK for ${KVER}"
