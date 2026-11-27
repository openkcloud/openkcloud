#!/usr/bin/env bash
# ============================================================
# entrypoint.sh: NVIDIA 드라이버 및 컨테이너 툴킷 설치 스크립트
# 상세: Host namespace(nsenter)에서 GPU 드라이버를 설치하고, MANAGE_CONTAINER_TOOLKIT=true 일
#       때만 nvidia-ctk·containerd/docker 런타임도 구성한다. 사전 컴파일 모듈이 동봉돼 있으면
#       정합성을 fail-closed 로 확인한 뒤 DKMS 대신 그 모듈을 host 에 설치한다.
# 생성일: 2026-04-17 | 수정일: 2026-11-16
# ============================================================
set -Eeuo pipefail
[[ "${DEBUG:-0}" == "1" ]] && set -x

# ============================================================================ #
# Host namespace helper
# ============================================================================ #
ns() { nsenter --target 1 --mount --uts --ipc --net --pid -- bash -lc "$*"; }

log_info()  { echo "[INFO] $*"; }
log_warn()  { echo "[WARN] $*" >&2; }
log_error() { echo "[ERR]  $*" >&2; }

# ============================================================================ #
# 커널별 prebuilt(Ubuntu linux-modules-nvidia) 헬퍼 — 실제 실행 경로(host-install).
#   build-precompiled 로 구운 이미지는 /opt/nvidia-deb/<kernel>/ 아래에 LRM .deb 를 동봉한다.
#   실행 커널이 맞으면 DKMS 빌드 대신 그 .deb 를 host 에 설치한다.
# ============================================================================ #
PREBUILT_ROOT="${PREBUILT_ROOT:-/opt/nvidia-deb}"

# select_prebuilt_deb <kernel>: 동봉된 그 커널용 linux-modules-nvidia 메타 .deb 경로를 출력하고
#   0 을 반환. 동봉본이 없으면 아무것도 출력하지 않고 1 을 반환한다.
select_prebuilt_deb() {
  local k="$1" f
  [[ -n "${k}" ]] || return 1
  for f in "${PREBUILT_ROOT:-/opt/nvidia-deb}/${k}"/linux-modules-nvidia-*"${k}"_*.deb; do
    [[ -e "${f}" ]] || continue
    echo "${f}"
    return 0
  done
  return 1
}

# install_prebuilt_onto_host <kernel> <major>: 이미지에 구운 사용자 공간과 커널 모듈을
#   host 에 설치한다. 버전은 이미지가 정한다. 실행 중 apt 로 사용자 공간을 고르지 않는다 —
#   그렇게 하면 이미지와 독립적인 두 번째 버전 출처가 생겨 두 값이 어긋날 수 있다(ADR-002).
#   순서는 사용자 공간 먼저다. 동봉 LRM .deb 가 nvidia-kernel-common-<major> 를 요구한다.
install_prebuilt_onto_host() {
  local k="$1" major="$2" src stage usrc missing ver
  select_prebuilt_deb "${k}" >/dev/null 2>&1 || { log_error "동봉 prebuilt .deb 없음 (${k})"; return 1; }
  src="${PREBUILT_ROOT}/${k}"
  usrc="${src}/userspace"
  stage="${MARKER_DIR}/prebuilt-deb/${k}"

  [[ -d "${usrc}" ]] || { log_error "동봉 사용자 공간 없음: ${usrc} — 이 이미지는 v2 이전이다"; return 1; }

  # dpkg -i 는 의존을 스스로 풀지 않는다. 호스트에 없는 외부 의존이 있으면 먼저 알린다.
  missing="$(userspace_missing_host_deps "${usrc}" | tr '\n' ' ')"
  if [[ -n "${missing// /}" ]]; then
    log_error "호스트에 없는 의존: ${missing}— 이미지 굽기 목록에 더해야 한다"
    return 1
  fi

  # host 에 남는 디렉터리라 앞선 시도의 다른 계열 .deb 가 남아 있을 수 있고, 아래 dpkg -i 가
  # 와일드카드로 그것까지 함께 집어 설치를 깨뜨린다. 이 커널 디렉터리만 비우고 상위는 둔다.
  rm -rf "${stage}" || { log_error "stage dir 정리 실패: ${stage}"; return 1; }
  mkdir -p "${stage}" || { log_error "stage dir 생성 실패: ${stage}"; return 1; }
  cp -f "${usrc}"/*.deb "${src}"/*.deb "${stage}/" \
    || { log_error "동봉 .deb 복사 실패 → ${stage}"; return 1; }

  log_info "구운 사용자 공간과 모듈을 host 로 설치 (${k})"
  apt_wait || { log_error "apt/dpkg 잠금 대기 실패(prebuilt)"; return 1; }
  ns "DEBIAN_FRONTEND=noninteractive dpkg -i ${stage}/*.deb" || {
    log_error "prebuilt dpkg -i 실패 (${k}) — 잔재를 정리하고 실패를 반환한다"
    ver="$(prebuilt_ko_version "${k}" 2>/dev/null || true)"
    ns "$(nvidia_prebuilt_cleanup_cmd "${major}" "${k}" "${ver}")" || true
    return 1
  }
  ns "depmod -a ${k}" || { log_error "depmod 실패 (${k})"; return 1; }
  return 0
}

# nvidia_upstream_ver <버전문자열>: epoch·debian revision 을 떼고 major.minor.patch 만 출력.
#   "580.178.04-0ubuntu0.22.04.1" → "580.178.04", "580.126.09" → "580.126.09". 못 뽑으면 빈 문자열.
nvidia_upstream_ver() {
  printf '%s\n' "$1" | grep -oP '^[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
}

# parse_nvrm_version: /proc/driver/nvidia/version 내용을 stdin 으로 받아 드라이버 버전을 낸다.
#   라벨 바로 뒤에서 숫자를 기대하는 정규식은 실제 형식과 맞지 않는다 — 라벨과 버전 사이에
#   "NVIDIA UNIX x86_64 Kernel Module" 같은 문구가 더 있어 빈 값을 낸다. 그 문구를 건너뛰는
#   형식을 먼저 시도하고, 안 되면 느슨한 형식으로 되돌아간다. X.Y·X.Y.Z·X.Y.Z.W 를 모두 받는다.
#   같은 파서가 entrypoint-v17.sh 에도 있다(이미지는 hostnamespace 로 위임만 해 거기선 실행되지
#   않으므로 다시 둔다). 버전을 못 뽑으면 빈 출력과 비0 을 낸다.
parse_nvrm_version() {
  local text v
  text="$(cat)"
  v=$(printf '%s\n' "${text}" | grep -oP 'NVRM version:\s+NVIDIA UNIX[^/]*Kernel Module\s+\K[0-9]+(\.[0-9]+)+' | head -n1 || true)
  [[ -n "${v}" ]] || v=$(printf '%s\n' "${text}" | grep -oP 'NVRM version:[^0-9]*\K[0-9]+(\.[0-9]+)+' | head -n1 || true)
  [[ -n "${v}" ]] || return 1
  printf '%s\n' "${v}"
}

# nvidia_prebuilt_coherent <동봉모듈버전> <정책DRIVER_VERSION>: 사전 컴파일 설치 전에 부르는
#   정합성 판정(순수 함수, fail-closed) — 두 값의 업스트림 접두가 같으면 0(정책이 비면 통과).
#   동봉 모듈과 사용자 공간의 일치는 여기서 보지 않는다 — 둘은 같은 이미지에서 오고 Dockerfile
#   의 빌드 시점 굽기 가드가 이미 보장하므로(ADR-002) 실행 중 아카이브를 다시 조회하지 않는다.
#   어긋나면 두 값을 로그로 남기고 1 을 반환한다. modprobe·apt 설치 없이 판정만 한다.
nvidia_prebuilt_coherent() {
  local bundled_raw="$1" want_raw="$2"
  local bundled want
  bundled="$(nvidia_upstream_ver "${bundled_raw}")"
  want="$(nvidia_upstream_ver "${want_raw}")"
  if [[ -z "${bundled}" ]]; then
    log_error "prebuilt 정합성 실패: 동봉 모듈 버전을 읽지 못함(동봉='${bundled_raw}', 정책='${want_raw}')"
    return 1
  fi
  if [[ -n "${want}" && "${bundled}" != "${want}" ]]; then
    log_error "prebuilt 정합성 실패: 동봉 모듈=${bundled}, 정책 DRIVER_VERSION=${want} — 동봉≠정책"
    return 1
  fi
  return 0
}

# nvidia_prebuilt_cleanup_cmd <major> <kernel>: 사전 컴파일 설치가 실패한 뒤 실행할 정리 명령을
#   출력한다. 미설정 상태로 남은 모듈 패키지는 이후 apt 설치를 막으므로 먼저 지우고 의존을
#   고친다(실패해도 넘어가 정리가 설치 실패를 가리지 않게 한다). host 에는 모듈 .deb 셋
#   (modules·objects·signatures)과 구운 사용자 공간 여섯 종이 함께 설치되므로 모두 대상이다 —
#   사용자 공간이 남으면 다음 시도의 dpkg -i 가 다운그레이드를 거절한다. 제거 순서는 의존
#   방향(모듈→kernel-common→firmware)과 맞춘다. firmware 는 <major>-<version>, signatures 는 계열 없이 커널만 든다.
nvidia_prebuilt_cleanup_cmd() {
  local major="$1" k="$2" ver="$3" p out=""
  for p in "linux-modules-nvidia-${major}-${k}" "linux-objects-nvidia-${major}-${k}" \
           "linux-signatures-nvidia-${k}" "nvidia-utils-${major}" "nvidia-compute-utils-${major}" \
           "libnvidia-cfg1-${major}" "libnvidia-compute-${major}" "nvidia-kernel-common-${major}" \
           "nvidia-firmware-${major}-${ver}"; do
    out="${out} ${p}"
  done
  printf '%s\n' "dpkg --remove --force-remove-reinstreq${out} 2>/dev/null || true; DEBIAN_FRONTEND=noninteractive apt-get install -y --fix-broken || true"
}

# prebuilt_ko_version <kernel>: 이미지에 동봉된 그 커널용 nvidia.ko 의 modinfo version 을 출력한다.
#   .ko 는 컨테이너 로컬 파일이라 ns 없이 컨테이너 modinfo 로 읽는다. 없으면 빈 문자열 + 비0.
prebuilt_ko_version() {
  local k="$1" ko
  for ko in "${PREBUILT_ROOT:-/opt/nvidia-deb}/${k}/lib/modules/${k}/kernel"/nvidia-*/nvidia.ko; do
    [[ -e "${ko}" ]] || continue
    modinfo -F version "${ko}" 2>/dev/null | head -1
    return 0
  done
  return 1
}

# ============================================================================ #
# apt / dpkg helpers — apt_wait 는 설치 전 점검(preflight.sh)과 설치 단계가 함께 부르므로 ENTRYPOINT_LIB_ONLY 가드 앞에 둔다(나머지는 설치 단계용)
# ============================================================================ #

# apt_wait: host 의 dpkg/apt 잠금이 풀릴 때까지 2초 간격으로 최대 180회 기다린다(풀리면 0, 시간 초과면 오류를 적고 1).
#   apt 를 부르는 모든 경로(설치·고정 설치·사전 컴파일 설치·설치 전 점검)가 먼저 부르며, host 를 건드리므로 시험은 가짜로 덮는다.
apt_wait() {
  ns "bash -lc '
    for i in {1..180}; do
      if ! fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
         && ! fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
         && ! fuser /var/lib/apt/lists/lock >/dev/null 2>&1; then
        exit 0
      fi
      sleep 2
    done
    echo \"[ERR] dpkg/apt lock held too long\" >&2
    exit 1
  '"
}

