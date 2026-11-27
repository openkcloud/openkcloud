#!/bin/sh
# ============================================================
# healthcheck.sh: Furiosa NPU(Warboy·RNGD 공용) 드라이버 헬스체크 (livenessProbe용)
# 상세: 모델별 장치 경로·커널 모듈이 달라 FURIOSA_MODEL 로 분기한다. 미지정 시 둘 중 하나라도
#       살아 있으면 정상으로 본다(한 노드에 한 모델만 있다는 전제).
# 생성일: 2026-09-09
# ============================================================
case "${FURIOSA_MODEL:-}" in
  warboy) CHECK='ls /dev/npu* >/dev/null 2>&1 && lsmod | grep -q npu_pdma' ;;
  rngd)   CHECK='test -c /dev/rngd/npu0mgmt && lsmod | grep -q furiosa_rngd' ;;
  *)      CHECK='{ ls /dev/npu* >/dev/null 2>&1 && lsmod | grep -q npu_pdma; } || { test -c /dev/rngd/npu0mgmt && lsmod | grep -q furiosa_rngd; }' ;;
esac
nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "${CHECK} || exit 1"
