#!/usr/bin/env bash
# ============================================================
# entrypoint-v17.sh: NVIDIA driver v17 — containerized install entrypoint
# 상세: 컨테이너 내부에서 nvidia.ko 를 빌드해 host /run/nvidia/driver 에 sideload 한다.
#       host /etc·/var/lib/dpkg·host apt 는 절대 건드리지 않는다(nsenter 미사용).
#       INSTALL_METHOD=hostnamespace 면 legacy v16 entrypoint.sh 로 위임한다.
# 생성일: 2026-04-27 | 수정일: 2026-10-11
# ============================================================
set -Eeuo pipefail
[[ "${DEBUG:-0}" == "1" ]] && set -x

# v17.2: dkms / subprocess 가 항상 표준 sbin/bin 경로 찾도록 PATH 강제 prepend
export PATH="/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}"

# ---------------------------------------------------------------------------- #
# 환경변수 (default)
# ---------------------------------------------------------------------------- #
INSTALL_METHOD="${INSTALL_METHOD:-containerized}"
HOST_LIB_MODULES="${HOST_LIB_MODULES:-/host/lib/modules}"            # ro mount of host /lib/modules
HOST_SYS_MODULE_VERSION="${HOST_SYS_MODULE_VERSION:-/host/sys/module/kernel/version}"
HOST_PROC_DRIVER_NVIDIA="${HOST_PROC_DRIVER_NVIDIA:-/host/proc/driver/nvidia/version}"
DRIVER_ROOT="${DRIVER_ROOT:-/run/nvidia/driver}"                     # writable sideload dest (host bind mount)
MARKER_DIR="${MARKER_DIR:-/var/lib/kcloud-operator}"                    # writable marker (host bind mount)

# 마커 디렉터리 개명(npu-operator → kcloud-operator)은 여기서 이관하지 않는다. v17 은 nsenter 를
# 쓰지 않는 것이 불변식이고 호스트 옛 경로는 이 컨테이너에 마운트되지 않는다. v17 노드는
# 새 경로에 마커를 새로 쓰고, 옛 마커는 detector 가 옛 경로 폴백으로 읽는다(MIGRATION_v17.md).
HEALTH_CHECK_INTERVAL="${HEALTH_CHECK_INTERVAL:-30}"
PREBUILT_FALLBACK="${PREBUILT_FALLBACK:-true}"

# 안전장치 동작 env (기본값 = 기존 동작 보존, 하위호환) — legacy entrypoint.sh 와 등가
#   SKIP_ON_PASSTHROUGH (기본 true) : (a) GPU 전량 vfio-pci 바인딩 시 설치 보류
#   ALLOW_DOWNGRADE     (기본 false): (b) 하위 버전 다운그레이드 허용 여부
#   VERSION_SOURCE      (기본 Policy): (c) Host=호스트 로드 버전을 desired 로 채택
SKIP_ON_PASSTHROUGH="${SKIP_ON_PASSTHROUGH:-true}"
ALLOW_DOWNGRADE="${ALLOW_DOWNGRADE:-false}"
VERSION_SOURCE="${VERSION_SOURCE:-Policy}"

# RUN_MODE: 설치/skip 완료 후 동작을 결정한다.
#   daemonset (기본) : 기존 동작 — exec sleep infinity / 상주 healthcheck 루프로 대기(회귀 0).
#   job              : 순간 install Job — 설치/skip 완료 후 exit 0(상주 안 함).
# 미지정 시 daemonset 으로 동작하여 기존 DS 이미지와 완전 하위호환.
RUN_MODE="${RUN_MODE:-daemonset}"

log_info()  { echo "[INFO]  $*"; }
log_warn()  { echo "[WARN]  $*" >&2; }
log_error() { echo "[ERR]   $*" >&2; }
log_step()  { echo; echo "[STEP]  =====  $*  ====="; }

# residency_exit: 설치/skip 완료 후 종료 동작 중앙화.
#   RUN_MODE=job       → exit 0 (순간 Job — 상주 안 함).
#   RUN_MODE=daemonset → exec sleep infinity (기존 상주, 기본값).
# 모든 idempotent-skip 경로가 이 함수로 종료하여 daemonset 동작을 바이트 보존한다.
residency_exit() {
  if [ "${RUN_MODE}" = "job" ]; then
    log_info "RUN_MODE=job — install/skip 완료, exit 0 (상주 없음)"
    exit 0
  fi
  exec sleep infinity
}

# v17.3 — NVRM version 파싱 (X.Y / X.Y.Z / X.Y.Z.W / 임의 dot 개수 모두 흡수).
# whitespace 변동 (1+ space/tab) 에 robust. 실패 시 빈 문자열 + caller log dump.
# arg1: /proc/driver/nvidia/version 경로
parse_nvrm_version() {
  local src="$1"
  local v=""
  [[ -r "${src}" ]] || { echo ""; return 1; }
  # 1) full prefix 매칭 (NVIDIA UNIX ... Kernel Module  <ver>)
  v=$(grep -oP 'NVRM version:\s+NVIDIA UNIX[^/]*Kernel Module\s+\K[0-9]+(\.[0-9]+)+' "${src}" 2>/dev/null \
      | head -n1 || true)
  # 2) generic fallback (NVRM version: <ver>)
  if [[ -z "${v}" ]]; then
    v=$(grep -oP 'NVRM version:[^0-9]*\K[0-9]+(\.[0-9]+)+' "${src}" 2>/dev/null \
        | head -n1 || true)
  fi
  echo "${v}"
}