apt_update() {
  apt_wait || { log_error "apt/dpkg 잠금 대기 실패"; return 1; }
  ns "apt-get update -y" || { log_error "apt-get update failed"; return 1; }
}

apt_install() {
  apt_wait || { log_error "apt/dpkg 잠금 대기 실패"; return 1; }
  ns "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $*" || return 1
}

# ============================================================================ #
# 안전장치 헬퍼: passthrough 감지
# ============================================================================ #

# 호스트 PCI 를 순회하며 NVIDIA GPU 의 "<total> <installable>" 개수를 출력한다.
#   total       : NVIDIA(0x10de) & display class(0x03xxxx) 장치 총 개수
#   installable : 그 중 driver 바인딩이 vfio-pci 가 아닌(=nvidia/미바인딩) 장치 개수
# 집계는 단일 ns 호출(호스트 mount namespace)에서 수행하여 왕복/쿼팅을 최소화한다.
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

# 설치 대상 GPU 존재 여부.
#   반환 0(true)  : installable GPU 가 1개 이상 있거나, GPU 가 아예 없는 비-GPU 노드(가드 미적용)
#   반환 1(false) : GPU 는 있으나 전량 vfio-pci 바인딩(passthrough)
nvidia_has_installable_gpu() {
  local out total installable
  out=$(nvidia_gpu_bindings 2>/dev/null | tail -n1 || echo "0 0")
  total=$(echo "$out" | awk '{print $1+0}')
  installable=$(echo "$out" | awk '{print $2+0}')
  # 비-GPU 노드는 가드 미적용 → 기존 동작 유지
  [ "${total:-0}" -eq 0 ] && return 0
  [ "${installable:-0}" -gt 0 ] && return 0
  return 1
}

# ============================================================================ #
# 정책 버전 고정과 후보 설치 루프(host apt 경로) — 발행 이미지의 실제 설치 경로. 아래 함수는
#   설치 블록(install_from_candidates·"모든 후보 실패" 분기·install_and_activate_driver)이 부르지만
#   단위 시험이 실제 본문을 source 할 수 있어야 하므로 ENTRYPOINT_LIB_ONLY 가드 앞에 둔다.
#   install_and_activate_driver 는 가드 뒤에 정의되므로 lib-only 로 source 한 시험은 stub 으로 둔다
#   (apt_wait 는 가드 앞에 있지만 host 를 부르므로 시험은 stub 으로 덮는다).

# resolve_pinned_pkg_version <pkg>: 이 시점의 DRIVER_VERSION(정책 값)에 맞는 host apt 패키지 버전을 찾는다
#   (VERSION_SOURCE=Host 로 호스트 값을 채택한 경우는 SKIP_DRIVER_INSTALL=1 이라 이 경로에 오지 않는다).
#   x.y(.z) 꼴일 때만 고정 대상이다(major 만 적은 580 같은 값은 계열 설치, 고정 안 함). `apt-cache madison`
#   에서 epoch 를 뗀 값이 접두 일치하는 첫 항목을 epoch 포함 원문으로 stdout 에 내고 0, 없으면 후보를 stderr 에
#   적고 1(대체 없음). 고정 대상이 아니면 빈 출력·0(종전 계열 설치). 호출부가 $(...) 로 값을 받으므로 stdout 에는
#   값 말고 아무것도 쓰지 않는다.

