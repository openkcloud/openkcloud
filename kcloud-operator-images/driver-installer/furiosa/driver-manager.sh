#!/usr/bin/env bash
# ============================================================
# driver-manager.sh: initContainer에서 실행 — 기존 Furiosa NPU 커널 모듈 해제 (Warboy·RNGD 공용)
# 상세: rolling upgrade 시 이 모델의 커널 모듈을 rmmod 하고 main container 가 재설치한다. 모델
#       표는 entrypoint.sh 와 같은 값을 쓴다. 안전장치(a 전량 passthrough 시 미수행/b 다운그레이드
#       방지/c Host 버전 존중)도 entrypoint.sh 와 같다.
# 생성일: 2026-09-09
# ============================================================
set -euo pipefail

log_info()  { echo "[INFO] $*"; }
log_warn()  { echo "[WARN] $*"; }
log_error() { echo "[ERR]  $*"; }

MARKER_DIR="${MARKER_DIR:-/var/lib/kcloud-operator}"
MAX_RETRIES=10
RETRY_INTERVAL=3

SKIP_ON_PASSTHROUGH="${SKIP_ON_PASSTHROUGH:-true}"
ALLOW_DOWNGRADE="${ALLOW_DOWNGRADE:-false}"
VERSION_SOURCE="${VERSION_SOURCE:-Policy}"

ns() { nsenter --target 1 --mount --uts --ipc --net -- bash -c "$1"; }

# 마커 디렉터리 개명 이관(npu-operator → kcloud-operator). 1회 수행되고 멱등이다.
# 아래 어떤 MARKER_DIR 사용보다 먼저 와야 한다.
# shellcheck source=/dev/null
. /usr/local/bin/marker-migrate.sh
migrate_marker_dir "${MARKER_DIR}"

# 모델 표 — entrypoint.sh 의 select_model 과 값이 같아야 한다.
select_model() {
  case "$1" in
    warboy) PCI_DEVICE_ID="0x0000"; PKG="furiosa-driver-warboy"; MODULES="npu_mgmt npu_pdma" ;;
    rngd)   PCI_DEVICE_ID="0x0001"; PKG="furiosa-driver-rngd";   MODULES="furiosa_rngd" ;;
    *) log_error "unknown FURIOSA_MODEL '$1' (warboy|rngd)"; exit 1 ;;
  esac
  HEALTH_MODULE="${MODULES##* }"
}

host_furiosa_device_ids() {
  ns 'for d in /sys/bus/pci/devices/*/; do
      [ -r "${d}vendor" ] || continue
      [ "$(cat "${d}vendor" 2>/dev/null)" = "0x1ed2" ] || continue
      cat "${d}device" 2>/dev/null
    done' | sort -u
}

detect_model() {
  local ids warboy=0 rngd=0
  ids="$(host_furiosa_device_ids | tr -d '\r')"
  while IFS= read -r id; do
    case "$id" in 0x0000) warboy=1 ;; 0x0001) rngd=1 ;; esac
  done <<<"$ids"
  if [[ "$warboy" -eq 1 && "$rngd" -eq 1 ]]; then
    log_error "Warboy 와 RNGD 가 한 노드에 함께 있다 — FURIOSA_MODEL 을 명시하라"; exit 1
  fi
  if [[ "$warboy" -eq 1 ]]; then echo warboy; return; fi
  if [[ "$rngd" -eq 1 ]]; then echo rngd; return; fi
  # initContainer 는 장치가 없는 노드에서 해제할 것도 없다. 통과.
  echo none
}

