#!/usr/bin/env bash
# ============================================================
# entrypoint.sh: Tenstorrent tt-kmd 드라이버 설치 (DaemonSet + install Job 겸용)
# 상세: 이미지에 baked 된 tt-kmd 소스(air-gap)를 DKMS 로 빌드/설치 → modprobe →
#       RUN_MODE=job 이면 self-check 후 exit 0, daemonset 이면 상시 헬스 루프.
#       노드 커널용 prebuilt .ko 가 이미지에 있으면 DKMS 없이 그 모듈을 설치한다.
#       안전장치: ALLOW_DOWNGRADE / VERSION_SOURCE=Host / idempotent skip.
# 생성일: 2026-04-22 | 수정일: 2026-09-22
# ============================================================
set -euo pipefail
[[ "${DEBUG:-0}" == "1" ]] && set -x

# ----- helpers -----
ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

log_info()  { echo "[INFO]  $*"; }
log_warn()  { echo "[WARN]  $*" >&2; }
log_error() { echo "[ERR]   $*" >&2; }

MARKER_DIR="/var/lib/kcloud-operator"

# 마커 디렉터리 개명 이관(npu-operator → kcloud-operator). 1회 수행되고 멱등이다.
# 아래 어떤 MARKER_DIR 사용보다 먼저 와야 한다.
# shellcheck source=/dev/null
. /usr/local/bin/marker-migrate.sh
migrate_marker_dir "${MARKER_DIR}"

# RUN_MODE: daemonset(기본)=상주 healthcheck 루프, job=설치 후 exit 0.
RUN_MODE="${RUN_MODE:-daemonset}"
# 안전장치 env (기본값 = 기존 동작 보존)
ALLOW_DOWNGRADE="${ALLOW_DOWNGRADE:-false}"
VERSION_SOURCE="${VERSION_SOURCE:-Policy}"

# 설치 대상 버전: 이미지에 baked 된 소스 버전(TT_KMD_VERSION)이 권위값이다.
# DRIVER_VERSION(DIP policy)과 다르면 경고만 하고 baked 버전으로 진행한다
# (이미지↔버전은 1:1 로 빌드되므로 tag 와 version 을 함께 bump 하는 것이 정상 운영).
TARGET_VERSION="${TT_KMD_VERSION:-}"
DRIVER_VERSION="${DRIVER_VERSION:-}"

require_host_ns() {
  if [[ ! -e /proc/1/ns/mnt ]]; then
    log_error "Need --pid=host & privileged to nsenter host namespace"
    exit 1
  fi
}

tt_module_loaded() {
  ns "lsmod 2>/dev/null | grep -q '^tenstorrent'" && return 0 || return 1
}

tt_device_present() {
  ns "ls /dev/tenstorrent/ >/dev/null 2>&1" && return 0 || return 1
}

# 현재 로드/설치된 tenstorrent 버전(installed) 반환. 없으면 빈 문자열.
# prebuilt 설치는 DKMS 기록을 남기지 않으므로 sysfs(/sys/module/tenstorrent/version)를
# 먼저 읽고, 값이 없으면 기존 dkms status 파싱으로 대체한다.
current_tt_version() {
  local v=""
  v="$(ns "cat /sys/module/tenstorrent/version 2>/dev/null || true" 2>/dev/null | tr -d ' ')" || true
  if [[ -n "$v" ]]; then
    echo "$v"
    return 0
  fi
  ns "dkms status tenstorrent 2>/dev/null | grep -m1 installed | sed -E 's#tenstorrent[/,] *([0-9][^,]*).*#\\1#' | tr -d ' '" 2>/dev/null || true
}

