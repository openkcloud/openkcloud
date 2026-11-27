#!/usr/bin/env bash
# ============================================================
# driver-manager.sh: initContainer에서 실행 — 기존 커널 모듈 완전 해제
# 상세: NVIDIA GPU Operator 의 k8s-driver-manager 패턴을 참조한다.
# 생성일: 2026-04-15 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

log_info()  { echo "[INFO] $*"; }
log_warn()  { echo "[WARN] $*"; }
log_error() { echo "[ERR]  $*"; }

MARKER_DIR="${MARKER_DIR:-/var/lib/kcloud-operator}"
MAX_RETRIES=10
RETRY_INTERVAL=3
# 0=진단만, 1=불일치 major 의 host nvidia-* apt 패키지 자동 remove --purge (위험)
FORCE_APT_PURGE="${FORCE_APT_PURGE:-0}"
# IfNeeded(default) | Strict (rmmod 실패 시 exit 42) | Require | Never
REBOOT_STRATEGY="${REBOOT_STRATEGY:-IfNeeded}"

ns() { nsenter --target 1 --mount --uts --ipc --net -- bash -c "$1"; }

# 마커 디렉터리 개명 이관(npu-operator → kcloud-operator). 1회 수행되고 멱등이다.
# 아래 어떤 MARKER_DIR 사용보다 먼저 와야 한다.
# shellcheck source=/dev/null
. /usr/local/bin/marker-migrate.sh
migrate_marker_dir "${MARKER_DIR}"

# ============================================================================ #
# 안전장치 동작 env (기본값 = 기존 동작 보존, 하위호환)
#   SKIP_ON_PASSTHROUGH (기본 true) : (a) GPU 전량 vfio-pci 바인딩 시 rmmod/purge 미수행
#   ALLOW_DOWNGRADE     (기본 false): (b) 기존이 desired 보다 최신이면 rmmod 안 함
#   VERSION_SOURCE      (기본 Policy): (c) Host=호스트 설치 버전 존중, 모듈 해제 안 함
# ============================================================================ #
SKIP_ON_PASSTHROUGH="${SKIP_ON_PASSTHROUGH:-true}"
ALLOW_DOWNGRADE="${ALLOW_DOWNGRADE:-false}"
VERSION_SOURCE="${VERSION_SOURCE:-Policy}"

# ============================================================================ #
# 안전장치 헬퍼: passthrough 감지 · 버전 비교 (entrypoint.sh 와 동일 로직)
# ============================================================================ #