# v17.3 — host kernel 에 nvidia 모듈이 lsmod / /proc/modules 에 잔존하는지 race-safe 검사.
# 1) /host/proc/modules ro mount 우선 (privileged DS host bind)
# 2) /proc/modules (host kernel 공유 가정)
# 결과: 잔존이면 0 (true), 미잔존이면 1 (false)
host_has_nvidia_module() {
  local cand
  for cand in "/host/proc/modules" "/proc/modules"; do
    if [[ -r "${cand}" ]]; then
      if grep -qE '^nvidia[[:space:]]' "${cand}" 2>/dev/null; then
        return 0
      fi
    fi
  done
  return 1
}

# ============================================================================ #
# 안전장치 헬퍼: passthrough 감지 · 버전 비교 (legacy entrypoint.sh 와 등가,
# containerized 모드이므로 nsenter 없이 privileged DS 의 공유 /sys · dpkg 직접 사용)
# ============================================================================ #

# 호스트 PCI 를 순회하며 NVIDIA GPU 의 "<total> <installable>" 개수를 출력한다.
#   total       : NVIDIA(0x10de) & display class(0x03xxxx) 장치 총 개수
#   installable : 그 중 driver 바인딩이 vfio-pci 가 아닌(=nvidia/미바인딩) 장치 개수
# privileged DS 의 /sys 는 host /sys 를 공유한다는 가정 (v17 이 /proc 를 직접 읽는 것과 동일).
nvidia_gpu_bindings() {
  local total=0 installable=0 d vendor class bound
  for d in /sys/bus/pci/devices/*/; do
    [ -r "${d}vendor" ] || continue
    vendor=$(cat "${d}vendor" 2>/dev/null || echo "")
    class=$(cat "${d}class" 2>/dev/null || echo "")
    [ "${vendor}" = "0x10de" ] || continue
    case "${class}" in 0x03*) ;; *) continue ;; esac
    total=$((total + 1))
    bound=""
    if [ -L "${d}driver" ]; then
      bound=$(basename "$(readlink "${d}driver")" 2>/dev/null || echo "")
    fi
    [ "${bound}" != "vfio-pci" ] && installable=$((installable + 1))
  done
  echo "${total} ${installable}"
}

# 설치 대상 GPU 존재 여부.
#   반환 0(true)  : installable GPU 가 1개 이상 있거나, GPU 가 아예 없는 비-GPU 노드(가드 미적용)
#   반환 1(false) : GPU 는 있으나 전량 vfio-pci 바인딩(passthrough)
nvidia_has_installable_gpu() {
  local out total installable
  out=$(nvidia_gpu_bindings 2>/dev/null | tail -n1 || echo "0 0")
  total=$(echo "${out}" | awk '{print $1+0}')
  installable=$(echo "${out}" | awk '{print $2+0}')
  # 비-GPU 노드는 가드 미적용 → 기존 동작 유지
  [ "${total:-0}" -eq 0 ] && return 0
  [ "${installable:-0}" -gt 0 ] && return 0
  return 1
}

# 버전 비교: $1 > $2 이면 0(true). dpkg 로 정상 비교되면 그 결과, 형식오류 등으로
# dpkg 가 판정 불가하면 major 정수 비교로 fallback. (container dpkg 직접 사용)
ver_gt() {
  local a="$1" b="$2" am bm
  if dpkg --compare-versions "${a}" gt "${b}" >/dev/null 2>&1; then
    return 0
  elif dpkg --compare-versions "${a}" le "${b}" >/dev/null 2>&1; then
    return 1
  fi
  # dpkg 가 gt/le 모두 판정 실패 → 형식 문제로 간주, major 정수 비교 fallback
  am="${a%%.*}"; bm="${b%%.*}"
  if [[ "${am}" =~ ^[0-9]+$ && "${bm}" =~ ^[0-9]+$ ]] && [[ "${am}" -gt "${bm}" ]]; then
    return 0
  fi
  return 1
}

# ============================================================================ #
# 0) hostnamespace 모드 분기 — legacy v16 으로 위임
# ============================================================================ #
if [[ "${INSTALL_METHOD}" == "hostnamespace" ]]; then
  log_info "INSTALL_METHOD=hostnamespace → legacy entrypoint.sh 위임"
  if [[ ! -x "/usr/local/bin/entrypoint.sh" ]]; then
    log_error "legacy entrypoint.sh 미존재 — 이미지에 v16 파일이 포함되어 있는지 확인"
    exit 1
  fi
  exec /usr/local/bin/entrypoint.sh "$@"
fi

if [[ "${INSTALL_METHOD}" != "containerized" ]]; then
  log_error "Unknown INSTALL_METHOD: ${INSTALL_METHOD} (expected: containerized | hostnamespace)"
  exit 1
fi

# ============================================================================ #
# 1) host kernel 버전 read-only detect
# ============================================================================ #
detect_host_kernel() {
  local k=""
  if [[ -r "${HOST_SYS_MODULE_VERSION}" ]]; then
    k=$(cat "${HOST_SYS_MODULE_VERSION}" 2>/dev/null || true)
  fi
  if [[ -z "${k}" && -d "${HOST_LIB_MODULES}" ]]; then
    # /host/lib/modules/<kver> 만 마운트된 경우 디렉토리 이름으로 추정
    k=$(ls -1 "${HOST_LIB_MODULES}" 2>/dev/null | head -n1 || true)
  fi
  if [[ -z "${k}" ]]; then
    # last resort: container 자체 uname -r (host 와 동일하다는 privileged DS 가정)
    k=$(uname -r)
    log_warn "host kernel 버전 read-only mount 부재 — uname -r=${k} 사용"
  fi
  echo "${k}"
}

# ENTRYPOINT_LIB_ONLY=1 이면 아래 host 하드웨어 probing/idempotent-skip/exit 블록
# 전부 건너뛴다 — 순수 bash 함수 시험이 실제 host 드라이버 상태(이미 로드된 모듈
# major 일치 시 residency_exit 가 exec sleep infinity 로 멈춤 등)에 좌우되지 않게 한다.
if [[ "${ENTRYPOINT_LIB_ONLY:-0}" != "1" ]]; then
KVER="$(detect_host_kernel)"
if [[ -z "${KVER}" ]]; then
  log_error "host kernel 버전 detect 실패"
  exit 1
fi
log_info "host kernel = ${KVER}"

# DRIVER_VERSION 처리 — 메이저 추출
DRIVER_VERSION_RAW="${DRIVER_VERSION:-}"
if [[ -z "${DRIVER_VERSION_RAW}" ]]; then
  log_error "DRIVER_VERSION 환경변수가 비었습니다 (containerized 모드는 명시 필수)"
  exit 1
fi
MAJOR="${DRIVER_VERSION_RAW%%.*}"
if ! [[ "${MAJOR}" =~ ^[0-9]+$ ]]; then
  log_error "DRIVER_VERSION 형식 오류: ${DRIVER_VERSION_RAW} (예: 590.48.01 또는 590)"
  exit 1
fi
log_info "DRIVER_VERSION=${DRIVER_VERSION_RAW} (major=${MAJOR})"
# 정책 버전이 desired 일 때 1, 호스트 버전을 채택하면 0 — 고정 설치 여부의 기준
DESIRED_FROM_POLICY=1

# ============================================================================ #
# 1.4) PASSTHROUGH GUARD (a) — 설치/빌드/modprobe 이전 최전방. 호스트의 모든 NVIDIA GPU 가
#   vfio-pci(passthrough)에 바인딩된 노드면 드라이버 설치/빌드/sideload/modprobe 를 전면
#   skip 한다(GPU 없는 비-GPU 노드는 가드 미적용, installable GPU 가 1개라도 있으면 정상
#   진행). skip 시 passthrough-skip + ready 마커 후 idle(exec sleep infinity)로 Pod
#   Running 을 유지하고 어떤 모듈도 로드하지 않는다.
if [[ "${SKIP_ON_PASSTHROUGH}" != "false" ]] && ! nvidia_has_installable_gpu; then
  log_info "All NVIDIA GPUs bound to vfio-pci (passthrough) — skip install/build/modprobe"
  mkdir -p "${MARKER_DIR}" 2>/dev/null || true
  touch "${MARKER_DIR}/passthrough-skip" 2>/dev/null || true
  touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
  touch /tmp/driver-ready 2>/dev/null || true
  log_info "passthrough 노드 — 설치 대상 없음 (드라이버 설치/빌드/modprobe 미수행)"
  residency_exit
fi

# ============================================================================ #
# 1.5) v17.2 — host idempotent skip: host kernel 모듈 NVRM major 가 desired major 와
#   일치하면 install/build 절차를 전체 skip 하고 driver.ready marker 후 exec sleep
#   infinity 로 멈춘다(Validating timeout·dkms 경합·불필요한 host 영향 차단). 검사
#   우선순위는 HOST_PROC_DRIVER_NVIDIA(ro bind mount) 다음 container
#   /proc/driver/nvidia/version(privileged DS 의 host /proc 공유) 순이다.
HOST_NVRM=""
HOST_NVRM_SRC=""
for _cand in "${HOST_PROC_DRIVER_NVIDIA}" "/proc/driver/nvidia/version"; do
  if [[ -r "${_cand}" ]]; then
    HOST_NVRM="$(parse_nvrm_version "${_cand}")"
    if [[ -n "${HOST_NVRM}" ]]; then
      HOST_NVRM_SRC="${_cand}"
      log_info "host NVRM detect 경로=${_cand} → ${HOST_NVRM}"
      break
    else
      log_warn "host NVRM 파싱 실패 (${_cand}) — 출력 dump:"
      sed -n '1,5p' "${_cand}" 2>/dev/null | sed 's/^/  | /' >&2 || true
    fi
  fi
done

# --- HOST VERSION RESPECT (c) ---
# VERSION_SOURCE=Host 이고 호스트에 드라이버가 로드돼 있으면 그 버전을 effective desired
# 로 채택하고 설치를 보류(존중)한다. major 일치 여부와 무관하게 host 버전을 존중.
# 호스트에 드라이버가 없으면 policy DRIVER_VERSION 으로 fallback(기존 install 절차 진행).
if [[ "${VERSION_SOURCE}" == "Host" ]]; then
  if [[ -n "${HOST_NVRM}" ]]; then
    log_info "VERSION_SOURCE=Host, adopting existing host driver ${HOST_NVRM} as desired (skip install)"
    DRIVER_VERSION_RAW="${HOST_NVRM}"
    DESIRED_FROM_POLICY=0  # 호스트 버전 채택 — 정책이 아니므로 고정 대상 아님(이 분기는 곧 residency_exit 로 끝나 실질 영향 없음, 뜻만 명시)
    MAJOR="${HOST_NVRM%%.*}"
    mkdir -p "${MARKER_DIR}" 2>/dev/null || true
    echo "${HOST_NVRM}" > "${MARKER_DIR}/nvidia.ok" 2>/dev/null || true
    touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
    touch /tmp/driver-ready 2>/dev/null || true
    log_info "VERSION_SOURCE=Host idempotent skip"
    residency_exit
  else
    log_info "VERSION_SOURCE=Host but no host driver detected — falling back to policy DRIVER_VERSION=${DRIVER_VERSION_RAW}"
  fi
fi

if [[ -n "${HOST_NVRM}" ]]; then
  HOST_MAJOR="${HOST_NVRM%%.*}"
  if [[ "${HOST_MAJOR}" == "${MAJOR}" ]]; then
    log_info "host already has matching major ${HOST_MAJOR} loaded (NVRM=${HOST_NVRM}) — skip install, mark ready"
    mkdir -p "${MARKER_DIR}" 2>/dev/null || true
    echo "${HOST_NVRM}" > "${MARKER_DIR}/nvidia.ok" 2>/dev/null || true
    touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
    touch /tmp/driver-ready 2>/dev/null || true
    log_info "v17.2 idempotent skip path"
    residency_exit
  fi
  # --- DOWNGRADE GUARD (b) ---
  # 기존 host 로드 버전이 desired 보다 최신이면(major 동일/상이 무관) 다운그레이드를
  # 방지하고 기존을 유지한다. ALLOW_DOWNGRADE=true 일 때만 하위 버전 설치를 허용.
  if ver_gt "${HOST_NVRM}" "${DRIVER_VERSION_RAW}" && [[ "${ALLOW_DOWNGRADE}" != "true" ]]; then
    log_warn "Existing host driver ${HOST_NVRM} > desired ${DRIVER_VERSION_RAW}; downgrade disabled (ALLOW_DOWNGRADE=false) → keep existing, skip install"
    mkdir -p "${MARKER_DIR}" 2>/dev/null || true
    echo "${HOST_NVRM}" > "${MARKER_DIR}/nvidia.ok" 2>/dev/null || true
    touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
    touch /tmp/driver-ready 2>/dev/null || true
    log_info "downgrade guard idempotent skip"
    residency_exit
  fi
  log_info "host has ${HOST_NVRM} (major ${HOST_MAJOR}) — desired ${DRIVER_VERSION_RAW} (major ${MAJOR}) mismatch, proceed install"
else
  log_info "host NVRM 미감지 — install 절차 진행"
fi

# v17.3 — Phase A: install path 진입 직전 race-safe 모듈 잔존 재검사.
# rationale: HOST_PROC_DRIVER_NVIDIA 가 비어있어도 (/proc 마운트 못 받았어도)
# host kernel 의 module pool 에 'nvidia' module 이 잔존하면 DKMS sideload+insmod
# 이 "File exists" 로 실패한다. 잔존 시:
#   - 모듈 major 식별 가능하면 매치 → idempotent skip 으로 fall-through
#   - 식별 실패 시 fail-fast 로 driver-manager.sh cleanup path 위임 (exit 1)
if host_has_nvidia_module; then
  log_warn "race-safe 재검사 — host module pool 에 nvidia module 잔존 감지"
  # 모듈이 잔존하지만 NVRM major 식별 실패한 경우: cleanup path 로 위임
  if [[ -z "${HOST_NVRM}" ]]; then
    log_error "host nvidia module 잔존하나 NVRM major 식별 실패 — driver-manager.sh cleanup path 필요"
    log_error "(restart 시 host 의 driver-manager 가 cleanup → 본 entrypoint 재진입 시 idempotent skip 가능)"
    exit 1
  fi
  HOST_MAJOR="${HOST_NVRM%%.*}"
  if [[ "${HOST_MAJOR}" == "${MAJOR}" ]]; then
    log_info "host nvidia module (major ${HOST_MAJOR}) == desired (${MAJOR}) — fall-through to idempotent skip"
    mkdir -p "${MARKER_DIR}" 2>/dev/null || true
    echo "${HOST_NVRM}" > "${MARKER_DIR}/nvidia.ok" 2>/dev/null || true
    touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
    touch /tmp/driver-ready 2>/dev/null || true
    log_info "race-safe idempotent skip"
    residency_exit
  fi
  log_warn "host nvidia module (major ${HOST_MAJOR}) != desired (${MAJOR}) — driver-manager.sh cleanup 후 재진입 필요"
  log_error "host module pool 에 cross-major nvidia 잔존 — 본 컨테이너에서 직접 rmmod 회피 (host /etc 무영향 원칙). exit 1 → restart 후 cleanup path 재시도"
  exit 1
fi

# ============================================================================ #
# 2) host invariant 보호 — 본 스크립트는 host /etc, /var/lib/dpkg 절대 write 금지
# ============================================================================ #
HOST_INVARIANT_PATHS=(
  "/host/etc"
  "/host/var/lib/dpkg"
  "/host/var/lib/apt"
)
for p in "${HOST_INVARIANT_PATHS[@]}"; do
  if [[ -e "${p}" ]]; then
    log_warn "host invariant path bind-mounted: ${p} — 본 스크립트는 절대 write 하지 않음"
  fi
done

# ============================================================================ #
# 3) cleanup trap — 실패 시 partial state 정리 (sideload 한 module 만)
# ============================================================================ #
INSMOD_LOADED=0
SKIP_CLEANUP=0
fi

rmmod_safely() {
  # privileged DS + /sys bind mount 가정하에 rmmod (host kernel 모듈 직접).
  for m in nvidia_uvm nvidia_drm nvidia_modeset nvidia; do
    rmmod "${m}" 2>/dev/null || true
  done
}

cleanup_on_error() {
  local ec=$?
  if [[ ${SKIP_CLEANUP} -eq 1 ]]; then
    return ${ec}
  fi
  if [[ ${ec} -ne 0 && ${INSMOD_LOADED} -eq 1 ]]; then
    log_warn "에러 발생 (exit=${ec}) — sideload 한 module 정리 시도"
    rmmod_safely
  fi
  return ${ec}
}
# ENTRYPOINT_LIB_ONLY=1 이면 이 trap 도 등록하지 않는다 — 등록하면 시험 스크립트 자신의
# EXIT trap 을 덮어쓰고, SKIP_CLEANUP/INSMOD_LOADED 는 위 host-flow 블록에서만 설정되므로
# (스킵됐으므로 unset) 시험 종료 시 nounset 오류가 난다.
if [[ "${ENTRYPOINT_LIB_ONLY:-0}" != "1" ]]; then
  trap cleanup_on_error EXIT
fi

# ============================================================================ #
# 4) prebuilt fallback 검사 — host /lib/modules ro 에 prebuilt nvidia.ko 가 있는지
# ============================================================================ #
PREBUILT_OK=0
PREBUILT_DIR=""

check_prebuilt() {
  if [[ "${PREBUILT_FALLBACK}" != "true" ]]; then
    log_info "PREBUILT_FALLBACK=false — DKMS 빌드만 사용"
    return 1
  fi
  if [[ ! -d "${HOST_LIB_MODULES}/${KVER}" ]]; then
    log_info "host /lib/modules/${KVER} 미접근 — prebuilt 검사 skip"
    return 1
  fi
  # ubuntu prebuilt: linux-modules-nvidia-<major>-<kver> → /lib/modules/<kver>/kernel/drivers/video/nvidia*.ko*
  local found
  found=$(find "${HOST_LIB_MODULES}/${KVER}" -name "nvidia*.ko*" 2>/dev/null | head -n1 || true)
  if [[ -z "${found}" ]]; then
    log_info "host prebuilt nvidia.ko 미발견"
    return 1
  fi
  # major 매칭 — modinfo 가능하면 검증
  local pre_ver=""
  if command -v modinfo >/dev/null 2>&1; then
    pre_ver=$(modinfo -F version "${found}" 2>/dev/null || true)
  fi
  if [[ -n "${pre_ver}" && "${pre_ver%%.*}" != "${MAJOR}" ]]; then
    log_info "host prebuilt nvidia.ko version=${pre_ver} != desired major=${MAJOR} → DKMS 빌드 필요"
    return 1
  fi
  PREBUILT_DIR=$(dirname "${found}")
  log_info "host prebuilt nvidia 모듈 발견: ${PREBUILT_DIR} (version=${pre_ver:-unknown})"
  PREBUILT_OK=1
  return 0
}

# ============================================================================ #
# 5) container 내부 빌드 환경 — kernel-headers 확보
# ============================================================================ #
ensure_kernel_headers() {
  log_step "kernel headers 확보 (${KVER})"
  if [[ -d "/usr/src/linux-headers-${KVER}" ]]; then
    log_info "container 내부 kernel headers 이미 존재 (image prebake)"
    return 0
  fi
  log_warn "linux-headers-${KVER} container 에 없음 → 런타임 install 시도 (container apt)"
  apt-get update -y || { log_error "container apt-get update 실패"; return 1; }
  if ! DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confnew --no-install-recommends "linux-headers-${KVER}"; then
    log_warn "linux-headers-${KVER} 실패 → linux-headers-generic"
    DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confnew --no-install-recommends linux-headers-generic || {
      log_error "kernel-headers 확보 실패 — DKMS 빌드 불가"
      return 1
    }
  fi
  return 0
}

# ============================================================================ #
# 6) container 내부에서 nvidia driver 패키지 설치 — host apt 미사용
# ============================================================================ #
# resolve_pinned_pkg_version <pkg>: 정책 버전이 desired(DESIRED_FROM_POLICY=1)이고 x.y(.z)
#   꼴이면(major 만 적은 값은 고정 안 함) `apt-cache madison` 에서 epoch 를 뗀 값이 접두
#   일치하는 첫 항목을 epoch 포함 원문으로 내고 0, 없으면 후보를 stderr 에 적고 1(대체 없음).
resolve_pinned_pkg_version() {
  local pkg="$1" want="${DRIVER_VERSION_RAW:-}"
  if [[ "${DESIRED_FROM_POLICY:-1}" != "1" || ! "${want}" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
    return 0
  fi
  local line ver cmp
  while IFS= read -r line; do
    ver="$(echo "${line}" | awk -F'|' '{gsub(/[ \t]/,"",$2); print $2}')"
    cmp="${ver#*:}"
    if [[ "${cmp}" == "${want}" || "${cmp}" == "${want}-"* ]]; then
      echo "${ver}"; return 0
    fi
  done < <(apt-cache madison "${pkg}" 2>/dev/null)
  log_error "정책 버전 ${want} 의 ${pkg} 가 저장소에 없다. 후보: $(apt-cache madison "${pkg}" 2>/dev/null | awk -F'|' '{gsub(/[ \t]/,"",$2); print $2}' | tr '\n' ' ')"
  log_error "정책 version 을 저장소에 있는 버전으로 바꾸거나 커널별 사전 컴파일 이미지(kernelImages)를 쓴다"
  return 1
}

install_nvidia_pkg_in_container() {
  log_step "nvidia-driver-${MAJOR} 패키지를 CONTAINER 안에서 install"

  apt-get update -y || { log_error "apt-get update (container) 실패"; return 1; }

  # PPA 시도 (container 안에서만 — host /etc/apt 미수정)
  if ! apt-cache search "^nvidia-driver-${MAJOR}\b" 2>/dev/null | grep -q "nvidia-driver-${MAJOR}"; then
    log_info "ppa:graphics-drivers/ppa 추가 (container 내부)"
    if command -v add-apt-repository >/dev/null 2>&1; then
      add-apt-repository -y ppa:graphics-drivers/ppa >/dev/null 2>&1 || true
    fi
    apt-get update -y || true
  fi

  local pinned
  pinned="$(resolve_pinned_pkg_version "nvidia-driver-${MAJOR}")" || return 1
  local suffix=""
  [[ -n "${pinned}" ]] && suffix="=${pinned}"
  local pkgs=(
    "nvidia-driver-${MAJOR}${suffix}"
    "nvidia-dkms-${MAJOR}${suffix}"
    "nvidia-kernel-source-${MAJOR}${suffix}"
  )
  if [[ -n "${pinned}" ]]; then
    log_info "정책 버전 ${DRIVER_VERSION_RAW} 고정: ${pinned}"
  else
    # major 만 적은 정책 버전(예: 580)은 계열 설치이며 고정하지 않는다 — 호스트 버전 채택 등도 같은 분기
    log_info "고정 없음 — 계열 최신 설치 (DRIVER_VERSION=${DRIVER_VERSION_RAW:-unset})"
  fi
  log_info "install 패키지: ${pkgs[*]}"
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confnew --no-install-recommends "${pkgs[@]}"; then
    return 0
  fi

  if [[ -n "${pinned}" ]]; then
    log_error "정책 버전 고정 상태에서는 -server/-server-open 변형을 시도하지 않는다 — 정책 version 을 바꾸거나 versionSource=Host 로"
    return 1
  fi

  log_warn "nvidia-driver-${MAJOR} install 실패 → -server / -open 변형 시도"
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confnew --no-install-recommends \
        "nvidia-driver-${MAJOR}-server-open" "nvidia-dkms-${MAJOR}-server-open"; then
    return 0
  fi
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confnew --no-install-recommends \
        "nvidia-driver-${MAJOR}-server" "nvidia-dkms-${MAJOR}-server"; then
    return 0
  fi
  log_error "nvidia-driver-${MAJOR}{,-server,-server-open} 모두 실패"
  return 1
}

# ============================================================================ #
# 7) DKMS 명시적 빌드 — `dkms autoinstall` 절대 사용 금지 (다른 vendor 격리)
# ============================================================================ #
dkms_build_explicit() {
  log_step "DKMS 명시적 빌드 (container 내부, autoinstall 미사용)"

  if [[ ! -d "/var/lib/dkms/nvidia" ]]; then
    log_error "/var/lib/dkms/nvidia 디렉토리 없음 — nvidia-dkms 패키지 install 확인 필요"
    return 1
  fi
  local dkms_version
  dkms_version=$(ls -1 "/var/lib/dkms/nvidia" 2>/dev/null | grep -E '^[0-9]' | sort -V | tail -n1 || true)
  if [[ -z "${dkms_version}" ]]; then
    log_error "DKMS nvidia 빌드 가능한 버전을 /var/lib/dkms/nvidia 에서 찾지 못함"
    return 1
  fi
  log_info "DKMS target: nvidia/${dkms_version}, kernel=${KVER}"

  # ★ 절대 dkms autoinstall 사용 금지 — 다른 vendor (furiosa-driver-warboy 등) 빌드 실패 영향 차단
  log_info "dkms build (명시적, autoinstall 미사용)"
  if ! dkms build "nvidia/${dkms_version}" -k "${KVER}"; then
    log_error "dkms build nvidia/${dkms_version} -k ${KVER} 실패 — make.log 확인 필요"
    if [[ -f "/var/lib/dkms/nvidia/${dkms_version}/build/make.log" ]]; then
      tail -n 60 "/var/lib/dkms/nvidia/${dkms_version}/build/make.log" >&2 || true
    fi
    return 1
  fi

  log_info "dkms install (명시적, --force)"
  if ! dkms install "nvidia/${dkms_version}" -k "${KVER}" --force; then
    log_error "dkms install nvidia/${dkms_version} -k ${KVER} 실패"
    return 1
  fi

  if ! ls "/lib/modules/${KVER}/updates/dkms/nvidia.ko"* >/dev/null 2>&1; then
    log_error "DKMS 빌드 산출물 nvidia.ko 가 /lib/modules/${KVER}/updates/dkms/ 에 없음"
    return 1
  fi
  log_info "DKMS 빌드 완료 — /lib/modules/${KVER}/updates/dkms/"
  return 0
}

# ============================================================================ #
# 8) 빌드된 모듈을 host /run/nvidia/driver 에 sideload
# ============================================================================ #
sideload_modules() {
  log_step "모듈 sideload → ${DRIVER_ROOT}"

  local src_dir target_dir
  if [[ ${PREBUILT_OK} -eq 1 ]]; then
    src_dir="${PREBUILT_DIR}"
  else
    src_dir="/lib/modules/${KVER}/updates/dkms"
  fi
  target_dir="${DRIVER_ROOT}/lib/modules/${KVER}/kernel/drivers/video"

  if ! mkdir -p "${target_dir}"; then
    log_error "target_dir 생성 실패: ${target_dir} (DRIVER_ROOT 가 writable bind mount 인지 확인)"
    return 1
  fi

  local copied=0
  shopt -s nullglob
  local f
  for f in "${src_dir}"/nvidia*.ko "${src_dir}"/nvidia*.ko.zst "${src_dir}"/nvidia*.ko.xz "${src_dir}"/nvidia*.ko.gz; do
    [[ -f "${f}" ]] || continue
    if ! cp -f "${f}" "${target_dir}/"; then
      log_error "cp 실패: ${f}"
      shopt -u nullglob
      return 1
    fi
    copied=$((copied + 1))
  done
  shopt -u nullglob

  if [[ ${copied} -eq 0 ]]; then
    log_error "복사된 nvidia 모듈 0개 — src_dir=${src_dir}"
    return 1
  fi
  log_info "${copied} 개 모듈 파일 복사 완료"

  # depmod for sideload tree
  log_info "depmod -b ${DRIVER_ROOT} ${KVER}"
  if ! depmod -b "${DRIVER_ROOT}" -a "${KVER}" 2>/dev/null; then
    log_warn "depmod -b 실패 — modules.dep 누락 가능 (직접 insmod 로 fallback 가능)"
  fi

  # libnvidia-* 도 함께 sideload (nvidia-smi runtime 용)
  local lib_target="${DRIVER_ROOT}/usr/lib/x86_64-linux-gnu"
  mkdir -p "${lib_target}" 2>/dev/null || true
  shopt -s nullglob
  local so
  for so in /usr/lib/x86_64-linux-gnu/libnvidia-*.so* /usr/lib/x86_64-linux-gnu/libcuda.so*; do
    [[ -f "${so}" ]] || continue
    cp -af "${so}" "${lib_target}/" 2>/dev/null || true
  done
  shopt -u nullglob

  return 0
}

# ============================================================================ #
# 9) modprobe / insmod — chroot 또는 직접 insmod
# ============================================================================ #
load_modules() {
  log_step "kernel module 로드"

  # stale 모듈 우선 정리
  rmmod_safely

  # mountpoint 면 chroot modprobe 시도 (의존성 자동 처리)
  if mountpoint -q "${DRIVER_ROOT}" 2>/dev/null; then
    log_info "${DRIVER_ROOT} 는 mountpoint → chroot modprobe 시도"
    if chroot "${DRIVER_ROOT}" modprobe nvidia 2>/dev/null \
       && chroot "${DRIVER_ROOT}" modprobe nvidia_uvm 2>/dev/null \
       && chroot "${DRIVER_ROOT}" modprobe nvidia_drm 2>/dev/null; then
      INSMOD_LOADED=1
      log_info "chroot modprobe 성공"
      return 0
    fi
    log_warn "chroot modprobe 실패 → 직접 insmod fallback"
  else
    log_info "${DRIVER_ROOT} 는 mountpoint 가 아님 → 직접 insmod"
  fi

  # 직접 insmod (의존성 순서)
  local mod_dir="${DRIVER_ROOT}/lib/modules/${KVER}/kernel/drivers/video"
  if [[ ! -d "${mod_dir}" ]]; then
    log_error "module dir 없음: ${mod_dir}"
    return 1
  fi

  # nvidia → nvidia_modeset → nvidia_drm → nvidia_uvm 순
  local m f
  for m in nvidia nvidia-modeset nvidia-drm nvidia-uvm; do
    f=$(ls "${mod_dir}/${m}.ko"* 2>/dev/null | head -n1 || true)
    if [[ -z "${f}" ]]; then
      log_warn "${m}.ko 미존재 (${mod_dir}) — skip"
      continue
    fi
    # 압축 해제
    case "${f}" in
      *.zst) zstd -df "${f}" -o "${mod_dir}/${m}.ko" 2>/dev/null && f="${mod_dir}/${m}.ko" ;;
      *.xz)  xz -df "${f}" 2>/dev/null && f="${mod_dir}/${m}.ko" ;;
      *.gz)  gunzip -f "${f}" 2>/dev/null && f="${mod_dir}/${m}.ko" ;;
    esac
    log_info "insmod ${f}"
    if ! insmod "${f}" 2>&1; then
      log_warn "insmod ${m} 실패 (이미 로드됐거나 의존성 — 다음 모듈 진행)"
    fi
  done
  INSMOD_LOADED=1
  return 0
}

# ============================================================================ #
# 10) healthcheck — container 안에서 직접 (nsenter 미사용)
# ============================================================================ #
container_healthcheck() {
  log_step "container healthcheck"

  if [[ ! -f "/proc/driver/nvidia/version" ]]; then
    log_error "/proc/driver/nvidia/version 없음 (container 내) — 모듈 로드 실패"
    return 1
  fi

  local loaded loaded_major
  loaded="$(parse_nvrm_version /proc/driver/nvidia/version)"
  if [[ -z "${loaded}" ]]; then
    log_error "NVRM version 파싱 실패 — /proc/driver/nvidia/version 출력 dump:"
    sed -n '1,5p' /proc/driver/nvidia/version 2>/dev/null | sed 's/^/  | /' >&2 || true
    return 1
  fi
  loaded_major="${loaded%%.*}"
  if [[ "${loaded_major}" != "${MAJOR}" ]]; then
    log_error "version mismatch: loaded=${loaded}, expected major=${MAJOR}"
    return 1
  fi
  log_info "버전 검증 OK: loaded=${loaded}"

  if command -v nvidia-smi >/dev/null 2>&1; then
    if nvidia-smi -L >/dev/null 2>&1; then
      log_info "nvidia-smi -L OK"
    else
      log_warn "nvidia-smi -L 실패 — /dev/nvidia* 마운트 / 디바이스 노드 확인 필요 (healthcheck 는 통과로 처리)"
    fi
  fi
  return 0
}

# ============================================================================ #
# 11) MAIN
# ============================================================================ #
main() {
  log_info "v17 containerized install 시작 — host immutable 모드"

  mkdir -p "${MARKER_DIR}" 2>/dev/null || true
  mkdir -p "${DRIVER_ROOT}" 2>/dev/null || true

  # idempotency: 이미 sideload + 로드되어 있고 메이저 일치하면 install skip
  local skip_install=0
  if [[ -f "/proc/driver/nvidia/version" ]]; then
    local cur cur_major
    cur="$(parse_nvrm_version /proc/driver/nvidia/version)"
    if [[ -n "${cur}" ]]; then
      cur_major="${cur%%.*}"
      if [[ "${cur_major}" == "${MAJOR}" ]]; then
        log_info "동일 메이저 (${MAJOR}) 이미 로드 (${cur}) — install skip, healthcheck 진행"
        INSMOD_LOADED=1
        skip_install=1
      else
        log_info "기존 로드된 버전 ${cur} (major ${cur_major}) != desired ${MAJOR} → 재설치"
      fi
    else
      log_warn "/proc/driver/nvidia/version 존재하나 NVRM 파싱 실패 — 출력 dump:"
      sed -n '1,5p' /proc/driver/nvidia/version 2>/dev/null | sed 's/^/  | /' >&2 || true
    fi
  fi

  if [[ ${skip_install} -ne 1 ]]; then
    check_prebuilt || true

    if [[ ${PREBUILT_OK} -ne 1 ]]; then
      ensure_kernel_headers || { log_error "kernel-headers 확보 실패"; exit 1; }
      install_nvidia_pkg_in_container || { log_error "container apt install 실패"; exit 1; }
      dkms_build_explicit || { log_error "DKMS 빌드 실패"; exit 1; }
    fi

    sideload_modules || { log_error "sideload 실패"; exit 1; }
    load_modules || { log_error "module 로드 실패"; exit 1; }
  fi

  container_healthcheck || { log_error "healthcheck 실패"; exit 1; }

  # marker (writable bind mount 가정)
  echo "${DRIVER_VERSION_RAW}" > "${MARKER_DIR}/nvidia.ok" 2>/dev/null || true
  touch "${MARKER_DIR}/driver.ready" 2>/dev/null || true
  touch /tmp/driver-ready

  # 정상 흐름 — cleanup trap 의 rmmod 분기를 비활성화
  SKIP_CLEANUP=1
  trap - EXIT

  # RUN_MODE=job: install 완료(container_healthcheck 통과) → exit 0 (상주 루프 없음).
  if [ "${RUN_MODE}" = "job" ]; then
    log_info "RUN_MODE=job — v17 install 완료, install Job 정상 종료(exit 0)"
    exit 0
  fi

  log_info "v17 install 완료 — 상시 모니터링 루프 (interval=${HEALTH_CHECK_INTERVAL}s)"
  while true; do
    sleep "${HEALTH_CHECK_INTERVAL}"
    if ! /usr/local/bin/healthcheck-v17.sh; then
      log_error "healthcheck 실패 — livenessProbe 재시작 트리거"
      exit 1
    fi
  done
}

# 함수 정의만 필요할 때(순수 bash 시험)는 여기서 멈춘다 — 아래 main 은 호스트를 바꾼다.
if [[ "${ENTRYPOINT_LIB_ONLY:-0}" == "1" ]]; then
  return 0 2>/dev/null || exit 0
fi

main "$@"