npu_bindings() {
  ns "total=0; installable=0
    for d in /sys/bus/pci/devices/*/; do
      [ -r \"\${d}vendor\" ] || continue
      vendor=\$(cat \"\${d}vendor\" 2>/dev/null || echo \"\")
      device=\$(cat \"\${d}device\" 2>/dev/null || echo \"\")
      [ \"\$vendor\" = \"0x1ed2\" ] || continue
      [ \"\$device\" = \"${PCI_DEVICE_ID}\" ] || continue
      total=\$((total+1))
      bound=\"\"
      if [ -L \"\${d}driver\" ]; then
        bound=\$(basename \"\$(readlink \"\${d}driver\")\" 2>/dev/null || echo \"\")
      fi
      [ \"\$bound\" != \"vfio-pci\" ] && installable=\$((installable+1))
    done
    echo \"\$total \$installable\""
}

has_installable_npu() {
  local out total installable
  out=$(npu_bindings 2>/dev/null | tail -n1 || echo "0 0")
  total=${out%% *}; installable=${out##* }
  case "$total" in ''|*[!0-9]*) total=0 ;; esac
  case "$installable" in ''|*[!0-9]*) installable=0 ;; esac
  [ "${total:-0}" -eq 0 ] && return 0
  [ "${installable:-0}" -gt 0 ] && return 0
  return 1
}

# 버전 비교: $1 > $2 이면 0(true). dpkg 우선, 판정 불가 시 major 정수 비교 fallback.
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

# ----- main -----
log_info "이전 driver.ready 마커 삭제"
rm -f "${MARKER_DIR}/driver.ready" /tmp/driver-ready 2>/dev/null || true

MODEL="${FURIOSA_MODEL:-}"
if [[ -z "${MODEL}" ]]; then
  MODEL="$(detect_model)"
  if [[ "${MODEL}" == "none" ]]; then
    log_info "Furiosa 장치 없음 — 해제할 모듈 없음, 통과"
    exit 0
  fi
  log_info "FURIOSA_MODEL 미지정 — PCI 로 판별: ${MODEL}"
fi
select_model "${MODEL}"

# (a) 전량 vfio-pci(passthrough) 노드: main container 가 idle 진입하므로 모듈 해제 불필요.
if [[ "${SKIP_ON_PASSTHROUGH}" != "false" ]] && ! has_installable_npu; then
  log_info "모든 ${MODEL} NPU 가 vfio-pci(passthrough) 바인딩 — 모듈 해제 불필요, 통과"
  exit 0
fi

# (c) VERSION_SOURCE=Host: 호스트 설치 버전을 존중하므로 버전 변경/해제 없음.
if [[ "${VERSION_SOURCE}" == "Host" ]]; then
  log_info "VERSION_SOURCE=Host — 기존 호스트 드라이버 존중, 모듈 해제 불필요, 통과"
  exit 0
fi

DRIVER_VERSION="${DRIVER_VERSION:-}"
if [[ -z "$DRIVER_VERSION" ]]; then
  log_info "DRIVER_VERSION 미지정 — 모듈 해제 불필요, 통과"
  exit 0
fi

CURRENT_VER=""
if ns "test -f ${MARKER_DIR}/driver.ok" 2>/dev/null; then
  CURRENT_VER=$(ns "dpkg-query -W -f='\${Version}' ${PKG} 2>/dev/null" || true)
fi

if [[ -z "$CURRENT_VER" ]]; then
  log_info "${PKG} 미설치 또는 모듈 미로드 상태 — 해제 불필요"
  exit 0
fi

if [[ "$CURRENT_VER" == "$DRIVER_VERSION" ]]; then
  log_info "동일 버전 (${CURRENT_VER}) — 모듈 해제 불필요"
  exit 0
fi

# (b) 다운그레이드 가드: 기존 버전이 desired 보다 최신이면 재설치하지 않으므로 해제도 하지 않는다.
if ver_gt "$CURRENT_VER" "$DRIVER_VERSION" && [[ "${ALLOW_DOWNGRADE}" != "true" ]]; then
  log_warn "기존 드라이버 ${CURRENT_VER} > desired ${DRIVER_VERSION}; 다운그레이드 비활성(ALLOW_DOWNGRADE=false) → 기존 유지, 모듈 해제 생략"
  exit 0
fi

log_info "드라이버 버전 변경 감지: ${CURRENT_VER} → ${DRIVER_VERSION}"
log_info "${MODEL} 커널 모듈 해제 시작 (${MODULES})"

# 로드 순서의 역순으로 내린다.
REVERSED=""
for mod in ${MODULES}; do REVERSED="${mod} ${REVERSED}"; done

for i in $(seq 1 $MAX_RETRIES); do
  log_info "rmmod 시도 ${i}/${MAX_RETRIES}"
  for mod in ${REVERSED}; do
    ns "rmmod ${mod} 2>/dev/null || true"
  done

  if ! ns "lsmod | grep -q '^${HEALTH_MODULE} '"; then
    log_info "${MODEL} 커널 모듈 해제 성공 (시도 ${i})"
    exit 0
  fi

  REFS=$(ns "cat /proc/modules | grep '^${HEALTH_MODULE} ' | awk '{print \$3}'" || echo "?")
  log_warn "${HEALTH_MODULE} 모듈 참조 카운트: ${REFS}, ${RETRY_INTERVAL}초 후 재시도"
  sleep $RETRY_INTERVAL
done

log_error "${MODEL} 모듈 해제 실패 (${MAX_RETRIES}회 시도)"
log_error "노드 재부팅이 필요할 수 있습니다"

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