# 버전 비교: $1 > $2 이면 0(true). dpkg 로 판정, 실패 시 정수 major 비교 fallback.
ver_gt() {
  local a="$1" b="$2" am bm
  if ns "dpkg --compare-versions '${a}' gt '${b}'" >/dev/null 2>&1; then
    return 0
  elif ns "dpkg --compare-versions '${a}' le '${b}'" >/dev/null 2>&1; then
    return 1
  fi
  am="${a%%.*}"; bm="${b%%.*}"
  if [[ "$am" =~ ^[0-9]+$ && "$bm" =~ ^[0-9]+$ && "$am" -gt "$bm" ]]; then
    return 0
  fi
  return 1
}

# 기존(구버전) tenstorrent 모듈 언로드 + DKMS 제거. 업그레이드/다운그레이드 전 호출.
unload_old() {
  local old="$1"
  log_info "기존 tenstorrent ${old} 언로드 시작 (버전 변경 → ${TARGET_VERSION})"
  local i
  for i in $(seq 1 10); do
    ns "rmmod tenstorrent 2>/dev/null || true"
    if ! ns "lsmod | grep -q '^tenstorrent'"; then
      log_info "rmmod tenstorrent 성공 (시도 ${i})"
      break
    fi
    local refs
    refs=$(ns "cat /proc/modules 2>/dev/null | grep '^tenstorrent ' | awk '{print \$3}'" || echo "?")
    log_warn "tenstorrent 참조 카운트 ${refs} — 3초 후 재시도 ${i}/10"
    sleep 3
  done
  if ns "lsmod | grep -q '^tenstorrent'"; then
    log_error "rmmod 실패 — 모듈이 사용 중(refcount>0). drain 후 재시도 필요"
    return 1
  fi
  ns "dkms remove -m tenstorrent -v ${old} --all 2>/dev/null || true"
  return 0
}

# baked tt-kmd 소스를 DKMS 로 빌드/설치 (air-gap: 런타임 clone 없음).
install_tt_dkms() {
  local ver="$1" kver
  kver=$(ns "uname -r")

  # prebuilt 분기: 이미지에 이 커널용 .ko 가 함께 구워져 있으면 DKMS 빌드 없이 설치한다.
  # install Job 은 host /lib/modules 를 컨테이너 /lib/modules 로 마운트하므로
  # (internal/driverjob/job.go 의 host-modules VolumeMount), 컨테이너에서 그 경로에 쓰면
  # host 의 /lib/modules 에 그대로 반영된다. depmod·modprobe 는 host 네임스페이스에서 수행한다.
  local host_modules_root="/lib/modules"   # host /lib/modules 의 컨테이너 마운트 지점(job.go 와 일치)
  local prebuilt_ko="/opt/tt-kmd-ko/${kver}/tenstorrent.ko"
  if [[ -f "${prebuilt_ko}" ]]; then
    local prebuilt_vermagic
    prebuilt_vermagic="$(cat "/opt/tt-kmd-ko/${kver}/vermagic" 2>/dev/null || true)"
    log_info "prebuilt module found for ${kver}: ${prebuilt_vermagic}"
    install -D -m 0644 "${prebuilt_ko}" "${host_modules_root}/${kver}/updates/dkms/tenstorrent.ko" || {
      log_error "prebuilt module copy failed for ${kver}"
      return 1
    }
    ns "depmod -a ${kver}" || {
      log_error "depmod failed for ${kver}"
      return 1
    }
    if ! ns "modprobe tenstorrent"; then
      log_error "prebuilt modprobe 실패 (vermagic 불일치 또는 Secure Boot 서명 거부 가능: ${prebuilt_vermagic})"
      ns "dmesg 2>/dev/null | grep -i tenstorrent | tail -20" || true
      return 1
    fi
    log_info "prebuilt module for ${kver} installed (dkms skipped)"
    return 0
  fi

  local src_host="/usr/src/tenstorrent-${ver}"   # host /usr/src (bind-mount) — DKMS 기본 sourcetree
  local src_baked="/opt/tt-kmd-src/${ver}"        # 이미지 baked (host /usr/src 마운트에 가려지지 않는 경로)

  if [[ ! -f "${src_baked}/dkms.conf" ]]; then
    log_error "baked tt-kmd 소스 없음: ${src_baked} (이미지 빌드 오류 — TT_KMD_GIT_TAG 확인)"
    return 1
  fi

  # 커널 헤더 확인 (없으면 apt 시도 — air-gap 이면 실패해도 이미 존재 시 빌드 진행)
  if ! ns "[ -d /lib/modules/${kver}/build ]" && ! ns "dpkg -s linux-headers-${kver} >/dev/null 2>&1"; then
    log_info "linux-headers-${kver} 부재 — apt 설치 시도"
    ns "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends linux-headers-${kver}" \
      || log_warn "linux-headers-${kver} 설치 실패 — 기존 헤더로 빌드 시도"
  fi

  # baked 소스 → host /usr/src (컨테이너 /usr/src 는 host bind-mount 이므로 host 에 반영됨)
  log_info "baked tt-kmd ${ver} 소스를 ${src_host} 로 전개"
  rm -rf "${src_host}"
  cp -a "${src_baked}" "${src_host}"

  # 동일 버전 DKMS 잔재 정리 후 add/build/install
  ns "dkms remove -m tenstorrent -v ${ver} --all 2>/dev/null || true"
  ns "dkms add -m tenstorrent -v ${ver} 2>/dev/null || true"
  if ! ns "dkms build -m tenstorrent -v ${ver} -k ${kver}"; then
    log_error "DKMS 빌드 실패 (tenstorrent/${ver}, kernel ${kver})"
    ns "find /var/lib/dkms/tenstorrent/${ver} -name make.log 2>/dev/null -exec tail -n 60 {} \\;" || true
    return 1
  fi
  if ! ns "dkms install -m tenstorrent -v ${ver} -k ${kver} --force"; then
    log_error "DKMS install 실패 (tenstorrent/${ver})"
    return 1
  fi
  ns "modprobe tenstorrent" || {
    log_error "DKMS install 후 modprobe tenstorrent 실패"
    ns "dmesg 2>/dev/null | grep -i tenstorrent | tail -20" || true
    return 1
  }
  log_info "tenstorrent ${ver} DKMS 빌드·설치·로드 완료"
  return 0
}