# PIN_LOOKUP_QUIET 를 켠 호출(설치 전 점검: PPA 추가 전이라 부재가 정상)에서는 아무것도 적지 않고 1 만 돌려준다 —
#   부재를 어떻게 알릴지는 호출부가 정한다(run_preflight 는 판정 불가 경고 한 줄을 로그와 종료 메시지에 같은 문장으로
#   남기므로, 여기서도 적으면 같은 사실이 두 번 남는다).
resolve_pinned_pkg_version() {
  local pkg="$1" want="${DRIVER_VERSION:-}"
  if [[ ! "${want}" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
    return 0
  fi
  local line ver cmp
  while IFS= read -r line; do
    ver="$(echo "${line}" | awk -F'|' '{gsub(/[ \t]/,"",$2); print $2}')"
    cmp="${ver#*:}"
    if [[ "${cmp}" == "${want}" || "${cmp}" == "${want}-"* ]]; then
      echo "${ver}"; return 0
    fi
  done < <(ns "apt-cache madison ${pkg}" 2>/dev/null)
  [[ -z "${PIN_LOOKUP_QUIET:-}" ]] || return 1
  log_error "정책 버전 ${want} 의 ${pkg} 가 host 저장소에 없다. 후보: $(ns "apt-cache madison ${pkg}" 2>/dev/null | awk -F'|' '{gsub(/[ \t]/,"",$2); print $2}' | tr '\n' ' ')"
  log_error "정책 version 을 저장소에 있는 버전으로 바꾸거나 커널별 사전 컴파일 이미지(kernelImages)를 쓴다"
  return 1
}

# installed_sibling_pkgs <major>: 설치된 nvidia-driver-<major> 와 같은 소스 패키지에서 나온
#   설치(ii) 패키지를 한 줄에 하나 낸다(메타·dkms·kernel-source 셋 제외). 같은 소스·같은 버전의
#   바이너리는 저장소에 함께 올라오므로 고정 버전에 함께 묶어야 apt 가 하향·상향을 푼다. 이름에
#   버전이 든 패키지(nvidia-firmware-580-580.178.04)는 이름이 버전마다 달라 제외한다 — 맞는
#   패키지는 고정한 nvidia-kernel-common 의존으로 apt 가 가져온다. 메타패키지가 없으면 아무것도 내지 않는다.
#   목록 조회가 실패하면 stderr 에 경고하고 아무것도 내지 않는다.
installed_sibling_pkgs() {
  local major="$1" src list
  src="$(ns "dpkg-query -W -f='\${source:Package}' nvidia-driver-${major} 2>/dev/null" || true)"
  [[ -n "${src}" ]] || return 0
  if ! list="$(ns "dpkg-query -W -f='\${db:Status-Abbrev} \${binary:Package} \${source:Package}\n' 2>/dev/null")"; then
    log_warn "같은 소스(${src}) 설치 패키지 조회 실패 — 메타·dkms·kernel-source 셋만 고정한다(사용자 공간 버전이 다르면 apt 가 거부한다)"
    return 0
  fi
  awk -v s="${src}" -v m="${major}" '$1 == "ii" && $3 == s {
      n = $2; sub(/:.*/, "", n)
      if (n ~ /-[0-9]+\.[0-9]+/) next
      if (n != "nvidia-driver-" m && n != "nvidia-dkms-" m && n != "nvidia-kernel-source-" m) print $2
    }' <<< "${list}"
}

# 설치를 막는 hold 충돌 종료 코드. operator 의 driverjob.ExitCodeHostPackageConflict 와 같은
#   값이다(kcloud-operator internal/driverjob/preflightjob.go). 값을 바꾸면 두 곳을 함께 바꾼다.
EXIT_HOLD_CONFLICT=3

# unmet_dep_targets <apt 출력 파일>: "unmet dependencies" 절의 의존 대상(Depends·PreDepends·Breaks·
#   Conflicts 오른쪽)을 한 줄에 하나 낸다. 아키텍처 접미사는 뗀다.
unmet_dep_targets() {
  awk '
    /^The following packages have unmet dependencies:/ { s = 1; next }
    s && /^E:/ { s = 0 }
    s {
      line = $0
      while (match(line, /(Pre)?Depends: [^ ]+|Breaks: [^ ]+|Conflicts: [^ ]+/)) {
        t = substr(line, RSTART, RLENGTH); sub(/^[A-Za-z]+: /, "", t); sub(/:.*/, "", t); print t
        line = substr(line, RSTART + RLENGTH)
      }
    }' "$1" | sort -u
}

# unmet_dep_requirers <apt 출력 파일>: 같은 절에서 의존을 요구하는 쪽(줄 맨 앞 "<패키지> :")을 낸다.
unmet_dep_requirers() {
  awk '
    /^The following packages have unmet dependencies:/ { s = 1; next }
    s && /^E:/ { s = 0 }
    s && match($0, /^ *[^ ]+ :/) {
      n = substr($0, RSTART, RLENGTH); sub(/ :$/, "", n); gsub(/ /, "", n); sub(/:.*/, "", n); print n
    }' "$1" | sort -u
}

# unmet_dep_pkgs <apt 출력 파일>: 두 목록의 합집합. 운영 코드는 부르지 않는다 — hold 지목(held_conflict_pkgs)은 두 목록을
#   따로 읽는다. 호출부는 단위 시험(held-conflict_test.sh 사례 1)뿐이다.
unmet_dep_pkgs() { { unmet_dep_targets "$1"; unmet_dep_requirers "$1"; } | sort -u; }

# held_change_pkgs <apt 출력 파일>: "held packages will be changed" 절의 패키지를 낸다(설치
#   목록에 hold 된 패키지가 든 경우). 아키텍처 접미사는 뗀다.
held_change_pkgs() {
  awk '
    /^The following held packages will be changed:/ { s = 1; next }
    s && /^  / { for (i = 1; i <= NF; i++) { n = $i; sub(/:.*/, "", n); print n }; next }
    s { s = 0 }' "$1" | sort -u
}

# held_conflict_pkgs <apt 출력 파일>: 이 거절에 걸린 hold 패키지를 낸다. unmet 절은 apt-mark
#   showhold 와 교집합만 본다 — "held broken packages" 문구는 hold 가 없어도 나온다. 의존 대상 쪽
#   (unmet_dep_targets)의 hold 를 먼저 지목하고, 그쪽에 hold 가 없을 때만 요구하는 쪽(unmet_dep_requirers)의
#   hold 를 쓴다 — 대상이 hold 면 그것이 막은 원인이고, 요구하는 쪽까지 지목하면 unhold 안내가 넓어진다.
#   "held packages will be changed" 절의 패키지는 교집합과 무관하게 더한다.
held_conflict_pkgs() {
  local holds hits=""
  holds="$(ns "apt-mark showhold" 2>/dev/null | sed 's/:.*//' | sort -u)" || holds=""
  {
    if [[ -n "${holds}" ]]; then
      hits="$(unmet_dep_targets "$1" | grep -Fx -f <(printf '%s\n' "${holds}") || true)"
      [[ -n "${hits}" ]] || hits="$(unmet_dep_requirers "$1" | grep -Fx -f <(printf '%s\n' "${holds}") || true)"
      [[ -z "${hits}" ]] || printf '%s\n' "${hits}"
    fi
    held_change_pkgs "$1"
  } | sort -u
}

# hold_conflict_message <apt 출력 파일> <hold 패키지 목록>: 상태·이벤트에 그대로 실릴 한 줄.
#   hold 패키지 이름을 맨 앞에 두고 apt 의 거절 줄(Depends·PreDepends·Breaks·Conflicts 줄, E: 줄)을 최대
#   세 줄 붙인다. 세 줄은 grep -m3 로 자른다 — head 로 자르면 출력이 클 때 grep 이 SIGPIPE 로 끝나
#   pipefail 아래서 거절 줄이 빈 값이 된다.
hold_conflict_message() {
  local held lines
  held="$(printf '%s\n' "$2" | paste -sd' ' -)"
  lines="$(grep -m3 -E ' : (Pre)?Depends: | : Breaks: | : Conflicts: |^E: ' "$1" | sed 's/^ *//' | paste -sd' ' -)" || lines=""
  printf 'hold 된 패키지(%s)가 드라이버 설치를 막는다 — %s — 노드에서 apt-mark showhold 로 확인하고, hold 를 풀지는 노드 이미지 관리자가 정한다(apt-mark unhold %s)' \
    "${held}" "${lines}" "${held}"
}

# write_termination_message <문장>: 컨테이너 종료 메시지(/dev/termination-log)에 앞 2048 바이트를
#   쓴다. operator 가 이 값을 DriverUpgradeState 메시지에 싣는다. 쓰기 실패는 무시한다.
write_termination_message() {
  printf '%s' "$1" | head -c 2048 | iconv -c -f UTF-8 -t UTF-8 2>/dev/null >"${TERMINATION_LOG:-/dev/termination-log}" || true
}

# dkms_build_pkgs: DKMS 로 모듈을 빌드할 때 설치 경로가 apt 로 까는 빌드 도구. 헤더·모듈 확인 뒤의 빌드 도구
#   설치와 설치 전 점검이 이 목록을 쓴다(기반 도구 설치 줄의 build-essential dkms 는 따로 적혀 있다).
dkms_build_pkgs() { printf '%s' "build-essential dkms gcc make"; }

# kernel_headers_present <커널>·kernel_modules_present <커널>: 호스트에 그 커널의 헤더·모듈 디렉터리가
#   있는지. 설치 경로(없으면 apt 설치)와 설치 전 점검(없으면 모의 설치 목록에 추가)이 같은 판정을 쓴다.
kernel_headers_present() { ns "[[ -d /usr/src/linux-headers-$1 ]]"; }
kernel_modules_present() { ns "[[ -d /lib/modules/$1 ]]"; }

# missing_kernel_pkgs <커널>: 호스트에 없어 설치 경로가 apt 로 깔 커널 패키지 이름을 낸다(없으면 빈 값).
missing_kernel_pkgs() {
  local out=""
  kernel_headers_present "$1" || out="linux-headers-$1"
  kernel_modules_present "$1" || out="${out:+${out} }linux-modules-extra-$1"
  printf '%s' "${out}"
}

# simulate_apt_install <출력 파일> <apt 인자>: apt 설치를 모의(-s -y)하고 출력을 파일에 쓴다. apt 의
#   종료 상태를 돌려준다. 출력의 영어 문구를 파싱하므로 LC_ALL=C 로 번역을 끈다(출력을 읽지 않는
#   실제 설치 명령에는 붙이지 않는다). 인자는 호출부가 정한다. 빌드 도구는 설치 경로의 apt_install 과
#   같은 인자(--no-upgrade 포함)로 모의한다(run_preflight 참고).
simulate_apt_install() {
  ns "LC_ALL=C DEBIAN_FRONTEND=noninteractive apt-get -s -y install $2" >"$1" 2>&1
}

# simulate_driver_install <출력 파일>: DRIVER_PKG_OPTS·DRIVER_PKG_LIST 설치를 모의하고 출력을 파일에
#   쓴다. apt 의 종료 상태를 돌려준다. 실제 설치와 같은 인자다.
simulate_driver_install() {
  simulate_apt_install "$1" "${DRIVER_PKG_OPTS:+${DRIVER_PKG_OPTS} }${DRIVER_PKG_LIST}"
}

# classify_hold_conflict <apt 출력 파일>: 이 apt 거절이 hold 충돌인지 판정한다. 충돌이면
#   HOLD_CONFLICT_MSG 에 사유를 남기고 log_error 한 뒤 0, 아니면 1. diagnose_hold_conflict(설치 실패
#   뒤)와 run_preflight(설치 전 점검)가 이 판정 하나를 쓴다.
classify_hold_conflict() {
  local held
  held="$(held_conflict_pkgs "$1")"
  [[ -n "${held}" ]] || return 1
  HOLD_CONFLICT_MSG="$(hold_conflict_message "$1" "${held}")"
  log_error "${HOLD_CONFLICT_MSG}"
  return 0  # 판정은 로그 출력과 무관하다 — stderr 가 닫혀 log_error 가 실패해도 충돌이다
}

# diagnose_hold_conflict: 설치가 실패한 뒤 같은 인자로 모의 설치(simulate_driver_install)하고 그
#   출력을 classify_hold_conflict 로 판정한다. 충돌이면 HOLD_CONFLICT_MSG 에 사유를 남기고 0,
#   아니면 1. hold 는 바꾸지 않는다.
diagnose_hold_conflict() {
  local out rc=1
  out="$(mktemp)"
  simulate_driver_install "${out}" || true
  classify_hold_conflict "${out}" && rc=0
  rm -f "${out}"
  return "${rc}"
}

# driver_pkg_args <pkg>: <pkg> 를 설치할 apt 인자를 정한다. 설치(install_driver_pkg_on_host)와
#   설치 전 점검(run_preflight)이 같은 목록을 쓰도록 결정은 여기 하나에만 둔다. pkg 가 정책
#   계열 메타패키지(nvidia-driver-<major>, major==DRIVER_VERSION 의 major)일 때만 정책 버전으로
#   고정하고 같은 소스의 설치된 패키지도 같은 버전으로 묶는다. 결과는 전역 DRIVER_PKG_OPTS·
#   DRIVER_PKG_LIST·DRIVER_PKG_PINNED 에 남긴다. 반환: 0 정함, 2 정책 버전이 저장소에 없음.
driver_pkg_args() {
  local pkg="$1" want="${DRIVER_VERSION:-}" pinned="" major sib
  DRIVER_PKG_OPTS=""
  DRIVER_PKG_LIST="${pkg}"
  DRIVER_PKG_PINNED=""
  if [[ "${pkg}" =~ ^nvidia-driver-([0-9]+)$ && "${BASH_REMATCH[1]}" == "${want%%.*}" ]]; then
    # local 선언과 대입을 나눠야 피호출자의 종료 상태가 보존된다.
    pinned="$(resolve_pinned_pkg_version "${pkg}")" || return 2
  fi
  [[ -n "${pinned}" ]] || return 0
  major="${want%%.*}"
  DRIVER_PKG_PINNED="${pinned}"
  DRIVER_PKG_LIST="nvidia-driver-${major}=${pinned} nvidia-dkms-${major}=${pinned} nvidia-kernel-source-${major}=${pinned}"
  while IFS= read -r sib; do
    [[ -n "${sib}" ]] && DRIVER_PKG_LIST+=" ${sib}=${pinned}"
  done < <(installed_sibling_pkgs "${major}")
  DRIVER_PKG_OPTS="--allow-downgrades"
}

# install_driver_pkg_on_host <pkg>: host apt 로 드라이버 패키지를 설치한다. 인자는 driver_pkg_args
#   가 정한다. 반환: 0 성공, 1 고정 없는 설치 실패, 2 고정 관련 실패(저장소에 정책 버전 부재
#   또는 hold 가 아닌 고정 설치 실패), 3 hold 된 호스트 패키지가 설치를 막음(EXIT_HOLD_CONFLICT)
#   — 2·3 은 호출부에서 다른 후보로 대체하지 않는다.
install_driver_pkg_on_host() {
  local pkg="$1" want="${DRIVER_VERSION:-}"
  # madison 조회도 apt 락을 봐야 한다 — 다른 apt 작업이 색인을 다시 쓰는 중이면 출력이
  # 불완전해, 저장소에 있는 정책 버전을 없다고 판정한다.
  apt_wait || { log_error "apt/dpkg 잠금 대기 실패 — 고정 설치를 진행하지 않는다"; return 2; }
  driver_pkg_args "${pkg}" || return 2
  if [[ -n "${DRIVER_PKG_PINNED}" ]]; then
    log_info "정책 버전 ${want} 고정: ${DRIVER_PKG_PINNED} (${DRIVER_PKG_LIST})"
  else
    log_info "고정 없음 — 계열 최신 설치 (DRIVER_VERSION=${want:-unset})"
  fi
  if ns "DEBIAN_FRONTEND=noninteractive apt-get install -y ${DRIVER_PKG_OPTS:+${DRIVER_PKG_OPTS} }${DRIVER_PKG_LIST}"; then
    return 0
  fi
  if diagnose_hold_conflict; then
    return "${EXIT_HOLD_CONFLICT}"
  fi
  if [[ -n "${DRIVER_PKG_PINNED}" ]]; then
    log_error "정책 버전 고정 상태에서는 다른 후보로 대체하지 않는다 — 정책 version 을 바꾸거나 versionSource=Host 로"
    log_error "고정 설치 목록: ${DRIVER_PKG_LIST} — 고정 목록은 같은 버전으로 묶이며 하나라도 저장소에 없으면 실패한다"
    return 2
  fi
  return 1
}

# collect_reboot_guard_state: 재부팅 유예 가드가 읽는 디스크 상태를 TARGET_PKG_OK·
#   DISK_MOD_VER·DISK_MOD_MAJOR 전역에 남긴다. 패키지도 모듈도 없는 새 노드(설치 자체가
#   실패한 가장 흔한 경로)에서는 dpkg-query·modinfo 가 비0 으로 끝나는데, set -Eeuo pipefail
#   아래서 이 비0 이 보호 없이 올라가면 "All driver candidates failed" 진단(log_error) 전에
#   스크립트가 조용히 끊긴다 — 두 조회 모두 `|| true` 로 종료 상태만 삼킨다(값은 그대로
#   0·빈 문자열). grep -c 는 무매치에도 0 을 낸다.
collect_reboot_guard_state() {
  TARGET_PKG_OK=$(ns "dpkg-query -W -f='\${Status}' nvidia-driver-${EXPECTED_MAJOR} 2>/dev/null" | grep -c 'install ok installed' || true)
  DISK_MOD_VER=$(ns "modinfo -k ${CURRENT_KERNEL} -F version nvidia 2>/dev/null" | head -1 || true)
  DISK_MOD_MAJOR="${DISK_MOD_VER%%.*}"
}

# reboot_defer_applicable: 재부팅 유예 가드(cross-major 전이와 같은 계열 patch 교체). PIN_FAILED 를
#   첫 조건으로 두는 이유 — PIN_FAILED=1 은 정책 버전 고정 실패(후보가 2)와 hold 충돌(후보가 3)에서 선다.
#   고정 실패 뒤에는 정책이 아닌 버전이 디스크에 있을 수 있고, hold 충돌을 유예하면 정상 종료(재부팅 위임)로
#   가려지므로 둘 다 유예하지 않고 exit_install_failed 로 보낸다(hold 충돌은 종료 코드 3).

# 정책이 x.y.z 이면 디스크 모듈이 그 patch 와 같고 로드된 모듈이 그 patch 가 아닐 때 참이다(같은 계열 patch
#   교체 포함). major 만 적은 정책은 로드된 major 가 다를 때 참이다. 부팅 뒤 검증기(operator 의
#   internal/validator/validator.go)는 로드된 모듈 버전만 보고 dpkg 메타패키지는 읽지 않는다. 사전 컴파일은
#   메타패키지를 설치하지 않아 그 값이 옛 apt 이력이므로 조건에 넣지 않는다.
reboot_defer_applicable() {
  [[ "${PIN_FAILED:-0}" != "1" \
        && -n "${EXPECTED_MAJOR}" && "${TARGET_PKG_OK}" -ge 1 \
        && "${DISK_MOD_MAJOR}" == "${EXPECTED_MAJOR}" ]] || return 1
  if [[ "${DRIVER_VERSION:-}" == *.*.* ]]; then
    [[ "${DISK_MOD_VER:-}" == "${DRIVER_VERSION}" && "${ACTIVE_NVRM:-}" != "${DRIVER_VERSION}" ]]
    return
  fi
  [[ "${ACTIVE_MAJOR}" != "${EXPECTED_MAJOR}" ]]
}

# loaded_needs_reboot <로드된 버전> <정책 버전> <설치 생략 0|1>: 설치 뒤 로드된 모듈이 정책과 달라
#   재부팅으로만 교체되는지. 정책이 x.y.z 면 전체 버전, major 만이면 major 를 비교한다. 설치를
#   건너뛴 실행(다운그레이드 가드·동일 버전·Host 채택)은 정책 값이 로드 버전과 다를 수 있어 판정하지 않는다.
loaded_needs_reboot() {
  local loaded="$1" want="$2" skipped="$3"
  [[ "${skipped}" != "1" && "${loaded}" != "unknown" && -n "${loaded}" && -n "${want}" ]] || return 1
  if [[ "${want}" == *.*.* ]]; then
    [[ "${loaded}" != "${want}" ]]
    return
  fi
  [[ "${loaded%%.*}" != "${want%%.*}" ]]
}

# install_from_candidates: DRIVER_CANDIDATES 를 순서대로 시도한다. 결과는 전역에 남긴다 — INSTALL_SUCCESS=1
#   설치·활성 성공, PIN_FAILED=1 정책 버전 고정 실패(후보가 2 를 돌려줌) 또는 hold 충돌, HOLD_CONFLICT=1
#   hold 충돌(후보가 3 을 돌려줌). rc 2 에서 남은 후보로 대체하지 않는 것이 이 루프가 지키는 규칙이다 —
#   대체하면 정책이 아닌 버전이 조용히 깔린다. 전역을 쓰는 채로 함수로 감싼 이유는 단위 시험이 그 규칙을
#   직접 부를 수 있게 하는 것뿐이다. local 로 바꾸면 호출부와 reboot_defer_applicable 이 결과를 보지 못한다.
install_from_candidates() {
  INSTALL_SUCCESS=0
  # 고정 실패·hold 충돌 표식 — reboot_defer_applicable 이 읽는다. set -u 때문에 루프 앞에서 초기화한다.
  PIN_FAILED=0
  HOLD_CONFLICT=0
  for cand in "${DRIVER_CANDIDATES[@]}"; do
    if [[ "${cand}" == "__UBUNTU_DRIVERS_AUTOINSTALL__" ]]; then
      log_info "Trying ubuntu-drivers autoinstall as last resort"
    else
      log_info "Trying driver candidate: ${cand}"
    fi

    if install_and_activate_driver "${cand}" "${EXPECTED_MAJOR}"; then
      INSTALL_SUCCESS=1
      break
    else
      cand_rc=$?
      log_warn "Driver candidate failed: ${cand}"
      if [[ "${cand_rc}" -eq "${EXIT_HOLD_CONFLICT}" ]]; then
        # hold 된 호스트 패키지가 막았다 — 다른 후보도 같은 hold 에 막히므로 대체하지 않는다.
        # PIN_FAILED=1 은 아래 "모든 후보 실패" 경로의 재부팅 유예 가드를 건너뛰게 한다.
        log_error "hold 된 호스트 패키지가 설치를 막는다 — 남은 후보로 대체하지 않는다"
        HOLD_CONFLICT=1
        PIN_FAILED=1
        break
      fi
      if [[ "${cand_rc}" -eq 2 ]]; then
        # 정책 버전 고정 실패 — 계열 최신·수동 지정 후보로 대체하면 정책이 아닌 버전이
        # 조용히 깔린다. 다음 후보로 넘어가지 않고 아래 "모든 후보 실패" 경로로 간다.
        # PIN_FAILED=1 은 그 경로의 재부팅 유예 가드를 건너뛰게 한다.
        log_error "정책 버전 고정 실패 — 남은 후보로 대체하지 않는다"
        PIN_FAILED=1
        break
      fi
    fi
  done
}

# apply_reboot_defer: 모든 후보가 실패한 뒤 재부팅 유예를 적용한다. 0 = needs-reboot 마커를 남겼다
#   (호출부가 정상 종료로 재부팅을 위임), 1 = 유예할 수 없거나 rebootStrategy=Never(호출부가
#   실패로 끝낸다). 종료는 호출부가 정한다 — 단위 시험이 이 함수를 직접 부른다.
apply_reboot_defer() {
  reboot_defer_applicable || return 1
  case "${REBOOT_STRATEGY:-IfNeeded}" in
    Never)
      log_error "reboot-deferred(모듈 교체, target ${DRIVER_VERSION:-${EXPECTED_MAJOR}}, active=${ACTIVE_NVRM:-none}) 이나 rebootStrategy=Never — 재부팅 없이 신 모듈 로드 불가 → install 실패"
      return 1 ;;
  esac
  log_warn "reboot-deferred(모듈 교체, target ${DRIVER_VERSION:-${EXPECTED_MAJOR}} 모듈 디스크 준비 완료, active=${ACTIVE_NVRM:-none/broken} — 구 모듈 busy/broken) — needs-reboot 마커 생성 후 정상 종료(reboot 위임)."
  ns "mkdir -p ${MARKER_DIR}; touch ${MARKER_DIR}/needs-reboot" || true
  return 0
}