# 호스트 PCI 를 순회하며 NVIDIA GPU 의 "<total> <installable>" 개수를 출력.
#   installable = driver 바인딩이 vfio-pci 가 아닌(=nvidia/미바인딩) GPU 개수
nvidia_gpu_bindings() {
  ns 'total=0; installable=0
    for d in /sys/bus/pci/devices/*/; do
      [ -r "${d}vendor" ] || continue
      vendor=$(cat "${d}vendor" 2>/dev/null || echo "")
      class=$(cat "${d}class" 2>/dev/null || echo "")
      [ "$vendor" = "0x10de" ] || continue
      case "$class" in 0x03*) ;; *) continue ;; esac
      total=$((total+1))
      bound=""
      if [ -L "${d}driver" ]; then
        bound=$(basename "$(readlink "${d}driver")" 2>/dev/null || echo "")
      fi
      [ "$bound" != "vfio-pci" ] && installable=$((installable+1))
    done
    echo "$total $installable"'
}

# 반환 0(true): installable GPU 있음 또는 비-GPU 노드. 1(false): 전량 vfio-pci(passthrough)
nvidia_has_installable_gpu() {
  local out total installable
  out=$(nvidia_gpu_bindings 2>/dev/null | tail -n1 || echo "0 0")
  total=$(echo "$out" | awk '{print $1+0}')
  installable=$(echo "$out" | awk '{print $2+0}')
  [ "${total:-0}" -eq 0 ] && return 0
  [ "${installable:-0}" -gt 0 ] && return 0
  return 1
}

# 버전 비교: $1 > $2 이면 0(true). dpkg 판정 불가 시 major 정수 비교 fallback.
ver_gt() {
  local a="$1" b="$2" am bm
  if ns "dpkg --compare-versions '${a}' gt '${b}'" >/dev/null 2>&1; then
    return 0
  elif ns "dpkg --compare-versions '${a}' le '${b}'" >/dev/null 2>&1; then
    return 1
  fi
  am="${a%%.*}"; bm="${b%%.*}"
  if [[ "$am" =~ ^[0-9]+$ && "$bm" =~ ^[0-9]+$ ]] && [[ "$am" -gt "$bm" ]]; then
    return 0
  fi
  return 1
}

# driver.ready 마커 삭제
log_info "이전 driver.ready 마커 삭제"
rm -f "${MARKER_DIR}/driver.ready" /tmp/driver-ready 2>/dev/null || true

# --- PASSTHROUGH GUARD (a) ---
# 전량 vfio-pci(passthrough) 바인딩 노드는 rmmod/purge 를 일절 하지 않고 통과(passthrough 보호).
if [[ "${SKIP_ON_PASSTHROUGH:-true}" != "false" ]] && ! nvidia_has_installable_gpu; then
  log_info "All NVIDIA GPUs bound to vfio-pci (passthrough) — skipping module unload (passthrough protected)"
  exit 0
fi

DRIVER_VERSION="${DRIVER_VERSION:-}"
if [[ -z "$DRIVER_VERSION" ]]; then
  log_info "DRIVER_VERSION 미지정 — 모듈 해제 불필요, 통과"
  exit 0
fi

# ============================================================================ #
# host 의 desired major 와 다른 nvidia-* apt 패키지 / DKMS 잔존 검사 (H4 차단)
#   - default: 진단 + 경고만 (운영자가 수동 처리)
#   - FORCE_APT_PURGE=1: 자동 apt remove --purge (위험 — host 시스템 변경)
#   - DKMS 잔존 모듈은 항상 진단 출력
# ============================================================================ #
DESIRED_MAJOR="${DRIVER_VERSION%%.*}"
log_info "host 의 잔존 nvidia-* apt 패키지 검사 (desired major=${DESIRED_MAJOR})"
CONFLICTS=$(ns "dpkg -l 2>/dev/null | grep -E '^ii\\s+nvidia-(driver|dkms|kernel-source)-[0-9]+\\s'" 2>/dev/null || true)
if [[ -n "$CONFLICTS" ]]; then
  while IFS= read -r LINE; do
    [[ -z "$LINE" ]] && continue
    PKG=$(echo "$LINE" | awk '{print $2}')
    VER=$(echo "$LINE" | awk '{print $3}')
    PKG_MAJOR=$(echo "$PKG" | grep -oE '[0-9]+$' || echo "")
    if [[ -n "$PKG_MAJOR" && "$PKG_MAJOR" != "$DESIRED_MAJOR" ]]; then
      log_warn "잔존 apt 패키지 감지: $PKG ($VER) — desired major $DESIRED_MAJOR 와 충돌 가능"
      if [[ "${FORCE_APT_PURGE:-0}" == "1" ]]; then
        log_info "FORCE_APT_PURGE=1 → $PKG 제거 시도"
        ns "DEBIAN_FRONTEND=noninteractive apt-get remove --purge -y $PKG" || log_error "$PKG 제거 실패"
      else
        log_warn "  조치 (수동): ssh <user>@<node> 후 sudo apt-get remove --purge -y $PKG"
      fi
    fi
  done <<<"$CONFLICTS"
else
  log_info "잔존 nvidia-* apt 패키지 없음"
fi

# DKMS 잔존 모듈 검사 (host kernel module pool 충돌 원인)
DKMS_NVIDIA=$(ns "ls /lib/modules/\$(uname -r)/updates/dkms/ 2>/dev/null | grep -i nvidia" 2>/dev/null | head -5 || true)
if [[ -n "$DKMS_NVIDIA" ]]; then
  log_warn "DKMS nvidia 모듈 잔존: $(echo "$DKMS_NVIDIA" | tr '\n' ' ') — host kernel module pool 충돌 가능"
  log_warn "  조치 (수동): ssh <user>@<node> 후 sudo dkms status | grep nvidia; sudo dkms remove nvidia/<ver> --all"
fi

CURRENT_VER=""
if ns "test -f /proc/driver/nvidia/version" 2>/dev/null; then
  CURRENT_VER=$(ns "cat /proc/driver/nvidia/version 2>/dev/null" | grep -oP 'NVRM version: \K[0-9]+\.[0-9]+\.[0-9]+' || true)
fi

if [[ -z "$CURRENT_VER" ]]; then
  log_info "nvidia 모듈 미로드 상태 — 해제 불필요"
  exit 0
fi

# --- HOST VERSION RESPECT (c) ---
# VERSION_SOURCE=Host 이고 호스트에 드라이버가 로드돼 있으면 그 버전을 존중하고 모듈 해제를 하지 않는다.
if [[ "${VERSION_SOURCE:-Policy}" == "Host" ]]; then
  log_info "VERSION_SOURCE=Host — 기존 host 드라이버 ${CURRENT_VER} 존중, 모듈 해제 안 함"
  exit 0
fi

CURRENT_MAJOR="${CURRENT_VER%%.*}"
DESIRED_MAJOR="${DRIVER_VERSION%%.*}"

# --- DOWNGRADE GUARD (b) ---
# 기존 로드 버전이 desired 보다 최신이면 다운그레이드 방지를 위해 모듈 해제를 하지 않는다.
if ver_gt "$CURRENT_VER" "$DRIVER_VERSION" && [[ "${ALLOW_DOWNGRADE:-false}" != "true" ]]; then
  log_info "기존 드라이버 ${CURRENT_VER} > desired ${DRIVER_VERSION}; 다운그레이드 비활성(ALLOW_DOWNGRADE=false) — 모듈 해제 안 함"
  exit 0
fi

if [[ "$CURRENT_MAJOR" == "$DESIRED_MAJOR" ]]; then
  log_info "동일 메이저 버전 (${CURRENT_MAJOR}) — 모듈 해제 불필요"
  exit 0
fi

log_info "드라이버 버전 변경 감지: ${CURRENT_VER} → ${DRIVER_VERSION}"
log_info "nvidia 커널 모듈 해제 시작"

log_info "GPU 관련 system service 중지 (적극)"
ns "systemctl stop nvidia-persistenced 2>/dev/null || true"
ns "systemctl stop dcgm-exporter 2>/dev/null || true"
ns "systemctl stop nvidia-fabricmanager 2>/dev/null || true"

log_info "GPU device fd holder SIGKILL"
ns "fuser -k -SIGKILL /dev/nvidia* 2>&1" || true
sleep 2

REFS="?"
for i in $(seq 1 $MAX_RETRIES); do
  log_info "rmmod 시도 ${i}/${MAX_RETRIES}"
  ns "rmmod nvidia_uvm 2>/dev/null || true"
  ns "rmmod nvidia_drm 2>/dev/null || true"
  ns "rmmod nvidia_modeset 2>/dev/null || true"
  ns "rmmod nvidia 2>/dev/null || true"

  if ! ns "lsmod | grep -q '^nvidia '"; then
    log_info "nvidia 커널 모듈 해제 성공 (시도 ${i})"
    exit 0
  fi

  REFS=$(ns "cat /proc/modules | grep '^nvidia ' | awk '{print \$3}'" || echo "?")
  PROCS=$(ns "fuser -v /dev/nvidia* 2>&1" 2>/dev/null | tail -10 || echo "(none)")
  log_warn "nvidia 모듈 참조 카운트: ${REFS}, ${RETRY_INTERVAL}초 후 재시도"
  log_warn "GPU device 사용 process:"
  echo "$PROCS" | sed 's/^/  /' >&2
  sleep $RETRY_INTERVAL
done

log_error "nvidia 모듈 해제 실패 (${MAX_RETRIES}회 시도)"
log_error "최종 모듈 참조 카운트: ${REFS}"
log_error "노드 재부팅이 필요할 수 있습니다"

case "$REBOOT_STRATEGY" in
  Strict)
    log_error "rebootStrategy=Strict: 모듈 해제 실패 시 즉시 종료 (init container fail)"
    log_error "조치: ssh <user>@\${NODE_NAME:-<node>} 후 sudo systemctl stop dcgm-exporter; sudo rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia; 또는 노드 reboot"
    exit 42
    ;;
  Require)
    log_info "rebootStrategy=Require: needs-reboot 마커 생성"
    touch "${MARKER_DIR}/needs-reboot"
    exit 0
    ;;
  Never)
    log_error "rebootStrategy=Never: 모듈 해제 실패, 설치 중단"
    exit 1
    ;;
  IfNeeded|*)
    log_warn "rebootStrategy=IfNeeded: 모듈 해제 실패, main container에서 재시도"
    log_warn "⚠ host 잔존 module 과 신규 install 충돌 가능 — Pod ready=false 영원 stuck 위험"
    log_warn "⚠ 회귀 테스트 시에는 REBOOT_STRATEGY=Strict 권장 (init container 즉시 fail 로 운영자 인지)"
    exit 0
    ;;
esac