health_check() {
  tt_module_loaded || { log_warn "tenstorrent 모듈 미로드"; return 1; }
  tt_device_present || { log_warn "/dev/tenstorrent/* 디바이스 없음"; return 1; }
  return 0
}

# ============================================================
# MAIN
# ============================================================
log_info "Tenstorrent tt-kmd driver installer 시작 (RUN_MODE=${RUN_MODE})"
require_host_ns
ns "mkdir -p ${MARKER_DIR}"

if [[ -z "${TARGET_VERSION}" ]]; then
  log_error "TT_KMD_VERSION(baked) 미설정 — 이미지 빌드 오류"
  exit 1
fi
if [[ -n "${DRIVER_VERSION}" && "${DRIVER_VERSION}" != "${TARGET_VERSION}" ]]; then
  log_warn "DIP DRIVER_VERSION(${DRIVER_VERSION}) != baked TT_KMD_VERSION(${TARGET_VERSION}) — baked 버전으로 진행"
fi

CURRENT_VERSION="$(current_tt_version)"
LOADED=1; tt_module_loaded && LOADED=0
[[ -n "${CURRENT_VERSION}" ]] && log_info "기존 설치 버전 감지: ${CURRENT_VERSION} (loaded=$([[ $LOADED -eq 0 ]] && echo true || echo false))"

SKIP_INSTALL=0

# (c) VERSION_SOURCE=Host: 호스트 로드 버전을 존중, 설치 보류.
if [[ "${VERSION_SOURCE}" == "Host" && $LOADED -eq 0 && -n "${CURRENT_VERSION}" ]]; then
  log_info "VERSION_SOURCE=Host: 호스트 버전 ${CURRENT_VERSION} 존중 — 설치 보류"
  SKIP_INSTALL=1
fi

