#!/usr/bin/env bash
# ============================================================
# nvidia-blacklist_test.sh: unattended-upgrades nvidia blacklist 적용 시험
# 상세: write_unattended_blacklist 가 8개 nvidia 패턴을 담은 드롭인을 멱등하게 쓰는지
#       검증한다. unattended-upgrades+graphics-drivers PPA 가 재부팅마다 드라이버를
#       대역외로 되돌리는 경로의 회귀를 막는다.
# 생성일: 2026-09-17
# ============================================================
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAILED=0

# ns 는 원래 nsenter 로 호스트에서 도는 래퍼다. 시험에서는 로컬에서 그대로 실행해
# stdin(heredoc)이 로컬 cat 으로 흘러가게 한다.
ns() { bash -c "$1"; }

check() {
  local label="$1" want="$2" got="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "  [FAIL] ${label}: got '${got}', want '${want}'"
    FAILED=1
  else
    echo "  [ok]   ${label}"
  fi
}

LIB="${HERE}/nvidia-blacklist.sh"
if [[ ! -f "${LIB}" ]]; then
  echo "  [FAIL] ${LIB} 이 없다"
  echo "FAIL"
  exit 1
fi
# shellcheck source=/dev/null
source "${LIB}"

if ! declare -F write_unattended_blacklist >/dev/null; then
  echo "  [FAIL] write_unattended_blacklist 가 정의되지 않았다"
  echo "FAIL"
  exit 1
fi

TMPFILE="$(mktemp)"
trap 'rm -f "${TMPFILE}"' EXIT
export NVIDIA_BLACKLIST_PATH="${TMPFILE}"

write_unattended_blacklist
check "쓰기 성공(exit 0)" 0 "$?"
check "파일 생성됨" yes "$([[ -s "${TMPFILE}" ]] && echo yes || echo no)"

for pat in nvidia-driver- nvidia-dkms- nvidia-kernel- nvidia-compute-utils- \
           nvidia-utils- nvidia-firmware- libnvidia- xserver-xorg-video-nvidia; do
  check "패턴 포함: ${pat}" yes \
    "$(grep -q -- "\"${pat}" "${TMPFILE}" && echo yes || echo no)"
done

check "Unattended-Upgrade::Package-Blacklist 형식" yes \
  "$(grep -q '^Unattended-Upgrade::Package-Blacklist:: "' "${TMPFILE}" && echo yes || echo no)"

# apt-config 로 실제 파싱 가능한지(문법 오류 없이 로드되는지) 확인.
if command -v apt-config >/dev/null 2>&1; then
  check "apt-config 로 파싱 가능" 0 \
    "$(apt-config -c="${TMPFILE}" dump >/dev/null 2>&1; echo $?)"
fi

BEFORE="$(cat "${TMPFILE}")"
write_unattended_blacklist
AFTER="$(cat "${TMPFILE}")"
check "멱등(두 번 써도 내용 동일)" yes "$([[ "${BEFORE}" == "${AFTER}" ]] && echo yes || echo no)"
check "멱등(중복 누적 없음)" 1 "$(grep -c -- '"nvidia-driver-' "${TMPFILE}")"

if [[ "${FAILED}" -ne 0 ]]; then
  echo "FAIL"
  exit 1
fi
echo "PASS"