# exit_install_failed: 모든 후보가 실패했을 때 사유를 종료 메시지에 쓰고 프로세스를 끝낸다. hold 충돌
#   (HOLD_CONFLICT=1)이면 EXIT_HOLD_CONFLICT 로, 그 밖이면 1 로 끝난다. 재부팅 유예(apply_reboot_defer)가 정상 종료로
#   끝나지 않은 경우에만 오며, hold 충돌은 PIN_FAILED 가 유예를 막으므로 항상 여기서 끝난다. 단위 시험이 하위 셸에서
#   종료 상태와 종료 메시지를 확인할 수 있도록 가드 앞에 둔다. 호출부는 "모든 후보 실패" 분기 한 곳이다.
exit_install_failed() {
  if [[ "${HOLD_CONFLICT:-0}" == "1" ]]; then
    write_termination_message "${HOLD_CONFLICT_MSG:-hold 된 호스트 패키지가 드라이버 설치를 막는다}"
    exit "${EXIT_HOLD_CONFLICT}"
  fi
  write_termination_message "드라이버 설치 실패 — install Job 로그의 apt·DKMS 출력을 확인한다"
  log_error "All driver candidates failed; see the apt and DKMS output above."
  exit 1
}

# detect_existing_driver_version: 호스트의 NVIDIA 드라이버 버전을 전역 EXISTING_VER 에, 출처를
#   EXISTING_VER_SOURCE(loaded|package)에 둔다. 로드된 모듈이 먼저, 없으면 설치된 nvidia-driver-*
#   패키지의 upstream 버전. 없으면 둘 다 빈 값. /proc/driver/nvidia/version 은 있으나 버전을 읽지 못하면
#   EXISTING_VER_UNPARSED=1(그 밖은 0)로 알린다 — 설치 경로가 그 경우의 경고를 남긴다. stdout 에 쓰지 않는다
#   (호출부가 값을 캡처하지 않는다).
detect_existing_driver_version() {
  EXISTING_VER=""; EXISTING_VER_SOURCE=""; EXISTING_VER_UNPARSED=0
  if ns "test -f /proc/driver/nvidia/version" 2>/dev/null; then
    EXISTING_VER="$(ns "cat /proc/driver/nvidia/version 2>/dev/null" | parse_nvrm_version || true)"
    [[ -z "${EXISTING_VER}" ]] || EXISTING_VER_SOURCE="loaded"
    [[ -n "${EXISTING_VER}" ]] || EXISTING_VER_UNPARSED=1
  fi
  # driver-manager 가 모듈을 해제한 경우에도 설치된 패키지 버전을 본다.
  if [[ -z "${EXISTING_VER}" ]]; then
    local pkg
    pkg="$(ns "dpkg-query -W -f='\${Status} \${Version}\n' 'nvidia-driver-*' 2>/dev/null | grep '^install' | awk '{print \$4}' | head -1" || true)"
    if [[ -n "${pkg}" ]]; then
      # dpkg 버전에서 upstream 버전만 추출 (580.126.09-0ubuntu... → 580.126.09)
      EXISTING_VER="$(echo "${pkg}" | grep -oP '^[0-9]+\.[0-9]+\.[0-9]+' || echo "${pkg}")"
      EXISTING_VER_SOURCE="package"
    fi
  fi
}

# host_driver_adopted: VERSION_SOURCE=Host 이고 호스트에 드라이버가 있으면 0 — 설치기는 그 버전을
#   채택하고 설치하지 않는다. 설치 경로와 설치 전 점검이 이 판정 하나를 쓴다.
host_driver_adopted() { [[ "${VERSION_SOURCE:-Policy}" == "Host" && -n "${EXISTING_VER:-}" ]]; }

# run_preflight: 설치 전 점검. 설치가 쓸 apt 인자로 모의 설치(-s -y)를 돌려 hold 된 호스트 패키지가 설치를
#   막는지 본다. 드라이버 세트가 통과하면 DKMS 빌드에 필요한 커널 헤더·빌드 도구도 설치 경로와 같은 인자(빌드
#   도구는 --no-upgrade)로 모의 설치한다. VERSION_SOURCE=Host 이고 호스트에 드라이버가 있으면 설치기가 그
#   버전을 채택하고 설치하지 않으므로 점검하지 않는다. 호스트에는 apt 목록 갱신만 하고 패키지를 설치·제거·hold
#   하지 않는다. 반환: 0 통과 또는 점검 대상 아님, EXIT_HOLD_CONFLICT hold 충돌, 1 판정 불가(operator 는 설치를
#   막지 않는다). 사유는 종료 메시지에 남긴다. preflight.sh 가 부른다.
run_preflight() {
  local major sim msg kpkgs
  if [[ "${SKIP_ON_PASSTHROUGH:-true}" != "false" ]] && ! nvidia_has_installable_gpu; then
    log_info "설치 전 점검: GPU 가 모두 vfio-pci 에 묶여 설치 대상이 없다 — 통과"
    return 0
  fi
  CURRENT_KERNEL="$(ns "uname -r")"
  if select_prebuilt_deb "${CURRENT_KERNEL}" >/dev/null 2>&1; then
    log_info "설치 전 점검: ${CURRENT_KERNEL} 용 사전 컴파일 .deb 를 써 apt 메타패키지 설치를 점검하지 않는다 — 통과"
    return 0
  fi
  detect_existing_driver_version
  if host_driver_adopted; then
    log_info "설치 전 점검: VERSION_SOURCE=Host 이고 호스트 드라이버 ${EXISTING_VER} 가 있어 설치기가 그 버전을 채택한다 — 통과"
    return 0
  fi
  major="${DRIVER_VERSION:-}"
  major="${major%%.*}"
  if [[ ! "${major}" =~ ^[0-9]+$ ]]; then
    log_info "설치 전 점검: DRIVER_VERSION(${DRIVER_VERSION:-unset})이 계열 번호로 시작하지 않아 점검하지 않는다 — 통과"
    return 0
  fi
  msg=""
  if ! apt_wait; then
    msg="설치 전 점검 판정 불가: apt/dpkg 잠금 대기 실패 — 설치 단계가 다시 시도한다"
  elif ! ns "apt-get update -y"; then
    msg="설치 전 점검 판정 불가: apt 목록 갱신 실패 — 설치 단계가 다시 시도한다"
  fi
  if [[ -n "${msg}" ]]; then
    log_warn "${msg}"; write_termination_message "${msg}"
    return 1
  fi
  # 점검은 PPA 추가 전이라 정책 버전이 저장소에 없는 것이 정상일 수 있다. 조회 함수는 아무것도 적지 않게(PIN_LOOKUP_QUIET) 하고
  # 아래 판정 불가 경고 한 줄이 로그와 종료 메시지를 겸한다. 설치 경로로 새지 않도록 local 로 둔다.
  local PIN_LOOKUP_QUIET=1
  if ! driver_pkg_args "nvidia-driver-${major}"; then
    msg="설치 전 점검 판정 불가: 정책 버전 ${DRIVER_VERSION} 을 저장소에서 찾지 못했다(PPA 추가 전 판정) — 설치 단계가 다시 확인한다"
    log_warn "${msg}"; write_termination_message "${msg}"
    return 1
  fi
  sim="$(mktemp)"
  if simulate_driver_install "${sim}"; then
    # 드라이버 세트가 통과해도 DKMS 빌드에 필요한 헤더·빌드 도구가 hold 에 막히면 설치 단계의 빌드가 실패한다.
    # 설치 경로의 빌드 도구 설치(apt_install --no-upgrade)와 같은 인자다 — 이미 설치된 도구는 올리지 않으므로
    # hold 된 옛 버전이어도 막지 않고, 설치되지 않은 패키지가 깔리는지만 본다.
    kpkgs="$(missing_kernel_pkgs "${CURRENT_KERNEL}")"
    if simulate_apt_install "${sim}" "--no-upgrade --no-install-recommends${kpkgs:+ ${kpkgs}} $(dkms_build_pkgs)"; then
      log_info "설치 전 점검 통과: ${DRIVER_PKG_LIST} + $(dkms_build_pkgs)"
      rm -f "${sim}"
      return 0
    fi
    if classify_hold_conflict "${sim}"; then
      write_termination_message "${HOLD_CONFLICT_MSG}"
      rm -f "${sim}"
      return "${EXIT_HOLD_CONFLICT}"
    fi
    # hold 와 무관한 실패(예: 이 커널의 헤더 패키지가 저장소에 없음)는 설치 경로도 경고만 하고 넘어가므로 통과다.
    # 한계: 헤더 패키지가 저장소에 없으면 apt 가 거기서 먼저 멈춰 같은 명령의 빌드 도구 hold 충돌은 가려지고 통과한다.
    log_warn "설치 전 점검: 헤더·빌드 도구 모의 설치가 hold 와 무관하게 실패 — $(grep -m1 '^E: ' "${sim}" || echo 'E: 사유 미상') — 설치 단계에서 다시 시도한다"
    rm -f "${sim}"
    return 0
  fi
  if classify_hold_conflict "${sim}"; then
    write_termination_message "${HOLD_CONFLICT_MSG}"
    rm -f "${sim}"
    return "${EXIT_HOLD_CONFLICT}"
  fi
  msg="설치 전 점검 판정 불가: apt 모의 설치 실패(hold 와 무관) — $(grep -m1 '^E: ' "${sim}" || echo 'E: 사유 미상') — 설치 단계가 다시 시도한다"
  log_warn "${msg}"; write_termination_message "${msg}"
  rm -f "${sim}"
  return 1
}