# idempotency: 로드된 버전이 target 과 동일하면 재설치 불필요.
if [[ $SKIP_INSTALL -eq 0 && $LOADED -eq 0 && "${CURRENT_VERSION}" == "${TARGET_VERSION}" ]]; then
  log_info "요청 버전 ${TARGET_VERSION} 이미 로드됨 — 설치 보류(idempotent)"
  SKIP_INSTALL=1
fi

# (b) 다운그레이드 가드: 기존 > target 이고 ALLOW_DOWNGRADE!=true 이면 기존 유지.
if [[ $SKIP_INSTALL -eq 0 && -n "${CURRENT_VERSION}" ]] \
   && ver_gt "${CURRENT_VERSION}" "${TARGET_VERSION}" && [[ "${ALLOW_DOWNGRADE}" != "true" ]]; then
  log_warn "기존 ${CURRENT_VERSION} > target ${TARGET_VERSION}; ALLOW_DOWNGRADE=false → 다운그레이드 보류, 기존 유지"
  SKIP_INSTALL=1
fi

if [[ $SKIP_INSTALL -eq 1 ]]; then
  log_info "설치 단계 건너뜀 (기존 드라이버 존중)"
  # 모듈이 미로드 상태면 로드만 시도(설치 없이).
  tt_module_loaded || ns "modprobe tenstorrent 2>/dev/null || true"
else
  # 버전 변경(업그레이드/다운그레이드)이면 기존 모듈 먼저 언로드.
  if [[ $LOADED -eq 0 && -n "${CURRENT_VERSION}" && "${CURRENT_VERSION}" != "${TARGET_VERSION}" ]]; then
    unload_old "${CURRENT_VERSION}" || { log_error "기존 모듈 언로드 실패 — 설치 중단"; exit 1; }
  fi
  install_tt_dkms "${TARGET_VERSION}" || { log_error "tt-kmd ${TARGET_VERSION} 설치 실패 — 종료"; exit 1; }
fi

# --- 디바이스/진단 ---
if tt_device_present; then
  log_info "/dev/tenstorrent/ 디바이스 확인 완료"
else
  log_warn "/dev/tenstorrent/ 디바이스 없음 — 모듈 로드됐으나 HW 부재 가능"
fi

ns "lsmod 2>/dev/null | grep tenstorrent > ${MARKER_DIR}/tt-kmd.lsmod 2>/dev/null || true"
ns "dkms status tenstorrent > ${MARKER_DIR}/tt-kmd.dkms 2>/dev/null || true"
ns "ls -la /dev/tenstorrent/ > ${MARKER_DIR}/tt-kmd.devices 2>/dev/null || true"
ns "dmesg 2>/dev/null | grep -i tenstorrent | tail -20 > ${MARKER_DIR}/tt-kmd.dmesg 2>/dev/null || true"
ns "echo ok > ${MARKER_DIR}/driver.ok"

log_info "tt-kmd driver 처리 완료 (target=${TARGET_VERSION})"

# --- ready 마커 ---
touch /tmp/driver-ready
ns "touch ${MARKER_DIR}/driver.ready" || true

# RUN_MODE=job: self-check 1회 후 exit 0 (상주 루프 없음).
if [[ "${RUN_MODE}" == "job" ]]; then
  log_info "RUN_MODE=job — 설치 완료, self-check 후 exit 0"
  if ! health_check; then
    log_error "RUN_MODE=job self-check 실패 — Job 실패(exit 1)"
    exit 1
  fi
  log_info "RUN_MODE=job self-check 통과 — install Job 정상 종료(exit 0)"
  exit 0
fi

# DaemonSet 모드: 상시 헬스 모니터링 루프.
log_info "Ready 마커 생성 — 상시 모니터링 루프 시작 (interval=${HEALTH_CHECK_INTERVAL:-30}s)"
while true; do
  sleep "${HEALTH_CHECK_INTERVAL:-30}"
  if ! health_check; then
    log_error "tt-kmd 드라이버 이상 감지 — livenessProbe 트리거를 위해 종료"
    exit 1
  fi
done
