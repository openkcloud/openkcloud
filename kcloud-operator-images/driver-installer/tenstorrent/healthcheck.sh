#!/bin/sh
# ============================================================
# healthcheck.sh: Tenstorrent tt-kmd 드라이버 헬스체크 (livenessProbe용)
# 상세: 커널 모듈 로드 확인 + /dev/tenstorrent/* 디바이스 존재 확인
# 생성일: 2026-04-22 | 수정일: 2026-10-11
# ============================================================
nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "
  lsmod | grep -q '^tenstorrent' || exit 1
  ls /dev/tenstorrent/ >/dev/null 2>&1 || exit 1
"