# 함수 정의만 필요할 때(단위 시험)는 여기서 멈춘다 — 아래 실행부(marker 이관·설치·트랩)를 건너뛴다.
if [[ "${ENTRYPOINT_LIB_ONLY:-0}" == "1" ]]; then
  return 0 2>/dev/null || exit 0
fi

# 마커 디렉토리 (passthrough 가드가 하위 MAIN FLOW 보다 먼저 참조하므로 상단에서 정의)
MARKER_DIR="${MARKER_DIR:-/var/lib/kcloud-operator}"

# 마커 디렉터리 개명 이관(npu-operator → kcloud-operator). 1회 수행되고 멱등이다.
# 아래 어떤 MARKER_DIR 사용보다 먼저 와야 한다.
# shellcheck source=/dev/null
. /usr/local/bin/marker-migrate.sh
migrate_marker_dir "${MARKER_DIR}"

# unattended-upgrades 가 nvidia 드라이버 패키지를 대역외로 바꾸지 못하게 막는 헬퍼.
# shellcheck source=/dev/null
. /usr/local/bin/nvidia-blacklist.sh

# 구운 사용자 공간 .deb 의 호스트 외부 의존을 설치 전에 확인하는 헬퍼.
# install_prebuilt_onto_host 가 userspace_missing_host_deps 를 부른다.
# shellcheck source=/dev/null
. /usr/local/bin/userspace-deps-check.sh

# ============================================================================ #
# 안전장치 동작 env (기본값 = 기존 동작 보존, 하위호환)
#   SKIP_ON_PASSTHROUGH (기본 true) : (a) GPU 전량 vfio-pci 바인딩 시 설치 보류
#   ALLOW_DOWNGRADE     (기본 false): (b) 하위 버전 다운그레이드 허용 여부
#   VERSION_SOURCE      (기본 Policy): (c) Host=호스트 설치 버전을 desired 로 채택
# ============================================================================ #
SKIP_ON_PASSTHROUGH="${SKIP_ON_PASSTHROUGH:-true}"
ALLOW_DOWNGRADE="${ALLOW_DOWNGRADE:-false}"
VERSION_SOURCE="${VERSION_SOURCE:-Policy}"

# RUN_MODE: 설치 완료 후 동작을 결정한다.
#   daemonset (기본) : 기존 동작 — 상주 healthcheck 루프로 무한 대기(회귀 0).
#   job              : 순간 install Job — 설치/skip 완료 후 exit 0(상주 안 함).
# 미지정 시 daemonset 으로 동작하여 기존 DS 이미지와 완전 하위호환.
RUN_MODE="${RUN_MODE:-daemonset}"

# MANAGE_CONTAINER_TOOLKIT: 이 드라이버 설치기가 컨테이너 툴킷 설치·
# containerd/CDI 런타임 설정·containerd/kubelet 재시작을 수행할지 여부.
#   기본 false — 런타임 설정 소유권은 상주 nvidia-container-toolkit DS 단독(이중 소유 금지).
#   드라이버 설치기는 커널 모듈 + userspace(host apt) 설치와 /dev 노드 생성까지만 책임진다.
#   true 로 설정하면 legacy 동작(설치기가 툴킷·런타임까지 구성) 복원 — toolkit DS 미상주
#   클러스터용 opt-in.
MANAGE_CONTAINER_TOOLKIT="${MANAGE_CONTAINER_TOOLKIT:-false}"

# ============================================================================ #
# 드라이버 선택 우선순위: 1) LOCAL_DRIVER_CHOICE(로컬 docker run 테스트용) 2) NVIDIA_DRIVER_CHOICE
#   (수동 env 지정) 3) DRIVER_VERSION(DriverInstallPolicy.spec.driver.version) 4) ubuntu-drivers
#   devices 기반 recommended 계열 + autoinstall.


