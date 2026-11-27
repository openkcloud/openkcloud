#!/usr/bin/env bash
# ============================================================
# driver-manager.sh: initContainer에서 실행 — Tenstorrent tt-kmd 커널 모듈 해제
# 상세: driver-manager initContainer 패턴 (Tenstorrent Blackhole 전용)
#       버전 변경 감지 시 tenstorrent 모듈 rmmod 후 재설치 경로 열어줌
# 생성일: 2026-04-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

log_info()  { echo "[INFO] $*"; }
log_warn()  { echo "[WARN] $*"; }
log_error() { echo "[ERR]  $*"; }

MARKER_DIR="${MARKER_DIR:-/var/lib/kcloud-operator}"
MAX_RETRIES=10
RETRY_INTERVAL=3

ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

# 마커 디렉터리 개명 이관(npu-operator → kcloud-operator). 1회 수행되고 멱등이다.
# 아래 어떤 MARKER_DIR 사용보다 먼저 와야 한다.
# shellcheck source=/dev/null
. /usr/local/bin/marker-migrate.sh
migrate_marker_dir "${MARKER_DIR}"

# driver.ready 마커 삭제 (재설치 경로 클리어)
log_info "이전 driver.ready 마커 삭제"
rm -f "${MARKER_DIR}/driver.ready" /tmp/driver-ready 2>/dev/null || true

DRIVER_VERSION="${DRIVER_VERSION:-}"
if [[ -z "$DRIVER_VERSION" ]]; then
  log_info "DRIVER_VERSION 미지정 — 모듈 해제 불필요, 통과"
  exit 0
fi

# 현재 로드된 tenstorrent 모듈 버전 확인
if ! ns "lsmod 2>/dev/null | grep -q '^tenstorrent'" 2>/dev/null; then
  log_info "tenstorrent 모듈 미로드 상태 — 해제 불필요"
  exit 0
fi

# 현재 버전 읽기 (dkms 기반)
CURRENT_VER=""
CURRENT_VER=$(ns "dkms status tenstorrent 2>/dev/null | grep installed | awk -F',' '{print \$2}' | tr -d ' '" 2>/dev/null || true)

if [[ -z "$CURRENT_VER" ]]; then
  log_info "tenstorrent DKMS 버전 정보 없음 — 해제 불필요"
  exit 0
fi

if [[ "$CURRENT_VER" == "$DRIVER_VERSION" ]]; then
  log_info "동일 버전 (${CURRENT_VER}) — 모듈 해제 불필요"
  exit 0
fi

log_info "드라이버 버전 변경 감지: ${CURRENT_VER} → ${DRIVER_VERSION}"
log_info "tenstorrent 커널 모듈 해제 시작"

for i in $(seq 1 $MAX_RETRIES); do
  log_info "rmmod 시도 ${i}/${MAX_RETRIES}"
  ns "rmmod tenstorrent 2>/dev/null || true"

  if ! ns "lsmod | grep -q '^tenstorrent '"; then
    log_info "tenstorrent 커널 모듈 해제 성공 (시도 ${i})"
    exit 0
  fi

  REFS=$(ns "cat /proc/modules 2>/dev/null | grep '^tenstorrent ' | awk '{print \$3}'" || echo "?")
  log_warn "tenstorrent 모듈 참조 카운트: ${REFS}, ${RETRY_INTERVAL}초 후 재시도"
  sleep $RETRY_INTERVAL
done

log_error "tenstorrent 모듈 해제 실패 (${MAX_RETRIES}회 시도)"

REBOOT_STRATEGY="${REBOOT_STRATEGY:-IfNeeded}"
if [[ "$REBOOT_STRATEGY" == "Require" ]]; then
  log_info "rebootStrategy=Require: needs-reboot 마커 생성"
  touch "${MARKER_DIR}/needs-reboot"
  exit 0
fi

if [[ "$REBOOT_STRATEGY" == "Never" ]]; then
  log_error "rebootStrategy=Never: 모듈 해제 실패, 설치 중단"
  exit 1
fi

log_warn "rebootStrategy=IfNeeded: 모듈 해제 실패, main container에서 재시도"
exit 0