choose_driver_candidates() {
  local candidates=()

  # 우선순위: 1) DRIVER_VERSION, 2) LOCAL_DRIVER_CHOICE, 3) NVIDIA_DRIVER_CHOICE
  local driver_version="${DRIVER_VERSION:-}"
  local local_choice="${LOCAL_DRIVER_CHOICE:-}"
  local policy_choice="${NVIDIA_DRIVER_CHOICE:-}"

  # 1) DRIVER_VERSION (Operator DriverInstallPolicy.spec.driver.version → env로 들어온 값)
  #    버전 번호(예: 590.48.01)이면 메이저 버전을 추출하여 패키지명으로 변환
  if [[ -n "${driver_version}" ]]; then
    log_info "DRIVER_VERSION specified (from policy): ${driver_version}"
    local major_ver="${driver_version%%.*}"
    if [[ "${major_ver}" =~ ^[0-9]+$ ]]; then
      # 숫자로 시작하면 버전 번호 → 패키지명 변환 (nvidia-driver-590)
      local pkg_name="nvidia-driver-${major_ver}"
      log_info "Resolved DRIVER_VERSION ${driver_version} → package: ${pkg_name}"
      candidates+=("${pkg_name}")
    else
      # 이미 패키지명 형태 (nvidia-driver-590 등)
      candidates+=("${driver_version}")
    fi
  fi

  # 2) LOCAL_DRIVER_CHOICE (로컬 docker run 테스트용 최우선 수동 오버라이드)
  if [[ -n "${local_choice}" ]]; then
    if [[ "${local_choice}" != "${driver_version}" ]]; then
      log_info "LOCAL_DRIVER_CHOICE specified: ${local_choice}"
      candidates+=("${local_choice}")
    else
      log_info "LOCAL_DRIVER_CHOICE '${local_choice}' == DRIVER_VERSION; skipping duplicate"
    fi
  fi

  # 3) NVIDIA_DRIVER_CHOICE (Operator에서 env로 직접 세팅하는 경우)
  if [[ -n "${policy_choice}" ]]; then
    if [[ "${policy_choice}" != "${driver_version}" && "${policy_choice}" != "${local_choice}" ]]; then
      log_info "NVIDIA_DRIVER_CHOICE (env) specified: ${policy_choice}"
      candidates+=("${policy_choice}")
    else
      log_info "NVIDIA_DRIVER_CHOICE '${policy_choice}' already in candidates; skipping duplicate"
    fi
  fi

  # 4) 위 세 가지 모두 없으면 ubuntu-drivers devices 기반 추천 사용
  if [[ ${#candidates[@]} -eq 0 ]]; then
    log_info "No explicit driver choice; using ubuntu-drivers devices recommended order"

    local rec ver server_pkg server_open_pkg
    rec="$(ns "ubuntu-drivers devices 2>/dev/null | awk '/recommended/ {print \$3}' | tail -n1" || true)"

    if [[ -z "${rec}" ]]; then
      log_warn "No recommended driver found by ubuntu-drivers; falling back to ubuntu-drivers autoinstall later"
    else
      log_info "ubuntu-drivers recommended: ${rec}"
      if [[ "${rec}" =~ ^nvidia-driver-([0-9]+) ]]; then
        ver="${BASH_REMATCH[1]}"
        server_pkg="nvidia-driver-${ver}-server"
        server_open_pkg="nvidia-driver-${ver}-server-open"

        # 요구 순서: <ver>-server -> <ver>-server-open -> <ver>(=rec)
        candidates+=("${server_pkg}" "${server_open_pkg}" "${rec}")
      else
        log_warn "Could not parse version from recommended (${rec}); using it as single candidate"
        candidates+=("${rec}")
      fi
    fi
  fi

  # 5) DRIVER_VERSION이 명시되지 않은 경우에만 ubuntu-drivers autoinstall fallback 추가
  #    명시된 경우 잘못된 버전이 설치되는 것을 방지
  if [[ -z "${driver_version}" ]]; then
    candidates+=("__UBUNTU_DRIVERS_AUTOINSTALL__")
  fi

  # 배열을 전역 변수로 넘김
  DRIVER_CANDIDATES=("${candidates[@]}")
}

# ============================================================================ #
# Driver health check
# ============================================================================ #
driver_healthy() {
  local expected_major="${1:-}"
  if ! ns "test -f /proc/driver/nvidia/version" 2>/dev/null; then
    log_warn "/proc/driver/nvidia/version not found"
    return 1
  fi
  if ! ns "nvidia-smi -L >/dev/null 2>&1"; then
    log_warn "nvidia-smi query failed"
    return 1
  fi
  # 기대 메이저 버전이 지정된 경우 로드된 버전과 비교
  if [[ -n "${expected_major}" ]]; then
    local loaded_ver
    loaded_ver=$(ns "cat /proc/driver/nvidia/version 2>/dev/null" | parse_nvrm_version || true)
    local loaded_major="${loaded_ver%%.*}"
    if [[ "${loaded_major}" != "${expected_major}" ]]; then
      log_warn "Version mismatch: loaded=${loaded_ver} (major ${loaded_major}), expected major=${expected_major}"
      return 1
    fi
    log_info "Version verified: loaded=${loaded_ver}, expected major=${expected_major}"
  fi
  return 0
}

# ============================================================================ #
# 안전장치 헬퍼: 버전 비교(다운그레이드 가드) · DKMS 로그 출력
# ============================================================================ #

# 버전 비교: $1 > $2 이면 0(true). dpkg 로 정상 비교되면 그 결과, 형식오류 등으로
# dpkg 가 판정 불가하면 major 정수 비교로 fallback.
ver_gt() {
  local a="$1" b="$2" am bm
  if ns "dpkg --compare-versions '${a}' gt '${b}'" >/dev/null 2>&1; then
    return 0
  elif ns "dpkg --compare-versions '${a}' le '${b}'" >/dev/null 2>&1; then
    return 1
  fi
  # dpkg 가 gt/le 모두 판정 실패 → 형식 문제로 간주, major 정수 비교 fallback
  am="${a%%.*}"; bm="${b%%.*}"
  if [[ "$am" =~ ^[0-9]+$ && "$bm" =~ ^[0-9]+$ ]] && [[ "$am" -gt "$bm" ]]; then
    return 0
  fi
  return 1
}

print_dkms_logs_if_any() {
  ns "bash -lc '
    shopt -s nullglob
    for d in /var/lib/dkms/nvidia*/[0-9]*/build; do
      if [ -f \"\$d/make.log\" ]; then
        echo
        echo \"========== DKMS make.log from \$d ==========\"
        tail -n 60 \"\$d/make.log\" || true
        echo \"==========================================\"
        echo
      fi
    done
  '" || true
}

# ============================================================================ #
# 하나의 driver 패키지 설치 + DKMS + 모듈 로드 + health 체크
# ============================================================================ #
install_and_activate_driver() {
  local pkg="$1"
  local expected_major="${2:-}"
  local prebuilt_used=0

  # 사전 컴파일: 실행 커널용 동봉 .deb 가 있으면 apt 드라이버 설치 전에 host 로 설치(모듈=prebuilt).
  # 동봉본이 있는데 설치 실패면 fail-closed 로 실패 반환 — DKMS 로 조용히 되돌아가지 않는다.
  # 동봉본이 없는 커널은 이 분기를 건너뛰고 기존 DKMS 경로를 그대로 탄다.
  if [[ "${pkg}" != "__UBUNTU_DRIVERS_AUTOINSTALL__" ]] && select_prebuilt_deb "${CURRENT_KERNEL}" >/dev/null 2>&1; then
    # 정합성 가드(fail-closed): 동봉 모듈 버전이 정책 DRIVER_VERSION 과 일치할 때만 진행한다.
    # 동봉 모듈과 사용자 공간의 일치는 빌드 시점 굽기 가드가 이미지 안에서 이미 보장하므로
    # 실행 중 아카이브를 조회하지 않는다(ADR-002). major 는 install_prebuilt_onto_host 에 넘긴다.
    local prebuilt_ver major
    prebuilt_ver="$(prebuilt_ko_version "${CURRENT_KERNEL}" || true)"
    major="${prebuilt_ver%%.*}"
    [[ -n "${major}" ]] || major="${DRIVER_VERSION%%.*}"
    if ! nvidia_prebuilt_coherent "${prebuilt_ver}" "${DRIVER_VERSION:-}"; then
      log_error "prebuilt 정합성 가드 실패 (${CURRENT_KERNEL}) — 설치 거부, DKMS 폴백 없음"
      return 1
    fi
    if install_prebuilt_onto_host "${CURRENT_KERNEL}" "${major}"; then
      prebuilt_used=1
    else
      log_error "prebuilt 설치 실패 (${CURRENT_KERNEL}) — DKMS 폴백 없이 실패"
      return 1
    fi
  fi

  if [[ "${pkg}" == "__UBUNTU_DRIVERS_AUTOINSTALL__" ]]; then
    log_info "Trying ubuntu-drivers autoinstall as fallback"
    apt_wait || { log_error "apt/dpkg 잠금 대기 실패(autoinstall)"; return 1; }
    ns "DEBIAN_FRONTEND=noninteractive ubuntu-drivers autoinstall || true"
  else
    if [[ "${prebuilt_used}" -eq 1 ]]; then
      log_info "사전 컴파일 사용 — 메타패키지 설치를 건너뛴다(DKMS 유입 방지)"
    else
      log_info "Installing driver package: ${pkg}"
      ns "rm -f /var/crash/nvidia-kernel-source-* /var/crash/*nvidia*.crash 2>/dev/null || true"
      # 드라이버 패키지는 --no-install-recommends 없이 설치 (커널 모듈 패키지 포함 필요)
      local pkg_rc=0
      install_driver_pkg_on_host "${pkg}" || pkg_rc=$?
      if [[ "${pkg_rc}" -eq "${EXIT_HOLD_CONFLICT}" ]]; then
        # hold 충돌 — 사유는 install_driver_pkg_on_host 가 HOLD_CONFLICT_MSG 에 남겼다.
        return "${EXIT_HOLD_CONFLICT}"
      fi
      if [[ "${pkg_rc}" -eq 2 ]]; then
        # 정책 버전 고정 관련 실패 — 사유는 install_driver_pkg_on_host 가 이미 남겼다.
        return 2
      fi
      if [[ "${pkg_rc}" -ne 0 ]]; then
        log_error "apt-get install ${pkg} failed"
        return 1
      fi
    fi
  fi

  # 설치된 드라이버 패키지를 manual로 마킹 (autoremove 방지)
  if [[ -n "${expected_major}" ]]; then
    log_info "Marking nvidia-driver-${expected_major} packages as manually installed..."
    ns "apt-mark manual nvidia-driver-${expected_major} nvidia-dkms-${expected_major} nvidia-kernel-source-${expected_major} 2>/dev/null || true"
  fi

  # stale 모듈이 남아있으면 제거 후 새 모듈 로드
  log_info "Removing any stale nvidia kernel modules before modprobe..."
  ns "rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia 2>/dev/null || true"

  # dpkg configure 완료 대기 (nvidia-dkms 패키지가 iU 상태일 수 있음)
  log_info "Ensuring all packages are fully configured (dpkg --configure -a)..."
  ns "dpkg --configure -a 2>/dev/null || true"

  # DKMS / depmod / modprobe — prebuilt 를 썼으면 DKMS 빌드 단계는 생략한다.
  if [[ ${prebuilt_used} -eq 1 ]]; then
    log_info "prebuilt module for ${CURRENT_KERNEL} installed (dkms skipped)"
  else
    log_info "Running 'dkms autoinstall -k ${CURRENT_KERNEL}'..."
    ns "dkms autoinstall -k ${CURRENT_KERNEL} || true"
  fi

  log_info "Running 'depmod -a ${CURRENT_KERNEL}'..."
  ns "depmod -a ${CURRENT_KERNEL} || true"

  log_info "Trying to load NVIDIA kernel modules..."
  ns "modprobe nvidia || true"
  ns "modprobe nvidia_uvm || true"
  ns "modprobe nvidia_drm || true"

  # 사전 컴파일을 썼으면 실제 로드된 모듈이 동봉본인지 확인한다(fail-closed). depmod 검색 순서상
  # DKMS 산출(updates/dkms)이 LRM(kernel/nvidia-<major>)을 가릴 수 있어, 로드된 버전이 동봉본과
  # 다르면 "가려짐" 으로 보고 실패한다 — major 만 보는 driver_healthy 로는 이 어긋남을 못 잡는다.
  if [[ ${prebuilt_used} -eq 1 ]]; then
    local loaded_ver bundled_ver
    bundled_ver="$(prebuilt_ko_version "${CURRENT_KERNEL}" || true)"
    loaded_ver="$(ns "cat /sys/module/nvidia/version 2>/dev/null" | head -1 || true)"
    if [[ "$(nvidia_upstream_ver "${loaded_ver}")" != "$(nvidia_upstream_ver "${bundled_ver}")" ]]; then
      log_error "prebuilt 검증 실패: 로드된 /sys/module/nvidia/version=${loaded_ver:-<없음>}, 동봉 모듈=${bundled_ver:-<없음>}, 정책 DRIVER_VERSION=${DRIVER_VERSION:-<unset>} — 사전 컴파일 모듈이 DKMS 산출에 가려짐"
      print_dkms_logs_if_any
      ns "rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia 2>/dev/null || true"
      return 1
    fi
    log_info "prebuilt 모듈 로드 확인: /sys/module/nvidia/version=${loaded_ver} (동봉=${bundled_ver})"
  fi

  if driver_healthy "${expected_major}"; then
    log_info "Driver '${pkg}' installed and GPU is healthy (major: ${expected_major:-any})"
    return 0
  fi

  log_error "Driver '${pkg}' installed but GPU not healthy or version mismatch"
  print_dkms_logs_if_any
  # 실패한 모듈 정리
  ns "rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia 2>/dev/null || true"
  return 1
}

# ============================================================================ #
# CRI detection (containerd / docker)
# ============================================================================ #
detect_cri() {
  local cri="${NVIDIA_CRI:-auto}"
  case "${cri}" in
    containerd|docker) echo "${cri}"; return 0 ;;
    auto) ;;
    *) echo "containerd"; return 0 ;;
  esac
  if ns "systemctl is-active --quiet containerd"; then echo "containerd"; return 0; fi
  if ns "systemctl is-active --quiet docker"; then echo "docker"; return 0; fi
  echo "containerd"
}

# ============================================================================ #
# 디바이스 노드 보장. 모듈만 로드되고 노드 생성 프로세스가 없으면 /dev/nvidia0·nvidiactl·
# nvidia-uvm 이 부재하여 toolkit CDI generate·GPU 파드가 데드락한다(idempotent-skip 경로에서
# 특히). 설치/skip 무관하게 nvidia-modprobe 로 노드를 생성하고, 부재 시 nvidia-smi 로
# fallback 한다. host namespace(nsenter)에서 host userspace 바이너리를 쓴다(host-install 전제와 정합).
ensure_device_nodes() {
  log_info "Ensuring /dev/nvidia* device nodes (nvidia-modprobe)"
  # -c0: control 0 + /dev/nvidia0, -u: nvidia-uvm 노드, -m: modeset 노드
  ns "nvidia-modprobe -c0 -u -m 2>/dev/null || true"
  # nvidia-smi 는 모든 GPU minor 에 대해 /dev/nvidiaN 을 생성한다(다중 GPU 커버).
  # nvidia-modprobe 부재/실패 시 fallback.
  ns "test -e /dev/nvidia0 || nvidia-smi >/dev/null 2>&1 || true"
  if ns "test -e /dev/nvidia0"; then
    log_info "device node /dev/nvidia0 present"
  else
    log_warn "device node /dev/nvidia0 여전히 부재 — GPU 파드가 실패할 수 있음(수동 확인 필요)"
  fi
}

# ============================================================================ #
# MAIN FLOW
# ============================================================================ #

log_info "Starting NVIDIA driver installation"

CURRENT_KERNEL=$(ns "uname -r")
log_info "CURRENT_KERNEL=${CURRENT_KERNEL}"

# --- PASSTHROUGH GUARD (a) ---
# 호스트의 모든 NVIDIA GPU 가 vfio-pci(passthrough)에 바인딩된 노드면 드라이버 설치를 보류한다.
# GPU 가 없는 비-GPU 노드는 가드 미적용(기존 동작 유지). installable GPU 가 1개라도 있으면 정상 진행.
if [[ "${SKIP_ON_PASSTHROUGH:-true}" != "false" ]] && ! nvidia_has_installable_gpu; then
  log_info "All NVIDIA GPUs bound to vfio-pci (passthrough) — skipping driver install"
  ns "mkdir -p ${MARKER_DIR}" || true
  ns "touch ${MARKER_DIR}/passthrough-skip" || true
  SKIP_DRIVER_INSTALL=1
  # 설치/toolkit/modprobe 를 모두 건너뛰고 Pod 는 Running 유지 (idle 루프)
  ns "touch ${MARKER_DIR}/driver.ready" || true
  touch /tmp/driver-ready
  if [[ "${RUN_MODE}" == "job" ]]; then
    log_info "RUN_MODE=job + passthrough skip — 설치 대상 없음, exit 0"
    exit 0
  fi
  log_info "passthrough 노드 — idle 루프 진입 (드라이버 설치/toolkit 미수행, modprobe 없음)"
  while true; do sleep "${HEALTH_CHECK_INTERVAL:-30}"; done
fi

# --- PRE-INSTALL CLEANUP ---
log_info "Pre-install cleanup: dpkg state, locks, crash reports"
ns "bash -lc '
  dpkg --configure -a 2>/dev/null || true
  rm -f /var/lib/dpkg/lock* /var/cache/apt/archives/lock 2>/dev/null || true
  rm -f /var/crash/nvidia-kernel-source-*.crash /var/crash/*nvidia*.crash 2>/dev/null || true
'"

# --- BASE TOOLS ---
log_info "Apt update + base tools"
apt_update
apt_install --no-upgrade ubuntu-drivers-common jq curl gnupg ca-certificates software-properties-common build-essential dkms || true

log_info "Ensuring NVIDIA PPA"
ns "add-apt-repository -y ppa:graphics-drivers/ppa >/dev/null 2>&1 || true"
apt_update

# --- KERNEL HEADERS & MODULES ---
log_info "Checking kernel headers and modules for ${CURRENT_KERNEL}"

if select_prebuilt_deb "${CURRENT_KERNEL}" >/dev/null 2>&1; then
  # 이 커널용 사전 컴파일 .deb 가 동봉돼 있으면 커널 헤더를 설치하지 않는다. 헤더가 없는 호스트는
  # 뒤이은 apt userspace 설치가 끌어오는 nvidia-dkms postinst 가 빌드를 못 하므로, DKMS 산출물이
  # 동봉 모듈을 가리는 일이 원천 차단된다. 헤더가 이미 있는 호스트는 postinst 가 여전히 빌드할 수
  # 있고, 그 경우 install_and_activate_driver 의 로드 후 검증이 버전 불일치를 잡아 실패시킨다.
  log_info "prebuilt .deb 동봉(${CURRENT_KERNEL}) — 커널 헤더 설치 생략(DKMS 빌드 차단)"
elif kernel_headers_present "${CURRENT_KERNEL}"; then
  log_info "Found kernel headers: /usr/src/linux-headers-${CURRENT_KERNEL}"
else
  log_info "linux-headers-${CURRENT_KERNEL} not found; trying to install..."
  if ! apt_install "linux-headers-${CURRENT_KERNEL}"; then
    log_warn "Failed to install linux-headers-${CURRENT_KERNEL}; trying linux-headers-generic"
    apt_install linux-headers-generic || log_warn "linux-headers-generic install failed as well"
  fi
fi

if kernel_modules_present "${CURRENT_KERNEL}"; then
  log_info "Found /lib/modules/${CURRENT_KERNEL}"
else
  log_warn "/lib/modules/${CURRENT_KERNEL} not found; trying linux-modules-extra-${CURRENT_KERNEL}"
  apt_install "linux-modules-extra-${CURRENT_KERNEL}" || log_warn "linux-modules-extra-${CURRENT_KERNEL} install failed"
fi

log_info "Holding kernel packages to prevent unintended upgrades"
ns "apt-mark hold linux-image-generic linux-headers-generic || true"
ns "apt-mark hold linux-image-${CURRENT_KERNEL} || true"
ns "apt-mark hold linux-headers-${CURRENT_KERNEL} || true"
ns "apt-mark hold linux-modules-extra-${CURRENT_KERNEL} || true"

# 빠진 빌드 도구만 설치한다(--no-upgrade). 이미 설치된 지명 도구는 hold 여부와 무관하게 올리지 않는다. 설치 전 점검
# (run_preflight)이 같은 인자로 모의 설치한다. 목록은 점검과 공유하며 공백으로 나뉘어 와야 하므로 단어 분리가 의도다.
# shellcheck disable=SC2046
apt_install --no-upgrade $(dkms_build_pkgs) || true

# --- EXISTING DRIVER CHECK ---
# 감지는 설치 전 점검과 같은 함수를 쓴다. 출처 loaded 는 로드된 모듈, package 는 driver-manager 가 모듈을
# 해제한 뒤에도 남은 설치 패키지다. 모듈 파일은 있으나 버전을 읽지 못하고 패키지 폴백도 찾지 못하면 감지 로그가 나오지 않으므로 경고 한 줄을 남긴다
# (감지 함수는 stdout 에 쓰지 않아야 해서 호출부인 여기서 남긴다).
detect_existing_driver_version
[[ "${EXISTING_VER_UNPARSED}" != "1" ]] \
  || log_warn "/proc/driver/nvidia/version 은 있으나 로드된 모듈 버전을 읽지 못했다 — 설치된 패키지 버전을 확인한다"
[[ -z "${EXISTING_VER_SOURCE}" ]] || log_info "Existing NVIDIA driver detected (${EXISTING_VER_SOURCE}): ${EXISTING_VER}"

# unattended-upgrades blacklist 는 설치 여부와 무관하게 매 실행 적용한다(멱등) — operator 가
# 드라이버 버전을 소유하므로 idle(설치 skip) 노드에서도 호스트가 대역외로 드라이버를 못 바꾸게 한다.
write_unattended_blacklist && log_info "unattended-upgrades nvidia blacklist 적용(무조건)" \
  || log_warn "unattended-upgrades blacklist 적용 실패(치명 아님)"

# --- HOST VERSION RESPECT (c) ---
# VERSION_SOURCE=Host 이고 호스트에 드라이버가 설치돼 있으면 그 버전을 effective desired 로
# 채택하고 설치를 보류(존중)한다. 호스트에 드라이버가 없으면 policy DRIVER_VERSION 으로 fallback.
if [[ "${VERSION_SOURCE:-Policy}" == "Host" ]]; then
  if host_driver_adopted; then
    log_info "VERSION_SOURCE=Host, adopting existing host driver ${EXISTING_VER} as desired (skip install)"
    DRIVER_VERSION="${EXISTING_VER}"
    SKIP_DRIVER_INSTALL=1
  else
    log_info "VERSION_SOURCE=Host but no host driver detected — falling back to policy DRIVER_VERSION=${DRIVER_VERSION:-<unset>}"
  fi
fi

# If requested version matches existing, skip driver install
if [[ -n "${DRIVER_VERSION:-}" && -n "${EXISTING_VER}" && "${DRIVER_VERSION}" == "${EXISTING_VER}" ]]; then
  if driver_healthy; then
    log_info "Requested driver version ${DRIVER_VERSION} already installed and healthy, skipping driver install"
    # Jump to toolkit section
    SKIP_DRIVER_INSTALL=1
  fi
fi

# If different version exists, purge first
# 동일 버전이지만 unhealthy한 경우 purge하지 않고 repair (dpkg --configure + dkms)
DESIRED_MAJOR="${DRIVER_VERSION%%.*}"
EXISTING_MAJOR="${EXISTING_VER%%.*}"

# --- DOWNGRADE GUARD (b) ---
# 기존 설치 버전이 desired 보다 최신이면(major 동일/상이 무관) 다운그레이드를 방지하고 기존을 유지한다.
# ALLOW_DOWNGRADE=true 일 때만 하위 버전 설치(purge→install)를 허용.
if [[ -n "${EXISTING_VER}" && "${SKIP_DRIVER_INSTALL:-0}" != "1" && -n "${DRIVER_VERSION:-}" ]]; then
  if ver_gt "${EXISTING_VER}" "${DRIVER_VERSION}" && [[ "${ALLOW_DOWNGRADE:-false}" != "true" ]]; then
    log_warn "Existing driver ${EXISTING_VER} > desired ${DRIVER_VERSION}; downgrade disabled (ALLOW_DOWNGRADE=false) → keep existing, skip install"
    SKIP_DRIVER_INSTALL=1
  fi
fi

if [[ -n "${EXISTING_VER}" && "${SKIP_DRIVER_INSTALL:-0}" != "1" && "${EXISTING_MAJOR}" != "${DESIRED_MAJOR}" ]]; then
  # 기존 메이저 버전 추출
  OLD_MAJOR="${EXISTING_VER%%.*}"
  log_info "Purging existing NVIDIA driver ${EXISTING_VER} (major: ${OLD_MAJOR})"

  # driver-manager initContainer가 이미 모듈을 해제했는지 확인
  if ns "lsmod | grep -q '^nvidia '"; then
    log_info "nvidia 모듈이 아직 로드됨 — rmmod 후 purge"
    ns "bash -lc '
      systemctl stop nvidia-persistenced 2>/dev/null || true
      rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia 2>/dev/null || true
    '"
  else
    log_info "nvidia 모듈 이미 해제됨 (driver-manager initContainer)"
  fi

  # 기존 버전의 모든 nvidia 패키지 제거 (libnvidia-* 포함)
  log_info "Purging all nvidia packages with major version ${OLD_MAJOR}"
  ns "bash -lc '
    DEBIAN_FRONTEND=noninteractive apt-get purge -y \$(dpkg-query -W -f=\"\\\${Package}\\n\" 2>/dev/null | grep -i nvidia | grep \"${OLD_MAJOR}\") 2>/dev/null || true
    DEBIAN_FRONTEND=noninteractive apt-get purge -y \$(dpkg-query -W -f=\"\\\${Package}\\n\" 2>/dev/null | grep -i \"linux-modules-nvidia.*${OLD_MAJOR}\") 2>/dev/null || true
    dkms remove nvidia/${OLD_MAJOR} --all 2>/dev/null || true
    depmod -a 2>/dev/null || true
    rm -f /var/lib/kcloud-operator/nvidia.ok 2>/dev/null || true
  '"

  # 잔여 패키지 확인
  REMAINING=$(ns "dpkg-query -W -f='\${Status} \${Package}\n' 2>/dev/null | grep '^install' | grep -i nvidia | grep '${OLD_MAJOR}' | wc -l" || echo "0")
  if [[ "${REMAINING}" -gt 0 ]]; then
    log_warn "Remaining ${OLD_MAJOR} packages after purge: ${REMAINING}"
    ns "dpkg-query -W -f='\${Status} \${Package}\n' 2>/dev/null | grep '^install' | grep -i nvidia | grep '${OLD_MAJOR}'" || true
  fi

  apt_update
fi

# --- DRIVER SELECTION & INSTALL ---
if [[ "${SKIP_DRIVER_INSTALL:-0}" != "1" ]]; then
  choose_driver_candidates
  log_info "Driver candidate order: ${DRIVER_CANDIDATES[*]}"

  # DRIVER_VERSION에서 기대 메이저 버전 추출 (버전 검증용)
  EXPECTED_MAJOR=""
  if [[ -n "${DRIVER_VERSION:-}" ]]; then
    EXPECTED_MAJOR="${DRIVER_VERSION%%.*}"
  fi

  install_from_candidates

  if [[ "${INSTALL_SUCCESS}" -ne 1 ]]; then
    # 모듈 교체 지연(cross-major·같은 계열 patch)은 install 실패가 아니다. 구 모듈이 busy(GPU 홀더/refcount)면
    # rmmod·신 모듈 modprobe 가 실패해 모든 candidate 가 실패로 보이지만, 타겟 패키지는 이미 설치됐고 신 모듈은
    # 재부팅 후 로드된다. install 실패로 처리하면 needs-reboot 마커가 안 남아 operator 의 RebootRequired→reboot
    # 경로가 발화하지 못한다. TARGET_PKG_OK(타겟 설치 완료)·DISK_MOD_MAJOR/VER(디스크 모듈이 신 버전으로 빌드됨)·
    # ACTIVE_MAJOR/ACTIVE_NVRM(현재 로드 버전)·PIN_FAILED(고정 실패·hold 충돌이면 가드 skip) 네 신호가 모두 맞으면
    # 마커를 남기고 정상 종료해 재부팅에 위임한다. hold 충돌은 PIN_FAILED 로 가드를 건너뛰고 아래에서 종료 코드 3 으로 끝낸다.
    collect_reboot_guard_state
    ACTIVE_NVRM=$(ns "cat /proc/driver/nvidia/version 2>/dev/null" | parse_nvrm_version || echo "")
    ACTIVE_MAJOR="${ACTIVE_NVRM%%.*}"
    if apply_reboot_defer; then
      if [ "${RUN_MODE}" = "job" ]; then
        log_info "RUN_MODE=job — reboot-deferred(모듈 교체), install Job 정상 종료(exit 0)"
        exit 0
      fi
      exec sleep infinity
    fi
    # ------------------------------------------------------------------------------------------
    exit_install_failed
  fi
fi

# ============================================================================ #
# 디바이스 노드 보장 — 설치/skip 무관, toolkit/runtime 설정 이전에 수행
# ============================================================================ #
ensure_device_nodes

# ============================================================================ #
# NVIDIA CONTAINER TOOLKIT + RUNTIME 설정 (기본 비활성)
# 런타임(containerd/CDI) 설정 소유권은 상주 nvidia-container-toolkit DS 단독(이중 소유 금지).
# MANAGE_CONTAINER_TOOLKIT=true 일 때만 legacy 경로(설치기가 툴킷·런타임까지 구성)를 수행한다.
# ============================================================================ #
if [[ "${MANAGE_CONTAINER_TOOLKIT}" == "true" ]]; then
log_info "MANAGE_CONTAINER_TOOLKIT=true — 컨테이너 툴킷 설치 + 런타임 설정 수행(legacy 경로)"
log_info "Installing NVIDIA Container Toolkit"

apt_wait
ns "curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
    | gpg --dearmor --batch --yes -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg"
ns "curl -sSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
    > /etc/apt/sources.list.d/nvidia-container-toolkit.list"
ns "sed -i -e '/experimental/ s/^#//g' /etc/apt/sources.list.d/nvidia-container-toolkit.list"
apt_update

CTK_VER="${NVIDIA_CONTAINER_TOOLKIT_VERSION:-}"
if [[ -n "${CTK_VER}" ]]; then
  log_info "Installing pinned CTK version: ${CTK_VER}"
  apt_wait
  ns "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends --allow-downgrades \
      nvidia-container-toolkit=${CTK_VER} nvidia-container-toolkit-base=${CTK_VER}"
else
  log_info "Installing latest CTK"
  apt_install nvidia-container-toolkit nvidia-container-toolkit-base
fi

# ============================================================================ #
# RUNTIME CONFIGURE & CDI
# ============================================================================ #
RUNTIME="$(detect_cri)"
log_info "Configuring nvidia-ctk for runtime: ${RUNTIME}"

case "${RUNTIME}" in
  containerd)
    ns "nvidia-ctk runtime configure --runtime=containerd --set-as-default || true"
    # config_path 와 mirrors/configs 공존 시 containerd 1.7+ 에서 CRI plugin 로드 실패.
    # nvidia-ctk 가 남긴 registry.mirrors/configs 블록을 제거하여 재발 방지.
    # nvidia-ctk v1.19+ → conf.d drop-in, v1.17 이하 → main config 대상 — 둘 다 처리.
    for cfg in /etc/containerd/config.toml /etc/containerd/conf.d/99-nvidia.toml; do
      ns "[[ -f '${cfg}' ]]" || continue
      ns "grep -q 'config_path' '${cfg}'" || continue
      ns "python3 -c \"
import re
p='${cfg}'
c=open(p).read()
c=re.sub(r'\\n\\s*\\[plugins\\.\\\"io\\.containerd\\.grpc\\.v1\\.cri\\\"\\.registry\\.(mirrors|configs)[^\\n]*\\][\\s\\S]*?(?=\\n\\s*\\[|\\Z)','',c)
open(p,'w').write(c)\""
      log_info "Stripped registry.mirrors/configs from ${cfg} (config_path present)"
    done
    # nvidia-ctk 가 runc 의 binaryName 을 복사해 두 키가 공존하는 현상 정리.
    # lowercase binaryName 제거, 대문자 BinaryName 유지.
    for cfg in /etc/containerd/config.toml /etc/containerd/conf.d/99-nvidia.toml; do
      ns "[[ -f '${cfg}' ]]" || continue
      ns "python3 -c \"
import re
p='${cfg}'
c=open(p).read()
c=re.sub(r'(\\[plugins\\.\\\"io\\.containerd\\.grpc\\.v1\\.cri\\\"\\.containerd\\.runtimes\\.nvidia\\.options\\]\\n(?:.*\\n)*?)\\s*binaryName\\s*=\\s*\\\"[^\\\"]*\\\"\\n',r'\\1',c)
open(p,'w').write(c)\""
      log_info "Removed stale binaryName from ${cfg} (kept BinaryName)"
    done
    ns 'mkdir -p /etc/containerd/config.d'
    ns "cat > /etc/containerd/config.d/99-nvidia.toml <<EOF
[plugins.\"io.containerd.grpc.v1.cri\".containerd]
  default_runtime_name = \"nvidia\"
EOF"
    ;;
  docker)
    ns "nvidia-ctk runtime configure --runtime=docker --set-as-default || true"
    ;;
esac

# Verify containerd can see NVIDIA runtime
if [[ "${RUNTIME}" == "containerd" ]]; then
  log_info "Verifying containerd NVIDIA runtime configuration..."
  sleep 3
  if ns "crictl info 2>/dev/null | grep -q nvidia" 2>/dev/null; then
    log_info "containerd NVIDIA runtime verified"
  else
    log_warn "containerd NVIDIA runtime not yet visible in crictl info; restarting containerd"
    ns "systemctl restart containerd || true"
    sleep 5
    if ns "crictl info 2>/dev/null | grep -q nvidia" 2>/dev/null; then
      log_info "containerd NVIDIA runtime verified after restart"
    else
      log_warn "containerd NVIDIA runtime still not visible; kubelet restart may be needed"
    fi
  fi
fi

log_info "Generating CDI spec..."
ns 'mkdir -p /etc/cdi /var/run/cdi'
ns 'nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml || true'
ns 'test -s /var/run/cdi/nvidia.yaml || cp -f /etc/cdi/nvidia.yaml /var/run/cdi/nvidia.yaml || true'
ns 'test -s /etc/cdi/nvidia.yaml || cp -f /var/run/cdi/nvidia.yaml /etc/cdi/nvidia.yaml || true'
ns 'ln -sf /etc/cdi/nvidia.yaml /var/run/cdi/nvidia.yaml || true'
else
  log_info "MANAGE_CONTAINER_TOOLKIT=false — 툴킷 설치/런타임(containerd/CDI) 설정 skip (toolkit DS 소유)"
fi

# ============================================================================ #
# MARKERS & DIAGNOSTICS
# ============================================================================ #
MARKER_DIR="/var/lib/kcloud-operator"
MARKER_NOUVEAU="${MARKER_DIR}/nvidia.nouveau.blacklisted"
MARKER_RUNTIME="${MARKER_DIR}/nvidia.runtime.configured"
ns "mkdir -p ${MARKER_DIR}"

if ! ns "test -f ${MARKER_NOUVEAU}"; then
  log_info "Disabling nouveau (one-time)..."
  ns "bash -lc '
    cat > /etc/modprobe.d/blacklist-nouveau.conf <<EOF
blacklist nouveau
options nouveau modeset=0
EOF
    update-initramfs -u || true
    touch ${MARKER_NOUVEAU}
  '"
else
  log_info "nouveau already blacklisted"
fi

# containerd/kubelet 재시작 (기본 비활성).
# 런타임 lifecycle(재시작 포함)은 상주 toolkit DS 소유. 설치기가 재시작하면 이중 소유 +
# cordoned 노드 hang(③) 위험. MANAGE_CONTAINER_TOOLKIT=true 일 때만 수행.
if [[ "${MANAGE_CONTAINER_TOOLKIT}" == "true" ]]; then
  if ! ns "test -f ${MARKER_RUNTIME}"; then
    log_info "Restarting runtime and kubelet (one-time)..."
    case "${RUNTIME}" in
      containerd) ns "systemctl restart containerd || true" ;;
      docker)     ns "systemctl restart docker || true" ;;
    esac
    ns "systemctl restart kubelet || true"
    ns "touch ${MARKER_RUNTIME}"
  else
    log_info "Runtime already configured"
  fi
else
  log_info "MANAGE_CONTAINER_TOOLKIT=false — containerd/kubelet 재시작 skip (toolkit DS 소유)"
fi

ns "nvidia-smi -a > ${MARKER_DIR}/nvidia.smi 2>/dev/null || true"
ns "modinfo nvidia > ${MARKER_DIR}/nvidia.modinfo 2>/dev/null || true"
INSTALLED_VER=$(ns "cat /proc/driver/nvidia/version 2>/dev/null" | parse_nvrm_version || echo "unknown")
ns "echo ${INSTALLED_VER} > ${MARKER_DIR}/nvidia.ok"

log_info "NVIDIA driver installation completed successfully (runtime 설정은 toolkit DS 소유)."

# --- 재부팅 마커 (job/daemonset 공통) ---------------------------------------
# 설치는 성공했으나 구 모듈이 물려 있으면(로드 버전 != 설치 타겟) 새 모듈은 재부팅 뒤에만 로드된다.
# detector 가 읽는 needs-reboot 마커를 남긴다. 같은 계열 patch 교체도 대상이다.
if loaded_needs_reboot "${INSTALLED_VER}" "${DRIVER_VERSION:-}" "${SKIP_DRIVER_INSTALL:-0}"; then
  case "${REBOOT_STRATEGY:-IfNeeded}" in
    Never)
      log_warn "모듈 교체 대기(loaded ${INSTALLED_VER} != target ${DRIVER_VERSION}) — rebootStrategy=Never, 마커 생략" ;;
    *)
      log_warn "모듈 교체 대기(loaded ${INSTALLED_VER} != target ${DRIVER_VERSION}) — needs-reboot 마커 생성(재부팅 후 신 모듈 로드)"
      ns "mkdir -p ${MARKER_DIR}; touch ${MARKER_DIR}/needs-reboot" || true ;;
  esac
else
  # 로드 버전이 타겟과 같거나 설치를 건너뛰었다 → 남은 needs-reboot 마커 제거(재부팅 후 재실행 시 수렴).
  # ponytail: 재부팅 후 수렴은 operator 가 handleRebooting→install 재실행(entrypoint 재진입)으로
  #   마커를 지우는 흐름 전제. E2E(동결 해제 후)에서 최종 확정.
  ns "rm -f ${MARKER_DIR}/needs-reboot 2>/dev/null" || true
fi
# -----------------------------------------------------------------------------

# ============================================================================ #
# DAEMONSET 모드: Ready 마커 생성 + 상시 모니터링 루프
# startupProbe는 /tmp/driver-ready 파일 존재 여부를 확인한다.
# livenessProbe는 healthcheck.sh를 주기적으로 실행한다.
# ============================================================================ #
log_info "Ready 마커 생성 중..."
ns "touch ${MARKER_DIR}/driver.ready"
touch /tmp/driver-ready

# RUN_MODE=job: 설치 완료 → healthcheck self-check 1회 후 exit 0 (상주 루프 없음).
if [[ "${RUN_MODE}" == "job" ]]; then
  log_info "RUN_MODE=job — 설치 완료, healthcheck self-check 후 exit 0"
  if ! /usr/local/bin/healthcheck.sh; then
    log_error "RUN_MODE=job self-check 실패 — Job 실패(exit 1)"
    exit 1
  fi
  log_info "RUN_MODE=job self-check 통과 — install Job 정상 종료(exit 0)"
  exit 0
fi

log_info "DaemonSet 모드: startupProbe 통과 — 상시 모니터링 루프 시작 (interval=${HEALTH_CHECK_INTERVAL:-30}s)"
while true; do
  sleep "${HEALTH_CHECK_INTERVAL:-30}"
  if ! /usr/local/bin/healthcheck.sh; then
    log_error "드라이버 상태 이상 감지 — livenessProbe 재시작 트리거를 위해 종료"
    exit 1
  fi
done

